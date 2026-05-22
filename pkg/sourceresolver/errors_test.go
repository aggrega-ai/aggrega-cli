// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package sourceresolver_test

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/sourceresolver"
)

func TestSentinelReExportsIdentity(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name           string
		sourceResolver error
		canonical      error
	}{
		{"ErrFetchNotFound", sourceresolver.ErrFetchNotFound, oci.ErrFetchNotFound},
		{"ErrFetchUnauthorized", sourceresolver.ErrFetchUnauthorized, oci.ErrFetchUnauthorized},
		{"ErrFetchNetworkUnreachable", sourceresolver.ErrFetchNetworkUnreachable, oci.ErrFetchNetworkUnreachable},
		{"ErrFetchIntegrity", sourceresolver.ErrFetchIntegrity, oci.ErrFetchIntegrity},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			// Identity check: the re-export MUST be the same error
			// value (same pointer / same instance), not a copied
			// errors.New of an identical string. Identity is what
			// makes errors.Is across the two package surfaces
			// resolve to the same underlying sentinel.
			if tc.sourceResolver != tc.canonical {
				t.Errorf("re-export is not identity-equal to canonical pkg/oci declaration; the re-export must use assignment-by-reference, not errors.New")
			}
			// errors.Is symmetry: a chain wrapping the source-resolver
			// re-export resolves true against the canonical pkg/oci
			// sentinel, and vice versa.
			wrapped := fmt.Errorf("ctx: %w", tc.sourceResolver)
			if !errors.Is(wrapped, tc.canonical) {
				t.Errorf("errors.Is(wrapped via re-export, canonical) returned false")
			}
			wrappedCanonical := fmt.Errorf("ctx: %w", tc.canonical)
			if !errors.Is(wrappedCanonical, tc.sourceResolver) {
				t.Errorf("errors.Is(wrapped via canonical, re-export) returned false")
			}
		})
	}
}

func TestWrapPassThroughErrorsIs(t *testing.T) {
	t.Parallel()
	spec := sourceresolver.SourceSpec{
		Kind:    "test-kind",
		URL:     "oci://example.com/foo",
		Version: "1.0.0",
	}

	sentinels := []struct {
		name     string
		sentinel error
	}{
		{"ErrFetchNotFound", sourceresolver.ErrFetchNotFound},
		{"ErrFetchUnauthorized", sourceresolver.ErrFetchUnauthorized},
		{"ErrFetchNetworkUnreachable", sourceresolver.ErrFetchNetworkUnreachable},
		{"ErrFetchIntegrity", sourceresolver.ErrFetchIntegrity},
	}

	for _, tc := range sentinels {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cause := errors.New("transport-level error from upstream library")
			wrapped := sourceresolver.Wrap(spec, tc.sentinel, cause)
			if !errors.Is(wrapped, tc.sentinel) {
				t.Errorf("errors.Is(Wrap-result, %s) returned false", tc.name)
			}
			msg := wrapped.Error()
			if !strings.Contains(msg, "test-kind") {
				t.Errorf("wrapped message should contain kind; got %q", msg)
			}
			if !strings.Contains(msg, "oci://example.com/foo") {
				t.Errorf("wrapped message should contain URL; got %q", msg)
			}
			if !strings.Contains(msg, "1.0.0") {
				t.Errorf("wrapped message should contain version; got %q", msg)
			}
			if !strings.Contains(msg, "transport-level error") {
				t.Errorf("wrapped message should contain cause stringification; got %q", msg)
			}
		})
	}
}

func TestWrapNilCauseOmitsCauseClause(t *testing.T) {
	t.Parallel()
	spec := sourceresolver.SourceSpec{
		Kind:    "test-kind",
		URL:     "oci://example.com/foo",
		Version: "1.0.0",
	}
	wrapped := sourceresolver.Wrap(spec, sourceresolver.ErrFetchNotFound, nil)
	if !errors.Is(wrapped, sourceresolver.ErrFetchNotFound) {
		t.Errorf("nil-cause Wrap should still satisfy errors.Is against the sentinel")
	}
	msg := wrapped.Error()
	if strings.Contains(msg, "cause:") {
		t.Errorf("nil-cause Wrap should omit the cause clause; got %q", msg)
	}
}

func TestWrapAcceptsArbitrarySentinel(t *testing.T) {
	t.Parallel()
	// Verify Wrap is generic across sentinel types: pass an arbitrary
	// custom error, not one of the four fetch sentinels, and
	// confirm the wrapping discipline still applies.
	custom := errors.New("custom transport sentinel for future evolution")
	spec := sourceresolver.SourceSpec{Kind: "test-kind", URL: "x", Version: "1"}
	wrapped := sourceresolver.Wrap(spec, custom, nil)
	if !errors.Is(wrapped, custom) {
		t.Errorf("Wrap should pass through arbitrary sentinels; errors.Is returned false")
	}
}
