package support

import (
	"errors"
	"fmt"
)

// ErrContentSafetyRefusal is the sentinel identifying a review failure whose
// root cause is that the AI backend's content-safety system DECLINED to review
// the pull request — most commonly for security-related code (offensive
// security, exploit or credential-handling code, cryptography) and, for some
// models, certain biology content.
//
// Like [ErrContextWindowExceeded] it is a distinct class with its own handling:
//
//   - It is DETERMINISTIC on the same content — re-sampling the identical diff
//     is declined the same way, so the retry decorator short-circuits it
//     instead of burning the attempt budget on a review that cannot succeed.
//   - It has a DIFFERENT remedy. It is not a defect in the PR and not fixable
//     by pushing a commit; the review clears only with a human review, or by
//     pointing the reviewer at a model/backend whose safety classifiers cover
//     the content. The command layer renders a dedicated annotation saying so.
//
// Match it with [errors.Is]; extract the provider's classification label with
// [errors.As] against a [ContentSafetyRefusalError].
var ErrContentSafetyRefusal = errors.New("ai content-safety refusal")

// ContentSafetyRefusalError is the typed error backends return for a
// content-safety refusal. Category carries the provider's classification label
// for the policy that fired ("cyber", "bio", ...) or "" when the provider does
// not report one. The label is a coarse, provider-assigned classification —
// never raw model output — so the command layer may safely surface it in the
// annotation.
type ContentSafetyRefusalError struct {
	Category string
}

// Error implements the error interface.
func (e *ContentSafetyRefusalError) Error() string {
	if e.Category != "" {
		return fmt.Sprintf("ai declined to review the content (content-safety refusal: %s)", e.Category)
	}

	return "ai declined to review the content (content-safety refusal)"
}

// Is lets [errors.Is] match any ContentSafetyRefusalError against
// ErrContentSafetyRefusal regardless of its Category, so callers classify the
// failure with a single check while still being able to [errors.As] it for the
// category.
func (e *ContentSafetyRefusalError) Is(target error) bool {
	return target == ErrContentSafetyRefusal
}
