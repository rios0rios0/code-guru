package entitybuilders

import (
	testkit "github.com/rios0rios0/testkit/pkg/test"
)

// defaultFilePath is the file path every builder starts from. Shared so
// the default and the value `Reset` restores can never drift apart.
const defaultFilePath = "test.go"

// cloneBase narrows testkit's `Builder`-typed `Clone` back to the
// concrete `*testkit.BaseBuilder` that every builder here embeds.
//
// The assertion cannot fail in practice — `BaseBuilder.Clone` builds
// and returns a `*BaseBuilder` — but writing it unchecked hides that
// guarantee from the reader and leaves a silent `nil` embed if testkit
// ever changes the return type. Failing loudly in test-support code is
// the safe direction: a panic here is a broken fixture, not a broken
// production path.
func cloneBase(base *testkit.BaseBuilder) *testkit.BaseBuilder {
	clone, ok := base.Clone().(*testkit.BaseBuilder)
	if !ok {
		panic("entitybuilders: testkit.BaseBuilder.Clone did not return a *testkit.BaseBuilder")
	}
	return clone
}
