package commands

import (
	logger "github.com/sirupsen/logrus"

	"github.com/rios0rios0/codeguru/internal/domain/entities"
)

// VersionCommand prints the current Code Guru CLI version.
type VersionCommand struct {
	version entities.AppVersion
}

// NewVersionCommand creates a new VersionCommand.
func NewVersionCommand(version entities.AppVersion) *VersionCommand {
	return &VersionCommand{version: version}
}

// Execute logs the current Code Guru CLI version.
func (c *VersionCommand) Execute() {
	logger.Infof("code-guru version: %s", c.version)
}
