package entities

import "time"

// AuthToken holds OAuth 2.0 credentials for API authentication.
type AuthToken struct {
	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token"`
	ExpiresAt    time.Time `json:"expires_at"`
}

// IsExpired returns true if the access token has expired.
func (t *AuthToken) IsExpired() bool {
	return time.Now().After(t.ExpiresAt)
}
