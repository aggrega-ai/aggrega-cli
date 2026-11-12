// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/pluginops"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// The reach phrasings are pinned as literals here rather than built by calling
// the renderer the commands call. Sharing that helper between the surface and its
// assertion would move both together, so a swap of the two wordings — the one
// mistake that reverses what a reader is told — would pass. The FACT each row
// asserts still comes from the shared operation: every case runs the operation a
// second time in a twin workspace and holds its own ConfigIsMachineWide against
// the row, so the table cannot drift away from what the operation computes.
const (
	wantReachMachineWide = "(machine-wide — every project on this machine resolves through it)"
	wantReachThisProject = "(this project only)"
)

// twinAddResult runs the addition a second time, in its own workspace seeded the
// same way, and hands back the result the command line was rendering from.
//
// It is a separate call rather than a value captured out of the command, because
// an expectation read back from the surface's own projection cannot tell a
// carried fact from a constant — which is the class of defect this whole check
// exists to catch.
func twinAddResult(t *testing.T, look func(string) string, opts pluginops.AddOptions) pluginops.AddResult {
	t.Helper()
	twin := resolvedTestWorkspaceAgainst(t, look)
	res, err := pluginops.Add(context.Background(), twin, opts)
	if err != nil {
		t.Fatalf("twin pluginops.Add: %v", err)
	}
	return res
}

// TestPluginAddCarriesTheWriteReachFromTheSharedResult proves the confirmation
// line states how far the write reached, on both sides of that fact.
//
// Two rows differing only in write target is the whole point: "the line says this
// project only" passes identically whether the reach was read or hardcoded, so
// only the pair is evidence.
func TestPluginAddCarriesTheWriteReachFromTheSharedResult(t *testing.T) {
	// Not parallel: mutates the process-global search and tag-lister seams.
	withSearchTarget(t, fakeRepoLister{repos: []string{
		"plugin/provider/helm",
		"plugin/governance/tag-injector",
	}}, nil)
	withTagLister(t, []string{"1.0.0"})

	cases := []struct {
		name            string
		opts            pluginops.AddOptions
		global          bool
		wantMachineWide bool
		wantReach       string
	}{
		{
			name:            "a provider binding defaults to this project",
			opts:            pluginops.AddOptions{Socket: "provider", Query: "helm", Capability: "helm", Workspace: true},
			wantMachineWide: false,
			wantReach:       wantReachThisProject,
		},
		{
			name:            "the same binding forced global is machine-wide",
			opts:            pluginops.AddOptions{Socket: "provider", Query: "helm", Capability: "helm", Global: true},
			global:          true,
			wantMachineWide: true,
			wantReach:       wantReachMachineWide,
		},
		{
			name:            "a socket attachment defaults to this project",
			opts:            pluginops.AddOptions{Socket: "governance", Role: "label-transform", Query: "tag-injector", Workspace: true},
			wantMachineWide: false,
			wantReach:       wantReachThisProject,
		},
		{
			name:            "the same attachment forced global is machine-wide",
			opts:            pluginops.AddOptions{Socket: "governance", Role: "label-transform", Query: "tag-injector", Global: true},
			global:          true,
			wantMachineWide: true,
			wantReach:       wantReachMachineWide,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var look func(string) string
			if tc.global {
				look = frozenEnv(map[string]string{
					"AGGREGA_CONFIG": filepath.Join(t.TempDir(), "aggrega", "config.yaml"),
				})
			}

			// The fact under test, taken from the operation rather than from the
			// output being examined.
			res := twinAddResult(t, look, tc.opts)
			if res.ConfigIsMachineWide != tc.wantMachineWide {
				t.Fatalf("the operation computed ConfigIsMachineWide=%v for this write target; the row expects %v, "+
					"so the row no longer describes what the operation does",
					res.ConfigIsMachineWide, tc.wantMachineWide)
			}

			rw := resolvedTestWorkspaceAgainst(t, look)
			var out bytes.Buffer
			if err := pluginAddRun(context.Background(), pluginAddConfig{
				Socket:     tc.opts.Socket,
				Query:      tc.opts.Query,
				Capability: tc.opts.Capability,
				Role:       tc.opts.Role,
				Global:     tc.opts.Global,
				Workspace:  tc.opts.Workspace,
				ws:         rw,
				Out:        &out,
			}); err != nil {
				t.Fatalf("pluginAddRun: %v", err)
			}

			got := out.String()
			if !strings.Contains(got, tc.wantReach) {
				t.Errorf("the confirmation line does not state the reach %s that %s computed; it read:\n%s",
					tc.wantReach, "the addition", got)
			}
			// The opposite reach must be absent, or a line carrying both would pass
			// the check above while telling a reader two contradictory things.
			other := wantReachMachineWide
			if tc.wantMachineWide {
				other = wantReachThisProject
			}
			if strings.Contains(got, other) {
				t.Errorf("the confirmation line also states the opposite reach %s:\n%s", other, got)
			}
		})
	}
}

// TestPluginAddCarriesThePinnedReferenceForACommandExtension proves the
// command-extension confirmation names the reference the addition pinned and how
// far that pin reached — the two facts the install path itself cannot report,
// since it never sees the result.
func TestPluginAddCarriesThePinnedReferenceForACommandExtension(t *testing.T) {
	// Not parallel: mutates the process-global search, tag-lister and install seams.
	withCLIRegistrations(t, nil)
	withSearchTarget(t, fakeRepoLister{repos: []string{"plugin/cli/moo"}}, nil)
	withTagLister(t, []string{"1.0.0", "1.2.0"})

	cases := []struct {
		name            string
		workspace       bool
		wantMachineWide bool
		wantReach       string
	}{
		{
			name:            "a command extension defaults to the machine-wide file",
			wantMachineWide: true,
			wantReach:       wantReachMachineWide,
		},
		{
			name:            "the same install pinned to this project only",
			workspace:       true,
			wantMachineWide: false,
			wantReach:       wantReachThisProject,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("XDG_CACHE_HOME", t.TempDir())
			withInstallSeams(t,
				fixedFetch("oci://hub.aggrega.ai/plugin/cli/moo:1.2.0", []byte("\x00asm\x01\x00\x00\x00")),
				fixedDescribe(cliDesc("moo", "a command extension")))

			look := frozenEnv(map[string]string{
				"AGGREGA_CONFIG": filepath.Join(t.TempDir(), "aggrega", "config.yaml"),
			})

			res := twinAddResult(t, look, pluginops.AddOptions{
				Socket:           "cli",
				Query:            "moo",
				Workspace:        tc.workspace,
				AssumeYes:        true,
				InstallCLIPlugin: installCLIPlugin,
				In:               strings.NewReader(""),
				Notices:          &bytes.Buffer{},
			})
			if res.Reference == "" {
				t.Fatal("the addition pinned no reference, so there is no reference for the line to carry")
			}
			if res.ConfigIsMachineWide != tc.wantMachineWide {
				t.Fatalf("the operation computed ConfigIsMachineWide=%v; the row expects %v",
					res.ConfigIsMachineWide, tc.wantMachineWide)
			}

			rw := resolvedTestWorkspaceAgainst(t, look)
			var out bytes.Buffer
			if err := pluginAddRun(context.Background(), pluginAddConfig{
				Socket:    "cli",
				Query:     "moo",
				Workspace: tc.workspace,
				AssumeYes: true,
				ws:        rw,
				In:        strings.NewReader(""),
				Out:       &out,
			}); err != nil {
				t.Fatalf("pluginAddRun (cli): %v", err)
			}

			got := out.String()
			// Built from the operation's own reference, so a line naming a
			// reference the addition did not pin fails here.
			want := "Pinned " + res.Reference + " " + tc.wantReach
			if !strings.Contains(got, want) {
				t.Errorf("output does not carry %q; it read:\n%s", want, got)
			}
		})
	}
}

// TestPluginRemoveCarriesTheWriteReachFromTheSharedResult proves a removal states
// how far its write reached, on both sides of that fact.
//
// The command-extension row is the one that matters most: a cli entry is removed
// from the machine-wide file by default, so a reader standing in one workspace is
// the reader most likely to take a change every workspace sees for a local one.
func TestPluginRemoveCarriesTheWriteReachFromTheSharedResult(t *testing.T) {
	t.Parallel()

	const socketBody = "workspace: {}\nplugins:\n  governance:\n" +
		"    - uri: governance/tag-injector:1.2.0\n      role: label-transform\n"
	cliBody := "workspace: {}\nplugins:\n  cli:\n" +
		"    - uri: oci://hub.aggrega.ai/plugin/cli/greet:1.0.0\n      digest: " + placeholderPin('a') + "\n"

	cases := []struct {
		name            string
		opts            pluginops.RemoveOptions
		globalBody      string
		wantMachineWide bool
		wantReach       string
	}{
		{
			name:            "a socket removal defaults to this project",
			opts:            pluginops.RemoveOptions{Socket: "governance", Query: "tag-injector", Workspace: true},
			wantMachineWide: false,
			wantReach:       wantReachThisProject,
		},
		{
			name:            "a command-extension removal defaults to the machine-wide file",
			opts:            pluginops.RemoveOptions{Socket: "cli", Query: "greet"},
			globalBody:      cliBody,
			wantMachineWide: true,
			wantReach:       wantReachMachineWide,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			// seed builds one workspace for this row: the entry to remove lives in
			// the workspace file, or in a named global file for the row whose
			// removal is meant to land there.
			seed := func() *workspaceconfig.ResolvedWorkspace {
				if tc.globalBody == "" {
					rw, _ := seedWorkspaceConfig(t, socketBody)
					return rw
				}
				globalPath := filepath.Join(t.TempDir(), "config.yaml")
				if err := os.WriteFile(globalPath, []byte(tc.globalBody), 0o644); err != nil {
					t.Fatalf("seed global: %v", err)
				}
				rw, _ := seedWorkspaceConfigAgainst(t, "workspace: {}\n",
					frozenEnv(map[string]string{"AGGREGA_CONFIG": globalPath}))
				return rw
			}

			res, err := pluginops.Remove(seed(), tc.opts)
			if err != nil {
				t.Fatalf("twin pluginops.Remove: %v", err)
			}
			if res.ConfigIsMachineWide != tc.wantMachineWide {
				t.Fatalf("the operation computed ConfigIsMachineWide=%v; the row expects %v",
					res.ConfigIsMachineWide, tc.wantMachineWide)
			}

			var out bytes.Buffer
			if err := pluginRemoveRun(pluginRemoveConfig{
				Socket:    tc.opts.Socket,
				Query:     tc.opts.Query,
				Global:    tc.opts.Global,
				Workspace: tc.opts.Workspace,
				ws:        seed(),
				Out:       &out,
			}); err != nil {
				t.Fatalf("pluginRemoveRun: %v", err)
			}

			got := out.String()
			if !strings.Contains(got, tc.wantReach) {
				t.Errorf("the confirmation line does not state the reach %s the removal computed; it read:\n%s",
					tc.wantReach, got)
			}
			other := wantReachMachineWide
			if tc.wantMachineWide {
				other = wantReachThisProject
			}
			if strings.Contains(got, other) {
				t.Errorf("the confirmation line also states the opposite reach %s:\n%s", other, got)
			}
		})
	}
}
