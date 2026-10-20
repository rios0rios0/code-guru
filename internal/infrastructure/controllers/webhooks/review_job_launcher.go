package webhooks

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	forgeEntities "github.com/rios0rios0/gitforge/v4/pkg/global/domain/entities"
	logger "github.com/sirupsen/logrus"
	"gopkg.in/yaml.v3"

	"github.com/rios0rios0/codeguru/internal/domain/entities"
)

// Labels and annotations the executor stamps on every review Job. The label
// keys are what the scheduler selects on and what a NetworkPolicy can target
// the review pods with; the annotations carry everything needed to talk about
// the pull request again later — to report a Job that died without posting —
// without re-parsing the container's arguments.
const (
	reviewJobLabel            = "code-guru/review-job"
	reviewJobPullRequestLabel = "code-guru/pull-request"
	componentLabel            = "app.kubernetes.io/component"
	componentReview           = "review"
	managedByLabel            = "app.kubernetes.io/managed-by"
	managedByCodeGuru         = "code-guru"

	annotationProvider        = "code-guru/provider"
	annotationOrganization    = "code-guru/organization"
	annotationProject         = "code-guru/project"
	annotationRepository      = "code-guru/repository"
	annotationRepositoryID    = "code-guru/repository-id"
	annotationPullRequestID   = "code-guru/pull-request-id"
	annotationPullRequestURL  = "code-guru/pull-request-url"
	annotationUserMentioned   = "code-guru/user-mentioned"
	annotationInstallationID  = "code-guru/github-installation-id"
	annotationFailureReported = "code-guru/failure-reported"
	annotationEnqueuedAt      = "code-guru/enqueued-at"

	// reviewJobNamePrefix starts every review Job name. With the 10-char
	// pull request hash, a base-36 millisecond stamp and a 4-char random
	// tail the name stays well under the 63 characters Kubernetes allows for
	// the `job-name` label it copies onto the pod.
	reviewJobNamePrefix  = "code-guru-review-"
	reviewJobHashLength  = 10
	reviewJobNameEntropy = 4

	// reviewContainerName is the container the executor points at the
	// pull request. A template with no container of that name gets its
	// first container used instead.
	reviewContainerName = "review"

	// defaultReviewJobTTLSeconds is applied when the template sets no
	// `ttlSecondsAfterFinished`: a finished review Job, and its pod, are
	// deleted fifteen minutes after they end — long enough to read the
	// logs of one that just failed, short enough not to pile up.
	defaultReviewJobTTLSeconds = 900

	// defaultReviewJobDeadlineSeconds is applied when the template sets no
	// `activeDeadlineSeconds`. The deadline only runs while a Job is not
	// suspended, so it bounds a review's run, not its wait in the queue;
	// without one, a pod that never starts — an image it cannot pull, a
	// node pool with no room — would hold a review slot forever. An hour
	// covers the agentic review's 30-minute default timeout plus the
	// checkout and the posting; a template that raises that timeout must
	// set a deadline of its own.
	defaultReviewJobDeadlineSeconds = 3600

	// installationIDEnv carries a GitHub App installation into the review
	// pod, which mints its own short-lived token from the App credentials.
	installationIDEnv = "CODE_GURU_GITHUB_INSTALLATION_ID"

	// reviewJobEnqueueTimeout bounds one enqueue's listing and create.
	reviewJobEnqueueTimeout = 20 * time.Second
)

// valueTrue is the string form of true in labels and annotations, as
// [strconv.FormatBool] writes it.
const valueTrue = "true"

// reviewJobSelector selects every Job the executor created.
const reviewJobSelector = reviewJobLabel + "=" + valueTrue

// ReviewJobRequest describes one pull request to hand to a review Job.
type ReviewJobRequest struct {
	Provider             string
	Repository           forgeEntities.Repository
	PullRequestID        int
	UserMentioned        bool
	GitHubInstallationID int64
}

// pullRequestKey is the stable identity of the pull request, hashed so it
// fits a label value. The repository ID is preferred over its name because
// a rename must not make one pull request look like two.
func (r ReviewJobRequest) pullRequestKey() string {
	repoKey := r.Repository.ID
	if repoKey == "" {
		repoKey = r.Repository.Name
	}
	digest := sha256.Sum256([]byte(strings.Join([]string{
		r.Provider, r.Repository.Organization, r.Repository.Project, repoKey, strconv.Itoa(r.PullRequestID),
	}, "\x00")))
	return hex.EncodeToString(digest[:])[:2*reviewJobHashLength]
}

// pullRequestWebURLBuilders map a provider to the web URL of a pull request
// (Mapper pattern). The URL is BUILT from validated names rather than copied
// from the webhook payload: the review pod parses it to decide which provider
// and which host to talk to, so it must not carry anything a payload chose.
//
//nolint:gochecknoglobals // read-only lookup table used as a constant
var pullRequestWebURLBuilders = map[string]func(repo forgeEntities.Repository, id int) (string, error){
	"github": func(repo forgeEntities.Repository, id int) (string, error) {
		if repo.Organization == "" || repo.Name == "" {
			return "", errors.New("github pull request needs an owner and a repository name")
		}
		return fmt.Sprintf("https://github.com/%s/%s/pull/%d",
			url.PathEscape(repo.Organization), url.PathEscape(repo.Name), id), nil
	},
	"azuredevops": func(repo forgeEntities.Repository, id int) (string, error) {
		if repo.Organization == "" || repo.Project == "" || repo.Name == "" {
			return "", errors.New("azure devops pull request needs an organization, a project and a repository name")
		}
		return fmt.Sprintf("https://dev.azure.com/%s/%s/_git/%s/pullrequest/%d",
			url.PathEscape(repo.Organization), url.PathEscape(repo.Project), url.PathEscape(repo.Name), id), nil
	},
}

// webURL builds the pull request URL the review pod is pointed at.
func (r ReviewJobRequest) webURL() (string, error) {
	build, ok := pullRequestWebURLBuilders[r.Provider]
	if !ok {
		return "", fmt.Errorf("provider %q is not supported by the kubernetes executor", r.Provider)
	}
	return build(r.Repository, r.PullRequestID)
}

// annotations renders the request as the Job annotations the scheduler's
// failure reporter reads back (see reviewJobRequestFromAnnotations).
func (r ReviewJobRequest) annotations(webURL string) map[string]string {
	values := map[string]string{
		annotationProvider:       r.Provider,
		annotationOrganization:   r.Repository.Organization,
		annotationProject:        r.Repository.Project,
		annotationRepository:     r.Repository.Name,
		annotationRepositoryID:   r.Repository.ID,
		annotationPullRequestID:  strconv.Itoa(r.PullRequestID),
		annotationPullRequestURL: webURL,
		annotationUserMentioned:  strconv.FormatBool(r.UserMentioned),
	}
	if r.GitHubInstallationID != 0 {
		values[annotationInstallationID] = strconv.FormatInt(r.GitHubInstallationID, 10)
	}
	return values
}

// reviewJobRequestFromAnnotations is the inverse of annotations.
func reviewJobRequestFromAnnotations(values map[string]string) (ReviewJobRequest, error) {
	prID, err := strconv.Atoi(values[annotationPullRequestID])
	if err != nil || prID <= 0 {
		return ReviewJobRequest{}, fmt.Errorf("job carries no valid %s annotation", annotationPullRequestID)
	}
	installationID, _ := strconv.ParseInt(values[annotationInstallationID], 10, 64)
	return ReviewJobRequest{
		Provider: values[annotationProvider],
		Repository: forgeEntities.Repository{
			ID:           values[annotationRepositoryID],
			Name:         values[annotationRepository],
			Organization: values[annotationOrganization],
			Project:      values[annotationProject],
		},
		PullRequestID:        prID,
		UserMentioned:        values[annotationUserMentioned] == valueTrue,
		GitHubInstallationID: installationID,
	}, nil
}

// ReviewJobTemplate is the operator's `batch/v1` Job manifest every review Job
// is rendered from. The operator owns the pod — image, resources, volumes,
// security context, environment, service account — and the executor only
// fills in the per-review fields (see Render).
type ReviewJobTemplate struct {
	manifest map[string]any
}

// LoadReviewJobTemplate reads and validates the template file.
func LoadReviewJobTemplate(path string) (*ReviewJobTemplate, error) {
	if strings.TrimSpace(path) == "" {
		return nil, errors.New("server.kubernetes.job_template is required by the kubernetes executor")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading the review job template: %w", err)
	}
	return ParseReviewJobTemplate(data)
}

// ParseReviewJobTemplate parses a template given as YAML or JSON (JSON is
// YAML) and validates that it can produce a runnable Job: a pod template
// with at least one container, no restart policy a Job rejects, and one pod
// per review.
func ParseReviewJobTemplate(data []byte) (*ReviewJobTemplate, error) {
	var manifest map[string]any
	if err := yaml.Unmarshal(data, &manifest); err != nil {
		return nil, fmt.Errorf("parsing the review job template: %w", err)
	}
	if manifest == nil {
		return nil, errors.New("the review job template is empty")
	}
	// Round-trip through JSON so every nested value has the shape
	// encoding/json produces (map[string]any, []any, float64) — the shape
	// Render navigates and the API server is sent.
	normalised, err := deepCopyManifest(manifest)
	if err != nil {
		return nil, err
	}
	if kind, ok := normalised["kind"].(string); ok && kind != "" && kind != "Job" {
		return nil, fmt.Errorf("the review job template must be a Job, got %q", kind)
	}
	if _, err = reviewContainer(normalised); err != nil {
		return nil, err
	}
	if policy, _ := nestedString(normalised, "spec", "template", "spec", "restartPolicy"); policy == "Always" {
		return nil, errors.New("the review job template sets restartPolicy Always, which a Job does not allow")
	}
	if err = requireOnePod(normalised); err != nil {
		return nil, err
	}
	return &ReviewJobTemplate{manifest: normalised}, nil
}

// reviewJobPods is how many pods a review Job runs.
const reviewJobPods = 1

// requireOnePod rejects a template whose `parallelism` or `completions`
// would run a review in more pods than one, or in none. Every pod runs the
// whole review, so a second one posts every finding again, and the scheduler
// counts Jobs, not pods, against the cap.
func requireOnePod(manifest map[string]any) error {
	spec, _ := manifest["spec"].(map[string]any)
	for _, field := range []string{"parallelism", "completions"} {
		value, set := spec[field]
		if !set || value == nil {
			continue
		}
		if count, isNumber := value.(float64); isNumber && count == reviewJobPods {
			continue
		}
		return fmt.Errorf(
			"the review job template sets spec.%s to %v, but a review Job runs exactly one pod: leave it unset or set it to 1",
			field,
			value,
		)
	}
	return nil
}

// Render produces the manifest for one review. On a fresh copy of the
// template it sets the name, the selector labels (on the Job and its pod),
// the request annotations, `spec.suspend: true` — every review Job is
// created queued and only the scheduler starts it, which is what keeps the
// concurrency cap exact across server replicas — and the review container's
// arguments, which name the pull request and the Job itself. A missing
// `backoffLimit`, `ttlSecondsAfterFinished`, `activeDeadlineSeconds` or pod
// `restartPolicy` gets the executor's default.
func (t *ReviewJobTemplate) Render(request ReviewJobRequest, name string) (map[string]any, error) {
	webURL, err := request.webURL()
	if err != nil {
		return nil, err
	}
	manifest, err := deepCopyManifest(t.manifest)
	if err != nil {
		return nil, err
	}
	manifest["apiVersion"] = "batch/v1"
	manifest["kind"] = "Job"

	labels := map[string]string{
		reviewJobLabel:            valueTrue,
		reviewJobPullRequestLabel: request.pullRequestKey(),
		componentLabel:            componentReview,
		managedByLabel:            managedByCodeGuru,
	}
	metadata := ensureMap(manifest, "metadata")
	delete(metadata, "generateName")
	// The namespace is the one the client posts to; a stale one in the
	// template would only make the API server reject the request.
	delete(metadata, "namespace")
	metadata["name"] = name
	mergeStringMap(metadata, "labels", labels)
	mergeStringMap(metadata, "annotations", request.annotations(webURL))

	spec := ensureMap(manifest, "spec")
	spec["suspend"] = true
	setDefault(spec, "backoffLimit", 0)
	setDefault(spec, "ttlSecondsAfterFinished", defaultReviewJobTTLSeconds)
	setDefault(spec, "activeDeadlineSeconds", defaultReviewJobDeadlineSeconds)
	podTemplate := ensureMap(spec, "template")
	mergeStringMap(ensureMap(podTemplate, "metadata"), "labels", labels)
	setDefault(ensureMap(podTemplate, "spec"), "restartPolicy", "Never")

	container, err := reviewContainer(manifest)
	if err != nil {
		return nil, err
	}
	args := []any{"review", webURL}
	if request.UserMentioned {
		args = append(args, "--mentioned")
	}
	// The Job's name rides into the review's outcome notices, which is how
	// the scheduler tells this Job's own completion or failure notice from
	// an earlier review's when it decides whether a dead Job still needs
	// reporting.
	args = append(args, "--job-name", name)
	container["args"] = args
	if request.GitHubInstallationID != 0 {
		setEnv(container, installationIDEnv, strconv.FormatInt(request.GitHubInstallationID, 10))
	}
	return manifest, nil
}

// EnqueueOutcome reports what happened to one review request.
type EnqueueOutcome struct {
	// JobName is the Job that carries the review — the new one, or the
	// unfinished one a duplicate request matched.
	JobName string
	// Duplicate is set when an unfinished review Job already existed for
	// the pull request, so nothing was created.
	Duplicate bool
	// Queued is set when the new Job is waiting for a free slot.
	Queued bool
	// QueuePosition is the 1-based position among queued Jobs when Queued.
	QueuePosition int
	// BehindOwnPullRequest is set when the new Job waits for another review
	// of the same pull request to finish, rather than for a free slot.
	BehindOwnPullRequest bool
}

// ReviewJobLauncher turns review requests into review Jobs.
type ReviewJobLauncher struct {
	client    ReviewJobClient
	template  *ReviewJobTemplate
	scheduler *ReviewJobScheduler
	now       func() time.Time

	// locks serialises each pull request's list-then-create across
	// replicas. Nil enqueues unlocked, which is only safe with a single
	// replica.
	locks ReviewJobLocks
}

// NewReviewJobLauncher wires a launcher. The scheduler is reconciled right
// after every create, so a review starts immediately when a slot is free
// instead of waiting for the next poll.
func NewReviewJobLauncher(
	client ReviewJobClient, template *ReviewJobTemplate, scheduler *ReviewJobScheduler,
) *ReviewJobLauncher {
	return &ReviewJobLauncher{client: client, template: template, scheduler: scheduler, now: time.Now}
}

// NewInClusterReviewJobExecutor assembles the Kubernetes executor from the
// server's own pod credentials: a Job client for the configured namespace (the
// pod's own when none is set), the Lease-backed locks that serialise the
// replicas, the scheduler that enforces the concurrency cap and reports dead
// Jobs through `report`, and the launcher that enqueues.
func NewInClusterReviewJobExecutor(
	config entities.KubernetesExecutorConfig,
	template *ReviewJobTemplate,
	report ReviewJobFailureReporter,
) (*ReviewJobLauncher, *ReviewJobScheduler, error) {
	if !IsInKubernetes() {
		return nil, nil, errors.New("the kubernetes executor needs the server to run inside a Kubernetes pod")
	}
	namespace := strings.TrimSpace(config.Namespace)
	if namespace == "" {
		raw, err := os.ReadFile(podNamespaceFile)
		if err != nil {
			return nil, nil, fmt.Errorf("reading the pod namespace from %s: %w", podNamespaceFile, err)
		}
		namespace = strings.TrimSpace(string(raw))
	}
	client, err := newInClusterJobClient(namespace)
	if err != nil {
		return nil, nil, err
	}
	leases, err := newInClusterLeaseClient(namespace)
	if err != nil {
		return nil, nil, err
	}
	holder, err := os.Hostname()
	if err != nil || holder == "" {
		holder = managedByCodeGuru
	}
	locks := newLeaseReviewJobLocks(leases, holder)
	if err = locks.probe(context.Background()); err != nil {
		return nil, nil, fmt.Errorf(
			"the kubernetes executor cannot take its locks (grant the server get, create and delete on "+
				"coordination.k8s.io leases): %w", err)
	}
	scheduler := NewReviewJobScheduler(client, config.ConcurrentJobs(), config.SchedulerInterval(), report).
		WithLocks(locks)
	return NewReviewJobLauncher(client, template, scheduler).WithLocks(locks), scheduler, nil
}

// WithLocks makes every enqueue hold its pull request's lock across the
// list-then-create.
func (l *ReviewJobLauncher) WithLocks(locks ReviewJobLocks) *ReviewJobLauncher {
	l.locks = locks
	return l
}

// Enqueue creates the review Job for one pull request, unless the request
// duplicates one that is already queued or running for it.
//
// A push-triggered request is a duplicate of ANY unfinished Job for the pull
// request. Azure DevOps fires `created` and `updated` for every new pull
// request and every push fires `updated` again, and the review-once gate only
// closes when a review FINISHES — so without this check every push during a
// long review would start a parallel one that posts the same findings twice.
//
// A mention is a duplicate only of a mention Job that is still QUEUED: that
// one has not checked anything out yet, so when it starts it reads the newest
// code and every thread, which answers the new request too. Someone who asks
// for a re-review while a review is RUNNING — a first review, or an earlier
// re-review that checked out the code before their latest push — gets one,
// queued behind it: the scheduler never runs two Jobs of one pull request at
// once, so it starts after the running review has posted and reads the
// threads it left. Dropping the mention instead would leave the request
// unanswered.
//
// The list-then-create runs under the pull request's lock, so two deliveries
// for one pull request on two replicas cannot both find no Job and both
// create one.
func (l *ReviewJobLauncher) Enqueue(ctx context.Context, request ReviewJobRequest) (EnqueueOutcome, error) {
	name, duplicate, err := l.create(ctx, request)
	if err != nil || duplicate {
		return EnqueueOutcome{JobName: name, Duplicate: duplicate}, err
	}

	if _, err = l.scheduler.startQueued(ctx); err != nil {
		logger.Warnf(
			"review job %s created but the scheduler pass failed (%v); the next poll will start it",
			name,
			err,
		)
	}
	state, err := l.scheduler.QueuePosition(ctx, name)
	if err != nil {
		// Unknown state: report it as started rather than claim a queue
		// position that may not exist. The Job is created either way.
		logger.Warnf("review job %s created but its queue position could not be read: %v", name, err)
		return EnqueueOutcome{JobName: name}, nil
	}
	return EnqueueOutcome{
		JobName:              name,
		Queued:               state.Queued,
		QueuePosition:        state.Position,
		BehindOwnPullRequest: state.BehindOwnPullRequest,
	}, nil
}

// create is Enqueue's critical section: under the pull request's lock, it
// lists the pull request's Jobs and creates a new one unless the request
// duplicates one of them. It returns the Job's name and whether it was a
// duplicate. The lock is released before the reconcile that follows, which
// can take far longer than this and holds the scheduler lock of its own.
func (l *ReviewJobLauncher) create(ctx context.Context, request ReviewJobRequest) (string, bool, error) {
	key := request.pullRequestKey()
	unlock := lockOrProceed(ctx, l.locks, reviewJobEnqueueLockPrefix+key)
	defer unlock()
	// Bounded well under the lock's Lease duration, which nothing renews: a
	// critical section that outlived it would let another replica in.
	ctx, cancel := context.WithTimeout(ctx, reviewJobEnqueueTimeout)
	defer cancel()

	existing, err := l.client.List(ctx, reviewJobSelector+","+reviewJobPullRequestLabel+"="+key)
	if err != nil {
		return "", false, fmt.Errorf("listing the review jobs of PR #%d: %w", request.PullRequestID, err)
	}
	if duplicate, found := duplicateReviewJob(existing, request.UserMentioned); found {
		return duplicate, true, nil
	}

	now := l.now()
	name := reviewJobName(key, now)
	manifest, err := l.template.Render(request, name)
	if err != nil {
		return "", false, err
	}
	mergeStringMap(ensureMap(manifest, "metadata"), "annotations", map[string]string{
		annotationEnqueuedAt: now.UTC().Format(time.RFC3339Nano),
	})
	if _, err = l.client.Create(ctx, manifest); err != nil {
		if isAlreadyExists(err) {
			return name, true, nil
		}
		return "", false, fmt.Errorf("creating the review job for PR #%d: %w", request.PullRequestID, err)
	}
	return name, false, nil
}

// duplicateReviewJob returns the unfinished Job a new request duplicates, if
// any (see Enqueue for the rule): any unfinished Job for a push, only a
// queued mention Job for a mention.
func duplicateReviewJob(existing []ReviewJob, mention bool) (string, bool) {
	for _, job := range existing {
		if job.Finished() {
			continue
		}
		if !mention || (job.Suspended() && job.Metadata.Annotations[annotationUserMentioned] == valueTrue) {
			return job.Metadata.Name, true
		}
	}
	return "", false
}

// reviewJobName builds a review Job name: the prefix, the pull request's hash
// (so `kubectl get jobs` groups one pull request's runs), the creation time in
// base-36 milliseconds and a short random tail, so two Jobs for the same pull
// request created within one millisecond still get distinct names.
func reviewJobName(pullRequestKey string, now time.Time) string {
	const alphabet = "0123456789abcdefghijklmnopqrstuvwxyz"
	tail := make([]byte, reviewJobNameEntropy)
	if _, err := rand.Read(tail); err != nil {
		// crypto/rand does not fail on supported platforms; a fixed tail
		// only re-opens the one-millisecond collision the create handles.
		copy(tail, "zzzz")
	}
	for i := range tail {
		tail[i] = alphabet[int(tail[i])%len(alphabet)]
	}
	return reviewJobNamePrefix + pullRequestKey[:reviewJobHashLength] + "-" +
		strconv.FormatInt(now.UnixMilli(), 36) + string(tail)
}

// reviewContainer returns the container the executor points at the pull
// request: the one named `review`, else the first one.
func reviewContainer(manifest map[string]any) (map[string]any, error) {
	spec, _ := nestedMap(manifest, "spec", "template", "spec")
	containers, _ := spec["containers"].([]any)
	if len(containers) == 0 {
		return nil, errors.New("the review job template has no spec.template.spec.containers")
	}
	for _, raw := range containers {
		if container, ok := raw.(map[string]any); ok && container["name"] == reviewContainerName {
			return container, nil
		}
	}
	first, ok := containers[0].(map[string]any)
	if !ok {
		return nil, errors.New("the review job template's first container is not an object")
	}
	return first, nil
}

// deepCopyManifest copies a manifest through JSON, which also normalises it
// to encoding/json's value shapes.
func deepCopyManifest(manifest map[string]any) (map[string]any, error) {
	data, err := json.Marshal(manifest)
	if err != nil {
		return nil, fmt.Errorf("encoding the review job template: %w", err)
	}
	var out map[string]any
	if err = json.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("decoding the review job template: %w", err)
	}
	return out, nil
}

// ensureMap returns parent[key] as an object, creating it when absent or
// replacing a value of the wrong shape.
func ensureMap(parent map[string]any, key string) map[string]any {
	if child, ok := parent[key].(map[string]any); ok {
		return child
	}
	child := map[string]any{}
	parent[key] = child
	return child
}

func nestedMap(root map[string]any, path ...string) (map[string]any, bool) {
	current := root
	for _, key := range path {
		next, ok := current[key].(map[string]any)
		if !ok {
			return nil, false
		}
		current = next
	}
	return current, true
}

func nestedString(root map[string]any, path ...string) (string, bool) {
	parent, ok := nestedMap(root, path[:len(path)-1]...)
	if !ok {
		return "", false
	}
	value, ok := parent[path[len(path)-1]].(string)
	return value, ok
}

// mergeStringMap writes values into parent[key], keeping the template's own
// entries for keys the executor does not set.
func mergeStringMap(parent map[string]any, key string, values map[string]string) {
	target := ensureMap(parent, key)
	for k, v := range values {
		target[k] = v
	}
}

func setDefault(parent map[string]any, key string, value any) {
	if _, ok := parent[key]; !ok {
		parent[key] = value
	}
}

// setEnv sets one plain environment variable on a container, replacing an
// entry of the same name.
func setEnv(container map[string]any, name, value string) {
	entry := map[string]any{"name": name, "value": value}
	env, _ := container["env"].([]any)
	for i, raw := range env {
		if existing, ok := raw.(map[string]any); ok && existing["name"] == name {
			env[i] = entry
			container["env"] = env
			return
		}
	}
	container["env"] = append(env, entry)
}
