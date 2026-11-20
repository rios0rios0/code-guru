package entities

// ProvideSettingsForTest runs the settings provider `serve` and the
// dependency-injection container start from.
func ProvideSettingsForTest() *Settings {
	return provideSettings()
}
