// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package main

// One documentation bundle, acquired once, read by every surface on the machine.
//
// The bundle is kept in the machine-wide cache, so whichever surface acquires it
// first is the only one that reaches the registry: the assistant server acquiring
// it at the start of its run leaves a copy the command line and the language
// server read over workspaces of their own. All three are driven over their real
// entry points — the tool protocol's message loop, the command tree, the daemon's
// dispatch loop — under one stated cache root and one registry double that
// counts every reach, and serves the bundle on the first reach only: a surface
// that looked somewhere of its own would reach the double again and find nothing
// there, so it would both raise the count and miss the walk-through.

import (
	"context"
	"strings"
	"sync"
	"testing"

	"oras.land/oras-go/v2"
	"oras.land/oras-go/v2/content/memory"

	"github.com/aggrega-ai/aggrega-cli/internal/docsbundle"
	"github.com/aggrega-ai/aggrega-cli/pkg/docscache"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// onceServingDouble is a registry double that counts every reach and serves the
// bundle on the first one only, through the production pull-time verification
// for the reaching workspace.
type onceServingDouble struct {
	mu      sync.Mutex
	reaches int
	bundle  oras.ReadOnlyTarget
}

func (d *onceServingDouble) target(cfg workspaceconfig.Config, ref, version string, warn func(string)) (oras.ReadOnlyTarget, docscache.VerifyAtPull, error) {
	d.mu.Lock()
	d.reaches++
	var served oras.ReadOnlyTarget = memory.New()
	if d.reaches == 1 {
		served = d.bundle
	}
	d.mu.Unlock()
	return served, docsbundle.Verifier(cfg, ref, version, served, warn), nil
}

func (d *onceServingDouble) count() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.reaches
}

// TestOneAcquiredBundleIsReadByEverySurfaceWithoutASecondDownload is the parity
// proof for where the documentation bundle is kept: acquired once by the
// assistant server over one workspace, it is read by `aggrega explain` over a
// third and by the language server's aggrega/explainError over a fourth, all
// three print the same walk-through, and the registry double is reached once in
// total.
func TestOneAcquiredBundleIsReadByEverySurfaceWithoutASecondDownload(t *testing.T) {
	// Not parallel: isolates the process environment and the working directory.
	isolateCLIEnvironment(t)
	short, code, message := explainProbe(t)
	double := &onceServingDouble{bundle: explainBundleStore(t, seededPages(short, code, message, true))}
	root := t.TempDir()
	ctx := docsbundle.WithTarget(docsbundle.WithCacheRoot(context.Background(), root), double.target)

	// Nothing is cached before the first surface runs, or the count below would
	// prove nothing about who acquired what.
	if cached, err := docscache.Cached(root, docsbundle.Ref(explainFixtureRegistry), provenanceCompilerVersion()); err != nil || cached {
		t.Fatalf("the cache root holds a bundle before any surface ran (%v, %v)", cached, err)
	}

	assistantWorkspace, commandWorkspace, daemonWorkspace := explainFixtureWorkspace(t), explainFixtureWorkspace(t), explainFixtureWorkspace(t)

	// The assistant session runs first and acquires. It moves the working
	// directory to its workspace, which the two surfaces after it do not read:
	// each is handed its own workspace root.
	diagnostic := "[CATALOG_ERROR " + short + "] recipe.yaml: " + message
	fromAssistant, _ := explainOverTheAssistantWireWith(t, ctx, assistantWorkspace, diagnostic, assistantDiagnosticKind)
	if got := double.count(); got != 1 {
		t.Fatalf("the assistant server's run reached the registry %d times, want the one acquisition", got)
	}

	fromCommand, notes, err := runExplainCommandWith(t, ctx, "explain", short, "--workspace-root", commandWorkspace)
	if err != nil {
		t.Fatalf("aggrega explain %s: %v\nnotes: %s", short, err, notes)
	}
	fromDaemon := explainOverTheDaemonWireWith(t, ctx, daemonWorkspace, short)

	if got := double.count(); got != 1 {
		t.Errorf("the registry was reached %d times in total, want once: a surface after the first "+
			"downloaded a bundle already on this machine", got)
	}

	// Each surface carries the walk-through, rather than the reason it is missing.
	if len(fromAssistant.Entries) == 0 || fromAssistant.Note != "" {
		t.Errorf("the assistant server carried %d entries and the note %q", len(fromAssistant.Entries), fromAssistant.Note)
	}
	if strings.Contains(notes, "not available") {
		t.Errorf("the command line reports the walk-through missing:\n%s", notes)
	}
	if len(fromDaemon.Entries) == 0 || fromDaemon.Note != "" {
		t.Fatalf("the language server carried %d entries and the note %q", len(fromDaemon.Entries), fromDaemon.Note)
	}

	// And it is the same walk-through on all three, compared through the formats
	// the surface parity case writes independently of the shared composition.
	if got := renderReply(fromDaemon); got != fromCommand {
		t.Errorf("the language server and the command line answer %s differently.\n--- the language server, rendered ---\n%s\n"+
			"--- the command line ---\n%s", short, got, fromCommand)
	}
	if want := renderAssistantGuidance(diagnostic, fromCommand, fromAssistant.DocPointer); fromAssistant.Guidance != want {
		t.Errorf("the assistant server's prose is not the command line's answer.\n--- got ---\n%s\n--- want ---\n%s",
			fromAssistant.Guidance, want)
	}
}
