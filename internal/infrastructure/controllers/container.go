package controllers

import (
	"go.uber.org/dig"

	"github.com/rios0rios0/codeguru/internal/domain/entities"
	"github.com/rios0rios0/codeguru/internal/infrastructure/controllers/webhooks"
)

// RegisterProviders registers all controller providers with the DIG container.
func RegisterProviders(container *dig.Container) error {
	if err := container.Provide(NewReviewController); err != nil {
		return err
	}
	if err := container.Provide(NewReviewAllController); err != nil {
		return err
	}
	if err := container.Provide(NewDiscoverController); err != nil {
		return err
	}
	if err := container.Provide(NewSelfUpdateController); err != nil {
		return err
	}
	if err := container.Provide(NewVersionController); err != nil {
		return err
	}
	if err := container.Provide(webhooks.NewDispatcher); err != nil {
		return err
	}
	if err := container.Provide(NewServeController); err != nil {
		return err
	}
	if err := container.Provide(NewHealthCheckController); err != nil {
		return err
	}
	if err := container.Provide(NewControllers); err != nil {
		return err
	}

	return nil
}

// NewControllers aggregates all subcommand controllers into a slice for AppInternal.
func NewControllers(
	reviewController *ReviewController,
	reviewAllController *ReviewAllController,
	discoverController *DiscoverController,
	selfUpdateController *SelfUpdateController,
	versionController *VersionController,
	serveController *ServeController,
	healthCheckController *HealthCheckController,
) *[]entities.Controller {
	return &[]entities.Controller{
		reviewController,
		reviewAllController,
		discoverController,
		selfUpdateController,
		versionController,
		serveController,
		healthCheckController,
	}
}
