// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package recipepin_test

import (
	"bytes"
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
  capability: helm
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

// blankLinesBeforeSourceDigests returns the count of consecutive blank
// (empty or whitespace-only) lines immediately preceding the first line whose
// left-trimmed content begins with "sourceDigests:". It returns -1 when no such
// line is present.
func blankLinesBeforeSourceDigests(t *testing.T, body []byte) int {
	t.Helper()
	lines := strings.Split(string(body), "\n")
	for i, line := range lines {
		if !strings.HasPrefix(strings.TrimLeft(line, " \t"), "sourceDigests:") {
			continue
		}
		n := 0
		for j := i - 1; j >= 0 && strings.TrimSpace(lines[j]) == ""; j-- {
			n++
		}
		return n
	}
	return -1
}

func TestWritePinsBlankLineBeforeSourceDigestsIdempotent(t *testing.T) {
	p := writeRecipe(t, bareRecipe)
	src := []types.SourceDigest{{Kind: "helm-https-chart", URL: "https://x/c.tgz", Version: "1.0.0", Digest: "sha256:aaa"}}

	if _, err := recipepin.WritePins(p, src, nil); err != nil {
		t.Fatalf("first WritePins: %v", err)
	}
	if n := blankLinesBeforeSourceDigests(t, mustRead(t, p)); n != 1 {
		t.Fatalf("first freeze: want exactly 1 blank line before sourceDigests, got %d:\n%s", n, mustRead(t, p))
	}

	// Re-emit (a changed digest forces a re-marshal): the guard must not
	// accumulate a second blank line before the block.
	src2 := []types.SourceDigest{{Kind: "helm-https-chart", URL: "https://x/c.tgz", Version: "1.0.0", Digest: "sha256:bbb"}}
	changed, err := recipepin.WritePins(p, src2, nil)
	if err != nil {
		t.Fatalf("re-freeze WritePins: %v", err)
	}
	if !changed {
		t.Fatalf("expected changed=true when the digest changed")
	}
	if n := blankLinesBeforeSourceDigests(t, mustRead(t, p)); n != 1 {
		t.Fatalf("re-freeze: want exactly 1 blank line before sourceDigests, got %d:\n%s", n, mustRead(t, p))
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

	pins, err := recipepin.ArtifactPinsForRecipe(arts, "Recipe", "pinned-recipe")
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
	if _, ok := recipepin.MatchSourcePin(src, "helm-https-chart", "u", "", "", "1.0.0"); !ok {
		t.Errorf("MatchSourcePin should find the pin")
	}
	if _, ok := recipepin.MatchSourcePin(src, "helm-https-chart", "u", "", "", "2.0.0"); ok {
		t.Errorf("MatchSourcePin should not match a different version")
	}
	gitSrc := []types.SourceDigest{
		{Kind: "terraform-module-git", URL: "g", ModuleSubdir: "modules/alpha", Version: "sha", Digest: "da"},
		{Kind: "terraform-module-git", URL: "g", ModuleSubdir: "modules/beta", Version: "sha", Digest: "db"},
	}
	if p, ok := recipepin.MatchSourcePin(gitSrc, "terraform-module-git", "g", "", "modules/beta", "sha"); !ok || p.Digest != "db" {
		t.Errorf("MatchSourcePin should select the pin for the named module subdirectory; got %+v ok=%v", p, ok)
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

// mixedRecipe carries the mixed content a byte-faithful splice must preserve:
// 2-space indentation, a folded (>) scalar in metadata, a comment, quoted
// scalars, and a literal (|) block whose body contains an internal blank line.
const mixedRecipe = `apiVersion: aggrega.ai/v1alpha1
kind: Recipe
metadata:
  name: mixed
  version: "1.0.0"
  description: >
    A folded description that spans several
    words and must survive the freeze verbatim.
spec:
  capability: helm
  implements: "core/datastore/thing"
  # a preserved comment
  provides:
    host: "{{ .context.instance_id }}-svc"
    port: "5432"
  valuesMapping: |
    image: nginx

    replicas: 3
`

// excisePinSection removes the pin section (the blank separator plus the
// contiguous sourceDigests/artifactDigests blocks written at the end of spec)
// from b, returning what remains. It relies on the pin section sitting at the
// end of the file, which is where WritePins appends it.
func excisePinSection(b []byte) []byte {
	lines := strings.Split(string(b), "\n")
	first := -1
	for i, l := range lines {
		t := strings.TrimLeft(l, " ")
		if strings.HasPrefix(t, "sourceDigests:") || strings.HasPrefix(t, "artifactDigests:") {
			first = i
			break
		}
	}
	if first == -1 {
		return b
	}
	start := first
	if start > 0 && strings.TrimSpace(lines[start-1]) == "" {
		start--
	}
	end := len(lines)
	for end > first && strings.TrimSpace(lines[end-1]) == "" {
		end--
	}
	res := append([]string{}, lines[:start]...)
	res = append(res, lines[end:]...)
	return []byte(strings.Join(res, "\n"))
}

// differingLines returns the 0-indexed lines that differ between a and b.
func differingLines(a, b []byte) []int {
	la := strings.Split(string(a), "\n")
	lb := strings.Split(string(b), "\n")
	var diff []int
	n := len(la)
	if len(lb) > n {
		n = len(lb)
	}
	for i := 0; i < n; i++ {
		var x, y string
		if i < len(la) {
			x = la[i]
		}
		if i < len(lb) {
			y = lb[i]
		}
		if x != y {
			diff = append(diff, i)
		}
	}
	return diff
}

func TestWritePinsByteFaithfulAdd(t *testing.T) {
	p := writeRecipe(t, mixedRecipe)
	original := []byte(mixedRecipe)

	src := []types.SourceDigest{{Kind: "helm-https-chart", URL: "https://x/c.tgz", Version: "1.0.0", Digest: "sha256:aaa"}}
	art := []types.ArtifactDigest{{Name: "extra.yaml", Digest: "sha256:bbb"}}
	changed, err := recipepin.WritePins(p, src, art)
	if err != nil {
		t.Fatalf("WritePins: %v", err)
	}
	if !changed {
		t.Fatalf("expected changed=true on first freeze")
	}
	result := mustRead(t, p)

	// Every byte outside the appended pin section is identical to the original.
	if got := excisePinSection(result); !bytes.Equal(got, original) {
		t.Fatalf("bytes outside the pin section drifted:\n--- want ---\n%q\n--- got ---\n%q", original, got)
	}

	// The distinctive mixed-content fragments survive verbatim.
	for _, frag := range []string{
		"  description: >\n    A folded description that spans several\n    words and must survive the freeze verbatim.\n",
		"  # a preserved comment\n",
		"  implements: \"core/datastore/thing\"\n",
		"  valuesMapping: |\n    image: nginx\n\n    replicas: 3\n",
	} {
		if !bytes.Contains(result, []byte(frag)) {
			t.Errorf("fragment not preserved verbatim:\n%q\nin:\n%s", frag, result)
		}
	}

	// The pin section is present, at 2-space indentation, sourceDigests first.
	if !bytes.Contains(result, []byte("\n  sourceDigests:\n")) {
		t.Errorf("sourceDigests block not written at 2-space indent:\n%s", result)
	}
	if !bytes.Contains(result, []byte("\n  artifactDigests:\n")) {
		t.Errorf("artifactDigests block not written at 2-space indent:\n%s", result)
	}
	if si, ai := bytes.Index(result, []byte("sourceDigests:")), bytes.Index(result, []byte("artifactDigests:")); si > ai {
		t.Errorf("sourceDigests must precede artifactDigests (src=%d art=%d)", si, ai)
	}
}

func TestWritePinsByteFaithfulChange(t *testing.T) {
	p := writeRecipe(t, mixedRecipe)
	src := []types.SourceDigest{{Kind: "helm-https-chart", URL: "https://x/c.tgz", Version: "1.0.0", Digest: "sha256:aaa"}}
	art := []types.ArtifactDigest{{Name: "extra.yaml", Digest: "sha256:bbb"}}
	if _, err := recipepin.WritePins(p, src, art); err != nil {
		t.Fatalf("first WritePins: %v", err)
	}
	added := mustRead(t, p)

	// Change only the source digest: only the digest line may differ.
	src2 := []types.SourceDigest{{Kind: "helm-https-chart", URL: "https://x/c.tgz", Version: "1.0.0", Digest: "sha256:CHANGED"}}
	changed, err := recipepin.WritePins(p, src2, art)
	if err != nil {
		t.Fatalf("change WritePins: %v", err)
	}
	if !changed {
		t.Fatalf("expected changed=true when the digest changed")
	}
	after := mustRead(t, p)

	diff := differingLines(added, after)
	if len(diff) != 1 {
		t.Fatalf("expected exactly one differing line, got %d: %v\n--- added ---\n%s\n--- after ---\n%s", len(diff), diff, added, after)
	}
	afterLines := strings.Split(string(after), "\n")
	if dl := strings.TrimSpace(afterLines[diff[0]]); dl != "digest: sha256:CHANGED" {
		t.Errorf("the differing line is not the digest line: %q", dl)
	}
}

func TestWritePinsByteFaithfulRemoveRestoresOriginal(t *testing.T) {
	p := writeRecipe(t, mixedRecipe)
	original := []byte(mixedRecipe)

	src := []types.SourceDigest{{Kind: "helm-https-chart", URL: "https://x/c.tgz", Version: "1.0.0", Digest: "sha256:aaa"}}
	art := []types.ArtifactDigest{{Name: "extra.yaml", Digest: "sha256:bbb"}}
	if _, err := recipepin.WritePins(p, src, art); err != nil {
		t.Fatalf("add WritePins: %v", err)
	}

	// Removing all pins restores the file byte-for-byte.
	changed, err := recipepin.WritePins(p, nil, nil)
	if err != nil {
		t.Fatalf("remove WritePins: %v", err)
	}
	if !changed {
		t.Fatalf("expected changed=true when clearing present sections")
	}
	if got := mustRead(t, p); !bytes.Equal(got, original) {
		t.Fatalf("remove did not restore the original bytes:\n--- want ---\n%q\n--- got ---\n%q", original, got)
	}
}

// TestWritePinsByteFaithfulOnRealRecipe copies a real-shaped recipe fixture
// into a temp dir (the fixture itself is never touched), freezes one source
// pin, and asserts that no byte outside the pin region drifts: the folded
// description, the literal valuesMapping block, and the 2-space indentation
// carry through byte-for-byte, and the frozen result carries a sourceDigests
// block and no artifactDigests.
//
// The fixture is read as-is, so it may arrive pin-free or already carrying a
// frozen pin section — the freeze correspondingly adds the block or replaces the
// one present. The non-pin comparison therefore excises the pin region from both
// the frozen result and the copied original, which isolates the bytes the splice
// must not touch without constraining what the fixture happens to carry.
//
// The property under test — a pin splice touches no byte outside the pin
// region — holds for any real-shaped recipe, so the fixture supplies shape
// rather than standing in for any particular published document.
func TestWritePinsByteFaithfulOnRealRecipe(t *testing.T) {
	original, err := os.ReadFile(filepath.Join("testdata", "mariadb-bitnami.yaml"))
	if err != nil {
		t.Fatalf("read recipe fixture: %v", err)
	}
	p := filepath.Join(t.TempDir(), "mariadb-bitnami.yaml")
	if err := os.WriteFile(p, original, 0o644); err != nil {
		t.Fatalf("copy: %v", err)
	}

	src := []types.SourceDigest{{
		Kind:           "helm-oci-chart",
		URL:            "oci://registry-1.docker.io/bitnamicharts/mariadb",
		Version:        "1.0.0",
		Digest:         "sha256:deadbeef",
		ManifestDigest: "sha256:cafebabe",
	}}
	changed, err := recipepin.WritePins(p, src, nil)
	if err != nil {
		t.Fatalf("WritePins: %v", err)
	}
	if !changed {
		t.Fatalf("expected changed=true")
	}
	result := mustRead(t, p)

	// Every byte outside the pin region is identical. Both sides are excised so
	// the comparison isolates the non-pin bytes regardless of whether the example
	// already carried a frozen pin section of its own.
	if got, want := excisePinSection(result), excisePinSection(original); !bytes.Equal(got, want) {
		t.Fatalf("bytes outside the pin section drifted from the original recipe at line(s) %v", differingLines(got, want))
	}
	// Only a sourceDigests block was added; no artifactDigests appeared.
	if !bytes.Contains(result, []byte("\n  sourceDigests:\n")) {
		t.Errorf("sourceDigests block not added")
	}
	if bytes.Contains(result, []byte("artifactDigests")) {
		t.Errorf("artifactDigests must not appear when no artifact pins are frozen")
	}
	// The folded description, the literal block, and the 2-space indentation
	// survive verbatim.
	for _, frag := range []string{
		"  description: >\n",
		"    Aggregate into source resolution.\n",
		"  valuesMapping: |\n",
		"    ## ── Image overrides",
		"  capability: helm\n",
		"    - ReadWriteOnce\n",
	} {
		if !bytes.Contains(result, []byte(frag)) {
			t.Errorf("fragment not preserved verbatim: %q", frag)
		}
	}
}
