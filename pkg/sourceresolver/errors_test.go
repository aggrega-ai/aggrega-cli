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

func TestWrapAppendsRemediationPerSentinel(t *testing.T) {
	t.Parallel()
	spec := sourceresolver.SourceSpec{
		Kind:    "helm-oci-chart",
		URL:     "oci://hub.aggrega.ai/charts/mariadb",
		Version: "25.0.8",
	}
	cases := []struct {
		name     string
		sentinel error
		want     string
	}{
		{"NotFound", sourceresolver.ErrFetchNotFound, "remediation: verify the URL and version"},
		{"Unauthorized", sourceresolver.ErrFetchUnauthorized, "docker login hub.aggrega.ai"},
		{"NetworkUnreachable", sourceresolver.ErrFetchNetworkUnreachable, "check network connectivity to hub.aggrega.ai"},
		{"Integrity", sourceresolver.ErrFetchIntegrity, "aggrega pull --regenerate"},
	}
	for _, tc := range cases {
		err := sourceresolver.Wrap(spec, tc.sentinel, fmt.Errorf("underlying"))
		if !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: Wrap message = %q; want substring %q", tc.name, err.Error(), tc.want)
		}
		if !errors.Is(err, tc.sentinel) {
			t.Errorf("%s: errors.Is should still pass through after remediation append", tc.name)
		}
	}
}

func TestWrapHostExtractionFromOCIScheme(t *testing.T) {
	t.Parallel()
	spec := sourceresolver.SourceSpec{
		Kind:    "helm-oci-chart",
		URL:     "oci://hub.aggrega.ai/charts/mariadb",
		Version: "1.0.0",
	}
	err := sourceresolver.Wrap(spec, sourceresolver.ErrFetchUnauthorized, nil)
	if !strings.Contains(err.Error(), "hub.aggrega.ai") {
		t.Errorf("host extraction failed for oci:// URL; got %q", err.Error())
	}
}

func TestWrapHostExtractionFromHTTPSScheme(t *testing.T) {
	t.Parallel()
	spec := sourceresolver.SourceSpec{
		Kind:    "helm-https-chart",
		URL:     "https://charts.bitnami.com/bitnami/mariadb-25.0.8.tgz",
		Version: "25.0.8",
	}
	err := sourceresolver.Wrap(spec, sourceresolver.ErrFetchNetworkUnreachable, nil)
	if !strings.Contains(err.Error(), "charts.bitnami.com") {
		t.Errorf("host extraction failed for https:// URL; got %q", err.Error())
	}
}

func TestWrapHostExtractionFromGitScheme(t *testing.T) {
	t.Parallel()
	spec := sourceresolver.SourceSpec{
		Kind:    "terraform-module-git",
		URL:     "git+ssh://git@github.com/example/module.git",
		Version: "abc123",
	}
	err := sourceresolver.Wrap(spec, sourceresolver.ErrFetchUnauthorized, nil)
	if !strings.Contains(err.Error(), "git@github.com") {
		t.Errorf("host extraction failed for git+ssh:// URL; got %q", err.Error())
	}
}

func TestWrapSkipsRemediationForNonFetchSentinel(t *testing.T) {
	t.Parallel()
	spec := sourceresolver.SourceSpec{
		Kind:    "helm-oci-chart",
		URL:     "oci://r/c",
		Version: "1.0.0",
	}
	customSentinel := errors.New("custom test sentinel")
	err := sourceresolver.Wrap(spec, customSentinel, nil)
	if strings.Contains(err.Error(), "remediation:") {
		t.Errorf("non-fetch sentinel should not get remediation appendix; got %q", err.Error())
	}
}

var _ = oci.ErrFetchIntegrity // keep oci import healthy if test set shrinks
