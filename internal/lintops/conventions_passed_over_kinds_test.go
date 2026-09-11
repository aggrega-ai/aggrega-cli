// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package lintops

import (
	"bytes"
	"io"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/entitykind"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// catalogWideOutput runs the whole-catalog conventions pass over a constructed
// workspace and returns what it wrote to the summary stream. Findings go to the
// diagnostic stream and are discarded: this file is about what the pass says it
// covered, not about what it found.
func catalogWideOutput(t *testing.T, ws string) string {
	t.Helper()
	rw, err := workspaceconfig.Resolve(ws, "")
	if err != nil {
		t.Fatalf("resolve the constructed workspace: %v", err)
	}
	var out bytes.Buffer
	if err := RunCatalogWide(t.Context(), rw, []string{ws}, &out, io.Discard, nil); err != nil {
		t.Fatalf("whole-catalog conventions pass: %v", err)
	}
	return out.String()
}

// seedWorkspace writes a workspace marker and returns the directory.
func seedWorkspace(t *testing.T) string {
	t.Helper()
	ws := t.TempDir()
	mustWriteFile(t, filepath.Join(ws, workspaceconfig.ConfigFilename), "workspace: {}\n")
	return ws
}

const passedOverProbeComponent = `apiVersion: aggrega.ai/v1alpha1
kind: Component
metadata:
  name: core/server
  version: "1.0.0"
  labels:
    component.aggrega.ai/type: server
spec:
  schema:
    values:
      port: { type: integer, default: 8080 }
`

const passedOverProbeAggregate = `apiVersion: aggrega.ai/v1alpha1
kind: Aggregate
metadata:
  name: probe/app
  version: "1.0.0"
spec:
  components: {}
`

// TestCatalogWideNamesTheKindsItPassedOver drives the real whole-catalog pass and
// holds it to naming the recognised entity kinds it did not check.
//
// The pass reports a count of the entities it checked. That count is the only
// thing it says about coverage, so on a tree holding a kind the walk declines,
// the count silently describes a smaller corpus than the tree — a file that
// reached no rule reads exactly like a file that was checked and found clean.
// The assertion is on the invocation, not on the emitter: a summary line that
// stops being written reaches this test even though the function that formats it
// still passes its own cases.
func TestCatalogWideNamesTheKindsItPassedOver(t *testing.T) {
	ws := seedWorkspace(t)
	mustWriteFile(t, filepath.Join(ws, "server.yaml"), passedOverProbeComponent)
	mustWriteFile(t, filepath.Join(ws, "app.yaml"), passedOverProbeAggregate)

	got := catalogWideOutput(t, ws)

	// Non-vacuity: the pass must have run and reported on the Component, or the
	// assertion below would be reading the output of a pass that did nothing.
	if !strings.Contains(got, "conventions checked: 1 entity,") {
		t.Fatalf("the pass did not report checking exactly the one Component; output was:\n%s", got)
	}
	if !strings.Contains(got, "not checked: 1 Aggregate") {
		t.Errorf("the pass checked 1 of the 2 entity documents in the tree and did not say which kind it passed over; a reader cannot tell the Aggregate from a file that was checked and found clean. Output was:\n%s", got)
	}
}

// TestCatalogWideSaysNothingWhenItPassedOverNothing is the companion that keeps
// the assertion above from being satisfied by a line that always prints. A tree
// of catalog entities alone has no coverage gap to report, and a permanent line
// saying so would train a reader to ignore it.
func TestCatalogWideSaysNothingWhenItPassedOverNothing(t *testing.T) {
	ws := seedWorkspace(t)
	mustWriteFile(t, filepath.Join(ws, "server.yaml"), passedOverProbeComponent)

	got := catalogWideOutput(t, ws)

	if !strings.Contains(got, "conventions checked: 1 entity,") {
		t.Fatalf("the pass did not report checking the one Component; output was:\n%s", got)
	}
	if strings.Contains(got, "not checked:") {
		t.Errorf("the pass named a coverage gap over a tree of catalog entities alone; output was:\n%s", got)
	}
}

// TestWalkCountsOnlyRecognisedKindsAsPassedOver holds the walk's tally to the one
// class it is meant to count. A catalog tree legitimately carries YAML that is
// not an Aggrega document, and those files name kinds of their own or none at
// all; counting them would report the tree's ordinary contents as a gap in the
// conventions pass, which is the failure mode that makes such a line worth
// ignoring.
func TestWalkCountsOnlyRecognisedKindsAsPassedOver(t *testing.T) {
	ws := t.TempDir()
	mustWriteFile(t, filepath.Join(ws, "server.yaml"), passedOverProbeComponent)
	mustWriteFile(t, filepath.Join(ws, "app.yaml"), passedOverProbeAggregate)
	mustWriteFile(t, filepath.Join(ws, "release.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Release
metadata:
  name: probe/prod
  version: "1.0.0"
spec: {}
`)
	// A kind that is not an entity kind at all, and a document with no kind.
	mustWriteFile(t, filepath.Join(ws, "deployment.yaml"), "apiVersion: apps/v1\nkind: Deployment\nmetadata:\n  name: web\n")
	mustWriteFile(t, filepath.Join(ws, "values.yaml"), "replicaCount: 2\nimage:\n  tag: latest\n")

	files, passedOver, err := walkCatalogEntityFiles(t.Context(), ws, []string{ws}, "")
	if err != nil {
		t.Fatalf("walkCatalogEntityFiles: %v", err)
	}
	if len(files) != 1 {
		t.Fatalf("the walk returned %d entity files, want the one Component; the tally below would be measured against the wrong corpus", len(files))
	}
	want := map[string]int{
		string(entitykind.Aggregate): 1,
		string(entitykind.Release):   1,
	}
	if len(passedOver) != len(want) {
		t.Errorf("the walk counted %v as passed over, want %v: a kind that is not an entity kind, or a document carrying no kind, is ordinary catalog-tree content rather than a coverage gap", passedOver, want)
	}
	for k, n := range want {
		if passedOver[k] != n {
			t.Errorf("the walk counted %d %s as passed over, want %d", passedOver[k], k, n)
		}
	}
}

// TestPassedOverKindsAreNamedInVocabularyOrder holds the line's kind order to the
// declared entity-kind vocabulary rather than to map iteration, so two runs over
// one tree write identical bytes.
func TestPassedOverKindsAreNamedInVocabularyOrder(t *testing.T) {
	tally := map[string]int{
		string(entitykind.Release):   2,
		string(entitykind.Aggregate): 1,
		string(entitykind.Manifest):  1,
	}
	var first bytes.Buffer
	writePassedOverKinds(&first, tally)

	// The vocabulary declares Aggregate before Manifest before Release, so that is
	// the order the line names them in whatever order the tally was built.
	if !strings.Contains(first.String(), "not checked: 1 Aggregate, 1 Manifest, 2 Releases —") {
		t.Errorf("the line did not name the kinds in the declared vocabulary order, or did not pluralise a repeated kind; got:\n%s", first.String())
	}

	for i := 0; i < 8; i++ {
		var again bytes.Buffer
		writePassedOverKinds(&again, tally)
		if again.String() != first.String() {
			t.Fatalf("two runs over one tally wrote different bytes:\n%q\n%q", first.String(), again.String())
		}
	}

	var empty bytes.Buffer
	writePassedOverKinds(&empty, map[string]int{})
	if empty.Len() != 0 {
		t.Errorf("an empty tally wrote %q, want nothing", empty.String())
	}
}
