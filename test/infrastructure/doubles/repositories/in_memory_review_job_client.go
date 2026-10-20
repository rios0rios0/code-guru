package repositories

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/rios0rios0/codeguru/internal/infrastructure/controllers/webhooks"
)

// ErrInMemoryJobConflict is what the in-memory Job API returns for a patch
// whose resourceVersion is stale — the API server's Conflict outcome, built by
// the production package so the scheduler classifies it exactly as it would a
// real one.
var ErrInMemoryJobConflict = webhooks.NewConflictError("PATCH")

// InMemoryReviewJobClient is an in-memory `batch/v1` Job API for the review
// executor's tests. It assigns resource versions and creation times, evaluates
// equality label selectors, and applies merge patches to `spec.suspend` and
// `metadata.annotations` with the same optimistic concurrency the API server
// enforces: a patch carrying a stale `metadata.resourceVersion` fails with
// Conflict and changes nothing.
type InMemoryReviewJobClient struct {
	mu        sync.Mutex
	jobs      map[string]*webhooks.ReviewJob
	manifests map[string]map[string]any
	version   int
	clock     time.Time
	ListErr   error
	CreateErr error
	// AfterList, when set, runs after every List returns its snapshot and
	// before the caller acts on it, so a test can interleave another
	// replica's work into that window.
	AfterList func()
	// Patches records every patch applied, by Job name, in order.
	Patches []string
	// patchErrs makes every patch of the named Job fail (see SetPatchError).
	patchErrs map[string]error
}

// NewInMemoryReviewJobClient returns an empty Job API.
func NewInMemoryReviewJobClient() *InMemoryReviewJobClient {
	return &InMemoryReviewJobClient{
		jobs:      map[string]*webhooks.ReviewJob{},
		manifests: map[string]map[string]any{},
		clock:     time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
	}
}

func (c *InMemoryReviewJobClient) nextVersion() string {
	c.version++
	return strconv.Itoa(c.version)
}

// Create stores the manifest as a Job.
func (c *InMemoryReviewJobClient) Create(_ context.Context, manifest map[string]any) (*webhooks.ReviewJob, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.CreateErr != nil {
		return nil, c.CreateErr
	}
	data, err := json.Marshal(manifest)
	if err != nil {
		return nil, err
	}
	var job webhooks.ReviewJob
	if err = json.Unmarshal(data, &job); err != nil {
		return nil, err
	}
	if _, exists := c.jobs[job.Metadata.Name]; exists {
		return nil, fmt.Errorf("job %s already exists", job.Metadata.Name)
	}
	c.clock = c.clock.Add(time.Second)
	job.Metadata.CreationTimestamp = c.clock
	job.Metadata.ResourceVersion = c.nextVersion()
	c.jobs[job.Metadata.Name] = &job
	c.manifests[job.Metadata.Name] = manifest
	created := job
	return &created, nil
}

// List returns the Jobs matching an equality-based label selector.
func (c *InMemoryReviewJobClient) List(_ context.Context, selector string) ([]webhooks.ReviewJob, error) {
	matched, err := c.snapshot(selector)
	if err == nil && c.AfterList != nil {
		c.AfterList()
	}
	return matched, err
}

func (c *InMemoryReviewJobClient) snapshot(selector string) ([]webhooks.ReviewJob, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.ListErr != nil {
		return nil, c.ListErr
	}
	var matched []webhooks.ReviewJob
	for _, job := range c.jobs {
		if matchesSelector(job.Metadata.Labels, selector) {
			copied := *job
			copied.Metadata.Annotations = copyStrings(job.Metadata.Annotations)
			matched = append(matched, copied)
		}
	}
	sort.Slice(matched, func(i, j int) bool { return matched[i].Metadata.Name < matched[j].Metadata.Name })
	return matched, nil
}

func copyStrings(values map[string]string) map[string]string {
	if values == nil {
		return nil
	}
	copied := make(map[string]string, len(values))
	maps.Copy(copied, values)
	return copied
}

// Patch applies a merge patch to `spec.suspend` and `metadata.annotations`.
func (c *InMemoryReviewJobClient) Patch(_ context.Context, name string, patch any) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	job, ok := c.jobs[name]
	if !ok {
		return errors.New("job not found: " + name)
	}
	if err := c.patchErrs[name]; err != nil {
		return err
	}
	data, err := json.Marshal(patch)
	if err != nil {
		return err
	}
	var parsed struct {
		Metadata struct {
			ResourceVersion string            `json:"resourceVersion"`
			Annotations     map[string]string `json:"annotations"`
		} `json:"metadata"`
		Spec struct {
			Suspend *bool `json:"suspend"`
		} `json:"spec"`
	}
	if err = json.Unmarshal(data, &parsed); err != nil {
		return err
	}
	if parsed.Metadata.ResourceVersion != "" && parsed.Metadata.ResourceVersion != job.Metadata.ResourceVersion {
		return ErrInMemoryJobConflict
	}
	if parsed.Spec.Suspend != nil {
		suspend := *parsed.Spec.Suspend
		job.Spec.Suspend = &suspend
	}
	for key, value := range parsed.Metadata.Annotations {
		if job.Metadata.Annotations == nil {
			job.Metadata.Annotations = map[string]string{}
		}
		job.Metadata.Annotations[key] = value
	}
	job.Metadata.ResourceVersion = c.nextVersion()
	c.Patches = append(c.Patches, name)
	return nil
}

// SetPatchError makes every patch of the named Job fail with err, the way an
// admission webhook or a quota would.
func (c *InMemoryReviewJobClient) SetPatchError(name string, err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.patchErrs == nil {
		c.patchErrs = map[string]error{}
	}
	c.patchErrs[name] = err
}

// Manifest returns the manifest a Job was created from.
func (c *InMemoryReviewJobClient) Manifest(name string) map[string]any {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.manifests[name]
}

// Job returns a copy of the named Job, and whether it exists.
func (c *InMemoryReviewJobClient) Job(name string) (webhooks.ReviewJob, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	job, ok := c.jobs[name]
	if !ok {
		return webhooks.ReviewJob{}, false
	}
	return *job, true
}

// Finish marks the named Job terminal: Complete when reason is empty, Failed
// with that reason otherwise.
func (c *InMemoryReviewJobClient) Finish(name, reason string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	job := c.jobs[name]
	condition := webhooks.ReviewJobCondition{Type: "Complete", Status: "True"}
	if reason != "" {
		condition = webhooks.ReviewJobCondition{Type: "Failed", Status: "True", Reason: reason}
	}
	job.Status.Conditions = append(job.Status.Conditions, condition)
	job.Metadata.ResourceVersion = c.nextVersion()
}

// Bump changes a Job's resourceVersion without changing its content, the way
// any write by another party would, so a test can make a listing stale.
func (c *InMemoryReviewJobClient) Bump(name string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.jobs[name].Metadata.ResourceVersion = c.nextVersion()
}

func matchesSelector(labels map[string]string, selector string) bool {
	for term := range strings.SplitSeq(selector, ",") {
		key, value, found := strings.Cut(strings.TrimSpace(term), "=")
		if !found || labels[key] != value {
			return false
		}
	}
	return true
}
