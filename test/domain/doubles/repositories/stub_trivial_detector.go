package repositories

import (
	"context"

	"github.com/rios0rios0/codeguru/internal/domain/repositories"
)

// StubTrivialDetector is a test double for the TrivialDetector interface.
type StubTrivialDetector struct {
	NameValue    string
	DetectResult repositories.DetectionResult
}

// Name returns the configured adapter name.
func (d *StubTrivialDetector) Name() string {
	return d.NameValue
}

// Detect returns the configured detection result.
func (d *StubTrivialDetector) Detect(_ context.Context, _ repositories.DetectionContext) repositories.DetectionResult {
	return d.DetectResult
}
