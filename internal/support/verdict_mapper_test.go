//go:build unit

package support_test

import (
	"testing"

	forgeEntities "github.com/rios0rios0/gitforge/pkg/global/domain/entities"
	"github.com/stretchr/testify/assert"

	"github.com/rios0rios0/codeguru/internal/support"
)

func TestMapVerdictToReview(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		verdict     string
		summary     string
		wantOK      bool
		wantVerdict forgeEntities.ReviewVerdict
		wantBody    string
	}{
		{
			name:        "should return Approve submission for approve verdict",
			verdict:     "approve",
			summary:     "all good",
			wantOK:      true,
			wantVerdict: forgeEntities.ReviewVerdictApprove,
			wantBody:    "all good",
		},
		{
			name:        "should return RequestChanges submission for reject verdict",
			verdict:     "reject",
			summary:     "blocking concerns",
			wantOK:      true,
			wantVerdict: forgeEntities.ReviewVerdictRequestChanges,
			wantBody:    "blocking concerns",
		},
		{
			name:    "should skip native review for comment verdict",
			verdict: "comment",
			summary: "FYI",
			wantOK:  false,
		},
		{
			name:    "should skip native review for unknown verdict",
			verdict: "made-up",
			summary: "noise",
			wantOK:  false,
		},
		{
			name:    "should skip native review for empty verdict",
			verdict: "",
			summary: "",
			wantOK:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			// given / when
			sub, ok := support.MapVerdictToReview(tt.verdict, tt.summary)

			// then
			assert.Equal(t, tt.wantOK, ok)
			if tt.wantOK {
				assert.Equal(t, tt.wantVerdict, sub.Verdict)
				assert.Equal(t, tt.wantBody, sub.Body)
			}
		})
	}
}
