//go:build unit

package support_test

import (
	"errors"
	"fmt"
	"io/fs"
	"os/exec"
	"runtime"
	"strings"
	"syscall"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/rios0rios0/codeguru/internal/support"
)

func TestLooksLikeArgumentListTooLong(t *testing.T) {
	t.Parallel()

	t.Run("should match the real E2BIG error os/exec produces for an oversized argument", func(t *testing.T) {
		t.Parallel()

		// given: the genuine failure, not a hand-built imitation. The whole
		// point of this classifier is that it survives however `os/exec` wraps
		// the kernel's refusal, so the test drives a REAL exec past the
		// argument limit rather than asserting against a fabricated
		// `*fs.PathError`.
		//
		// 4 MiB clears every mainstream POSIX limit, because the platforms do
		// not agree on WHICH limit applies: Linux caps a SINGLE argument at
		// `MAX_ARG_STRLEN` (128 KiB), while macOS and the BSDs impose no
		// per-argument cap and instead bound the whole argument block by
		// `ARG_MAX` (1-2 MiB). Sizing past the largest of them keeps this a
		// real-exec test on every POSIX platform instead of a Linux-only one
		// — the classifier only has to recognise `E2BIG`, not care which of
		// the two limits produced it.
		if runtime.GOOS == "windows" {
			t.Skip("relies on a POSIX binary and the POSIX argument-size limits")
		}
		oversized := strings.Repeat("x", 4*1024*1024)

		// when
		err := exec.Command("/bin/echo", oversized).Run() //nolint:gosec // fixed, non-user-controlled binary

		// then
		require.Error(t, err, "a 4 MiB argument must be refused by the kernel on any POSIX platform")
		assert.True(t, support.LooksLikeArgumentListTooLong(err),
			"the classifier must recognise the exec failure the OS actually produces")
	})

	t.Run("should match a wrapped E2BIG so a backend may add its own context", func(t *testing.T) {
		t.Parallel()

		// given: backends wrap the exec failure with their own detail before
		// classifying it, so the check must unwrap rather than compare.
		wrapped := fmt.Errorf("claude CLI failed: %w",
			&fs.PathError{Op: "fork/exec", Path: "/usr/local/bin/claude", Err: syscall.E2BIG})

		// when
		matched := support.LooksLikeArgumentListTooLong(wrapped)

		// then
		assert.True(t, matched, "a wrapped E2BIG must still classify — backends never return it bare")
	})

	t.Run("should not match an unrelated exec failure", func(t *testing.T) {
		t.Parallel()

		// given: misclassifying a transient failure here would wrongly suppress
		// retries and post operator guidance for a blip, so the negative case
		// matters as much as the positive one.
		unrelated := fmt.Errorf("claude CLI failed: %w", errors.New("exit status 1"))

		// when
		matched := support.LooksLikeArgumentListTooLong(unrelated)

		// then
		assert.False(t, matched, "only the OS argument-limit condition may classify as ErrArgumentListTooLong")
	})

	t.Run("should not match a nil error", func(t *testing.T) {
		t.Parallel()

		// given / when
		matched := support.LooksLikeArgumentListTooLong(nil)

		// then
		assert.False(t, matched, "a successful exec must never classify as a failure")
	})
}
