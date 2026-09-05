// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"testing"
)

func TestBuildNoAcquireInputsFlagRegistered(t *testing.T) {
	t.Parallel()
	c := newBuildCmd()
	f := c.Flags().Lookup("no-acquire-inputs")
	if f == nil {
		t.Fatalf("build command missing the --no-acquire-inputs flag")
	}
	if f.DefValue != "false" {
		t.Errorf("--no-acquire-inputs default = %q, want false (acquisition on by default)", f.DefValue)
	}
}

func TestAcquireCommandRegistered(t *testing.T) {
	t.Parallel()
	root := newRootCmd()
	var found bool
	for _, sub := range root.Commands() {
		if sub.Name() == "acquire" {
			found = true
			if f := sub.Flags().Lookup("release"); f == nil {
				t.Errorf("acquire command missing the --release flag")
			}
			if f := sub.Flags().Lookup("catalog"); f == nil {
				t.Errorf("acquire command missing the --catalog flag")
			}
		}
	}
	if !found {
		t.Fatalf("root command tree does not register the acquire command")
	}
}
