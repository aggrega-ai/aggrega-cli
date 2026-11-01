// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// schemaKeywordAdvisory is the line a run shows for the inline x-aggrega-*
// keyword the fixture's Component authors.
const schemaKeywordAdvisory = `  warning: entity "test/svc" authors x-aggrega-probe at </values/greeting/x-aggrega-probe> ` +
	`in schema content; recommended path is the appropriate declarative table on spec`

// twoRunFillWorkspace is lintOnceWorkspace with its Component authoring an inline
// x-aggrega-* keyword and its Recipe reading a key the render refuses. The refusal
// is what makes a filling build make two runs: the fill's discovery render stops
// on it, so the build has no finished render to present and renders again,
// stopping on it a second time.
func twoRunFillWorkspace(t *testing.T, root string) {
	t.Helper()
	lintOnceWorkspace(t, root)
	catalog := filepath.Join(root, workspaceconfig.ConventionalCatalogDir)
	writeFixture(t, filepath.Join(catalog, "component", "test", "svc", "1.0.0", "component.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Component
metadata:
  name: test/svc
  version: "1.0.0"
  labels:
    component.aggrega.ai/type: server
spec:
  schema:
    values:
      greeting: { type: string, default: "hello", x-aggrega-probe: planted }
    infra:
      replicas: { type: integer, default: 1 }
`)
	writeFixture(t, filepath.Join(catalog, "recipe", "helm", "svc", "1.0.0", "recipe.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Recipe
metadata:
  name: svc
  version: "1.0.0"
spec:
  capability: helm
  implements: test/svc
  workload: |
    apiVersion: aggrega.ai/v1alpha1
    kind: Workload
    replicas: {{ .infra.replicas | default 1 }}
    containers:
      server:
        image: test-server:latest
        variables:
          GREETING: "{{ .context.instance_idd }}"
`)
}

// TestABuildThatFillsItsLockfileShowsARunsAdvisoryOnce drives `aggrega build
// --auto-pull` through the real command tree over a workspace where the fill's
// discovery render stops, so the build renders the Release again: two runs in one
// command, each raising the advisory for the Component's inline x-aggrega-*
// keyword. The command shows it exactly once — the advisory is one fact about the
// workspace, and the reader runs one command.
//
// The assertion is exact, so a command that stopped showing the line fails here
// as surely as one that shows it twice; and the build is required to have stopped
// on the render's refusal, which is what proves the second run happened rather
// than the fill's render being presented.
//
// 🔴 PLANTED DEFECT: hand the fill its own display rather than the build's
// (cmd/aggrega/build.go, buildDispatchConfig — drop `display: display` from the
// pull configuration), or show the catalog view's advisory with a plain write in
// place of showOnce (internal/compiler/pipeline/pipeline.go) — and the line is
// counted twice.
func TestABuildThatFillsItsLockfileShowsARunsAdvisoryOnce(t *testing.T) {
	// Not parallel: the lines are read from the process stderr, and the test
	// moves the process working directory.
	root := t.TempDir()
	twoRunFillWorkspace(t, root)
	t.Chdir(root)

	var out string
	var err error
	stderr := captureStderr(t, func() {
		out, err = runAggrega(t, "--workspace-root", root, "build", "--release", "release.yaml", "--auto-pull")
	})
	if err == nil {
		t.Fatalf("the build rendered past the key the render refuses, so the fixture no longer makes a second run\n"+
			"output:\n%s\nstderr:\n%s", out, stderr)
	}
	if !strings.Contains(err.Error()+out+stderr, "instance_idd") {
		t.Fatalf("the build stopped for a reason other than the render's refusal, so the second run is not proven: %v\n"+
			"output:\n%s\nstderr:\n%s", err, out, stderr)
	}
	// The fill finished — it survives its discovery render stopping and writes the
	// lockfile — so the refusal above came from the build's own render: two runs.
	if !strings.Contains(out+stderr, "Pull complete") {
		t.Fatalf("the fill did not complete, so the build never made a second run\noutput:\n%s\nstderr:\n%s", out, stderr)
	}
	if n := strings.Count(out+"\n"+stderr, schemaKeywordAdvisory+"\n"); n != 1 {
		t.Errorf("a build that fills its lockfile showed the advisory %d time(s); want exactly once\noutput:\n%s\nstderr:\n%s",
			n, out, stderr)
	}
}
