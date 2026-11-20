package controllers

import (
	logger "github.com/sirupsen/logrus"

	"github.com/rios0rios0/codeguru/internal/domain/entities"
)

// unreadSettingsReason explains, in each warning about a configuration file
// left unread, why the working directory is not read.
const unreadSettingsReason = "a configuration file in the working directory is read only when --config names it, " +
	"because the working directory may be a checkout of the code under review"

// settingsFileFor returns the configuration file a command reads: the one
// `--config` names, else the operator's own in the home directory
// (entities.FindSettingsFile), else "". It warns about a configuration file in
// the working directory that it leaves unread, which earlier releases read
// first, so an operator who kept theirs there learns how to go on using it.
func settingsFileFor(configPath string) string {
	if configPath != "" {
		return configPath
	}

	path, unread := entities.FindSettingsFile()
	if unread != "" {
		if path != "" {
			logger.Warnf("not reading %s: %s; using %s, pass --config %s to use the other file instead",
				unread, unreadSettingsReason, path, unread)
		} else {
			logger.Warnf("not reading %s: %s; pass --config %s to use it", unread, unreadSettingsReason, unread)
		}
	}
	return path
}
