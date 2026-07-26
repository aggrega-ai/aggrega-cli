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

// TestRunTier4Validation_RequiredAttachmentTypeDiagnosticsReachTheEditor drives
// the daemon's aggrega/validate over a fixture whose two root components each
// pull in a Component that declares a presence mandate — one unmet, one
// malformed — and pins that BOTH reach the editor as positioned diagnostics.
//
// The two classes are asserted separately on purpose. A malformed declaration
// must not be reported as an unmet requirement, and an unmet requirement must
// not be reported as a malformed declaration: they have different remediations,
// and collapsing them would let a typo in the catalog read as a deployment that
// simply forgot an attachment.
//
// Positioning is asserted, not just presence. Each diagnostic must land on the
// component-instance key that pulled the declaring type in — not at the head of
// the file — because a diagnostic the editor cannot point at is not usable in
// the editor.
func TestRunTier4Validation_RequiredAttachmentTypeDiagnosticsReachTheEditor(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	fixtureDir := filepath.Join(cwd, "testdata", "tier4", "required-attachment-type")

	for _, rel := range []string{
		"release.yaml", "manifest.yaml", "aggregate.yaml", ".aggrega-config.yaml",
		"catalog/components/needs-exporter.yaml",
		"catalog/components/broken-requirement.yaml",
		"catalog/features/exporter.yaml",
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

	var in, out bytes.Buffer
	in.Write(framedRequest(1, "aggrega/validate", validateParams{
		File:    aggPath,
		Content: string(aggBytes),
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
		t.Fatal("expected diagnostics from the mandate fixture; got none")
	}

	// The unmet mandate: names the requirement's label and the required type,
	// and does NOT read as a malformed declaration.
	unmet := findDiagnostic(t, result.Diagnostics, "requires an attachment of type")
	for _, want := range []string{`"example/exporter"`, `"monitoring"`, "unmonitored"} {
		if !containsSubstring(unmet.Message, want) {
			t.Errorf("unmet diagnostic must name %s; got %q", want, unmet.Message)
		}
	}
	if containsSubstring(unmet.Message, "malformed") {
		t.Errorf("unmet diagnostic must not read as malformed: %q", unmet.Message)
	}
	if unmet.Severity != "error" {
		t.Errorf("unmet diagnostic severity = %q; want error", unmet.Severity)
	}

	// The malformed declaration: a distinct diagnostic that does NOT read as an
	// unsatisfied requirement.
	malformed := findDiagnostic(t, result.Diagnostics, "malformed attachment requirement in annotation")
	if containsSubstring(malformed.Message, "requires an attachment of type") {
		t.Errorf("malformed diagnostic must not read as an unmet requirement: %q", malformed.Message)
	}
	if !containsSubstring(malformed.Message, "broken") {
		t.Errorf("malformed diagnostic must name the offending instance: %q", malformed.Message)
	}

	// Positioning: each diagnostic lands on its component-instance key rather
	// than at file:1:1.
	unmetLine := fixtureKeyLine(t, string(aggBytes), "unmonitored:")
	if unmet.Line != unmetLine {
		t.Errorf("unmet diagnostic at line %d; want the `unmonitored:` key at line %d (diagnostics: %v)",
			unmet.Line, unmetLine, diagMessages(result.Diagnostics))
	}
	brokenLine := fixtureKeyLine(t, string(aggBytes), "broken:")
	if malformed.Line != brokenLine {
		t.Errorf("malformed diagnostic at line %d; want the `broken:` key at line %d (diagnostics: %v)",
			malformed.Line, brokenLine, diagMessages(result.Diagnostics))
	}
	if unmet.Line == 1 || malformed.Line == 1 {
		t.Error("a mandate diagnostic positioned at file:1:1 is not usable in the editor")
	}
}

// findDiagnostic returns the single diagnostic whose message contains needle,
// failing when none or more than one matches — an ambiguous match would let the
// assertions above pass against the wrong entry.
func findDiagnostic(t *testing.T, diags []Diagnostic, needle string) Diagnostic {
	t.Helper()
	var found []Diagnostic
	for _, d := range diags {
		if containsSubstring(d.Message, needle) {
			found = append(found, d)
		}
	}
	if len(found) != 1 {
		t.Fatalf("want exactly 1 diagnostic containing %q, got %d; all: %v",
			needle, len(found), diagMessages(diags))
	}
	return found[0]
}

// fixtureKeyLine returns the 1-based line the supplied key appears on, so the
// position assertions read against the fixture rather than a hard-coded number
// that silently rots when the fixture is edited.
func fixtureKeyLine(t *testing.T, content, key string) int {
	t.Helper()
	line := 1
	start := 0
	for i := 0; i <= len(content); i++ {
		if i == len(content) || content[i] == '\n' {
			if containsSubstring(content[start:i], key) {
				return line
			}
			line++
			start = i + 1
		}
	}
	t.Fatalf("key %q not found in fixture", key)
	return 0
}
