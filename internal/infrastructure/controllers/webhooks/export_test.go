//go:build unit

package webhooks

import (
	"net/http"
	"time"
)

// Re-exports of the unexported parsing/normalisation helpers in the ADO
// handler so the external `webhooks_test` package can pin their contracts
// directly — the integration tests on `HandleAzureDevOps` cover the happy
// paths, but each helper has its own surface area of edge cases (URL
// shapes ADO actually delivers, status normalisation, ref prefixes) that
// deserves dedicated coverage.
//
// The variable indirection keeps the production identifiers unexported in
// non-test builds (this file is gated on the `unit` build tag).
var (
	ExtractADOOrganization       = extractADOOrganization
	IsClosedADOPullRequestStatus = isClosedADOPullRequestStatus
	IsSupportedADOEvent          = isSupportedADOEvent
	RefToBranch                  = refToBranch
	IsSkinnyADOResource          = isSkinnyADOResource
	AppendAPIVersion             = appendAPIVersion
	IsADOAPIHost                 = isADOAPIHost
)

// NewTestHTTPADOHydrator returns a hydrator whose host validator is
// permissive, so tests can drive it against `httptest.NewServer` (which
// serves on `127.0.0.1`, a host the production validator correctly
// refuses as part of the SSRF defence). Production code wires the
// validator via `NewHTTPADOHydrator`, so this escape hatch never reaches
// non-`unit` builds.
func NewTestHTTPADOHydrator(client *http.Client) ADOResourceHydrator {
	if client == nil {
		client = &http.Client{Timeout: adoHydrationTimeout}
	}
	return &httpADOHydrator{
		client:        client,
		hostValidator: func(string) bool { return true },
	}
}

// ADOResource / ADORepository / ADOProject are test-only aliases for the
// unexported wire-shape structs in `azuredevops.go`. External tests construct
// payloads with these aliases to drive `isSkinnyADOResource` and
// `mergeHydratedADOResource` without poking at internal names.
type (
	ADOResource   = adoResource
	ADORepository = adoRepository
	ADOProject    = adoProject
)

// MergeHydratedADOResource exposes the merge helper for tests.
func MergeHydratedADOResource(original, hydrated ADOResource) ADOResource {
	return mergeHydratedADOResource(original, hydrated)
}

// WebhookDedupCache is the test-only alias for the unexported
// `webhookDedupCache` so external tests can drive the cache with a
// frozen clock instead of `time.Now()` (deterministic, no flakes).
type WebhookDedupCache = webhookDedupCache

// NewWebhookDedupCache constructs a cache with the supplied TTL. The
// production path uses the package-level `webhookDedupTTL` constant;
// tests use shorter TTLs so a single test run can exercise the
// expiry branch without a real-time wait.
func NewWebhookDedupCache(ttl time.Duration) *WebhookDedupCache {
	return newWebhookDedupCache(ttl)
}

// SeenRecently exposes the unexported `seenRecently` method on the
// cache so external tests can drive both the "first call records"
// and "subsequent within TTL returns true" branches.
func (c *WebhookDedupCache) SeenRecently(key string, now time.Time) bool {
	return c.seenRecently(key, now)
}
