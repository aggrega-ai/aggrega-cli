// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package serve

import (
	"path/filepath"
	"strings"
	"testing"
)

// TestPolicyInputDirIsConfinedToTheWorkspace pins the destination rule on the
// governance-payload directory both build methods take: absent asks for nothing,
// a path inside the workspace resolves beneath its root, and one that is absolute
// or climbs out is refused as invalid params naming the field.
func TestPolicyInputDirIsConfinedToTheWorkspace(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	s := &Server{workspaceRoot: root}

	tests := []struct {
		name    string
		raw     string
		wantDir string
		refused bool
	}{
		{name: "absent asks for nothing", raw: "", wantDir: ""},
		{name: "inside the workspace", raw: "policy-inputs", wantDir: filepath.Join(root, "policy-inputs")},
		{name: "climbing out is refused", raw: "../elsewhere", refused: true},
		{name: "absolute is refused", raw: filepath.Join(root, "abs"), refused: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dir, envelope := s.policyInputDir("build", tc.raw)
			if tc.refused {
				if envelope == nil {
					t.Fatalf("%q was accepted as %q", tc.raw, dir)
				}
				if envelope.Code != jsonRPCInvalidParams || !strings.Contains(envelope.Message, "policyInput") {
					t.Fatalf("refusal = %d %q; want invalid params naming policyInput", envelope.Code, envelope.Message)
				}
				return
			}
			if envelope != nil {
				t.Fatalf("%q was refused: %s", tc.raw, envelope.Message)
			}
			if dir != tc.wantDir {
				t.Fatalf("dir = %q, want %q", dir, tc.wantDir)
			}
		})
	}
}
