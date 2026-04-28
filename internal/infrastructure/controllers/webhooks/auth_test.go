//go:build unit

package webhooks_test

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/rios0rios0/codeguru/internal/infrastructure/controllers/webhooks"
)

func computeHMACHeader(secret, payload string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(payload))
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

func basicAuthHeader(user, pass string) string {
	return "Basic " + base64.StdEncoding.EncodeToString([]byte(user+":"+pass))
}

func TestVerifyHMACSHA256(t *testing.T) {
	t.Parallel()

	t.Run("should accept a valid sha256 signature", func(t *testing.T) {
		// given
		secret := "topsecret"
		payload := []byte(`{"hello":"world"}`)
		header := computeHMACHeader(secret, string(payload))

		// when
		err := webhooks.VerifyHMACSHA256(secret, payload, header)

		// then
		require.NoError(t, err)
	})

	t.Run("should reject a tampered payload", func(t *testing.T) {
		// given
		secret := "topsecret"
		header := computeHMACHeader(secret, "original")

		// when
		err := webhooks.VerifyHMACSHA256(secret, []byte("tampered"), header)

		// then
		require.Error(t, err)
		assert.True(t, errors.Is(err, webhooks.ErrInvalidSignature))
	})

	t.Run("should reject when the header is missing", func(t *testing.T) {
		// given
		secret := "topsecret"

		// when
		err := webhooks.VerifyHMACSHA256(secret, []byte("payload"), "")

		// then
		require.Error(t, err)
		assert.True(t, errors.Is(err, webhooks.ErrMissingHeader))
	})

	t.Run("should reject when the secret is empty", func(t *testing.T) {
		// given
		payload := []byte("payload")
		header := computeHMACHeader("topsecret", string(payload))

		// when
		err := webhooks.VerifyHMACSHA256("", payload, header)

		// then
		require.Error(t, err)
		assert.True(t, errors.Is(err, webhooks.ErrInvalidSignature))
	})

	t.Run("should reject when the prefix is wrong", func(t *testing.T) {
		// given
		mac := hmac.New(sha256.New, []byte("s"))
		mac.Write([]byte("payload"))
		header := "sha1=" + hex.EncodeToString(mac.Sum(nil))

		// when
		err := webhooks.VerifyHMACSHA256("s", []byte("payload"), header)

		// then
		require.Error(t, err)
		assert.True(t, errors.Is(err, webhooks.ErrInvalidSignature))
	})

	t.Run("should reject when the hex is malformed", func(t *testing.T) {
		// given
		header := "sha256=zzzz"

		// when
		err := webhooks.VerifyHMACSHA256("s", []byte("payload"), header)

		// then
		require.Error(t, err)
		assert.True(t, errors.Is(err, webhooks.ErrInvalidSignature))
	})
}

func TestVerifyBasicAuth(t *testing.T) {
	t.Parallel()

	t.Run("should accept the configured username and secret", func(t *testing.T) {
		// given
		secret := "swordfish"
		header := basicAuthHeader(webhooks.BasicAuthUsername, secret)

		// when
		err := webhooks.VerifyBasicAuth(secret, header)

		// then
		require.NoError(t, err)
	})

	t.Run("should reject a wrong password", func(t *testing.T) {
		// given
		header := basicAuthHeader(webhooks.BasicAuthUsername, "wrong")

		// when
		err := webhooks.VerifyBasicAuth("right", header)

		// then
		require.Error(t, err)
		assert.True(t, errors.Is(err, webhooks.ErrInvalidBasicAuth))
	})

	t.Run("should reject an unexpected username", func(t *testing.T) {
		// given
		header := basicAuthHeader("attacker", "secret")

		// when
		err := webhooks.VerifyBasicAuth("secret", header)

		// then
		require.Error(t, err)
		assert.True(t, errors.Is(err, webhooks.ErrInvalidBasicAuth))
	})

	t.Run("should reject when the header is missing", func(t *testing.T) {
		// given
		// (no header provided)

		// when
		err := webhooks.VerifyBasicAuth("secret", "")

		// then
		require.Error(t, err)
		assert.True(t, errors.Is(err, webhooks.ErrMissingHeader))
	})

	t.Run("should reject when the prefix is wrong", func(t *testing.T) {
		// given
		header := "Bearer " + base64.StdEncoding.EncodeToString([]byte("code-guru:secret"))

		// when
		err := webhooks.VerifyBasicAuth("secret", header)

		// then
		require.Error(t, err)
		assert.True(t, errors.Is(err, webhooks.ErrInvalidBasicAuth))
	})

	t.Run("should reject when base64 is malformed", func(t *testing.T) {
		// given
		header := "Basic !!!"

		// when
		err := webhooks.VerifyBasicAuth("secret", header)

		// then
		require.Error(t, err)
		assert.True(t, errors.Is(err, webhooks.ErrInvalidBasicAuth))
	})

	t.Run("should reject when the secret is empty", func(t *testing.T) {
		// given
		header := basicAuthHeader(webhooks.BasicAuthUsername, "anything")

		// when
		err := webhooks.VerifyBasicAuth("", header)

		// then
		require.Error(t, err)
		assert.True(t, errors.Is(err, webhooks.ErrInvalidBasicAuth))
	})

	t.Run("should reject when the credentials lack a colon", func(t *testing.T) {
		// given
		header := "Basic " + base64.StdEncoding.EncodeToString([]byte("nocolonhere"))

		// when
		err := webhooks.VerifyBasicAuth("secret", header)

		// then
		require.Error(t, err)
		assert.True(t, errors.Is(err, webhooks.ErrInvalidBasicAuth))
	})

	t.Run("should accept the lowercase scheme prefix per RFC 7617", func(t *testing.T) {
		// given
		secret := "swordfish"
		header := "basic " + base64.StdEncoding.EncodeToString([]byte(webhooks.BasicAuthUsername+":"+secret))

		// when
		err := webhooks.VerifyBasicAuth(secret, header)

		// then
		require.NoError(t, err)
	})
}
