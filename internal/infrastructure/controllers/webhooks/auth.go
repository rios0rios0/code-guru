package webhooks

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"strings"
)

// BasicAuthUsername is the fixed username expected on the Authorization Basic header.
// Azure DevOps Service Hooks always send a single password-style secret, so the username
// is a constant placeholder validated to prevent variable-username attacks.
const BasicAuthUsername = "code-guru"

// ErrMissingHeader is returned when the expected auth header is absent or empty.
var ErrMissingHeader = errors.New("missing authentication header")

// ErrInvalidSignature is returned when an HMAC signature does not match the payload.
var ErrInvalidSignature = errors.New("invalid HMAC signature")

// ErrInvalidBasicAuth is returned when Basic Auth credentials are invalid or malformed.
var ErrInvalidBasicAuth = errors.New("invalid basic auth credentials")

// VerifyHMACSHA256 verifies a GitHub-style "sha256=<hex>" signature against the given payload.
func VerifyHMACSHA256(secret string, payload []byte, signatureHeader string) error {
	if signatureHeader == "" {
		return ErrMissingHeader
	}
	if secret == "" {
		return ErrInvalidSignature
	}

	const prefix = "sha256="
	if !strings.HasPrefix(signatureHeader, prefix) {
		return ErrInvalidSignature
	}
	provided, err := hex.DecodeString(signatureHeader[len(prefix):])
	if err != nil {
		return ErrInvalidSignature
	}

	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(payload)
	expected := mac.Sum(nil)

	if subtle.ConstantTimeCompare(expected, provided) != 1 {
		return ErrInvalidSignature
	}
	return nil
}

// VerifyBasicAuth verifies the "Authorization: Basic <base64(user:pass)>" header.
// The username must equal BasicAuthUsername and the password must equal secret.
// Both are compared in constant time to resist timing attacks.
func VerifyBasicAuth(secret string, header string) error {
	if header == "" {
		return ErrMissingHeader
	}
	if secret == "" {
		return ErrInvalidBasicAuth
	}

	// RFC 7617/7235: the auth scheme token ("Basic") is case-insensitive, so
	// "basic <...>" and "BASIC <...>" must be accepted. The credentials part
	// (base64 token) remains case-sensitive.
	const prefix = "Basic "
	if len(header) < len(prefix) || !strings.EqualFold(header[:len(prefix)], prefix) {
		return ErrInvalidBasicAuth
	}

	decoded, err := base64.StdEncoding.DecodeString(header[len(prefix):])
	if err != nil {
		return ErrInvalidBasicAuth
	}

	user, pass, ok := strings.Cut(string(decoded), ":")
	if !ok {
		return ErrInvalidBasicAuth
	}

	userMatch := subtle.ConstantTimeCompare([]byte(user), []byte(BasicAuthUsername)) == 1
	passMatch := subtle.ConstantTimeCompare([]byte(pass), []byte(secret)) == 1
	if !userMatch || !passMatch {
		return ErrInvalidBasicAuth
	}
	return nil
}
