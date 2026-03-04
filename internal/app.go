package internal

import "github.com/rios0rios0/codeguru/internal/domain/entities"

// AppInternal aggregates all controllers for the application.
type AppInternal struct {
	controllers []entities.Controller
}

// NewAppInternal creates a new AppInternal with the given controllers.
func NewAppInternal(controllers *[]entities.Controller) *AppInternal {
	return &AppInternal{controllers: *controllers}
}

// GetControllers returns the registered controllers.
func (a *AppInternal) GetControllers() []entities.Controller {
	return a.controllers
}
