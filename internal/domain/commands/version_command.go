package commands

import logger "github.com/sirupsen/logrus"

// CodeGuruVersion is set at build time via ldflags through the main package bridge.
// During development (`go run`), it defaults to "dev".
//
//nolint:gochecknoglobals // Version set at build time via ldflags
var CodeGuruVersion = "dev"

type VersionCommand struct{}

func NewVersionCommand() *VersionCommand {
	return &VersionCommand{}
}

func (c *VersionCommand) Execute() {
	logger.Infof("code-guru version: %s", CodeGuruVersion)
}
