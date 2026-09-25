// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package recipepin_test

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

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

// mixedRecipe carries the mixed content an authored document may hold: 2-space
// indentation, a folded (>) scalar in metadata, a comment, quoted scalars, and a
// literal (|) block whose body contains an internal blank line. None of it is
// the pin writer's business, and the assertions below are that none of it moves.
const mixedRecipe = `apiVersion: aggrega.ai/v1alpha1
kind: Recipe
metadata:
  name: mixed
  version: "1.0.0"
  description: >
    A folded description that spans several
    words and must survive verbatim.
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

func writeRecipe(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "recipe.yaml")
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	return p
}

func mustRead(t *testing.T, p string) []byte {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	return b
}

func sampleSourcePin(dig string) []types.SourceDigest {
	return []types.SourceDigest{{Kind: "helm-https-chart", URL: "https://x/c.tgz", Version: "1.0.0", Digest: dig}}
}

func sampleArtifactPin(dig string) []types.ArtifactDigest {
	return []types.ArtifactDigest{{Name: "extra.yaml", Digest: dig}}
}

// TestLockPathIsTheEntitySibling pins the placement the whole design rests on:
// the lock is found by the directory the entity sits in, under one name, so a
// reader who knows the entity's path knows the lock's without knowing its kind.
func TestLockPathIsTheEntitySibling(t *testing.T) {
	got := recipepin.LockPath(filepath.Join("catalog", "recipe", "helm", "app", "1.0.0", "recipe.yaml"))
	want := filepath.Join("catalog", "recipe", "helm", "app", "1.0.0", recipepin.LockFileName)
	if got != want {
		t.Errorf("LockPath = %q; want %q", got, want)
	}
}

// TestWriteLockRecordsEveryPinClass drives the writer over every class at once
// and reads the result back through the reader, so the round trip is asserted
// through the pair rather than against a hand-spelled document. The reader
// validates the file against its published schema before it decodes it, so a
// class the schema did not admit would fail here rather than decode as nothing.
func TestWriteLockRecordsEveryPinClass(t *testing.T) {
	p := writeRecipe(t, bareRecipe)
	src := sampleSourcePin("sha256:aaa")
	art := sampleArtifactPin("sha256:bbb")
	local := []types.LocalSourceDigest{{Path: "chart/templates/cm.yaml", Digest: "sha256:ccc"}, {Path: "chart/Chart.yaml", Digest: "sha256:ddd"}}

	changed, err := recipepin.WriteLock(p, recipepin.Pins{SourceDigests: src, ArtifactDigests: art, LocalSourceDigests: local})
	if err != nil {
		t.Fatalf("WriteLock: %v", err)
	}
	if !changed {
		t.Fatalf("expected changed=true on the first write")
	}

	lock, present, err := recipepin.ReadLock(p)
	if err != nil {
		t.Fatalf("ReadLock: %v", err)
	}
	if !present {
		t.Fatalf("ReadLock reported no lock after one was written")
	}
	if len(lock.SourceDigests) != 1 || lock.SourceDigests[0].Digest != "sha256:aaa" {
		t.Errorf("source pins round-tripped as %+v", lock.SourceDigests)
	}
	if len(lock.ArtifactDigests) != 1 || lock.ArtifactDigests[0].Digest != "sha256:bbb" {
		t.Errorf("artifact pins round-tripped as %+v", lock.ArtifactDigests)
	}
	// Handed over out of order, recorded in path order.
	wantLocal := []types.LocalSourceDigest{{Path: "chart/Chart.yaml", Digest: "sha256:ddd"}, {Path: "chart/templates/cm.yaml", Digest: "sha256:ccc"}}
	if !reflect.DeepEqual(lock.LocalSourceDigests, wantLocal) {
		t.Errorf("local source pins round-tripped as %+v; want %+v", lock.LocalSourceDigests, wantLocal)
	}
	if lock.Version != 1 {
		t.Errorf("lock version = %d; want 1", lock.Version)
	}
}

// TestWriteLockNeverWritesTheAuthoredDocument is the executable form of the
// ruling this file exists to serve: whatever the pins do — appear, move, or go
// away — the entity document's bytes do not change.
//
// It is asserted over the three transitions rather than over one, because the
// three reach the writer by different paths and a document write could be
// introduced on any of them alone.
func TestWriteLockNeverWritesTheAuthoredDocument(t *testing.T) {
	p := writeRecipe(t, mixedRecipe)
	original := []byte(mixedRecipe)

	for _, step := range []struct {
		name string
		src  []types.SourceDigest
		art  []types.ArtifactDigest
	}{
		{name: "pins appear", src: sampleSourcePin("sha256:aaa"), art: sampleArtifactPin("sha256:bbb")},
		{name: "a pin moves", src: sampleSourcePin("sha256:CHANGED"), art: sampleArtifactPin("sha256:bbb")},
		{name: "pins go away"},
	} {
		if _, err := recipepin.WriteLock(p, recipepin.Pins{SourceDigests: step.src, ArtifactDigests: step.art}); err != nil {
			t.Fatalf("%s: WriteLock: %v", step.name, err)
		}
		if got := mustRead(t, p); !bytes.Equal(got, original) {
			t.Fatalf("%s: the authored document moved:\n--- want ---\n%q\n--- got ---\n%q", step.name, original, got)
		}
	}
}

// TestWriteLockLeavesARealRecipeUntouched repeats the claim above against a
// real-shaped published recipe rather than a synthetic one. The fixture supplies
// shape; the property — a pin write touches no byte of the entity document —
// holds for any document, so the fixture stands in for none in particular.
func TestWriteLockLeavesARealRecipeUntouched(t *testing.T) {
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
	if _, werr := recipepin.WriteLock(p, recipepin.Pins{SourceDigests: src}); werr != nil {
		t.Fatalf("WriteLock: %v", werr)
	}
	if got := mustRead(t, p); !bytes.Equal(got, original) {
		t.Fatalf("the published recipe's bytes moved")
	}

	lockBody := mustRead(t, recipepin.LockPath(p))
	if !bytes.Contains(lockBody, []byte("manifestDigest: sha256:cafebabe")) {
		t.Errorf("the OCI manifest digest did not reach the lock:\n%s", lockBody)
	}
	if bytes.Contains(lockBody, []byte("artifactDigests")) {
		t.Errorf("an empty artifact list must not be emitted:\n%s", lockBody)
	}
}

// TestWriteLockIsIdempotent asserts the file is left byte-identical when the
// declarations have not moved. It is what lets an author keep the lock under
// version control: a re-run that churned the file would put a diff in front of
// them that says nothing about their inputs.
func TestWriteLockIsIdempotent(t *testing.T) {
	p := writeRecipe(t, bareRecipe)
	src := sampleSourcePin("sha256:aaa")
	art := sampleArtifactPin("sha256:bbb")

	if _, err := recipepin.WriteLock(p, recipepin.Pins{SourceDigests: src, ArtifactDigests: art}); err != nil {
		t.Fatalf("first WriteLock: %v", err)
	}
	first := mustRead(t, recipepin.LockPath(p))

	// The same pins, handed over in the opposite order, to prove the canonical
	// sort rather than the caller's ordering is what the file records.
	permutedSrc := append(sampleSourcePin("sha256:aaa"), types.SourceDigest{
		Kind: "git-repository", URL: "https://a/b", Version: "sha", Digest: "sha256:zzz",
	})
	if _, err := recipepin.WriteLock(p, recipepin.Pins{SourceDigests: permutedSrc, ArtifactDigests: art}); err != nil {
		t.Fatalf("widening WriteLock: %v", err)
	}
	reordered := []types.SourceDigest{
		{Kind: "git-repository", URL: "https://a/b", Version: "sha", Digest: "sha256:zzz"},
		sampleSourcePin("sha256:aaa")[0],
	}
	changed, err := recipepin.WriteLock(p, recipepin.Pins{SourceDigests: reordered, ArtifactDigests: art})
	if err != nil {
		t.Fatalf("re-ordered WriteLock: %v", err)
	}
	if changed {
		t.Errorf("a re-ordered but equal pin set reported a change")
	}

	if _, err := recipepin.WriteLock(p, recipepin.Pins{SourceDigests: src, ArtifactDigests: art}); err != nil {
		t.Fatalf("narrowing WriteLock: %v", err)
	}
	if got := mustRead(t, recipepin.LockPath(p)); !bytes.Equal(got, first) {
		t.Errorf("returning to the original pins did not reproduce the original bytes:\n--- want ---\n%s\n--- got ---\n%s", first, got)
	}
}

// TestWriteLockRemovesTheFileWhenNothingIsPinned asserts an entity that stops
// reaching outside itself stops carrying a lock, rather than carrying one that
// records nothing.
func TestWriteLockRemovesTheFileWhenNothingIsPinned(t *testing.T) {
	p := writeRecipe(t, bareRecipe)
	if _, err := recipepin.WriteLock(p, recipepin.Pins{SourceDigests: sampleSourcePin("sha256:aaa")}); err != nil {
		t.Fatalf("WriteLock: %v", err)
	}
	changed, err := recipepin.WriteLock(p, recipepin.Pins{})
	if err != nil {
		t.Fatalf("clearing WriteLock: %v", err)
	}
	if !changed {
		t.Errorf("clearing a present lock reported no change")
	}
	if _, serr := os.Stat(recipepin.LockPath(p)); !os.IsNotExist(serr) {
		t.Errorf("the lock file survived a clear: %v", serr)
	}
	// And clearing again is a no-op rather than an error.
	changed, err = recipepin.WriteLock(p, recipepin.Pins{})
	if err != nil {
		t.Fatalf("second clearing WriteLock: %v", err)
	}
	if changed {
		t.Errorf("clearing an absent lock reported a change")
	}
}

// TestReadLockAbsentIsNotAFailure holds the distinction the reader's flag
// exists for: nothing recorded is a different answer from something that would
// not read, and only the second is a failure.
func TestReadLockAbsentIsNotAFailure(t *testing.T) {
	p := writeRecipe(t, bareRecipe)
	lock, present, err := recipepin.ReadLock(p)
	if err != nil {
		t.Fatalf("ReadLock over a directory with no lock: %v", err)
	}
	if present {
		t.Errorf("ReadLock reported a lock where none was written")
	}
	if len(lock.SourceDigests) != 0 || len(lock.ArtifactDigests) != 0 {
		t.Errorf("an absent lock yielded pins: %+v", lock)
	}
}

// TestReadLockRefusesWhatItsSchemaRejects is the falsifiability half of the
// reader: a lock whose document the schema refuses is an error rather than a
// silently empty pin set. The three rows are the three shapes that would
// otherwise decode to something and lose a pin on the way — a key with no field
// behind it, a version this package does not speak, and a row missing the digest
// that is the whole point of the row.
func TestReadLockRefusesWhatItsSchemaRejects(t *testing.T) {
	for _, tc := range []struct {
		name string
		body string
	}{
		{
			name: "a key the shape has no field for",
			body: "version: 1\nsourceDigest:\n  - digest: sha256:aaa\n",
		},
		{
			name: "a version this package does not speak",
			body: "version: 2\n",
		},
		{
			name: "a source row with no digest",
			body: "version: 1\nsourceDigests:\n  - kind: helm-https-chart\n    url: https://x/c.tgz\n    version: 1.0.0\n",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := writeRecipe(t, bareRecipe)
			if err := os.WriteFile(recipepin.LockPath(p), []byte(tc.body), 0o644); err != nil {
				t.Fatalf("plant the lock: %v", err)
			}
			lock, present, err := recipepin.ReadLock(p)
			if err == nil {
				t.Fatalf("ReadLock admitted a refused document: present=%v lock=%+v", present, lock)
			}
			if !strings.Contains(err.Error(), recipepin.LockFileName) {
				t.Errorf("the failure does not name the file that would not read: %v", err)
			}
		})
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
		{Kind: "git-repository", URL: "g", ModuleSubdir: "modules/alpha", Version: "sha", Digest: "da"},
		{Kind: "git-repository", URL: "g", ModuleSubdir: "modules/beta", Version: "sha", Digest: "db"},
	}
	if p, ok := recipepin.MatchSourcePin(gitSrc, "git-repository", "g", "", "modules/beta", "sha"); !ok || p.Digest != "db" {
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
