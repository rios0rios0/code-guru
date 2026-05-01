//go:build unit

package webhooks_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/rios0rios0/codeguru/internal/infrastructure/controllers/webhooks"
)

// These tests pin the hydration contract that compensates for the
// stripped-down `resource` block ADO **org-wide** subscriptions emit
// (only `{ url, pullRequestId }`, regardless of `resourceVersion`).
// Each scenario maps to a wire shape we have observed in production
// against subscriptions `fea3e13f-…` and `564b23d9-…`, so any future
// "let me clean up the hydrator" refactor must keep these green.

func TestIsSkinnyADOResource(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		resource webhooks.ADOResource
		want     bool
	}{
		{
			name: "should detect the org-wide skinny shape (only url + pullRequestId)",
			resource: webhooks.ADOResource{
				PullRequestID: NNNN,
				URL:           "https://dev.azure.com/ExampleOrg/project-uuid-B/_apis/git/repositories/project-uuid-C/pullRequests/NNNN",
			},
			want: true,
		},
		{
			name: "should NOT flag a hydrated/full resource (repository.id present)",
			resource: webhooks.ADOResource{
				PullRequestID: NNNN,
				URL:           "https://dev.azure.com/ExampleOrg/_apis/.../pullRequests/NNNN",
				Repository:    webhooks.ADORepository{ID: "project-uuid-C"},
			},
			want: false,
		},
		{
			name:     "should NOT flag a fully empty resource (no url, no id)",
			resource: webhooks.ADOResource{},
			want:     false,
		},
		{
			name: "should NOT flag a payload missing pullRequestId (defensive — webhook envelope without an id is malformed)",
			resource: webhooks.ADOResource{
				URL: "https://dev.azure.com/ExampleOrg/_apis/.../pullRequests/0",
			},
			want: false,
		},
		{
			name: "should NOT flag a payload whose url is whitespace-only",
			resource: webhooks.ADOResource{
				PullRequestID: NNNN,
				URL:           "   ",
			},
			want: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// when
			got := webhooks.IsSkinnyADOResource(tc.resource)

			// then
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestAppendAPIVersion(t *testing.T) {
	t.Parallel()

	t.Run("should append api-version on a URL without query", func(t *testing.T) {
		// given
		raw := "https://dev.azure.com/ExampleOrg/_apis/git/repositories/abc/pullRequests/1"

		// when
		got, err := webhooks.AppendAPIVersion(raw, "7.1-preview.1")

		// then
		require.NoError(t, err)
		assert.Equal(t, raw+"?api-version=7.1-preview.1", got)
	})

	t.Run("should override an existing api-version query param", func(t *testing.T) {
		// given
		raw := "https://dev.azure.com/ExampleOrg/_apis/git/pullRequests/1?api-version=5.0&foo=bar"

		// when
		got, err := webhooks.AppendAPIVersion(raw, "7.1-preview.1")

		// then
		require.NoError(t, err)
		assert.Contains(t, got, "api-version=7.1-preview.1")
		assert.Contains(t, got, "foo=bar")
		assert.NotContains(t, got, "api-version=5.0")
	})

	t.Run("should reject a relative URL (only an absolute one identifies the org)", func(t *testing.T) {
		// when
		got, err := webhooks.AppendAPIVersion("/_apis/git/pullRequests/1", "7.1-preview.1")

		// then
		require.Error(t, err)
		assert.Empty(t, got)
	})

	t.Run("should reject a URL with a control character that fails url.Parse", func(t *testing.T) {
		// when
		got, err := webhooks.AppendAPIVersion("https://dev.azure.com/\x7f", "7.1-preview.1")

		// then
		require.Error(t, err)
		assert.Empty(t, got)
	})
}

func TestMergeHydratedADOResource(t *testing.T) {
	t.Parallel()

	t.Run("should prefer hydrated fields when both sides supply them", func(t *testing.T) {
		// given
		original := webhooks.ADOResource{PullRequestID: NNNN, URL: "https://orig"}
		hydrated := webhooks.ADOResource{
			PullRequestID: NNNN,
			URL:           "https://hydrated",
			Status:        "active",
			Title:         "smoke",
			SourceRefName: "refs/heads/feat/x",
			TargetRefName: "refs/heads/main",
		}
		hydrated.Repository.ID = "e3555597"
		hydrated.Repository.Name = "catalog"
		hydrated.Repository.RemoteURL = "https://dev.azure.com/Org/Project/_git/catalog"
		hydrated.Repository.Project.Name = "backend"

		// when
		merged := webhooks.MergeHydratedADOResource(original, hydrated)

		// then
		assert.Equal(t, "https://hydrated", merged.URL)
		assert.Equal(t, "active", merged.Status)
		assert.Equal(t, "catalog", merged.Repository.Name)
		assert.Equal(t, "backend", merged.Repository.Project.Name)
	})

	t.Run("should fall back to original pullRequestId when hydrated body omitted it", func(t *testing.T) {
		// given
		original := webhooks.ADOResource{PullRequestID: NNNN, URL: "https://orig"}
		hydrated := webhooks.ADOResource{Status: "active"}
		hydrated.Repository.ID = "e3555597"

		// when
		merged := webhooks.MergeHydratedADOResource(original, hydrated)

		// then
		assert.Equal(t, NNNN, merged.PullRequestID)
		assert.Equal(t, "https://orig", merged.URL)
		assert.Equal(t, "e3555597", merged.Repository.ID)
	})
}

// Hydrate end-to-end coverage: stand up a fake ADO API and verify the
// hydrator reaches it with the right Authorization header, parses the
// response, and surfaces non-2xx as an error.

func TestHTTPADOHydrator(t *testing.T) {
	t.Parallel()

	t.Run("should fetch and decode a full PR resource", func(t *testing.T) {
		// given
		var receivedAuth string
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			receivedAuth = r.Header.Get("Authorization")
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{
				"pullRequestId": NNNN,
				"status": "active",
				"title": "smoke",
				"sourceRefName": "refs/heads/chore/smoke-test-6",
				"targetRefName": "refs/heads/main",
				"url": "https://dev.azure.com/ExampleOrg/_apis/git/repositories/abc/pullRequests/NNNN",
				"repository": {
					"id": "project-uuid-C",
					"name": "catalog",
					"remoteUrl": "https://dev.azure.com/ExampleOrg/backend/_git/catalog",
					"project": { "name": "backend" }
				}
			}`))
		}))
		defer server.Close()

		hydrator := webhooks.NewHTTPADOHydrator(&http.Client{Timeout: 2 * time.Second})

		// when
		got, err := hydrator.Hydrate(context.Background(), server.URL+"/_apis/git/pullRequests/NNNN", "test-pat")

		// then
		require.NoError(t, err)
		assert.Equal(t, NNNN, got.PullRequestID)
		assert.Equal(t, "active", got.Status)
		assert.Equal(t, "catalog", got.Repository.Name)
		assert.Equal(t, "backend", got.Repository.Project.Name)
		assert.Equal(t, "refs/heads/chore/smoke-test-6", got.SourceRefName)
		assert.NotEmpty(t, receivedAuth)
		assert.Contains(t, receivedAuth, "Basic ")
	})

	t.Run("should surface a non-2xx response as an error", func(t *testing.T) {
		// given
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "forbidden", http.StatusForbidden)
		}))
		defer server.Close()

		hydrator := webhooks.NewHTTPADOHydrator(&http.Client{Timeout: 2 * time.Second})

		// when
		_, err := hydrator.Hydrate(context.Background(), server.URL+"/_apis/git/pullRequests/1", "test-pat")

		// then
		require.Error(t, err)
		assert.Contains(t, err.Error(), "403")
	})

	t.Run("should reject an empty token", func(t *testing.T) {
		// given
		hydrator := webhooks.NewHTTPADOHydrator(nil)

		// when
		_, err := hydrator.Hydrate(context.Background(), "https://dev.azure.com/_apis/git/pullRequests/1", "")

		// then
		require.Error(t, err)
		assert.Contains(t, err.Error(), "PAT is empty")
	})

	t.Run("should reject an empty resource URL", func(t *testing.T) {
		// given
		hydrator := webhooks.NewHTTPADOHydrator(nil)

		// when
		_, err := hydrator.Hydrate(context.Background(), "", "test-pat")

		// then
		require.Error(t, err)
		assert.Contains(t, err.Error(), "URL is empty")
	})

	t.Run("should reject a malformed (relative) URL before issuing a request", func(t *testing.T) {
		// given
		hydrator := webhooks.NewHTTPADOHydrator(nil)

		// when
		_, err := hydrator.Hydrate(context.Background(), "/_apis/git/pullRequests/1", "test-pat")

		// then
		require.Error(t, err)
		assert.Contains(t, err.Error(), "malformed resource URL")
	})
}
