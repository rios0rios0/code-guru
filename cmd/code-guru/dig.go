package main

import (
	"github.com/rios0rios0/codeguru/internal"
	"github.com/rios0rios0/codeguru/internal/domain/entities"
	"github.com/rios0rios0/codeguru/internal/infrastructure/controllers"
	"go.uber.org/dig"
)

func provideVersion(container *dig.Container) {
	if err := container.Provide(func() entities.AppVersion {
		return entities.AppVersion(version)
	}); err != nil {
		panic(err)
	}
}

func injectAppContext() *internal.AppInternal {
	container := dig.New()
	provideVersion(container)

	if err := internal.RegisterProviders(container); err != nil {
		panic(err)
	}

	var appInternal *internal.AppInternal
	if err := container.Invoke(func(ai *internal.AppInternal) {
		appInternal = ai
	}); err != nil {
		panic(err)
	}

	return appInternal
}

func injectReviewController() *controllers.ReviewController {
	container := dig.New()
	provideVersion(container)

	if err := internal.RegisterProviders(container); err != nil {
		panic(err)
	}

	var reviewController *controllers.ReviewController
	if err := container.Invoke(func(rc *controllers.ReviewController) {
		reviewController = rc
	}); err != nil {
		panic(err)
	}

	return reviewController
}
