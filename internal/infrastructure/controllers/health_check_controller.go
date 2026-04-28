package controllers

import (
	"context"
	"net/http"
	"os"
	"time"

	logger "github.com/sirupsen/logrus"
	"github.com/spf13/cobra"

	"github.com/rios0rios0/codeguru/internal/domain/entities"
)

const (
	defaultHealthCheckURL     = "http://127.0.0.1:8080/health"
	defaultHealthCheckTimeout = 3 * time.Second
)

// HealthCheckController probes a code-guru `serve` listener and exits with a
// status code suitable for a Dockerfile HEALTHCHECK directive.
//
// The image is built FROM gcr.io/distroless/static-debian12:nonroot, which
// ships no shell, no curl, and no wget; the only executable available inside
// the container is the code-guru binary itself, so the binary has to be its
// own healthcheck client.
type HealthCheckController struct{}

// NewHealthCheckController returns a controller for the `health` subcommand.
func NewHealthCheckController() *HealthCheckController {
	return &HealthCheckController{}
}

// GetBind returns the Cobra command metadata.
func (c *HealthCheckController) GetBind() entities.ControllerBind {
	return entities.ControllerBind{
		Use:   "health",
		Short: "Probe a running code-guru serve listener",
		Long: "Send a GET request to a code-guru /health endpoint and exit 0 " +
			"if it returns 200, 1 otherwise. Used by the Dockerfile HEALTHCHECK " +
			"directive; can also be invoked directly for ad-hoc smoke tests.",
	}
}

// BindFlags registers the --url and --timeout flags.
func (c *HealthCheckController) BindFlags(cmd *cobra.Command) {
	cmd.Flags().String("url", defaultHealthCheckURL,
		"URL of the /health endpoint to probe")
	cmd.Flags().Duration("timeout", defaultHealthCheckTimeout,
		"per-request timeout (must be shorter than the HEALTHCHECK --timeout)")
}

// Execute performs the probe and calls [os.Exit] on its outcome.
func (c *HealthCheckController) Execute(cmd *cobra.Command, _ []string) {
	url, _ := cmd.Flags().GetString("url")
	timeout, _ := cmd.Flags().GetDuration("timeout")

	if err := c.Probe(url, timeout); err != nil {
		logger.Errorf("health: %v", err)
		os.Exit(1)
	}
}

// Probe issues a GET against url and returns nil if the response status is
// 200, an error otherwise. Exposed so callers (and tests) can use the probe
// without going through Cobra and [os.Exit].
func (c *HealthCheckController) Probe(url string, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, http.NoBody)
	if err != nil {
		return &healthCheckError{stage: "build request", inner: err}
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return &healthCheckError{stage: "request", inner: err}
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return &healthCheckError{stage: "status", status: resp.StatusCode}
	}

	return nil
}

type healthCheckError struct {
	stage  string
	inner  error
	status int
}

func (e *healthCheckError) Error() string {
	if e.inner != nil {
		return e.stage + ": " + e.inner.Error()
	}
	return e.stage + ": unexpected status " + http.StatusText(e.status)
}
