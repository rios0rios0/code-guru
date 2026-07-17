//go:build unit

package anthropic_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	forgeEntities "github.com/rios0rios0/gitforge/pkg/global/domain/entities"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/rios0rios0/codeguru/internal/domain/entities"
	"github.com/rios0rios0/codeguru/internal/infrastructure/repositories/anthropic"
	"github.com/rios0rios0/codeguru/internal/support"
)

func newRequest() entities.ReviewRequest {
	return entities.ReviewRequest{
		PullRequest: forgeEntities.PullRequestDetail{
			PullRequest:  forgeEntities.PullRequest{Title: "test"},
			SourceBranch: "feature",
			TargetBranch: "main",
		},
		Diffs: []entities.FileDiff{{Path: "a.go", Diff: "+x", Language: "go"}},
	}
}

func TestAIReviewerRepositoryName(t *testing.T) {
	t.Parallel()

	t.Run("should return anthropic as backend name", func(t *testing.T) {
		t.Parallel()
		// given
		repo := anthropic.NewAIReviewerRepository("k", "")

		// when
		name := repo.Name()

		// then
		assert.Equal(t, "anthropic", name)
	})
}

func TestReviewDiff(t *testing.T) {
	t.Parallel()

	t.Run("should return summary when API returns a single text block", func(t *testing.T) {
		t.Parallel()
		// given
		server := newAnthropicStub(t, http.StatusOK, map[string]any{
			"content": []map[string]any{
				{"type": "text", "text": `{"summary":"lgtm","comments":[]}`},
			},
		})
		defer server.Close()
		repo := anthropic.NewAIReviewerRepository("k", "m", anthropic.WithEndpoint(server.URL))

		// when
		result, err := repo.ReviewDiff(context.Background(), newRequest())

		// then
		require.NoError(t, err)
		assert.Equal(t, "lgtm", result.Summary)
	})

	t.Run("should concatenate multiple text blocks in order", func(t *testing.T) {
		t.Parallel()
		// given
		server := newAnthropicStub(t, http.StatusOK, map[string]any{
			"content": []map[string]any{
				{"type": "text", "text": `{"summary":"par`},
				{"type": "tool_use", "text": "ignored"},
				{"type": "text", "text": `t1+part2","comments":[]}`},
			},
		})
		defer server.Close()
		repo := anthropic.NewAIReviewerRepository("k", "m", anthropic.WithEndpoint(server.URL))

		// when
		result, err := repo.ReviewDiff(context.Background(), newRequest())

		// then
		require.NoError(t, err)
		assert.Equal(t, "part1+part2", result.Summary)
	})

	t.Run("should return error when content has no text blocks", func(t *testing.T) {
		t.Parallel()
		// given
		server := newAnthropicStub(t, http.StatusOK, map[string]any{
			"content": []map[string]any{
				{"type": "tool_use", "text": "nope"},
			},
		})
		defer server.Close()
		repo := anthropic.NewAIReviewerRepository("k", "m", anthropic.WithEndpoint(server.URL))

		// when
		result, err := repo.ReviewDiff(context.Background(), newRequest())

		// then
		require.Error(t, err)
		assert.Nil(t, result)
		assert.Contains(t, err.Error(), "no text content")
	})

	t.Run("should surface API error message when non-2xx response has JSON error payload", func(t *testing.T) {
		t.Parallel()
		// given
		server := newAnthropicStub(t, http.StatusUnauthorized, map[string]any{
			"error": map[string]any{
				"type":    "authentication_error",
				"message": "invalid x-api-key",
			},
		})
		defer server.Close()
		repo := anthropic.NewAIReviewerRepository("bad", "m", anthropic.WithEndpoint(server.URL))

		// when
		_, err := repo.ReviewDiff(context.Background(), newRequest())

		// then
		require.Error(t, err)
		assert.Contains(t, err.Error(), "authentication_error")
		assert.Contains(t, err.Error(), "invalid x-api-key")
	})

	t.Run("should truncate plain-text body in error when non-2xx response is not JSON", func(t *testing.T) {
		t.Parallel()
		// given
		body := strings.Repeat("A", 2000)
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = io.WriteString(w, body)
		}))
		defer server.Close()
		repo := anthropic.NewAIReviewerRepository("k", "m", anthropic.WithEndpoint(server.URL))

		// when
		_, err := repo.ReviewDiff(context.Background(), newRequest())

		// then
		require.Error(t, err)
		assert.Contains(t, err.Error(), "status 500")
		assert.Contains(t, err.Error(), "truncated")
		assert.Less(t, len(err.Error()), 1000)
	})

	t.Run("should send canonical headers and model/prompt in request body", func(t *testing.T) {
		t.Parallel()
		// given
		var capturedAPIKey, capturedVersion, capturedContentType, capturedModel string
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			capturedAPIKey = req.Header.Get("X-Api-Key")
			capturedVersion = req.Header.Get("Anthropic-Version")
			capturedContentType = req.Header.Get("Content-Type")

			var payload map[string]any
			_ = json.NewDecoder(req.Body).Decode(&payload)
			if m, ok := payload["model"].(string); ok {
				capturedModel = m
			}

			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"content":[{"type":"text","text":"{\"summary\":\"ok\",\"comments\":[]}"}]}`)
		}))
		defer server.Close()
		repo := anthropic.NewAIReviewerRepository("secret-key", "claude-custom", anthropic.WithEndpoint(server.URL))

		// when
		_, err := repo.ReviewDiff(context.Background(), newRequest())

		// then
		require.NoError(t, err)
		assert.Equal(t, "secret-key", capturedAPIKey)
		assert.Equal(t, "2023-06-01", capturedVersion)
		assert.Equal(t, "application/json", capturedContentType)
		assert.Equal(t, "claude-custom", capturedModel)
	})
}

func TestReviewDiffContextWindow(t *testing.T) {
	t.Parallel()

	t.Run("should send the 1M context-window beta header when enabled", func(t *testing.T) {
		t.Parallel()
		// given / when
		beta := captureAnthropicBetaHeader(t, anthropic.WithContext1M(true))

		// then
		assert.Equal(t, "context-1m-2025-08-07", beta,
			"enabling the 1M window must send the context-1m beta so large PRs fit in one pass")
	})

	t.Run("should NOT send the beta header when the 1M window is disabled", func(t *testing.T) {
		t.Parallel()
		// given / when
		beta := captureAnthropicBetaHeader(t, anthropic.WithContext1M(false))

		// then
		assert.Empty(t, beta, "the beta header must be absent when the operator opts out")
	})

	t.Run("should NOT send the beta header by default (bare constructor)", func(t *testing.T) {
		t.Parallel()
		// given / when: no WithContext1M option — the factory is the single
		// place that resolves the default-ON toggle, so a bare backend is off.
		beta := captureAnthropicBetaHeader(t)

		// then
		assert.Empty(t, beta)
	})

	t.Run("should classify a 'prompt is too long' 400 as a context-window failure", func(t *testing.T) {
		t.Parallel()
		// given: the real Anthropic too-large 400 shape
		server := newAnthropicStub(t, http.StatusBadRequest, map[string]any{
			"error": map[string]any{
				"type":    "invalid_request_error",
				"message": "prompt is too long: 258000 tokens > 200000 maximum",
			},
		})
		defer server.Close()
		repo := anthropic.NewAIReviewerRepository("k", "m", anthropic.WithEndpoint(server.URL))

		// when
		_, err := repo.ReviewDiff(context.Background(), newRequest())

		// then
		require.Error(t, err)
		assert.ErrorIs(t, err, support.ErrContextWindowExceeded,
			"a prompt-too-long 400 must carry the sentinel so retries are skipped and the PR gets 'too large' guidance")
		assert.Contains(t, err.Error(), "prompt is too long",
			"the raw provider detail must remain in the (log-only) error for diagnosis")
	})
}

// captureAnthropicBetaHeader runs one successful review against a stub that
// records the outbound Anthropic-Beta header, returning whatever the backend
// sent (empty when it sent none). Keeps the three header-contract tests to a
// single line each.
func captureAnthropicBetaHeader(t *testing.T, opts ...anthropic.Option) string {
	t.Helper()
	var captured string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		captured = req.Header.Get("Anthropic-Beta")
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"content":[{"type":"text","text":"{\"summary\":\"ok\",\"comments\":[]}"}]}`)
	}))
	defer server.Close()
	repo := anthropic.NewAIReviewerRepository(
		"k", "m", append([]anthropic.Option{anthropic.WithEndpoint(server.URL)}, opts...)...,
	)
	_, err := repo.ReviewDiff(context.Background(), newRequest())
	require.NoError(t, err)

	return captured
}

func newAnthropicStub(t *testing.T, status int, payload map[string]any) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(payload)
	}))
}
