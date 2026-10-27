// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"bytes"
	"strings"
	"testing"
)

// TestTheSignCommandRefusesABoundBelowOne drives `aggrega sign` through its own
// flag parsing with --jobs below 1 and requires the refusal to name the flag and
// to come before anything else the command does. The command runs with no
// workspace attached, so any step past the refusal — reading the workspace, and
// after it any reference — would fail differently; the control row, a legal
// bound, is that different failure, which shows the flag was parsed and the
// command went on past it.
func TestTheSignCommandRefusesABoundBelowOne(t *testing.T) {
	t.Parallel()
	const ref = "registry.example.test/component/core/api-server:1.0.0"
	for _, row := range []struct {
		name string
		args []string
		want string
	}{
		{"a zero bound", []string{"--jobs", "0", ref}, "aggrega sign: --jobs must be at least 1, got 0"},
		{"a negative bound", []string{"--jobs=-3", ref}, "aggrega sign: --jobs must be at least 1, got -3"},
		{"control: a legal bound passes the check", []string{"--jobs", "2", ref}, ""},
	} {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			c := newSignCmd()
			var out bytes.Buffer
			c.SetOut(&out)
			c.SetErr(&out)
			c.SetArgs(row.args)
			c.SilenceUsage = true
			c.SilenceErrors = true
			err := c.ExecuteContext(t.Context())
			if err == nil {
				t.Fatal("the command ran with no workspace attached and reported no failure")
			}
			if row.want == "" {
				if strings.Contains(err.Error(), "--jobs") {
					t.Errorf("a legal bound was refused: %v", err)
				}
				return
			}
			if err.Error() != row.want {
				t.Errorf("the refusal reads %q; want %q", err.Error(), row.want)
			}
		})
	}
}

// TestTheSignCommandDeclaresItsBound reads the --jobs flag the command declares:
// a whole number, 1 when not passed, whose help says what it bounds.
func TestTheSignCommandDeclaresItsBound(t *testing.T) {
	t.Parallel()
	f := newSignCmd().Flags().Lookup("jobs")
	if f == nil {
		t.Fatal("aggrega sign declares no --jobs flag")
	}
	if f.Value.Type() != "int" || f.DefValue != "1" {
		t.Errorf("--jobs is a %s defaulting to %q; want an int defaulting to \"1\"", f.Value.Type(), f.DefValue)
	}
	if !strings.Contains(f.Usage, "references signed at once") {
		t.Errorf("the --jobs help does not say what it bounds: %q", f.Usage)
	}
}
