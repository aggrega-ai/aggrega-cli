// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package publishops

import (
	"context"
	"errors"
	"testing"
	"time"

	"oras.land/oras-go/v2/registry/remote/errcode"

	"github.com/aggrega-ai/aggrega-cli/internal/retry"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
)

// shrinkRetryDelay sets the shared backoff base to a sub-millisecond value for
// the duration of a test so retries do not sleep for real seconds, restoring the
// production default afterward.
func shrinkRetryDelay(t *testing.T) {
	t.Helper()
	prev := retry.BaseDelay
	retry.BaseDelay = 1 * time.Millisecond
	t.Cleanup(func() { retry.BaseDelay = prev })
}

// timeoutError is a net.Error whose Timeout reports true, standing in for a
// transient network timeout in the classifier table.
type timeoutError struct{}

func (timeoutError) Error() string   { return "i/o timeout" }
func (timeoutError) Timeout() bool   { return true }
func (timeoutError) Temporary() bool { return true }

// TestIsRetryablePublishError holds the signing classifier to what it is: the
// registry classifier's answer, plus the keyless authority's token-processing
// failure. The 408 row is one only the registry classifier knows, so it fails if
// this classifier stops deferring to it; the 429 row follows that classifier in
// leaving a registry that asks to slow down to the registry client.
func TestIsRetryablePublishError(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"immutable-tag conflict", oci.ErrImmutableTagConflict(), false},
		{"auth denied", oci.ErrFetchUnauthorized(), false},
		{"caller cancellation", context.Canceled, false},
		{"registry 503", &errcode.ErrorResponse{StatusCode: 503}, true},
		{"registry 408", &errcode.ErrorResponse{StatusCode: 408}, true},
		{"registry 429", &errcode.ErrorResponse{StatusCode: 429}, false},
		{"registry 403", &errcode.ErrorResponse{StatusCode: 403}, false},
		{"net timeout", timeoutError{}, true},
		{"fulcio token 400", errors.New(`signing: keyless bundle: Fulcio returned 400: {"code":3,"message":"There was an error processing the identity token"}`), true},
		{"arbitrary", errors.New("nope"), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsRetryablePublishError(tc.err); got != tc.want {
				t.Errorf("IsRetryablePublishError(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}
