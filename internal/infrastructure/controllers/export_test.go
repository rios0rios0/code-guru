package controllers

import (
	"context"

	registry "github.com/rios0rios0/gitforge/v4/pkg/registry/infrastructure"

	"github.com/rios0rios0/codeguru/internal/domain/commands"
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
	token, _, err := ResolveTokenWithProvenanceForTest(ctx, settings, request, mint, fallbackTokens)
	return token, err
}

// ResolveTokenWithProvenanceForTest is ResolveTokenForTest that also reports
// whether the token is the runner's own, the fallback's.
func ResolveTokenWithProvenanceForTest(
	ctx context.Context,
	settings *entities.Settings,
	request TokenRequestForTest,
	mint func(ctx context.Context, app entities.GitHubAppConfig, installationID int64) (string, error),
	fallbackTokens repositories.FallbackTokenRepository,
) (string, bool, error) {
	controller := &ReviewController{mintInstallationToken: mint, fallbackTokens: fallbackTokens}
	token, err := controller.resolveToken(ctx, settings, request.toTokenRequest())
	return token.value, token.runner, err
}

// RunnerReviewOptionsForTest returns the options withRunnerCredential gives
// the review of the pull request `request` names, with providers built by
// `providerRegistry` and fresh tokens from `fallbackTokens`.
func RunnerReviewOptionsForTest(
	providerRegistry *registry.ProviderRegistry,
	fallbackTokens repositories.FallbackTokenRepository,
	request TokenRequestForTest,
) commands.ReviewOptions {
	controller := &ReviewController{providerRegistry: providerRegistry, fallbackTokens: fallbackTokens}
	return controller.withRunnerCredential(commands.ReviewOptions{}, request.toTokenRequest())
}

// ReviewOptionsFromForTest exposes reviewOptionsFrom, the options of a
// `review` run.
//
//nolint:gochecknoglobals // test-only re-export of an unexported helper
var ReviewOptionsFromForTest = reviewOptionsFrom

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

func (r TokenRequestForTest) toTokenRequest() tokenRequest {
	return tokenRequest{
		providerType:   r.ProviderType,
		organization:   r.Organization,
		installationID: r.InstallationID,
		reviewJob:      r.ReviewJob,
	}
}
