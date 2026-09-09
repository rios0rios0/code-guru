package webhooks_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/rios0rios0/codeguru/internal/infrastructure/controllers/webhooks"
)

// adoSelfIdentityID is the identity id the fake connectionData endpoint
// reports for the PAT. Shaped like a real Azure DevOps identity GUID so
// the resolver's normalisation and the handler's comparison are both
// exercised on a realistic value.
const adoSelfIdentityID = "8f3a1e2b-4c5d-6e7f-8a9b-0c1d2e3f4a5b"

// connectionDataServer stands up a real HTTP server that answers
// `_apis/connectionData` the way Azure DevOps does, recording every
// request so the caching contract can be asserted on the call count.
// A real server (not a transport double) keeps the test honest about
// URL construction, auth header shape, and status handling.
func connectionDataServer(t *testing.T, body string) (*httptest.Server, *atomic.Int32, *atomic.Value) {
	t.Helper()
	calls := &atomic.Int32{}
	lastAuth := &atomic.Value{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		lastAuth.Store(r.Header.Get("Authorization"))
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprint(w, body)
	}))
	t.Cleanup(server.Close)
	return server, calls, lastAuth
}

func connectionDataBody(id string) string {
	return fmt.Sprintf(
		`{"authenticatedUser":{"id":%q,"providerDisplayName":"svc-codeguru"},"deploymentType":"hosted"}`,
		id,
	)
}

func TestHTTPADOIdentityResolver(t *testing.T) {
	t.Parallel()

	t.Run("should return the authenticated user id when connectionData answers", func(t *testing.T) {
		t.Parallel()

		// given
		server, _, lastAuth := connectionDataServer(t, connectionDataBody(adoSelfIdentityID))
		resolver := webhooks.NewTestHTTPADOIdentityResolver(server.Client(), server.URL)

		// when
		id, err := resolver.ResolveSelfID(context.Background(), adoOrgSlug, "ado-pat-test")

		// then
		require.NoError(t, err)
		assert.Equal(t, adoSelfIdentityID, id)
		assert.Equal(t, "Basic OmFkby1wYXQtdGVzdA==", lastAuth.Load(),
			"the PAT must be sent as Basic auth with an empty username, the documented ADO scheme")
	})

	t.Run("should lower-case the id so it compares equal to the comment box's markup", func(t *testing.T) {
		t.Parallel()

		// given
		upper := "8F3A1E2B-4C5D-6E7F-8A9B-0C1D2E3F4A5B"
		server, _, _ := connectionDataServer(t, connectionDataBody(upper))
		resolver := webhooks.NewTestHTTPADOIdentityResolver(server.Client(), server.URL)

		// when
		id, err := resolver.ResolveSelfID(context.Background(), adoOrgSlug, "ado-pat-test")

		// then
		require.NoError(t, err)
		assert.Equal(t, adoSelfIdentityID, id)
	})

	t.Run("should resolve once per organization and serve later calls from cache", func(t *testing.T) {
		t.Parallel()

		// given
		server, calls, _ := connectionDataServer(t, connectionDataBody(adoSelfIdentityID))
		resolver := webhooks.NewTestHTTPADOIdentityResolver(server.Client(), server.URL)

		// when
		first, firstErr := resolver.ResolveSelfID(context.Background(), adoOrgSlug, "ado-pat-test")
		second, secondErr := resolver.ResolveSelfID(context.Background(), adoOrgSlug, "ado-pat-test")

		// then
		require.NoError(t, firstErr)
		require.NoError(t, secondErr)
		assert.Equal(t, first, second)
		assert.Equal(
			t,
			int32(1),
			calls.Load(),
			"the identity behind a PAT does not change; a second lookup would be a wasted round-trip on the webhook path",
		)
	})

	t.Run("should not cache a failure so a transient outage does not pin the bot as unidentified", func(t *testing.T) {
		t.Parallel()

		// given
		var status atomic.Int32
		status.Store(http.StatusInternalServerError)
		calls := &atomic.Int32{}
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			calls.Add(1)
			code := int(status.Load())
			w.WriteHeader(code)
			if code == http.StatusOK {
				_, _ = fmt.Fprint(w, connectionDataBody(adoSelfIdentityID))
			}
		}))
		t.Cleanup(server.Close)
		resolver := webhooks.NewTestHTTPADOIdentityResolver(server.Client(), server.URL)

		// when
		_, firstErr := resolver.ResolveSelfID(context.Background(), adoOrgSlug, "ado-pat-test")
		status.Store(http.StatusOK)
		id, secondErr := resolver.ResolveSelfID(context.Background(), adoOrgSlug, "ado-pat-test")

		// then
		require.Error(t, firstErr)
		require.NoError(t, secondErr)
		assert.Equal(t, adoSelfIdentityID, id)
		assert.Equal(t, int32(2), calls.Load())
	})

	t.Run("should return an error when connectionData carries no authenticated user", func(t *testing.T) {
		t.Parallel()

		// given
		server, _, _ := connectionDataServer(t, `{"deploymentType":"hosted"}`)
		resolver := webhooks.NewTestHTTPADOIdentityResolver(server.Client(), server.URL)

		// when
		id, err := resolver.ResolveSelfID(context.Background(), adoOrgSlug, "ado-pat-test")

		// then
		require.Error(t, err)
		assert.Empty(t, id)
	})

	t.Run("should return an error when the authenticated user id is not a canonical GUID", func(t *testing.T) {
		t.Parallel()

		// given
		server, _, _ := connectionDataServer(t, connectionDataBody("not-an-identity"))
		resolver := webhooks.NewTestHTTPADOIdentityResolver(server.Client(), server.URL)

		// when
		id, err := resolver.ResolveSelfID(context.Background(), adoOrgSlug, "ado-pat-test")

		// then
		require.Error(t, err)
		assert.Empty(
			t,
			id,
			"the id is compared against comment content and written to logs, so a non-GUID response is rejected outright",
		)
	})

	t.Run("should return an error when the response body is not JSON", func(t *testing.T) {
		t.Parallel()

		// given
		server, _, _ := connectionDataServer(t, "<html>sign in</html>")
		resolver := webhooks.NewTestHTTPADOIdentityResolver(server.Client(), server.URL)

		// when
		_, err := resolver.ResolveSelfID(context.Background(), adoOrgSlug, "ado-pat-test")

		// then
		require.Error(t, err)
	})

	t.Run("should refuse to call out for a malformed organization slug", func(t *testing.T) {
		t.Parallel()

		// given
		server, calls, _ := connectionDataServer(t, connectionDataBody(adoSelfIdentityID))
		resolver := webhooks.NewTestHTTPADOIdentityResolver(server.Client(), server.URL)

		// when
		_, err := resolver.ResolveSelfID(context.Background(), "../../evil.example.com", "ado-pat-test")

		// then
		require.Error(t, err)
		assert.Equal(t, int32(0), calls.Load(),
			"the org slug is the only caller-supplied part of the endpoint and comes from a webhook body")
	})

	t.Run("should refuse to call out when the organization or token is missing", func(t *testing.T) {
		t.Parallel()

		// given
		server, calls, _ := connectionDataServer(t, connectionDataBody(adoSelfIdentityID))
		resolver := webhooks.NewTestHTTPADOIdentityResolver(server.Client(), server.URL)

		// when
		_, noOrgErr := resolver.ResolveSelfID(context.Background(), "", "ado-pat-test")
		_, noTokenErr := resolver.ResolveSelfID(context.Background(), adoOrgSlug, "")

		// then
		require.Error(t, noOrgErr)
		require.Error(t, noTokenErr)
		assert.Equal(t, int32(0), calls.Load(), "an incomplete lookup must never reach the network")
	})
}
