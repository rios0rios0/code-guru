package controllers

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	logger "github.com/sirupsen/logrus"
	"github.com/spf13/cobra"

	"github.com/rios0rios0/codeguru/internal/domain/entities"
	"github.com/rios0rios0/codeguru/internal/infrastructure/controllers/webhooks"
)

const (
	defaultServerPort = 8080
	// defaultShutdownTimeout caps how long `serve` waits for in-flight
	// reviews to drain after SIGTERM before forcibly cancelling them.
	// `90s` is enough for typical reviews to flush their inline comment
	// posts + completion annotation (p95 ≈90s end-to-end), short enough
	// not to fight `kubectl rollout`'s default per-pod grace period
	// (typically 30-300s tunable via `terminationGracePeriodSeconds`),
	// and bounded by the K8s-Lease backend's renewal-loop + lease
	// duration (≤60s recovery) so jobs the drain timeout cancels do
	// not orphan their dedup leases for long.
	defaultShutdownTimeout   = 90 * time.Second
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

	c.installDedupBackend()

	pool := webhooks.NewPool(
		c.settings.Server.Workers,
		c.settings.Server.QueueSize,
		func(ctx context.Context, job webhooks.Job) error {
			// Release the dedup record AFTER the review completes
			// (success or failure). The K8s-Lease backend needs this
			// explicit release because Kubernetes does not auto-delete
			// `Lease` objects when `leaseDurationSeconds` elapses; without
			// it a successful review would leak its lease in etcd forever
			// and block all future webhook deliveries for the same PR.
			defer c.dispatcher.ReleaseDedup(ctx, job.DedupKey)

			// Renew the dedup slot on a ticker for the lifetime of the
			// review so a long job (LLM reviews routinely 5-10 min) does
			// not let its own lease lapse and get stolen by the takeover
			// path. The renewal goroutine's context is the job's context,
			// so the deferred ReleaseDedup above also stops it; on a hard
			// crash the goroutine dies with the pod and the lease becomes
			// stale within ~leaseDurationSeconds, unblocking the next
			// webhook delivery in seconds rather than minutes.
			renewCtx, cancelRenew := context.WithCancel(ctx)
			defer cancelRenew()
			go c.dispatcher.RenewDedup(renewCtx, job.DedupKey)

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
	// Release every dedup slot still held by workers the pool drain
	// could not finish. Without this, jobs cancelled by the drain
	// timeout leave their leases in etcd and block all future webhook
	// deliveries for the same PR until the renewal-loop lapse + lease
	// duration window elapses (≤60s per the K8s-Lease backend) — the
	// safety net for whatever this call cannot release. Captured live
	// at 2026-05-02T00:18Z where four orphaned leases blocked PR
	// reviews for 12 minutes after a routine `kubectl rollout restart`.
	c.dispatcher.ReleaseAllInFlight(shutdownCtx)

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

// installDedupBackend swaps the dispatcher's default in-memory dedup
// for the cross-pod K8s-Lease backend whenever the bot is running
// inside a Kubernetes pod. Cross-pod gating is required because ADO
// fires both `pullrequest.created` and `pullrequest.updated` for every
// new PR and the K8s `Service` round-robins one delivery to each
// replica — without a shared lock both pods independently process,
// produce two reviews, and post duplicate comments (live across
// `internal-terraform/internal-customer-app#NNNN..#NNNN` on `2026-05-01`).
//
// Outside a pod (local CLI runs, unit tests) the dispatcher keeps the
// per-pod in-memory cache the constructor wired in — no operator
// action is required to opt in/out, the heuristic is the standard
// `KUBERNETES_SERVICE_HOST` env var the kubelet always injects.
//
// Failures during in-cluster wiring (e.g. missing RBAC on the
// ServiceAccount) are logged at `Warn` and the dispatcher keeps the
// in-memory cache — never WORSE than the pre-PR baseline. The
// per-pod cache still gates ADO retry storms that loop back to the
// same replica, so the fallback is operationally safe.
func (c *ServeController) installDedupBackend() {
	if !webhooks.IsInKubernetes() {
		logger.Info("serve: KUBERNETES_SERVICE_HOST not set — using per-pod in-memory webhook dedup")
		return
	}
	holder, err := os.Hostname()
	if err != nil || holder == "" {
		holder = "code-guru"
	}
	dedup, err := webhooks.NewK8sLeaseDedupFromInCluster("", holder)
	if err != nil {
		logger.WithError(err).Warn(
			"serve: failed to wire K8s-Lease dedup; falling back to per-pod in-memory cache " +
				"(check the ServiceAccount has the `coordination.k8s.io/leases` Role from dedup_lease.go)",
		)
		return
	}
	c.dispatcher.SetDedup(dedup)
	logger.Infof(
		"serve: K8s-Lease webhook dedup wired (holder=%q) — cross-pod duplicates are now gated by the API server",
		holder,
	)
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
