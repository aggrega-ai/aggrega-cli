// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package catalog

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-abi-go/pkg/abi"
)

// probedParentDoc authors an x-aggrega-* extension keyword inline in its schema
// content, the shape the discouragement names; probedChildDoc extends it and
// authors none of its own.
const probedParentDoc = `apiVersion: aggrega.ai/v1alpha1
kind: Component
metadata:
  name: test/probed
  version: "1.0.0"
  labels:
    component.aggrega.ai/type: server
spec:
  schema:
    values:
      database:
        type: object
        x-aggrega-probe: planted
`

const probedChildDoc = `apiVersion: aggrega.ai/v1alpha1
kind: Component
metadata:
  name: test/probed-child
  version: "1.0.0"
  labels:
    component.aggrega.ai/type: server
spec:
  extends: "test/probed"
  schema: inherited
`

// brokenChildDoc extends a parent no catalog holds.
const brokenChildDoc = `apiVersion: aggrega.ai/v1alpha1
kind: Component
metadata:
  name: test/orphan
  version: "1.0.0"
  labels:
    component.aggrega.ai/type: server
spec:
  extends: "test/missing-parent"
  schema: inherited
`

// writeCatalogDocs lays files down under a fresh workspace's catalog directory
// and returns the workspace root and the directory.
func writeCatalogDocs(t *testing.T, files map[string]string) (string, string) {
	t.Helper()
	ws := t.TempDir()
	dir := filepath.Join(ws, "catalog")
	for name, body := range files {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	return ws, dir
}

// TestSchemaAdvisoriesAreHeldOncePerEntityAsItsDocumentAuthoredThem holds the
// x-aggrega-* discouragement to what a run reports: one structured advisory per
// entity and keyword, however often the view reads the entity, naming the entity
// whose document authors the keyword — never a child that merely extends it —
// and written to no stream at all, the advisory reaching a reader only through
// the run that reads the view's advisories.
//
// The drive reads the child (which reads its parent through the chain) and the
// parent three times over, dropping the view's resolved entities between rounds
// so each round resolves both again.
//
// 🔴 PLANTED DEFECT: ask the check in the per-document parse again
// (resolveComponentDocument) and write its findings to the process stream, or
// drop the held-sentence test in noteSchemaAdvisories, or ask it of the merged
// entity rather than the resolved document — and this fails.
func TestSchemaAdvisoriesAreHeldOncePerEntityAsItsDocumentAuthoredThem(t *testing.T) {
	ws, dir := writeCatalogDocs(t, map[string]string{
		"probed.yaml":       probedParentDoc,
		"probed-child.yaml": probedChildDoc,
	})
	view := NewLazyLookup(ws, ".aggrega/cache", []string{dir}, nil)
	var notices bytes.Buffer
	view.WriteNoticesTo(&notices)

	stray := captureStderr(t, func() {
		for i := 0; i < 3; i++ {
			view.SetPins(Pins{})
			if _, ok := view.Component("test/probed-child:1.0.0"); !ok {
				t.Fatalf("round %d: the child did not resolve, so the drive reads nothing", i)
			}
			if _, ok := view.Component("test/probed:1.0.0"); !ok {
				t.Fatalf("round %d: the parent did not resolve", i)
			}
		}
	})

	const want = `entity "test/probed" authors x-aggrega-probe at </values/database/x-aggrega-probe> ` +
		`in schema content; recommended path is the appropriate declarative table on spec`
	got := view.SchemaAdvisories()
	if len(got) != 1 {
		t.Fatalf("three rounds of reads held %d advisories, want exactly one: %+v", len(got), got)
	}
	if got[0].Message != want {
		t.Errorf("advisory message = %q\nwant %q", got[0].Message, want)
	}
	if got[0].SourceRef != "test/probed" {
		t.Errorf("advisory names %q as its source, want the entity that authored the keyword", got[0].SourceRef)
	}
	if got[0].Severity != abi.SeverityWarn {
		t.Errorf("advisory severity = %q, want %q", got[0].Severity, abi.SeverityWarn)
	}
	if got[0].Location != nil || got[0].Code != "" {
		t.Errorf("advisory carries a location %+v or a code %q it has no source for", got[0].Location, got[0].Code)
	}
	for _, d := range got {
		if strings.Contains(d.Message, "test/probed-child") {
			t.Errorf("the child is named for a keyword only its parent authors: %q", d.Message)
		}
	}
	if stray != "" {
		t.Errorf("reading the entities wrote to the process's standard error:\n%s", stray)
	}
	if notices.Len() != 0 {
		t.Errorf("the advisory was written as a notice; it is the run's to report:\n%s", notices.String())
	}
}

// TestSchemaAdvisoriesAreEmptyForAnEntityAuthoringNoKeyword is the control that
// gives the assertion above its meaning: a catalog authoring no x-aggrega-*
// keyword holds no advisory.
func TestSchemaAdvisoriesAreEmptyForAnEntityAuthoringNoKeyword(t *testing.T) {
	ws, dir := writeCatalogDocs(t, map[string]string{"base.yaml": baseComponentDoc})
	view := NewLazyLookup(ws, ".aggrega/cache", []string{dir}, nil)
	if _, ok := view.Component("test/base:1.0.0"); !ok {
		t.Fatal("the control entity did not resolve, so its silence says nothing")
	}
	if got := view.SchemaAdvisories(); len(got) != 0 {
		t.Errorf("an entity authoring no keyword held advisories: %+v", got)
	}
}

// TestAnExtendsFailureIsWrittenToTheNoticeWriterAndNowhereElse holds the line a
// view writes for an entity whose parent does not resolve to the writer its
// surface handed it — in the line shape a terminal reader is shown — and to
// nothing else: not the process's standard error, and not the advisories a run
// reports, because a validation already reports the broken parent as a fault and
// a build's answer as a notice of its own class.
//
// 🔴 PLANTED DEFECT: write the extends line to the process stream again
// (componentContext / featureContext), or hold it among the schema advisories,
// and this fails.
func TestAnExtendsFailureIsWrittenToTheNoticeWriterAndNowhereElse(t *testing.T) {
	ws, dir := writeCatalogDocs(t, map[string]string{"orphan.yaml": brokenChildDoc})
	view := NewLazyLookup(ws, ".aggrega/cache", []string{dir}, nil)
	var notices bytes.Buffer
	view.WriteNoticesTo(&notices)

	stray := captureStderr(t, func() {
		_, _ = view.Component("test/orphan:1.0.0")
	})

	var extendsLines []string
	for _, line := range strings.Split(notices.String(), "\n") {
		if strings.HasPrefix(line, "  extends: ") {
			extendsLines = append(extendsLines, line)
		}
	}
	if len(extendsLines) != 1 {
		t.Fatalf("want one extends line on the notice writer, got %d:\n%s", len(extendsLines), notices.String())
	}
	if !strings.Contains(extendsLines[0], "test/missing-parent") {
		t.Errorf("the extends line does not name the parent that would not resolve: %q", extendsLines[0])
	}
	if stray != "" {
		t.Errorf("the extends failure was written to the process's standard error:\n%s", stray)
	}
	if got := view.SchemaAdvisories(); len(got) != 0 {
		t.Errorf("the extends failure reached the advisories a run reports: %+v", got)
	}
}
