// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package serve

// The daemon's validation runs the same pre-render pipeline a build runs, and two
// of that pipeline's phases dispatch operator-attached guests. So the daemon holds
// the workspace configuration to the record of the plugin identities trusted for
// its verdict attachments before that pipeline runs.
//
// This is the daemon leg. It threads its inputs differently from the command-line
// verb and the editor tool — the Release path comes off the active-Release state
// rather than off an argument — so its coverage is its own.

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/pluginstrust"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

const serveTrustGateSocket = "linter"
const serveTrustGateName = "house-style"
const serveTrustGateVersion = "0.3.0"
const serveTrustGateCoordinate = serveTrustGateSocket + "/" + serveTrustGateName
const serveTrustGateURI = serveTrustGateCoordinate + ":" + serveTrustGateVersion

// serveTrustGatePluginEntity is the Plugin entity the fixture's record names. It
// is attached at the linter socket, a verdict-tier socket the pre-render pipeline
// never dispatches, so the attachment joins the verdict attachment set the record
// is compared against without any guest bytes needing to exist — presence is
// decided before anything is loaded.
const serveTrustGatePluginEntity = `apiVersion: aggrega.ai/v1beta1
kind: Plugin
metadata:
  name: ` + serveTrustGateName + `
  version: "` + serveTrustGateVersion + `"
  pluginType: ` + serveTrustGateSocket + `
spec:
  abiVersion: v1beta1
  exports:
    - name: lint_rules
`

// serveTrustServer lays the clean parity workspace, writes a record beside its
// Release naming the fixture gate, and returns a daemon pointed at it with attach
// as its linter attachment set. It also returns the Aggregate path and bytes the
// validation is requested for.
func serveTrustServer(t *testing.T, attach ...string) (srv *Server, releasePath, aggPath string, content []byte) {
	t.Helper()
	root := t.TempDir()
	writeServeFile(t, filepath.Join(root, "aggregate.yaml"), serveBackendAggregate)
	writeServeFile(t, filepath.Join(root, "manifest.yaml"), serveCleanManifest)
	releasePath = filepath.Join(root, "release.yaml")
	writeServeFile(t, releasePath, serveDefaultRelease)

	overlay := filepath.Join(root, "overlay")
	writeServeFile(t,
		filepath.Join(overlay, "plugin", serveTrustGateSocket, serveTrustGateName, serveTrustGateVersion, "plugin.yaml"),
		serveTrustGatePluginEntity)

	record := &pluginstrust.Trust{
		Version: pluginstrust.CurrentVersion,
		Plugins: []pluginstrust.TrustedPlugin{{
			Plugin:          serveTrustGateCoordinate,
			ResolvedVersion: serveTrustGateVersion,
			Digest:          "sha256:1111111111111111111111111111111111111111111111111111111111111111",
		}},
	}
	if err := pluginstrust.Write(pluginstrust.PathFor(releasePath), record); err != nil {
		t.Fatalf("write trust record: %v", err)
	}

	dirs := []string{overlay, serveFullstackCatalogDir(t)}
	srv = newTestServer(t, root, nil, nil)
	srv.wsConfig.Workspace.CatalogDirs = dirs
	for _, uri := range attach {
		srv.wsConfig.Plugins.Linter = append(srv.wsConfig.Plugins.Linter, workspaceconfig.PluginRef{URI: uri})
	}
	srv.lazyCatalog = catalog.NewLazyLookup(root, "", dirs, nil)

	aggPath = filepath.Join(root, "aggregate.yaml")
	var err error
	content, err = os.ReadFile(aggPath)
	if err != nil {
		t.Fatalf("read aggregate: %v", err)
	}
	return srv, releasePath, aggPath, content
}

// TestServeValidationRefusesAGateTheConfigurationDropped is the planted defect at
// the daemon leg: with a record naming a gate the configuration attaches nowhere,
// the validation refuses and the refusal reaches the editor as a diagnostic rather
// than being computed and discarded.
func TestServeValidationRefusesAGateTheConfigurationDropped(t *testing.T) {
	srv, releasePath, aggPath, content := serveTrustServer(t)
	doc := parseYAML(t, string(content))

	diags := srv.runTier4Validation(context.Background(), releasePath, aggPath, content, doc)
	if len(diags) == 0 {
		t.Fatal("a record naming a gate the configuration attaches nowhere must refuse; got no diagnostics")
	}
	want := conditionCodesOf(pluginstrust.ErrVerdictAttachmentNotConfigured()).Code
	if want == "" {
		t.Fatal("the not-configured condition carries no diagnostic code; the assertion below would be vacuous")
	}
	var found bool
	for _, d := range diags {
		if d.Code == want && d.Severity == editorSeverityError {
			found = true
			if !strings.Contains(d.Message, serveTrustGateCoordinate) {
				t.Errorf("the diagnostic must name the dropped gate; got %q", d.Message)
			}
		}
	}
	if !found {
		t.Fatalf("no error diagnostic carried the not-configured condition (code %q); got %+v", want, diags)
	}
}

// TestServeValidationWithAMatchingConfigurationStaysSilent is the A/B that makes
// the refusal above attributable, and the silence half of the proof: over the
// identical workspace and the identical record, with the configuration still
// attaching the recorded gate, the daemon reports nothing at all.
func TestServeValidationWithAMatchingConfigurationStaysSilent(t *testing.T) {
	srv, releasePath, aggPath, content := serveTrustServer(t, serveTrustGateURI)
	// The comparison against the record is suppressed when an attachment's plugin
	// cannot be identified — deliberately, because an unidentifiable attachment
	// might be the very one accounting for a record entry. So a fixture whose Plugin
	// entity does not resolve would produce silence with the record unenforced,
	// which is indistinguishable from a match. Both halves are asserted: the
	// configuration attaches exactly the recorded gate, and that attachment's entity
	// resolves to the coordinate the record is addressed by.
	attached := srv.wsConfig.VerdictAttachments()
	if len(attached) != 1 || attached[0].Ref.URI != serveTrustGateURI {
		t.Fatalf("fixture no longer attaches the recorded gate, so the silence would be vacuous; attachments %+v", attached)
	}
	entity, ok := srv.lazyCatalog.ForRequest(nil).PluginByURI(serveTrustGateURI)
	if !ok {
		t.Fatal("the attached gate's Plugin entity does not resolve, so the comparison would be suppressed rather than satisfied")
	}
	if got := entity.ArtifactReference(); got != serveTrustGateCoordinate {
		t.Fatalf("the attached gate resolves to coordinate %q; the record is addressed by %q, so the silence would not be a match",
			got, serveTrustGateCoordinate)
	}
	doc := parseYAML(t, string(content))

	diags := srv.runTier4Validation(context.Background(), releasePath, aggPath, content, doc)
	if len(diags) != 0 {
		t.Fatalf("a record whose gate is still attached must refuse nothing; got %+v", diags)
	}
}
