// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package main

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A command that accepts the run's context and one that mints its own look identical
// from the outside: both produce the same output, and the difference appears only
// when the reader asks the run to stop. The proofs here ask each command the question
// as a withdrawn-versus-live comparison, with the reason a stronger form is not
// available written at the test.

// TestTheValidateCommandRunsUnderTheRunsOwnContext proves `validate --release` hands
// its own run's context to the validation rather than starting a fresh one.
//
// There is no seam beneath this command a test can park in — the closure resolution
// and the validation build their own catalog view — so the question is asked by
// withdrawing the run before it begins and reading which failure comes back. The
// control is the same invocation under a live context, which must fail for its own
// reason, so the assertion distinguishes a withdrawn run from any other failure
// rather than matching whatever error happens to arrive.
//
// A planted defect makes this fail: replace the command's context with a background
// one anywhere between the command and the validation, and the withdrawn invocation
// reports the control's diagnostics instead of the withdrawal.
func TestTheValidateCommandRunsUnderTheRunsOwnContext(t *testing.T) {
	root, releasePath, catalogDir := probeReleaseWorkspace(t)

	ended, cancel := context.WithCancel(context.Background())
	cancel()
	endedErr := runThroughTheCommandTree(ended, []string{
		"--workspace-root", root, "validate", "--release", releasePath, "--catalog", catalogDir,
	})
	if endedErr == nil {
		t.Fatal("a validation whose run had already been withdrawn reported success")
	}
	if !strings.Contains(endedErr.Error(), context.Canceled.Error()) {
		t.Errorf("the withdrawn validation ended with %v; want a failure naming the withdrawal, "+
			"which is what it reports when the command's own context reaches it", endedErr)
	}

	liveErr := runThroughTheCommandTree(context.Background(), []string{
		"--workspace-root", root, "validate", "--release", releasePath, "--catalog", catalogDir,
	})
	if liveErr == nil {
		t.Fatal("the control validation reported success; this Release has no Recipe for its " +
			"component and must report diagnostics")
	}
	if strings.Contains(liveErr.Error(), context.Canceled.Error()) {
		t.Errorf("the control validation reported the withdrawal too, so the assertion above cannot "+
			"distinguish a withdrawn run from any other failure: %v", liveErr)
	}
}

// TestTheInspectCommandRunsUnderTheRunsOwnContext proves `inspect <release>` hands its
// own run's context to the graph construction rather than starting a fresh one. It is
// the second of the two sites the per-kind renderer reshape opened, and it is the one
// that reaches work: the resolved view builds the graph a build would render.
//
// A planted defect makes this fail: pass context.Background() to BuildGraph instead of
// the renderer's parameter, and the withdrawn invocation renders the entity exactly as
// the control does.
func TestTheInspectCommandRunsUnderTheRunsOwnContext(t *testing.T) {
	root, releasePath, catalogDir := probeReleaseWorkspace(t)

	ended, cancel := context.WithCancel(context.Background())
	cancel()
	endedErr := runThroughTheCommandTree(ended, []string{
		"--workspace-root", root, "inspect", releasePath, "--catalog", catalogDir,
	})
	if endedErr == nil {
		t.Fatal("an inspection whose run had already been withdrawn reported success")
	}
	if !strings.Contains(endedErr.Error(), context.Canceled.Error()) {
		t.Errorf("the withdrawn inspection ended with %v; want a failure naming the withdrawal, "+
			"which is what the graph construction reports when the renderer's context reaches it",
			endedErr)
	}

	if liveErr := runThroughTheCommandTree(context.Background(), []string{
		"--workspace-root", root, "inspect", releasePath, "--catalog", catalogDir,
	}); liveErr != nil {
		t.Errorf("the control inspection failed (%v); the assertion above can only distinguish a "+
			"withdrawn run from a working one if the same invocation succeeds under a live context",
			liveErr)
	}
}

// TestThePluginSyncCommandRunsUnderTheRunsOwnContext proves `plugin sync` hands its
// own run's context to the record alignment, and that the context reaches the guest
// the alignment describes rather than stopping at the operation's edge.
//
// The withdrawn run's failure comes from the runtime closing the module, which is
// what a guest load looks like when the context it runs under ends — so this asserts
// the stop reached the guest, not merely that the caller was released.
//
// A planted defect makes this fail: hand pluginops.Sync a background context instead
// of the command's, and the withdrawn invocation records the host functions exactly
// as the control does.
func TestThePluginSyncCommandRunsUnderTheRunsOwnContext(t *testing.T) {
	wsRoot, entityPath, pluginPath := writeLargeGuestPlugin(t, "")

	ended, cancel := context.WithCancel(context.Background())
	cancel()
	endedErr := runThroughTheCommandTree(ended, []string{
		"--workspace-root", wsRoot, "plugin", "sync", entityPath, "--plugin", pluginPath,
	})
	if endedErr == nil {
		t.Fatal("a record alignment whose run had already been withdrawn reported success")
	}
	if !strings.Contains(endedErr.Error(), context.Canceled.Error()) {
		t.Errorf("the withdrawn alignment ended with %v; want a failure naming the withdrawal, "+
			"which is what the guest load reports when the command's own context reaches it", endedErr)
	}

	if liveErr := runThroughTheCommandTree(context.Background(), []string{
		"--workspace-root", wsRoot, "plugin", "sync", entityPath, "--plugin", pluginPath,
	}); liveErr != nil {
		t.Errorf("the control alignment failed (%v); the assertion above can only distinguish a "+
			"withdrawn run from a working one if the same invocation succeeds under a live context",
			liveErr)
	}
}

// runThroughTheCommandTree dispatches the real command tree with the given arguments
// and returns what it reported, with both of cobra's own streams discarded so the
// test's output carries only its own findings.
func runThroughTheCommandTree(ctx context.Context, args []string) error {
	root := newRootCmd()
	root.SetArgs(args)
	root.SetOut(io.Discard)
	root.SetErr(io.Discard)
	_, err := root.ExecuteContextC(ctx)
	return err
}

// probeReleaseWorkspace writes a Release with a resolvable Aggregate, Manifest and
// Component, and a declared workspace so resolution never climbs above the temporary
// tree. It carries no Recipe and its Manifest maps nothing, which is what makes a
// live validation fail for its own reason — no Recipe resolves for the component —
// while a live inspection still renders. A Manifest mapping the type to a Recipe no
// catalog holds would halt both commands before either reached its own work.
func probeReleaseWorkspace(t *testing.T) (wsRoot, releasePath, catalogDir string) {
	t.Helper()
	root := t.TempDir()
	catalogDir = filepath.Join(root, "catalog")
	if err := os.MkdirAll(filepath.Join(catalogDir, "components"), 0o755); err != nil {
		t.Fatalf("create the probe catalog: %v", err)
	}
	writeFixture(t, filepath.Join(root, "aggregate.yaml"), "apiVersion: aggrega.ai/v1beta1\n"+
		"kind: Aggregate\nmetadata:\n  name: probe\n  version: \"1.0.0\"\n"+
		"spec:\n  components:\n    api:\n      type: server\n")
	writeFixture(t, filepath.Join(root, "manifest.yaml"), "apiVersion: aggrega.ai/v1beta1\n"+
		"kind: Manifest\nmetadata:\n  name: probe\n  version: \"1.0.0\"\n"+
		"spec:\n  mappings: {}\n")
	releasePath = filepath.Join(root, "release.yaml")
	writeFixture(t, releasePath, "apiVersion: aggrega.ai/v1beta1\nkind: Release\n"+
		"metadata:\n  name: probe\n  environment: testing\n  version: \"1.0.0\"\n"+
		"spec:\n  aggregate: ./aggregate.yaml\n  manifest: ./manifest.yaml\n"+
		"  boundaries:\n    main:\n      directory: ./dist/main\n      components:\n        api: {}\n")
	writeFixture(t, filepath.Join(catalogDir, "components", "server.yaml"), "apiVersion: aggrega.ai/v1beta1\n"+
		"kind: Component\nmetadata:\n  name: server\n  version: \"1.0.0\"\n  labels:\n"+
		"    component.aggrega.ai/type: \"server\"\nspec:\n  schema:\n    values: {}\n")
	writeWorkspaceConfig(t, root, "workspace:\n  catalog_dirs: []\n")
	return root, releasePath, catalogDir
}
