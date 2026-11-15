// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package main

// The drafting command's own two promises, driven at the real entrypoint: that
// it writes nothing, and that what it hands back is a document this product
// accepts once the author has filled in the values it left for them.
//
// Both are asserted against a spawned binary rather than against the command
// object. A write from somewhere other than the command body — a cache file, a
// stray output directory, a configuration file seeded on the way past — appears
// nowhere else, and the acceptance of a drafted document is a property of the
// whole validation path rather than of the drafting walk.

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// draftedComponentPlaceholders are the values a drafted Component leaves for its
// author, each paired with a value that satisfies the schema.
//
// They are the discriminator the validation test rests on. A drafted document is
// refused as it stands, and the interesting question is whether it is refused
// for its shape or only for the values nobody has supplied yet. Supplying
// exactly these — adding no field, moving none, reordering nothing — and getting
// an acceptance answers it: the shape was right and the placeholders were the
// whole of the objection.
//
// A schema that grows a required field breaks this test rather than silently
// weakening it, which is the failure worth having: the new field arrives as a
// placeholder with no entry here, so the filled document is still refused and
// the test says so.
var draftedComponentPlaceholders = []struct {
	emitted string
	filled  string
}{
	{`  version: ""`, `  version: "0.1.0"`},
	{`    component.aggrega.ai/type: ""`, `    component.aggrega.ai/type: "server"`},
}

// TestTheCommandDraftsWithoutTouchingTheWorkspace pins what the command's help
// promises: the answer is a document to write, and nothing was written to
// produce it. A redirect is how it reaches a file, and the caller drives that.
func TestTheCommandDraftsWithoutTouchingTheWorkspace(t *testing.T) {
	t.Parallel()
	bin := buildServeBinary(t)

	ws := t.TempDir()
	if err := os.WriteFile(filepath.Join(ws, "already-here.yaml"), []byte("kind: NotAggrega\n"), 0o644); err != nil {
		t.Fatalf("seed the workspace: %v", err)
	}
	before := treeSnapshot(t, ws)
	if len(before) == 0 {
		t.Fatal("the snapshot read nothing, so comparing it against itself would assert nothing")
	}

	for _, kind := range scaffoldParityKinds {
		cmd := exec.Command(bin, "draft", kind, scaffoldParityName)
		cmd.Dir = ws
		cmd.Env = builtBinaryEnv(t)
		var stdout, stderr strings.Builder
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr
		if err := cmd.Run(); err != nil {
			t.Fatalf("the command refused %q: %v (%s)", kind, err, stderr.String())
		}
		if strings.TrimSpace(stdout.String()) == "" {
			t.Fatalf("the command drafted nothing for %q, so the workspace comparison asserts nothing", kind)
		}
	}

	if diff := diffSnapshots(before, treeSnapshot(t, ws)); diff != "" {
		t.Fatalf("drafting changed the workspace:\n%s", diff)
	}
}

// TestADraftedDocumentIsRefusedOnlyForItsPlaceholders drives the drafted
// document back through this product's own validation, in both states.
//
// As drafted it is refused, which is correct and is the point of a placeholder:
// an empty required value is not a value. With those placeholders filled and
// nothing else touched it is accepted, which is what makes the refusal
// attributable — the document's shape came out of the schema right, and what it
// was missing was the author.
func TestADraftedDocumentIsRefusedOnlyForItsPlaceholders(t *testing.T) {
	t.Parallel()
	bin := buildServeBinary(t)
	ws := t.TempDir()

	cmd := exec.Command(bin, "draft", "component", "my-service")
	cmd.Dir = ws
	cmd.Env = builtBinaryEnv(t)
	var drafted, stderr strings.Builder
	cmd.Stdout = &drafted
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("the command refused to draft a Component: %v (%s)", err, stderr.String())
	}

	asDrafted := drafted.String()
	filled := asDrafted
	for _, p := range draftedComponentPlaceholders {
		if !strings.Contains(filled, p.emitted) {
			t.Fatalf("the drafted document carries no %q, so filling it in asserts nothing:\n%s",
				p.emitted, asDrafted)
		}
		filled = strings.Replace(filled, p.emitted, p.filled, 1)
	}

	draftedPath := filepath.Join(ws, "as-drafted.yaml")
	filledPath := filepath.Join(ws, "filled.yaml")
	if err := os.WriteFile(draftedPath, []byte(asDrafted), 0o644); err != nil {
		t.Fatalf("write the drafted document: %v", err)
	}
	if err := os.WriteFile(filledPath, []byte(filled), 0o644); err != nil {
		t.Fatalf("write the filled document: %v", err)
	}

	if _, err := validateEntity(t, bin, ws, draftedPath); err == nil {
		t.Error("the document was accepted with its placeholders unfilled, so an empty required " +
			"value reads to this product as a value")
	}

	if out, err := validateEntity(t, bin, ws, filledPath); err != nil {
		t.Errorf("filling the placeholders and changing nothing else left the document refused, so it "+
			"was refused for its shape rather than for its placeholders: %v\n%s", err, out)
	}
}

// validateEntity runs the validate command over one entity file and returns
// everything it emitted, so a failing assertion reports the product's own words
// rather than only an exit status.
func validateEntity(t *testing.T, bin, ws, path string) (string, error) {
	t.Helper()
	cmd := exec.Command(bin, "validate", path)
	cmd.Dir = ws
	cmd.Env = builtBinaryEnv(t)
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	return stdout.String() + stderr.String(), err
}
