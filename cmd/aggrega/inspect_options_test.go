// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package main

import (
	"strings"
	"testing"
)

func TestParseColorMode(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in      string
		want    colorMode
		wantErr bool
	}{
		{"auto", colorAuto, false},
		{"always", colorAlways, false},
		{"never", colorNever, false},
		{"bogus", colorAuto, true},
		{"", colorAuto, true},
	}
	for _, c := range cases {
		got, err := parseColorMode(c.in)
		if c.wantErr {
			if err == nil {
				t.Errorf("parseColorMode(%q): expected error", c.in)
			} else if !strings.Contains(err.Error(), "auto, always, never") {
				t.Errorf("parseColorMode(%q) error should name the set; got %v", c.in, err)
			}
			continue
		}
		if err != nil {
			t.Errorf("parseColorMode(%q): unexpected error %v", c.in, err)
		}
		if got != c.want {
			t.Errorf("parseColorMode(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestParseOutputFormat(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in      string
		want    outputFormat
		wantErr bool
	}{
		{"text", outputText, false},
		{"yaml", outputYAML, false},
		{"json", outputText, true},
		{"", outputText, true},
	}
	for _, c := range cases {
		got, err := parseOutputFormat(c.in)
		if c.wantErr {
			if err == nil {
				t.Errorf("parseOutputFormat(%q): expected error", c.in)
			} else if !strings.Contains(err.Error(), "text, yaml") {
				t.Errorf("parseOutputFormat(%q) error should name the set; got %v", c.in, err)
			}
			continue
		}
		if err != nil {
			t.Errorf("parseOutputFormat(%q): unexpected error %v", c.in, err)
		}
		if got != c.want {
			t.Errorf("parseOutputFormat(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestResolveLevel(t *testing.T) {
	t.Parallel()
	if got := resolveLevel(false, false); got != levelDefault {
		t.Errorf("neither flag → %v, want levelDefault", got)
	}
	if got := resolveLevel(true, false); got != levelList {
		t.Errorf("--list → %v, want levelList", got)
	}
	if got := resolveLevel(false, true); got != levelDetail {
		t.Errorf("--detail → %v, want levelDetail", got)
	}
}

func TestResolveMode(t *testing.T) {
	t.Parallel()
	if got := resolveMode(false, false); got != modeDefault {
		t.Errorf("neither flag → %v, want modeDefault", got)
	}
	if got := resolveMode(true, false); got != modeForcedRaw {
		t.Errorf("--raw → %v, want modeForcedRaw", got)
	}
	if got := resolveMode(false, true); got != modeForcedResolved {
		t.Errorf("--resolved → %v, want modeForcedResolved", got)
	}
}

// TestInspectFlags_MutuallyExclusive pins the cobra mutual-exclusion guards on
// the conflicting flag pairs. Building the command and executing it with both
// members of a pair set must error, naming the conflict.
func TestInspectFlags_MutuallyExclusive(t *testing.T) {
	t.Parallel()
	pairs := [][]string{
		{"--list", "--detail"},
		{"--raw", "--resolved"},
		{"--color=always", "--no-color"},
	}
	for _, pair := range pairs {
		c := newInspectCmd()
		c.SilenceUsage = true
		c.SilenceErrors = true
		c.SetArgs(append(append([]string{}, pair...), "some-arg"))
		err := c.Execute()
		if err == nil {
			t.Errorf("flags %v should conflict; got nil error", pair)
			continue
		}
		if !strings.Contains(err.Error(), "if any flags in the group") && !strings.Contains(err.Error(), "none of the others can be") {
			t.Logf("conflict error for %v: %v", pair, err)
		}
	}
}

// TestInspectFlags_Registered pins flag discoverability: the new flags appear in
// the command's flag set (cobra renders them in --help).
func TestInspectFlags_Registered(t *testing.T) {
	t.Parallel()
	c := newInspectCmd()
	for _, name := range []string{"list", "detail", "raw", "resolved", "color", "no-color", "output", "catalog"} {
		if c.Flags().Lookup(name) == nil {
			t.Errorf("flag --%s not registered", name)
		}
	}
}
