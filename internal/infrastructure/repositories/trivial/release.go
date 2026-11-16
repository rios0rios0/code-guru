package trivial

import (
	"context"
	"path"
	"regexp"
	"strings"

	logger "github.com/sirupsen/logrus"

	"github.com/rios0rios0/codeguru/internal/domain/repositories"
)

const (
	// chlogUnreleasedDir is where chlog keeps the changelog fragments a
	// release has not compiled yet; a release deletes the ones it compiles.
	chlogUnreleasedDir = ".changes/unreleased/"
	// versionPlaceholder stands in for every version string when two lines
	// are compared, so lines that differ only in versions compare equal.
	versionPlaceholder = "\x00"
)

// versionPattern matches a version string: three or more dot-separated
// numbers, with an optional `v` prefix and pre-release and build suffixes
// (`1.4.0`, `v1.4.0`, `1.4.0-rc.1`, `1.0.0.1`).
//

var versionPattern = regexp.MustCompile(
	`\bv?\d+(?:\.\d+){2,}(?:-[0-9A-Za-z][0-9A-Za-z.-]*)?(?:\+[0-9A-Za-z][0-9A-Za-z.-]*)?`,
)

// releaseHeadingPattern matches a Keep a Changelog version heading such as
// `## [1.4.0] - 2026-01-31`, capturing what sits between the brackets.
//

var releaseHeadingPattern = regexp.MustCompile(`^##\s+\[([^\]]+)\]`)

// isChangelog reports whether filePath is a changelog file.
func isChangelog(filePath string) bool {
	return strings.EqualFold(path.Base(filePath), changelogFile)
}

// isChlogFragment reports whether filePath is a pending chlog changelog
// fragment: a YAML file directly under `.changes/unreleased/`.
func isChlogFragment(filePath string) bool {
	name, found := strings.CutPrefix(filePath, chlogUnreleasedDir)
	if !found || name == "" || strings.Contains(name, "/") {
		return false
	}
	ext := strings.ToLower(path.Ext(name))
	return ext == ".yaml" || ext == ".yml"
}

// isPendingChlogFragment reports whether the pull request adds or edits
// filePath as a pending chlog fragment: the record a chlog repository keeps
// of a change until a release compiles it. A deleted fragment is one a
// release compiled.
func isPendingChlogFragment(dctx repositories.DetectionContext, filePath string) bool {
	return isChlogFragment(filePath) && !dctx.DeletedFiles[filePath]
}

// changedLines returns the lines a unified diff removes and the lines it
// adds, without their `-` / `+` markers. Only hunk bodies are read, so the
// file header lines (`--- a/x`, `+++ b/x`) are never taken for changes.
func changedLines(diff string) ([]string, []string) {
	var removed, added []string
	inHunk := false
	for line := range strings.SplitSeq(diff, "\n") {
		line = strings.TrimSuffix(line, "\r")
		switch {
		case strings.HasPrefix(line, "@@"):
			inHunk = true
		case strings.HasPrefix(line, "diff --git "):
			inHunk = false
		case !inHunk:
			continue
		case strings.HasPrefix(line, "-"):
			removed = append(removed, line[1:])
		case strings.HasPrefix(line, "+"):
			added = append(added, line[1:])
		}
	}
	return removed, added
}

// normalizeVersion drops the optional `v` prefix, so `v1.4.0` and `1.4.0`
// name the same release.
func normalizeVersion(version string) string {
	return strings.TrimPrefix(version, "v")
}

// isVersion reports whether s is a version string and nothing else.
func isVersion(s string) bool {
	loc := versionPattern.FindStringIndex(s)
	return loc != nil && loc[0] == 0 && loc[1] == len(s)
}

// addedReleaseVersions returns the versions of the release headings a
// changelog diff adds, normalised and each once. `[Unreleased]` is not a
// release.
func addedReleaseVersions(diff string) []string {
	_, added := changedLines(diff)
	var versions []string
	seen := make(map[string]bool)
	for _, line := range added {
		match := releaseHeadingPattern.FindStringSubmatch(line)
		if match == nil {
			continue
		}
		version := normalizeVersion(strings.TrimSpace(match[1]))
		if !isVersion(version) || seen[version] {
			continue
		}
		seen[version] = true
		versions = append(versions, version)
	}
	return versions
}

// changedChangelogs returns the changelog files the pull request edits.
func changedChangelogs(dctx repositories.DetectionContext) []string {
	var changelogs []string
	for _, f := range dctx.Files {
		if isChangelog(f) && !dctx.DeletedFiles[f] {
			changelogs = append(changelogs, f)
		}
	}
	return changelogs
}

// releaseVersion returns the one version the pull request's changelog gains
// a heading for. It reports false when a changelog diff cannot be read, when
// no heading is added, or when headings for more than one version are.
// The caller guarantees dctx.FileDiffFetcher is set.
func releaseVersion(ctx context.Context, dctx repositories.DetectionContext) (string, bool) {
	var release string
	for _, f := range changedChangelogs(dctx) {
		diff, err := dctx.FileDiffFetcher.GetFileDiff(ctx, f)
		if err != nil {
			logger.Debugf("trivial: cannot read the diff of %q: %v", f, err)
			return "", false
		}
		for _, version := range addedReleaseVersions(diff) {
			if release != "" && release != version {
				return "", false
			}
			release = version
		}
	}
	return release, release != ""
}

// replacesOnlyVersion reports whether a diff does nothing but replace
// version strings with release: every removed line pairs with an added line
// that is identical once each version string is masked, at least one of
// each pair's version strings changes, and every version string that
// changes becomes release. A diff that adds, removes or edits anything else
// fails, so code can never pass for a version bump.
func replacesOnlyVersion(diff, release string) bool {
	removed, added := changedLines(diff)
	if len(removed) == 0 || len(removed) != len(added) {
		return false
	}
	pending := make(map[string][]string, len(removed))
	for _, line := range removed {
		key := versionPattern.ReplaceAllString(line, versionPlaceholder)
		pending[key] = append(pending[key], line)
	}
	for _, line := range added {
		key := versionPattern.ReplaceAllString(line, versionPlaceholder)
		candidates := pending[key]
		if len(candidates) == 0 {
			return false
		}
		pending[key] = candidates[1:]
		if !changesOnlyToRelease(candidates[0], line, release) {
			return false
		}
	}
	return true
}

// changesOnlyToRelease compares a removed line with the added line that
// replaces it, both known to match outside their version strings. It reports
// whether at least one version string changed and every changed one became
// release.
func changesOnlyToRelease(before, after, release string) bool {
	oldVersions := versionPattern.FindAllString(before, -1)
	newVersions := versionPattern.FindAllString(after, -1)
	if len(oldVersions) != len(newVersions) {
		return false
	}
	changed := false
	for i, version := range newVersions {
		if version == oldVersions[i] {
			continue
		}
		if normalizeVersion(version) != release {
			return false
		}
		changed = true
	}
	return changed
}

// declinesAsRelease reports whether an adapter that does not detect releases
// (docs-only, update-*) must leave the pull request alone because it is, or
// may be, a release: it changes nothing but the changelog, or its changelog
// gains a version heading. A release must never be settled — and so perhaps
// auto-merged — as a documentation or dependency update.
//
// A changelog diff that cannot be read counts as a release, for the same
// reason. Without a diff fetcher at all the heading cannot be looked for, and
// only the changelog-only rule applies.
func declinesAsRelease(ctx context.Context, dctx repositories.DetectionContext) bool {
	if isChangelogOnly(dctx.Files) {
		return true
	}
	if dctx.FileDiffFetcher == nil {
		return false
	}
	for _, f := range changedChangelogs(dctx) {
		diff, err := dctx.FileDiffFetcher.GetFileDiff(ctx, f)
		if err != nil {
			logger.Debugf("trivial: cannot read the diff of %q, treating the change as a release: %v", f, err)
			return true
		}
		if len(addedReleaseVersions(diff)) > 0 {
			return true
		}
	}
	return false
}
