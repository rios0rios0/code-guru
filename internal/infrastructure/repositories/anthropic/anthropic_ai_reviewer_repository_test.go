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

func newAnthropicStub(t *testing.T, status int, payload map[string]any) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(payload)
	}))
}
