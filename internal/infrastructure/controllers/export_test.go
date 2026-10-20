package controllers

import (
	"context"

	"github.com/rios0rios0/codeguru/internal/domain/entities"
)

// ResolveTokenForTest runs the review command's credential choice, with
// installation tokens minted by `mint` instead of exchanged with GitHub.
func ResolveTokenForTest(
	ctx context.Context,
	settings *entities.Settings,
	providerType, installationID string,
	mint func(ctx context.Context, app entities.GitHubAppConfig, installationID int64) (string, error),
) (string, error) {
	controller := &ReviewController{mintInstallationToken: mint}
	return controller.resolveToken(ctx, settings, providerType, installationID)
}
