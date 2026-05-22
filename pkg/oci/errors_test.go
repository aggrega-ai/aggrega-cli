// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package oci_test

import (
	"errors"
	"fmt"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
)

func TestSentinelPassThroughErrorsIs(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name     string
		sentinel error
	}{
		{"ErrFetchNotFound", oci.ErrFetchNotFound},
		{"ErrFetchUnauthorized", oci.ErrFetchUnauthorized},
		{"ErrFetchNetworkUnreachable", oci.ErrFetchNetworkUnreachable},
		{"ErrFetchIntegrity", oci.ErrFetchIntegrity},
		{"ErrUnknownEntityKind", oci.ErrUnknownEntityKind},
		{"ErrLibraryVersionMismatch", oci.ErrLibraryVersionMismatch},
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
		{"Provider", oci.MediaTypeProvider},
		{"SchemaLibrary", oci.MediaTypeSchemaLibrary},
		{"MappingLibrary", oci.MediaTypeMappingLibrary},
		{"CueModule", oci.MediaTypeCueModule},
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
	if !errors.Is(err, oci.ErrUnknownEntityKind) {
		t.Errorf("unknown kind should return ErrUnknownEntityKind; got %v", err)
	}
	// Aggregate is intentionally not in the registry — must NOT
	// resolve.
	_, err = oci.MediaTypeForKind("Aggregate")
	if !errors.Is(err, oci.ErrUnknownEntityKind) {
		t.Errorf("Aggregate must NOT resolve via MediaTypeForKind; got %v", err)
	}
}
