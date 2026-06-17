package trivial

import (
	"context"
	"path/filepath"
	"strings"

	"github.com/rios0rios0/codeguru/internal/domain/entities"
	"github.com/rios0rios0/codeguru/internal/domain/repositories"
)

// Shared constants used by every detector in this package. The verdict
// strings are part of the trivial-detector vocabulary (cross-mapped to
// the LLM vocab in `internal/support/verdict_mapper.go`); `changelogFile`
// is the canonical CHANGELOG path the bump / update detectors require.
const (
	verdictApprove = "approve"
	verdictReject  = "reject"
	changelogFile  = "CHANGELOG.md"
)

// isChangelogOnly reports whether every changed file is the CHANGELOG.
//
// A change that touches ONLY the CHANGELOG is the signature of a version
// bump / release ceremony — exactly what the bump-* detectors exist to
// claim (e.g. bump-go = CHANGELOG.md only, since Go versions via git
// tags). The docs-only and update-* detectors must therefore decline a
// CHANGELOG-only change: it is neither documentation nor a dependency
// update. Treating it as either would auto-approve a version bump even
// when the bump-* adapters are intentionally disabled — the CHANGELOG
// sits in every detector's allowed set, so without this guard a bump PR
// is swallowed by whichever non-bump detector runs first. With the
// guard, a CHANGELOG-only change is matched solely by the bump-*
// detectors, so disabling those adapters reliably keeps version bumps
// out of trivial auto-merge. The CHANGELOG may still ACCOMPANY a docs or
// update PR; it just can never be the sole trigger.
func isChangelogOnly(files []string) bool {
	if len(files) == 0 {
		return false
	}
	for _, f := range files {
		if !strings.EqualFold(filepath.Base(f), changelogFile) {
			return false
		}
	}
	return true
}

// allDetectors contains all built-in trivial PR detectors keyed by name.
//
//nolint:gochecknoglobals // constant lookup map
var allDetectors = map[string]repositories.TrivialDetector{
	"update-go":     &UpdateGoDetector{},
	"update-node":   &UpdateNodeDetector{},
	"update-python": &UpdatePythonDetector{},
	"bump-go":       &BumpGoDetector{},
	"bump-node":     &BumpNodeDetector{},
	"bump-python":   &BumpPythonDetector{},
	"docs-only":     &DocsOnlyDetector{},
}

// DetectorRegistry holds the enabled trivial PR detectors.
type DetectorRegistry struct {
	detectors []repositories.TrivialDetector
}

// NewDetectorRegistry creates a registry with only the named detectors enabled.
// If enabled is empty, no detectors are registered.
func NewDetectorRegistry(enabled []string) *DetectorRegistry {
	var detectors []repositories.TrivialDetector
	for _, name := range enabled {
		if d, ok := allDetectors[name]; ok {
			detectors = append(detectors, d)
		}
	}
	return &DetectorRegistry{detectors: detectors}
}

// NewDetectorRegistryFromConfig is the canonical translation from a
// loaded `entities.TrivialConfig` to a runtime registry. Both the CLI
// `review` controller and the long-lived webhook DI provider call this
// — having one helper prevents the kind of webhook-vs-CLI drift that
// shipped an empty registry to the dispatcher path. An empty / disabled
// config yields an empty registry which short-circuits in
// `handleTrivialDetection`.
func NewDetectorRegistryFromConfig(cfg entities.TrivialConfig) *DetectorRegistry {
	if !cfg.Enabled || len(cfg.Adapters) == 0 {
		return NewDetectorRegistry(nil)
	}
	return NewDetectorRegistry(cfg.Adapters)
}

// Detect checks the file list against all enabled detectors.
// Returns the first matching detector, its result, and true;
// or nil, empty result, and false if none match.
func (r *DetectorRegistry) Detect(
	ctx context.Context,
	dctx repositories.DetectionContext,
) (repositories.TrivialDetector, repositories.DetectionResult, bool) {
	for _, d := range r.detectors {
		result := d.Detect(ctx, dctx)
		if result.Detected {
			return d, result, true
		}
	}
	return nil, repositories.DetectionResult{}, false
}

// AvailableDetectors returns the names of all built-in detectors.
func AvailableDetectors() []string {
	names := make([]string, 0, len(allDetectors))
	for name := range allDetectors {
		names = append(names, name)
	}
	return names
}
