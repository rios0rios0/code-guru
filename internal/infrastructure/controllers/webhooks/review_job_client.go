package webhooks

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// This file is the `batch/v1` Job adapter behind the Kubernetes review
// executor, built on the same `net/http` core as the Lease adapter (see
// `kubernetes_api.go`). It needs three verbs — create, list, merge-patch — on
// one namespaced resource.

const (
	// jobAPIPathFormat is the namespaced collection endpoint for `batch/v1`
	// Jobs.
	jobAPIPathFormat = "%s/apis/batch/v1/namespaces/%s/jobs"

	// maxJobResponseBytes caps a Job API response. A review Job, with its
	// rendered pod template, is a few kilobytes; a list of a few hundred
	// of them stays well under this.
	maxJobResponseBytes = 16 << 20

	jobConditionComplete = "Complete"
	jobConditionFailed   = "Failed"
	conditionStatusTrue  = "True"
)

// ReviewJob mirrors the subset of a `batch/v1` Job the review executor reads.
type ReviewJob struct {
	Metadata ReviewJobMetadata `json:"metadata"`
	Spec     ReviewJobSpec     `json:"spec"`
	Status   ReviewJobStatus   `json:"status"`
}

// ReviewJobMetadata is the slice of `metav1.ObjectMeta` the executor needs.
// `ResourceVersion` backs every write's optimistic concurrency: the scheduler
// echoes it on each patch, so two server replicas acting on the same listing
// cannot both start the same Job or both report the same failure.
type ReviewJobMetadata struct {
	Name              string            `json:"name"`
	ResourceVersion   string            `json:"resourceVersion,omitempty"`
	CreationTimestamp time.Time         `json:"creationTimestamp"`
	Labels            map[string]string `json:"labels,omitempty"`
	Annotations       map[string]string `json:"annotations,omitempty"`
}

// ReviewJobSpec carries the one spec field the scheduler drives.
type ReviewJobSpec struct {
	Suspend *bool `json:"suspend,omitempty"`
}

// ReviewJobStatus carries the status fields the scheduler classifies on.
type ReviewJobStatus struct {
	StartTime  *time.Time           `json:"startTime,omitempty"`
	Conditions []ReviewJobCondition `json:"conditions,omitempty"`
}

// ReviewJobCondition is one entry of `JobStatus.conditions`.
type ReviewJobCondition struct {
	Type    string `json:"type"`
	Status  string `json:"status"`
	Reason  string `json:"reason,omitempty"`
	Message string `json:"message,omitempty"`
}

// Suspended reports whether the Job is held back by `spec.suspend` — a
// queued review that has not been released yet.
func (j ReviewJob) Suspended() bool {
	return j.Spec.Suspend != nil && *j.Spec.Suspend
}

// Finished reports whether the Job reached a terminal condition.
func (j ReviewJob) Finished() bool {
	return j.condition(jobConditionComplete) != nil || j.condition(jobConditionFailed) != nil
}

// FailureReason returns the reason of a true `Failed` condition (for example
// `DeadlineExceeded` or `BackoffLimitExceeded`) and whether there is one.
func (j ReviewJob) FailureReason() (string, bool) {
	failed := j.condition(jobConditionFailed)
	if failed == nil {
		return "", false
	}
	if failed.Reason == "" {
		return jobConditionFailed, true
	}
	return failed.Reason, true
}

func (j ReviewJob) condition(kind string) *ReviewJobCondition {
	for i := range j.Status.Conditions {
		c := &j.Status.Conditions[i]
		if c.Type == kind && c.Status == conditionStatusTrue {
			return c
		}
	}
	return nil
}

// ReviewJobClient is the Job API surface the executor consumes. The REST
// adapter below implements it against the API server; tests substitute an
// in-memory double.
type ReviewJobClient interface {
	// Create submits a rendered Job manifest and returns the created
	// object.
	Create(ctx context.Context, manifest map[string]any) (*ReviewJob, error)

	// List returns the Jobs matching an equality-based label selector
	// (`key=value,key2=value2`).
	List(ctx context.Context, labelSelector string) ([]ReviewJob, error)

	// Patch applies an RFC 7386 merge patch to the named Job. A patch that
	// carries `metadata.resourceVersion` is conditional: it fails with a
	// Conflict error (see isConflict) when the Job changed since that
	// version was read.
	Patch(ctx context.Context, name string, patch any) error
}

// jobRESTClient is the ReviewJobClient that talks to the real API server.
type jobRESTClient struct {
	collectionURL string
	httpClient    *http.Client
	token         *tokenSource
}

// newInClusterJobClient builds a Job client from the credentials the kubelet
// projects into every pod.
func newInClusterJobClient(namespace string) (*jobRESTClient, error) {
	base, client, token, err := newInClusterTransport()
	if err != nil {
		return nil, err
	}
	return newJobRESTClient(base, namespace, client, token), nil
}

// newJobRESTClient assembles a client against an arbitrary base URL so tests
// can point it at an `httptest` server.
func newJobRESTClient(baseURL, namespace string, client *http.Client, token *tokenSource) *jobRESTClient {
	return &jobRESTClient{
		collectionURL: fmt.Sprintf(jobAPIPathFormat, strings.TrimSuffix(baseURL, "/"), url.PathEscape(namespace)),
		httpClient:    client,
		token:         token,
	}
}

func (c *jobRESTClient) Create(ctx context.Context, manifest map[string]any) (*ReviewJob, error) {
	data, err := c.do(ctx, kubernetesRequest{method: http.MethodPost, endpoint: c.collectionURL, payload: manifest})
	if err != nil {
		return nil, err
	}
	var job ReviewJob
	if err = json.Unmarshal(data, &job); err != nil {
		return nil, fmt.Errorf("kubernetes: decode created job: %w", err)
	}
	return &job, nil
}

func (c *jobRESTClient) List(ctx context.Context, labelSelector string) ([]ReviewJob, error) {
	endpoint := c.collectionURL + "?labelSelector=" + url.QueryEscape(labelSelector)
	data, err := c.do(ctx, kubernetesRequest{method: http.MethodGet, endpoint: endpoint})
	if err != nil {
		return nil, err
	}
	var list struct {
		Items []ReviewJob `json:"items"`
	}
	if err = json.Unmarshal(data, &list); err != nil {
		return nil, fmt.Errorf("kubernetes: decode job list: %w", err)
	}
	return list.Items, nil
}

func (c *jobRESTClient) Patch(ctx context.Context, name string, patch any) error {
	_, err := c.do(ctx, kubernetesRequest{
		method:      http.MethodPatch,
		endpoint:    c.collectionURL + "/" + url.PathEscape(name),
		payload:     patch,
		contentType: contentTypeMergePatch,
	})
	return err
}

func (c *jobRESTClient) do(ctx context.Context, call kubernetesRequest) ([]byte, error) {
	call.maxBody = maxJobResponseBytes
	return doKubernetesRequest(ctx, c.httpClient, c.token, call)
}
