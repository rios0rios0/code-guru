package repositories

import "context"

// FallbackTokenRepository supplies the forge token of the person running a CLI
// command — one their environment exports, or their gh or az CLI session —
// for a provider nothing configured a token for. It is the last source of a
// token, after every configured one.
//
// Such a token can be short-lived (an Azure CLI token lasts about an hour, and
// az may hand over a cached one with minutes left), so a caller asks again for
// each run and each organization instead of keeping one for a whole batch.
//
// Only the commands a person runs take it. The webhook server and the review
// Jobs it starts never do: they act only as the identity their operator
// configured.
type FallbackTokenRepository interface {
	// GetToken returns a token for the provider ("github", "azuredevops"); on
	// Azure DevOps it is issued in the Microsoft Entra tenant of the
	// organization. An error names every source that was tried.
	GetToken(ctx context.Context, providerType, organization string) (string, error)
}
