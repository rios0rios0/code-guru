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
	"os"
	"strings"
	"sync"
	"time"
)

// This file is the small, shared core of the two Kubernetes adapters the
// server carries — the `coordination.k8s.io/v1` Lease client behind the
// cross-pod webhook dedup and the `batch/v1` Job client behind the review-Job
// executor. Both speak the documented REST API directly over `net/http`
// instead of going through `k8s.io/client-go`; see `dedup_lease_client.go`
// for why that dependency was removed and must not come back.

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

	// tokenRefreshInterval bounds how long a cached ServiceAccount token
	// is reused. Projected tokens are short-lived (one hour by default)
	// and the kubelet rewrites the file in place well before expiry, so
	// the token MUST be re-read periodically — `client-go` did this for
	// us via its reloading transport. Reading once at startup would
	// authenticate fine for an hour and then fail every call with 401.
	tokenRefreshInterval = 60 * time.Second

	reasonNotFound     = "NotFound"
	reasonAlreadyExist = "AlreadyExists"
	reasonConflict     = "Conflict"

	// contentTypeJSON and contentTypeMergePatch are the request bodies the
	// adapters send: whole objects, and RFC 7386 merge patches.
	contentTypeJSON       = "application/json"
	contentTypeMergePatch = "application/merge-patch+json"
)

// apiStatusError carries the API server's own error classification. The
// `reason` string is what distinguishes outcomes that share a status code —
// `AlreadyExists` and `Conflict` are both HTTP 409, so the status code alone
// cannot tell "another pod holds this lease" from "your precondition did not
// match".
type apiStatusError struct {
	Verb    string
	Code    int
	Reason  string
	Message string
}

func (e *apiStatusError) Error() string {
	if e.Message != "" {
		return fmt.Sprintf("kubernetes: %s returned %d (%s): %s", e.Verb, e.Code, e.Reason, e.Message)
	}
	return fmt.Sprintf("kubernetes: %s returned %d (%s)", e.Verb, e.Code, e.Reason)
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

// isConflict reports a failed optimistic-concurrency precondition: the object
// changed between the read and the write that carried its resourceVersion.
// Only the reason identifies it — the status code is shared with
// AlreadyExists.
func isConflict(err error) bool {
	var status *apiStatusError
	if !errors.As(err, &status) {
		return false
	}
	return status.Reason == reasonConflict
}

// isRefusal reports a write the API server answered and turned down — an
// invalid object, a denied admission, a missing permission, an object that is
// gone — so the change certainly did not land. A conflict is left out (the
// object changed, and the caller's next read decides what that means), and
// so are throttling and every failure without the API server's answer, whose
// write may or may not have landed.
func isRefusal(err error) bool {
	var status *apiStatusError
	if !errors.As(err, &status) {
		return false
	}
	return status.Code >= http.StatusBadRequest && status.Code < http.StatusInternalServerError &&
		status.Code != http.StatusConflict && status.Code != http.StatusTooManyRequests
}

// tokenSource serves the pod's ServiceAccount bearer token, re-reading
// it from disk at most once per `tokenRefreshInterval` so rotation is
// picked up. A read failure falls back to the last token that worked:
// the kubelet replaces the file atomically, but a transient failure
// must not turn into a hard authentication outage on the hot path.
type tokenSource struct {
	path string
	// refreshAfter is how long a cached token is served before the file is
	// re-read. Injected rather than read from the constant so a test can
	// pass zero and force every call to go to disk — otherwise the cache
	// short-circuits before the filesystem is ever touched and both the
	// rotation and the read-failure fallback go untested.
	refreshAfter time.Duration
	mu           sync.Mutex
	cached       string
	readAt       time.Time
}

func (s *tokenSource) get() (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.cached != "" && time.Since(s.readAt) < s.refreshAfter {
		return s.cached, nil
	}
	raw, err := os.ReadFile(s.path)
	if err != nil {
		if s.cached != "" {
			return s.cached, nil
		}
		return "", fmt.Errorf("kubernetes: read service account token from %s: %w", s.path, err)
	}
	s.cached = strings.TrimSpace(string(raw))
	s.readAt = time.Now()
	return s.cached, nil
}

// newInClusterTransport builds the API server base URL, an HTTP client that
// trusts the cluster CA, and the rotating token source from the credentials
// the kubelet projects into every pod. It replaces `rest.InClusterConfig`.
func newInClusterTransport() (string, *http.Client, *tokenSource, error) {
	host := os.Getenv(kubernetesServiceHostEnv)
	port := os.Getenv(kubernetesServicePortEnv)
	if host == "" || port == "" {
		return "", nil, nil, ErrLeaseClientNotConfigured
	}

	caPEM, err := os.ReadFile(podCAFile)
	if err != nil {
		return "", nil, nil, fmt.Errorf("kubernetes: read cluster CA from %s: %w", podCAFile, err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(caPEM) {
		return "", nil, nil, fmt.Errorf("kubernetes: no usable certificate in %s", podCAFile)
	}

	client := &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12},
		},
	}
	// net.JoinHostPort brackets IPv6 literals; a dual-stack cluster hands
	// out a bare IPv6 address in KUBERNETES_SERVICE_HOST.
	base := "https://" + net.JoinHostPort(host, port)
	return base, client, &tokenSource{path: podTokenFile, refreshAfter: tokenRefreshInterval}, nil
}

// kubernetesRequest is one API call: the verb, the absolute endpoint, an
// optional JSON-encodable payload with its content type, and the cap on how
// much of the response is read.
type kubernetesRequest struct {
	method      string
	endpoint    string
	payload     any
	contentType string
	maxBody     int64
}

// doKubernetesRequest performs one API call and returns the raw response
// body. Non-2xx responses become `*apiStatusError` so callers can branch on
// isNotFound / isAlreadyExists / isConflict.
func doKubernetesRequest(
	ctx context.Context, client *http.Client, token *tokenSource, call kubernetesRequest,
) ([]byte, error) {
	var body io.Reader
	if call.payload != nil {
		encoded, err := json.Marshal(call.payload)
		if err != nil {
			return nil, fmt.Errorf("kubernetes: encode %s payload: %w", call.method, err)
		}
		body = bytes.NewReader(encoded)
	}

	// every variable path segment (namespace, object name) is url.PathEscape'd by the adapters, so no
	// caller-supplied value can steer the request off the in-cluster API server.
	request, err := http.NewRequestWithContext(ctx, call.method, call.endpoint, body)
	if err != nil {
		return nil, fmt.Errorf("kubernetes: build %s request: %w", call.method, err)
	}
	bearer, err := token.get()
	if err != nil {
		return nil, err
	}
	request.Header.Set("Authorization", "Bearer "+bearer)
	request.Header.Set("Accept", contentTypeJSON)
	if call.payload != nil {
		contentType := call.contentType
		if contentType == "" {
			contentType = contentTypeJSON
		}
		request.Header.Set("Content-Type", contentType)
	}

	response, err := client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("kubernetes: %s request failed: %w", call.method, err)
	}
	defer func() { _ = response.Body.Close() }()

	data, err := io.ReadAll(io.LimitReader(response.Body, call.maxBody))
	if err != nil {
		return nil, fmt.Errorf("kubernetes: read %s response: %w", call.method, err)
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return nil, newStatusError(call.method, response.StatusCode, data)
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

// NewConflictError builds the API server's Conflict outcome for `verb`, so
// test doubles of the Kubernetes adapters can return exactly the error the
// production code branches on.
func NewConflictError(verb string) error {
	return &apiStatusError{Verb: verb, Code: http.StatusConflict, Reason: reasonConflict}
}
