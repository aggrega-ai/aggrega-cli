// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Tests that the `$import:` value an author wrote survives a catalog load
// beside the resolved path rather than being consumed by it, for both kinds
// that carry an artifacts surface.
//
// The load runs through LoadAll over a real directory tree, not through the
// per-entity parse, and the assertions read the loaded entity back — a value
// built by hand into the struct would satisfy a check on the field while a
// load that overwrote it went unnoticed. Every case chdirs into an unrelated
// directory first, so the authored value's independence from the directory the
// process runs in is observable rather than assumed.
//
// The authoring axis is one table: the three content-shape modes plus the
// empty value the envelope schema admits. An authoring form nobody covered is
// then a missing row rather than an absent function.

package catalog

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// relativeImportPath is the value the file-import cases author, kept in one
// place so the expectation and the fixture cannot come to disagree. The
// leading `./` is part of it: what the field carries is the text as written,
// not a tidied reading of it.
const relativeImportPath = "./extras/policy.yaml"

// retentionCatalog writes a catalog directory holding a Recipe and a
// FeatureRecipe that each import the same artifact value, drops the imported
// file beside them carrying the beside sentinel, plants a decoy copy at the
// same relative path in the directory the test then runs from, and returns the
// catalog directory.
func retentionCatalog(t *testing.T, artifactImport string) string {
	t.Helper()
	root := t.TempDir()
	catalogDir := filepath.Join(root, "catalog")
	runDir := filepath.Join(root, "elsewhere")
	for dir, body := range map[string]string{catalogDir: beside, runDir: decoy} {
		target := filepath.Join(dir, "extras", "policy.yaml")
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(target, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	entities := map[string]string{
		"recipe.yaml": `apiVersion: aggrega.ai/v1beta1
kind: Recipe
metadata:
  name: retained-import
  version: "1.0.0"
spec:
  capability: helm
  implements: "core/compute/api-server"
  artifacts:
    policy.yaml:
      $import: ` + artifactImport + `
`,
		"featurerecipe.yaml": `apiVersion: aggrega.ai/v1beta1
kind: FeatureRecipe
metadata:
  name: retained-import-fr
  version: "1.0.0"
spec:
  capability: helm
  implements: "core/monitoring"
  artifacts:
    policy.yaml:
      $import: ` + artifactImport + `
`,
	}
	for name, body := range entities {
		if err := os.WriteFile(filepath.Join(catalogDir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	t.Chdir(runDir)
	return catalogDir
}

// loadedArtifactEntries returns the single Recipe's and the single
// FeatureRecipe's `policy.yaml` artifact entry from a loaded catalog, keyed by
// the kind for the failure messages. Both kinds come back together because
// they carry the same surface: a retention reaching one and not the other
// would pass every assertion made against the first alone.
func loadedArtifactEntries(t *testing.T, catalogDir string) map[string]types.ArtifactEntry {
	t.Helper()
	c, err := LoadAll("", catalogDir)
	if err != nil {
		t.Fatalf("LoadAll: %v", err)
	}
	if len(c.Recipes) != 1 {
		t.Fatalf("expected 1 Recipe; got %d", len(c.Recipes))
	}
	if len(c.FeatureRecipes) != 1 {
		t.Fatalf("expected 1 FeatureRecipe; got %d", len(c.FeatureRecipes))
	}
	out := make(map[string]types.ArtifactEntry, 2)
	for _, r := range c.Recipes {
		entry, ok := r.Spec.Artifacts.Get("policy.yaml")
		if !ok {
			t.Fatal("Recipe: artifacts entry not loaded")
		}
		out["Recipe"] = entry
	}
	for _, fr := range c.FeatureRecipes {
		entry, ok := fr.Spec.Artifacts.Get("policy.yaml")
		if !ok {
			t.Fatal("FeatureRecipe: artifacts entry not loaded")
		}
		out["FeatureRecipe"] = entry
	}
	return out
}

// TestArtifactAuthoredImportRetainsWhatWasWritten walks every authoring form an
// artifacts entry admits and pins, for both kinds, that the authored value comes
// back exactly as written and that an empty authored value means only that the
// entry carries no `$import:`.
//
// The rows are the whole authoring axis:
//
//   - A relative local path is the case the resolution rewrites, so it is the
//     one where the authored text would otherwise be lost. An absolute path is
//     not an authoring form here: it names a file outside the entity and the load
//     refuses it, which the containment tests cover.
//   - A bare-name library reference is an identity rather than a path and is
//     also handed back unchanged.
//   - An empty value is what makes the empty authored value readable as absent:
//     the envelope schema constrains the `$import:` value only by type and by a
//     leading-`oci://` refusal, so an empty string reaches the loader. It
//     arrives as an entry carrying no import at all, which is the same state the
//     empty authored value already describes — so the empty value cannot also
//     mean "an import was authored and its text was lost".
func TestArtifactAuthoredImportRetainsWhatWasWritten(t *testing.T) {
	cases := []struct {
		name string
		// authored yields the text the fixture writes after `$import:`, and the
		// value the field must carry back. The empty row writes a quoted empty
		// scalar, so the two differ there and only there.
		authored func(t *testing.T) (written, want string)
		check    func(t *testing.T, kind string, entry types.ArtifactEntry)
	}{
		{
			name: "relative local path",
			authored: func(*testing.T) (string, string) {
				return relativeImportPath, relativeImportPath
			},
			check: func(t *testing.T, kind string, entry types.ArtifactEntry) {
				if !filepath.IsAbs(entry.Import) {
					t.Errorf("%s: resolved path not absolute: %q", kind, entry.Import)
					return
				}
				body, err := os.ReadFile(entry.Import)
				if err != nil {
					t.Errorf("%s: resolved path unreadable: %v", kind, err)
					return
				}
				if got := strings.TrimSpace(string(body)); got != beside {
					t.Errorf("%s: resolved against the wrong base: read %q from %q, want %q",
						kind, got, entry.Import, beside)
				}
			},
		},
		{
			name: "bare-name library reference",
			authored: func(*testing.T) (string, string) {
				const ref = "SharedCRDs#/artifacts/WidgetCRD"
				return ref, ref
			},
			check: func(t *testing.T, kind string, entry types.ArtifactEntry) {
				if !entry.IsLibraryImport() {
					t.Errorf("%s: library reference no longer reports as one after load", kind)
				}
				if entry.Import != entry.AuthoredImport {
					t.Errorf("%s: a library reference was rewritten: %q became %q",
						kind, entry.AuthoredImport, entry.Import)
				}
			},
		},
		{
			name: "empty value",
			authored: func(*testing.T) (string, string) {
				return `""`, ""
			},
			check: func(t *testing.T, kind string, entry types.ArtifactEntry) {
				if !entry.IsEmpty() {
					t.Errorf("%s: expected an entry with an empty $import to report as empty", kind)
				}
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			written, want := tc.authored(t)
			catalogDir := retentionCatalog(t, written)
			for kind, entry := range loadedArtifactEntries(t, catalogDir) {
				if entry.AuthoredImport != want {
					t.Errorf("%s: authored value did not survive the load: got %q, want %q",
						kind, entry.AuthoredImport, want)
				}
				// The empty authored value carries one meaning, and this is
				// what makes it carry only one: it is empty exactly when the
				// resolved value is.
				if (entry.AuthoredImport == "") != (entry.Import == "") {
					t.Errorf("%s: authored and resolved values disagree on emptiness: %q vs %q",
						kind, entry.AuthoredImport, entry.Import)
				}
				tc.check(t, kind, entry)
			}
		})
	}
}

// TestArtifactAuthoredImportDoesNotDependOnWhereTheCatalogSits pins the property
// the retention exists for: the authored value carries nothing that describes the
// machine it was loaded on, so what an author can be shown reads the same from
// any checkout, while the resolved value beside it stays absolute and still opens
// the file beside the entity.
func TestArtifactAuthoredImportDoesNotDependOnWhereTheCatalogSits(t *testing.T) {
	catalogDir := retentionCatalog(t, relativeImportPath)
	for kind, entry := range loadedArtifactEntries(t, catalogDir) {
		if filepath.IsAbs(entry.AuthoredImport) {
			t.Errorf("%s: authored value carries an absolute prefix: %q", kind, entry.AuthoredImport)
		}
		if vol := filepath.VolumeName(entry.AuthoredImport); vol != "" {
			t.Errorf("%s: authored value carries a volume prefix %q: %q", kind, vol, entry.AuthoredImport)
		}
		if strings.Contains(entry.AuthoredImport, catalogDir) {
			t.Errorf("%s: authored value carries the catalog location: %q", kind, entry.AuthoredImport)
		}
		if !filepath.IsAbs(entry.Import) {
			t.Errorf("%s: resolved path no longer absolute: %q", kind, entry.Import)
		}
	}
}
