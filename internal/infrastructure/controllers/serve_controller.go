package controllers

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os/signal"
	"syscall"
	"time"

	logger "github.com/sirupsen/logrus"
	"github.com/spf13/cobra"

	"github.com/rios0rios0/codeguru/internal/domain/entities"
	"github.com/rios0rios0/codeguru/internal/infrastructure/controllers/webhooks"
)

const (
	defaultServerPort        = 8080
	defaultShutdownTimeout   = 30 * time.Second
	defaultReadHeaderTimeout = 10 * time.Second
)

// ServeController handles the "serve" subcommand for the webhook server.
type ServeController struct {
	dispatcher *webhooks.Dispatcher
	settings   *entities.Settings
}

// NewServeController creates a new ServeController.
func NewServeController(
	dispatcher *webhooks.Dispatcher,
	settings *entities.Settings,
) *ServeController {
	return &ServeController{dispatcher: dispatcher, settings: settings}
}

// GetBind returns the Cobra command metadata.
func (c *ServeController) GetBind() entities.ControllerBind {
	return entities.ControllerBind{
		Use:   "serve",
		Short: "Start the webhook server for automatic PR review",
		Long: `Start an HTTP server that receives webhook events from GitHub App
or Azure DevOps Service Hooks. Every supported event is enqueued onto a
bounded worker pool that runs reviews asynchronously.`,
	}
}

// BindFlags registers the --port flag on the Cobra subcommand.
func (c *ServeController) BindFlags(cmd *cobra.Command) {
	cmd.Flags().Int("port", 0, "HTTP port to listen on (overrides config)")
}

// Execute starts the webhook HTTP server.
func (c *ServeController) Execute(cmd *cobra.Command, _ []string) {
	if err := c.validateSettings(); err != nil {
		logger.Fatalf("serve: invalid settings: %v", err)
	}

	port := c.resolvePort(cmd)
	shutdownTimeout := c.resolveShutdownTimeout()

	if c.settings.GitHubApp.AppID != 0 && c.settings.GitHubApp.PrivateKey != "" {
		tokenizer, err := webhooks.NewGitHubAppTokenizer(c.settings.GitHubApp.AppID, c.settings.GitHubApp.PrivateKey)
		if err != nil {
			logger.Fatalf("failed to initialize GitHub App tokenizer: %v", err)
		}
		c.dispatcher.SetGitHubTokenizer(tokenizer)
	}

	pool := webhooks.NewPool(
		c.settings.Server.Workers,
		c.settings.Server.QueueSize,
		func(ctx context.Context, job webhooks.Job) error {
			return c.dispatcher.HandlePR(ctx, job.Provider, job.Repo, job.PR, job.CIPassed)
		},
	)
	c.dispatcher.SetSubmitter(pool)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprint(w, "ok")
	})
	mux.HandleFunc("POST /webhooks/github", c.dispatcher.HandleGitHub)
	mux.HandleFunc("POST /webhooks/azuredevops", c.dispatcher.HandleAzureDevOps)

	addr := fmt.Sprintf(":%d", port)
	logger.Infof("starting webhook server on %s (workers=%d, queue=%d)",
		addr, c.settings.Server.Workers, c.settings.Server.QueueSize)

	//nolint:exhaustruct // only setting required server fields
	server := &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: defaultReadHeaderTimeout,
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	serverErr := make(chan error, 1)
	go func() {
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serverErr <- err
		}
		close(serverErr)
	}()

	select {
	case err := <-serverErr:
		if err != nil {
			logger.Fatalf("server failed: %v", err)
		}
	case <-ctx.Done():
		logger.Info("shutdown signal received; draining server and worker pool")
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()

	if err := server.Shutdown(shutdownCtx); err != nil {
		logger.Errorf("HTTP server shutdown error: %v", err)
	}
	if err := pool.Shutdown(shutdownCtx); err != nil {
		logger.Errorf("worker pool shutdown error: %v", err)
	}

	logger.Info("server stopped")
}

func (c *ServeController) resolvePort(cmd *cobra.Command) int {
	if cmd != nil {
		if flagPort, err := cmd.Flags().GetInt("port"); err == nil && flagPort > 0 {
			return flagPort
		}
	}
	if c.settings.Server.Port > 0 {
		return c.settings.Server.Port
	}
	return defaultServerPort
}

func (c *ServeController) resolveShutdownTimeout() time.Duration {
	if c.settings.Server.ShutdownTimeout > 0 {
		return c.settings.Server.ShutdownTimeout
	}
	return defaultShutdownTimeout
}

// validateSettings enforces the minimum configuration required by the webhook
// server. provideSettings falls back to an empty *Settings on error so the rest
// of the CLI keeps working; this guard ensures `serve` does not silently start
// with that empty fallback.
func (c *ServeController) validateSettings() error {
	if c.settings == nil {
		return errors.New("settings are not configured")
	}
	if c.settings.AI.Backend == "" {
		return errors.New("ai.backend is required (set CODE_GURU_BACKEND or configure via YAML)")
	}
	if c.settings.Server.WebhookSecret == "" {
		return errors.New("server.webhook_secret is required for webhook authentication")
	}
	return nil
}
