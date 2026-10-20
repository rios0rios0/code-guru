package claude

import (
	"errors"
	"regexp"
	"slices"
	"strings"
	"unicode"

	"gopkg.in/yaml.v3"
)

// frontmatterFence opens and closes a Markdown file's YAML frontmatter.
const frontmatterFence = "---"

// frontmatterFenceSpace is the trailing whitespace a fence line may carry and
// still count as a clean fence here. Claude Code accepts more — its pattern is
// `^---\s*\n`, and JavaScript's `\s` includes non-breaking and other Unicode
// spaces — so any other character after `---` fails closed instead of being
// read as "no frontmatter".
const frontmatterFenceSpace = " \t\r"

// yamlStringTag is the resolved tag of a plain or quoted YAML string.
const yamlStringTag = "!!str"

// byteOrderMark is dropped from the start of a file before its frontmatter is
// looked for.
const byteOrderMark = "\ufeff"

// installableFrontmatterKeys are the only frontmatter keys an installed skill
// or command keeps, by normalised spelling, mapped to the spelling written
// back. It is an allowlist on purpose: every key that can widen what the agent
// may do while a skill is active — `allowed-tools`, `hooks`, a permission
// mode, MCP servers, a forked `context` run as an `agent` — is absent, so a key
// Claude Code learns tomorrow is dropped by default rather than passed through.
//
//nolint:gochecknoglobals // read-only lookup table used as a constant
var installableFrontmatterKeys = map[string]string{
	"name":                   "name",
	"description":            "description",
	"argumenthint":           "argument-hint",
	"model":                  "model",
	"version":                "version",
	"license":                "license",
	"disablemodelinvocation": "disable-model-invocation",
	"userinvocable":          "user-invocable",
}

// grantLinePattern is a last check on the rebuilt header: no line of it may
// read as a tool grant, even inside a block scalar, in case a reader ever
// takes a header line by line rather than as YAML.
var grantLinePattern = regexp.MustCompile(`(?im)^\s*["']?allowed[-_ ]?tools["']?\s*:`)

// plainScalarTags are the only value types an installed key keeps.
//
//nolint:gochecknoglobals // read-only lookup table used as a constant
var plainScalarTags = map[string]bool{yamlStringTag: true, "!!bool": true, "!!int": true, "!!float": true}

var (
	errUncleanFence = errors.New(
		"its first line opens like a frontmatter fence but carries more than whitespace",
	)
	errUnclosedFrontmatter   = errors.New("its frontmatter is never closed by a clean fence")
	errUnparsableFrontmatter = errors.New("its frontmatter is not valid YAML")
	errFrontmatterNotMapping = errors.New("its frontmatter is not a mapping of keys")
	errFrontmatterKey        = errors.New("its frontmatter has a key that is not a plain string")
	errFrontmatterIndirect   = errors.New("its frontmatter uses YAML anchors, aliases, merge keys or custom tags")
	errFrontmatterDuplicate  = errors.New("its frontmatter repeats a key")
	errFrontmatterResidue    = errors.New("its frontmatter still reads as a tool grant")
)

// sanitizeFrontmatter returns a skill or command file with its frontmatter
// rebuilt from installableFrontmatterKeys alone, or an error saying why the
// file must not be installed at all.
//
// It finds the frontmatter the way Claude Code does — the header ends at the
// first `---` after the opening line, wherever it is — and refuses every file
// where the two readings could differ: a fence carrying anything but ASCII
// whitespace, a header closed by `---` in the middle of a line, a frontmatter
// that never closes. It then parses the header as YAML and fails closed on
// whatever another parser could resolve differently: a key that is not a plain
// string (Bun turns `[allowed-tools]` into the string `allowed-tools`), a merge
// key in any quoting (Bun merges a quoted `"<<"` too), anchors and aliases,
// custom tags and repeated keys. What survives is re-emitted from scratch —
// only allowlisted keys, only plain scalar values — so nothing in the original
// spelling can reach the agent. A file without frontmatter grants nothing and
// is installed as it is.
func sanitizeFrontmatter(content string) (string, error) {
	content = strings.ReplaceAll(strings.TrimPrefix(content, byteOrderMark), "\r\n", "\n")
	header, body, found, err := splitFrontmatter(content)
	if err != nil {
		return "", err
	}
	if !found {
		return content, nil
	}

	var document yaml.Node
	if err = yaml.Unmarshal([]byte(header), &document); err != nil {
		return "", errUnparsableFrontmatter
	}
	kept := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	if len(document.Content) > 0 {
		if kept.Content, err = installableEntries(document.Content[0]); err != nil {
			return "", err
		}
	}

	rebuilt := ""
	if len(kept.Content) > 0 {
		encoded, marshalErr := yaml.Marshal(kept)
		if marshalErr != nil {
			return "", errUnparsableFrontmatter
		}
		rebuilt = string(encoded)
	}
	if strings.Contains(rebuilt, frontmatterFence) {
		// A value spelling `---` (through an escape, say) would close the
		// header early in Claude Code's reading.
		return "", errUnclosedFrontmatter
	}
	if grantLinePattern.MatchString(rebuilt) {
		return "", errFrontmatterResidue
	}
	return frontmatterFence + "\n" + rebuilt + frontmatterFence + "\n" + body, nil
}

// installableEntries returns the key/value pairs of a frontmatter mapping that
// survive the allowlist, re-created as fresh nodes, or the reason the whole
// file is refused.
func installableEntries(mapping *yaml.Node) ([]*yaml.Node, error) {
	if mapping.Kind != yaml.MappingNode {
		return nil, errFrontmatterNotMapping
	}
	if usesIndirection(mapping) {
		return nil, errFrontmatterIndirect
	}
	var kept []*yaml.Node
	seen := map[string]bool{}
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		key, value := mapping.Content[i], mapping.Content[i+1]
		if key.Kind != yaml.ScalarNode || key.ShortTag() != yamlStringTag {
			return nil, errFrontmatterKey
		}
		if key.Value == "<<" {
			return nil, errFrontmatterIndirect
		}
		if seen[key.Value] {
			// YAML readers disagree on a repeated key — the first wins, the
			// last wins, or the document is rejected.
			return nil, errFrontmatterDuplicate
		}
		seen[key.Value] = true
		canonical, installable := installableFrontmatterKeys[normaliseFrontmatterKey(key.Value)]
		if !installable || value.Kind != yaml.ScalarNode || !plainScalarTags[value.ShortTag()] {
			continue
		}
		kept = append(kept,
			&yaml.Node{Kind: yaml.ScalarNode, Tag: yamlStringTag, Value: canonical},
			&yaml.Node{Kind: yaml.ScalarNode, Tag: value.ShortTag(), Value: value.Value},
		)
	}
	return kept, nil
}

// splitFrontmatter separates the YAML header from the body, reading it the way
// Claude Code does. `found` is false when the file has no frontmatter. Leading
// whitespace of any kind before the opening fence is skipped — JavaScript's
// trim also removes no-break spaces and byte-order marks — so a reader that
// trims the file first cannot find a header this one missed, and a file
// written back unchanged really has none.
func splitFrontmatter(content string) (string, string, bool, error) {
	rest := strings.TrimLeftFunc(content, isLeadingSpace)
	opening, rest, _ := strings.Cut(rest, "\n")
	if !strings.HasPrefix(opening, frontmatterFence) {
		return "", "", false, nil
	}
	if strings.TrimRight(opening, frontmatterFenceSpace) != frontmatterFence {
		return "", "", false, errUncleanFence
	}
	end := strings.Index(rest, frontmatterFence)
	if end < 0 || (end > 0 && rest[end-1] != '\n') {
		return "", "", false, errUnclosedFrontmatter
	}
	closing, body, _ := strings.Cut(rest[end:], "\n")
	if strings.TrimRight(closing, frontmatterFenceSpace) != frontmatterFence {
		return "", "", false, errUnclosedFrontmatter
	}
	return rest[:end], body, true, nil
}

// isLeadingSpace is JavaScript's notion of whitespace: Unicode's plus the
// byte-order mark.
func isLeadingSpace(r rune) bool {
	return unicode.IsSpace(r) || string(r) == byteOrderMark
}

// usesIndirection reports whether a node, or anything under it, is an alias,
// carries an anchor, a merge key or a custom tag.
func usesIndirection(node *yaml.Node) bool {
	if node.Kind == yaml.AliasNode || node.Anchor != "" || node.ShortTag() == "!!merge" ||
		(node.Tag != "" && !strings.HasPrefix(node.ShortTag(), "!!")) {
		return true
	}
	return slices.ContainsFunc(node.Content, usesIndirection)
}

// normaliseFrontmatterKey folds the spellings of one key together: case,
// hyphens, underscores and spaces.
func normaliseFrontmatterKey(key string) string {
	return strings.NewReplacer("-", "", "_", "", " ", "").Replace(strings.ToLower(strings.TrimSpace(key)))
}
