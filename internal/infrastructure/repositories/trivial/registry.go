package trivial

import "github.com/rios0rios0/codeguru/internal/domain/repositories"

// allDetectors contains all built-in trivial PR detectors keyed by name.
var allDetectors = map[string]repositories.TrivialDetector{
	"bump-go":     &BumpGoDetector{},
	"bump-node":   &BumpNodeDetector{},
	"bump-python": &BumpPythonDetector{},
	"docs-only":   &DocsOnlyDetector{},
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
// Returns the first matching detector and true, or nil and false if none match.
func (r *DetectorRegistry) Detect(files []string) (repositories.TrivialDetector, bool) {
	for _, d := range r.detectors {
		if d.IsTrivial(files) {
			return d, true
		}
	}
	return nil, false
}

// AvailableDetectors returns the names of all built-in detectors.
func AvailableDetectors() []string {
	names := make([]string, 0, len(allDetectors))
	for name := range allDetectors {
		names = append(names, name)
	}
	return names
}
