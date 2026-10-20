package claude_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/rios0rios0/codeguru/internal/infrastructure/repositories/claude"
)

func TestSanitizeFrontmatter(t *testing.T) {
	t.Parallel()

	t.Run("should remove a tool grant written in any spelling Claude Code reads", func(t *testing.T) {
		t.Parallel()

		// given: the bypasses of a byte-exact stripper — CRLF line endings, a
		// fence with trailing whitespace, a quoted key, another spelling, a
		// flow mapping and a byte-order mark.
		cases := map[string]string{
			"CRLF":         "---\r\nname: s\r\nallowed-tools: Bash(make:*)\r\n---\r\nbody\r\n",
			"fence space":  "--- \nname: s\nallowed-tools: Bash(make:*)\n---  \nbody\n",
			"quoted key":   "---\nname: s\n\"allowed-tools\": Bash(make:*)\n---\nbody\n",
			"camel case":   "---\nname: s\nallowedTools: [\"Bash(make:*)\"]\n---\nbody\n",
			"flow mapping": "---\n{name: s, allowed-tools: \"Bash(make:*)\"}\n---\nbody\n",
			"BOM":          "\ufeff---\nname: s\nallowed-tools: Bash(make:*)\n---\nbody\n",
		}

		for name, content := range cases {
			// when
			sanitized, err := claude.SanitizeFrontmatter(content)

			// then
			require.NoError(t, err, name)
			assert.NotContains(t, sanitized, "make", name)
			assert.Contains(t, sanitized, "name: s", name)
			assert.Contains(t, sanitized, "body", name)
		}
	})

	t.Run("should keep everything else in order and the body untouched", func(t *testing.T) {
		t.Parallel()

		// given
		content := "---\nname: go-review\nallowed-tools:\n  - Bash(make *)\n  - Edit\ndescription: Go checklist.\n" +
			"hooks:\n  PreToolUse: []\n---\n\n# Check\n\n---\n\nallowed-tools: in the body is just text\n"

		// when
		sanitized, err := claude.SanitizeFrontmatter(content)

		// then
		require.NoError(t, err)
		assert.Equal(
			t,
			"---\nname: go-review\ndescription: Go checklist.\n---\n\n# Check\n\n---\n\nallowed-tools: in the body is just text\n",
			sanitized,
		)
	})

	t.Run("should install a file without frontmatter as it is", func(t *testing.T) {
		t.Parallel()

		// given
		content := "# A command\n\nallowed-tools: Bash(make:*) is only text here.\n"

		// when
		sanitized, err := claude.SanitizeFrontmatter(content)

		// then
		require.NoError(t, err)
		assert.Equal(t, content, sanitized)
	})

	t.Run("should refuse the shapes Claude Code's YAML parser resolves to a tool grant", func(t *testing.T) {
		t.Parallel()

		// given: each of these reads as a top-level `allowed-tools` grant to
		// Bun's YAML parser, which Claude Code loads skills with.
		cases := map[string]string{
			"sequence key":          "---\n[allowed-tools]: Bash(make:*)\nname: s\n---\nbody\n",
			"complex key":           "---\n? [allowed-tools]\n: Bash(make:*)\nname: s\n---\nbody\n",
			"double-quoted merge":   "---\nname: s\n\"<<\": {allowed-tools: Bash(make:*)}\n---\nbody\n",
			"single-quoted merge":   "---\nname: s\n'<<': {allowed-tools: Bash(make:*)}\n---\nbody\n",
			"no-break space fence":  "---\u00a0\nallowed-tools: Bash(make:*)\n---\nbody\n",
			"byte-order-mark fence": "---\ufeff\nallowed-tools: Bash(make:*)\n---\nbody\n",
			"fence inside a value":  "---\nname: a---b\nallowed-tools: Bash(make:*)\n---\nbody\n",
			"custom tag":            "---\nname: s\ndescription: !grant Bash(make:*)\n---\nbody\n",
		}

		for name, content := range cases {
			// when
			sanitized, err := claude.SanitizeFrontmatter(content)

			// then
			require.Error(t, err, name)
			assert.Empty(t, sanitized, name)
		}
	})

	t.Run("should find a frontmatter behind leading whitespace a trimming reader would skip", func(t *testing.T) {
		t.Parallel()

		// given: a no-break space and a byte-order mark before the fence, which
		// JavaScript's trim removes.
		cases := map[string]string{
			"no-break space":  "\u00a0---\nallowed-tools: Bash(make:*)\nname: s\n---\nbody\n",
			"byte-order mark": "\n \ufeff---\nallowed-tools: Bash(make:*)\nname: s\n---\nbody\n",
		}

		for name, content := range cases {
			// when
			sanitized, err := claude.SanitizeFrontmatter(content)

			// then
			require.NoError(t, err, name)
			assert.Equal(t, "---\nname: s\n---\nbody\n", sanitized, name)
		}
	})

	t.Run("should keep only allowlisted keys with plain values, whatever else the header holds", func(t *testing.T) {
		t.Parallel()

		// given: a forked context run as an agent, nested metadata, a key with
		// an explicit string tag, and a list-valued description.
		content := "---\nname: s\ncontext: fork\nagent: general-purpose\nmetadata: {owner: x}\n" +
			"!!str allowed-tools: Bash(make:*)\nDescription: Review Go code.\nargument_hint: '<pr>'\n" +
			"model: [opus]\n---\nbody\n"

		// when
		sanitized, err := claude.SanitizeFrontmatter(content)

		// then
		require.NoError(t, err)
		assert.Equal(t,
			"---\nname: s\ndescription: Review Go code.\nargument-hint: <pr>\n---\nbody\n",
			sanitized,
			"unknown keys, a non-scalar value and every grant are dropped; known keys keep their canonical spelling")
	})

	t.Run("should refuse a frontmatter it cannot read with certainty", func(t *testing.T) {
		t.Parallel()

		// given
		cases := map[string]string{
			"never closed":   "---\nname: s\nallowed-tools: Bash(make:*)\nbody\n",
			"invalid YAML":   "---\nname: [s\nallowed-tools: Bash(make:*)\n---\nbody\n",
			"not a mapping":  "---\n- allowed-tools: Bash(make:*)\n---\nbody\n",
			"merge key":      "---\nbase: &b {allowed-tools: Bash(make:*)}\nname: s\n<<: *b\n---\nbody\n",
			"grant residue":  "---\nname: s\ndescription: |\n  allowed-tools: Bash(make:*)\n---\nbody\n",
			"duplicate keys": "---\nname: s\nname: t\n---\nbody\n",
		}

		for name, content := range cases {
			// when
			_, err := claude.SanitizeFrontmatter(content)

			// then
			require.Error(t, err, name)
		}
	})
}
