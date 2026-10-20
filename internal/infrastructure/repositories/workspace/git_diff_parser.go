package workspace

import (
	"strconv"
	"strings"

	"github.com/rios0rios0/codeguru/internal/domain/entities"
)

// nameStatusEntry is one record of `git diff --name-status -z`.
type nameStatusEntry struct {
	status string
	path   string
}

// lineCounts is one record of `git diff --numstat -z`.
type lineCounts struct {
	additions int
	deletions int
	binary    bool
}

// parseNameStatus parses `git diff --name-status -z -M`. With `-z` every field
// is NUL-terminated and paths are emitted verbatim (no quoting), which is what
// makes a path with spaces, quotes or a newline parse correctly. A rename or
// copy record carries a score after its letter (`R087`) and TWO paths — the
// old one, then the new one; the new one is what the review is about.
func parseNameStatus(output string) []nameStatusEntry {
	fields := strings.Split(output, "\x00")
	var entries []nameStatusEntry
	for i := 0; i < len(fields); {
		status := fields[i]
		if status == "" {
			i++
			continue
		}
		letter := status[:1]
		pathFields := 1
		if letter == "R" || letter == "C" {
			pathFields = 2
		}
		if i+pathFields >= len(fields) {
			break
		}
		entries = append(entries, nameStatusEntry{status: letter, path: fields[i+pathFields]})
		i += 1 + pathFields
	}
	return entries
}

// parseNumStat parses `git diff --numstat -z -M` into line counts keyed by the
// file's new path. A rename record has an EMPTY path field, followed by the old
// and the new path as two more NUL-terminated fields. A binary file reports
// `-` for both counts.
func parseNumStat(output string) map[string]lineCounts {
	const columns = 3
	fields := strings.Split(output, "\x00")
	counts := make(map[string]lineCounts, len(fields))
	for i := 0; i < len(fields); i++ {
		parts := strings.SplitN(fields[i], "\t", columns)
		if len(parts) != columns {
			continue
		}
		path := parts[2]
		if path == "" {
			if i+2 >= len(fields) {
				break
			}
			path = fields[i+2]
			i += 2
		}
		var entry lineCounts
		if parts[0] == "-" && parts[1] == "-" {
			entry.binary = true
		} else {
			entry.additions, _ = strconv.Atoi(parts[0])
			entry.deletions, _ = strconv.Atoi(parts[1])
		}
		counts[path] = entry
	}
	return counts
}

// mergeFileChanges joins the two listings, keeping name-status order. A file
// missing from the numstat listing (which git does not produce in practice)
// keeps zero counts rather than being dropped: the change kind is the part
// the review cannot do without.
func mergeFileChanges(entries []nameStatusEntry, counts map[string]lineCounts) []entities.WorkspaceFileChange {
	changes := make([]entities.WorkspaceFileChange, 0, len(entries))
	for _, entry := range entries {
		count := counts[entry.path]
		changes = append(changes, entities.WorkspaceFileChange{
			Path:      entry.path,
			Status:    entry.status,
			Additions: count.additions,
			Deletions: count.deletions,
			Binary:    count.binary,
		})
	}
	return changes
}
