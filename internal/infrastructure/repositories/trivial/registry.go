package trivial

import (
	"context"

	"github.com/rios0rios0/codeguru/internal/domain/repositories"
)

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
