package commands

import (
	"context"
	"fmt"

	logger "github.com/sirupsen/logrus"

	forgeEntities "github.com/rios0rios0/gitforge/domain/entities"
	forgeRepos "github.com/rios0rios0/gitforge/domain/repositories"

	"github.com/rios0rios0/codeguru/internal/domain/entities"
	"github.com/rios0rios0/codeguru/internal/domain/repositories"
	"github.com/rios0rios0/codeguru/internal/support"
)

// Review is the interface for the review command.
type Review interface {
	Execute(
		ctx context.Context,
		provider forgeRepos.ReviewProvider,
		repo forgeEntities.Repository,
		pr forgeEntities.PullRequestDetail,
		opts ReviewOptions,
	) (*entities.ReviewResult, error)
}

// ReviewOptions holds runtime options for a single review.
type ReviewOptions struct {
	DryRun  bool
	Verbose bool
}

// ReviewCommand orchestrates a single PR review.
type ReviewCommand struct {
	aiReviewer repositories.AIReviewerRepository
	rulesRepo  repositories.RulesRepository
}

// NewReviewCommand creates a new ReviewCommand.
func NewReviewCommand(
	aiReviewer repositories.AIReviewerRepository,
	rulesRepo repositories.RulesRepository,
) *ReviewCommand {
	return &ReviewCommand{
		aiReviewer: aiReviewer,
		rulesRepo:  rulesRepo,
	}
}

// Execute performs a review of a single pull request.
func (c *ReviewCommand) Execute(
	ctx context.Context,
	provider forgeRepos.ReviewProvider,
	repo forgeEntities.Repository,
	pr forgeEntities.PullRequestDetail,
	opts ReviewOptions,
) (*entities.ReviewResult, error) {
	logger.Infof("reviewing PR #%d: %s", pr.ID, pr.Title)

	// fetch changed files
	files, err := provider.GetPullRequestFiles(ctx, repo, pr.ID)
	if err != nil {
		return nil, fmt.Errorf("failed to get PR files: %w", err)
	}

	if len(files) == 0 {
		logger.Infof("PR #%d has no changed files, skipping", pr.ID)
		return &entities.ReviewResult{PullRequestURL: pr.URL, Summary: "no files changed"}, nil
	}

	// classify languages and build diffs
	var paths []string
	var diffs []entities.FileDiff
	for _, f := range files {
		paths = append(paths, f.Path)
		diffs = append(diffs, entities.FileDiff{
			Path:     f.Path,
			Diff:     f.Patch,
			Language: support.ClassifyFile(f.Path),
		})
	}

	// fallback: if all patches are empty (e.g. Azure DevOps), fetch the full unified diff
	if allDiffsEmpty(diffs) {
		logger.Debugf("no per-file patches available, fetching full unified diff for PR #%d", pr.ID)
		fullDiff, diffErr := provider.GetPullRequestDiff(ctx, repo, pr.ID)
		if diffErr != nil {
			return nil, fmt.Errorf("failed to get PR diff: %w", diffErr)
		}

		chunks := support.SplitUnifiedDiff(fullDiff)
		for i := range diffs {
			if chunk, ok := chunks[diffs[i].Path]; ok {
				diffs[i].Diff = chunk
			}
		}
	}

	// load rules for detected languages
	languages := support.ClassifyFiles(paths)
	rules, err := c.rulesRepo.LoadForLanguages(languages, paths)
	if err != nil {
		return nil, fmt.Errorf("failed to load rules: %w", err)
	}

	logger.Infof("loaded %d rules for languages: %v", len(rules), languages)

	// build review request and call AI
	request := entities.ReviewRequest{
		Repository:  repo,
		PullRequest: pr,
		Diffs:       diffs,
		Rules:       rules,
	}

	result, err := c.aiReviewer.ReviewDiff(ctx, request)
	if err != nil {
		return nil, fmt.Errorf("AI review failed: %w", err)
	}

	result.PullRequestURL = pr.URL

	if !opts.DryRun {
		c.postComments(ctx, provider, repo, pr.ID, result)
	}

	return result, nil
}

func (c *ReviewCommand) postComments(
	ctx context.Context,
	provider forgeRepos.ReviewProvider,
	repo forgeEntities.Repository,
	prID int,
	result *entities.ReviewResult,
) {
	if result.Summary != "" {
		if err := provider.PostPullRequestComment(ctx, repo, prID, result.Summary); err != nil {
			logger.Errorf("failed to post summary comment: %v", err)
		}
	}

	for _, comment := range result.Comments {
		if comment.Line > 0 {
			err := provider.PostPullRequestThreadComment(
				ctx, repo, prID, comment.FilePath, comment.Line, comment.Body,
			)
			if err != nil {
				logger.Errorf("failed to post inline comment on %s:%d: %v", comment.FilePath, comment.Line, err)
			}
		} else {
			body := fmt.Sprintf("**[%s]** %s: %s", comment.Severity, comment.FilePath, comment.Body)
			if err := provider.PostPullRequestComment(ctx, repo, prID, body); err != nil {
				logger.Errorf("failed to post comment: %v", err)
			}
		}
	}
}

func allDiffsEmpty(diffs []entities.FileDiff) bool {
	for _, d := range diffs {
		if d.Diff != "" {
			return false
		}
	}
	return true
}
