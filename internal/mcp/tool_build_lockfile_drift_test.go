// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package mcp

// The build tool's lockfile posture, across all three inputs a caller can hand
// it: no lockfile, a current one, and one that has drifted.
//
// It sits in its own file rather than beside the other gate cases because the
// subject spans two tools: the refusal is the build tool's, and the remedy the
// refusal names is the dependency-resolution tool's, so the round trip is a
// cross-tool integration case and its tier should be readable from the filename.
//
// Drift is introduced by moving the ENTITY, never by splicing the lockfile. A
// recorded pin's identity is what the verification compares, so editing the
// Aggregate moves it — and it moves it without touching the file whose first
// recorded version can belong to a local-path entity carrying no version at all,
// which is a row a splice can silently land on.

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compilerops"
	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
)

// driftTheAggregate appends a comment to the workspace's Aggregate, which moves
// the digest the lockfile recorded for it and nothing else.
//
// One edit, one fault. The rendered output is unchanged by a comment, so the
// drifted case and the positive control below differ in the drift and in nothing
// else — which is what makes a refusal here attributable to the drift and not to
// some second difference between two fixtures.
//
// It asserts the edit actually landed. A helper that wrote nothing would leave
// the lockfile agreeing with the resolution, and every case built on it would
// then be asserting against a workspace carrying no drift at all.
func driftTheAggregate(t *testing.T, root string) {
	t.Helper()
	path := filepath.Join(root, "aggregate.yaml")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read the Aggregate to drift it: %v", err)
	}
	after := append(bytes.Clone(before), []byte("\n# The recorded identity of this entity moves with its bytes.\n")...)
	if err := os.WriteFile(path, after, 0o644); err != nil {
		t.Fatalf("write the drifted Aggregate: %v", err)
	}
	if bytes.Equal(before, after) {
		t.Fatal("the edit changed nothing, so the lockfile still matches what the Release resolves to and " +
			"this fixture carries no drift")
	}
}

// resolveTheWorkspace runs the dependency-resolution tool so the workspace holds
// a lockfile that matches what the Release resolves to, and returns its path.
func resolveTheWorkspace(t *testing.T, s *server, root string, dirs []string, releasePath string) string {
	t.Helper()
	res := callPull(t, s, pullArgs{Release: releasePath, WorkspaceRoot: root, CatalogDirs: dirs})
	if res.IsError {
		t.Fatalf("the preparing resolution refused, so no case here has a current lockfile to start from: %s",
			res.Content[0].Text)
	}
	return told(unmarshalPullPayload(t, res).Lockfile)
}

// callBuild drives the build tool through the real tools/call dispatch.
func callBuild(t *testing.T, s *server, root string, dirs []string, releasePath string) *callToolResult {
	t.Helper()
	return callTool(t, s, "build", buildArgs{
		Release:       releasePath,
		WorkspaceRoot: root,
		CatalogDirs:   dirs,
	})
}

// TestBuildToolRefusesADriftedLockfile is the behaviour this surface was missing:
// a lockfile that no longer describes what the Release resolves to stops the
// build here exactly as it stops one at a terminal.
//
// Before, this surface rendered such a Release and returned a complete-looking
// artifact set — resolved against stale pins, while its own source claimed those
// bytes were the bytes a build emits. A build would have refused rather than
// emitted them.
//
// The refusal is pinned to the drift verification by the CONDITION that
// verification raises, not by the summary sentence: the sentence is this
// surface's presentation and free to be reworded, while the condition is what
// the gate is. An any-error assertion would keep passing once some other gate
// started refusing the same workspace, and would silently stop covering drift.
func TestBuildToolRefusesADriftedLockfile(t *testing.T) {
	s, root, dirs, releasePath := pullWorkspace(t)
	resolveTheWorkspace(t, s, root, dirs, releasePath)
	driftTheAggregate(t, root)

	res := callBuild(t, s, root, dirs, releasePath)
	if !res.IsError {
		t.Fatalf("a drifted lockfile must refuse the build; got a success payload: %s", res.Content[0].Text)
	}
	text := res.Content[0].Text
	if !strings.Contains(text, lockfile.ErrLockfileDrift().Error()) {
		t.Errorf("the refusal must come from the lockfile-drift verification, whose condition it does not "+
			"carry:\n%s", text)
	}
}

// TestBuildToolBuildsTheSameWorkspaceWithACurrentLockfile is the matched positive
// control. Without it the case above would also pass on a tool that refused every
// build — the failure mode a single negative case structurally cannot
// distinguish. The two differ in one appended comment and in nothing else.
func TestBuildToolBuildsTheSameWorkspaceWithACurrentLockfile(t *testing.T) {
	s, root, dirs, releasePath := pullWorkspace(t)
	resolveTheWorkspace(t, s, root, dirs, releasePath)

	res := callBuild(t, s, root, dirs, releasePath)
	if res.IsError {
		t.Fatalf("the same workspace with a current lockfile must build: %s", res.Content[0].Text)
	}
	if len(unmarshalBuildPayload(t, res).Files) == 0 {
		t.Fatal("a successful build must return the rendered files")
	}
}

// TestBuildToolWithNoLockfileStillBuilds is the input this change deliberately
// left alone, asserted rather than assumed.
//
// The drift verification stands down on its own inputs when there is no lockfile,
// so no condition was added for this case and none is needed. That is precisely
// why it owes an assertion: a guard written to hold it would be visible in the
// diff, whereas a structural stand-down is invisible, and the way it would break
// is by starting to refuse a Release nobody had pinned yet.
func TestBuildToolWithNoLockfileStillBuilds(t *testing.T) {
	s, root, dirs, releasePath := pullWorkspace(t)

	lockPath := lockfile.PathFor(releasePath)
	if _, err := os.Stat(lockPath); err == nil {
		t.Fatalf("the fixture already carries a lockfile at %s, so this case would measure the current-lockfile "+
			"input instead of the absent one", lockPath)
	}

	res := callBuild(t, s, root, dirs, releasePath)
	if res.IsError {
		t.Fatalf("a Release with no lockfile must render from whatever pins the workspace holds; it refused: %s",
			res.Content[0].Text)
	}
	if len(unmarshalBuildPayload(t, res).Files) == 0 {
		t.Fatal("a build with no lockfile must still return the rendered files")
	}
}

// TestBuildToolDriftIsClearedByTheRemedyItNames asserts the round trip rather
// than the refusal.
//
// A halt whose named next step does not work is worse than the silence it
// replaced, so what is asserted is the sequence a caller actually performs:
// refuse, resolve the dependencies through the tool the refusal names — with the
// same Release and no named upgrade targets, which is the reconciling form — then
// build. The third step succeeding is the whole point.
//
// It also pins the reconciling form as SUFFICIENT. Drift is a membership or
// identity divergence, and re-resolving the closure is enough to clear it. The
// command-line diagnostic that travels in the same guidance names that same
// reconcile at a terminal, and the refusal is held to carrying it, so the two
// instructions a caller receives cannot come to disagree.
func TestBuildToolDriftIsClearedByTheRemedyItNames(t *testing.T) {
	s, root, dirs, releasePath := pullWorkspace(t)
	lockPath := resolveTheWorkspace(t, s, root, dirs, releasePath)
	driftTheAggregate(t, root)

	refused := callBuild(t, s, root, dirs, releasePath)
	if !refused.IsError {
		t.Fatalf("the drifted build did not refuse, so this case would prove the remedy clears nothing: %s",
			refused.Content[0].Text)
	}
	if remedy := compilerops.LockfileDriftRemedy(releasePath); !strings.Contains(refused.Content[0].Text, remedy) {
		t.Errorf("the refusal does not carry the shared remedy %q, so the command line it names is not the one "+
			"every other surface names:\n%s", remedy, refused.Content[0].Text)
	}
	drifted, err := os.ReadFile(lockPath)
	if err != nil {
		t.Fatalf("read the lockfile the refusal was about: %v", err)
	}

	// The remedy, invoked as the summary names it: the same Release, no upgrade
	// targets. UpgradeTargets left nil is what asks for a reconcile.
	remedy := callPull(t, s, pullArgs{Release: releasePath, WorkspaceRoot: root, CatalogDirs: dirs})
	if remedy.IsError {
		t.Fatalf("the remedy the refusal names refused: %s", remedy.Content[0].Text)
	}
	reconciled, err := os.ReadFile(lockPath)
	if err != nil {
		t.Fatalf("read the lockfile the remedy wrote: %v", err)
	}
	if bytes.Equal(drifted, reconciled) {
		t.Fatal("the remedy left the lockfile byte-identical, so it cannot have re-recorded the identity that " +
			"drifted and any success below would be for some other reason")
	}

	built := callBuild(t, s, root, dirs, releasePath)
	if built.IsError {
		t.Fatalf("following the remedy the refusal names did not clear it, which makes the halt a dead end: %s",
			built.Content[0].Text)
	}
	if len(unmarshalBuildPayload(t, built).Files) == 0 {
		t.Fatal("the build after the remedy returned no rendered files")
	}
}

// TestBuildDriftSummaryNamesTheInProtocolRemedy asserts the summary IN ISOLATION,
// and that isolation is the whole reason this case exists separately from the
// round trip above.
//
// The composed guidance a caller receives carries the shared diagnostic as well,
// and that diagnostic already names a command line containing the word "pull".
// So an assertion made against the composed text would pass on a summary that
// named no remedy at all — the shared text would satisfy it. Calling the summary
// directly is what makes the assertion about this surface's own sentence.
//
// What is asserted is the tokens a caller ACTS on — the tool it invokes and the
// arguments it sends — not the wording around them, which is free to change.
func TestBuildDriftSummaryNamesTheInProtocolRemedy(t *testing.T) {
	summary := previewFailureSummary(&compilerops.PreviewResult{
		FailedGate: compilerops.GateLockfileDrift,
	}, nil)

	for _, owed := range []string{"pull", "release", "upgrade_targets"} {
		if !strings.Contains(summary, owed) {
			t.Errorf("the drift refusal's summary does not name %q, so a caller with no terminal is told the "+
				"build refused and not how to clear it: %q", owed, summary)
		}
	}
	// The command-line form must not be this surface's instruction. It reaches the
	// caller anyway, inside the shared diagnostic; repeating it here as the remedy
	// would tell an assistant to shell out, which is the dead end naming the tool
	// exists to remove.
	if strings.Contains(summary, "aggrega pull") {
		t.Errorf("the drift refusal's summary instructs the caller at a terminal rather than in the protocol: %q",
			summary)
	}
}

// TestBuildFailureSummariesAreDistinctPerGate keeps the arm above from being
// satisfied by a summary that says the same thing for everything.
//
// The summary is chosen by the gate that refused, and its value is that an agent
// can route on it. A switch collapsed to one sentence would satisfy every
// per-gate assertion made about content while destroying exactly that property.
//
// Three gates are deliberately absent, and their absence is the assertion's
// scope rather than an oversight: GateNone and GateClosureResolution both fall
// to the default arm on purpose — a run that reached no gate and one that failed
// to resolve the closure are the same sentence to a reader — and the deprecation
// arm appends the recorded markers, so its text is a function of the run rather
// than of the gate alone.
func TestBuildFailureSummariesAreDistinctPerGate(t *testing.T) {
	gates := []compilerops.PreviewGate{
		compilerops.GateManifestConsistency,
		compilerops.GatePinnedIntegrity,
		compilerops.GateLockfileDrift,
		compilerops.GateClosurePillars,
		compilerops.GateExtends,
		compilerops.GateAxisReconstruction,
		compilerops.GateRecordedSignatures,
		compilerops.GateRender,
	}
	seen := make(map[string]compilerops.PreviewGate, len(gates))
	for _, g := range gates {
		summary := previewFailureSummary(&compilerops.PreviewResult{FailedGate: g}, nil)
		if summary == "" {
			t.Errorf("gate %v yields an empty summary, so a caller is handed nothing to route on", g)
			continue
		}
		if prior, clash := seen[summary]; clash {
			t.Errorf("gates %v and %v yield the identical summary, so the sentence no longer distinguishes "+
				"what refused: %q", prior, g, summary)
			continue
		}
		seen[summary] = g
	}
}
