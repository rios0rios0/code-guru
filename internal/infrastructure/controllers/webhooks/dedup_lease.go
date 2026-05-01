// Package webhooks — see `dedup_cache.go` for the in-memory backend
// and the WebhookDedup contract. This file holds the cross-pod
// distributed-lock backend backed by Kubernetes `coordination.k8s.io/v1`
// `Lease` objects.
//
// # Why a Kubernetes Lease
//
// The bot runs on AKS with `replicas: 2`. Azure DevOps fires both
// `git.pullrequest.created` AND `git.pullrequest.updated` for every
// new PR; the K8s `Service` round-robins one delivery to each pod so
// each pod thinks it is the first to see the PR, both run the AI,
// both post comments — observed live across `internal-terraform/internal-customer-app#NNNN`,
// `internal-terraform/pipelines#NNNN`, and `internal-terraform/internal-customer-app#NNNN`
// on `2026-05-01`. The in-memory cache from PR `#100` cannot dedup
// across pods (each pod sees only its own deliveries).
//
// Operator-rejected alternatives:
//   - `replicas: 1` — gives up availability during deploys / restarts.
//   - drop one ADO subscription — loses coverage for the events the
//     dropped subscription was the only carrier for.
//
// `Lease` is the same primitive client-go's leader-election library
// uses, and the K8s API server already provides optimistic concurrency
// for it via the `Create` verb: exactly one `Create` for a given
// `(namespace, name)` succeeds; every concurrent `Create` returns
// `AlreadyExists`. That is precisely the semantics we need for
// "exactly one pod processes this PR". The lease's
// `spec.leaseDurationSeconds` provides the self-healing TTL — if the
// owning pod crashes mid-review the lease ages out and a future event
// can re-acquire.
//
// # Required RBAC
//
// The bot's ServiceAccount must be granted the following Role +
// RoleBinding in the namespace it runs in. This is intentionally NOT
// shipped in the application repo — it is operator-side configuration
// applied via the deployment manifests / Terraform module.
//
//	apiVersion: rbac.authorization.k8s.io/v1
//	kind: Role
//	metadata:
//	  namespace: code-guru
//	  name: code-guru-webhook-dedup
//	rules:
//	  - apiGroups: ["coordination.k8s.io"]
//	    resources: ["leases"]
//	    verbs: ["get", "list", "create", "delete", "update", "patch"]
//	---
//	apiVersion: rbac.authorization.k8s.io/v1
//	kind: RoleBinding
//	metadata:
//	  namespace: code-guru
//	  name: code-guru-webhook-dedup
//	subjects:
//	  - kind: ServiceAccount
//	    name: code-guru
//	    namespace: code-guru
//	roleRef:
//	  apiGroup: rbac.authorization.k8s.io
//	  kind: Role
//	  name: code-guru-webhook-dedup
//	  apiGroup: rbac.authorization.k8s.io
//
// `update` and `patch` are listed for forward compatibility with a
// future "renew the lease while a long review is in progress" path;
// today the implementation only uses `create` and `delete`.
package webhooks

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	logger "github.com/sirupsen/logrus"
	coordinationv1 "k8s.io/api/coordination/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

// leaseAPITimeout caps every call to the K8s API server. The webhook
// hot path MUST NOT stall on a wedged control plane; on timeout the
// dedup degrades to "process the webhook" — never worse than today's
// no-dedup baseline.
const leaseAPITimeout = 5 * time.Second

// leaseDurationSeconds is the TTL applied to every dedup lease.
// 5 minutes is comfortably above the longest review the bot has been
// observed to run (≈8 minutes is the outlier; the typical p95 is
// ≈90 seconds). On a clean review the lease is `Delete`d explicitly
// AFTER `submitter.Submit` succeeds AND the worker finishes — so the
// TTL is only the safety net for a pod crash mid-review. A new push
// minutes later still acquires immediately because the owning pod
// has already deleted the lease.
const leaseDurationSeconds int32 = 300

// k8sNameMaxLen mirrors RFC 1123 subdomain limits enforced by the K8s
// API server. Lease names that exceed this are rejected with
// `Invalid` — defending in the client keeps the failure mode in our
// own log instead of producing an opaque API-server error.
const k8sNameMaxLen = 253

// LeaseClient is the narrow subset of
// `k8s.io/client-go/kubernetes/typed/coordination/v1.LeaseInterface`
// the dedup backend actually uses. Defining our own narrow port keeps
// the test stub tractable (~30 lines instead of implementing the full
// 9-method generated client) and follows the project rule "the bigger
// the interface, the weaker the abstraction".
//
// The signatures match the upstream `LeaseInterface` exactly so the
// real client satisfies this interface via Go's structural typing
// without a wrapper.
type LeaseClient interface {
	Create(ctx context.Context, lease *coordinationv1.Lease, opts metav1.CreateOptions) (*coordinationv1.Lease, error)
	Delete(ctx context.Context, name string, opts metav1.DeleteOptions) error
}

// K8sLeaseDedup is the cross-pod WebhookDedup backend. It treats a
// successful `Create` of a `Lease` as the dedup acquisition (this pod
// owns the review), and a `Delete` as the explicit release that lets
// a real follow-up push re-acquire immediately.
type K8sLeaseDedup struct {
	client LeaseClient
	// holderIdentity is recorded on every lease so logs / `kubectl get
	// leases` show which pod owns a given review. The K8s downward API
	// usually populates `HOSTNAME` with the pod name; tests inject a
	// fixed value for determinism.
	holderIdentity string
}

// NewK8sLeaseDedup wires the lease-backed dedup. `client` is typically
// `kubernetes.NewForConfig(cfg).CoordinationV1().Leases(namespace)`;
// tests pass a hand-rolled fake.
func NewK8sLeaseDedup(client LeaseClient, holderIdentity string) *K8sLeaseDedup {
	return &K8sLeaseDedup{client: client, holderIdentity: holderIdentity}
}

// NewK8sLeaseDedupFromInCluster wires the lease-backed dedup using
// the in-cluster Kubernetes config (the ServiceAccount token + CA
// mounted into every pod). Returns `ErrLeaseClientNotConfigured`
// when the process is not running inside a pod, so the caller can
// fall back to the in-memory backend without surfacing the error to
// the operator (the fallback is a valid local-dev workflow).
//
// `namespace` is the namespace the leases will be created in. When
// empty, the function reads the standard
// `/var/run/secrets/kubernetes.io/serviceaccount/namespace` file —
// the same default `client-go` uses for namespace-scoped operations.
//
// `holderIdentity` is recorded on every lease so `kubectl get
// leases` shows which pod owns a given review. Production callers
// pass `os.Hostname()` (the pod name when the downward API populates
// it); a constant per-pod value works fine for local testing.
func NewK8sLeaseDedupFromInCluster(namespace, holderIdentity string) (*K8sLeaseDedup, error) {
	if !IsInKubernetes() {
		return nil, ErrLeaseClientNotConfigured
	}
	cfg, err := rest.InClusterConfig()
	if err != nil {
		return nil, fmt.Errorf("dedup-lease: in-cluster config: %w", err)
	}
	clientset, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("dedup-lease: build clientset: %w", err)
	}
	if namespace == "" {
		ns, readErr := os.ReadFile(podNamespaceFile)
		if readErr != nil {
			return nil, fmt.Errorf("dedup-lease: read pod namespace from %s: %w", podNamespaceFile, readErr)
		}
		namespace = strings.TrimSpace(string(ns))
	}
	if namespace == "" {
		return nil, errors.New("dedup-lease: pod namespace is empty after fallback read")
	}
	return NewK8sLeaseDedup(clientset.CoordinationV1().Leases(namespace), holderIdentity), nil
}

// SeenRecently performs the optimistic-create dance against the K8s
// API server: if `Create` returns 201 Created → this pod acquired the
// lease and the caller proceeds; if it returns 409 AlreadyExists →
// another pod owns the review and the caller short-circuits; on any
// other error the call degrades to "not seen" so the webhook is
// processed (the same baseline as no dedup at all).
func (d *K8sLeaseDedup) SeenRecently(ctx context.Context, key string) bool {
	leaseName := sanitizeLeaseName(key)
	duration := leaseDurationSeconds
	now := metav1.NewMicroTime(time.Now())
	holder := d.holderIdentity
	lease := &coordinationv1.Lease{
		ObjectMeta: metav1.ObjectMeta{Name: leaseName},
		Spec: coordinationv1.LeaseSpec{
			HolderIdentity:       &holder,
			LeaseDurationSeconds: &duration,
			AcquireTime:          &now,
			RenewTime:            &now,
		},
	}

	callCtx, cancel := context.WithTimeout(ctx, leaseAPITimeout)
	defer cancel()

	_, err := d.client.Create(callCtx, lease, metav1.CreateOptions{})
	if err == nil {
		// Acquired — this pod owns the review.
		return false
	}
	if apierrors.IsAlreadyExists(err) {
		// Another pod already holds the lease — duplicate.
		return true
	}
	logger.WithFields(logger.Fields{
		"key":        key,
		"lease_name": leaseName,
	}).Warnf("dedup-lease: Create failed (%v) — falling back to process the webhook", err)
	return false
}

// Forget releases the lease so a legitimate re-review (e.g. a new
// push minutes later) can re-acquire immediately without waiting for
// the TTL. Idempotent: a `NotFound` is treated as success because the
// caller may invoke `Forget` after the lease has already aged out, or
// after another rollback path has already removed it.
func (d *K8sLeaseDedup) Forget(ctx context.Context, key string) {
	leaseName := sanitizeLeaseName(key)
	callCtx, cancel := context.WithTimeout(ctx, leaseAPITimeout)
	defer cancel()

	err := d.client.Delete(callCtx, leaseName, metav1.DeleteOptions{})
	if err == nil || apierrors.IsNotFound(err) {
		return
	}
	logger.WithFields(logger.Fields{
		"key":        key,
		"lease_name": leaseName,
	}).Warnf("dedup-lease: Delete failed (%v) — lease will be released by TTL", err)
}

// sanitizeLeaseName converts an arbitrary dedup key (e.g.
// `ado:repo-id:12345`) into a string that conforms to the RFC 1123
// subdomain rules the K8s API server enforces on resource names:
// lowercase alphanumeric or `-`, ≤ 253 chars, must start and end with
// alphanumeric. The transformation is deterministic so two callers
// producing the same key always land on the same lease name.
//
// The `code-guru-` prefix scopes the lease names to this application
// so a `kubectl get leases` against a shared namespace shows our
// dedup leases distinctly from leader-election leases owned by other
// controllers.
func sanitizeLeaseName(key string) string {
	const prefix = "code-guru-"
	var b strings.Builder
	b.Grow(len(prefix) + len(key))
	b.WriteString(prefix)
	for _, r := range strings.ToLower(key) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-':
			b.WriteRune(r)
		default:
			// `:` from `ado:repo:pr` and `/` from `gh:owner/repo:pr`
			// are the two characters we hit in practice; `-` keeps the
			// segment boundaries readable in `kubectl get leases`.
			b.WriteRune('-')
		}
	}
	name := b.String()
	if len(name) > k8sNameMaxLen {
		name = name[:k8sNameMaxLen]
	}
	// Trim trailing `-` so the name still ends with an alphanumeric
	// (RFC 1123). Untrimmed names would be rejected by the API server
	// with `Invalid`; trimming defends against the truncation step
	// above landing the cut on a `-`.
	name = strings.TrimRight(name, "-")
	if name == "" {
		// Defensive fallback for a pathological all-symbols key. Not
		// reachable from the production key shapes (`ado:...` /
		// `gh:...`) but keeps the contract total.
		return "code-guru-empty"
	}
	return name
}

// ErrLeaseClientNotConfigured is returned by NewK8sLeaseDedupFromInCluster
// when the caller asks for an in-cluster client outside of a pod (no
// `KUBERNETES_SERVICE_HOST`). The caller is expected to fall back to
// the in-memory backend in that case.
var ErrLeaseClientNotConfigured = errors.New("K8s in-cluster config not available; falling back to in-memory dedup")

// kubernetesServiceHostEnv is the env var the kubelet injects into
// every pod. Its presence is the canonical signal "I am running
// inside a Kubernetes pod" — the same check the standard
// `rest.InClusterConfig` uses internally to decide whether the
// in-cluster wiring is available.
const kubernetesServiceHostEnv = "KUBERNETES_SERVICE_HOST"

// podNamespaceFile is the standard projected path for the pod's
// namespace. Every pod has it mounted by the kubelet via the
// downward API, so we never need to require an explicit
// `POD_NAMESPACE` env var on the deployment manifest.
const podNamespaceFile = "/var/run/secrets/kubernetes.io/serviceaccount/namespace"

// IsInKubernetes returns true when the process is running inside a
// Kubernetes pod. Used by the serve controller to decide between the
// cross-pod K8s-Lease dedup backend and the per-pod in-memory cache.
// Lives next to the lease implementation so the heuristic is in one
// place — when the heuristic needs to evolve (e.g. adding a forced
// `CODE_GURU_DEDUP_BACKEND=lease` override), it changes here.
func IsInKubernetes() bool {
	return os.Getenv(kubernetesServiceHostEnv) != ""
}
