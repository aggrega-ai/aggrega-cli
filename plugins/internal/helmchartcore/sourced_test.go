// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package helmchartcore

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestReadChartMeta_MultiDocumentHalts pins the loud guardrail: a Chart.yaml
// carrying `---`-separated documents must not be silently parsed as only its
// first document. readChartMeta halts with a CatalogError naming the
// multiple-documents condition. Mirrors compose/sourced.go's equivalent.
func TestReadChartMeta_MultiDocumentHalts(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	body := []byte(strings.TrimLeft(`
apiVersion: v2
name: api
version: 1.0.0
---
apiVersion: v2
name: sneaky
version: 9.9.9
`, "\n"))
	if err := os.WriteFile(filepath.Join(dir, "Chart.yaml"), body, 0o644); err != nil {
		t.Fatalf("write Chart.yaml: %v", err)
	}
	_, _, err := readChartMeta(dir)
	if err == nil {
		t.Fatal("expected a loud error for a multi-document Chart.yaml, got nil (silent first-doc drop)")
	}
	if !strings.Contains(err.Error(), "multiple YAML documents") {
		t.Errorf("expected the multiple-documents catalog error, got: %v", err)
	}
}

// TestReadChartMeta_SingleDocumentResolves is the non-vacuity counterpart: a
// single-document Chart.yaml resolves name/version exactly as before.
func TestReadChartMeta_SingleDocumentResolves(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	body := []byte(strings.TrimLeft(`
apiVersion: v2
name: api
version: 1.0.0
`, "\n"))
	if err := os.WriteFile(filepath.Join(dir, "Chart.yaml"), body, 0o644); err != nil {
		t.Fatalf("write Chart.yaml: %v", err)
	}
	name, version, err := readChartMeta(dir)
	if err != nil {
		t.Fatalf("single-document Chart.yaml must resolve, got: %v", err)
	}
	if name != "api" || version != "1.0.0" {
		t.Errorf("name/version = %q/%q; want api/1.0.0", name, version)
	}
}
