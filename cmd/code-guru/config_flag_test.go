package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// configFlagDescription returns the description the flag table of the Markdown
// document at path gives `-c, --config`, written as the help shows it: without
// the Markdown backticks, and starting in lowercase like every flag's.
func configFlagDescription(t *testing.T, path string) string {
	t.Helper()
	content, err := os.ReadFile(path)
	require.NoError(t, err)
	for line := range strings.SplitSeq(string(content), "\n") {
		cells := strings.Split(line, "|")
		if len(cells) < 3 || strings.TrimSpace(cells[1]) != "`-c, --config`" {
			continue
		}
		description := strings.ReplaceAll(strings.TrimSpace(cells[2]), "`", "")
		first, size := utf8.DecodeRuneInString(description)
		return string(unicode.ToLower(first)) + description[size:]
	}
	t.Fatalf("%s has no `-c, --config` row", path)
	return ""
}

func TestConfigFlagHelp(t *testing.T) {
	t.Parallel()

	for _, document := range []struct {
		name string
		path string
	}{
		{name: "the README", path: "README.md"},
		{name: "the Copilot instructions", path: filepath.Join(".github", "copilot-instructions.md")},
	} {
		t.Run("should describe --config in the help as the flag table of "+document.name+" does", func(t *testing.T) {
			t.Parallel()

			// given
			want := configFlagDescription(t, filepath.Join("..", "..", document.path))
			root := buildRootCommand(nil, nil)

			// when
			help := root.UsageString()

			// then
			assert.Contains(t, help, "--config string")
			assert.Contains(t, help, want)
		})
	}
}
