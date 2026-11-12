// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

// Package serve — integration proof that an inconsistent Manifest mapping is
// published by the daemon WITHOUT displacing the rest of a file's diagnostics.

package serve

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
)

// TestRunTier4Validation_InconsistentMappingPublishesBesideOtherDiagnostics is
// the daemon's half of the carried-findings design, and the assertion that makes
// it non-negotiable.
//
// The fixture carries two independent faults: a Manifest mapping onto a recipe
// that does not implement the mapped type, and a Release parameter that violates
// the Aggregate's declared contract. The mapped type is used by no component, so
// neither fault can be the cause of the other.
//
// Both must be published, each on its own document: the mapping finding on the
// Manifest it is written in, the parameter diagnostic on the Aggregate. The mapping
// verdict reaches the daemon as a finding on the resolved closure, so the
// resolution succeeds and the pipeline still runs. If that verdict were instead
// returned as a resolution failure, the daemon's closure-level fall-back would emit
// ONE diagnostic at line 1 of the Aggregate and return — discarding the parameter
// diagnostic and everything else it had to say about the buffer, on every
// keystroke.
//
// The Aggregate assertion is therefore the load-bearing one: a test checking only
// that the mapping diagnostic appears would pass under exactly that regression.
func TestRunTier4Validation_InconsistentMappingPublishesBesideOtherDiagnostics(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	fixtureDir := filepath.Join(cwd, "testdata", "tier4", "manifest-mapping-inconsistent")

	for _, name := range []string{"release.yaml", "manifest.yaml", "aggregate.yaml", ".aggrega-config.yaml"} {
		data, err := os.ReadFile(filepath.Join(fixtureDir, name))
		if err != nil {
			t.Fatalf("read fixture %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(tmp, name), data, 0o644); err != nil {
			t.Fatalf("write fixture %s: %v", name, err)
		}
	}
	// The mapping table drives source descent, so the mapped type must exist as a
	// Component even though no component instance uses it — otherwise the closure
	// walk fails on an unresolvable URI and the resolution never reaches the gate.
	for _, e := range []struct{ subdir, name string }{
		{"recipes", "elsewhere.yaml"},
		{"components", "unused-server.yaml"},
	} {
		dstDir := filepath.Join(tmp, "catalog", e.subdir)
		if err := os.MkdirAll(dstDir, 0o755); err != nil {
			t.Fatalf("mkdir catalog/%s: %v", e.subdir, err)
		}
		data, err := os.ReadFile(filepath.Join(fixtureDir, "catalog", e.subdir, e.name))
		if err != nil {
			t.Fatalf("read catalog/%s/%s: %v", e.subdir, e.name, err)
		}
		if err := os.WriteFile(filepath.Join(dstDir, e.name), data, 0o644); err != nil {
			t.Fatalf("write catalog/%s/%s: %v", e.subdir, e.name, err)
		}
	}

	srv := newTestServer(t, tmp, nil, nil)
	srv.wsConfig.Workspace.CatalogDirs = []string{filepath.Join(tmp, "catalog")}
	// The lazy catalog captures its catalog dirs at construction; newTestServer
	// built it with nil dirs, so rewire it now that the fixture's dir is known.
	srv.lazyCatalog = catalog.NewLazyLookup(tmp, "", []string{filepath.Join(tmp, "catalog")}, nil)

	// No separate enumeration calibration is needed here, and the reason is worth
	// stating so nobody adds a broken one. A mapping onto a recipe the gate cannot
	// resolve is SKIPPED rather than judged — so a fixture recipe that failed to
	// enumerate would produce NO mapping finding, which reds the Manifest assertion
	// below rather than letting it pass on nothing. The positive assertion is its
	// own calibration; it is the negative direction that would have needed one.

	validate := func(name string) []Diagnostic {
		t.Helper()
		path := filepath.Join(tmp, name)
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		var in, out bytes.Buffer
		in.Write(framedRequest(1, "aggrega/validate", validateParams{
			File:    path,
			Content: string(body),
			Release: "release.yaml",
		}))
		resp := runOneRequest(t, srv, &in, &out)
		if resp.Error != nil {
			t.Fatalf("validate %s: unexpected envelope error: %+v", name, resp.Error)
		}
		var result validateResult
		if err := json.Unmarshal(resp.Result, &result); err != nil {
			t.Fatalf("unmarshal the result for %s: %v", name, err)
		}
		return result.Diagnostics
	}

	aggDiags := validate("aggregate.yaml")
	var sawParameter bool
	for _, d := range aggDiags {
		// The parameter-contract violation is recovered to the `port` key in the
		// Aggregate (line 8), so matching on the position as well as the text keeps
		// this from being satisfied by a closure-level fall-back at line 1 that
		// happened to mention the word.
		if d.Line == 8 && strings.Contains(d.Message, "port") {
			sawParameter = true
		}
		if strings.Contains(d.Message, "spec.implements") {
			t.Errorf("the mapping finding is the Manifest's to carry, yet the Aggregate carries it: %q", d.Message)
		}
	}
	if !sawParameter {
		t.Errorf("the daemon must still publish its OTHER diagnostics for the Aggregate: "+
			"expected the parameter-contract violation positioned at the aggregate's port key (line 8), got %q",
			diagMessages(aggDiags))
	}

	manifestDiags := validate("manifest.yaml")
	var sawMapping bool
	for _, d := range manifestDiags {
		if strings.Contains(d.Message, "spec.implements") {
			sawMapping = true
		}
	}
	if !sawMapping {
		t.Errorf("the daemon must publish the Manifest mapping finding on the Manifest; got %q",
			diagMessages(manifestDiags))
	}
}
