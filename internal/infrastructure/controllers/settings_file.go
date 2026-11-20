package controllers

import (
	"errors"
	"fmt"

	logger "github.com/sirupsen/logrus"

	"github.com/rios0rios0/codeguru/internal/domain/entities"
)

// ErrNoSettingsFile is returned by `review-all` and `discover` when no
// configuration file names the providers and organizations to walk.
var ErrNoSettingsFile = errors.New(
	"no config file found: create ~/.config/code-guru.yaml, or name with --config a file " +
		"that is not part of the code under review",
)

// unreadSettingsReason explains, in each warning about a configuration file
// left unread, why the working directory is not read.
const unreadSettingsReason = "a configuration file in the working directory is read only when --config names it, " +
	"because the working directory may be a checkout of the code under review"

// settingsFileFor returns the configuration file a command reads: the one
// `--config` names, else the operator's own in the home directory
// (entities.FindSettingsFile), else "". It warns about a configuration file in
// the working directory that it leaves unread, which earlier releases read
// first. The warning points at the home directory, never at `--config <that
// file>`: in a checkout, naming the file would hand the pull request's author
// the configuration again.
func settingsFileFor(configPath string) string {
	if configPath != "" {
		return configPath
	}

	path, unread := entities.FindSettingsFile()
	if unread != "" {
		if path != "" {
			logger.Warnf("not reading %s: %s; using %s", unread, unreadSettingsReason, path)
		} else {
			logger.Warnf("not reading %s: %s; move your configuration to ~/.config/code-guru.yaml, "+
				"or name with --config a file that is not part of the code under review", unread, unreadSettingsReason)
		}
	}
	return path
}

// batchSettings loads the configuration `review-all` and `discover` walk. Unlike
// `review`, they have no environment-only mode: the providers and
// organizations come from the file.
func batchSettings(configPath string) (*entities.Settings, error) {
	path := settingsFileFor(configPath)
	if path == "" {
		return nil, ErrNoSettingsFile
	}
	logger.Infof("using config file: %s", path)

	settings, err := entities.NewSettings(path)
	if err != nil {
		return nil, fmt.Errorf("failed to load config: %w", err)
	}
	return settings, nil
}

// failCommandf logs why a `review-all` or `discover` run could not start and
// exits with reviewFailureExitCode, so a scheduled job that read no
// configuration and reviewed nothing fails instead of passing.
func failCommandf(format string, args ...any) {
	logger.Errorf(format, args...)
	exitProcess(reviewFailureExitCode)
}

// warnAboutUnreadServerSettings warns about what the server leaves unread. Its
// settings are loaded before the flags are parsed (provideSettings), from the
// home directory or the CODE_GURU_* variables, so it never reads `--config`,
// and never a configuration file in the working directory
// (entities.FindSettingsFile).
func warnAboutUnreadServerSettings(configFlag string) {
	if configFlag != "" {
		logger.Warnf("not reading %s: the server does not take --config; it reads ~/.code-guru.yaml or "+
			"~/.config/code-guru.yaml, else the CODE_GURU_* variables", configFlag)
	}
	if _, unread := entities.FindSettingsFile(); unread != "" {
		logger.Warnf("not reading %s: the server reads its configuration from ~/.code-guru.yaml or "+
			"~/.config/code-guru.yaml, else from the CODE_GURU_* variables, never from the working directory, "+
			"which may be a checkout of the code under review; move the file to ~/.config/code-guru.yaml to use it",
			unread)
	}
}
