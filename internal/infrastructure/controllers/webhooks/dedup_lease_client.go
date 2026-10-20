package webhooks

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// This file speaks the `coordination.k8s.io/v1` REST API directly over
// `net/http` instead of going through `k8s.io/client-go`.
//
// The dedup backend needs four verbs on one namespaced resource. Pulling
// `client-go` in for that dragged the whole `k8s.io/api` +
// `k8s.io/apimachinery` + `k8s.io/kube-openapi` +
// `sigs.k8s.io/structured-merge-diff` tree behind it, which cost two
// recurring problems: the release trains move in lockstep so a single
// out-of-step bump stops the build compiling, and `client-go` master
// carries a `v0.0.0-` pseudo-version that sorts below the "fixed in"
// version of two 2021 advisories, so `govulncheck` reports them against
// current code forever. Neither is a real defect in this bot, and both
// disappear with the dependency.
//
// The wire contract implemented here is the stable, documented Kubernetes
// REST API, which changes far more slowly than the Go client's module graph.

const (
	// leaseAPIPathFormat is the namespaced collection endpoint for the
	// `coordination.k8s.io/v1` Lease resource.
	leaseAPIPathFormat = "%s/apis/coordination.k8s.io/v1/namespaces/%s/leases"

	// maxLeaseResponseBytes caps what we read off the API server. A Lease
	// is a few hundred bytes; the cap exists so a wedged or hostile
	// endpoint cannot stream unbounded data into the webhook hot path.
	maxLeaseResponseBytes = 1 << 20

	leaseAPIVersion   = "coordination.k8s.io/v1"
	leaseKind         = "Lease"
	deleteAPIVersion  = "meta.k8s.io/v1"
	deleteOptionsKind = "DeleteOptions"
)

// Lease mirrors the subset of the `coordination.k8s.io/v1` Lease object
// the dedup backend reads and writes. Fields the backend never touches
// (labels, annotations, ownerReferences, managedFields…) are omitted:
// the API server preserves what it is not sent on create, and the
// renewal path round-trips the object it just read, so nothing is lost.
type Lease struct {
	APIVersion string    `json:"apiVersion,omitempty"`
	Kind       string    `json:"kind,omitempty"`
	Metadata   LeaseMeta `json:"metadata"`
	Spec       LeaseSpec `json:"spec"`
}

// LeaseMeta is the slice of `metav1.ObjectMeta` this package needs.
// `UID` backs the takeover path's delete precondition and
// `ResourceVersion` backs the renewal path's optimistic concurrency —
// without echoing the version back on an update the API server would
// accept a write based on a stale read.
type LeaseMeta struct {
	Name            string `json:"name"`
	UID             string `json:"uid,omitempty"`
	ResourceVersion string `json:"resourceVersion,omitempty"`
}

// LeaseSpec mirrors `coordinationv1.LeaseSpec`. Every field is a pointer
// because the API server distinguishes "absent" from "zero", and
// `leaseExpired` relies on that distinction to treat a malformed lease
// as takeover-eligible rather than as one acquired at the epoch.
type LeaseSpec struct {
	HolderIdentity       *string    `json:"holderIdentity,omitempty"`
	LeaseDurationSeconds *int32     `json:"leaseDurationSeconds,omitempty"`
	AcquireTime          *MicroTime `json:"acquireTime,omitempty"`
	RenewTime            *MicroTime `json:"renewTime,omitempty"`
}

// MicroTime marshals as RFC 3339 with microsecond precision, which is
// the shape the API server uses for `metav1.MicroTime` fields. Go's
// default `time.Time` marshalling emits nanoseconds, which the API
// server's decoder rejects for this type.
type MicroTime struct {
	time.Time
}

// microTimeLayout is `metav1.MicroTime`'s wire format.
const microTimeLayout = "2006-01-02T15:04:05.000000Z07:00"

func (t MicroTime) MarshalJSON() ([]byte, error) {
	if t.IsZero() {
		return []byte("null"), nil
	}
	return []byte(`"` + t.UTC().Format(microTimeLayout) + `"`), nil
}

func (t *MicroTime) UnmarshalJSON(data []byte) error {
	if string(data) == "null" {
		return nil
	}
	var raw string
	if err := json.Unmarshal(data, &raw); err != nil {
		return fmt.Errorf("dedup-lease: decode microtime: %w", err)
	}
	if raw == "" {
		return nil
	}
	// RFC 3339 parsing accepts a fractional-second field even though the
	// layout does not spell one out, so this reads both the microsecond
	// form the API server emits and a whole-second form.
	parsed, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return fmt.Errorf("dedup-lease: parse microtime %q: %w", raw, err)
	}
	t.Time = parsed
	return nil
}

// leaseRESTClient is the `LeaseClient` implementation that talks to the
// real API server.
type leaseRESTClient struct {
	collectionURL string
	httpClient    *http.Client
	token         *tokenSource
}

// newInClusterLeaseClient builds a lease client from the credentials the
// kubelet projects into every pod. It replaces
// `rest.InClusterConfig` + `kubernetes.NewForConfig`.
func newInClusterLeaseClient(namespace string) (*leaseRESTClient, error) {
	base, client, token, err := newInClusterTransport()
	if err != nil {
		return nil, err
	}
	return newLeaseRESTClient(base, namespace, client, token), nil
}

// newLeaseRESTClient assembles a client against an arbitrary base URL so
// tests can point it at an `httptest` server.
func newLeaseRESTClient(baseURL, namespace string, client *http.Client, token *tokenSource) *leaseRESTClient {
	return &leaseRESTClient{
		collectionURL: fmt.Sprintf(
			leaseAPIPathFormat,
			strings.TrimSuffix(baseURL, "/"),
			url.PathEscape(namespace),
		),
		httpClient: client,
		token:      token,
	}
}

func (c *leaseRESTClient) objectURL(name string) string {
	return c.collectionURL + "/" + url.PathEscape(name)
}

func (c *leaseRESTClient) Create(ctx context.Context, lease *Lease) (*Lease, error) {
	return decodeLease(c.do(ctx, http.MethodPost, c.collectionURL, lease))
}

func (c *leaseRESTClient) Get(ctx context.Context, name string) (*Lease, error) {
	return decodeLease(c.do(ctx, http.MethodGet, c.objectURL(name), nil))
}

func (c *leaseRESTClient) Update(ctx context.Context, lease *Lease) (*Lease, error) {
	// The API server rejects an update whose body omits the type, and the
	// object may have come from a Get that elided it.
	lease.APIVersion = leaseAPIVersion
	lease.Kind = leaseKind
	return decodeLease(c.do(ctx, http.MethodPut, c.objectURL(lease.Metadata.Name), lease))
}

// Delete removes the lease. A non-empty `uid` is sent as a precondition
// so the takeover path cannot delete a lease that was recreated by
// another pod between our Get and our Delete.
func (c *leaseRESTClient) Delete(ctx context.Context, name, uid string) error {
	var payload any
	if uid != "" {
		payload = deleteOptions{
			APIVersion:    deleteAPIVersion,
			Kind:          deleteOptionsKind,
			Preconditions: &preconditions{UID: uid},
		}
	}
	_, err := c.do(ctx, http.MethodDelete, c.objectURL(name), payload)
	return err
}

type deleteOptions struct {
	APIVersion    string         `json:"apiVersion"`
	Kind          string         `json:"kind"`
	Preconditions *preconditions `json:"preconditions,omitempty"`
}

type preconditions struct {
	UID string `json:"uid"`
}

// do performs one API call through the shared Kubernetes request helper,
// capped at maxLeaseResponseBytes. Non-2xx responses become
// `*apiStatusError` so the callers' `isNotFound` / `isAlreadyExists`
// checks work against them.
func (c *leaseRESTClient) do(ctx context.Context, method, endpoint string, payload any) ([]byte, error) {
	return doKubernetesRequest(ctx, c.httpClient, c.token, kubernetesRequest{
		method:   method,
		endpoint: endpoint,
		payload:  payload,
		maxBody:  maxLeaseResponseBytes,
	})
}

func decodeLease(data []byte, err error) (*Lease, error) {
	if err != nil {
		return nil, err
	}
	var lease Lease
	if unmarshalErr := json.Unmarshal(data, &lease); unmarshalErr != nil {
		return nil, fmt.Errorf("dedup-lease: decode lease response: %w", unmarshalErr)
	}
	return &lease, nil
}
