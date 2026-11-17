// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package catalog

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
)

// libraryKindCase is one library kind as the storage tests drive it: how a
// document of the kind is written, and how each catalog backing is read back for
// it. Every test below runs once per kind, because the four kinds share one
// identity rule and one precedence rule and a kind left out is a kind that drifts.
type libraryKindCase struct {
	kind      string
	mediaType string
	// section is the document's spec body, one entry of the kind's own section.
	section string
	// eagerHeld is the identity-keyed index the eager load holds the kind in.
	eagerHeld func(c *Catalog) []string
	// eagerDefault is the version the default selection reads for a name.
	eagerDefault func(s LibrarySet, name string) (string, bool)
	// lazyRead reads a library through the view's per-kind read, which builds the
	// enumeration the kind lives in, and reports the version read.
	lazyRead func(l *LazyLookup, uri string) (string, bool)
	// lazyHeld is the identity-keyed index the view holds the kind in, read after
	// lazyRead has built it.
	lazyHeld func(l *LazyLookup) []string
}

// doc is a document of the kind declaring the library Lib at version.
func (k libraryKindCase) doc(version string) string {
	return fmt.Sprintf("apiVersion: aggrega.ai/v1beta1\nkind: %s\nmetadata:\n  name: Lib\n  version: %s\nspec:\n%s",
		k.kind, version, k.section)
}

func libraryKindCases() []libraryKindCase {
	return []libraryKindCase{
		{
			kind:      "SchemaLibrary",
			mediaType: oci.MediaTypeSchemaLibrary,
			section:   "  schemas:\n    Settings:\n      level: { type: string }\n",
			eagerHeld: func(c *Catalog) []string { return sortedMapKeys(c.SchemaLibraryVersions) },
			eagerDefault: func(s LibrarySet, name string) (string, bool) {
				lib, ok := s.Schemas[name]
				return lib.Metadata.Version, ok
			},
			lazyRead: func(l *LazyLookup, uri string) (string, bool) {
				lib, ok := l.SchemaLibrary(uri)
				return lib.Metadata.Version, ok
			},
			lazyHeld: func(l *LazyLookup) []string { return sortedMapKeys(l.schemaLibraryVersions) },
		},
		{
			kind:      "MappingLibrary",
			mediaType: oci.MediaTypeMappingLibrary,
			section:   "  mappings:\n    Values: 'level: 1'\n",
			eagerHeld: func(c *Catalog) []string { return sortedMapKeys(c.MappingLibraryVersions) },
			eagerDefault: func(s LibrarySet, name string) (string, bool) {
				lib, ok := s.Mappings[name]
				return lib.Metadata.Version, ok
			},
			lazyRead: func(l *LazyLookup, uri string) (string, bool) {
				lib, ok := l.MappingLibrary(uri)
				return lib.Metadata.Version, ok
			},
			lazyHeld: func(l *LazyLookup) []string { return sortedMapKeys(l.mappingLibraryVersions) },
		},
		{
			kind:      "WorkloadLibrary",
			mediaType: oci.MediaTypeWorkloadLibrary,
			section:   "  workloads:\n    Deployment: 'kind: Deployment'\n",
			eagerHeld: func(c *Catalog) []string { return sortedMapKeys(c.WorkloadLibraryVersions) },
			eagerDefault: func(s LibrarySet, name string) (string, bool) {
				lib, ok := s.Workloads[name]
				return lib.Metadata.Version, ok
			},
			lazyRead: func(l *LazyLookup, uri string) (string, bool) {
				lib, ok := l.WorkloadLibrary(uri)
				return lib.Metadata.Version, ok
			},
			lazyHeld: func(l *LazyLookup) []string { return sortedMapKeys(l.workloadLibraryVersions) },
		},
		{
			kind:      "ArtifactLibrary",
			mediaType: oci.MediaTypeArtifactLibrary,
			section:   "  artifacts:\n    WidgetCRD:\n      widget-config.yaml: \"kind: ConfigMap\"\n",
			eagerHeld: func(c *Catalog) []string { return sortedMapKeys(c.ArtifactLibraryVersions) },
			eagerDefault: func(s LibrarySet, name string) (string, bool) {
				lib, ok := s.Artifacts[name]
				return lib.Metadata.Version, ok
			},
			lazyRead: func(l *LazyLookup, uri string) (string, bool) {
				lib, ok := l.ArtifactLibrary(uri)
				return lib.Metadata.Version, ok
			},
			lazyHeld: func(l *LazyLookup) []string { return sortedMapKeys(l.artifactLibraryVersions) },
		},
	}
}

// libraryFileNamings are the two ways two files of one directory can sort: the
// lower version first, and the higher version first. A rule that held only for one
// of them would be file order, not version order.
var libraryFileNamings = []struct {
	name                 string
	lowerFile, upperFile string
}{
	{name: "lower version sorts first", lowerFile: "a-lib.yaml", upperFile: "b-lib.yaml"},
	{name: "higher version sorts first", lowerFile: "b-lib.yaml", upperFile: "a-lib.yaml"},
}

func mkCatalogDir(t *testing.T, path string) string {
	t.Helper()
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", path, err)
	}
	return path
}

// TestLoadHoldsTwoVersionsOfOneLibraryInOneDirectory: a catalog directory holding
// two versions of one library name loads whole, each version under its own
// identity with its own bytes, with nothing shadowed; a bare reference reads the
// higher version whichever file sorts first.
func TestLoadHoldsTwoVersionsOfOneLibraryInOneDirectory(t *testing.T) {
	t.Parallel()
	for _, k := range libraryKindCases() {
		for _, naming := range libraryFileNamings {
			t.Run(k.kind+"/"+naming.name, func(t *testing.T) {
				t.Parallel()
				ws := t.TempDir()
				writeFixture(t, ws, naming.lowerFile, k.doc("1.0.0"))
				writeFixture(t, ws, naming.upperFile, k.doc("1.1.0"))

				c, err := LoadAllWithNamedDirs(ws, "", nil, ws)
				if err != nil {
					t.Fatalf("load of a directory holding Lib 1.0.0 and 1.1.0: %v", err)
				}
				if got, want := k.eagerHeld(c), []string{"Lib:1.0.0", "Lib:1.1.0"}; !reflect.DeepEqual(got, want) {
					t.Errorf("held identities = %v, want %v", got, want)
				}
				for _, v := range []string{"1.0.0", "1.1.0"} {
					if got, ok := c.EntityBytes(k.kind, "Lib:"+v); !ok || string(got) != k.doc(v) {
						t.Errorf("bytes held for Lib:%s = %q (ok %v), want that version's own document", v, got, ok)
					}
				}
				if v, ok := k.eagerDefault(c.DefaultLibraries(), "Lib"); !ok || v != "1.1.0" {
					t.Errorf("default selection of Lib = %q (ok %v), want 1.1.0", v, ok)
				}
				if len(c.Shadows) != 0 {
					t.Errorf("two versions of one directory recorded shadows: %+v", c.Shadows)
				}
			})
		}
	}
}

// TestLoadRefusesOneLibraryIdentityTwiceInOneDirectory: the same library identity
// declared by two files of one directory halts the load with the duplicate-identity
// condition, naming the identity with its version.
func TestLoadRefusesOneLibraryIdentityTwiceInOneDirectory(t *testing.T) {
	t.Parallel()
	for _, k := range libraryKindCases() {
		t.Run(k.kind, func(t *testing.T) {
			t.Parallel()
			ws := t.TempDir()
			writeFixture(t, ws, "a-lib.yaml", k.doc("1.0.0"))
			writeFixture(t, ws, "b-lib.yaml", k.doc("1.0.0"))

			_, err := LoadAllWithNamedDirs(ws, "", nil, ws)
			if !errors.Is(err, ErrDuplicateEntityIdentity()) {
				t.Fatalf("load of two files declaring Lib:1.0.0: got %v, want the duplicate-identity condition", err)
			}
			if chain := cerrors.CauseChain(err); !strings.Contains(chain, "Lib:1.0.0") {
				t.Errorf("the refusal does not name the identity Lib:1.0.0: %s", chain)
			}
		})
	}
}

// TestLibraryNameOwnedByFirstDirectory: across a stack of catalog directories the
// first directory holding any version of a library name owns the name. A later
// directory's copy of that name is shadowed and recorded as a shadow, whatever its
// version — a higher one included — and swapping the stack swaps the owner.
func TestLibraryNameOwnedByFirstDirectory(t *testing.T) {
	t.Parallel()
	for _, k := range libraryKindCases() {
		t.Run(k.kind, func(t *testing.T) {
			t.Parallel()
			ws := t.TempDir()
			older := mkCatalogDir(t, filepath.Join(ws, "older"))
			newer := mkCatalogDir(t, filepath.Join(ws, "newer"))
			writeFixture(t, older, "lib.yaml", k.doc("1.0.0"))
			writeFixture(t, newer, "lib.yaml", k.doc("1.1.0"))

			for _, stack := range []struct {
				first, second   string
				owned, shadowed string
			}{
				{first: older, second: newer, owned: "Lib:1.0.0", shadowed: "Lib:1.1.0"},
				{first: newer, second: older, owned: "Lib:1.1.0", shadowed: "Lib:1.0.0"},
			} {
				c, err := LoadAllWithNamedDirs(ws, "", nil, stack.first, stack.second)
				if err != nil {
					t.Fatalf("load of [%s %s]: %v", stack.first, stack.second, err)
				}
				if got, want := k.eagerHeld(c), []string{stack.owned}; !reflect.DeepEqual(got, want) {
					t.Errorf("stack [%s %s]: held identities = %v, want %v", stack.first, stack.second, got, want)
				}
				want := ShadowedEntity{
					Kind: k.kind, Identity: stack.shadowed,
					WinnerDir: stack.first, WinnerPath: filepath.Join(stack.first, "lib.yaml"),
					ShadowedDir: stack.second, ShadowedPath: filepath.Join(stack.second, "lib.yaml"),
				}
				if len(c.Shadows) != 1 || c.Shadows[0] != want {
					t.Errorf("stack [%s %s]: shadows = %+v, want exactly %+v", stack.first, stack.second, c.Shadows, want)
				}
			}
		})
	}
}

// TestLazyLibraryVersionsCoexistInOneDirectory: the lazy view holds two versions of
// one library name from one directory, each under its own identity with its own
// bytes and its own document, refuses neither and announces nothing; its per-kind
// read of the bare name reads the higher version whichever file sorts first.
func TestLazyLibraryVersionsCoexistInOneDirectory(t *testing.T) {
	t.Parallel()
	for _, k := range libraryKindCases() {
		for _, naming := range libraryFileNamings {
			t.Run(k.kind+"/"+naming.name, func(t *testing.T) {
				t.Parallel()
				ws := t.TempDir()
				writeFixture(t, ws, naming.lowerFile, k.doc("1.0.0"))
				writeFixture(t, ws, naming.upperFile, k.doc("1.1.0"))
				view := NewLazyLookup(ws, "", []string{ws}, nil)
				var notices bytes.Buffer
				view.WriteNoticesTo(&notices)

				if v, ok := k.lazyRead(view, "Lib"); !ok || v != "1.1.0" {
					t.Errorf("read of Lib = %q (ok %v), want 1.1.0", v, ok)
				}
				if got, want := k.lazyHeld(view), []string{"Lib:1.0.0", "Lib:1.1.0"}; !reflect.DeepEqual(got, want) {
					t.Errorf("held identities = %v, want %v", got, want)
				}
				for v, file := range map[string]string{"1.0.0": naming.lowerFile, "1.1.0": naming.upperFile} {
					claim := entityBytesKey(k.kind, "Lib:"+v)
					if got := view.entityBytes[claim]; string(got) != k.doc(v) {
						t.Errorf("bytes held for Lib:%s = %q, want that version's own document", v, got)
					}
					if doc, ok := view.origins[claim].authoredDocument(); !ok || doc != filepath.Join(ws, file) {
						t.Errorf("document recorded for Lib:%s = %q (ok %v), want %s", v, doc, ok, filepath.Join(ws, file))
					}
				}
				if skips := view.Skips(); len(skips) != 0 {
					t.Errorf("two versions of one directory were refused: %v", skips)
				}
				if notices.Len() != 0 {
					t.Errorf("two versions of one directory were announced:\n%s", notices.String())
				}
			})
		}
	}
}

// TestLazyLibraryVersionsRefuseOneIdentityTwiceInOneDirectory: the same library
// identity declared by two files of one directory is the load's refusal in the lazy
// view too — the second file in walk order is recorded with the duplicate-identity
// condition naming the identity with its version, and the first is read.
func TestLazyLibraryVersionsRefuseOneIdentityTwiceInOneDirectory(t *testing.T) {
	t.Parallel()
	for _, k := range libraryKindCases() {
		t.Run(k.kind, func(t *testing.T) {
			t.Parallel()
			ws := t.TempDir()
			writeFixture(t, ws, "a-lib.yaml", k.doc("1.0.0"))
			writeFixture(t, ws, "b-lib.yaml", k.doc("1.0.0"))
			view := NewLazyLookup(ws, "", []string{ws}, nil)

			if _, ok := k.lazyRead(view, "Lib"); !ok {
				t.Fatal("Lib is not readable; the first file declaring it should be")
			}
			if doc, _ := view.origins[entityBytesKey(k.kind, "Lib:1.0.0")].authoredDocument(); doc != filepath.Join(ws, "a-lib.yaml") {
				t.Errorf("document recorded for Lib:1.0.0 = %q, want the first file in walk order", doc)
			}
			err := view.Skips()[filepath.Join(ws, "b-lib.yaml")]
			if !errors.Is(err, ErrDuplicateEntityIdentity()) {
				t.Fatalf("record of the second file = %v, want the duplicate-identity condition", err)
			}
			if chain := cerrors.CauseChain(err); !strings.Contains(chain, "Lib:1.0.0") {
				t.Errorf("the record does not name the identity Lib:1.0.0: %s", chain)
			}
		})
	}
}

// TestLazySchemaLibraryFirstDirectoryOwnsName: in the lazy view, as in the eager
// load, the first catalog directory holding any version of a library name owns it.
// A later directory's copy — at a higher version here — is shadowed: not read, not
// recorded as a refusal, and announced with its identity. SchemaLibrary is
// enumerated by a walk of its own and is the case this holds for least obviously;
// the other three kinds run beside it so the four cannot drift apart.
func TestLazySchemaLibraryFirstDirectoryOwnsName(t *testing.T) {
	t.Parallel()
	for _, k := range libraryKindCases() {
		t.Run(k.kind, func(t *testing.T) {
			t.Parallel()
			ws := t.TempDir()
			first := mkCatalogDir(t, filepath.Join(ws, "first"))
			second := mkCatalogDir(t, filepath.Join(ws, "second"))
			writeFixture(t, first, "lib.yaml", k.doc("1.0.0"))
			writeFixture(t, second, "lib.yaml", k.doc("1.1.0"))
			shadowedFile := filepath.Join(second, "lib.yaml")
			view := NewLazyLookup(ws, "", []string{first, second}, nil)
			var notices bytes.Buffer
			view.WriteNoticesTo(&notices)

			if v, ok := k.lazyRead(view, "Lib"); !ok || v != "1.0.0" {
				t.Errorf("read of Lib = %q (ok %v), want the first directory's 1.0.0", v, ok)
			}
			if got, want := k.lazyHeld(view), []string{"Lib:1.0.0"}; !reflect.DeepEqual(got, want) {
				t.Errorf("held identities = %v, want %v", got, want)
			}
			if err, refused := view.Skips()[shadowedFile]; refused {
				t.Errorf("the shadowed file was recorded as a refusal: %v", err)
			}
			if !strings.Contains(notices.String(), shadowedFile) || !strings.Contains(notices.String(), "Lib:1.1.0") ||
				!strings.Contains(notices.String(), "shadowed") {
				t.Errorf("the shadowed file %s is not announced as shadowed with its identity Lib:1.1.0:\n%s", shadowedFile, notices.String())
			}
		})
	}
}

// TestLazyPinnedSlotOfLocallyHeldLibraryName: a lockfile pin of a library name a
// catalog directory holds, at another version, does not add that version beside
// the local one. The catalog directory owns the name; the pinned slot is not
// folded and is recorded as a pinned duplicate, under the label its pin reads.
func TestLazyPinnedSlotOfLocallyHeldLibraryName(t *testing.T) {
	t.Parallel()
	for _, k := range libraryKindCases() {
		t.Run(k.kind, func(t *testing.T) {
			t.Parallel()
			ws := t.TempDir()
			writeFixture(t, ws, "lib.yaml", k.doc("1.1.0"))
			key := PinKey{Kind: k.kind, URI: "Lib"}
			pin := writePinnedSlot(t, ws, cacheUnderCatalogDir, "Lib:1.0.0", k.mediaType, []byte(k.doc("1.0.0")))
			view := NewLazyLookup(ws, cacheUnderCatalogDir, []string{ws}, nil)
			view.SetPins(Pins{key: pin})

			if v, ok := k.lazyRead(view, "Lib"); !ok || v != "1.1.0" {
				t.Errorf("read of Lib = %q (ok %v), want the catalog directory's 1.1.0", v, ok)
			}
			if got, want := k.lazyHeld(view), []string{"Lib:1.1.0"}; !reflect.DeepEqual(got, want) {
				t.Errorf("held identities = %v, want %v", got, want)
			}
			if err := view.Skips()[pinnedSlotLabel(key)]; err == nil || !strings.Contains(err.Error(), "Lib:1.0.0") {
				t.Errorf("record of the pinned slot = %v, want the pinned duplicate naming Lib:1.0.0", err)
			}
		})
	}
}
