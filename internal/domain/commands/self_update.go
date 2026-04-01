package commands

// SelfUpdate defines the domain command for performing a CLI self-update.
type SelfUpdate interface {
	Execute(dryRun, force bool) error
}
