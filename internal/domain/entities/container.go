package entities

import (
	"fmt"

	logger "github.com/sirupsen/logrus"
	"go.uber.org/dig"
)

// RegisterProviders registers all entity providers with the DIG container.
//
// The webhook server (`serve`) needs *Settings at startup to learn its port,
// secret, allowlists, and worker pool dimensions. CLI subcommands like
// `review` continue to load settings independently inside the controller and
// ignore this provider, so a startup error here is recorded on the settings it
// returns (Settings.LoadError) rather than failing DI for the whole binary.
func RegisterProviders(container *dig.Container) error {
	return container.Provide(provideSettings)
}

// provideSettings reads the operator's configuration file in the home
// directory, never one in the working directory (see FindSettingsFile), and
// says nothing about one it leaves there: it runs before the flags are parsed,
// for every subcommand, so a warning here would also reach
// `review --config <that file>`. `serve` warns instead.
func provideSettings() *Settings {
	if path, _ := FindSettingsFile(); path != "" {
		s, err := NewSettings(path)
		if err == nil {
			return s
		}
		logger.Warnf("settings: failed to load %q: %v -- falling back to env", path, err)
	}
	s, err := NewSettingsFromEnv()
	if err != nil {
		// Recorded, and logged at debug level only: this provider runs for every
		// subcommand, `version` and `health` included, and the CLI subcommands
		// (review, review-all, discover, ...) load their own settings and ignore
		// these, so an error line here would only be noise. The subcommand that
		// relies on them, `serve`, reports the recorded error when it validates
		// them (ServeController.validateSettings), so it still fails saying why.
		loadErr := fmt.Errorf("loading the settings from the CODE_GURU_* variables: %w", err)
		logger.Debugf("settings: %v -- using empty defaults", loadErr)
		return &Settings{loadErr: loadErr}
	}
	return s
}
