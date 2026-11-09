// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package mcp

// The editor's validate tool runs the same pre-render pipeline a build runs, and
// two of that pipeline's phases dispatch operator-attached guests. So it holds the
// workspace configuration to the record of the plugin identities trusted for its
// verdict attachments before that pipeline runs.
//
// This is the editor-tool leg. It threads its inputs differently from the
// command-line verb and the daemon — the Release path is resolved from the call's
// own argument against the resolved workspace root — so its coverage is its own.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/pluginstrust"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

const mcpTrustGateSocket = "linter"
const mcpTrustGateName = "house-style"
const mcpTrustGateVersion = "0.3.0"
const mcpTrustGateCoordinate = mcpTrustGateSocket + "/" + mcpTrustGateName
const mcpTrustGateURI = mcpTrustGateCoordinate + ":" + mcpTrustGateVersion

// mcpTrustGatePluginEntity is the Plugin entity the fixture's record names. It is
// attached at the linter socket, a verdict-tier socket the pre-render pipeline
// never dispatches, so the attachment joins the verdict attachment set the record
// is compared against without any guest bytes needing to exist — presence is
// decided before anything is loaded.
const mcpTrustGatePluginEntity = `apiVersion: aggrega.ai/v1beta1
kind: Plugin
metadata:
  name: ` + mcpTrustGateName + `
  version: "` + mcpTrustGateVersion + `"
  pluginType: ` + mcpTrustGateSocket + `
spec:
  abiVersion: v1beta1
  exports:
    - name: lint_rules
`

// writeMCPTrustWorkspace lays the clean release-parity workspace, writes a record
// beside its Release naming the fixture gate, and writes a workspace configuration
// attaching each URI in attach at the linter socket. The tool resolves its own
// configuration from that file, which is why the attachment is authored rather than
// injected.
func writeMCPTrustWorkspace(t *testing.T, attach ...string) (root string, dirs []string, releasePath string) {
	t.Helper()
	root = t.TempDir()
	writeCatalogFile(t, filepath.Join(root, "aggregate.yaml"), releaseParityBackendAggregate)
	writeCatalogFile(t, filepath.Join(root, "manifest.yaml"), releaseParityCleanManifest)
	releasePath = filepath.Join(root, "release.yaml")
	writeCatalogFile(t, releasePath, releaseParityDefaultRelease)

	overlay := filepath.Join(root, "overlay")
	writeCatalogFile(t,
		filepath.Join(overlay, "plugin", mcpTrustGateSocket, mcpTrustGateName, mcpTrustGateVersion, "plugin.yaml"),
		mcpTrustGatePluginEntity)

	cfg := "plugins:\n  linter:\n"
	if len(attach) == 0 {
		cfg = "plugins: {}\n"
	}
	for _, uri := range attach {
		cfg += "    - uri: " + uri + "\n"
	}
	if err := os.WriteFile(filepath.Join(root, workspaceconfig.ConfigFilename), []byte(cfg), 0o644); err != nil {
		t.Fatalf("write workspace config: %v", err)
	}

	record := &pluginstrust.Trust{
		Version: pluginstrust.CurrentVersion,
		Plugins: []pluginstrust.TrustedPlugin{{
			Plugin:          mcpTrustGateCoordinate,
			ResolvedVersion: mcpTrustGateVersion,
			Digest:          "sha256:1111111111111111111111111111111111111111111111111111111111111111",
		}},
	}
	if err := pluginstrust.Write(pluginstrust.PathFor(releasePath), record); err != nil {
		t.Fatalf("write trust record: %v", err)
	}
	return root, []string{overlay, fullstackCatalogDir(t)}, releasePath
}

// TestValidateToolRefusesAGateTheConfigurationDropped is the planted defect at the
// editor-tool leg: with a record naming a gate the configuration attaches nowhere,
// the tool reports the Release as invalid and carries the refusal in its
// diagnostics rather than computing and discarding it.
func TestValidateToolRefusesAGateTheConfigurationDropped(t *testing.T) {
	s := newTestServer(t)
	root, dirs, releasePath := writeMCPTrustWorkspace(t)

	res := callTool(t, s, "validate", validateArgs{
		Release:       releasePath,
		WorkspaceRoot: root,
		CatalogDirs:   dirs,
	})
	if res.IsError {
		t.Fatalf("release scope reported a protocol error rather than diagnostics: %s", res.Content[0].Text)
	}
	payload := unmarshalValidatePayload(t, res)
	if payload.Valid {
		t.Fatalf("a record naming a gate the configuration attaches nowhere must refuse; got a valid verdict: %+v", payload)
	}
	var found bool
	for _, d := range payload.Diagnostics {
		if strings.Contains(d, mcpTrustGateCoordinate) &&
			strings.Contains(d, pluginstrust.ErrVerdictAttachmentNotConfigured().Error()) {
			found = true
		}
	}
	if !found {
		t.Fatalf("no diagnostic carried the not-configured condition naming the dropped gate; got %#v", payload.Diagnostics)
	}
}

// TestValidateToolWithAMatchingConfigurationStaysSilent is the A/B that makes the
// refusal above attributable, and the silence half of the proof: over the identical
// workspace and the identical record, with the configuration still attaching the
// recorded gate, the tool reports a clean Release.
func TestValidateToolWithAMatchingConfigurationStaysSilent(t *testing.T) {
	s := newTestServer(t)
	root, dirs, releasePath := writeMCPTrustWorkspace(t, mcpTrustGateURI)

	// The comparison against the record is suppressed when an attachment's plugin
	// cannot be identified — deliberately, because an unidentifiable attachment
	// might be the very one accounting for a record entry. So a fixture whose Plugin
	// entity does not resolve would produce silence with the record unenforced,
	// which is indistinguishable from a match. Both halves are asserted: the
	// configuration attaches exactly the recorded gate, and that attachment's entity
	// resolves to the coordinate the record is addressed by.
	tw := resolvedToolWorkspace(t, s, root, dirs)
	attached := tw.cfg.VerdictAttachments()
	if len(attached) != 1 || attached[0].Ref.URI != mcpTrustGateURI {
		t.Fatalf("fixture no longer attaches the recorded gate, so the silence would be vacuous; attachments %+v", attached)
	}
	entity, ok := tw.newView("", nil).PluginByURI(mcpTrustGateURI)
	if !ok {
		t.Fatal("the attached gate's Plugin entity does not resolve, so the comparison would be suppressed rather than satisfied")
	}
	if got := entity.ArtifactReference(); got != mcpTrustGateCoordinate {
		t.Fatalf("the attached gate resolves to coordinate %q; the record is addressed by %q, so the silence would not be a match",
			got, mcpTrustGateCoordinate)
	}

	res := callTool(t, s, "validate", validateArgs{
		Release:       releasePath,
		WorkspaceRoot: root,
		CatalogDirs:   dirs,
	})
	if res.IsError {
		t.Fatalf("release scope reported a protocol error rather than diagnostics: %s", res.Content[0].Text)
	}
	payload := unmarshalValidatePayload(t, res)
	if !payload.Valid {
		t.Fatalf("a record whose gate is still attached must refuse nothing; got %#v", payload.Diagnostics)
	}
}
