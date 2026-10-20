package commands_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	forgeEntities "github.com/rios0rios0/gitforge/v4/pkg/global/domain/entities"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/rios0rios0/codeguru/internal/domain/commands"
	"github.com/rios0rios0/codeguru/internal/domain/entities"
	"github.com/rios0rios0/codeguru/internal/support"
	doubles "github.com/rios0rios0/codeguru/test/domain/doubles/repositories"
)

// tokenRecordingProvider adds the two identity methods the workspace path
// calls (Name, AuthToken) to the shared recording provider.
type tokenRecordingProvider struct {
	*recordingReviewProvider
}

func (p *tokenRecordingProvider) Name() string      { return "github" }
func (p *tokenRecordingProvider) AuthToken() string { return "fixture-provider-token-placeholder" }

// diffServingProvider adds the unified-diff download the diff path makes to
// the recording provider, answering with an empty diff.
type diffServingProvider struct {
	*tokenRecordingProvider
}

func (p *diffServingProvider) GetPullRequestDiff(
	_ context.Context, _ forgeEntities.Repository, _ int,
) (string, error) {
	return "", nil
}

func workspaceFixture() *entities.ReviewWorkspace {
	return &entities.ReviewWorkspace{
		Root: "/tmp/ws", Path: "/tmp/ws/repo", BaseCommit: "base", HeadCommit: "head",
		ChangedFiles: []entities.WorkspaceFileChange{
			{Path: "internal/foo.go", Status: "M", Additions: 2, Deletions: 1},
			{Path: "README.md", Status: "M", Additions: 1},
		},
	}
}

// bodiesOf lists every PR-wide comment a recorder received.
func bodiesOf(recorder *recordingReviewProvider) []string {
	bodies := make([]string, 0, len(recorder.calls))
	for _, call := range recorder.calls {
		bodies = append(bodies, call.body)
	}
	return bodies
}

func workspacePR() forgeEntities.PullRequestDetail {
	return forgeEntities.PullRequestDetail{
		ID: 77, Title: "feat: x", URL: "https://example/pr/77",
		SourceBranch: "feat/x",
		TargetBranch: "main",
	}
}

func TestExecuteWithWorkspaceReviewer(t *testing.T) {
	t.Parallel()

	repo := forgeEntities.Repository{Organization: "acme", Name: "app"}

	t.Run("should hand the checkout to the reviewer and release it afterwards", func(t *testing.T) {
		t.Parallel()

		// given
		ai := &doubles.StubAIReviewerRepository{
			NameValue: "claude", RequiresWorkspaceValue: true,
			Result: &entities.ReviewResult{Verdict: "approve", Summary: "fine"},
		}
		workspaces := &doubles.StubWorkspaceRepository{Workspace: workspaceFixture()}
		provider := &tokenRecordingProvider{&recordingReviewProvider{
			files: []forgeEntities.PullRequestFile{{Path: "/internal/foo.go"}, {Path: "/README.md"}},
		}}
		cmd := commands.NewReviewCommand(ai, &doubles.StubRulesRepository{}, nil, nil).WithWorkspace(workspaces)

		// when
		_, err := cmd.Execute(context.Background(), provider, repo, workspacePR(), commands.ReviewOptions{
			LoadProjectGuidelines: true,
		})

		// then
		require.NoError(t, err)
		assert.Equal(t, 1, workspaces.PrepareCalls)
		assert.Equal(t, []*entities.ReviewWorkspace{workspaces.Workspace}, workspaces.Released)
		assert.Same(t, workspaces.Workspace, ai.LastRequest.Workspace)
		assert.Empty(t, ai.LastRequest.ProjectGuidelines,
			"the reviewer reads AGENTS.md / CLAUDE.md from the checkout itself; the provider copy is not fetched")
		require.Len(t, ai.LastRequest.Diffs, 2)
		assert.Equal(t, "internal/foo.go", ai.LastRequest.Diffs[0].Path)
		assert.Empty(t, ai.LastRequest.Diffs[0].Diff, "the diff text is read from the checkout, not assembled")
	})

	t.Run("should post a failure notice when the pull request cannot be checked out", func(t *testing.T) {
		t.Parallel()

		// given
		ai := &doubles.StubAIReviewerRepository{NameValue: "claude", RequiresWorkspaceValue: true}
		workspaces := &doubles.StubWorkspaceRepository{Err: errors.New("fetch failed")}
		recorder := &recordingReviewProvider{files: []forgeEntities.PullRequestFile{{Path: "a.go"}}}
		provider := &tokenRecordingProvider{recorder}
		cmd := commands.NewReviewCommand(ai, &doubles.StubRulesRepository{}, nil, nil).WithWorkspace(workspaces)

		// when
		_, err := cmd.Execute(context.Background(), provider, repo, workspacePR(), commands.ReviewOptions{})

		// then
		require.ErrorIs(t, err, support.ErrWorkspaceUnavailable)
		require.Len(t, recorder.calls, 2, "the reviewing marker, then the failure notice that closes it")
		assert.Contains(t, recorder.calls[1].body, "**Code Guru review failed.**")
		assert.Contains(t, recorder.calls[1].body, "could not be checked out")
	})

	t.Run("should refuse a checkout that changes none of the files the provider lists", func(t *testing.T) {
		t.Parallel()

		// given: the shape of a fork's head fetched from a same-named branch
		// of the target repository — a valid checkout of the wrong code.
		ai := &doubles.StubAIReviewerRepository{
			NameValue: "claude", RequiresWorkspaceValue: true,
			Result: &entities.ReviewResult{Verdict: "approve", Summary: "nothing to see"},
		}
		wrong := workspaceFixture()
		wrong.ChangedFiles = []entities.WorkspaceFileChange{{Path: "upstream-only.go", Status: "A"}}
		workspaces := &doubles.StubWorkspaceRepository{Workspace: wrong}
		recorder := &recordingReviewProvider{files: []forgeEntities.PullRequestFile{{Path: "/internal/foo.go"}}}
		cmd := commands.NewReviewCommand(ai, &doubles.StubRulesRepository{}, nil, nil).WithWorkspace(workspaces)

		// when
		_, err := cmd.Execute(context.Background(), &tokenRecordingProvider{recorder}, repo, workspacePR(),
			commands.ReviewOptions{})

		// then
		require.ErrorIs(t, err, support.ErrWorkspaceUnavailable)
		assert.Nil(t, ai.LastRequest.Workspace, "the reviewer must never see the wrong checkout")
		assert.Equal(t, []*entities.ReviewWorkspace{wrong}, workspaces.Released)
		require.Len(t, recorder.calls, 2, "the reviewing marker, then the failure notice that closes it")
		assert.Contains(t, recorder.calls[1].body, "could not be checked out")
	})

	t.Run("should refuse a checkout that changes nothing at all", func(t *testing.T) {
		t.Parallel()

		// given
		ai := &doubles.StubAIReviewerRepository{NameValue: "claude", RequiresWorkspaceValue: true}
		empty := workspaceFixture()
		empty.ChangedFiles = nil
		workspaces := &doubles.StubWorkspaceRepository{Workspace: empty}
		recorder := &recordingReviewProvider{files: []forgeEntities.PullRequestFile{{Path: "a.go"}}}
		cmd := commands.NewReviewCommand(ai, &doubles.StubRulesRepository{}, nil, nil).WithWorkspace(workspaces)

		// when
		_, err := cmd.Execute(context.Background(), &tokenRecordingProvider{recorder}, repo, workspacePR(),
			commands.ReviewOptions{})

		// then
		require.ErrorIs(t, err, support.ErrWorkspaceUnavailable)
		assert.Contains(t, err.Error(), "the checkout changes 0 file(s)")
		assert.Equal(t, []*entities.ReviewWorkspace{empty}, workspaces.Released)
	})

	t.Run("should fail clearly when a workspace reviewer has no workspace repository", func(t *testing.T) {
		t.Parallel()

		// given
		ai := &doubles.StubAIReviewerRepository{NameValue: "claude", RequiresWorkspaceValue: true}
		provider := &tokenRecordingProvider{
			&recordingReviewProvider{files: []forgeEntities.PullRequestFile{{Path: "a.go"}}},
		}
		cmd := commands.NewReviewCommand(ai, &doubles.StubRulesRepository{}, nil, nil)

		// when
		_, err := cmd.Execute(context.Background(), provider, repo, workspacePR(), commands.ReviewOptions{})

		// then
		require.ErrorIs(t, err, support.ErrWorkspaceUnavailable)
		assert.Contains(t, err.Error(), "no workspace repository is configured")
	})

	t.Run("should post scored findings with their header and redact the provider token", func(t *testing.T) {
		t.Parallel()

		// given: a review that quotes the provider token in a finding.
		ai := &doubles.StubAIReviewerRepository{
			NameValue: "claude", RequiresWorkspaceValue: true,
			Result: &entities.ReviewResult{
				Verdict: "request_changes",
				Comments: []entities.ReviewComment{{
					FilePath: "internal/foo.go", Line: 3, Severity: "error", Dimension: "security", Confidence: 92,
					Body: "token fixture-provider-token-placeholder is logged",
				}},
			},
		}
		workspaces := &doubles.StubWorkspaceRepository{Workspace: workspaceFixture()}
		recorder := &recordingReviewProvider{files: []forgeEntities.PullRequestFile{{Path: "/internal/foo.go"}}}
		cmd := commands.NewReviewCommand(ai, &doubles.StubRulesRepository{}, nil, nil).WithWorkspace(workspaces)

		// when
		_, err := cmd.Execute(context.Background(), &tokenRecordingProvider{recorder}, repo, workspacePR(),
			commands.ReviewOptions{})

		// then
		require.NoError(t, err)
		require.Len(t, recorder.threadComments, 1)
		assert.Equal(t, "**security (confidence 92)**\n\ntoken [redacted] is logged", recorder.threadComments[0].body)
	})

	t.Run("should post a path-less scored finding without an empty path, and a diff-path one as before",
		func(t *testing.T) {
			t.Parallel()

			// given: one pull-request-wide finding from each reviewer.
			agentic := &doubles.StubAIReviewerRepository{
				NameValue: "claude", RequiresWorkspaceValue: true,
				Result: &entities.ReviewResult{Verdict: "comment", Comments: []entities.ReviewComment{{
					Severity: "warning", Dimension: "design", Confidence: 82, Body: "split this change",
				}}},
			}
			diff := &doubles.StubAIReviewerRepository{
				NameValue: "claude",
				Result: &entities.ReviewResult{Verdict: "comment", Comments: []entities.ReviewComment{{
					Severity: "warning", Body: "split this change",
				}}},
			}
			agenticRecorder := &recordingReviewProvider{
				files: []forgeEntities.PullRequestFile{{Path: "/internal/foo.go"}},
			}
			diffRecorder := &recordingReviewProvider{files: []forgeEntities.PullRequestFile{{Path: "/internal/foo.go"}}}
			agenticCmd := commands.NewReviewCommand(agentic, &doubles.StubRulesRepository{}, nil, nil).
				WithWorkspace(&doubles.StubWorkspaceRepository{Workspace: workspaceFixture()})
			diffCmd := commands.NewReviewCommand(diff, &doubles.StubRulesRepository{}, nil, nil)

			// when
			_, agenticErr := agenticCmd.Execute(context.Background(), &tokenRecordingProvider{agenticRecorder}, repo,
				workspacePR(), commands.ReviewOptions{})
			_, diffErr := diffCmd.Execute(
				context.Background(),
				&diffServingProvider{
					&tokenRecordingProvider{diffRecorder},
				},
				repo,
				workspacePR(),
				commands.ReviewOptions{},
			)

			// then
			require.NoError(t, agenticErr)
			require.NoError(t, diffErr)
			assert.Contains(
				t,
				bodiesOf(agenticRecorder),
				"**[warning]** **design (confidence 82)**\n\nsplit this change",
			)
			assert.Contains(t, bodiesOf(diffRecorder), "**[warning]** : split this change",
				"the diff path's comments keep their exact shape")
		})

	t.Run("should stamp the review Job's name on the completion notice", func(t *testing.T) {
		t.Parallel()

		// given
		ai := &doubles.StubAIReviewerRepository{
			NameValue: "claude", RequiresWorkspaceValue: true,
			Result: &entities.ReviewResult{Verdict: "approve", Summary: "fine"},
		}
		workspaces := &doubles.StubWorkspaceRepository{Workspace: workspaceFixture()}
		recorder := &recordingReviewProvider{files: []forgeEntities.PullRequestFile{{Path: "/internal/foo.go"}}}
		cmd := commands.NewReviewCommand(ai, &doubles.StubRulesRepository{}, nil, nil).
			WithWorkspace(workspaces).
			WithJobName("code-guru-review-abc")

		// when
		_, err := cmd.Execute(context.Background(), &tokenRecordingProvider{recorder}, repo, workspacePR(),
			commands.ReviewOptions{})

		// then
		require.NoError(t, err)
		bodies := make([]string, 0, len(recorder.calls))
		for _, call := range recorder.calls {
			bodies = append(bodies, call.body)
		}
		assert.True(t, support.HasReviewOutcomeOf(bodies, "code-guru-review-abc"),
			"the server must be able to tell this Job's outcome from an earlier review's")
	})

	t.Run("should stamp the review Job's name on the failure notice", func(t *testing.T) {
		t.Parallel()

		// given
		ai := &doubles.StubAIReviewerRepository{NameValue: "claude", RequiresWorkspaceValue: true}
		workspaces := &doubles.StubWorkspaceRepository{Err: errors.New("fetch failed")}
		recorder := &recordingReviewProvider{files: []forgeEntities.PullRequestFile{{Path: "a.go"}}}
		cmd := commands.NewReviewCommand(ai, &doubles.StubRulesRepository{}, nil, nil).
			WithWorkspace(workspaces).
			WithJobName("code-guru-review-abc")

		// when
		_, err := cmd.Execute(context.Background(), &tokenRecordingProvider{recorder}, repo, workspacePR(),
			commands.ReviewOptions{})

		// then
		require.ErrorIs(t, err, support.ErrWorkspaceUnavailable)
		require.Len(t, recorder.calls, 2)
		assert.True(t, strings.HasSuffix(recorder.calls[1].body, " · review job `code-guru-review-abc`._"))
	})
}

func TestTriage(t *testing.T) {
	t.Parallel()

	repo := forgeEntities.Repository{Organization: "acme", Name: "app"}

	t.Run("should report that an AI review is needed without posting anything", func(t *testing.T) {
		t.Parallel()

		// given
		ai := &doubles.StubAIReviewerRepository{NameValue: "claude"}
		recorder := &recordingReviewProvider{files: []forgeEntities.PullRequestFile{{Path: "a.go"}}}
		cmd := commands.NewReviewCommand(ai, &doubles.StubRulesRepository{}, nil, nil)

		// when
		result, needsReview, err := cmd.Triage(
			context.Background(),
			recorder,
			repo,
			workspacePR(),
			commands.ReviewOptions{},
		)

		// then
		require.NoError(t, err)
		assert.True(t, needsReview)
		assert.Nil(t, result)
		assert.Empty(t, recorder.calls, "triage posts no reviewing marker")
		assert.Empty(t, ai.LastRequest.PullRequest.Title, "triage never calls the AI")
	})

	t.Run("should settle a draft", func(t *testing.T) {
		t.Parallel()

		// given
		cmd := commands.NewReviewCommand(&doubles.StubAIReviewerRepository{}, &doubles.StubRulesRepository{}, nil, nil)
		pr := workspacePR()
		pr.IsDraft = true

		// when
		result, needsReview, err := cmd.Triage(context.Background(), &recordingReviewProvider{}, repo, pr,
			commands.ReviewOptions{})

		// then
		require.NoError(t, err)
		assert.False(t, needsReview)
		assert.Equal(t, "skipped: pull request is a draft", result.Summary)
	})

	t.Run("should settle a pull request with no files", func(t *testing.T) {
		t.Parallel()

		// given
		cmd := commands.NewReviewCommand(&doubles.StubAIReviewerRepository{}, &doubles.StubRulesRepository{}, nil, nil)

		// when
		result, needsReview, err := cmd.Triage(context.Background(), &recordingReviewProvider{}, repo, workspacePR(),
			commands.ReviewOptions{})

		// then
		require.NoError(t, err)
		assert.False(t, needsReview)
		assert.Equal(t, "no files changed", result.Summary)
	})

	t.Run("should surface a file-listing failure", func(t *testing.T) {
		t.Parallel()

		// given
		cmd := commands.NewReviewCommand(&doubles.StubAIReviewerRepository{}, &doubles.StubRulesRepository{}, nil, nil)
		provider := &recordingReviewProvider{getPullRequestFilesErr: errors.New("api down")}

		// when
		_, needsReview, err := cmd.Triage(context.Background(), provider, repo, workspacePR(), commands.ReviewOptions{})

		// then
		require.Error(t, err)
		assert.False(t, needsReview)
	})
}

func TestRenderCommentBody(t *testing.T) {
	t.Parallel()

	t.Run("should prefix a scored finding and leave a diff-path comment as written", func(t *testing.T) {
		t.Parallel()

		// given
		scored := entities.ReviewComment{Body: "nil deref", Dimension: "bug", Confidence: 85}
		plain := entities.ReviewComment{Body: "nil deref"}

		// when
		renderedScored, renderedPlain := commands.RenderCommentBody(scored), commands.RenderCommentBody(plain)

		// then
		assert.Equal(t, "**bug (confidence 85)**\n\nnil deref", renderedScored)
		assert.Equal(t, "nil deref", renderedPlain)
	})

	t.Run("should match a re-posted finding whose score moved as a duplicate", func(t *testing.T) {
		t.Parallel()

		// given: the posted body carries the header; the new finding scored differently.
		posted := commands.RenderCommentBody(
			entities.ReviewComment{Body: "nil deref", Dimension: "bug", Confidence: 85},
		)

		// when
		fresh, reposted := commands.CommentDedupKey("a.go", 3, "nil deref"), commands.CommentDedupKey("a.go", 3, posted)

		// then
		assert.Equal(t, fresh, reposted,
			"the dedup fingerprint ignores the header, so a moved score is still the same finding")
	})
}

func TestBuildReviewReportBlock(t *testing.T) {
	t.Parallel()

	t.Run("should render the findings index, the per-dimension coverage and every dropped finding", func(t *testing.T) {
		t.Parallel()

		// given
		result := &entities.ReviewResult{
			Comments: []entities.ReviewComment{
				{FilePath: "/a.go", Line: 3, Severity: "error", Dimension: "bug", Confidence: 90, Body: "x"},
				{FilePath: "", Line: 0, Severity: "warning", Dimension: "design", Confidence: 82, Body: "y"},
			},
			Coverage: entities.ReviewCoverage{
				Correctness: "Read every changed\nfunction.",
				Security:    "No new input paths.",
			},
			Dropped: []entities.DroppedFinding{
				{FilePath: "b.go", Line: 9, Dimension: "design", Confidence: 40, Reason: "nit"},
			},
		}

		// when
		block := commands.BuildReviewReportBlock(result)

		// then
		assert.Contains(
			t,
			block,
			"**Findings**\n1. `a.go:3` — bug (confidence 90), error\n2. (pull request) — design (confidence 82), warning\n",
		)
		assert.Contains(t, block, "- Correctness: Read every changed function.\n- Security: No new input paths.\n")
		assert.NotContains(t, block, "Design:", "an empty dimension statement is left out")
		assert.Contains(
			t,
			block,
			"**Dropped** (did not reach the confidence bar)\n- `b.go:9` — design (confidence 40): nit\n",
		)
	})

	t.Run("should render nothing for a diff-path review, keeping its notice unchanged", func(t *testing.T) {
		t.Parallel()

		// given
		result := &entities.ReviewResult{Comments: []entities.ReviewComment{{FilePath: "a.go", Line: 1, Body: "x"}}}

		// when
		block := commands.BuildReviewReportBlock(result)
		notice := commands.BuildReviewCompleteBody(time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC), result)

		// then
		assert.Empty(t, block)
		assert.Equal(
			t,
			"✅ **Code Guru review complete.**\n\nVerdict: `comment` · 1 inline comment.\n\n_Completed at 2026-10-19T12:00:00Z._",
			notice,
		)
	})

	t.Run("should bound a pathological dropped list and count the rest", func(t *testing.T) {
		t.Parallel()

		// given
		var dropped []entities.DroppedFinding
		for range 45 {
			dropped = append(dropped, entities.DroppedFinding{Reason: strings.Repeat("r", 400), Confidence: 10})
		}

		// when
		block := commands.BuildReviewReportBlock(&entities.ReviewResult{Dropped: dropped})

		// then
		assert.Contains(t, block, "- …and 5 more\n")
		assert.NotContains(t, block, strings.Repeat("r", 301), "each entry is cut to its bound")
	})
}

func TestReviewJobNotices(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)

	t.Run("should say how many reviews are ahead without setting the review-once gate", func(t *testing.T) {
		t.Parallel()

		// given
		ahead := []int{1, 3}

		// when
		first := commands.BuildReviewQueuedBody(now, ahead[0])
		third := commands.BuildReviewQueuedBody(now, ahead[1])

		// then
		assert.Contains(t, first, "It is next in line")
		assert.Contains(t, third, "2 reviews are ahead of it")
		assert.Contains(t, third, "**Code Guru ", "the annotation marker lets re-reviews recognise the bot")
		assert.NotContains(t, third, "**Code Guru review", "the review-once gate must stay open until the review runs")
	})

	t.Run(
		"should say a requested re-review waits for the running review, without setting the gate",
		func(t *testing.T) {
			t.Parallel()

			// given
			queuedAt := now

			// when
			body := commands.BuildReReviewQueuedBody(queuedAt)

			// then
			assert.Contains(t, body, "**Code Guru queued the re-review you asked for.**")
			assert.NotContains(t, body, "**Code Guru review")
		},
	)

	t.Run("should explain known failure reasons and set the review-once gate", func(t *testing.T) {
		t.Parallel()

		// given
		known, novel := "DeadlineExceeded", "SomethingNew"

		// when
		deadline := commands.BuildReviewJobFailedBody(now, known)
		unknown := commands.BuildReviewJobFailedBody(now, novel)

		// then
		assert.Contains(t, deadline, "ran past its time limit")
		assert.Contains(t, unknown, "it failed (`SomethingNew`)")
		assert.Contains(t, deadline, "**Code Guru review failed.**")
		assert.True(t, support.HasReviewOutcomeOf([]string{support.StampReviewJob(deadline, "job-1")}, "job-1"),
			"the scheduler must recognise its own notice as an outcome")
	})
}
