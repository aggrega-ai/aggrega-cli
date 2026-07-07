// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package terraform

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
)

// terraformNodeCtx returns the render context a terraform artifacts placement
// stamps provenance from — the identity the shared HCL-comment formatter reads.
func terraformNodeCtx() renderer.NodeRenderContext {
	return renderer.NodeRenderContext{
		NodeID:          "db",
		BoundaryPath:    "production",
		RecipeName:      "mariadb-rds",
		RecipeVersion:   "1.0.0",
		ProviderName:    "terraform",
		ProviderVersion: "1.0.0",
		Provenance: renderer.ProvenanceContext{
			CompilerVersion: "0.1.0",
			AggregateName:   "shop",
			Environment:     "production",
		},
	}
}

// wantProvenanceComment is the exact HCL provenance comment block PlaceArtifacts
// stamps for terraformNodeCtx's node. Written literally (not produced by the
// formatter) so this test independently pins the marker, the aggrega.ai/* key
// set, the deterministic alphabetical line order, and the exclusion of the
// Kubernetes-only app.kubernetes.io/name key.
const wantProvenanceComment = `# Aggrega build provenance
# aggrega.ai/aggregate-name: shop
# aggrega.ai/boundary-path: production
# aggrega.ai/compiler-version: 0.1.0
# aggrega.ai/component-id: db
# aggrega.ai/environment: production
# aggrega.ai/provider: terraform
# aggrega.ai/provider-version: 1.0.0
# aggrega.ai/recipe-uri: mariadb-rds:1.0.0
`

const hclBody = `resource "aws_db_instance" "prod-db" {
  identifier        = "prod-db"
  engine            = "mariadb"
  endpoint_probe    = "${aws_db_instance.prod-db.endpoint}"
}
`

const jsonBody = `{
  "resource": {
    "aws_db_instance": {
      "prod-db": {
        "identifier": "prod-db",
        "endpoint_probe": "${aws_db_instance.prod-db.endpoint}"
      }
    }
  }
}
`

func entry(name, content string) renderer.ArtifactProjectionEntry {
	return renderer.ArtifactProjectionEntry{Name: name, Origin: "inline", Content: []byte(content)}
}

func fileByPath(t *testing.T, files []renderer.RenderedFile, path string) renderer.RenderedFile {
	t.Helper()
	for _, f := range files {
		if f.Path == path {
			return f
		}
	}
	t.Fatalf("no file named %q; got %d files", path, len(files))
	return renderer.RenderedFile{}
}

// TestPlaceArtifacts_PureHCLPrependsProvenance pins the pure-HCL contract: the
// build provenance is prepended as an HCL comment block, the HCL below it is
// byte-identical to the input, the Terraform ${...} interpolation survives, and
// no Kubernetes-only key leaks into Terraform output. No companion is emitted.
func TestPlaceArtifacts_PureHCLPrependsProvenance(t *testing.T) {
	t.Parallel()
	proj := renderer.ArtifactsProjection{Entries: []renderer.ArtifactProjectionEntry{entry("main.tf", hclBody)}}
	files := PlaceArtifacts(proj, terraformNodeCtx())

	if len(files) != 1 {
		t.Fatalf("expected 1 file (no companion for pure HCL), got %d", len(files))
	}
	got := fileByPath(t, files, "main.tf")
	want := wantProvenanceComment + hclBody
	if string(got.Content) != want {
		t.Errorf("body not provenance-comment + verbatim HCL.\n--- got ---\n%s\n--- want ---\n%s", got.Content, want)
	}
	if strings.TrimPrefix(string(got.Content), wantProvenanceComment) != hclBody {
		t.Errorf("HCL below the comment is not byte-identical to the input")
	}
	for _, banned := range []string{"app.kubernetes.io/name", "argocd.argoproj.io", "metadata:", "\nlabels:"} {
		if strings.Contains(string(got.Content), banned) {
			t.Errorf("terraform output must not contain %q", banned)
		}
	}
}

// TestPlaceArtifacts_JSONBodyEmitsCompanion pins the .tf.json contract: a JSON
// body cannot carry a `#` comment, so it emits byte-for-byte and the provenance
// travels in a single companion aggrega.provenance.tf comment-only file. The JSON
// body stays valid JSON and the ${...} interpolation survives.
func TestPlaceArtifacts_JSONBodyEmitsCompanion(t *testing.T) {
	t.Parallel()
	proj := renderer.ArtifactsProjection{Entries: []renderer.ArtifactProjectionEntry{entry("main.tf.json", jsonBody)}}
	files := PlaceArtifacts(proj, terraformNodeCtx())

	if len(files) != 2 {
		t.Fatalf("expected 2 files (JSON body + companion), got %d", len(files))
	}
	body := fileByPath(t, files, "main.tf.json")
	companion := fileByPath(t, files, ProvenanceFileName)

	if string(body.Content) != jsonBody {
		t.Errorf("JSON body not emitted byte-for-byte.\n--- got ---\n%s\n--- want ---\n%s", body.Content, jsonBody)
	}
	if strings.HasPrefix(string(body.Content), "#") || strings.Contains(string(body.Content), "# Aggrega build provenance") {
		t.Errorf("JSON body must not carry an HCL `#` provenance comment; got:\n%s", body.Content)
	}
	var parsed any
	if err := json.Unmarshal(body.Content, &parsed); err != nil {
		t.Fatalf("emitted main.tf.json is not valid JSON (a provenance prefix would corrupt it): %v", err)
	}
	if !strings.Contains(string(body.Content), `"endpoint_probe": "${aws_db_instance.prod-db.endpoint}"`) {
		t.Errorf("${...} interpolation should survive verbatim in the JSON body")
	}
	if string(companion.Content) != wantProvenanceComment {
		t.Errorf("companion provenance mismatch.\n--- got ---\n%s\n--- want ---\n%s", companion.Content, wantProvenanceComment)
	}
}

// TestPlaceArtifacts_MixedHCLAndJSON pins the mixed-node rule: when any JSON body
// is present, the single companion is the sole provenance carrier and NO body is
// prefixed — so the block is never duplicated and the JSON is never corrupted.
func TestPlaceArtifacts_MixedHCLAndJSON(t *testing.T) {
	t.Parallel()
	proj := renderer.ArtifactsProjection{Entries: []renderer.ArtifactProjectionEntry{
		entry("main.tf", hclBody),
		entry("extra.tf.json", jsonBody),
	}}
	files := PlaceArtifacts(proj, terraformNodeCtx())

	if len(files) != 3 {
		t.Fatalf("expected 3 files (HCL + JSON + one companion), got %d", len(files))
	}
	hcl := fileByPath(t, files, "main.tf")
	if string(hcl.Content) != hclBody {
		t.Errorf("mixed-node HCL body should emit verbatim (no prefix); got:\n%s", hcl.Content)
	}
	if strings.Contains(string(hcl.Content), "# Aggrega build provenance") {
		t.Errorf("mixed-node HCL body must not carry a prepended provenance comment")
	}
	jsonFile := fileByPath(t, files, "extra.tf.json")
	if string(jsonFile.Content) != jsonBody {
		t.Errorf("mixed-node JSON body should emit verbatim")
	}
	companion := fileByPath(t, files, ProvenanceFileName)
	if string(companion.Content) != wantProvenanceComment {
		t.Errorf("companion provenance mismatch:\n%s", companion.Content)
	}
	count := 0
	for _, f := range files {
		if f.Path == ProvenanceFileName {
			count++
		}
	}
	if count != 1 {
		t.Errorf("expected exactly one companion, got %d", count)
	}
}

// TestPlaceArtifacts_Deterministic pins Invariant 8: two independent placements of
// the same input produce byte-identical output.
func TestPlaceArtifacts_Deterministic(t *testing.T) {
	t.Parallel()
	proj := renderer.ArtifactsProjection{Entries: []renderer.ArtifactProjectionEntry{
		entry("main.tf", hclBody),
		entry("extra.tf.json", jsonBody),
	}}
	a := PlaceArtifacts(proj, terraformNodeCtx())
	b := PlaceArtifacts(proj, terraformNodeCtx())
	if len(a) != len(b) {
		t.Fatalf("file count differs: %d vs %d", len(a), len(b))
	}
	for i := range a {
		if a[i].Path != b[i].Path || string(a[i].Content) != string(b[i].Content) {
			t.Errorf("non-deterministic output at index %d (%q)", i, a[i].Path)
		}
	}
}
