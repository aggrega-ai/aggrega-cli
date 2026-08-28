// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

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
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
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
// Both must be published. The mapping verdict reaches the daemon as a finding on
// the resolved closure, so the resolution succeeds and the pipeline still runs. If
// that verdict were instead returned as a resolution failure, the daemon's
// closure-level fall-back would emit ONE diagnostic at line 1 and return —
// discarding the parameter diagnostic and everything else it had to say about the
// buffer, on every keystroke.
//
// The second assertion is therefore the load-bearing one: a test checking only
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

	releasePath := filepath.Join(tmp, "release.yaml")
	rel, err := resolver.LoadRelease(releasePath)
	if err != nil {
		t.Fatalf("load release: %v", err)
	}

	srv := newTestServer(t, tmp, nil, nil)
	srv.activeRelease = rel
	srv.activeReleasePath = releasePath
	srv.cascadeAttempt = cascadeAttempt{
		AuthoringReleaseConfigured: true,
		ReleaseCandidatesFound:     1,
		ResolvedStep:               1,
	}
	srv.wsConfig.Workspace.CatalogDirs = []string{filepath.Join(tmp, "catalog")}
	// The lazy catalog captures its catalog dirs at construction; newTestServer
	// built it with nil dirs, so rewire it now that the fixture's dir is known.
	srv.lazyCatalog = catalog.NewLazyLookup(tmp, "", []string{filepath.Join(tmp, "catalog")}, nil)

	// No separate enumeration calibration is needed here, and the reason is worth
	// stating so nobody adds a broken one. A mapping onto a recipe the gate cannot
	// resolve is SKIPPED rather than judged — so a fixture recipe that failed to
	// enumerate would produce NO mapping finding, which reds the first assertion
	// below rather than letting it pass on nothing. The positive assertion is its
	// own calibration; it is the negative direction that would have needed one.

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

	var sawMapping bool
	var sawParameter bool
	for _, d := range result.Diagnostics {
		if strings.Contains(d.Message, "spec.implements") {
			sawMapping = true
		}
		// The parameter-contract violation is recovered to the `port` key in the
		// Aggregate (line 8), so matching on the position as well as the text keeps
		// this from being satisfied by a closure-level fall-back at line 1 that
		// happened to mention the word.
		if d.Line == 8 && strings.Contains(d.Message, "port") {
			sawParameter = true
		}
	}
	if !sawMapping {
		t.Errorf("the daemon must publish the Manifest mapping finding; got %q",
			diagMessages(result.Diagnostics))
	}
	if !sawParameter {
		t.Errorf("the daemon must still publish its OTHER diagnostics for the file: "+
			"expected the parameter-contract violation positioned at the aggregate's port key (line 8), got %q",
			diagMessages(result.Diagnostics))
	}
	if len(result.Diagnostics) < 2 {
		t.Errorf("expected the mapping finding AND the parameter diagnostic; got %d: %q",
			len(result.Diagnostics), diagMessages(result.Diagnostics))
	}
}
