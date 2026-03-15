package controllers

import (
	"fmt"
	"net/http"

	logger "github.com/sirupsen/logrus"
	"github.com/spf13/cobra"

	"github.com/rios0rios0/codeguru/internal/domain/entities"
	"github.com/rios0rios0/codeguru/internal/infrastructure/controllers/webhooks"
)

// ServeController handles the "serve" subcommand for the webhook server.
type ServeController struct {
	dispatcher *webhooks.Dispatcher
}

// NewServeController creates a new ServeController.
func NewServeController(dispatcher *webhooks.Dispatcher) *ServeController {
	return &ServeController{dispatcher: dispatcher}
}

// GetBind returns the Cobra command metadata.
func (c *ServeController) GetBind() entities.ControllerBind {
	return entities.ControllerBind{
		Use:   "serve",
		Short: "Start the webhook server for automatic PR review",
		Long: `Start an HTTP server that receives webhook events from GitHub App
or Azure DevOps Service Hooks. When CI completes on a PR, the server
automatically reviews it and optionally merges trivial PRs.`,
	}
}

// Execute starts the webhook HTTP server.
func (c *ServeController) Execute(cmd *cobra.Command, _ []string) {
	port, _ := cmd.Flags().GetInt("port")
	if port == 0 {
		port = 8080
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprint(w, "ok")
	})
	mux.HandleFunc("POST /webhooks/github", c.dispatcher.HandleGitHub)
	mux.HandleFunc("POST /webhooks/azuredevops", c.dispatcher.HandleAzureDevOps)

	addr := fmt.Sprintf(":%d", port)
	logger.Infof("starting webhook server on %s", addr)

	if err := http.ListenAndServe(addr, mux); err != nil {
		logger.Fatalf("server failed: %v", err)
	}
}
