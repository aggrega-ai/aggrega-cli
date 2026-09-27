// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package catalog

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
	"oras.land/oras-go/v2/content/memory"

	"github.com/aggrega-ai/aggrega-cli/internal/sourceref"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/sourcecache"
)

// layoutCacheDir is the cache directory every test below resolves against.
const layoutCacheDir = ".aggrega/cache"

// writeTree writes files (relative path → body) under dir.
func writeTree(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for rel, body := range files {
		p := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// publishedArtifact returns what a publication of the entity at entityPath
// carries, and its media type: the files the entity declares, read through the
// same function a publication reads them through, packed by the real bundler.
func publishedArtifact(t *testing.T, kind, entityPath string) ([]byte, string) {
	t.Helper()
	data, err := os.ReadFile(entityPath)
	if err != nil {
		t.Fatal(err)
	}
	declared, err := DeclaredFilesOf(data, entityPath)
	if err != nil {
		t.Fatalf("DeclaredFilesOf: %v", err)
	}
	blob, mediaType, err := oci.Bundle(kind, entityPath, declared.Files())
	if err != nil {
		t.Fatalf("Bundle: %v", err)
	}
	return blob, mediaType
}

// seedPinnedSlot publishes the entity at entityPath as a publication would,
// pushes it to an in-memory registry and pulls it back, so the manifest the
// verified read demands is a real one, then writes the cache slot a registry
// pull writes. It returns the pin a lockfile would record for it.
func seedPinnedSlot(t *testing.T, root, kind, entityPath, slotURI, version string) Pin {
	t.Helper()
	blob, mediaType := publishedArtifact(t, kind, entityPath)
	return seedPinnedArtifact(t, root, blob, mediaType, slotURI, version)
}

// seedPinnedArtifact is seedPinnedSlot over bytes the caller packed itself — the
// shape of an artifact some other client published, which no publication through
// this product would have produced.
func seedPinnedArtifact(t *testing.T, root string, blob []byte, mediaType, slotURI, version string) Pin {
	t.Helper()
	ctx := context.Background()
	store := memory.New()
	if _, err := oci.Push(ctx, store, version, mediaType, blob, nil); err != nil {
		t.Fatalf("oci.Push: %v", err)
	}
	pulled, err := oci.Pull(ctx, store, version)
	if err != nil {
		t.Fatalf("oci.Pull: %v", err)
	}
	if err := writeEntityToCacheSlot(root, layoutCacheDir, slotURI, version, pulled); err != nil {
		t.Fatalf("writeEntityToCacheSlot: %v", err)
	}
	return Pin{ResolvedVersion: version, Digest: rowDigestOf(pulled.Bytes), ManifestDigest: pulled.Digest}
}

// slotTree returns the tree a pinned slot's files are laid out in. The tree is
// named by the bytes the slot holds — the digest of its blob, as the layout itself
// derives it — and not by the pin: a pin records what the publication carries,
// entry by entry, while the tree is keyed by the one artifact it was unpacked
// from, so an artifact replaced under a reader gets a tree of its own.
func slotTree(t *testing.T, slot string) string {
	t.Helper()
	blob, err := os.ReadFile(filepath.Join(slot, "blob"))
	if err != nil {
		t.Fatalf("read the slot's blob: %v", err)
	}
	return sourcecache.ExtractedTree(slot, contentDigest(blob))
}

// carriedChart is the local chart a Recipe or FeatureRecipe carries beside it.
var carriedChart = map[string]string{
	"chart/Chart.yaml":            "apiVersion: v2\nname: carried\nversion: 0.1.0\n",
	"chart/templates/banner.yaml": "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: carried\ndata:\n  carried-by: the-published-chart\n",
}

const carriedRecipeDoc = `apiVersion: aggrega.ai/v1alpha1
kind: Recipe
metadata:
  name: carried
  version: 1.0.0
spec:
  capability: helm
  implements: acme/compute/carried:1.0.0
  source: ./chart
  valuesMapping: |
    {"k": "v"}
`

const carriedFeatureRecipeDoc = `apiVersion: aggrega.ai/v1alpha1
kind: FeatureRecipe
metadata:
  name: carried
  version: 1.0.0
spec:
  capability: helm
  implements: acme/banner:1.0.0
  source: ./chart
  valuesMapping: |
    text: "v"
`

// publishedRecipeSource resolves the pinned entity of kind at uri through the
// lazy lookup and returns the source its parse anchored.
func publishedRecipeSource(t *testing.T, ll *LazyLookup, kind, uri string) (string, bool) {
	t.Helper()
	var source any
	switch kind {
	case "Recipe":
		r, ok := ll.RecipeByURI(uri)
		if !ok {
			return "", false
		}
		source = r.Spec.Source
	case "FeatureRecipe":
		r, ok := ll.FeatureRecipeByURI(uri)
		if !ok {
			return "", false
		}
		source = r.Spec.Source
	default:
		t.Fatalf("no accessor for kind %q", kind)
	}
	s, isString := source.(string)
	if !isString {
		t.Fatalf("anchored source is %T; want the local path string", source)
	}
	return s, true
}

// TestPinnedEntityReadsTheFilesItsArchiveCarries drives the registry tier for
// both kinds whose archive carries a local source: the pinned slot is laid out
// from its verified artifact, and the parse is anchored at the document's place
// in that layout — so the source names the chart the publication carried, inside
// the slot, rather than a directory derived from the pin's label.
func TestPinnedEntityReadsTheFilesItsArchiveCarries(t *testing.T) {
	rows := []struct {
		name, kind, file, doc, uri string
	}{
		{"recipe", "Recipe", "recipe.yaml", carriedRecipeDoc, "helm/carried:1.0.0"},
		{"feature recipe", "FeatureRecipe", "recipe.yaml", carriedFeatureRecipeDoc, "helm/carried:1.0.0"},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			pub := t.TempDir()
			writeTree(t, pub, carriedChart)
			writeTree(t, pub, map[string]string{row.file: row.doc})
			root := t.TempDir()
			pin := seedPinnedSlot(t, root, row.kind, filepath.Join(pub, row.file), row.uri, "1.0.0")

			ll := NewLazyLookup(root, layoutCacheDir, nil, nil)
			ll.SetPins(map[string]Pin{row.uri: pin})
			got, ok := publishedRecipeSource(t, ll, row.kind, row.uri)
			if !ok {
				t.Fatalf("the pinned %s did not resolve; skips = %v", row.kind, ll.Skips())
			}

			slot := mustCachePath(root, layoutCacheDir, row.uri, "1.0.0")
			want := filepath.Join(slotTree(t, slot), "chart")
			if got != want {
				t.Fatalf("source anchored at %q; want the chart inside the slot's layout %q", got, want)
			}
			carried, err := os.ReadFile(filepath.Join(want, "templates", "banner.yaml"))
			if err != nil {
				t.Fatalf("the layout does not hold the carried chart: %v", err)
			}
			if !bytes.Equal(carried, []byte(carriedChart["chart/templates/banner.yaml"])) {
				t.Errorf("the laid-out chart differs from the one published:\n%s", carried)
			}
		})
	}
}

// TestPinnedRecipeRenderDocumentIsItsLayoutDocument pins the document the render
// is handed for a pulled Recipe or FeatureRecipe: its place in the slot's layout,
// which is what a source written as a template is anchored at once it has been
// evaluated. It also pins what that document is NOT: it is never reported as a
// file anybody authored, and never as a writable home the pin step would write a
// lock beside.
func TestPinnedRecipeRenderDocumentIsItsLayoutDocument(t *testing.T) {
	rows := []struct {
		name, kind, doc string
	}{
		{"recipe", "Recipe", carriedRecipeDoc},
		{"feature recipe", "FeatureRecipe", carriedFeatureRecipeDoc},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			const uri = "helm/carried:1.0.0"
			pub := t.TempDir()
			writeTree(t, pub, carriedChart)
			writeTree(t, pub, map[string]string{"recipe.yaml": row.doc})
			root := t.TempDir()
			pin := seedPinnedSlot(t, root, row.kind, filepath.Join(pub, "recipe.yaml"), uri, "1.0.0")
			want := filepath.Join(slotTree(t, mustCachePath(root, layoutCacheDir, uri, "1.0.0")), "recipe.yaml")

			ll := NewLazyLookup(root, layoutCacheDir, nil, nil)
			ll.SetPins(map[string]Pin{uri: pin})
			lookup := ll.RecipeLookupWithManifest(nil)
			var got string
			var ok bool
			switch row.kind {
			case "Recipe":
				r, found := ll.RecipeByURI(uri)
				if !found {
					t.Fatalf("the pinned Recipe did not resolve; skips = %v", ll.Skips())
				}
				got, ok = lookup.RecipeDocument(&r)
			case "FeatureRecipe":
				fr, found := ll.FeatureRecipeByURI(uri)
				if !found {
					t.Fatalf("the pinned FeatureRecipe did not resolve; skips = %v", ll.Skips())
				}
				got, ok = lookup.FeatureRecipeDocument(&fr)
			}
			if !ok || got != want {
				t.Errorf("render document = (%q, %v); want the layout document %q", got, ok, want)
			}
			if p, reported := ll.LocalEntityPath(row.kind, "helm", "carried", "1.0.0"); reported {
				t.Errorf("a pulled %s was reported as authored at %q; its slot is not a file anybody authored", row.kind, p)
			}
			writablePath := ll.LocalRecipePath
			if row.kind == "FeatureRecipe" {
				writablePath = ll.LocalFeatureRecipePath
			}
			if p, writable := writablePath("helm", "carried", "1.0.0"); writable {
				t.Errorf("a pulled %s has a writable home %q; the pin step would write beside a cache slot", row.kind, p)
			}
		})
	}
}

// TestPinnedSlotLayoutIsRebuiltFromTheBlobOnEveryRead pins that nothing in a
// slot's layout is trusted on its own: a file changed, a file added and the
// whole tree removed are each answered by rebuilding it from the verified blob,
// byte for byte, at the next read.
func TestPinnedSlotLayoutIsRebuiltFromTheBlobOnEveryRead(t *testing.T) {
	const uri = "helm/carried:1.0.0"
	pub := t.TempDir()
	writeTree(t, pub, carriedChart)
	writeTree(t, pub, map[string]string{"recipe.yaml": carriedRecipeDoc})
	root := t.TempDir()
	pin := seedPinnedSlot(t, root, "Recipe", filepath.Join(pub, "recipe.yaml"), uri, "1.0.0")
	slot := mustCachePath(root, layoutCacheDir, uri, "1.0.0")
	template := filepath.Join(slotTree(t, slot), "chart", "templates", "banner.yaml")
	stray := filepath.Join(slotTree(t, slot), "chart", "templates", "stray.yaml")

	read := func() {
		t.Helper()
		ll := NewLazyLookup(root, layoutCacheDir, nil, nil)
		ll.SetPins(map[string]Pin{uri: pin})
		if _, ok := ll.RecipeByURI(uri); !ok {
			t.Fatalf("the pinned Recipe did not resolve; skips = %v", ll.Skips())
		}
	}
	assertOriginal := func(stage string) {
		t.Helper()
		got, err := os.ReadFile(template)
		if err != nil {
			t.Fatalf("%s: the carried template is missing from the layout: %v", stage, err)
		}
		if !bytes.Equal(got, []byte(carriedChart["chart/templates/banner.yaml"])) {
			t.Errorf("%s: the layout was not rebuilt from the blob; template reads:\n%s", stage, got)
		}
		if _, err := os.Stat(stray); !os.IsNotExist(err) {
			t.Errorf("%s: a file added to the layout by hand survived the read (stat err = %v)", stage, err)
		}
	}

	read()
	assertOriginal("first read")

	if err := os.WriteFile(template, []byte("changed in the cache\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(stray, []byte("added in the cache\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	read()
	assertOriginal("after the layout was edited")

	if err := os.RemoveAll(sourcecache.ExtractedDir(slot)); err != nil {
		t.Fatal(err)
	}
	read()
	assertOriginal("after the layout was deleted, the blob kept")
}

// TestPinnedSlotWhoseBlobFailsVerificationIsNeverLaidOut replaces a pinned
// slot's blob with a different, well-formed archive. The verified read refuses it
// against the pin and the manifest, so the entity does not resolve and no layout
// is ever written from the substituted bytes.
func TestPinnedSlotWhoseBlobFailsVerificationIsNeverLaidOut(t *testing.T) {
	const uri = "helm/carried:1.0.0"
	pub := t.TempDir()
	writeTree(t, pub, carriedChart)
	writeTree(t, pub, map[string]string{"recipe.yaml": carriedRecipeDoc})
	root := t.TempDir()
	pin := seedPinnedSlot(t, root, "Recipe", filepath.Join(pub, "recipe.yaml"), uri, "1.0.0")
	slot := mustCachePath(root, layoutCacheDir, uri, "1.0.0")

	substitute := t.TempDir()
	writeTree(t, substitute, map[string]string{
		"recipe.yaml":                 carriedRecipeDoc,
		"chart/Chart.yaml":            carriedChart["chart/Chart.yaml"],
		"chart/templates/banner.yaml": "substituted\n",
	})
	forged, _ := publishedArtifact(t, "Recipe", filepath.Join(substitute, "recipe.yaml"))
	if err := os.WriteFile(filepath.Join(slot, "blob"), forged, 0o644); err != nil {
		t.Fatal(err)
	}

	ll := NewLazyLookup(root, layoutCacheDir, nil, nil)
	ll.SetPins(map[string]Pin{uri: pin})
	if _, ok := ll.RecipeByURI(uri); ok {
		t.Fatal("a Recipe whose blob no longer matches its pin resolved")
	}
	if _, err := os.Stat(sourcecache.ExtractedDir(slot)); !os.IsNotExist(err) {
		t.Errorf("the substituted blob was laid out (stat err = %v); an artifact that fails verification must never be unpacked", err)
	}
}

// TestPinnedRecipeSourceLeavingItsDirectoryIsRefused publishes, through the
// bundler alone — the one route by which such a Recipe reaches a registry, since
// a publication through the parse refuses it — a Recipe whose source climbs out
// of its own directory, and plants a real directory exactly where the climb lands
// inside the slot. The containment rule holds over the layout unchanged: the
// Recipe is refused under the pinned reference with the named condition, and the
// planted directory is never taken as its source.
func TestPinnedRecipeSourceLeavingItsDirectoryIsRefused(t *testing.T) {
	const uri = "helm/escape:1.0.0"
	pub := t.TempDir()
	writeTree(t, pub, map[string]string{"recipe.yaml": `apiVersion: aggrega.ai/v1alpha1
kind: Recipe
metadata:
  name: escape
  version: 1.0.0
spec:
  capability: helm
  implements: acme/compute/carried:1.0.0
  source: ../outside-chart
  valuesMapping: |
    {"k": "v"}
`})
	root := t.TempDir()
	blob, mediaType, err := oci.Bundle("Recipe", filepath.Join(pub, "recipe.yaml"), nil)
	if err != nil {
		t.Fatalf("Bundle: %v", err)
	}
	pin := seedPinnedArtifact(t, root, blob, mediaType, uri, "1.0.0")
	slot := mustCachePath(root, layoutCacheDir, uri, "1.0.0")
	writeTree(t, slot, map[string]string{"outside-chart/Chart.yaml": "apiVersion: v2\nname: outside\nversion: 0.1.0\n"})

	ll := NewLazyLookup(root, layoutCacheDir, nil, nil)
	ll.SetPins(map[string]Pin{uri: pin})
	if _, ok := ll.RecipeByURI(uri); ok {
		t.Fatal("a Recipe whose source leaves its own directory resolved from its slot")
	}
	reason, recorded := ll.Skips()["lockfile-pinned:"+uri]
	if !recorded {
		t.Fatalf("the refusal was not recorded under the pinned reference; skips = %v", ll.Skips())
	}
	if !errors.Is(reason, sourceref.ErrContentOutsideEntityDirectory()) {
		t.Errorf("the refusal is not the containment condition:\n%v", reason)
	}
}

// TestPulledComponentReadsTheSchemaItsArchiveCarries pins where a pulled
// Component's schema import is read from. A Component whose schema is imported
// from a file publishes that file beside its document, so its slot's layout holds
// both, and the import resolves inside the layout to the contract that was
// published. The file planted in the working directory, at the path the import
// would reach if it were anchored at the Component's coordinate, is the needle:
// it must never be taken as the published contract.
func TestPulledComponentReadsTheSchemaItsArchiveCarries(t *testing.T) {
	const uri = "acme/compute/imported:1.0.0"
	pub := t.TempDir()
	const doc = `apiVersion: aggrega.ai/v1alpha1
kind: Component
metadata:
  name: acme/compute/imported
  version: 1.0.0
  labels:
    component.aggrega.ai/type: server
spec:
  schema:
    $import: ./schema.yaml
`
	writeTree(t, pub, map[string]string{
		"component.yaml": doc,
		"schema.yaml":    "values:\n  replicas_hint: { type: integer, default: 1 }\n",
	})
	root := t.TempDir()
	pin := seedPinnedSlot(t, root, "Component", filepath.Join(pub, "component.yaml"), uri, "1.0.0")
	slot := mustCachePath(root, layoutCacheDir, uri, "1.0.0")

	cwd := t.TempDir()
	t.Chdir(cwd)
	writeTree(t, cwd, map[string]string{
		"acme/compute/schema.yaml": "values:\n  planted_by_the_consumer: { type: string, default: needle }\n",
	})

	ll := NewLazyLookup(root, layoutCacheDir, nil, nil)
	ll.SetPins(map[string]Pin{uri: pin})
	c, ok := ll.Component(uri)
	if !ok {
		t.Fatalf("the pinned Component did not resolve from the archive carrying its schema; skips = %v", ll.Skips())
	}
	contract, err := yaml.Marshal(&c.Spec.Schema)
	if err != nil {
		t.Fatalf("marshal the resolved schema: %v", err)
	}
	if !strings.Contains(string(contract), "replicas_hint") {
		t.Errorf("the resolved contract is not the published one:\n%s", contract)
	}
	if strings.Contains(string(contract), "planted_by_the_consumer") {
		t.Errorf("the resolved contract was read from the consumer's working directory:\n%s", contract)
	}
	laid, err := os.ReadFile(filepath.Join(slotTree(t, slot), "component.yaml"))
	if err != nil {
		t.Fatalf("the slot's layout does not hold the published document under its own name: %v", err)
	}
	if string(laid) != doc {
		t.Errorf("the laid-out document differs from the one published:\n%s", laid)
	}
	if _, err := os.Stat(filepath.Join(slotTree(t, slot), "schema.yaml")); err != nil {
		t.Errorf("the layout does not hold the schema file the publication carried: %v", err)
	}
}

// TestPulledComponentResolvesFromItsLaidOutDocument is the positive leg: a
// Component that imports nothing resolves from its slot, and the slot's layout
// holds its document under the single-document name.
func TestPulledComponentResolvesFromItsLaidOutDocument(t *testing.T) {
	const uri = "acme/compute/plain:1.0.0"
	pub := t.TempDir()
	writeTree(t, pub, map[string]string{"component.yaml": `apiVersion: aggrega.ai/v1alpha1
kind: Component
metadata:
  name: acme/compute/plain
  version: 1.0.0
  labels:
    component.aggrega.ai/type: server
spec:
  schema:
    values:
      greeting: { type: string, default: hello }
`})
	root := t.TempDir()
	pin := seedPinnedSlot(t, root, "Component", filepath.Join(pub, "component.yaml"), uri, "1.0.0")
	slot := mustCachePath(root, layoutCacheDir, uri, "1.0.0")

	ll := NewLazyLookup(root, layoutCacheDir, nil, nil)
	ll.SetPins(map[string]Pin{uri: pin})
	if _, ok := ll.Component(uri); !ok {
		t.Fatalf("the pinned Component did not resolve from its slot; skips = %v", ll.Skips())
	}
	if _, err := os.Stat(filepath.Join(slotTree(t, slot), oci.SingleDocumentFileName)); err != nil {
		t.Errorf("the slot's layout does not hold the document: %v", err)
	}
}
