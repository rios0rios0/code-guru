package commands

import "go.uber.org/dig"

// RegisterProviders registers all command providers with the DIG container.
func RegisterProviders(container *dig.Container) error {
	if err := container.Provide(NewReviewCommand); err != nil {
		return err
	}
	if err := container.Provide(NewReviewAllCommand); err != nil {
		return err
	}
	if err := container.Provide(NewDiscoverCommand); err != nil {
		return err
	}
	if err := container.Provide(NewVersionCommand); err != nil {
		return err
	}
	if err := container.Provide(NewSelfUpdateCommand); err != nil {
		return err
	}

	if err := container.Provide(func(impl *ReviewCommand) Review {
		return impl
	}); err != nil {
		return err
	}
	if err := container.Provide(func(impl *ReviewAllCommand) ReviewAll {
		return impl
	}); err != nil {
		return err
	}
	if err := container.Provide(func(impl *DiscoverCommand) Discover {
		return impl
	}); err != nil {
		return err
	}
	if err := container.Provide(func(impl *VersionCommand) Version {
		return impl
	}); err != nil {
		return err
	}
	if err := container.Provide(func(impl *SelfUpdateCommand) SelfUpdate {
		return impl
	}); err != nil {
		return err
	}

	return nil
}
