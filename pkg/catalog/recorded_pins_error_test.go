// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

// Tests for the load-side half of the entity lock: a lock that will not read
// stops the load rather than being read as an entity with nothing pinned.
//
// The reader's own refusal is covered where the reader lives. What is covered
// here is the CALLER propagating that refusal, which is a different failure:
// a reader that refuses correctly and a caller that swallows the refusal
// produce a load reporting success with nothing filed for the entity, and every
// comparison the pins exist for then silently does not happen — which on a
// build reads exactly like inputs that had not moved.

package catalog

import (
	"path"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/digest"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/recipepin"
)

// lockRelFor names the lock beside an entity written at rel. It derives the
// sibling the same way the product does rather than spelling the filename a
// second time, so a test fixture cannot keep planting a file at a name the
// reader has stopped looking for.
func lockRelFor(rel string) string {
	return path.Join(path.Dir(rel), recipepin.LockFileName)
}

// loadedEntityExists reports whether the loaded catalog holds the pin-bearing
// entity the row names, for whichever kind that is. The entity value itself
// carries no pins to read — an entity document declares none — so what a row
// asserts about an entity is that it loaded at all; the pins are read from the
// lookup instead.
func loadedEntityExists(t *testing.T, cat *Catalog, kind, uri string) bool {
	t.Helper()
	switch kind {
	case "Recipe":
		_, ok := cat.RecipeByURI(uri)
		return ok
	case "FeatureRecipe":
		_, ok := cat.FeatureRecipeByURI(uri)
		return ok
	default:
		t.Fatalf("loadedEntityExists does not know the kind %q", kind)
		return false
	}
}

const lockBearingRecipeYAML = `apiVersion: aggrega.ai/v1beta1
kind: Recipe
metadata:
  name: pinned-recipe
  version: "1.0.0"
spec:
  capability: helm
  implements: core/api-server:1.0.0
  source: https://example.invalid/c.tgz
`

const lockBearingFeatureRecipeYAML = `apiVersion: aggrega.ai/v1beta1
kind: FeatureRecipe
metadata:
  name: pinned-feature-recipe
  version: "1.0.0"
spec:
  capability: helm
  implements: core/sidecar
  source: https://example.invalid/c.tgz
`

// readableLockSourceDigest and readableLockArtifactDigest are the two pins
// readableLock records, each a real content digest — the one spelling the lock's
// schema admits.
var (
	readableLockSourceDigest   = digest.ContentDigest([]byte("aaa"))
	readableLockArtifactDigest = digest.ContentDigest([]byte("bbb"))
)

// readableLock is a lock the published schema admits. It is the positive
// control for every row below: without it a refusal could equally mean the
// load rejects any entity with a lock beside it, and the negative rows would
// pass for the wrong reason.
var readableLock = `version: 1
sourceDigests:
  - kind: helm-https-chart
    url: https://example.invalid/c.tgz
    version: "1.0.0"
    digest: ` + readableLockSourceDigest + `
artifactDigests:
  - name: extra.yaml
    digest: ` + readableLockArtifactDigest + `
`

// unreadableLocks are documents the lock's published schema refuses, one fault
// each so a row keeps covering its own fault when an earlier gate changes.
var unreadableLocks = map[string]string{
	"a version this package does not speak": "version: 2\n",
	"a key the shape has no field for":      "version: 1\nsourceDigest:\n  - digest: sha256:aaa\n",
	"a source row with no digest":           "version: 1\nsourceDigests:\n  - kind: helm-https-chart\n    url: https://example.invalid/c.tgz\n    version: \"1.0.0\"\n",
	"not a document at all":                 "\tthis is not yaml: [\n",
}

// pinBearingKinds is the grid's first axis. Both pin-bearing kinds route
// through one body, so a guard proved on one is not proved on the other: the
// call is written twice and only a per-kind row reports a call site that was
// left behind.
var pinBearingKinds = map[string]struct {
	rel  string
	body string
	uri  string
}{
	"Recipe": {
		rel:  "recipe/helm/pinned-recipe/recipe.yaml",
		body: lockBearingRecipeYAML,
		uri:  "helm/pinned-recipe:1.0.0",
	},
	"FeatureRecipe": {
		rel:  "featurerecipe/helm/pinned-feature-recipe/featurerecipe.yaml",
		body: lockBearingFeatureRecipeYAML,
		uri:  "helm/pinned-feature-recipe:1.0.0",
	},
}

// TestAnUnreadableLockStopsTheLoadRatherThanReadingTheEntityAsUnpinned is the
// caller-side assertion. Each row plants one refused lock beside one entity and
// requires the load to fail naming the file that would not read — never to
// succeed with the entity's pins silently empty.
func TestAnUnreadableLockStopsTheLoadRatherThanReadingTheEntityAsUnpinned(t *testing.T) {
	t.Parallel()
	for kind, k := range pinBearingKinds {
		kind, k := kind, k
		for name, body := range unreadableLocks {
			name, body := name, body
			t.Run(kind+"/"+name, func(t *testing.T) {
				t.Parallel()
				dir := t.TempDir()
				writeCatalogFile(t, dir, k.rel, k.body)
				writeCatalogFile(t, dir, lockRelFor(k.rel), body)

				cat, err := LoadAll("", dir)
				if err == nil {
					t.Fatalf("%s: the load admitted a lock that will not read; "+
						"the entity would be built against nothing and the build would report agreement (catalog=%+v)", kind, cat != nil)
				}
				// Read the whole chain, not the outermost frame: a frame
				// renders itself alone by design, so asking err.Error() here
				// would report the walk's own description and drop the frame
				// that names the file's actual fault.
				chain := cerrors.CauseChain(err)
				if !strings.Contains(chain, recipepin.LockFileName) {
					t.Errorf("%s: the failure does not name the file that would not read: %s", kind, chain)
				}
			})
		}
	}
}

// TestAReadableLockLeavesTheEntityLoadable is the positive control for the rows
// above: without it a refusal could equally mean the load rejects any entity
// with a lock beside it, and every negative row would pass for the wrong reason.
//
// It asserts loadability alone. What the lock's pins become is asserted by
// TestTheLoadRecordsEachEntitysPinsUnderItsOwnDocument below, which is the only
// place they are observable — an entity document declares no pins, so the loaded
// entity value has none to read.
func TestAReadableLockLeavesTheEntityLoadable(t *testing.T) {
	t.Parallel()
	for kind, k := range pinBearingKinds {
		kind, k := kind, k
		t.Run(kind, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			writeCatalogFile(t, dir, k.rel, k.body)
			writeCatalogFile(t, dir, lockRelFor(k.rel), readableLock)

			cat, err := LoadAll("", dir)
			if err != nil {
				t.Fatalf("%s: a readable lock must not stop the load: %v", kind, err)
			}
			if !loadedEntityExists(t, cat, kind, k.uri) {
				t.Fatalf("%s: the loaded catalog holds nothing at %q", kind, k.uri)
			}
		})
	}
}

// TestTheLoadRecordsEachEntitysPinsUnderItsOwnDocument is the producer half of
// the render's pin comparison: the load files what it settled under the document
// the entity was read from, which is the key the render looks it up by.
//
// It is a separate assertion from the one above rather than a second look at the
// same fact. Pins reaching the ENTITY is what a reader of that entity sees; pins
// reaching the LOOKUP is what the render sees, and the render holds no entity
// value it could read them off. A load that applied the lock correctly and filed
// nothing would satisfy every row above while leaving every comparison the render
// makes with nothing to compare — which reads exactly like inputs that had not
// moved.
//
// The lookup is indexed directly rather than through its accessor, so a failure
// here is attributable to the load filing nothing rather than to the accessor
// answering emptily.
func TestTheLoadRecordsEachEntitysPinsUnderItsOwnDocument(t *testing.T) {
	t.Parallel()
	for kind, k := range pinBearingKinds {
		kind, k := kind, k
		t.Run(kind, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			writeCatalogFile(t, dir, k.rel, k.body)
			writeCatalogFile(t, dir, lockRelFor(k.rel), readableLock)

			cat, err := LoadAll("", dir)
			if err != nil {
				t.Fatalf("%s: a readable lock must not stop the load: %v", kind, err)
			}
			document := filepath.Join(dir, filepath.FromSlash(k.rel))
			recorded := cat.RecordedPins()
			pins, filed := recorded[document]
			if !filed {
				t.Fatalf("%s: the load filed no pins for %q, so the render has no key for this entity "+
					"and every comparison its pins exist for silently does not happen (filed: %d document(s))",
					kind, document, len(recorded))
			}
			if len(pins.SourceDigests) != 1 || pins.SourceDigests[0].Digest != readableLockSourceDigest {
				t.Errorf("%s: the filed source pin is not the recorded one: %+v", kind, pins.SourceDigests)
			}
			if len(pins.ArtifactDigests) != 1 || pins.ArtifactDigests[0].Digest != readableLockArtifactDigest {
				t.Errorf("%s: the filed artifact pin is not the recorded one: %+v", kind, pins.ArtifactDigests)
			}
		})
	}
}

// TestNoLockLeavesTheEntityLoadable separates the two answers the reader's
// found-flag exists for: nothing recorded is not something that would not read,
// and only the second stops a load.
//
// It also states the whole of what an entity with no lock is pinned to, which is
// nothing. There is no second place a pin could come from: the document has no
// surface to declare one on.
func TestNoLockLeavesTheEntityLoadable(t *testing.T) {
	t.Parallel()
	for kind, k := range pinBearingKinds {
		kind, k := kind, k
		t.Run(kind, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			writeCatalogFile(t, dir, k.rel, k.body)

			cat, err := LoadAll("", dir)
			if err != nil {
				t.Fatalf("%s: an entity with no lock beside it must load: %v", kind, err)
			}
			if !loadedEntityExists(t, cat, kind, k.uri) {
				t.Fatalf("%s: the loaded catalog holds nothing at %q", kind, k.uri)
			}
			document := filepath.Join(dir, filepath.FromSlash(k.rel))
			if pins, filed := cat.RecordedPins()[document]; filed && !pins.Empty() {
				t.Errorf("%s: an entity with no lock acquired pins from somewhere: %+v", kind, pins)
			}
		})
	}
}
