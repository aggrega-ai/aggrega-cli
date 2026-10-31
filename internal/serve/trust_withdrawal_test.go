// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package serve

// A withdrawal marker on a plugin attached at a verdict role is recorded in the
// plugin trust record beside the Release, not in the lockfile, because such a
// plugin shapes no rendered byte and is not pinned there. So a daemon surface that
// reads the lockfile alone says nothing about a withdrawn gate plugin while it
// names a withdrawn pinned reference — and its silence reads as "nothing is
// withdrawn". These cases hold the daemon's validation and its closure answer to
// the one report over both stores.

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/pluginstrust"
)

const serveWithdrawnGateMessage = "withdrawn: unsound gate"

// recordWithdrawnServeGate rewrites the record beside releasePath so the fixture
// gate it names carries a retracted withdrawal marker, and returns the notice the
// record's own producer renders for it — the expectation is read from the store's
// producer rather than spelled here.
func recordWithdrawnServeGate(t *testing.T, releasePath string) string {
	t.Helper()
	record := &pluginstrust.Trust{
		Version: pluginstrust.CurrentVersion,
		Plugins: []pluginstrust.TrustedPlugin{{
			Plugin:          serveTrustGateCoordinate,
			ResolvedVersion: serveTrustGateVersion,
			Digest:          "sha256:1111111111111111111111111111111111111111111111111111111111111111",
			Deprecation:     &pluginstrust.Deprecation{Severity: "retracted", Message: serveWithdrawnGateMessage},
		}},
	}
	if err := pluginstrust.Write(pluginstrust.PathFor(releasePath), record); err != nil {
		t.Fatalf("write trust record: %v", err)
	}
	findings := record.DeprecationFindings()
	if len(findings) != 1 {
		t.Fatalf("the record's own producer reports %d withdrawals, so the expectation below would be vacuous", len(findings))
	}
	return findings[0].Notice()
}

// TestServeValidationReportsAWithdrawnTrustedPlugin drives the daemon's validation
// over a workspace whose lockfile records nothing and whose trust record carries a
// withdrawal on the attached gate. The notice must reach the editor as the
// closure-level advisory every withdrawal is published as, at the warning tier, and
// the validation must otherwise stay clean — the marker is reported, never turned
// into a verdict.
func TestServeValidationReportsAWithdrawnTrustedPlugin(t *testing.T) {
	srv, releasePath, aggPath, content := serveTrustServer(t, serveTrustGateURI)
	notice := recordWithdrawnServeGate(t, releasePath)
	doc := parseYAML(t, string(content))

	diags := srv.runTier4Validation(context.Background(), releasePath, aggPath, content, doc)

	want := closureAdvisoryDiagnostic(notice)
	var found bool
	for _, d := range diags {
		if d.Severity == editorSeverityError {
			t.Errorf("a recorded withdrawal is advisory and the fixture is otherwise clean; got an error diagnostic %+v", d)
		}
		if d.Message == want.Message && d.Severity == editorSeverityWarning {
			found = true
		}
	}
	if !found {
		t.Fatalf("the validation did not report the withdrawn gate plugin %q; got %+v", want.Message, diags)
	}
}

// TestServeReleaseClosureReportsAWithdrawnTrustedPlugin holds the closure answer's
// withdrawal projection to both stores: with no lockfile and a trust record carrying
// a withdrawal, the projection names the withdrawn plugin; with a record that is
// there and cannot be read, it refuses rather than answering as though nothing were
// withdrawn.
func TestServeReleaseClosureReportsAWithdrawnTrustedPlugin(t *testing.T) {
	dir := t.TempDir()
	releasePath := filepath.Join(dir, "release.yaml")
	notice := recordWithdrawnServeGate(t, releasePath)

	got, err := closureDeprecations(nil, releasePath)
	if err != nil {
		t.Fatalf("a readable record must not refuse: %v", err)
	}
	if len(got) != 1 || got[0] != notice {
		t.Fatalf("the closure answer must name the withdrawn gate plugin %q; got %#v", notice, got)
	}

	if err := os.WriteFile(pluginstrust.PathFor(releasePath), []byte("plugins: [\n"), 0o644); err != nil {
		t.Fatalf("write damaged record: %v", err)
	}
	if got, err := closureDeprecations(nil, releasePath); err == nil {
		t.Fatalf("a record that cannot be read must refuse rather than pass as empty; got %#v", got)
	}
}
