package workspace_test

import (
	"context"
	"encoding/base64"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	forgeEntities "github.com/rios0rios0/gitforge/v4/pkg/global/domain/entities"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/rios0rios0/codeguru/internal/domain/entities"
	"github.com/rios0rios0/codeguru/internal/infrastructure/repositories/workspace"
)

// originRepo is a throwaway "hosting provider" repository on disk: `main` plus
// a feature branch, built with an isolated git configuration so the developer's
// own `~/.gitconfig` never leaks into the test.
type originRepo struct {
	t   *testing.T
	dir string
}

func newOriginRepo(t *testing.T) *originRepo {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	origin := &originRepo{t: t, dir: t.TempDir()}
	origin.git("init", "--quiet", "--initial-branch=main", ".")
	return origin
}

func (o *originRepo) git(args ...string) string {
	o.t.Helper()
	cmd := exec.Command("git", append([]string{
		"-c", "user.name=Fixture", "-c", "user.email=fixture@example.com", "-c", "commit.gpgsign=false",
	}, args...)...)
	cmd.Dir = o.dir
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_NOSYSTEM=1")
	out, err := cmd.CombinedOutput()
	require.NoError(o.t, err, "git %v: %s", args, out)
	return strings.TrimSpace(string(out))
}

func (o *originRepo) write(path, content string) {
	o.t.Helper()
	full := filepath.Join(o.dir, path)
	// nosemgrep: go.lang.correctness.permissions.file_permission.incorrect-default-permission
	require.NoError(o.t, os.MkdirAll(filepath.Dir(full), 0o700))
	require.NoError(o.t, os.WriteFile(full, []byte(content), 0o600))
}

func (o *originRepo) commit(message string) string {
	o.t.Helper()
	o.git("add", "--all")
	o.git("commit", "--quiet", "-m", message)
	return o.git("rev-parse", "HEAD")
}

func (o *originRepo) url() string {
	return "file://" + o.dir
}

func pullRequest(source, target string) forgeEntities.PullRequestDetail {
	return forgeEntities.PullRequestDetail{
		ID: 7, Title: "feat: fixture",
		SourceBranch: source,
		TargetBranch: target,
	}
}

// branchProvider is a gitforge ReviewProvider stub that answers only what
// Prepare needs; any other call panics on the nil embedded interface, by
// design.
type branchProvider struct {
	forgeEntities.ReviewProvider

	name string
	open []forgeEntities.PullRequestDetail
}

func (p *branchProvider) Name() string      { return p.name }
func (p *branchProvider) AuthToken() string { return "fixture-token-placeholder" }

// stubForkChecker answers the fork check with a canned result and records
// whether it was asked.
type stubForkChecker struct {
	fork  bool
	err   error
	asked bool
}

func (c *stubForkChecker) IsFork(context.Context, string, forgeEntities.Repository, int) (bool, error) {
	c.asked = true
	return c.fork, c.err
}

func (p *branchProvider) ListOpenPullRequests(
	_ context.Context, _ forgeEntities.Repository,
) ([]forgeEntities.PullRequestDetail, error) {
	return p.open, nil
}

func TestGitWorkspaceRepository_PrepareFromURL(t *testing.T) {
	t.Parallel()

	t.Run("should check out the source head with the merge-base and every kind of change", func(t *testing.T) {
		t.Parallel()

		// given
		origin := newOriginRepo(t)
		origin.write("keep.go", "package keep\n")
		origin.write("edit.go", "package edit\n\nconst a = 1\n")
		origin.write("drop.go", "package drop\n")
		origin.write("old-name.go", "package moved\n\nconst unchanged = true\n")
		base := origin.commit("initial")
		origin.git("switch", "--quiet", "-c", "feat/x")
		origin.write("edit.go", "package edit\n\nconst a = 2\nconst b = 3\n")
		origin.write("added dir/new file.go", "package added\n")
		require.NoError(t, os.Remove(filepath.Join(origin.dir, "drop.go")))
		origin.git("mv", "old-name.go", "new-name.go")
		origin.write("logo.bin", "\x00\x01\x02binary")
		head := origin.commit("feature")
		origin.git("switch", "--quiet", "main")
		origin.write("main-only.go", "package mainonly\n")
		origin.commit("target moves on")
		repo := workspace.NewGitWorkspaceRepository(t.TempDir(), 0)

		// when
		ws, err := repo.PrepareFromURL(context.Background(), origin.url(), pullRequest("feat/x", "main"))

		// then
		require.NoError(t, err)
		t.Cleanup(func() { repo.Release(ws) })
		assert.Equal(t, head, ws.HeadCommit)
		assert.Equal(t, base, ws.BaseCommit, "the merge-base, not the target tip, is the left side of the diff")
		assert.FileExists(t, filepath.Join(ws.Path, "added dir", "new file.go"))
		assert.NoFileExists(t, filepath.Join(ws.Path, "main-only.go"), "the checkout is the source head")
		changes := map[string]entities.WorkspaceFileChange{}
		for _, change := range ws.ChangedFiles {
			changes[change.Path] = change
		}
		assert.Equal(t, "M", changes["edit.go"].Status)
		assert.Equal(t, 2, changes["edit.go"].Additions)
		assert.Equal(t, 1, changes["edit.go"].Deletions)
		assert.Equal(t, "A", changes["added dir/new file.go"].Status, "paths with spaces survive the -z parsing")
		assert.Equal(t, "D", changes["drop.go"].Status)
		assert.Equal(t, "R", changes["new-name.go"].Status, "a rename is reported under its new path")
		assert.True(t, changes["logo.bin"].Binary)
		assert.NotContains(t, changes, "main-only.go", "a target-only change is not part of the pull request")
	})

	t.Run("should deepen a shallow fetch until the merge-base is reachable", func(t *testing.T) {
		t.Parallel()

		// given: the branches diverged five commits back on each side, far
		// beyond a depth of one.
		origin := newOriginRepo(t)
		origin.write("a.txt", "0\n")
		base := origin.commit("base")
		origin.git("switch", "--quiet", "-c", "feat/long")
		for i := range 5 {
			origin.write("a.txt", strings.Repeat("s", i+1)+"\n")
			origin.commit("source")
		}
		origin.git("switch", "--quiet", "main")
		for i := range 5 {
			origin.write("b.txt", strings.Repeat("t", i+1)+"\n")
			origin.commit("target")
		}
		repo := workspace.NewGitWorkspaceRepository(t.TempDir(), 1)

		// when
		ws, err := repo.PrepareFromURL(context.Background(), origin.url(), pullRequest("feat/long", "main"))

		// then
		require.NoError(t, err)
		t.Cleanup(func() { repo.Release(ws) })
		assert.Equal(t, base, ws.BaseCommit)
		require.Len(t, ws.ChangedFiles, 1)
		assert.Equal(t, "a.txt", ws.ChangedFiles[0].Path)
	})

	t.Run("should check a symlink out as a plain file holding its target", func(t *testing.T) {
		t.Parallel()

		// given: a pull request that adds a link pointing at the process
		// environment, the classic way to make a file-reading agent leak it.
		origin := newOriginRepo(t)
		origin.write("README.md", "fixture\n")
		origin.commit("initial")
		origin.git("switch", "--quiet", "-c", "feat/link")
		require.NoError(t, os.Symlink("/proc/self/environ", filepath.Join(origin.dir, "notes.md")))
		origin.commit("add a link")
		repo := workspace.NewGitWorkspaceRepository(t.TempDir(), 0)

		// when
		ws, err := repo.PrepareFromURL(context.Background(), origin.url(), pullRequest("feat/link", "main"))

		// then
		require.NoError(t, err)
		t.Cleanup(func() { repo.Release(ws) })
		info, statErr := os.Lstat(filepath.Join(ws.Path, "notes.md"))
		require.NoError(t, statErr)
		assert.Zero(t, info.Mode()&os.ModeSymlink, "the checkout must not contain a live symlink")
		content, readErr := os.ReadFile(filepath.Join(ws.Path, "notes.md"))
		require.NoError(t, readErr)
		assert.Equal(t, "/proc/self/environ", string(content))
	})

	t.Run("should check out the pull request head ref, not a same-named branch of the target", func(t *testing.T) {
		t.Parallel()

		// given: a fork's pull request from its `feature` branch, while the
		// target repository has a `feature` branch of its own. Only the
		// head ref the provider publishes holds the fork's commit.
		origin := newOriginRepo(t)
		origin.write("a.go", "package a\n")
		origin.commit("initial")
		origin.git("switch", "--quiet", "-c", "feature")
		origin.write("upstream.go", "package upstream\n")
		origin.commit("the target repository's own feature branch")
		origin.git("switch", "--quiet", "-c", "fork-work", "main")
		origin.write("fork.go", "package fork\n")
		forkHead := origin.commit("the fork's change")
		origin.git("update-ref", "refs/pull/7/head", forkHead)
		origin.git("switch", "--quiet", "main")
		origin.git("branch", "--quiet", "-D", "fork-work")
		repo := workspace.NewGitWorkspaceRepository(t.TempDir(), 0)

		// when
		ws, err := repo.PrepareFromURLWithPullHeadRef(
			context.Background(), origin.url(), "refs/pull/%d/head", pullRequest("feature", "main"),
		)

		// then
		require.NoError(t, err)
		t.Cleanup(func() { repo.Release(ws) })
		assert.Equal(t, forkHead, ws.HeadCommit)
		require.Len(t, ws.ChangedFiles, 1)
		assert.Equal(t, "fork.go", ws.ChangedFiles[0].Path)
	})

	t.Run("should review a fork's main branch into the target's main branch", func(t *testing.T) {
		t.Parallel()

		// given: source and target branch share a name, the usual shape of a
		// pull request from a fork's default branch.
		origin := newOriginRepo(t)
		origin.write("a.go", "package a\n")
		base := origin.commit("initial")
		origin.git("switch", "--quiet", "-c", "fork-main")
		origin.write("a.go", "package a\n\nconst fixed = true\n")
		forkHead := origin.commit("the fork's fix")
		origin.git("update-ref", "refs/pull/7/head", forkHead)
		origin.git("switch", "--quiet", "main")
		origin.git("branch", "--quiet", "-D", "fork-main")
		repo := workspace.NewGitWorkspaceRepository(t.TempDir(), 0)

		// when
		ws, err := repo.PrepareFromURLWithPullHeadRef(
			context.Background(), origin.url(), "refs/pull/%d/head", pullRequest("main", "main"),
		)

		// then
		require.NoError(t, err)
		t.Cleanup(func() { repo.Release(ws) })
		assert.Equal(t, forkHead, ws.HeadCommit)
		assert.Equal(t, base, ws.BaseCommit)
		require.Len(t, ws.ChangedFiles, 1)
		assert.Equal(t, "a.go", ws.ChangedFiles[0].Path)
	})

	t.Run("should fail and leave nothing behind when the source branch does not exist", func(t *testing.T) {
		t.Parallel()

		// given
		origin := newOriginRepo(t)
		origin.write("a.txt", "a\n")
		origin.commit("initial")
		base := t.TempDir()
		repo := workspace.NewGitWorkspaceRepository(base, 0)

		// when
		ws, err := repo.PrepareFromURL(context.Background(), origin.url(), pullRequest("feat/missing", "main"))

		// then
		require.Error(t, err)
		assert.Nil(t, ws)
		entries, readErr := os.ReadDir(base)
		require.NoError(t, readErr)
		assert.Empty(t, entries, "a failed checkout must release its workspace directory")
	})
}

func TestGitWorkspaceRepository_Prepare(t *testing.T) {
	t.Parallel()

	t.Run("should refuse a provider without clone support", func(t *testing.T) {
		t.Parallel()

		// given
		repo := workspace.NewGitWorkspaceRepository(t.TempDir(), 0)
		provider := &branchProvider{name: "gitlab"}

		// when
		ws, err := repo.Prepare(
			context.Background(),
			provider,
			forgeEntities.Repository{Name: "r"},
			pullRequest("a", "b"),
		)

		// then
		require.ErrorContains(t, err, "no workspace clone support")
		assert.Nil(t, ws)
	})

	t.Run("should refuse a branch name git would read as an option", func(t *testing.T) {
		t.Parallel()

		// given
		repo := workspace.NewGitWorkspaceRepository(t.TempDir(), 0)
		provider := &branchProvider{name: "github"}
		target := forgeEntities.Repository{Organization: "acme", Name: "app"}

		// when
		ws, err := repo.Prepare(context.Background(), provider, target, pullRequest("--upload-pack=evil", "main"))

		// then
		require.ErrorContains(t, err, "refusing to check out")
		assert.Nil(t, ws)
	})

	t.Run("should refuse an Azure DevOps pull request from a fork before cloning anything", func(t *testing.T) {
		t.Parallel()

		// given: Azure DevOps publishes no head ref, so a fork's branch could
		// only be fetched by name from the target repository.
		base := t.TempDir()
		checker := &stubForkChecker{fork: true}
		repo := workspace.NewGitWorkspaceRepository(base, 0).WithForkChecker("azuredevops", checker)
		provider := &branchProvider{name: "azuredevops"}
		target := forgeEntities.Repository{Organization: "org", Project: "proj", Name: "app"}

		// when
		ws, err := repo.Prepare(context.Background(), provider, target, pullRequest("feature", "main"))

		// then
		require.ErrorContains(t, err, "comes from a fork")
		assert.Nil(t, ws)
		entries, readErr := os.ReadDir(base)
		require.NoError(t, readErr)
		assert.Empty(t, entries, "nothing is cloned for a refused pull request")
	})

	t.Run("should refuse to check out when the fork status cannot be read", func(t *testing.T) {
		t.Parallel()

		// given
		checker := &stubForkChecker{err: errors.New("401 Unauthorized")}
		repo := workspace.NewGitWorkspaceRepository(t.TempDir(), 0).WithForkChecker("azuredevops", checker)
		provider := &branchProvider{name: "azuredevops"}
		target := forgeEntities.Repository{Organization: "org", Project: "proj", Name: "app"}

		// when
		ws, err := repo.Prepare(context.Background(), provider, target, pullRequest("feature", "main"))

		// then
		require.ErrorContains(t, err, "checking whether PR #7 comes from a fork")
		assert.Nil(t, ws)
	})

	t.Run("should not ask about forks where the provider publishes a head ref", func(t *testing.T) {
		t.Parallel()

		// given: GitHub's refs/pull/<id>/head is right for a fork already.
		checker := &stubForkChecker{fork: true}
		repo := workspace.NewGitWorkspaceRepository(t.TempDir(), 0).WithForkChecker("github", checker)
		provider := &branchProvider{name: "github"}
		target := forgeEntities.Repository{Organization: "acme", Name: "app"}

		// when
		err := repo.RefuseUnreachableFork(context.Background(), provider, target, 7)

		// then
		require.NoError(t, err)
		assert.False(t, checker.asked)
	})

	t.Run("should fail when the branches cannot be resolved because the pull request is not open", func(t *testing.T) {
		t.Parallel()

		// given: a comment-event pull request entity carries no branches.
		repo := workspace.NewGitWorkspaceRepository(t.TempDir(), 0)
		provider := &branchProvider{name: "github", open: []forgeEntities.PullRequestDetail{pullRequest("x", "y")}}
		target := forgeEntities.Repository{Organization: "acme", Name: "app"}
		pr := forgeEntities.PullRequestDetail{ID: 99}

		// when
		ws, err := repo.Prepare(context.Background(), provider, target, pr)

		// then
		require.ErrorContains(t, err, "is not open")
		assert.Nil(t, ws)
	})
}

func TestGitWorkspaceRepository_Release(t *testing.T) {
	t.Parallel()

	t.Run("should remove the workspace root", func(t *testing.T) {
		t.Parallel()

		// given
		root := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(root, "f"), []byte("x"), 0o600))
		repo := workspace.NewGitWorkspaceRepository("", 0)

		// when
		repo.Release(&entities.ReviewWorkspace{Root: root})

		// then
		assert.NoDirExists(t, root)
	})

	t.Run("should ignore a nil workspace", func(t *testing.T) {
		t.Parallel()

		// given
		repo := workspace.NewGitWorkspaceRepository("", 0)

		// when
		release := func() { repo.Release(nil) }

		// then
		assert.NotPanics(t, release)
	})
}

func TestCloneTargets(t *testing.T) {
	t.Parallel()

	t.Run("should build URLs on constant hosts with every name path-escaped", func(t *testing.T) {
		t.Parallel()

		// given
		adoRepo := forgeEntities.Repository{
			Organization: "Example Org",
			Project:      "My Project",
			Name:         "my repo",
			RemoteURL:    "https://attacker.example/steal",
		}

		// when
		adoURL, adoErr := workspace.CloneURLFor("azuredevops", adoRepo)
		ghURL, ghErr := workspace.CloneURLFor("github", forgeEntities.Repository{Organization: "acme", Name: "app"})

		// then
		require.NoError(t, adoErr)
		require.NoError(t, ghErr)
		assert.Equal(t, "https://dev.azure.com/Example%20Org/My%20Project/_git/my%20repo", adoURL,
			"RemoteURL is ignored: it must never redirect an authenticated fetch")
		assert.Equal(t, "https://github.com/acme/app.git", ghURL)
	})

	t.Run("should scope the token to the provider host and keep it out of the URL", func(t *testing.T) {
		t.Parallel()

		// given
		repo := forgeEntities.Repository{Organization: "acme", Name: "app"}

		// when
		env, err := workspace.AuthEnvFor("github", repo, "fixture-token-placeholder")

		// then
		require.NoError(t, err)
		credentials := base64.StdEncoding.EncodeToString([]byte("x-access-token:fixture-token-placeholder"))
		assert.Equal(t, []string{
			"GIT_CONFIG_COUNT=1",
			"GIT_CONFIG_KEY_0=http.https://github.com/.extraHeader",
			"GIT_CONFIG_VALUE_0=Authorization: Basic " + credentials,
		}, env)
	})

	t.Run("should add no auth environment for an empty token", func(t *testing.T) {
		t.Parallel()

		// given
		repo := forgeEntities.Repository{Organization: "o", Project: "p", Name: "r"}

		// when
		env, err := workspace.AuthEnvFor("azuredevops", repo, "")

		// then
		require.NoError(t, err)
		assert.Empty(t, env)
	})
}

func TestParseChanges(t *testing.T) {
	t.Parallel()

	t.Run("should join rename and copy records on their new path", func(t *testing.T) {
		t.Parallel()

		// given: raw `-z` output for a modify, a rename and a binary add.
		nameStatus := "M\x00a.go\x00R091\x00old.go\x00new.go\x00A\x00img.png\x00"
		numStat := "3\t1\ta.go\x005\t2\t\x00old.go\x00new.go\x00-\t-\timg.png\x00"

		// when
		changes := workspace.ParseChanges(nameStatus, numStat)

		// then
		assert.Equal(t, []entities.WorkspaceFileChange{
			{Path: "a.go", Status: "M", Additions: 3, Deletions: 1},
			{Path: "new.go", Status: "R", Additions: 5, Deletions: 2},
			{Path: "img.png", Status: "A", Binary: true},
		}, changes)
	})
}
