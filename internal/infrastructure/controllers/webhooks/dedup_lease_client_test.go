package webhooks_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/rios0rios0/codeguru/internal/infrastructure/controllers/webhooks"
)

// capturedRequest records what the adapter actually put on the wire.
// The Kubernetes REST contract is the thing this adapter replaced
// client-go for, so the tests assert the request shape directly rather
// than trusting a round-trip through a hand-written server.
type capturedRequest struct {
	Method string
	Path   string
	Auth   string
	Body   string
}

// newRecordingAPIServer stands in for the Kubernetes API server. It is a
// real HTTP server under test control (not a transport mock), records
// each request, and replies with the caller-supplied status and body.
func newRecordingAPIServer(t *testing.T, status int, response string) (*httptest.Server, *[]capturedRequest) {
	t.Helper()
	captured := make([]capturedRequest, 0, 4)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// assert, not require: this runs on the server's goroutine, where
		// a FailNow would abandon the handler instead of failing the test.
		body, err := io.ReadAll(r.Body)
		assert.NoError(t, err)
		captured = append(captured, capturedRequest{
			Method: r.Method,
			Path:   r.URL.Path,
			Auth:   r.Header.Get("Authorization"),
			Body:   string(body),
		})
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(response))
	}))
	t.Cleanup(server.Close)
	return server, &captured
}

func leaseJSON(name, uid, resourceVersion string) string {
	return `{"apiVersion":"coordination.k8s.io/v1","kind":"Lease","metadata":{"name":"` + name +
		`","uid":"` + uid + `","resourceVersion":"` + resourceVersion +
		`"},"spec":{"holderIdentity":"pod-a","leaseDurationSeconds":60,` +
		`"acquireTime":"2026-09-13T10:00:00.000000Z","renewTime":"2026-09-13T10:00:30.000000Z"}}`
}

func TestLeaseRESTClientWireContract(t *testing.T) {
	t.Parallel()

	const namespace = "code-guru"

	t.Run("should POST to the namespaced collection when creating a lease", func(t *testing.T) {
		t.Parallel()

		// given
		server, captured := newRecordingAPIServer(t, http.StatusCreated, leaseJSON("code-guru-abc", "uid-1", "7"))
		client := webhooks.NewLeaseRESTClientForTest(server.URL, namespace, "token-value")
		holder := "pod-a"
		duration := int32(60)
		now := webhooks.MicroTime{Time: time.Now()}

		// when
		created, err := client.Create(context.Background(), &webhooks.Lease{
			APIVersion: "coordination.k8s.io/v1",
			Kind:       "Lease",
			Metadata:   webhooks.LeaseMeta{Name: "code-guru-abc"},
			Spec: webhooks.LeaseSpec{
				HolderIdentity:       &holder,
				LeaseDurationSeconds: &duration,
				AcquireTime:          &now,
				RenewTime:            &now,
			},
		})

		// then
		require.NoError(t, err)
		require.Len(t, *captured, 1)
		request := (*captured)[0]
		assert.Equal(t, http.MethodPost, request.Method)
		assert.Equal(t,
			"/apis/coordination.k8s.io/v1/namespaces/code-guru/leases", request.Path,
			"create must target the namespaced collection, not the object endpoint")
		assert.Equal(t, "Bearer token-value", request.Auth, "the ServiceAccount token authenticates every call")
		assert.Contains(t, request.Body, `"holderIdentity":"pod-a"`)
		assert.Equal(t, "uid-1", created.Metadata.UID, "the decoded UID backs the takeover precondition")
	})

	t.Run("should GET the object endpoint and decode the lease times", func(t *testing.T) {
		t.Parallel()

		// given
		server, captured := newRecordingAPIServer(t, http.StatusOK, leaseJSON("code-guru-abc", "uid-1", "7"))
		client := webhooks.NewLeaseRESTClientForTest(server.URL, namespace, "token-value")

		// when
		lease, err := client.Get(context.Background(), "code-guru-abc")

		// then
		require.NoError(t, err)
		assert.Equal(t, http.MethodGet, (*captured)[0].Method)
		assert.Equal(t, "/apis/coordination.k8s.io/v1/namespaces/code-guru/leases/code-guru-abc", (*captured)[0].Path)
		require.NotNil(t, lease.Spec.RenewTime, "renewTime drives the staleness check — it must decode")
		assert.Equal(t,
			"2026-09-13T10:00:30Z", lease.Spec.RenewTime.UTC().Format(time.RFC3339),
			"a microsecond-precision timestamp from the API server must parse")
		assert.Equal(t, "7", lease.Metadata.ResourceVersion)
	})

	t.Run("should PUT the resourceVersion back when updating so the write is conflict-checked", func(t *testing.T) {
		t.Parallel()

		// given: the renewal path reads then writes; dropping the
		// resourceVersion would let a stale read overwrite a newer lease.
		server, captured := newRecordingAPIServer(t, http.StatusOK, leaseJSON("code-guru-abc", "uid-1", "8"))
		client := webhooks.NewLeaseRESTClientForTest(server.URL, namespace, "token-value")

		// when
		_, err := client.Update(context.Background(), &webhooks.Lease{
			Metadata: webhooks.LeaseMeta{Name: "code-guru-abc", UID: "uid-1", ResourceVersion: "7"},
		})

		// then
		require.NoError(t, err)
		request := (*captured)[0]
		assert.Equal(t, http.MethodPut, request.Method)
		assert.Equal(t, "/apis/coordination.k8s.io/v1/namespaces/code-guru/leases/code-guru-abc", request.Path)
		assert.Contains(t, request.Body, `"resourceVersion":"7"`,
			"the update must echo the observed resourceVersion for optimistic concurrency")
		assert.Contains(t, request.Body, `"apiVersion":"coordination.k8s.io/v1"`,
			"the API server rejects a typed update whose body omits the type")
	})

	t.Run("should send a UID precondition only when deleting a specific lease instance", func(t *testing.T) {
		t.Parallel()

		// given
		server, captured := newRecordingAPIServer(t, http.StatusOK, `{"kind":"Status","status":"Success"}`)
		client := webhooks.NewLeaseRESTClientForTest(server.URL, namespace, "token-value")

		// when: the takeover path pins the UID; the release path does not
		require.NoError(t, client.Delete(context.Background(), "code-guru-abc", "uid-1"))
		require.NoError(t, client.Delete(context.Background(), "code-guru-abc", ""))

		// then
		require.Len(t, *captured, 2)
		assert.Equal(t, http.MethodDelete, (*captured)[0].Method)
		assert.Contains(t, (*captured)[0].Body, `"uid":"uid-1"`,
			"takeover must be conditioned on the UID it observed, or it can delete a lease another pod just recreated")
		assert.Empty(t, (*captured)[1].Body,
			"an unconditional release must not pin a UID — the caller may be releasing a lease it re-acquired")
	})
}

func TestLeaseRESTClientErrorMapping(t *testing.T) {
	t.Parallel()

	const namespace = "code-guru"

	t.Run("should map an AlreadyExists status body to the duplicate-acquisition outcome", func(t *testing.T) {
		t.Parallel()

		// given: the 409 that means "another pod already holds this
		// lease" — the single most load-bearing response in the backend.
		body := `{"kind":"Status","status":"Failure","reason":"AlreadyExists",` +
			`"message":"leases.coordination.k8s.io \"code-guru-abc\" already exists","code":409}`
		server, _ := newRecordingAPIServer(t, http.StatusConflict, body)
		client := webhooks.NewLeaseRESTClientForTest(server.URL, namespace, "token-value")

		// when
		_, err := client.Create(context.Background(), &webhooks.Lease{
			Metadata: webhooks.LeaseMeta{Name: "code-guru-abc"},
		})

		// then
		require.Error(t, err)
		assert.True(t, webhooks.IsAlreadyExistsForTest(err), "a 409/AlreadyExists must read as a contended acquire")
		assert.False(t, webhooks.IsNotFoundForTest(err))
	})

	t.Run("should distinguish a failed precondition from a name collision on the same status code", func(t *testing.T) {
		t.Parallel()

		// given: a delete whose UID precondition did not match also
		// returns 409, but with reason `Conflict`. Treating it as
		// AlreadyExists would make a takeover think it had acquired.
		body := `{"kind":"Status","status":"Failure","reason":"Conflict",` +
			`"message":"the UID in the precondition does not match","code":409}`
		server, _ := newRecordingAPIServer(t, http.StatusConflict, body)
		client := webhooks.NewLeaseRESTClientForTest(server.URL, namespace, "token-value")

		// when
		err := client.Delete(context.Background(), "code-guru-abc", "stale-uid")

		// then
		require.Error(t, err)
		assert.False(t, webhooks.IsAlreadyExistsForTest(err),
			"reason `Conflict` shares the 409 code with AlreadyExists but means something else entirely")
		assert.False(t, webhooks.IsNotFoundForTest(err))
	})

	t.Run("should map a NotFound status body to the released-lease outcome", func(t *testing.T) {
		t.Parallel()

		// given
		body := `{"kind":"Status","status":"Failure","reason":"NotFound","code":404}`
		server, _ := newRecordingAPIServer(t, http.StatusNotFound, body)
		client := webhooks.NewLeaseRESTClientForTest(server.URL, namespace, "token-value")

		// when
		_, err := client.Get(context.Background(), "code-guru-abc")

		// then
		require.Error(t, err)
		assert.True(t, webhooks.IsNotFoundForTest(err), "Forget and Renew both treat NotFound as success")
	})

	t.Run("should still classify by status code when the body carries no Status reason", func(t *testing.T) {
		t.Parallel()

		// given: an ingress or admission webhook can return a bare code
		// with an HTML or empty body; the fallback keeps the dedup
		// behaving instead of treating it as an unknown failure.
		server, _ := newRecordingAPIServer(t, http.StatusNotFound, "not found")
		client := webhooks.NewLeaseRESTClientForTest(server.URL, namespace, "token-value")

		// when
		_, err := client.Get(context.Background(), "code-guru-abc")

		// then
		require.Error(t, err)
		assert.True(t, webhooks.IsNotFoundForTest(err), "a bodyless 404 must still read as NotFound")
	})

	t.Run("should surface a server error as a plain failure so the caller degrades to processing", func(t *testing.T) {
		t.Parallel()

		// given: the "API server is wedged" case. It must be neither
		// NotFound nor AlreadyExists so `SeenRecently` falls through to
		// processing the webhook rather than silently dropping it.
		server, _ := newRecordingAPIServer(t, http.StatusInternalServerError, `{"kind":"Status","code":500}`)
		client := webhooks.NewLeaseRESTClientForTest(server.URL, namespace, "token-value")

		// when
		_, err := client.Create(context.Background(), &webhooks.Lease{
			Metadata: webhooks.LeaseMeta{Name: "code-guru-abc"},
		})

		// then
		require.Error(t, err)
		assert.False(t, webhooks.IsAlreadyExistsForTest(err))
		assert.False(t, webhooks.IsNotFoundForTest(err))
	})
}

func TestMicroTimeWireFormat(t *testing.T) {
	t.Parallel()

	t.Run("should marshal with microsecond precision the API server accepts", func(t *testing.T) {
		t.Parallel()

		// given: Go's default time marshalling emits nanoseconds, which
		// the API server rejects for a MicroTime field — every Create
		// would fail with Invalid.
		value := webhooks.MicroTime{Time: time.Date(2026, 9, 13, 10, 0, 0, 123456789, time.UTC)}

		// when
		encoded, err := json.Marshal(value)

		// then
		require.NoError(t, err)
		assert.Equal(t, `"2026-09-13T10:00:00.123456Z"`, string(encoded))
	})

	t.Run("should round-trip a timestamp through encode and decode", func(t *testing.T) {
		t.Parallel()

		// given
		original := webhooks.MicroTime{Time: time.Date(2026, 9, 13, 10, 0, 30, 500000000, time.UTC)}

		// when
		encoded, err := json.Marshal(original)
		require.NoError(t, err)
		var decoded webhooks.MicroTime
		err = json.Unmarshal(encoded, &decoded)

		// then
		require.NoError(t, err)
		assert.True(t, original.Equal(decoded.Time), "a lease's renewTime must survive the round trip intact")
	})

	t.Run("should decode a null timestamp as the zero value", func(t *testing.T) {
		t.Parallel()

		// given: leaseExpired treats a zero time as "expired", which is
		// how a malformed lease becomes recoverable instead of blocking
		// the key forever.
		var decoded webhooks.MicroTime

		// when
		err := json.Unmarshal([]byte("null"), &decoded)

		// then
		require.NoError(t, err)
		assert.True(t, decoded.IsZero())
	})
}

func TestServiceAccountTokenSource(t *testing.T) {
	t.Parallel()

	t.Run("should read the token from disk and trim the trailing newline", func(t *testing.T) {
		t.Parallel()

		// given
		path := filepath.Join(t.TempDir(), "token")
		require.NoError(t, os.WriteFile(path, []byte("projected-token\n"), 0o600))
		read := webhooks.TokenSourceForTest(path)

		// when
		token, err := read()

		// then
		require.NoError(t, err)
		assert.Equal(t, "projected-token", token, "a trailing newline would corrupt the Authorization header")
	})

	t.Run("should keep serving the last good token when a later read fails", func(t *testing.T) {
		t.Parallel()

		// given: projected tokens rotate hourly and the kubelet rewrites
		// the file in place. A transient read failure must not turn into
		// an authentication outage on the webhook hot path.
		path := filepath.Join(t.TempDir(), "token")
		require.NoError(t, os.WriteFile(path, []byte("projected-token"), 0o600))
		read := webhooks.TokenSourceForTest(path)
		first, err := read()
		require.NoError(t, err)
		require.NoError(t, os.Remove(path))

		// when
		second, err := read()

		// then
		require.NoError(t, err, "a vanished token file must not fail the call while a cached token is usable")
		assert.Equal(t, first, second)
	})

	t.Run("should report an error when no token has ever been read", func(t *testing.T) {
		t.Parallel()

		// given
		read := webhooks.TokenSourceForTest(filepath.Join(t.TempDir(), "absent"))

		// when
		_, err := read()

		// then
		require.Error(t, err, "with no cached token there is nothing to authenticate with — the caller must know")
		assert.Contains(t, err.Error(), "service account token")
	})
}
