// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package workspaceconfig

import "testing"

// TestSourceStringRoundTrip pins the wire tokens the Source enum emits; these
// are consumed by the daemon's aggrega/initialize "configSource" field.
func TestSourceStringRoundTrip(t *testing.T) {
	cases := []struct {
		src  Source
		want string
	}{
		{SourceDefaults, "defaults"},
		{SourceGlobal, "global"},
		{SourceWorkspace, "workspace"},
	}
	for _, tc := range cases {
		if got := tc.src.String(); got != tc.want {
			t.Errorf("Source(%d).String(): got %q, want %q", tc.src, got, tc.want)
		}
	}
}
