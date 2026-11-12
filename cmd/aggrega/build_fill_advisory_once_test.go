// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package main

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// captureProcessStreams runs fn with the process standard output and standard
// error both captured, and returns what fn wrote to each. A build writes its
// summaries — the fill's "Pull complete" among them — to the process standard
// output and its diagnostics to the process standard error, and neither reaches
// the command's own writer.
func captureProcessStreams(t *testing.T, fn func()) (stdout, stderr string) {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stdout = w
	done := make(chan string, 1)
	go func() {
		var b bytes.Buffer
		_, _ = io.Copy(&b, r)
		done <- b.String()
	}()
	stderr = captureStderr(t, fn)
	_ = w.Close()
	os.Stdout = old
	return <-done, stderr
}

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
	writeFixture(t, filepath.Join(catalog, "component", "test", "svc", "1.0.0", "component.yaml"), `apiVersion: aggrega.ai/v1beta1
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
	writeFixture(t, filepath.Join(catalog, "recipe", "helm", "svc", "1.0.0", "recipe.yaml"), `apiVersion: aggrega.ai/v1beta1
kind: Recipe
metadata:
  name: svc
  version: "1.0.0"
spec:
  capability: helm
  implements: test/svc
  workload: |
    apiVersion: aggrega.ai/v1beta1
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
// as surely as one that shows it twice. The two runs are proven rather than
// assumed, from three facts read off the command's own streams: the fill reports
// its discovery render stopped and still completes ("Pull complete", on the
// process standard output beside the build's other summaries), which is the first
// run; and the command then fails on the render's refusal, which — with the fill
// having returned — only the build's own render can raise, which is the second.
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
	stdout, stderr := captureProcessStreams(t, func() {
		out, err = runAggrega(t, "--workspace-root", root, "build", "--release", "release.yaml", "--auto-pull")
	})
	streams := "output:\n" + out + "\nstdout:\n" + stdout + "\nstderr:\n" + stderr
	// The first run: the fill's discovery render stopped on the refusal, and the
	// fill wrote past it and completed, so it handed the build no finished render.
	if !strings.Contains(stderr, "external inputs: could not finish rendering release.lock to discover them") ||
		!strings.Contains(stdout, "External inputs were not fully discovered") {
		t.Fatalf("the fill's discovery render did not stop, so the build may have presented the fill's render\n%s", streams)
	}
	if !strings.Contains(stdout, "Pull complete: release.lock") {
		t.Fatalf("the fill did not complete, so the build never made a second run\n%s", streams)
	}
	// The second run: the command fails on the same refusal after the fill
	// returned, which only the build's own render raises.
	if err == nil {
		t.Fatalf("the build rendered past the key the render refuses, so the fixture no longer makes a second run\n%s", streams)
	}
	// The refusal's text sits on a cause beneath the failing node's frame, so the
	// failure is read as the command prints it, whole.
	if failure := formatCompilerError(err); !strings.Contains(failure, "instance_idd") {
		t.Fatalf("the build stopped for a reason other than the render's refusal, so the second run is not proven:\n%s\n%s",
			failure, streams)
	}
	if n := strings.Count(out+"\n"+stdout+"\n"+stderr, schemaKeywordAdvisory+"\n"); n != 1 {
		t.Errorf("a build that fills its lockfile showed the advisory %d time(s); want exactly once\n%s", n, streams)
	}
}
