// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"io"
	"testing"
)

// stubLookup returns a LookupEnv-shaped function reporting present only for the
// keys in set (value is irrelevant to the color-off decision, which keys on
// presence per the no-color.org convention).
func stubLookup(set map[string]bool) func(string) (string, bool) {
	return func(k string) (string, bool) {
		if set[k] {
			return "", true
		}
		return "", false
	}
}

// TestResolveGlobalDisplay pins the single-sourced color-off / plain decision:
// color is off when any of --no-color, --plain, or NO_COLOR is set; plain mode
// tracks the --plain flag alone.
func TestResolveGlobalDisplay(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name         string
		noColorFlag  bool
		plainFlag    bool
		noColorEnv   bool
		wantColorOff bool
		wantPlain    bool
	}{
		{"default", false, false, false, false, false},
		{"no-color flag", true, false, false, true, false},
		{"NO_COLOR env", false, false, true, true, false},
		{"plain implies color off", false, true, false, true, true},
		{"plain plus env", false, true, true, true, true},
		{"all set", true, true, true, true, true},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			env := map[string]bool{}
			if tc.noColorEnv {
				env["NO_COLOR"] = true
			}
			got := resolveGlobalDisplay(tc.noColorFlag, tc.plainFlag, stubLookup(env))
			if got.colorOff != tc.wantColorOff {
				t.Errorf("colorOff = %v, want %v", got.colorOff, tc.wantColorOff)
			}
			if got.plain != tc.wantPlain {
				t.Errorf("plain = %v, want %v", got.plain, tc.wantPlain)
			}
		})
	}
}

// TestRootFlags_SetGlobalDisplay pins that the global --plain / --no-color flags
// are registered on the root and that the root's PersistentPreRunE resolves them
// into globalDisplay before a command's RunE runs.
func TestRootFlags_SetGlobalDisplay(t *testing.T) {
	// Not parallel: exercises the process-global globalDisplay via a real command
	// dispatch.
	orig := globalDisplay
	t.Cleanup(func() { globalDisplay = orig })

	root := newRootCmd()
	if root.PersistentFlags().Lookup("plain") == nil {
		t.Fatalf("root must register a persistent --plain flag")
	}
	if root.PersistentFlags().Lookup("no-color") == nil {
		t.Fatalf("root must register a persistent --no-color flag")
	}

	root.SetOut(io.Discard)
	root.SetErr(io.Discard)
	root.SetArgs([]string{"--plain", "version"})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !globalDisplay.plain {
		t.Errorf("--plain must set globalDisplay.plain")
	}
	if !globalDisplay.colorOff {
		t.Errorf("--plain must imply globalDisplay.colorOff")
	}
}
