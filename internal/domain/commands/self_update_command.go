package commands

import "github.com/rios0rios0/cliforge/selfupdate"

// SelfUpdateCommand runs a self-update of the Code Guru CLI binary.
type SelfUpdateCommand struct{}

// NewSelfUpdateCommand creates a new SelfUpdateCommand instance.
func NewSelfUpdateCommand() *SelfUpdateCommand {
	return &SelfUpdateCommand{}
}

// Execute performs the self-update, honoring dryRun and force flags.
func (c *SelfUpdateCommand) Execute(dryRun, force bool) error {
	cmd := selfupdate.NewSelfUpdateCommand("rios0rios0", "code-guru", "code-guru", CodeGuruVersion)
	return cmd.Execute(dryRun, force)
}
