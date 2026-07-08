// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package recipepin_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/aggrega-ai/aggrega-cli/pkg/digest"
	"github.com/aggrega-ai/aggrega-cli/pkg/recipepin"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

const bareRecipe = `apiVersion: aggrega.ai/v1alpha1
kind: Recipe
metadata:
  name: server
  version: "1.0.0"
spec:
  target: helm
  implements: server
  # a preserved comment
  valuesMapping: |
    image: nginx
`

func writeRecipe(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "recipe.yaml")
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	return p
}

func readSpec(t *testing.T, path string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	var doc map[string]any
	if err := yaml.Unmarshal(data, &doc); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	spec, _ := doc["spec"].(map[string]any)
	return spec
}

func TestWritePinsFreezesSourceAndArtifactSections(t *testing.T) {
	p := writeRecipe(t, bareRecipe)
	src := []types.SourceDigest{{Kind: "helm-https-chart", URL: "https://x/c.tgz", Version: "1.0.0", Digest: "sha256:aaa"}}
	art := []types.ArtifactDigest{{Name: "extra.yaml", Digest: "sha256:bbb"}}

	changed, err := recipepin.WritePins(p, src, art)
	if err != nil {
		t.Fatalf("WritePins: %v", err)
	}
	if !changed {
		t.Fatalf("expected changed=true on first freeze")
	}
	spec := readSpec(t, p)
	if _, ok := spec["sourceDigests"]; !ok {
		t.Errorf("sourceDigests not written: %v", spec)
	}
	if _, ok := spec["artifactDigests"]; !ok {
		t.Errorf("artifactDigests not written: %v", spec)
	}
	// Untouched keys and the comment survive.
	body, _ := os.ReadFile(p)
	if !strings.Contains(string(body), "preserved comment") {
		t.Errorf("comment not preserved:\n%s", body)
	}
	if !strings.Contains(string(body), "implements: server") {
		t.Errorf("existing keys not preserved:\n%s", body)
	}
}

func TestWritePinsIdempotentWhenUnchanged(t *testing.T) {
	p := writeRecipe(t, bareRecipe)
	src := []types.SourceDigest{{Kind: "helm-https-chart", URL: "https://x/c.tgz", Version: "1.0.0", Digest: "sha256:aaa"}}
	art := []types.ArtifactDigest{{Name: "extra.yaml", Digest: "sha256:bbb"}}

	if _, err := recipepin.WritePins(p, src, art); err != nil {
		t.Fatalf("first WritePins: %v", err)
	}
	first, _ := os.ReadFile(p)

	// Re-freeze the SAME pins (order permuted to prove canonicalization): no write.
	changed, err := recipepin.WritePins(p, src, art)
	if err != nil {
		t.Fatalf("second WritePins: %v", err)
	}
	if changed {
		t.Errorf("expected changed=false when pins unchanged (read, not re-frozen)")
	}
	second, _ := os.ReadFile(p)
	if string(first) != string(second) {
		t.Errorf("unchanged re-freeze churned the file:\n--- first ---\n%s\n--- second ---\n%s", first, second)
	}
}

func TestWritePinsReFreezesWhenPinsChange(t *testing.T) {
	p := writeRecipe(t, bareRecipe)
	src := []types.SourceDigest{{Kind: "helm-https-chart", URL: "https://x/c.tgz", Version: "1.0.0", Digest: "sha256:aaa"}}
	if _, err := recipepin.WritePins(p, src, nil); err != nil {
		t.Fatalf("first WritePins: %v", err)
	}
	// A changed source digest re-freezes.
	src2 := []types.SourceDigest{{Kind: "helm-https-chart", URL: "https://x/c.tgz", Version: "1.0.0", Digest: "sha256:ZZZ"}}
	changed, err := recipepin.WritePins(p, src2, nil)
	if err != nil {
		t.Fatalf("re-freeze: %v", err)
	}
	if !changed {
		t.Errorf("expected changed=true when a source digest changed")
	}
	if !strings.Contains(string(mustRead(t, p)), "sha256:ZZZ") {
		t.Errorf("re-freeze did not update the digest")
	}
}

func TestWritePinsRemovesEmptySections(t *testing.T) {
	p := writeRecipe(t, bareRecipe)
	src := []types.SourceDigest{{Kind: "helm-https-chart", URL: "https://x/c.tgz", Version: "1.0.0", Digest: "sha256:aaa"}}
	if _, err := recipepin.WritePins(p, src, nil); err != nil {
		t.Fatalf("freeze: %v", err)
	}
	// Now the recipe no longer references any remote source: the section is removed.
	changed, err := recipepin.WritePins(p, nil, nil)
	if err != nil {
		t.Fatalf("clear: %v", err)
	}
	if !changed {
		t.Errorf("expected changed=true when clearing a present section")
	}
	if _, ok := readSpec(t, p)["sourceDigests"]; ok {
		t.Errorf("sourceDigests should have been removed when empty")
	}
}

func TestArtifactPinsForRecipeHashesImportsSkipsInline(t *testing.T) {
	dir := t.TempDir()
	imp := filepath.Join(dir, "extra.tmpl")
	body := []byte("hello: world\n")
	if err := os.WriteFile(imp, body, 0o644); err != nil {
		t.Fatalf("write import: %v", err)
	}
	var arts types.OrderedMap[types.ArtifactEntry]
	arts.Set("inline.yaml", types.ArtifactEntry{Inline: "a: b"})
	arts.Set("extra.yaml", types.ArtifactEntry{Import: imp})

	pins, err := recipepin.ArtifactPinsForRecipe(arts)
	if err != nil {
		t.Fatalf("ArtifactPinsForRecipe: %v", err)
	}
	if len(pins) != 1 {
		t.Fatalf("want 1 pin (inline skipped), got %d: %v", len(pins), pins)
	}
	if pins[0].Name != "extra.yaml" {
		t.Errorf("pin name = %q; want extra.yaml", pins[0].Name)
	}
	if pins[0].Digest != digest.ContentDigest(body) {
		t.Errorf("pin digest = %q; want %q", pins[0].Digest, digest.ContentDigest(body))
	}
}

func TestMatchHelpers(t *testing.T) {
	src := []types.SourceDigest{{Kind: "helm-https-chart", URL: "u", Version: "1.0.0", Digest: "d"}}
	if _, ok := recipepin.MatchSourcePin(src, "helm-https-chart", "u", "1.0.0"); !ok {
		t.Errorf("MatchSourcePin should find the pin")
	}
	if _, ok := recipepin.MatchSourcePin(src, "helm-https-chart", "u", "2.0.0"); ok {
		t.Errorf("MatchSourcePin should not match a different version")
	}
	art := []types.ArtifactDigest{{Name: "x.yaml", Digest: "d"}}
	if _, ok := recipepin.MatchArtifactPin(art, "x.yaml"); !ok {
		t.Errorf("MatchArtifactPin should find the pin")
	}
	if _, ok := recipepin.MatchArtifactPin(art, "y.yaml"); ok {
		t.Errorf("MatchArtifactPin should not match a different name")
	}
}

func mustRead(t *testing.T, p string) []byte {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	return b
}
