package controllers

import (
	"context"

	"github.com/rios0rios0/codeguru/internal/domain/entities"
	"github.com/rios0rios0/codeguru/internal/domain/repositories"
	"github.com/rios0rios0/codeguru/internal/support"
)

// TokenRequestForTest names the pull request ResolveTokenForTest resolves a
// token for (see tokenRequest).
type TokenRequestForTest struct {
	ProviderType   string
	Organization   string
	InstallationID string
	ReviewJob      bool
}

// ResolveTokenForTest runs the review command's credential choice, with
// installation tokens minted by `mint` instead of exchanged with GitHub, and
// `fallbackTokens` standing in for the token of the person running the
// review (nil: there is none to fall back to).
func ResolveTokenForTest(
	ctx context.Context,
	settings *entities.Settings,
	request TokenRequestForTest,
	mint func(ctx context.Context, app entities.GitHubAppConfig, installationID int64) (string, error),
	fallbackTokens repositories.FallbackTokenRepository,
) (string, error) {
	controller := &ReviewController{mintInstallationToken: mint, fallbackTokens: fallbackTokens}
	return controller.resolveToken(ctx, settings, tokenRequest{
		providerType:   request.ProviderType,
		organization:   request.Organization,
		installationID: request.InstallationID,
		reviewJob:      request.ReviewJob,
	})
}

// NewTokenRequestForTest exposes newTokenRequest, which turns the reviewed
// pull request and `--job-name` into what resolveToken resolves.
func NewTokenRequestForTest(parsed *support.ParsedPRURL, jobName string) TokenRequestForTest {
	request := newTokenRequest(parsed, jobName)
	return TokenRequestForTest{
		ProviderType:   request.providerType,
		Organization:   request.organization,
		InstallationID: request.installationID,
		ReviewJob:      request.reviewJob,
	}
}
