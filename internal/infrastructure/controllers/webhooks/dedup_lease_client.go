package webhooks

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
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
	// kubernetesServicePortEnv pairs with `kubernetesServiceHostEnv`. The
	// kubelet injects both into every pod; together they form the API
	// server's in-cluster address.
	kubernetesServicePortEnv = "KUBERNETES_SERVICE_PORT"

	// podTokenFile and podCAFile are the projected ServiceAccount
	// credentials the kubelet mounts into every pod — the bearer token
	// used to authenticate and the CA that signs the API server's
	// certificate.
	podTokenFile = "/var/run/secrets/kubernetes.io/serviceaccount/token" //nolint:gosec // path, not a secret
	podCAFile    = "/var/run/secrets/kubernetes.io/serviceaccount/ca.crt"

	// leaseAPIPathFormat is the namespaced collection endpoint for the
	// `coordination.k8s.io/v1` Lease resource.
	leaseAPIPathFormat = "%s/apis/coordination.k8s.io/v1/namespaces/%s/leases"

	// maxLeaseResponseBytes caps what we read off the API server. A Lease
	// is a few hundred bytes; the cap exists so a wedged or hostile
	// endpoint cannot stream unbounded data into the webhook hot path.
	maxLeaseResponseBytes = 1 << 20

	// tokenRefreshInterval bounds how long a cached ServiceAccount token
	// is reused. Projected tokens are short-lived (one hour by default)
	// and the kubelet rewrites the file in place well before expiry, so
	// the token MUST be re-read periodically — `client-go` did this for
	// us via its reloading transport. Reading once at startup would
	// authenticate fine for an hour and then fail every call with 401.
	tokenRefreshInterval = 60 * time.Second

	leaseAPIVersion    = "coordination.k8s.io/v1"
	leaseKind          = "Lease"
	deleteAPIVersion   = "meta.k8s.io/v1"
	deleteOptionsKind  = "DeleteOptions"
	reasonNotFound     = "NotFound"
	reasonAlreadyExist = "AlreadyExists"
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

// apiStatusError carries the API server's own error classification. The
// `reason` string is what distinguishes the two outcomes the dedup dance
// is built on — `AlreadyExists` and `Conflict` are both HTTP 409, so the
// status code alone cannot tell "another pod holds this lease" from
// "your delete precondition did not match".
type apiStatusError struct {
	Verb    string
	Code    int
	Reason  string
	Message string
}

func (e *apiStatusError) Error() string {
	if e.Message != "" {
		return fmt.Sprintf("dedup-lease: %s returned %d (%s): %s", e.Verb, e.Code, e.Reason, e.Message)
	}
	return fmt.Sprintf("dedup-lease: %s returned %d (%s)", e.Verb, e.Code, e.Reason)
}

// isNotFound and isAlreadyExists replace `apierrors.IsNotFound` /
// `apierrors.IsAlreadyExists`. They prefer the API server's `reason`
// and fall back to the status code, which covers proxies and admission
// webhooks that return a bare code with no Status body.
func isNotFound(err error) bool {
	var status *apiStatusError
	if !errors.As(err, &status) {
		return false
	}
	return status.Reason == reasonNotFound || (status.Reason == "" && status.Code == http.StatusNotFound)
}

func isAlreadyExists(err error) bool {
	var status *apiStatusError
	if !errors.As(err, &status) {
		return false
	}
	return status.Reason == reasonAlreadyExist || (status.Reason == "" && status.Code == http.StatusConflict)
}

// tokenSource serves the pod's ServiceAccount bearer token, re-reading
// it from disk at most once per `tokenRefreshInterval` so rotation is
// picked up. A read failure falls back to the last token that worked:
// the kubelet replaces the file atomically, but a transient failure
// must not turn into a hard authentication outage on the hot path.
type tokenSource struct {
	path   string
	mu     sync.Mutex
	cached string
	readAt time.Time
}

func (s *tokenSource) get() (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.cached != "" && time.Since(s.readAt) < tokenRefreshInterval {
		return s.cached, nil
	}
	raw, err := os.ReadFile(s.path)
	if err != nil {
		if s.cached != "" {
			return s.cached, nil
		}
		return "", fmt.Errorf("dedup-lease: read service account token from %s: %w", s.path, err)
	}
	s.cached = strings.TrimSpace(string(raw))
	s.readAt = time.Now()
	return s.cached, nil
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
	host := os.Getenv(kubernetesServiceHostEnv)
	port := os.Getenv(kubernetesServicePortEnv)
	if host == "" || port == "" {
		return nil, ErrLeaseClientNotConfigured
	}

	caPEM, err := os.ReadFile(podCAFile)
	if err != nil {
		return nil, fmt.Errorf("dedup-lease: read cluster CA from %s: %w", podCAFile, err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(caPEM) {
		return nil, fmt.Errorf("dedup-lease: no usable certificate in %s", podCAFile)
	}

	client := &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12},
		},
	}
	// net.JoinHostPort brackets IPv6 literals; a dual-stack cluster hands
	// out a bare IPv6 address in KUBERNETES_SERVICE_HOST.
	base := "https://" + net.JoinHostPort(host, port)
	return newLeaseRESTClient(base, namespace, client, &tokenSource{path: podTokenFile}), nil
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

// do performs one API call and returns the raw response body. Non-2xx
// responses become `*apiStatusError` so the callers' `isNotFound` /
// `isAlreadyExists` checks work against them.
func (c *leaseRESTClient) do(ctx context.Context, method, endpoint string, payload any) ([]byte, error) {
	var body io.Reader
	if payload != nil {
		encoded, err := json.Marshal(payload)
		if err != nil {
			return nil, fmt.Errorf("dedup-lease: encode %s payload: %w", method, err)
		}
		body = bytes.NewReader(encoded)
	}

	//nolint:gosec // G704: the host comes from the kubelet-injected KUBERNETES_SERVICE_HOST/PORT,
	// and both variable path segments (namespace, lease name) are url.PathEscape'd — a lease name
	// is additionally constrained to [a-z0-9-] by sanitizeLeaseName, so no caller-supplied value
	// can steer the request off the in-cluster API server.
	request, err := http.NewRequestWithContext(ctx, method, endpoint, body)
	if err != nil {
		return nil, fmt.Errorf("dedup-lease: build %s request: %w", method, err)
	}
	token, err := c.token.get()
	if err != nil {
		return nil, err
	}
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Accept", "application/json")
	if payload != nil {
		request.Header.Set("Content-Type", "application/json")
	}

	//nolint:gosec // G704: same request as above — see the rationale on http.NewRequestWithContext.
	response, err := c.httpClient.Do(request)
	if err != nil {
		return nil, fmt.Errorf("dedup-lease: %s request failed: %w", method, err)
	}
	defer func() { _ = response.Body.Close() }()

	data, err := io.ReadAll(io.LimitReader(response.Body, maxLeaseResponseBytes))
	if err != nil {
		return nil, fmt.Errorf("dedup-lease: read %s response: %w", method, err)
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return nil, newStatusError(method, response.StatusCode, data)
	}
	return data, nil
}

// newStatusError turns the API server's `Status` body into a typed
// error. A body that does not parse still yields a usable error carrying
// the HTTP code, so the caller's fallbacks stay meaningful.
func newStatusError(verb string, code int, body []byte) error {
	status := struct {
		Reason  string `json:"reason"`
		Message string `json:"message"`
	}{}
	_ = json.Unmarshal(body, &status)
	return &apiStatusError{Verb: verb, Code: code, Reason: status.Reason, Message: status.Message}
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
