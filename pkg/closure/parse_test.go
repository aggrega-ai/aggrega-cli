// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package closure

import (
	"errors"
	"testing"
)

func TestParseIdentityRef(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name        string
		input       string
		wantName    string
		wantVersion string
		wantFrag    string
		wantErr     bool
	}{
		{
			name:     "bare-name identity ref",
			input:    "DbSizing#/schemas/Storage",
			wantName: "DbSizing",
			wantFrag: "/schemas/Storage",
		},
		{
			name:        "host-less version floor (colon separator)",
			input:       "DbSizing:1.0.0#/schemas/Storage",
			wantName:    "DbSizing",
			wantVersion: "1.0.0",
			wantFrag:    "/schemas/Storage",
		},
		{
			name:        "nested entry path with version floor",
			input:       "DbSizing:2.1.0#/schemas/nested/DeepRef",
			wantName:    "DbSizing",
			wantVersion: "2.1.0",
			wantFrag:    "/schemas/nested/DeepRef",
		},
		{
			name:    "stale @ version separator rejected with a steer",
			input:   "DbSizing@1.0.0#/schemas/Storage",
			wantErr: true,
		},
		{
			name:    "host-bearing reference rejected",
			input:   "oci://hub.aggrega.ai/schema-library/DbSizing#/schemas/Storage",
			wantErr: true,
		},
		{
			name:    "in-document anchor is not a library ref",
			input:   "#/$defs/Local",
			wantErr: true,
		},
		{
			name:    "fragment-less reference rejected",
			input:   "DbSizing",
			wantErr: true,
		},
		{
			name:    "mappings fragment is the $import surface, not $ref",
			input:   "BaseMappings#/mappings/SimpleValues",
			wantErr: true,
		},
		{
			name:    "empty input rejected",
			input:   "",
			wantErr: true,
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			name, version, fragment, err := LibraryAxisNamed("SchemaLibraries").ParseRef(tc.input)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error for input %q; got nil", tc.input)
				}
				if !errors.Is(err, ErrInvalidLibraryRef()) {
					t.Errorf("error should wrap ErrInvalidLibraryRef; got %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if name != tc.wantName {
				t.Errorf("name: got %q; want %q", name, tc.wantName)
			}
			if version != tc.wantVersion {
				t.Errorf("version: got %q; want %q", version, tc.wantVersion)
			}
			if fragment != tc.wantFrag {
				t.Errorf("fragment: got %q; want %q", fragment, tc.wantFrag)
			}
		})
	}
}

// TestParseIdentityImportRef mirrors TestParseIdentityRef on the MappingLibrary
// `$import:` surface (`#/mappings/`): a host-less bare-name reference parses into
// name + optional version floor + fragment; the host-bearing form, the
// `#/schemas/` (that is the `$ref` surface), a local file path, and an
// in-document anchor are all rejected.
func TestParseIdentityImportRef(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name        string
		input       string
		wantName    string
		wantVersion string
		wantFrag    string
		wantErr     bool
	}{
		{
			name:     "bare-name identity import",
			input:    "BaseMappings#/mappings/SimpleValues",
			wantName: "BaseMappings",
			wantFrag: "/mappings/SimpleValues",
		},
		{
			name:        "host-less version floor (colon separator)",
			input:       "BaseMappings:1.0.0#/mappings/SimpleValues",
			wantName:    "BaseMappings",
			wantVersion: "1.0.0",
			wantFrag:    "/mappings/SimpleValues",
		},
		{
			name:    "stale @ version separator rejected with a steer",
			input:   "BaseMappings@1.0.0#/mappings/SimpleValues",
			wantErr: true,
		},
		{
			name:    "host-bearing reference rejected",
			input:   "oci://hub.aggrega.ai/mapping-library/BaseMappings:1.0.0#/mappings/SimpleValues",
			wantErr: true,
		},
		{
			name:    "schemas fragment is the $ref surface, not $import",
			input:   "DbSizing#/schemas/Storage",
			wantErr: true,
		},
		{
			name:    "local file path is not a library import",
			input:   "./mappings/mariadb.tmpl",
			wantErr: true,
		},
		{
			name:    "in-document anchor is not a library ref",
			input:   "#/$defs/Local",
			wantErr: true,
		},
		{
			name:    "empty input rejected",
			input:   "",
			wantErr: true,
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			name, version, fragment, err := LibraryAxisNamed("MappingLibraries").ParseRef(tc.input)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error for input %q; got nil", tc.input)
				}
				if !errors.Is(err, ErrInvalidLibraryRef()) {
					t.Errorf("error should wrap ErrInvalidLibraryRef; got %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if name != tc.wantName {
				t.Errorf("name: got %q; want %q", name, tc.wantName)
			}
			if version != tc.wantVersion {
				t.Errorf("version: got %q; want %q", version, tc.wantVersion)
			}
			if fragment != tc.wantFrag {
				t.Errorf("fragment: got %q; want %q", fragment, tc.wantFrag)
			}
		})
	}
}

func TestSelectMVSVersionPicksMaxVersion(t *testing.T) {
	t.Parallel()
	// Two consumers pinning compatible minors: MVS picks the maximum.
	versionByConsumer := map[string]string{
		"hub.aggrega.ai/component/core/api-server": "1.0.0",
		"hub.aggrega.ai/component/core/worker":     "1.5.0",
	}
	got, err := selectMVSVersion("oci://hub.aggrega.ai/schema-library/core/base-types", versionByConsumer)
	if err != nil {
		t.Fatalf("selectMVSVersion: %v", err)
	}
	if got != "1.5.0" {
		t.Errorf("selected version: got %q; want %q", got, "1.5.0")
	}
}

func TestSelectMVSVersionDetectsMajorVersionDisagreement(t *testing.T) {
	t.Parallel()
	// Two consumers disagreeing on major version: MVS returns
	// ErrMVSConflict naming the library URI, the conflicting
	// versions, and the consumer chain.
	versionByConsumer := map[string]string{
		"hub.aggrega.ai/component/core/api-server": "1.5.0",
		"hub.aggrega.ai/component/core/worker":     "2.0.0",
	}
	_, err := selectMVSVersion("oci://hub.aggrega.ai/schema-library/core/base-types", versionByConsumer)
	if err == nil {
		t.Fatal("expected ErrMVSConflict; got nil")
	}
	if !errors.Is(err, ErrMVSConflict()) {
		t.Errorf("error should wrap ErrMVSConflict; got %v", err)
	}
	msg := err.Error()
	if !contains(msg, "base-types") {
		t.Errorf("error should name library URI; got: %v", err)
	}
	if !contains(msg, "1.5.0") || !contains(msg, "2.0.0") {
		t.Errorf("error should name both versions; got: %v", err)
	}
	if !contains(msg, "api-server") || !contains(msg, "worker") {
		t.Errorf("error should name both consumers; got: %v", err)
	}
}

func TestSelectMVSVersionAllUnpinnedReturnsEmpty(t *testing.T) {
	t.Parallel()
	versionByConsumer := map[string]string{
		"hub.aggrega.ai/component/core/api-server": "",
		"hub.aggrega.ai/component/core/worker":     "",
	}
	got, err := selectMVSVersion("oci://hub.aggrega.ai/schema-library/core/base-types", versionByConsumer)
	if err != nil {
		t.Fatalf("selectMVSVersion: %v", err)
	}
	if got != "" {
		t.Errorf("expected empty (defer to lookup-reported version); got %q", got)
	}
}

// contains is a tiny strings.Contains wrapper to keep the assertion
// surface tight.
func contains(haystack, needle string) bool {
	return len(needle) == 0 || (len(haystack) >= len(needle) && (haystack == needle || hasSubstring(haystack, needle)))
}

func hasSubstring(haystack, needle string) bool {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}

func TestFormatFragmentRef(t *testing.T) {
	t.Parallel()
	cases := []struct{ in, want string }{
		{"", ""},
		{"/schemas/PortSpec", "#/schemas/PortSpec"},
		{"/mappings/SimpleValues", "#/mappings/SimpleValues"},
	}
	for _, tc := range cases {
		if got := formatFragmentRef(tc.in); got != tc.want {
			t.Errorf("formatFragmentRef(%q) = %q; want %q", tc.in, got, tc.want)
		}
	}
}
