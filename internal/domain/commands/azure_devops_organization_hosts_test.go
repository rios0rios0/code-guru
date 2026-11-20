package commands_test

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/rios0rios0/gitforge/v4/pkg/providers/infrastructure/azuredevops"
	registry "github.com/rios0rios0/gitforge/v4/pkg/registry/infrastructure"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/rios0rios0/codeguru/internal/domain/commands"
	"github.com/rios0rios0/codeguru/internal/domain/entities"
	doubles "github.com/rios0rios0/codeguru/test/domain/doubles/repositories"
)

// foreignHost is a TLS server standing in for a host that is not Azure
// DevOps, such as one a cloned repository's `.code-guru.yaml` names. It
// records every request that reaches it, with its Authorization header.
type foreignHost struct {
	server *httptest.Server

	mu       sync.Mutex
	requests []string
}

func newForeignHost(t *testing.T) *foreignHost {
	t.Helper()
	host := &foreignHost{}
	host.server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host.mu.Lock()
		host.requests = append(host.requests, r.URL.Path+" Authorization="+r.Header.Get("Authorization"))
		host.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"count":0,"value":[]}`))
	}))
	t.Cleanup(host.server.Close)

	// The gitforge Azure DevOps provider has no transport seam: its client
	// uses http.DefaultTransport. Trusting the test server there is what
	// lets a request reach it at all, the way a real host's would.
	original := http.DefaultTransport
	http.DefaultTransport = host.server.Client().Transport //nolint:reassign // see above; restored on cleanup
	t.Cleanup(func() { http.DefaultTransport = original }) //nolint:reassign // restores the original transport

	return host
}

func (h *foreignHost) organization() string {
	return h.server.URL + "/attacker-org"
}

func (h *foreignHost) received() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]string(nil), h.requests...)
}

// azureDevOpsRegistry registers the real gitforge Azure DevOps provider, so
// the test exercises the URL it builds from an `organizations` entry.
func azureDevOpsRegistry() *registry.ProviderRegistry {
	providers := registry.NewProviderRegistry()
	providers.RegisterFactory("azuredevops", azuredevops.NewProvider)
	return providers
}

// TestAzureDevOpsOrganizationOnAnotherHost is the regression test for an
// `organizations` entry that is an https:// URL of another host: gitforge
// used it as the base URL of its requests, so the token, configured or the
// runner's own, went to that host.
//
//nolint:paralleltest // swaps http.DefaultTransport, which every HTTP client of the test binary shares
func TestAzureDevOpsOrganizationOnAnotherHost(t *testing.T) {
	t.Run("should refuse the organization in discover before the fallback is asked for a token", func(t *testing.T) {
		// given
		host := newForeignHost(t)
		fallback := &doubles.StubFallbackTokenRepository{Token: "fixture-runner-token-placeholder"}
		settings := &entities.Settings{Providers: []entities.ProviderConfig{
			{Type: "azuredevops", Organizations: []string{host.organization()}},
		}}

		// when
		results, err := commands.NewDiscoverCommand(azureDevOpsRegistry(), fallback).Execute(t.Context(), settings)

		// then
		require.NoError(t, err)
		assert.Empty(t, results)
		assert.Empty(
			t,
			fallback.Requests,
			"the runner's token must never be looked up for a host that is not Azure DevOps",
		)
		assert.Empty(t, host.received(), "no request may reach a host that is not Azure DevOps")
	})

	t.Run("should refuse the organization in review-all before the fallback is asked for a token", func(t *testing.T) {
		// given
		host := newForeignHost(t)
		fallback := &doubles.StubFallbackTokenRepository{Token: "fixture-runner-token-placeholder"}
		settings := &entities.Settings{Providers: []entities.ProviderConfig{
			{Type: "azuredevops", Organizations: []string{host.organization()}},
		}}

		// when
		_, err := commands.NewReviewAllCommand(azureDevOpsRegistry(), nil, fallback).
			Execute(t.Context(), settings, commands.ReviewOptions{})

		// then
		require.Error(t, err, "a refused organization counts as an error of the batch")
		assert.Empty(t, fallback.Requests)
		assert.Empty(t, host.received())
	})

	t.Run("should not send a configured token to a host that is not Azure DevOps", func(t *testing.T) {
		// given: the token a working-directory config reads from a file path
		// or an environment variable.
		host := newForeignHost(t)
		settings := &entities.Settings{Providers: []entities.ProviderConfig{{
			Type:          "azuredevops",
			Token:         "fixture-configured-token-placeholder",
			Organizations: []string{host.organization()},
		}}}

		// when
		results, err := commands.NewDiscoverCommand(azureDevOpsRegistry(), nil).Execute(t.Context(), settings)

		// then
		require.NoError(t, err)
		assert.Empty(t, results)
		assert.Empty(t, host.received())
	})
}
