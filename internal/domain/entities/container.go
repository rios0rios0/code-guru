package entities

import "go.uber.org/dig"

// RegisterProviders registers all entity providers with the DIG container.
func RegisterProviders(_ *dig.Container) error {
	return nil
}
