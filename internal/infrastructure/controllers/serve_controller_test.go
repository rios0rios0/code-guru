package controllers_test

import (
	"testing"

	logger "github.com/sirupsen/logrus"
	logrustest "github.com/sirupsen/logrus/hooks/test"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/dig"

	"github.com/rios0rios0/codeguru/internal/domain/entities"
	"github.com/rios0rios0/codeguru/internal/infrastructure/controllers"
)

// providedSettings returns the settings the dependency-injection container
// hands `serve`.
func providedSettings(t *testing.T) *entities.Settings {
	t.Helper()
	container := dig.New()
	require.NoError(t, entities.RegisterProviders(container))
	var settings *entities.Settings
	require.NoError(t, container.Invoke(func(provided *entities.Settings) { settings = provided }))
	return settings
}

// TestServeControllerSettingsLoadError is deliberately not parallel: it points
// HOME at a temporary directory, changes the working directory, sets
// CODE_GURU_* variables, replaces logrus's exit and captures the global logrus
// logger, which t.Setenv and t.Chdir forbid in a parallel test.
func TestServeControllerSettingsLoadError(t *testing.T) {
	t.Run("should stop the server naming why its settings could not be loaded "+
		"when there is no configuration file and the CODE_GURU_ variables do not validate", func(t *testing.T) {
		// given: a backend named without its API key
		isolateReviewSettings(t)
		t.Setenv("CODE_GURU_BACKEND", "anthropic")
		t.Setenv("CODE_GURU_ANTHROPIC_API_KEY", "")
		controller := controllers.NewServeController(nil, providedSettings(t))
		hook := logrustest.NewLocal(logger.StandardLogger())
		defer hook.Reset()

		// when
		status := runUntilLogrusExit(t, func() { controller.Execute(&cobra.Command{}, nil) })

		// then
		assert.Equal(t, 1, status)
		require.NotNil(t, hook.LastEntry())
		assert.Equal(t, logger.FatalLevel, hook.LastEntry().Level)
		assert.Contains(t, hook.LastEntry().Message, "serve: invalid settings: loading the settings from the "+
			"CODE_GURU_* variables: ai.anthropic.api_key is required when backend is anthropic")
	})
}
