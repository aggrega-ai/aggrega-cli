// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package serve

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
)

// connectionAttachmentFixtureDir is the committed workspace whose gateway draws
// connections through a socket that declares an attachment slot.
const connectionAttachmentFixtureDir = "../compiler/testdata/connection-attachment"

// TestAGraphFindingIsPublishedOnTheDocumentThatWroteIt drives the daemon's
// aggrega/validate over both documents of one Release and pins where a finding
// the graph's rules raise is published: on the document whose author wrote what
// it is about, on the authored key there, and on the other document not at all.
//
// The rule is the one a socket's required slot carries — each connection wired
// through the socket owes it a fill — because one fixture then produces the same
// finding from either document by moving a single connection: the Release's
// per-edge entry wires one, or the Aggregate's own block does. Each row validates
// both documents, so a finding published on both, on neither, or on the wrong one
// fails its row.
//
// The Aggregate row is the control. It is the case the daemon published correctly
// before a finding could name the Release's file, and it must still land on the
// Aggregate's key and draw nothing on the Release.
func TestAGraphFindingIsPublishedOnTheDocumentThatWroteIt(t *testing.T) {
	t.Parallel()

	const sentence = "required attachment slot unfilled"
	for _, row := range []struct {
		name string
		// extraConnection is written beside the filled one in the Aggregate's
		// gateway block; releaseGateway is the gateway's entry in the Release.
		extraConnection string
		releaseGateway  string
		// writtenIn is the document the finding must land on, and connection
		// the key it must land on there.
		writtenIn  string
		connection string
	}{
		{
			name:           "a connection the Release wires and leaves unfilled",
			releaseGateway: "          uses:\n            models:\n              gpt-c:\n                target: model\n",
			writtenIn:      "release.yaml",
			connection:     "gpt-c",
		},
		{
			name:            "a connection the Aggregate draws and leaves unfilled",
			extraConnection: "          gpt-b:\n            target: model\n",
			writtenIn:       "aggregate.yaml",
			connection:      "gpt-b",
		},
	} {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			ws := requiredSocketSlotWorkspace(t, row.extraConnection, row.releaseGateway)
			srv := newTestServer(t, ws, nil, nil)
			dirs := []string{filepath.Join(ws, "catalog")}
			srv.wsConfig.Workspace.CatalogDirs = dirs
			srv.lazyCatalog = catalog.NewLazyLookup(ws, "", dirs, nil)

			for _, document := range []string{"aggregate.yaml", "release.yaml"} {
				content, diags := validateDocumentAgainstRelease(t, srv, filepath.Join(ws, document))
				if document != row.writtenIn {
					for _, d := range diags {
						if containsSubstring(d.Message, sentence) {
							t.Errorf("%s carries a finding written in %s: %q at %d:%d",
								document, row.writtenIn, d.Message, d.Line, d.Column)
						}
					}
					continue
				}
				finding := findDiagnostic(t, diags, `left unfilled by the connection "`+row.connection+`"`)
				if !containsSubstring(finding.Message, sentence) {
					t.Errorf("the finding on %s does not open with the condition's sentence: %q", document, finding.Message)
				}
				if finding.Severity != "error" {
					t.Errorf("the finding on %s is published at %q; want error", document, finding.Severity)
				}
				if want := fixtureKeyLine(t, content, row.connection+":"); finding.Line != want {
					t.Errorf("the finding on %s is at line %d; want the %q key at line %d (diagnostics: %v)",
						document, finding.Line, row.connection, want, diagMessages(diags))
				}
			}
		})
	}
}

// TestACompositionFindingIsPublishedOnTheDocumentThatWroteIt pins the same rule
// for the findings raised before any graph exists — while the two documents are
// read, and once they are flattened — and for the conformance of a connection
// attachment's pillars: each is published on the document whose author wrote
// what it is about, on the authored key there, and on the other document not at
// all.
//
// Every Release row is a refusal of something the operator wrote on a
// connection entry inside a boundary, a path the Aggregate does not contain; the
// Aggregate row is the control, a refusal of the same kind the developer wrote,
// which must stay on the Aggregate.
func TestACompositionFindingIsPublishedOnTheDocumentThatWroteIt(t *testing.T) {
	t.Parallel()

	for _, row := range []struct {
		name            string
		extraConnection string
		releaseGateway  string
		// writtenIn is the document the finding must land on, key the authored
		// key it must land on there, and detail a value only this finding names.
		writtenIn string
		key       string
		detail    string
	}{
		{
			name: "the two documents name different Features for one connection's attachment",
			releaseGateway: "          uses:\n            models:\n              gpt:\n                features:\n" +
				"                  credential:\n                    type: feature.conn/identity\n",
			writtenIn: "release.yaml",
			key:       "gpt:",
			detail:    `the Release names "feature.conn/identity"`,
		},
		{
			name: "the Release writes a name its socket declares no slot for",
			releaseGateway: "          uses:\n            models:\n              gpt:\n                features:\n" +
				"                  credentail:\n                    type: feature.conn/token\n",
			writtenIn: "release.yaml",
			key:       "gpt:",
			detail:    `"credentail", near the declared slot "credential"`,
		},
		{
			name: "the Release sizes a connection against the socket's contract",
			releaseGateway: "          uses:\n            models:\n              gpt:\n                infra:\n" +
				"                  timeout: soon\n",
			writtenIn: "release.yaml",
			key:       "timeout:",
			detail:    `"infra.timeout"`,
		},
		{
			name: "the Release sizes a connection's attachment against its Feature's contract",
			releaseGateway: "          uses:\n            models:\n              gpt:\n                features:\n" +
				"                  credential:\n                    infra:\n                      tier: gold\n",
			writtenIn: "release.yaml",
			key:       "tier:",
			detail:    "infra pillar does not conform to the Feature schema",
		},
		{
			name: "the Aggregate writes a name its socket declares no slot for",
			extraConnection: "          gpt-b:\n            target: model\n            features:\n" +
				"              credentail:\n                type: feature.conn/token\n",
			writtenIn: "aggregate.yaml",
			key:       "gpt-b:",
			detail:    `"credentail", near the declared slot "credential"`,
		},
	} {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			ws := compositionFindingWorkspace(t, row.extraConnection, row.releaseGateway)
			srv := newTestServer(t, ws, nil, nil)
			dirs := []string{filepath.Join(ws, "catalog")}
			srv.wsConfig.Workspace.CatalogDirs = dirs
			srv.lazyCatalog = catalog.NewLazyLookup(ws, "", dirs, nil)

			for _, document := range []string{"aggregate.yaml", "release.yaml"} {
				content, diags := validateDocumentAgainstRelease(t, srv, filepath.Join(ws, document))
				if document != row.writtenIn {
					for _, d := range diags {
						if containsSubstring(d.Message, row.detail) {
							t.Errorf("%s carries a finding written in %s: %q at %d:%d",
								document, row.writtenIn, d.Message, d.Line, d.Column)
						}
					}
					continue
				}
				finding := findDiagnostic(t, diags, row.detail)
				if finding.Severity != "error" {
					t.Errorf("the finding on %s is published at %q; want error", document, finding.Severity)
				}
				if want := fixtureKeyLine(t, content, row.key); finding.Line != want {
					t.Errorf("the finding on %s is at line %d; want the %q key at line %d (diagnostics: %v)",
						document, finding.Line, row.key, want, diagMessages(diags))
				}
			}
		})
	}
}

// compositionFindingWorkspace is requiredSocketSlotWorkspace with what the
// composition's refusals need beside it: the gateway's socket declares an infra
// contract and its slot is optional, the token Feature declares an infra
// contract of its own, and a second Feature the Manifest maps stands ready to be
// named for one attachment by the other document.
func compositionFindingWorkspace(t *testing.T, extraConnection, releaseGateway string) string {
	t.Helper()
	ws := requiredSocketSlotWorkspace(t, extraConnection, releaseGateway)
	catalogDir := filepath.Join(ws, "catalog")
	manifest, err := os.ReadFile(filepath.Join(ws, "manifest.yaml"))
	if err != nil {
		t.Fatalf("read the workspace manifest: %v", err)
	}
	for path, body := range map[string]string{
		filepath.Join(catalogDir, "components", "gateway.yaml"): "apiVersion: aggrega.ai/v1alpha1\nkind: Component\n" +
			"metadata:\n  name: conn/gateway\n  version: \"1.0.0\"\n  labels:\n    component.aggrega.ai/type: \"server\"\n" +
			"spec:\n  featuresSchema:\n    db_password:\n      sensitive: true\n" +
			"  usesSchema:\n    models:\n      selector: \"component.aggrega.ai/category=model\"\n" +
			"      infraSchema:\n        timeout: { type: integer }\n" +
			"      featuresSchema:\n        credential:\n          sensitive: true\n" +
			"  schema:\n    values:\n      region: { type: string }\n    provides:\n      host: { type: string, required: true }\n",
		filepath.Join(catalogDir, "features", "token.yaml"): "apiVersion: aggrega.ai/v1alpha1\nkind: Feature\n" +
			"metadata:\n  name: feature.conn/token\n  version: \"1.0.0\"\n  labels:\n    feature.aggrega.ai/kind: secret\n" +
			"spec:\n  schema:\n    values:\n      key: { type: string }\n" +
			"    infra:\n      tier: { type: string, enum: [standard, premium] }\n" +
			"    provides:\n      key: { type: string, required: true }\n",
		filepath.Join(catalogDir, "features", "identity.yaml"): "apiVersion: aggrega.ai/v1alpha1\nkind: Feature\n" +
			"metadata:\n  name: feature.conn/identity\n  version: \"1.0.0\"\n  labels:\n    feature.aggrega.ai/kind: secret\n" +
			"spec:\n  schema:\n    values:\n      key: { type: string }\n    provides:\n      key: { type: string, required: true }\n",
		filepath.Join(catalogDir, "feature-recipes", "identity-compose.yaml"): "apiVersion: aggrega.ai/v1alpha1\n" +
			"kind: FeatureRecipe\nmetadata:\n  name: conn-identity-compose\n  version: \"1.0.0\"\n" +
			"spec:\n  capability: compose\n  implements: feature.conn/identity\n  provides:\n    key: \"{{ .values.key }}\"\n",
		filepath.Join(ws, "manifest.yaml"): string(manifest) + "        feature.conn/identity: conn-identity-compose:1.0.0\n",
	} {
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}
	return ws
}

// validateDocumentAgainstRelease drives aggrega/validate over one document of the
// workspace, naming the workspace's Release, and returns the document's content
// and the diagnostics the reply carries for it.
func validateDocumentAgainstRelease(t *testing.T, srv *Server, path string) (string, []Diagnostic) {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	resp := requestOverTheWire(t, srv, "aggrega/validate", validateParams{
		File:    path,
		Content: string(content),
		Release: "release.yaml",
	})
	if resp.Error != nil {
		t.Fatalf("validate %s: unexpected envelope %+v", path, resp.Error)
	}
	var result validateResult
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		t.Fatalf("unmarshal validate result for %s: %v", path, err)
	}
	return string(content), result.Diagnostics
}

// requiredSocketSlotWorkspace writes a workspace holding a copy of the committed
// connection-attachment catalog, its gateway's socket slot declared required, the
// committed Manifest, an Aggregate whose gateway fills that slot on one connection
// and draws extraConnection beside it, and a Release placing both components with
// releaseGateway as the gateway's own entry.
func requiredSocketSlotWorkspace(t *testing.T, extraConnection, releaseGateway string) string {
	t.Helper()
	ws := t.TempDir()
	catalogDir := filepath.Join(ws, "catalog")
	if err := os.CopyFS(catalogDir, os.DirFS(filepath.Join(connectionAttachmentFixtureDir, "catalog", "base"))); err != nil {
		t.Fatalf("copy the committed catalog: %v", err)
	}
	gateway := filepath.Join(catalogDir, "components", "gateway.yaml")
	if _, err := os.Stat(gateway); err != nil {
		t.Fatalf("the committed catalog no longer holds the gateway this workspace rewrites: %v", err)
	}
	manifest, err := os.ReadFile(filepath.Join(connectionAttachmentFixtureDir, "manifest.yaml"))
	if err != nil {
		t.Fatalf("read the committed manifest: %v", err)
	}
	gatewayEntry := "        gateway: {}\n"
	if releaseGateway != "" {
		gatewayEntry = "        gateway:\n" + releaseGateway
	}
	for path, body := range map[string]string{
		gateway: "apiVersion: aggrega.ai/v1alpha1\nkind: Component\nmetadata:\n  name: conn/gateway\n" +
			"  version: \"1.0.0\"\n  labels:\n    component.aggrega.ai/type: \"server\"\n" +
			"spec:\n  featuresSchema:\n    db_password:\n      sensitive: true\n" +
			"  usesSchema:\n    models:\n      selector: \"component.aggrega.ai/category=model\"\n" +
			"      featuresSchema:\n        credential:\n          sensitive: true\n          required: true\n" +
			"  schema:\n    values:\n      region: { type: string }\n    provides:\n      host: { type: string, required: true }\n",
		filepath.Join(ws, "manifest.yaml"): string(manifest),
		filepath.Join(ws, "aggregate.yaml"): "apiVersion: aggrega.ai/v1alpha1\nkind: Aggregate\n" +
			"metadata:\n  name: connection-attachment-app\n  version: \"1.0.0\"\nspec:\n  components:\n" +
			"    gateway:\n      type: conn/gateway\n      values:\n        region: eu\n" +
			"      features:\n        db_password:\n          type: feature.conn/token\n" +
			"          values:\n            key: db-key\n" +
			"      uses:\n        models:\n" +
			"          gpt:\n            target: model\n            features:\n" +
			"              credential:\n                type: feature.conn/token\n" +
			"                values:\n                  key: gpt-key\n" +
			extraConnection +
			"    model:\n      type: conn/model\n",
		filepath.Join(ws, "release.yaml"): "apiVersion: aggrega.ai/v1alpha1\nkind: Release\n" +
			"metadata:\n  name: connection-attachment-release\n  environment: local\n  version: \"1.0.0\"\n" +
			"spec:\n  aggregate: ./aggregate.yaml\n  manifest: ./manifest.yaml\n  boundaries:\n" +
			"    cluster:\n      directory: ./dist/cluster\n      components:\n" + gatewayEntry + "        model: {}\n",
	} {
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}
	return ws
}
