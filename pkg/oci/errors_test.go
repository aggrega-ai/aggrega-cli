// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package oci_test

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
)

func TestSentinelPassThroughErrorsIs(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name     string
		sentinel error
	}{
		{"ErrFetchNotFound", oci.ErrFetchNotFound()},
		{"ErrFetchUnauthorized", oci.ErrFetchUnauthorized()},
		{"ErrFetchNetworkUnreachable", oci.ErrFetchNetworkUnreachable()},
		{"ErrFetchIntegrity", oci.ErrFetchIntegrity()},
		{"ErrUnknownEntityKind", oci.ErrUnknownEntityKind()},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			wrapped := fmt.Errorf("context %s: %w", tc.name, tc.sentinel)
			if !errors.Is(wrapped, tc.sentinel) {
				t.Errorf("errors.Is(wrapped, %s) returned false; sentinel does not pass through", tc.name)
			}
		})
	}
}

func TestMediaTypeForKindDispatchesAllKinds(t *testing.T) {
	t.Parallel()
	cases := []struct {
		kind string
		want string
	}{
		{"Component", oci.MediaTypeComponent},
		{"Feature", oci.MediaTypeFeature},
		{"Recipe", oci.MediaTypeRecipe},
		{"FeatureRecipe", oci.MediaTypeFeatureRecipe},
		{"SchemaLibrary", oci.MediaTypeSchemaLibrary},
		{"MappingLibrary", oci.MediaTypeMappingLibrary},
		{"WorkloadLibrary", oci.MediaTypeWorkloadLibrary},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.kind, func(t *testing.T) {
			t.Parallel()
			got, err := oci.MediaTypeForKind(tc.kind)
			if err != nil {
				t.Fatalf("unexpected error for %s: %v", tc.kind, err)
			}
			if got != tc.want {
				t.Errorf("MediaTypeForKind(%q): got %q; want %q", tc.kind, got, tc.want)
			}
		})
	}
}

func TestMediaTypeForKindUnknownReturnsErrUnknownEntityKind(t *testing.T) {
	t.Parallel()
	_, err := oci.MediaTypeForKind("NotARealKind")
	if !errors.Is(err, oci.ErrUnknownEntityKind()) {
		t.Errorf("unknown kind should return ErrUnknownEntityKind; got %v", err)
	}
	// Aggregate is publishable — it resolves to its mediatype.
	mt, err := oci.MediaTypeForKind("Aggregate")
	if err != nil {
		t.Errorf("Aggregate should resolve via MediaTypeForKind; got %v", err)
	}
	if mt != oci.MediaTypeAggregate {
		t.Errorf("Aggregate mediatype = %q; want %q", mt, oci.MediaTypeAggregate)
	}
}

func TestRemediationForMapsEachSentinel(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name     string
		sentinel error
		host     string
		want     string
	}{
		{"NotFound", oci.ErrFetchNotFound(), "hub.aggrega.ai", "remediation: verify the URL and version"},
		{"Unauthorized", oci.ErrFetchUnauthorized(), "hub.aggrega.ai", "docker login hub.aggrega.ai"},
		{"NetworkUnreachable", oci.ErrFetchNetworkUnreachable(), "hub.aggrega.ai", "check network connectivity to hub.aggrega.ai"},
		{"Integrity", oci.ErrFetchIntegrity(), "hub.aggrega.ai", "aggrega pull --upgrade"},
	}
	for _, tc := range cases {
		got := oci.RemediationFor(tc.sentinel, tc.host)
		if !strings.Contains(got, tc.want) {
			t.Errorf("%s: RemediationFor(%v, %q) = %q; want substring %q", tc.name, tc.sentinel, tc.host, got, tc.want)
		}
	}
}

func TestRemediationForReturnsEmptyForNonSentinel(t *testing.T) {
	t.Parallel()
	got := oci.RemediationFor(errors.New("some other error"), "host")
	if got != "" {
		t.Errorf("RemediationFor(non-sentinel) = %q; want empty string", got)
	}
	if oci.RemediationFor(nil, "host") != "" {
		t.Errorf("RemediationFor(nil, ...) should return empty string")
	}
}

func TestRemediationForUsesPlaceholderWhenHostEmpty(t *testing.T) {
	t.Parallel()
	got := oci.RemediationFor(oci.ErrFetchUnauthorized(), "")
	if !strings.Contains(got, "<registry>") {
		t.Errorf("RemediationFor with empty host should use <registry> placeholder; got %q", got)
	}
}

func TestRemediationForTraversesWrappedChain(t *testing.T) {
	t.Parallel()
	// errors.Is should traverse a wrapped chain — callers may pass
	// either the bare sentinel or any error that wraps it.
	wrapped := fmt.Errorf("outer wrap: %w", oci.ErrFetchIntegrity())
	got := oci.RemediationFor(wrapped, "host")
	if !strings.Contains(got, "aggrega pull --upgrade") {
		t.Errorf("RemediationFor should traverse wrapped chain; got %q", got)
	}
}
