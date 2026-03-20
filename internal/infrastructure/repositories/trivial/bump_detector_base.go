package trivial

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	logger "github.com/sirupsen/logrus"

	"github.com/rios0rios0/codeguru/internal/domain/repositories"
	"github.com/rios0rios0/codeguru/internal/infrastructure/repositories/trivial/autobump"
)

const autobumpConfigPath = ".autobump.yaml"

// detectBump implements the shared bump detection logic used by all bump-* detectors.
//
// When .autobump.yaml is available, it is loaded first to expand the allowed file set
// with version_files. The PR files are then validated against the union of defaults
// and autobump entries. If autobump is active, all version_files must be present
// or the PR is rejected.
func detectBump(
	ctx context.Context,
	dctx repositories.DetectionContext,
	detectorName string,
	autobumpLangKey string,
	defaultAllowed map[string]bool,
	defaultAllowedFn func(string) bool,
) repositories.DetectionResult {
	if len(dctx.Files) == 0 {
		return repositories.DetectionResult{}
	}

	// try to load autobump config first to expand the allowed set
	autobumpRequired, autobumpActive := loadAutobumpRequired(ctx, dctx, autobumpLangKey)

	// check all PR files fall within the combined allowed set (defaults + autobump)
	for _, f := range dctx.Files {
		base := filepath.Base(f)
		inDefault := defaultAllowed[base] || (defaultAllowedFn != nil && defaultAllowedFn(f))
		inAutobump := autobumpRequired[f]
		if !inDefault && !inAutobump {
			return repositories.DetectionResult{}
		}
	}

	// if autobump is active, check all required files are present
	if autobumpActive {
		return validateAutobumpRequired(dctx, detectorName, autobumpRequired)
	}

	// fallback: all files matched the default set
	return repositories.DetectionResult{
		Detected: true,
		Verdict:  "approve",
		Summary: fmt.Sprintf(
			"%s version bump detected (%d files). Auto-approved by trivial PR policy.",
			detectorName, len(dctx.Files),
		),
	}
}

// loadAutobumpRequired attempts to load .autobump.yaml and returns the set of
// required files (CHANGELOG.md + version_files) and whether autobump is active.
func loadAutobumpRequired(
	ctx context.Context,
	dctx repositories.DetectionContext,
	autobumpLangKey string,
) (map[string]bool, bool) {
	if dctx.FileContentFetcher == nil || !dctx.FileContentFetcher.HasFile(ctx, autobumpConfigPath) {
		return nil, false
	}

	content, err := dctx.FileContentFetcher.GetFileContent(ctx, autobumpConfigPath)
	if err != nil {
		logger.Warnf("failed to read %s: %v, falling back to default patterns", autobumpConfigPath, err)
		return nil, false
	}

	cfg, err := autobump.ParseConfig(content)
	if err != nil {
		logger.Warnf("failed to parse %s: %v, falling back to default patterns", autobumpConfigPath, err)
		return nil, false
	}

	versionPaths := autobump.ResolveVersionFilePaths(cfg, autobumpLangKey, dctx.RepoName)

	required := map[string]bool{"CHANGELOG.md": true}
	for _, p := range versionPaths {
		required[p] = true
	}

	return required, true
}

// validateAutobumpRequired checks that all required autobump files are present in the PR.
func validateAutobumpRequired(
	dctx repositories.DetectionContext,
	detectorName string,
	required map[string]bool,
) repositories.DetectionResult {
	prFiles := make(map[string]bool, len(dctx.Files))
	for _, f := range dctx.Files {
		prFiles[f] = true
	}

	var missing []string
	for exp := range required {
		if !prFiles[exp] {
			missing = append(missing, exp)
		}
	}

	if len(missing) > 0 {
		return repositories.DetectionResult{
			Detected: true,
			Verdict:  "reject",
			Summary: fmt.Sprintf(
				"%s version bump is incomplete per .autobump.yaml: %s",
				detectorName, strings.Join(missing, ", "),
			),
		}
	}

	return repositories.DetectionResult{
		Detected: true,
		Verdict:  "approve",
		Summary: fmt.Sprintf(
			"%s version bump detected (%d files, validated against .autobump.yaml). Auto-approved by trivial PR policy.",
			detectorName,
			len(dctx.Files),
		),
	}
}
