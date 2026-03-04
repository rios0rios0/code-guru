package support

import "strings"

const (
	diffFilePrefix     = "diff --git "
	diffPathSplitLimit = 2
)

// SplitUnifiedDiff splits a multi-file unified diff into per-file chunks.
// Each returned element is keyed by the new-side file path (b/...) with its diff hunk.
func SplitUnifiedDiff(fullDiff string) map[string]string {
	result := make(map[string]string)
	lines := strings.Split(fullDiff, "\n")

	var currentPath string
	var currentChunk strings.Builder

	for _, line := range lines {
		if strings.HasPrefix(line, diffFilePrefix) {
			// flush previous chunk
			if currentPath != "" {
				result[currentPath] = strings.TrimSpace(currentChunk.String())
			}

			currentPath = extractFilePath(line)
			currentChunk.Reset()
			currentChunk.WriteString(line)
			currentChunk.WriteString("\n")

			continue
		}

		if currentPath != "" {
			currentChunk.WriteString(line)
			currentChunk.WriteString("\n")
		}
	}

	// flush last chunk
	if currentPath != "" {
		result[currentPath] = strings.TrimSpace(currentChunk.String())
	}

	return result
}

// extractFilePath parses the new-side file path from a "diff --git a/... b/..." line.
func extractFilePath(diffLine string) string {
	// format: "diff --git a/path/to/file b/path/to/file"
	parts := strings.SplitN(diffLine, " b/", diffPathSplitLimit)
	if len(parts) == diffPathSplitLimit {
		return parts[1]
	}

	return ""
}
