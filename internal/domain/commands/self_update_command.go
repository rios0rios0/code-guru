package commands

import "github.com/rios0rios0/codeguru/internal/domain/repositories"

// SelfUpdateCommand runs a self-update of the Code Guru CLI binary.
type SelfUpdateCommand struct {
	repository repositories.SelfUpdaterRepository
}

// NewSelfUpdateCommand creates a new SelfUpdateCommand instance.
func NewSelfUpdateCommand(repository repositories.SelfUpdaterRepository) *SelfUpdateCommand {
	return &SelfUpdateCommand{repository: repository}
}

// Execute performs the self-update, honoring dryRun and force flags.
func (c *SelfUpdateCommand) Execute(dryRun, force bool) error {
	return c.repository.Update(dryRun, force)
}
