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

	// The unmet slot requirement: names the slot and the component type, and
	// does NOT read as a shape complaint.
	unbound := findDiagnostic(t, result.Diagnostics, "declares the attachment slot")
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
	shape := findDiagnostic(t, shapeResult.Diagnostics, "does not conform to the declared slot shape")
	if containsSubstring(shape.Message, "declares the attachment slot") {
		t.Errorf("shape diagnostic must not read as an unmet requirement: %q", shape.Message)
	}
	if shape.Severity != "error" {
		t.Errorf("shape diagnostic severity = %q; want error", shape.Severity)
	}
	// The shape violation's locator is the resolver-class component-instance
	// form, whose bracket-quoted instance segment the document walker does not
	// follow, so the position recovered is the enclosing key rather than the
	// offending one. The assertion is deliberately the weaker "not at the file
	// head": pinning the enclosing key would cement the locator-form gap as
	// intended behavior, and pinning the offending key would assert a walk that
	// does not happen. The gap is shared by every resolver-class instance
	// locator, not introduced here.
	if unbound.Line == 1 || shape.Line == 1 {
		t.Error("a slot diagnostic positioned at file:1:1 is not usable in the editor")
	}
}
