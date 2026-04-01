package commands

import logger "github.com/sirupsen/logrus"

// CodeGuruVersion is set at build time via ldflags through the main package bridge.
// During development (`go run`), it defaults to "dev".
//
//nolint:gochecknoglobals // Version set at build time via ldflags
var CodeGuruVersion = "dev"

// VersionCommand prints the current Code Guru CLI version.
type VersionCommand struct{}

// NewVersionCommand creates a new VersionCommand.
func NewVersionCommand() *VersionCommand {
	return &VersionCommand{}
}

// Execute logs the current Code Guru CLI version.
func (c *VersionCommand) Execute() {
	logger.Infof("code-guru version: %s", CodeGuruVersion)
}
