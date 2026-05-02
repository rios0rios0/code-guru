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

// Forget exposes the unexported `forget` method on the cache so
// external tests can verify the rollback contract — when a caller
// records a key but the work it intended to gate (typically
// `submitter.Submit`) fails, calling Forget puts the cache back to
// the state that lets a retry through.
func (c *WebhookDedupCache) Forget(key string) { c.forget(key) }

// Renewal-loop invariant constants re-exported so the
// `TestLeaseDurationAndRenewIntervalInvariant` row in
// `dedup_lease_test.go` can pin the relationship without forcing the
// production constants to be exported. A future refactor that drops
// the freshness window below `renew interval + API timeout` would
// silently regress the dedup correctness — the invariant test fails
// if that ever happens.
var (
	LeaseDurationSecondsForTest = leaseDurationSeconds
	LeaseRenewIntervalForTest   = leaseRenewInterval
	LeaseAPITimeoutForTest      = leaseAPITimeout
)

// DedupRenewIntervalForTest re-exports the dispatcher-level renewal
// cadence so tests can assert the loop's tick frequency without
// timing-sensitive sleeps.
var DedupRenewIntervalForTest = dedupRenewInterval

// MarkInFlightForTest exposes the unexported `trackInFlight` helper so
// external dispatcher tests can populate the in-flight set without
// driving the full webhook handler stack. The production code only
// calls `trackInFlight` from `dedupSeen` (after `SeenRecently` returns
// false); the test analogue avoids the SeenRecently call so the test
// can assert the populated `ReleaseAllInFlight` path in isolation.
func (d *Dispatcher) MarkInFlightForTest(key string) { d.trackInFlight(key) }
