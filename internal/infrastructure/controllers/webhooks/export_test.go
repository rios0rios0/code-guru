//go:build unit

package webhooks

// Re-exports of the unexported parsing/normalisation helpers in the ADO
// handler so the external `webhooks_test` package can pin their contracts
// directly — the integration tests on `HandleAzureDevOps` cover the happy
// paths, but each helper has its own surface area of edge cases (URL
// shapes ADO actually delivers, status normalisation, ref prefixes, log
// truncation) that deserves dedicated coverage.
//
// The variable indirection keeps the production identifiers unexported in
// non-test builds (this file is gated on the `unit` build tag).
var (
	ExtractADOOrganization       = extractADOOrganization
	IsClosedADOPullRequestStatus = isClosedADOPullRequestStatus
	IsSupportedADOEvent          = isSupportedADOEvent
	RefToBranch                  = refToBranch
	TruncateForLog               = truncateForLog
)

// ADORawBodyLogLimit re-exports the body-truncation constant used by the
// allowlist-rejection diagnostic so test code can express "is the
// truncation budget honoured" without inlining the magic number.
const ADORawBodyLogLimit = adoRawBodyLogLimit
