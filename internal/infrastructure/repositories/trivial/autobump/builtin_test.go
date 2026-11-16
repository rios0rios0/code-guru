package autobump_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/rios0rios0/codeguru/internal/infrastructure/repositories/trivial/autobump"
)

func TestBuiltinVersionFilePatterns(t *testing.T) {
	t.Parallel()

	t.Run("should list the Swagger annotation and generated docs for Go", func(t *testing.T) {
		t.Parallel()

		// given
		language := "go"

		// when
		patterns := autobump.BuiltinVersionFilePatterns(language, "widget-http")

		// then
		assert.Contains(t, patterns, "cmd/main.go")
		assert.Contains(t, patterns, "cmd/*/main.go")
		assert.Contains(t, patterns, "docs/docs.go")
		assert.Contains(t, patterns, "docs/swagger.json")
		assert.Contains(t, patterns, "docs/openapi.yaml")
	})

	t.Run("should replace the project name placeholder for Python", func(t *testing.T) {
		t.Parallel()

		// given
		language := "python"

		// when
		patterns := autobump.BuiltinVersionFilePatterns(language, "widget")

		// then
		assert.Equal(t, []string{"widget/__init__.py"}, patterns)
	})

	t.Run("should return nil for a language with no built-in version file", func(t *testing.T) {
		t.Parallel()

		// given
		language := "terraform"

		// when
		patterns := autobump.BuiltinVersionFilePatterns(language, "widget")

		// then
		assert.Nil(t, patterns)
	})
}

func TestMatchesAny(t *testing.T) {
	t.Parallel()

	patterns := []string{"cmd/main.go", "cmd/*/main.go", "docs/swagger.json"}

	cases := []struct {
		name     string
		filePath string
		want     bool
	}{
		{name: "should match an exact path", filePath: "cmd/main.go", want: true},
		{name: "should match one directory level of a wildcard", filePath: "cmd/server/main.go", want: true},
		{name: "should not match two directory levels of a wildcard", filePath: "cmd/a/b/main.go", want: false},
		{name: "should not match the same name elsewhere", filePath: "internal/main.go", want: false},
		{name: "should not match a nested copy of an exact path", filePath: "api/docs/swagger.json", want: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			// given
			filePath := tc.filePath

			// when
			matched := autobump.MatchesAny(patterns, filePath)

			// then
			assert.Equal(t, tc.want, matched)
		})
	}
}
