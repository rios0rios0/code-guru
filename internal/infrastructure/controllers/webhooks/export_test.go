//go:build unit

package webhooks

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
)

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
