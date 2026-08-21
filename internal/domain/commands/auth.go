package commands

// Auth defines the domain command for managing authentication (login, logout, status).
type Auth interface {
	Login() error
	Logout() error
	Status() error
}
