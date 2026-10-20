package repositories_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/rios0rios0/codeguru/internal/domain/entities"
	"github.com/rios0rios0/codeguru/internal/domain/repositories"
	infraRepos "github.com/rios0rios0/codeguru/internal/infrastructure/repositories"
	"github.com/rios0rios0/codeguru/internal/support"
)

// workspaceFakeReviewer is a fakeAIReviewer that declares itself a workspace
// reviewer.
type workspaceFakeReviewer struct {
	fakeAIReviewer
}

func (w *workspaceFakeReviewer) RequiresWorkspace() bool { return true }

func TestRetryingAIReviewerAgenticBehaviour(t *testing.T) {
	t.Parallel()

	t.Run("should forward RequiresWorkspace from the wrapped reviewer", func(t *testing.T) {
		t.Parallel()

		// given
		wrappedWorkspace := infraRepos.WithRetry(&workspaceFakeReviewer{}, 3)
		wrappedDiff := infraRepos.WithRetry(&fakeAIReviewer{}, 3)

		// when
		workspace, diff := wrappedWorkspace.RequiresWorkspace(), wrappedDiff.RequiresWorkspace()

		// then
		assert.True(t, workspace, "a wrapped agentic backend must still be given its checkout")
		assert.False(t, diff)
	})

	for _, sentinel := range []error{support.ErrAgenticReviewIncomplete, support.ErrWorkspaceUnavailable} {
		t.Run(fmt.Sprintf("should not retry %v", sentinel), func(t *testing.T) {
			t.Parallel()

			// given
			fake := &fakeAIReviewer{errs: []error{fmt.Errorf("%w: detail", sentinel)}}
			reviewer := infraRepos.WithRetry(fake, 3)

			// when
			_, err := reviewer.ReviewDiff(context.Background(), entities.ReviewRequest{})

			// then
			require.ErrorIs(t, err, sentinel)
			assert.Equal(t, 1, fake.calls, "a re-run would stop at the same point, at the same cost")
		})
	}
}

func TestAIReviewerFactoryAgenticMode(t *testing.T) {
	t.Parallel()

	t.Run("should build a workspace reviewer when the claude backend runs agentic", func(t *testing.T) {
		t.Parallel()

		// given
		settings := &entities.Settings{AI: entities.AIConfig{
			Backend: "claude", Claude: entities.ClaudeConfig{Mode: "agentic"},
		}}

		// when
		reviewer := infraRepos.NewAIReviewerFactory().Create(settings)

		// then
		workspaceReviewer, ok := reviewer.(repositories.WorkspaceReviewer)
		require.True(t, ok)
		assert.True(t, workspaceReviewer.RequiresWorkspace())
	})

	t.Run("should keep the diff reviewer by default", func(t *testing.T) {
		t.Parallel()

		// given
		settings := &entities.Settings{AI: entities.AIConfig{Backend: "claude"}}

		// when
		reviewer := infraRepos.NewAIReviewerFactory().Create(settings)

		// then
		workspaceReviewer, ok := reviewer.(repositories.WorkspaceReviewer)
		assert.False(t, ok && workspaceReviewer.RequiresWorkspace())
	})
}

func TestRulesRepositoryFactoryAgenticFallback(t *testing.T) {
	t.Parallel()

	t.Run("should read the rules from the installed Claude configuration when rules.path is empty", func(t *testing.T) {
		t.Parallel()

		// given
		tree := t.TempDir()
		// nosemgrep: go.lang.correctness.permissions.file_permission.incorrect-default-permission
		require.NoError(t, os.MkdirAll(filepath.Join(tree, "rules"), 0o700))
		require.NoError(t, os.WriteFile(filepath.Join(tree, "rules", "security.md"), []byte("# Security\n"), 0o600))
		settings := &entities.Settings{AI: entities.AIConfig{
			Backend: "claude",
			Claude:  entities.ClaudeConfig{Mode: "agentic", Agentic: entities.ClaudeAgenticConfig{ConfigPath: tree}},
		}}

		// when
		rules, err := infraRepos.NewRulesRepositoryFactory().Create(settings).LoadAll()

		// then
		require.NoError(t, err)
		require.Len(t, rules, 1)
		assert.Equal(t, "security", rules[0].Name)
	})

	t.Run("should prefer an explicit rules.path", func(t *testing.T) {
		t.Parallel()

		// given
		explicit := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(explicit, "testing.md"), []byte("# Testing\n"), 0o600))
		settings := &entities.Settings{
			Rules: entities.RulesConfig{Path: explicit},
			AI: entities.AIConfig{Backend: "claude", Claude: entities.ClaudeConfig{
				Mode: "agentic", Agentic: entities.ClaudeAgenticConfig{ConfigPath: t.TempDir()},
			}},
		}

		// when
		rules, err := infraRepos.NewRulesRepositoryFactory().Create(settings).LoadAll()

		// then
		require.NoError(t, err)
		require.Len(t, rules, 1)
		assert.Equal(t, "testing", rules[0].Name)
	})
}
