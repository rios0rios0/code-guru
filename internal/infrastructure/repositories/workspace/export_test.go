package workspace

import (
	"context"

	forgeEntities "github.com/rios0rios0/gitforge/v4/pkg/global/domain/entities"

	"github.com/rios0rios0/codeguru/internal/domain/entities"
)

// PrepareFromURL runs the clone sequence against an arbitrary URL with no
// credentials, so tests can point it at a local `file://` repository instead
// of a hosting provider.
func (r *GitWorkspaceRepository) PrepareFromURL(
	ctx context.Context, url string, pr forgeEntities.PullRequestDetail,
) (*entities.ReviewWorkspace, error) {
	return r.prepareFrom(ctx, cloneTarget{url: url}, "", pr)
}

// PrepareFromURLWithPullHeadRef is PrepareFromURL for a provider that
// publishes each pull request's head under a ref of its own on the target
// repository (`%d` = the pull request id), as GitHub does.
func (r *GitWorkspaceRepository) PrepareFromURLWithPullHeadRef(
	ctx context.Context, url, pullHeadRefFormat string, pr forgeEntities.PullRequestDetail,
) (*entities.ReviewWorkspace, error) {
	return r.prepareFrom(ctx, cloneTarget{url: url, pullHeadRefFormat: pullHeadRefFormat}, "", pr)
}

// AuthEnvFor exposes the auth environment for one provider's clone target.
func AuthEnvFor(providerName string, repo forgeEntities.Repository, token string) ([]string, error) {
	target, err := cloneTargetBuilders[providerName](repo)
	if err != nil {
		return nil, err
	}
	return authEnv(target, token), nil
}

// CloneURLFor exposes the clone URL built for one provider's repository.
func CloneURLFor(providerName string, repo forgeEntities.Repository) (string, error) {
	target, err := cloneTargetBuilders[providerName](repo)
	return target.url, err
}

// ParseChanges runs both diff parsers and the join over raw git output.
func ParseChanges(nameStatus, numStat string) []entities.WorkspaceFileChange {
	return mergeFileChanges(parseNameStatus(nameStatus), parseNumStat(numStat))
}

// RefuseUnreachableFork runs the fork check Prepare runs before cloning, for
// the provider's own clone target.
func (r *GitWorkspaceRepository) RefuseUnreachableFork(
	ctx context.Context, provider forgeEntities.ReviewProvider, repo forgeEntities.Repository, prID int,
) error {
	target, err := cloneTargetBuilders[provider.Name()](repo)
	if err != nil {
		return err
	}
	return r.refuseUnreachableFork(ctx, provider, target, repo, prID)
}
