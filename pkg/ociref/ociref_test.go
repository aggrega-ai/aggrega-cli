// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package ociref

import "testing"

func TestSplitVersionTag(t *testing.T) {
	cases := []struct {
		name        string
		ref         string
		wantBase    string
		wantVersion string
		wantHas     bool
	}{
		{
			name:        "versioned",
			ref:         "oci://host/plugin/cli/moo:0.2.2",
			wantBase:    "oci://host/plugin/cli/moo",
			wantVersion: "0.2.2",
			wantHas:     true,
		},
		{
			name:        "host_port_with_version",
			ref:         "oci://reg.example.com:5000/plugin/cli/moo:1.0",
			wantBase:    "oci://reg.example.com:5000/plugin/cli/moo",
			wantVersion: "1.0",
			wantHas:     true,
		},
		{
			name:        "version_less",
			ref:         "oci://host/plugin/cli/moo",
			wantBase:    "oci://host/plugin/cli/moo",
			wantVersion: "",
			wantHas:     false,
		},
		{
			name:        "no_slash_bare",
			ref:         "moo",
			wantBase:    "moo",
			wantVersion: "",
			wantHas:     false,
		},
		{
			name:        "host_port_no_version",
			ref:         "oci://reg.example.com:5000/plugin/cli/moo",
			wantBase:    "oci://reg.example.com:5000/plugin/cli/moo",
			wantVersion: "",
			wantHas:     false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			base, version, has := SplitVersionTag(tc.ref)
			if base != tc.wantBase || version != tc.wantVersion || has != tc.wantHas {
				t.Errorf("SplitVersionTag(%q) = (%q, %q, %v); want (%q, %q, %v)",
					tc.ref, base, version, has, tc.wantBase, tc.wantVersion, tc.wantHas)
			}
		})
	}
}

func TestVersionlessBase(t *testing.T) {
	cases := []struct {
		ref  string
		want string
	}{
		{"oci://host/plugin/cli/moo:0.2.2", "oci://host/plugin/cli/moo"},
		{"oci://reg.example.com:5000/plugin/cli/moo:1.0", "oci://reg.example.com:5000/plugin/cli/moo"},
		{"oci://reg.example.com:5000/plugin/cli/moo", "oci://reg.example.com:5000/plugin/cli/moo"},
		{"moo", "moo"},
	}
	for _, tc := range cases {
		if got := VersionlessBase(tc.ref); got != tc.want {
			t.Errorf("VersionlessBase(%q) = %q; want %q", tc.ref, got, tc.want)
		}
	}
}
