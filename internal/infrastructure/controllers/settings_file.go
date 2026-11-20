package controllers

import (
	"errors"
	"fmt"
	"strings"

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
// (entities.FindSettingsFile), else "". It warns about every configuration file
// in the working directory that it leaves unread; earlier releases read the
// first of them. The warning points at the home directory, never at `--config
// <that file>`: in a checkout, naming the file would hand the pull request's
// author the configuration again.
func settingsFileFor(configPath string) string {
	if configPath != "" {
		return configPath
	}

	path, unread := entities.FindSettingsFile()
	if len(unread) > 0 {
		if path != "" {
			logger.Warnf("not reading %s: %s; using %s", unreadSettingsFiles(unread), unreadSettingsReason, path)
		} else {
			logger.Warnf("not reading %s: %s; move your configuration to ~/.config/code-guru.yaml, "+
				"or name with --config a file that is not part of the code under review",
				unreadSettingsFiles(unread), unreadSettingsReason)
		}
	}
	return path
}

// unreadSettingsFiles names the working-directory configuration files a warning
// is about, in the order entities.FindSettingsFile found them.
func unreadSettingsFiles(unread []string) string {
	return strings.Join(unread, ", ")
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

// serverConfigFlagError refuses a `--config` given to `serve`. The server's
// settings are loaded before the flags are parsed (provideSettings), from the
// home directory or the CODE_GURU_* variables, so the file would go unread, and
// the server would start without whatever it holds: its webhook allowlists, for
// one.
func serverConfigFlagError(configFlag string) error {
	if configFlag == "" {
		return nil
	}
	return fmt.Errorf("not reading %s: the server does not take --config; it reads ~/.code-guru.yaml or "+
		"~/.config/code-guru.yaml, else the CODE_GURU_* variables, so move the file there", configFlag)
}

// warnAboutUnreadServerSettings warns about every configuration file in the
// working directory, which the server, like every command, never reads
// (entities.FindSettingsFile).
func warnAboutUnreadServerSettings() {
	if _, unread := entities.FindSettingsFile(); len(unread) > 0 {
		logger.Warnf("not reading %s: the server reads its configuration from ~/.code-guru.yaml or "+
			"~/.config/code-guru.yaml, else from the CODE_GURU_* variables, never from the working directory, "+
			"which may be a checkout of the code under review; move your configuration to "+
			"~/.config/code-guru.yaml to use it",
			unreadSettingsFiles(unread))
	}
}
