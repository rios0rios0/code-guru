package repositories

// StubTrivialDetector is a test double for the TrivialDetector interface.
type StubTrivialDetector struct {
	NameValue    string
	TrivialValue bool
	SummaryValue string
}

// Name returns the configured adapter name.
func (d *StubTrivialDetector) Name() string {
	return d.NameValue
}

// IsTrivial returns the configured trivial value.
func (d *StubTrivialDetector) IsTrivial(_ []string) bool {
	return d.TrivialValue
}

// Summary returns the configured summary.
func (d *StubTrivialDetector) Summary(_ []string) string {
	return d.SummaryValue
}
