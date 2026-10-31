// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package mcp

// A withdrawal marker on a plugin attached at a verdict role is recorded in the
// plugin trust record beside the Release, not in the lockfile, because such a
// plugin shapes no rendered byte and is not pinned there. So a tool that reads the
// lockfile alone says nothing about a withdrawn gate plugin while it names a
// withdrawn pinned reference — and its silence reads as "nothing is withdrawn".
// These cases hold the describe and validate tools to the one report over both
// stores.

import (
	"encoding/json"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/pluginstrust"
)

const mcpWithdrawnGateMessage = "withdrawn: unsound gate"

// recordWithdrawnMCPGate rewrites the record beside releasePath so the fixture gate
// it names carries a retracted withdrawal marker, and returns the notice the
// record's own producer renders for it — the expectation is read from the store's
// producer rather than spelled here.
func recordWithdrawnMCPGate(t *testing.T, releasePath string) string {
	t.Helper()
	record := &pluginstrust.Trust{
		Version: pluginstrust.CurrentVersion,
		Plugins: []pluginstrust.TrustedPlugin{{
			Plugin:          mcpTrustGateCoordinate,
			ResolvedVersion: mcpTrustGateVersion,
			Digest:          "sha256:1111111111111111111111111111111111111111111111111111111111111111",
			Deprecation:     &pluginstrust.Deprecation{Severity: "retracted", Message: mcpWithdrawnGateMessage},
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

// TestValidateToolReportsAWithdrawnTrustedPlugin drives the validate tool over a
// workspace with no lockfile whose trust record carries a withdrawal on the attached
// gate. The notice travels on the advisory channel and the verdict is untouched.
func TestValidateToolReportsAWithdrawnTrustedPlugin(t *testing.T) {
	s := newTestServer(t)
	root, dirs, releasePath := writeMCPTrustWorkspace(t, mcpTrustGateURI)
	notice := recordWithdrawnMCPGate(t, releasePath)

	res := callTool(t, s, "validate", validateArgs{
		Release:       releasePath,
		WorkspaceRoot: root,
		CatalogDirs:   dirs,
	})
	if res.IsError {
		t.Fatalf("release scope reported a protocol error rather than a verdict: %s", res.Content[0].Text)
	}
	payload := unmarshalValidatePayload(t, res)
	if !slices.Contains(payload.Notices, "deprecation: "+notice) {
		t.Fatalf("validate must name the withdrawn gate plugin among its notices; want %q, notices %#v",
			"deprecation: "+notice, payload.Notices)
	}
	if !payload.Valid {
		t.Errorf("a recorded withdrawal is advisory and the fixture is otherwise clean; diagnostics %#v", payload.Diagnostics)
	}
}

// TestDescribeToolReportsAWithdrawnTrustedPlugin drives the describe tool over the
// same workspace: the description still arrives, and its deprecations name the
// withdrawn gate plugin.
func TestDescribeToolReportsAWithdrawnTrustedPlugin(t *testing.T) {
	s := newTestServer(t)
	root, dirs, releasePath := writeMCPTrustWorkspace(t, mcpTrustGateURI)
	notice := recordWithdrawnMCPGate(t, releasePath)

	res := callTool(t, s, "describe", describeArgs{
		Release:       releasePath,
		WorkspaceRoot: root,
		CatalogDirs:   dirs,
	})
	if res.IsError {
		t.Fatalf("describe must not refuse: a report verb describes the Release and names the fault; got: %s",
			res.Content[0].Text)
	}
	var payload describeReleasePayload
	if err := json.Unmarshal(res.StructuredContent, &payload); err != nil {
		t.Fatalf("unmarshal describe payload: %v (body: %s)", err, res.StructuredContent)
	}
	if !slices.Contains(payload.Deprecations, notice) {
		t.Fatalf("describe must name the withdrawn gate plugin; want %q, deprecations %#v", notice, payload.Deprecations)
	}
}

// TestDescribeToolRefusesAnUnreadableTrustRecord is the other half of reading the
// record: one that is there and cannot be read refuses the description, at the
// front door the lockfile is read at, rather than answering as though no plugin
// were withdrawn.
func TestDescribeToolRefusesAnUnreadableTrustRecord(t *testing.T) {
	s := newTestServer(t)
	root, dirs, releasePath := writeMCPTrustWorkspace(t, mcpTrustGateURI)
	if err := os.WriteFile(pluginstrust.PathFor(releasePath), []byte("plugins: [\n"), 0o644); err != nil {
		t.Fatalf("write damaged record: %v", err)
	}

	res := callTool(t, s, "describe", describeArgs{
		Release:       releasePath,
		WorkspaceRoot: root,
		CatalogDirs:   dirs,
	})
	if !res.IsError {
		t.Fatalf("a record that cannot be read must refuse rather than pass as empty; got: %s", res.Content[0].Text)
	}
	if text := res.Content[0].Text; !strings.Contains(text, "Could not read the plugin trust record beside the Release.") {
		t.Errorf("the refusal must say which record could not be read; got: %s", text)
	}
}
