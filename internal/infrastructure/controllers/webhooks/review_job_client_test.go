package webhooks_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/rios0rios0/codeguru/internal/infrastructure/controllers/webhooks"
)

// recordedJobRequest is one request the fake API server received.
type recordedJobRequest struct {
	method      string
	path        string
	query       string
	auth        string
	contentType string
	body        map[string]any
}

// fakeJobAPI is an `httptest` stand-in for the API server's Job endpoints: it
// records each request and answers with the configured status and body.
type fakeJobAPI struct {
	mu       sync.Mutex
	requests []recordedJobRequest
	status   int
	response string
}

func (f *fakeJobAPI) handler(w http.ResponseWriter, r *http.Request) {
	data, _ := io.ReadAll(r.Body)
	var body map[string]any
	_ = json.Unmarshal(data, &body)
	f.mu.Lock()
	f.requests = append(f.requests, recordedJobRequest{
		method: r.Method, path: r.URL.Path, query: r.URL.RawQuery,
		auth: r.Header.Get("Authorization"), contentType: r.Header.Get("Content-Type"), body: body,
	})
	status, response := f.status, f.response
	f.mu.Unlock()
	if status == 0 {
		status = http.StatusOK
	}
	w.WriteHeader(status)
	_, _ = io.WriteString(w, response)
}

func (f *fakeJobAPI) last() recordedJobRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.requests[len(f.requests)-1]
}

func newFakeJobAPI(t *testing.T, api *fakeJobAPI) webhooks.ReviewJobClient {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(api.handler))
	t.Cleanup(server.Close)
	return webhooks.NewJobRESTClientForTest(server.URL, "code guru", "fixture-token-placeholder")
}

func TestJobRESTClient(t *testing.T) {
	t.Parallel()

	t.Run("should POST the manifest to the namespaced collection with the bearer token", func(t *testing.T) {
		t.Parallel()

		// given
		api := &fakeJobAPI{status: http.StatusCreated, response: `{"metadata":{"name":"j1","resourceVersion":"7"}}`}
		client := newFakeJobAPI(t, api)

		// when
		job, err := client.Create(context.Background(), map[string]any{"metadata": map[string]any{"name": "j1"}})

		// then
		require.NoError(t, err)
		assert.Equal(t, "7", job.Metadata.ResourceVersion)
		request := api.last()
		assert.Equal(t, http.MethodPost, request.method)
		assert.Equal(t, "/apis/batch/v1/namespaces/code guru/jobs", request.path, "the namespace is path-escaped")
		assert.Equal(t, "Bearer fixture-token-placeholder", request.auth)
		assert.Equal(t, "application/json", request.contentType)
	})

	t.Run("should list with the label selector query-escaped", func(t *testing.T) {
		t.Parallel()

		// given
		api := &fakeJobAPI{
			response: `{"items":[{"metadata":{"name":"a"},"spec":{"suspend":true}},{"metadata":{"name":"b"}}]}`,
		}
		client := newFakeJobAPI(t, api)

		// when
		jobs, err := client.List(context.Background(), "code-guru/review-job=true,code-guru/pull-request=abc")

		// then
		require.NoError(t, err)
		require.Len(t, jobs, 2)
		assert.True(t, jobs[0].Suspended())
		assert.False(t, jobs[1].Suspended())
		assert.Equal(
			t,
			"labelSelector=code-guru%2Freview-job%3Dtrue%2Ccode-guru%2Fpull-request%3Dabc",
			api.last().query,
		)
	})

	t.Run("should send a patch as an RFC 7386 merge patch", func(t *testing.T) {
		t.Parallel()

		// given
		api := &fakeJobAPI{response: `{}`}
		client := newFakeJobAPI(t, api)

		// when
		err := client.Patch(context.Background(), "j1", map[string]any{"spec": map[string]any{"suspend": false}})

		// then
		require.NoError(t, err)
		request := api.last()
		assert.Equal(t, http.MethodPatch, request.method)
		assert.Equal(t, "/apis/batch/v1/namespaces/code guru/jobs/j1", request.path)
		assert.Equal(t, "application/merge-patch+json", request.contentType)
		assert.Equal(t, map[string]any{"spec": map[string]any{"suspend": false}}, request.body)
	})

	t.Run("should classify a stale resourceVersion as Conflict", func(t *testing.T) {
		t.Parallel()

		// given: the API server's answer to a conditional patch that lost.
		api := &fakeJobAPI{
			status:   http.StatusConflict,
			response: `{"kind":"Status","reason":"Conflict","message":"the object has been modified"}`,
		}
		client := newFakeJobAPI(t, api)

		// when
		err := client.Patch(context.Background(), "j1", map[string]any{})

		// then
		require.Error(t, err)
		assert.True(t, webhooks.IsConflictForTest(err))
		assert.False(t, webhooks.IsAlreadyExistsForTest(err), "Conflict and AlreadyExists share 409 but not meaning")
	})
}

func TestReviewJobConditions(t *testing.T) {
	t.Parallel()

	t.Run("should read a failed Job's reason and treat Complete and Failed as finished", func(t *testing.T) {
		t.Parallel()

		// given
		failed := webhooks.ReviewJob{Status: webhooks.ReviewJobStatus{Conditions: []webhooks.ReviewJobCondition{
			{Type: "Failed", Status: "True", Reason: "DeadlineExceeded"},
		}}}
		complete := webhooks.ReviewJob{Status: webhooks.ReviewJobStatus{Conditions: []webhooks.ReviewJobCondition{
			{Type: "Complete", Status: "True"},
		}}}
		pending := webhooks.ReviewJob{Status: webhooks.ReviewJobStatus{Conditions: []webhooks.ReviewJobCondition{
			{Type: "Failed", Status: "False"},
		}}}

		// when
		reason, isFailed := failed.FailureReason()
		_, completeFailed := complete.FailureReason()

		// then
		assert.True(t, isFailed)
		assert.Equal(t, "DeadlineExceeded", reason)
		assert.False(t, completeFailed)
		assert.True(t, failed.Finished())
		assert.True(t, complete.Finished())
		assert.False(t, pending.Finished(), "a condition that is not True does not count")
	})
}
