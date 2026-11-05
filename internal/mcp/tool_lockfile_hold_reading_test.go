// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package mcp

import (
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compilerops"
	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
)

// TestReadingToolsRefuseALockfileTheBuildRefuses holds the two tools that read a
// Release without building it — validate over a Release, and describe of a
// Release's dependency closure — to the lockfile beside it, as their commands are
// held: over a lockfile that matches each answers as it always has, and over one a
// pinned Recipe has drifted from each refuses with the drift halt the build tool
// refuses with, naming the drifted Recipe and the remedy, and the condition's
// descriptive code.
//
// The build tool runs over both states as the control: it passing over the first
// is what shows the lockfile matched, and it refusing over the second is what
// shows the edit drifted the lockfile at all.
func TestReadingToolsRefuseALockfileTheBuildRefuses(t *testing.T) {
	t.Parallel()
	root, releasePath, _ := driftAxesWorkspace(t)
	s := newTestServer(t)
	s.toolchain = pullToolchainStamp

	pinned := true
	if res := callPull(t, s, pullArgs{Release: releasePath, WorkspaceRoot: root, PinSources: &pinned}); res.IsError {
		t.Fatalf("the first resolution refused, so there is no current lockfile to hold anything to: %s", res.Content[0].Text)
	}

	tools := []struct {
		name string
		call func() *callToolResult
	}{
		{"validate", func() *callToolResult {
			return callTool(t, s, "validate", validateArgs{Release: releasePath, WorkspaceRoot: root})
		}},
		{"describe", func() *callToolResult {
			return callTool(t, s, "describe", describeArgs{Release: releasePath, WorkspaceRoot: root})
		}},
	}

	if res := callBuild(t, s, root, nil, releasePath); res.IsError {
		t.Fatalf("the build over the lockfile the first resolution wrote refused: %s", res.Content[0].Text)
	}
	for _, tool := range tools {
		if res := tool.call(); res.IsError {
			t.Errorf("%s over a lockfile that matches refused: %s", tool.name, res.Content[0].Text)
		}
	}

	driftFixtureFile(t, root, "catalog/recipe/helm/svc/1.0.0/recipe.yaml", "", "\n# edited after the lockfile was written\n")

	refused := callBuild(t, s, root, nil, releasePath)
	if !refused.IsError || !strings.Contains(refused.Content[0].Text, `"helm/svc:1.0.0"`) {
		t.Fatalf("the build over the edited Recipe did not refuse naming it, so the edit drifted nothing: %s",
			refused.Content[0].Text)
	}
	for _, tool := range tools {
		res := tool.call()
		if !res.IsError {
			t.Errorf("%s over a lockfile the build refuses answered instead of refusing: %s", tool.name, res.Content[0].Text)
			continue
		}
		text := res.Content[0].Text
		for _, want := range []string{
			lockfile.ErrLockfileDrift().Error(),
			`"helm/svc:1.0.0"`,
			compilerops.LockfileDriftRemedy(releasePath),
			"lockfile-drift",
		} {
			if !strings.Contains(text, want) {
				t.Errorf("%s's refusal does not carry %q:\n%s", tool.name, want, text)
			}
		}
	}
}
