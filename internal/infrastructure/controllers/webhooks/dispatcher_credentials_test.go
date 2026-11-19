package webhooks_test

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestDispatcherIgnoresTheHostsCredentials pins the webhook server's half of
// the credential fallback the CLI commands have: the server acts only as the
// identity its operator configured, so a token the host's environment exports
// never stands in for a missing one. It sets process environment (t.Setenv),
// which rules out t.Parallel.
func TestDispatcherIgnoresTheHostsCredentials(t *testing.T) {
	t.Run("should respond 500 (Internal Server Error) when no GitHub token is configured, "+
		"even with GITHUB_TOKEN and GH_TOKEN set on the host", func(t *testing.T) {
		// given
		t.Setenv("GITHUB_TOKEN", "fixture-host-token-placeholder")
		t.Setenv("GH_TOKEN", "fixture-host-token-placeholder")
		settings := defaultGitHubSettings()
		settings.Providers = nil
		d, sub := newDispatcherWithSettings(t, settings)
		req := githubRequest(t, ghOpenedPayload, "pull_request")
		w := httptest.NewRecorder()

		// when
		d.HandleGitHub(w, req)

		// then
		assert.Equal(t, http.StatusInternalServerError, w.Code)
		assert.Empty(t, sub.Jobs())
	})

	t.Run("should respond 500 (Internal Server Error) when no Azure DevOps PAT is configured, "+
		"even with AZURE_DEVOPS_EXT_PAT and SYSTEM_ACCESSTOKEN set on the host", func(t *testing.T) {
		// given
		t.Setenv("AZURE_DEVOPS_EXT_PAT", "fixture-host-token-placeholder")
		t.Setenv("SYSTEM_ACCESSTOKEN", "fixture-host-token-placeholder")
		settings := defaultADOSettings()
		settings.Providers = nil
		d, sub := newDispatcherWithSettings(t, settings)
		req := httptest.NewRequest(
			http.MethodPost,
			"/webhooks/azuredevops",
			bytes.NewBufferString(adoActivePRPayload()),
		)
		req.Header.Set("Authorization", adoBasicAuth(adoSecret))
		w := httptest.NewRecorder()

		// when
		d.HandleAzureDevOps(w, req)

		// then
		assert.Equal(t, http.StatusInternalServerError, w.Code)
		assert.Empty(t, sub.Jobs())
	})
}
