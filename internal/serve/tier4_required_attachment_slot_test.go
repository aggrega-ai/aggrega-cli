// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package serve

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
)

// TestRunTier4Validation_AttachmentSlotDiagnosticsReachTheEditor drives the
// daemon's aggrega/validate over a fixture whose two root components share one
// Component type declaring a required Black-Box slot, and pins that BOTH slot
// diagnostics reach the editor as positioned diagnostics.
//
// The two are asserted separately because they are different rules answered at
// different stages over different subjects: the requirement is asked of the
// node's attachment set after the cascade, the shape of the block the author
// wrote. An unbound slot must not read as a malformed fill, and a fill that
// over-reaches the slot's delivery allowlist must not read as an unbound slot.
//
// They are also asserted from two authoring variants rather than one document,
// because the shape violation halts at Step 1 and a document carrying it never
// reaches the Step-4 pass that reports the requirement.
//
// Positioning is asserted, not just presence: a diagnostic the editor cannot
// point at is not usable in the editor.
func TestRunTier4Validation_AttachmentSlotDiagnosticsReachTheEditor(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	fixtureDir := filepath.Join(cwd, "testdata", "tier4", "required-attachment-slot")

	for _, rel := range []string{
		"release.yaml", "manifest.yaml", "aggregate.yaml", ".aggrega-config.yaml",
		"catalog/components/slotted-db.yaml",
		"catalog/features/pw.yaml",
	} {
		data, err := os.ReadFile(filepath.Join(fixtureDir, rel))
		if err != nil {
			t.Fatalf("read fixture %s: %v", rel, err)
		}
		dst := filepath.Join(tmp, rel)
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			t.Fatalf("mkdir for %s: %v", rel, err)
		}
		if err := os.WriteFile(dst, data, 0o644); err != nil {
			t.Fatalf("write fixture %s: %v", rel, err)
		}
	}

	releasePath := filepath.Join(tmp, "release.yaml")
	release, err := resolver.LoadRelease(releasePath)
	if err != nil {
		t.Fatalf("load release: %v", err)
	}

	srv := newTestServer(t, tmp, nil, nil)
	srv.activeRelease = release
	srv.activeReleasePath = releasePath
	srv.cascadeAttempt = cascadeAttempt{
		AuthoringReleaseConfigured: true,
		ReleaseCandidatesFound:     1,
		ResolvedStep:               1,
	}
	srv.wsConfig.Workspace.CatalogDirs = []string{filepath.Join(tmp, "catalog")}
	srv.lazyCatalog = catalog.NewLazyLookup(tmp, "", []string{filepath.Join(tmp, "catalog")}, nil)

	aggPath := filepath.Join(tmp, "aggregate.yaml")
	aggBytes, err := os.ReadFile(aggPath)
	if err != nil {
		t.Fatalf("read aggregate: %v", err)
	}

	validateBuffer := func(content string) validateResult {
		t.Helper()
		var in, out bytes.Buffer
		in.Write(framedRequest(1, "aggrega/validate", validateParams{
			File:    aggPath,
			Content: content,
		}))
		resp := runOneRequest(t, srv, &in, &out)
		if resp.Error != nil {
			t.Fatalf("unexpected envelope error: %+v", resp.Error)
		}
		var result validateResult
		if err := json.Unmarshal(resp.Result, &result); err != nil {
			t.Fatalf("unmarshal result: %v", err)
		}
		if len(result.Diagnostics) == 0 {
			t.Fatal("expected diagnostics from the slot fixture; got none")
		}
		return result
	}

	result := validateBuffer(string(aggBytes))

	// The unmet slot requirement: found by the identity registered against the
	// rule rather than by a sentence, so the discriminator survives a rewording
	// of the diagnostic and fails when the rule stops reporting at all.
	unbound := findDiagnostic(t, result.Diagnostics, "required attachment slot unfilled")
	// Both coordinates a reader needs are in the text, because the editor
	// renders the message and does not render the locator. Naming only the
	// component type would leave two unbound slots on two instances of one type
	// reading as the same sentence.
	for _, want := range []string{`"root_password"`, `"example/slotted-db"`} {
		if !containsSubstring(unbound.Message, want) {
			t.Errorf("requirement diagnostic must name %s; got %q", want, unbound.Message)
		}
	}
	if containsSubstring(unbound.Message, "conform") {
		t.Errorf("requirement diagnostic must not read as a shape violation: %q", unbound.Message)
	}
	if unbound.Severity != "error" {
		t.Errorf("requirement diagnostic severity = %q; want error", unbound.Severity)
	}

	// Positioning: the requirement lands on the component instance that leaves
	// the slot unbound, not at file:1:1.
	//
	// The locator the finding carries names the slot under that component's
	// attachment block, and the component leaves the slot unbound precisely by
	// carrying no such key — so the document walk stops at the deepest key it
	// does reach, which is the component. That is what this line asserts, and it
	// is the same position the reading this replaced named directly. A component
	// authoring an attachment block that merely lacks this slot would position
	// one level deeper.
	unboundLine := fixtureKeyLine(t, string(aggBytes), "unbound:")
	if unbound.Line != unboundLine {
		t.Errorf("requirement diagnostic at line %d; want the `unbound:` key at line %d (diagnostics: %v)",
			unbound.Line, unboundLine, diagMessages(result.Diagnostics))
	}

	// The shape violation on the Black-Box slot, from the second variant: a
	// distinct diagnostic that does NOT read as an unmet requirement, positioned
	// on the offending key inside the fill.
	shapeBytes, err := os.ReadFile(filepath.Join(fixtureDir, "aggregate-shape.yaml"))
	if err != nil {
		t.Fatalf("read shape variant: %v", err)
	}
	// The semantic pass reads the Aggregate from disk (the request's content is
	// used for position recovery), so the variant is written to the Aggregate's
	// path rather than passed as buffer content.
	if err := os.WriteFile(aggPath, shapeBytes, 0o644); err != nil {
		t.Fatalf("write shape variant: %v", err)
	}
	shapeResult := validateBuffer(string(shapeBytes))
	shape := findDiagnostic(t, shapeResult.Diagnostics, "does not conform to its declared slot shape")
	if containsSubstring(shape.Message, "required attachment slot unfilled") {
		t.Errorf("shape diagnostic must not read as an unmet requirement: %q", shape.Message)
	}
	if shape.Severity != "error" {
		t.Errorf("shape diagnostic severity = %q; want error", shape.Severity)
	}
	// Positioning: the shape violation lands on the attachment the offending fill
	// was written under, inside the component that wrote it — NOT on the
	// same-named attachment of the sibling component above it, and not at the
	// file head.
	//
	// The sibling is what makes this assertion worth making: both components
	// declare an attachment called `root_password`, so a locator that named only
	// the attachment, or one the document walker could not follow past its first
	// segment, would land on the wrong one and still look plausible.
	boundLine := fixtureKeyLine(t, string(shapeBytes), "bound:")
	if shape.Line <= boundLine {
		t.Errorf("shape diagnostic at line %d; want a line inside the `bound:` component, which "+
			"starts at line %d — the sibling component declares an attachment of the same name "+
			"(diagnostics: %v)", shape.Line, boundLine, diagMessages(shapeResult.Diagnostics))
	}
	if unbound.Line == 1 || shape.Line == 1 {
		t.Error("a slot diagnostic positioned at file:1:1 is not usable in the editor")
	}
}
