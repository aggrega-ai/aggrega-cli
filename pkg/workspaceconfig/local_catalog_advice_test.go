// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package workspaceconfig_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// countPhrase is what the count branch — and only the count branch — writes.
// Asserting the absence of a count by the absence of this phrase is what lets a
// row say "no count" without pinning the whole sentence.
const countPhrase = "declaring an entity kind"

// countedDocuments is a document of each of two entity kinds, keyed by the file
// name it is written under. Two rather than one, because a count of one reads
// identically whether the implementation counted the documents or answered
// "there is at least one".
var countedDocuments = map[string]string{
	"component.yaml": "apiVersion: aggrega.ai/v1beta1\nkind: Component\nmetadata:\n  name: counted-component\n",
	"server.yml":     "apiVersion: aggrega.ai/v1beta1\nkind: Recipe\nmetadata:\n  name: counted-recipe\n",
}

// uncountedDocuments is the corpus a workspace root ordinarily carries that has
// nothing to do with this product, keyed by file name.
//
// 🔴 The fourth entry is the one the obvious three do not reach. A
// container-composition file, a linter's settings and a continuous-integration
// definition all carry no top-level kind at all, so an implementation that
// counted every document declaring ANY kind would pass a corpus made only of
// those three — and then count a Kubernetes manifest, which declares a kind this
// product does not read. The corpus carries one so the predicate is held to the
// vocabulary rather than to the presence of a field.
var uncountedDocuments = map[string]string{
	"docker-compose.yaml": "services:\n  web:\n    image: nginx\n",
	".linter.yaml":        "linters:\n  enable:\n    - govet\n",
	"pipeline.yml":        "on: push\njobs:\n  test:\n    runs-on: ubuntu-latest\n",
	"deployment.yaml":     "apiVersion: apps/v1\nkind: Deployment\nmetadata:\n  name: web\n",
	"README.md":           "kind: Component\n",
	"unparseable.yaml":    "kind: Component\n\tmetadata: [unclosed\n",
}

// writeRootDocuments writes each name→body pair into dir.
func writeRootDocuments(t *testing.T, dir string, docs map[string]string) {
	t.Helper()
	for name, body := range docs {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
}

// adviceWorkspace authors a workspace root carrying exactly config as its
// config body — written verbatim, because an absent key and an authored empty
// list are different documents and a helper that normalised them would erase
// the distinction under test — plus each named document, and resolves it.
func adviceWorkspace(t *testing.T, config string, docs ...map[string]string) *workspaceconfig.ResolvedWorkspace {
	t.Helper()
	root := t.TempDir()
	if config != "" {
		if err := os.WriteFile(filepath.Join(root, workspaceconfig.ConfigFilename), []byte(config), 0o644); err != nil {
			t.Fatalf("write workspace config: %v", err)
		}
	}
	for _, d := range docs {
		writeRootDocuments(t, root, d)
	}
	return resolveIn(t, root)
}

// TestLocalCatalogAdviceNoteAcrossTheAuthoredAndRootShapes walks the plane the
// note's own predicate reads: whether the reader wrote the catalog-directory
// list as an empty list, crossed with what sits at the workspace root.
//
// The third fact the whole advisory turns on — that a lookup for a named entity
// came back with nothing — is not on this plane and cannot be: the note carries
// no knowledge of any lookup, and the surfaces that report a failure are what
// supply it by calling. That half is driven at those surfaces; this pins the
// half the workspace answers.
//
// 🔴 The bottom row is the interaction one, and it is the cell a per-axis
// reading misses. A workspace can carry entity documents at its root AND declare
// the empty list, and an implementation that composed the count from the root
// and then suppressed only the sentence would leak the count there. Nothing in
// "silent under the empty list" or "counts documents at the root", read
// separately, requires that row to be checked.
func TestLocalCatalogAdviceNoteAcrossTheAuthoredAndRootShapes(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		// config is the `.aggrega-config.yaml` body; "" writes no file at all.
		config string
		docs   []map[string]string
		// wantNote is whether the workspace says anything at all.
		wantNote bool
		// wantCount is the rendered count the note carries, "" for no count.
		wantCount string
	}{
		{
			name:      "no config file at all, entity documents at the root",
			config:    "",
			docs:      []map[string]string{countedDocuments},
			wantNote:  true,
			wantCount: "2 documents",
		},
		{
			name:      "an absent key, entity documents at the root",
			config:    "workspace: {}\n",
			docs:      []map[string]string{countedDocuments},
			wantNote:  true,
			wantCount: "2 documents",
		},
		{
			name:      "an authored list, entity documents at the root",
			config:    "workspace:\n  catalog_dirs:\n    - ./vendored\n",
			docs:      []map[string]string{countedDocuments},
			wantNote:  true,
			wantCount: "2 documents",
		},
		{
			name:      "an absent key, only documents this product does not read",
			config:    "workspace: {}\n",
			docs:      []map[string]string{uncountedDocuments},
			wantNote:  true,
			wantCount: "",
		},
		{
			name:      "an absent key, a root carrying nothing",
			config:    "workspace: {}\n",
			wantNote:  true,
			wantCount: "",
		},
		{
			name:      "an absent key, one entity document among documents this product does not read",
			config:    "workspace: {}\n",
			docs:      []map[string]string{uncountedDocuments, {"only.yaml": countedDocuments["component.yaml"]}},
			wantNote:  true,
			wantCount: "1 document",
		},
		{
			name:     "an authored empty list, a root carrying nothing",
			config:   "workspace:\n  catalog_dirs: []\n",
			wantNote: false,
		},
		{
			name:     "an authored empty list, only documents this product does not read",
			config:   "workspace:\n  catalog_dirs: []\n",
			docs:     []map[string]string{uncountedDocuments},
			wantNote: false,
		},
		{
			name:     "an authored empty list, entity documents at the root",
			config:   "workspace:\n  catalog_dirs: []\n",
			docs:     []map[string]string{countedDocuments},
			wantNote: false,
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			rw := adviceWorkspace(t, tc.config, tc.docs...)
			got := rw.LocalCatalogAdviceNote("")

			if !tc.wantNote {
				if got != "" {
					t.Fatalf("a workspace declaring it reads no local catalog said %q; it must say nothing, because the advice contradicts the declaration", got)
				}
				return
			}
			if got == "" {
				t.Fatal("the workspace said nothing; a reader whose lookup found nothing is owed where entities are read from")
			}
			if want := "`" + workspaceconfig.ConventionalCatalogDir + "/`"; !strings.Contains(got, want) {
				t.Errorf("the note %q does not name %s, which is one of the two things a reader can act on", got, want)
			}
			if !strings.Contains(got, workspaceconfig.CatalogDirsSetting) {
				t.Errorf("the note %q does not name %s, which is the other", got, workspaceconfig.CatalogDirsSetting)
			}
			switch tc.wantCount {
			case "":
				if strings.Contains(got, countPhrase) {
					t.Errorf("the note %q counts documents at the root; this root carries none that declare an entity kind", got)
				}
			default:
				if !strings.Contains(got, "holds "+tc.wantCount+" "+countPhrase) {
					t.Errorf("the note %q does not report %q at the root", got, tc.wantCount)
				}
			}
		})
	}
}

// TestLocalCatalogAdviceCountsTheRootOnly holds the count to the workspace root
// itself. A count taken from a tree would be the whole-workspace walk a catalog
// search no longer performs, so reporting one would describe a search the reader
// cannot ask for — and the walk is exactly what this workspace stopped doing.
//
// The row that matters is the second: an entity document one directory down is
// the ordinary shape of a workspace that keeps its catalog where the product
// reads it, and counting it would tell such a reader they have documents in the
// wrong place when they do not.
func TestLocalCatalogAdviceCountsTheRootOnly(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	nested := filepath.Join(root, "services", "api")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatalf("mkdir nested: %v", err)
	}
	writeRootDocuments(t, nested, countedDocuments)
	// A DIRECTORY whose name ends in the extension the scan filters on. It is
	// not a document, and an implementation reading directory entries without
	// asking which are directories would try to parse it.
	if err := os.MkdirAll(filepath.Join(root, "generated.yaml"), 0o755); err != nil {
		t.Fatalf("mkdir the directory named like a document: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, workspaceconfig.ConfigFilename), []byte("workspace: {}\n"), 0o644); err != nil {
		t.Fatalf("write workspace config: %v", err)
	}

	got := resolveIn(t, root).LocalCatalogAdviceNote("")
	if got == "" {
		t.Fatal("a workspace whose entities sit one directory down is still owed where entities are read from")
	}
	if strings.Contains(got, countPhrase) {
		t.Errorf("the note %q counts documents the scan reached below the root; the scan reads the root and does not descend", got)
	}
}

// TestLocalCatalogAdviceNoteIsSafeOnNoWorkspace pins the answer for a caller
// holding no resolved workspace. A surface reporting a failure may have failed
// before resolving one, and a note is the last thing that should turn such a
// failure into a crash.
func TestLocalCatalogAdviceNoteIsSafeOnNoWorkspace(t *testing.T) {
	t.Parallel()
	var rw *workspaceconfig.ResolvedWorkspace
	if got := rw.LocalCatalogAdviceNote(""); got != "" {
		t.Errorf("an unresolved workspace said %q; it knows nothing to say", got)
	}
}
