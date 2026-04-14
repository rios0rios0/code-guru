package repositories

// SelfUpdaterRepository abstracts the mechanism used to update the CLI binary.
type SelfUpdaterRepository interface {
	// Update checks for a newer version and applies the update.
	Update(dryRun, force bool) error
	// CheckForUpdates prints a notice when a newer version is available, without applying it.
	CheckForUpdates()
}
