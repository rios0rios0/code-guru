// Package workspace prepares disposable local checkouts of pull requests for
// reviewers that read the repository instead of only the diff text.
package workspace

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	forgeEntities "github.com/rios0rios0/gitforge/v4/pkg/global/domain/entities"
	logger "github.com/sirupsen/logrus"

	"github.com/rios0rios0/codeguru/internal/domain/entities"
	"github.com/rios0rios0/codeguru/internal/infrastructure/repositories/prmetadata"
	"github.com/rios0rios0/codeguru/internal/support"
)

const (
	defaultGitBinary = "git"

	// workspaceDirPattern names each review's root directory. The prefix
	// makes an orphaned checkout (a process killed before Release ran)
	// attributable to this bot rather than anonymous litter.
	workspaceDirPattern = "code-guru-review-*"

	// checkoutDirName is the repository checkout inside the workspace root.
	checkoutDirName = "repo"

	// maxDeepenRounds bounds how many times a shallow fetch is deepened while
	// looking for the merge-base before falling back to a full unshallow.
	maxDeepenRounds = 6

	// gitOutputLogLimit caps the git output quoted in an error.
	gitOutputLogLimit = 2048

	// flagNoTags keeps every fetch to the two branch refs: tags are never
	// needed to review a pull request, and some repositories carry
	// thousands of them.
	flagNoTags = "--no-tags"

	// flagQuiet keeps git's progress chatter out of the captured stderr,
	// which is quoted in errors.
	flagQuiet = "--quiet"

	// headRef is the local ref the pull request's head is fetched into. It
	// is a constant outside `refs/remotes/` on purpose: a fork's branch is
	// often named like the target (`fork:main` into `main`), and fetching
	// both into `refs/remotes/origin/<branch>` would make the two refspecs
	// write the same ref.
	headRef = "refs/code-guru/head"
)

// errMergeBaseNotFound reports that the two branches share no history the
// fetch brought down.
var errMergeBaseNotFound = errors.New("no merge-base between the source and target branches")

// errForkWithoutHeadRef refuses a pull request from a fork on a provider that
// publishes no head ref for it on the target repository.
var errForkWithoutHeadRef = errors.New(
	"the pull request comes from a fork, and its provider publishes no head ref on the target repository " +
		"to check it out from")

// ForkChecker tells whether a pull request comes from a fork. It is consulted
// only for providers that publish no pull request head ref on the target
// repository, where a fork's source branch could otherwise only be fetched by
// name — from the target, where a branch of the same name may be a different
// one.
type ForkChecker interface {
	IsFork(ctx context.Context, token string, repo forgeEntities.Repository, prID int) (bool, error)
}

// cloneTarget describes how one provider's repositories are cloned: the URL,
// built from validated names against a CONSTANT host, and the username paired
// with the provider token in HTTP Basic auth.
type cloneTarget struct {
	url      string
	host     string
	username string

	// pullHeadRefFormat, when set, names the ref the provider publishes for a
	// pull request's head on the TARGET repository (`%d` = the pull request
	// id), and the head is always fetched from it. It is the one source that
	// is right for every pull request: for one from a fork, the source branch
	// is not on the target repository at all, or, worse, a different branch
	// of the same name is (`fork:main` into `main` is the usual case), and a
	// fetch by branch name would succeed against it and check out the wrong
	// code. Empty means the provider publishes no such ref, and the source
	// branch is fetched by name.
	pullHeadRefFormat string
}

// headSource names the ref the pull request's head is fetched from on the
// target repository.
func (t cloneTarget) headSource(sourceBranch string, prID int) string {
	if t.pullHeadRefFormat != "" {
		return fmt.Sprintf(t.pullHeadRefFormat, prID)
	}
	return "refs/heads/" + sourceBranch
}

// cloneTargetBuilders maps a gitforge provider name to the function that
// builds its clone target (Mapper pattern: a new provider is a new entry).
//
// The host is a constant on purpose, never `Repository.RemoteURL`: the URL is
// where the provider token is sent, and a remote URL that came from a webhook
// payload or a hand-built repository entity must not be able to redirect an
// authenticated fetch to another host. Every name is path-escaped, so a name
// cannot smuggle a host or a query in either.
//
//nolint:gochecknoglobals // read-only lookup table used as a constant
var cloneTargetBuilders = map[string]func(repo forgeEntities.Repository) (cloneTarget, error){
	"github": func(repo forgeEntities.Repository) (cloneTarget, error) {
		if repo.Organization == "" || repo.Name == "" {
			return cloneTarget{}, errors.New("github repository needs an owner and a name")
		}
		return cloneTarget{
			url: fmt.Sprintf(
				"https://github.com/%s/%s.git",
				url.PathEscape(repo.Organization), url.PathEscape(repo.Name),
			),
			host:              "github.com",
			username:          "x-access-token",
			pullHeadRefFormat: "refs/pull/%d/head",
		}, nil
	},
	"azuredevops": func(repo forgeEntities.Repository) (cloneTarget, error) {
		if repo.Organization == "" || repo.Project == "" || repo.Name == "" {
			return cloneTarget{}, errors.New("azure devops repository needs an organization, a project and a name")
		}
		return cloneTarget{
			url: fmt.Sprintf(
				"https://dev.azure.com/%s/%s/_git/%s",
				url.PathEscape(repo.Organization), url.PathEscape(repo.Project), url.PathEscape(repo.Name),
			),
			host:     "dev.azure.com",
			username: "pat",
		}, nil
	},
}

// GitWorkspaceRepository implements repositories.WorkspaceRepository with the
// git CLI. The CLI rather than a Go git library because the reviewer that reads
// the checkout runs `git log`, `git blame` and `git diff` itself, so the binary
// has to be there anyway, and because it handles every hosting provider's
// protocol quirks without per-provider transport tuning.
//
// Credentials never touch the command line or the repository's config: the
// token travels in an `http.<host>.extraHeader` set through `GIT_CONFIG_*`
// environment variables of the fetch processes only, scoped to the provider's
// host, so it is neither visible in `/proc/<pid>/cmdline` nor left behind in
// `.git/config` for whatever reads the checkout afterwards.
type GitWorkspaceRepository struct {
	gitBinary  string
	baseDir    string
	cloneDepth int

	// forkCheckers maps a provider without a pull request head ref to the
	// check that refuses its pull requests from forks.
	forkCheckers map[string]ForkChecker
}

// NewGitWorkspaceRepository builds the repository. `baseDir` is where each
// review's workspace root is created (empty = the OS temp directory);
// `cloneDepth` is the history depth fetched for both branches (0 = full).
// Azure DevOps pull requests are checked for a fork source before anything is
// cloned (see ForkChecker).
func NewGitWorkspaceRepository(baseDir string, cloneDepth int) *GitWorkspaceRepository {
	return &GitWorkspaceRepository{
		gitBinary:    defaultGitBinary,
		baseDir:      baseDir,
		cloneDepth:   max(cloneDepth, 0),
		forkCheckers: map[string]ForkChecker{"azuredevops": prmetadata.NewAzureDevOpsFetcher(nil)},
	}
}

// WithForkChecker replaces the fork check of one provider. Tests use it to
// keep the network out; production keeps the default.
func (r *GitWorkspaceRepository) WithForkChecker(providerName string, checker ForkChecker) *GitWorkspaceRepository {
	r.forkCheckers[providerName] = checker
	return r
}

// WithGitBinary overrides the git executable. Tests use it to run against a
// specific binary; production keeps the PATH lookup.
func (r *GitWorkspaceRepository) WithGitBinary(binary string) *GitWorkspaceRepository {
	r.gitBinary = binary
	return r
}

// Prepare clones the pull request's source and target branches, checks the
// source head out detached and computes the merge-base and the changed files.
func (r *GitWorkspaceRepository) Prepare(
	ctx context.Context,
	provider forgeEntities.ReviewProvider,
	repo forgeEntities.Repository,
	pr forgeEntities.PullRequestDetail,
) (*entities.ReviewWorkspace, error) {
	builder, ok := cloneTargetBuilders[provider.Name()]
	if !ok {
		return nil, fmt.Errorf("provider %q has no workspace clone support", provider.Name())
	}
	target, err := builder(repo)
	if err != nil {
		return nil, err
	}

	pr, err = resolveBranches(ctx, provider, repo, pr)
	if err != nil {
		return nil, err
	}
	if err = r.refuseUnreachableFork(ctx, provider, target, repo, pr.ID); err != nil {
		return nil, err
	}

	return r.prepareFrom(ctx, target, provider.AuthToken(), pr)
}

// refuseUnreachableFork fails a pull request from a fork when its provider
// publishes no head ref for it: the source branch would then be fetched by
// name from the TARGET repository, which checks out a different branch — and
// reviews, and may approve, code nobody read — whenever the target has one of
// the same name. The check fails closed: a pull request whose fork status
// cannot be read is not checked out.
func (r *GitWorkspaceRepository) refuseUnreachableFork(
	ctx context.Context,
	provider forgeEntities.ReviewProvider,
	target cloneTarget,
	repo forgeEntities.Repository,
	prID int,
) error {
	checker, ok := r.forkCheckers[provider.Name()]
	if target.pullHeadRefFormat != "" || !ok {
		return nil
	}
	fork, err := checker.IsFork(ctx, provider.AuthToken(), repo, prID)
	if err != nil {
		return fmt.Errorf("checking whether PR #%d comes from a fork: %w", prID, err)
	}
	if fork {
		return errForkWithoutHeadRef
	}
	return nil
}

// prepareFrom does the git work against an already-resolved clone target.
// Split from Prepare so tests can point it at a local repository (a `file://`
// URL needs no credentials and no network).
func (r *GitWorkspaceRepository) prepareFrom(
	ctx context.Context,
	target cloneTarget,
	token string,
	pr forgeEntities.PullRequestDetail,
) (*entities.ReviewWorkspace, error) {
	root, err := os.MkdirTemp(r.baseDir, workspaceDirPattern)
	if err != nil {
		return nil, fmt.Errorf("creating the workspace directory: %w", err)
	}
	workspace := &entities.ReviewWorkspace{
		Root:         root,
		Path:         filepath.Join(root, checkoutDirName),
		SourceBranch: pr.SourceBranch,
		TargetBranch: pr.TargetBranch,
	}

	if err = r.checkout(ctx, workspace, target, token, pr.ID); err != nil {
		r.Release(workspace)
		return nil, err
	}

	logger.Infof(
		"PR #%d: workspace ready (%d changed file(s), base=%s head=%s)",
		pr.ID, len(workspace.ChangedFiles), shortSHA(workspace.BaseCommit), shortSHA(workspace.HeadCommit),
	)
	return workspace, nil
}

// checkout runs the clone sequence into workspace.Path and fills in the
// commits and the changed files.
func (r *GitWorkspaceRepository) checkout(
	ctx context.Context,
	workspace *entities.ReviewWorkspace,
	target cloneTarget,
	token string,
	prID int,
) error {
	targetRef := "refs/remotes/origin/" + workspace.TargetBranch

	// `core.symlinks=false` checks symlinks out as plain files holding the
	// link target. The checkout is read by an agent with file tools, and a
	// pull request that adds `notes.md -> /proc/self/environ` must not turn a
	// "read this file" into a read of the process environment.
	// `core.hooksPath=/dev/null` keeps any hook out of every later git call.
	if _, err := r.git(ctx, "", nil, "init", flagQuiet, workspace.Path); err != nil {
		return err
	}
	for _, kv := range [][2]string{
		{"core.symlinks", "false"},
		{"core.hooksPath", os.DevNull},
		{"advice.detachedHead", "false"},
	} {
		if _, err := r.git(ctx, workspace.Path, nil, "config", kv[0], kv[1]); err != nil {
			return err
		}
	}
	if _, err := r.git(ctx, workspace.Path, nil, "remote", "add", "origin", target.url); err != nil {
		return err
	}

	// Pin the remote to the two refs the review needs. `remote add`
	// configures the default `refs/heads/*` refspec, and every later
	// refspec-less fetch — the deepening in mergeBase included — would
	// otherwise download every branch of the repository.
	auth := authEnv(target, token)
	headSpec := "+" + target.headSource(workspace.SourceBranch, prID) + ":" + headRef
	targetSpec := "+refs/heads/" + workspace.TargetBranch + ":" + targetRef
	if err := r.fetchBranches(ctx, workspace.Path, auth, headSpec, targetSpec); err != nil {
		return fmt.Errorf("fetching the pull request branches: %w", err)
	}
	if _, err := r.git(ctx, workspace.Path, nil, "checkout", flagQuiet, "--detach", headRef); err != nil {
		return err
	}

	head, err := r.git(ctx, workspace.Path, nil, "rev-parse", "HEAD")
	if err != nil {
		return err
	}
	workspace.HeadCommit = head

	base, err := r.mergeBase(ctx, workspace.Path, auth, targetRef, headRef)
	if err != nil {
		return err
	}
	workspace.BaseCommit = base

	files, err := r.changedFiles(ctx, workspace.Path, base, head)
	if err != nil {
		return err
	}
	workspace.ChangedFiles = files
	return nil
}

// fetchBranches replaces the remote's fetch refspecs with exactly the two
// given — the pull request head and the target branch — and fetches them,
// honouring the configured clone depth.
func (r *GitWorkspaceRepository) fetchBranches(
	ctx context.Context, dir string, auth []string, headSpec, targetSpec string,
) error {
	if _, err := r.git(ctx, dir, nil, "config", "--replace-all", "remote.origin.fetch", headSpec); err != nil {
		return err
	}
	if _, err := r.git(ctx, dir, nil, "config", "--add", "remote.origin.fetch", targetSpec); err != nil {
		return err
	}
	var depth []string
	if r.cloneDepth > 0 {
		depth = []string{"--depth", strconv.Itoa(r.cloneDepth)}
	}
	_, err := r.git(ctx, dir, auth, fetchArgs(depth...)...)
	return err
}

// fetchArgs builds a quiet, tag-less `git fetch origin` with the given extra
// options. Every fetch goes through the remote's pinned refspecs.
func fetchArgs(options ...string) []string {
	return append(append([]string{"fetch", flagQuiet, flagNoTags}, options...), "origin")
}

// mergeBase resolves the merge-base of the two refs, deepening a shallow fetch
// until it appears. Without the deepening a `clone_depth` shorter than the
// branch's divergence would make every review of a long-lived branch fail.
// The deepening fetches go through the remote's pinned refspecs, so they only
// ever extend the two branches the review needs.
func (r *GitWorkspaceRepository) mergeBase(
	ctx context.Context, dir string, auth []string, targetRef, head string,
) (string, error) {
	for round := 0; ; round++ {
		base, err := r.git(ctx, dir, nil, "merge-base", targetRef, head)
		if err == nil && base != "" {
			return base, nil
		}
		shallow, _ := r.git(ctx, dir, nil, "rev-parse", "--is-shallow-repository")
		if shallow != "true" {
			return "", errMergeBaseNotFound
		}
		deepen := fetchArgs("--unshallow")
		if round < maxDeepenRounds {
			deepen = fetchArgs("--deepen", strconv.Itoa(r.cloneDepth<<round))
		}
		if _, err = r.git(ctx, dir, auth, deepen...); err != nil {
			return "", fmt.Errorf("deepening the fetch to find the merge-base: %w", err)
		}
	}
}

// changedFiles lists the files changed between base and head, joining git's
// name-status (the change kind) with its numstat (the line counts).
func (r *GitWorkspaceRepository) changedFiles(
	ctx context.Context, dir, base, head string,
) ([]entities.WorkspaceFileChange, error) {
	nameStatus, err := r.git(ctx, dir, nil, "diff", "--name-status", "-z", "-M", base, head)
	if err != nil {
		return nil, err
	}
	numStat, err := r.git(ctx, dir, nil, "diff", "--numstat", "-z", "-M", base, head)
	if err != nil {
		return nil, err
	}
	return mergeFileChanges(parseNameStatus(nameStatus), parseNumStat(numStat)), nil
}

// Release removes the workspace root. Removal failures are logged, never
// returned: a leftover temporary directory must not fail a review that has
// already been posted.
func (r *GitWorkspaceRepository) Release(workspace *entities.ReviewWorkspace) {
	if workspace == nil || workspace.Root == "" {
		return
	}
	if err := os.RemoveAll(workspace.Root); err != nil {
		logger.Warnf("failed to remove the review workspace: %v", err)
	}
}

// git runs one git command and returns its trimmed stdout. `dir` is the
// working directory ("" = the current one); `extraEnv` adds variables on top of
// the isolated base environment.
func (r *GitWorkspaceRepository) git(
	ctx context.Context,
	dir string,
	extraEnv []string,
	args ...string,
) (string, error) {
	//nolint:gosec // G204: the binary is configuration, and every argument is a fixed flag, a validated ref, or a path this repository created
	cmd := exec.CommandContext(ctx, r.gitBinary, args...)
	cmd.Dir = dir
	cmd.Env = append(baseEnv(), extraEnv...)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf(
			"git %s failed: %w (stderr: %s)",
			args[0], err, support.TruncateBytesForLog(stderr.Bytes(), gitOutputLogLimit),
		)
	}
	return strings.TrimSpace(stdout.String()), nil
}

// baseEnv is the environment every git process starts from. It is built from
// an allowlist rather than inherited, for two reasons: the server's own
// secrets (provider token, AI credentials, webhook secret) have no business
// in a git process, and an operator's `~/.gitconfig` — an `insteadOf` rule
// that rewrites HTTPS to SSH, a credential helper — must not change how the
// bot fetches. `GIT_CONFIG_GLOBAL=/dev/null` and `GIT_CONFIG_NOSYSTEM` drop
// both config files; `GIT_TERMINAL_PROMPT=0` turns a credential prompt into a
// failure instead of a hang.
func baseEnv() []string {
	env := []string{
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_CONFIG_GLOBAL=" + os.DevNull,
		"GIT_TERMINAL_PROMPT=0",
		"GIT_ASKPASS=",
		"LC_ALL=C",
	}
	for _, name := range []string{
		"PATH", "HOME", "TMPDIR",
		"HTTPS_PROXY", "HTTP_PROXY", "NO_PROXY", "https_proxy", "http_proxy", "no_proxy",
		"SSL_CERT_FILE", "SSL_CERT_DIR", "GIT_SSL_CAINFO",
	} {
		if value, ok := os.LookupEnv(name); ok {
			env = append(env, name+"="+value)
		}
	}
	return env
}

// authEnv returns the `GIT_CONFIG_*` variables that make git send the token as
// HTTP Basic auth to the provider's host — and only to it: the key is scoped
// with `http.<url>.extraHeader`, so a redirect to another host does not carry
// the header along. An empty token (a public repository, a local test) adds
// nothing.
func authEnv(target cloneTarget, token string) []string {
	if token == "" || target.host == "" {
		return nil
	}
	credentials := base64.StdEncoding.EncodeToString([]byte(target.username + ":" + token))
	return []string{
		"GIT_CONFIG_COUNT=1",
		"GIT_CONFIG_KEY_0=http.https://" + target.host + "/.extraHeader",
		"GIT_CONFIG_VALUE_0=Authorization: Basic " + credentials,
	}
}

// resolveBranches fills in the source and target branch when the pull request
// entity arrived without them (an Azure DevOps comment event carries only the
// pull request id) by looking the pull request up among the open ones.
func resolveBranches(
	ctx context.Context,
	provider forgeEntities.ReviewProvider,
	repo forgeEntities.Repository,
	pr forgeEntities.PullRequestDetail,
) (forgeEntities.PullRequestDetail, error) {
	if pr.SourceBranch != "" && pr.TargetBranch != "" {
		return pr, validateBranches(pr)
	}
	open, err := provider.ListOpenPullRequests(ctx, repo)
	if err != nil {
		return pr, fmt.Errorf("resolving the branches of PR #%d: %w", pr.ID, err)
	}
	for i := range open {
		if open[i].ID == pr.ID {
			pr.SourceBranch = open[i].SourceBranch
			pr.TargetBranch = open[i].TargetBranch
			return pr, validateBranches(pr)
		}
	}
	return pr, fmt.Errorf("PR #%d is not open, so its branches cannot be checked out", pr.ID)
}

// validateBranches rejects branch names git would read as something else. The
// names end up in a refspec, so a leading `-` (an option) or a `:` / `..` /
// whitespace (a different refspec) must never get that far.
func validateBranches(pr forgeEntities.PullRequestDetail) error {
	for _, branch := range []string{pr.SourceBranch, pr.TargetBranch} {
		if branch == "" || strings.HasPrefix(branch, "-") || strings.ContainsAny(branch, ": \t\n\\^~?*[") ||
			strings.Contains(branch, "..") {
			return fmt.Errorf("refusing to check out the branch name %q", branch)
		}
	}
	return nil
}

// shortSHA abbreviates a commit id for a log line.
func shortSHA(sha string) string {
	const short = 12
	if len(sha) > short {
		return sha[:short]
	}
	return sha
}
