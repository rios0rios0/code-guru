package support

import (
	"errors"
	"syscall"
)

// ErrArgumentListTooLong marks a review failure whose root cause is that the
// assembled prompt could not be HANDED to a subprocess backend: the operating
// system refused the exec because one argument exceeded its per-argument
// limit (`E2BIG`).
//
// On Linux that limit is `MAX_ARG_STRLEN` — 32 pages, i.e. 128 KiB — and it
// caps a SINGLE argv string. It is independent of the far larger `ARG_MAX`
// total (2 MiB in a typical container), so an operator who measures `getconf
// ARG_MAX` and concludes there is plenty of headroom is reading the wrong
// number. It is a compile-time kernel constant; no ulimit moves it.
//
// It is its own failure class for the same two reasons the other sentinels
// are:
//
//   - It is DETERMINISTIC. The prompt is byte-for-byte identical on every
//     attempt, so the kernel refuses the re-sample identically — the retry
//     decorator short-circuits it instead of burning the attempt budget.
//   - It has a DIFFERENT remedy, and one the PR author cannot apply. Nothing
//     about the diff caused it: the oversized argument is the SYSTEM prompt,
//     assembled from the operator's rule corpus. It clears when the operator
//     shrinks the configured rules (`rules.categories`), not when the author
//     pushes a commit — which is exactly what the generic annotation would
//     otherwise tell them to do.
//
// The Claude CLI backend passes its system prompt via `--system-prompt-file`
// precisely so this cannot happen. The sentinel covers the fallback path (a
// temp file that could not be created) and any future subprocess backend.
var ErrArgumentListTooLong = errors.New("ai prompt exceeds the operating system argument limit")

// LooksLikeArgumentListTooLong reports whether an exec failure is the OS
// `E2BIG` condition, so a subprocess backend can wrap it with
// [ErrArgumentListTooLong].
//
// Unlike [LooksLikeContextWindowError] this needs no string matching: the
// condition is raised by the kernel, not phrased by a provider. `os/exec`
// surfaces it as a `*fs.PathError` wrapping `syscall.E2BIG` (rendered as
// "fork/exec <binary>: argument list too long"), which [errors.Is] unwraps
// directly. `syscall.E2BIG` is defined on every platform Go supports —
// including Windows — so this needs no build tag.
func LooksLikeArgumentListTooLong(err error) bool {
	return errors.Is(err, syscall.E2BIG)
}
