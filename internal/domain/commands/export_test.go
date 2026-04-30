//go:build unit

package commands

// ShouldPostSummary exposes the unexported `shouldPostSummary` predicate to
// the external `commands_test` package so the gating decision can be unit
// tested without standing up the full `Execute` flow with stubs for every
// repository/provider/registry. The variable indirection keeps the helper
// itself unexported in production builds (the file is gated on the `unit`
// build tag).
var ShouldPostSummary = shouldPostSummary
