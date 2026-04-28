//go:build unit

package controllers_test

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/rios0rios0/codeguru/internal/infrastructure/controllers"
)

func TestHealthCheckController(t *testing.T) {
	t.Parallel()

	t.Run("should return nil when the endpoint responds 200", func(t *testing.T) {
		t.Parallel()

		// given
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
		}))
		defer server.Close()
		ctrl := controllers.NewHealthCheckController()

		// when
		err := ctrl.Probe(server.URL+"/health", 1*time.Second)

		// then
		assert.NoError(t, err)
	})

	t.Run("should return an error when the endpoint responds 503", func(t *testing.T) {
		t.Parallel()

		// given
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusServiceUnavailable)
		}))
		defer server.Close()
		ctrl := controllers.NewHealthCheckController()

		// when
		err := ctrl.Probe(server.URL+"/health", 1*time.Second)

		// then
		require.Error(t, err)
		assert.Contains(t, err.Error(), "status")
	})

	t.Run("should return an error when the endpoint is unreachable", func(t *testing.T) {
		t.Parallel()

		// given a port that is virtually guaranteed to be closed
		ctrl := controllers.NewHealthCheckController()

		// when
		err := ctrl.Probe("http://127.0.0.1:1/health", 100*time.Millisecond)

		// then
		require.Error(t, err)
	})

	t.Run("should return an error when the URL is malformed", func(t *testing.T) {
		t.Parallel()

		// given
		ctrl := controllers.NewHealthCheckController()

		// when
		err := ctrl.Probe("://not a url", 1*time.Second)

		// then
		require.Error(t, err)
	})
}
