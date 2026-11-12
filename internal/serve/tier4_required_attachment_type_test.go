// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package serve

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
)

// TestRunTier4Validation_RequiredAttachmentTypeDiagnosticsReachTheEditor drives
// the daemon's aggrega/validate over the presence-mandate fixture and pins that
// each of the two classes reaches the editor as a positioned diagnostic.
//
// The two classes are asserted separately on purpose. A malformed declaration
// must not be reported as an unmet requirement, and an unmet requirement must
// not be reported as a malformed declaration: they have different remediations,
// and collapsing them would let a typo in the catalog read as an Aggregate that
// simply forgot an attachment.
//
// 🔴 They are asserted over SEPARATE documents, and that is a property of the
// product rather than of the test. A malformed declaration is reported as soon as
// the composition is assembled, which is earlier than the point the unmet
// requirement is read at, and the earlier report halts — so a document carrying
// both reports only the malformed one. Asserting both against one document would
// therefore assert something the product does not do. Each leg declares the one
// component whose class it is about.
//
// Positioning is asserted, not just presence. Each diagnostic must land on the
// component-instance key that pulled the declaring type in — not at the head of
// the file — because a diagnostic the editor cannot point at is not usable in
// the editor.
func TestRunTier4Validation_RequiredAttachmentTypeDiagnosticsReachTheEditor(t *testing.T) {
	t.Parallel()

	// The instance each leg declares, and nothing else, so the class under
	// assertion is the only one the document can produce.
	const unmetOnly = `apiVersion: aggrega.ai/v1beta1
kind: Aggregate
metadata:
  name: tier4-required-attachment-type
  version: "1.0.0"
spec:
  components:
    unmonitored:
      type: example/needs-exporter
`
	const malformedOnly = `apiVersion: aggrega.ai/v1beta1
kind: Aggregate
metadata:
  name: tier4-required-attachment-type
  version: "1.0.0"
spec:
  components:
    broken:
      type: example/broken-requirement
`

	t.Run("an unmet mandate reaches the editor positioned on its instance", func(t *testing.T) {
		t.Parallel()
		diags, aggBytes := validateMandateFixture(t, unmetOnly, "unmonitored")

		// The registered wording of the sentinel the rule carries, not a
		// composed sentence: a discriminator on the sentinel's own text survives
		// a rewording of the values beside it.
		unmet := findDiagnostic(t, diags, "missing required feature")
		for _, want := range []string{`"example/needs-exporter"`, `"example/exporter"`, `"monitoring"`} {
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
		wantLine := fixtureKeyLine(t, aggBytes, "unmonitored:")
		if unmet.Line != wantLine {
			t.Errorf("unmet diagnostic at line %d; want the `unmonitored:` key at line %d (diagnostics: %v)",
				unmet.Line, wantLine, diagMessages(diags))
		}
		if unmet.Line == 1 {
			t.Error("a mandate diagnostic positioned at file:1:1 is not usable in the editor")
		}
	})

	t.Run("a malformed declaration reaches the editor positioned on its instance", func(t *testing.T) {
		t.Parallel()
		diags, aggBytes := validateMandateFixture(t, malformedOnly, "broken")

		malformed := findDiagnostic(t, diags, "malformed attachment requirement")
		if containsSubstring(malformed.Message, "missing required feature") {
			t.Errorf("malformed diagnostic must not read as an unmet requirement: %q", malformed.Message)
		}
		// It names the catalog entity to correct and the annotation key, which is
		// what the remedy needs; the offending instance is the POSITION rather
		// than a word in the text.
		for _, want := range []string{`"example/broken-requirement"`, "features.aggrega.ai/requiredType.monitoring"} {
			if !containsSubstring(malformed.Message, want) {
				t.Errorf("malformed diagnostic must name %s; got %q", want, malformed.Message)
			}
		}
		if malformed.Severity != "error" {
			t.Errorf("malformed diagnostic severity = %q; want error", malformed.Severity)
		}
		wantLine := fixtureKeyLine(t, aggBytes, "broken:")
		if malformed.Line != wantLine {
			t.Errorf("malformed diagnostic at line %d; want the `broken:` key at line %d (diagnostics: %v)",
				malformed.Line, wantLine, diagMessages(diags))
		}
		if malformed.Line == 1 {
			t.Error("a mandate diagnostic positioned at file:1:1 is not usable in the editor")
		}
	})
}

// mandateRelease is the release each leg is validated through, with the one
// component that leg's aggregate declares substituted into its boundary. It is
// written per leg because a single release naming both components would, for
// either leg, name one the aggregate does not declare — see
// validateMandateFixture for why that is fatal here.
const mandateRelease = `apiVersion: aggrega.ai/v1beta1
kind: Release
metadata:
  name: tier4-required-attachment-type-release
  environment: test
  version: "1.0.0"
spec:
  aggregate: ./aggregate.yaml
  manifest: ./manifest.yaml
  boundaries:
    main:
      directory: ./dist/main
      components:
        %s: {}
`

// validateMandateFixture copies the presence-mandate fixture into a temporary
// workspace, replaces its aggregate with the supplied document and its release
// with one placing the named component, and drives the daemon's validate over it.
//
// The catalog comes from the committed fixture so the entities under test are the
// real ones. The aggregate is substituted because which class a document can
// produce is decided by which component it declares, and the release is
// substituted in step with it because the two have to agree: a boundary entry
// naming a component the aggregate does not declare places nothing, and that is
// reported while the composition is assembled — earlier than any attachment
// diagnostic, and it halts, so the class under assertion would never be reached.
func validateMandateFixture(t *testing.T, aggregate, component string) ([]Diagnostic, string) {
	t.Helper()
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
	if err := os.WriteFile(filepath.Join(tmp, "aggregate.yaml"), []byte(aggregate), 0o644); err != nil {
		t.Fatalf("write aggregate: %v", err)
	}
	releaseDoc := fmt.Sprintf(mandateRelease, component)
	if err := os.WriteFile(filepath.Join(tmp, "release.yaml"), []byte(releaseDoc), 0o644); err != nil {
		t.Fatalf("write release: %v", err)
	}

	srv := newTestServer(t, tmp, nil, nil)
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
		Release: "release.yaml",
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
	return result.Diagnostics, string(aggBytes)
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
