// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package compilerops

import (
	stderrors "errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/validator/envelope"
)

// versionlessFeature is a Feature document its kind's schema refuses for one
// fault: it declares no metadata.version.
const versionlessFeature = `apiVersion: aggrega.ai/v1beta1
kind: Feature
metadata:
  name: local/nightly-backup
spec:
  appliesTo: "component.aggrega.ai/category=datastore"
`

// strictLoadFailure writes files into a fresh catalog directory, loads it the
// way a by-name inspection does, and returns the directory and the failure.
func strictLoadFailure(t *testing.T, files map[string]string) (string, error) {
	t.Helper()
	dir := t.TempDir()
	for rel, body := range files {
		path := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}
	_, err := catalog.LoadAllWithCacheDir("", "", dir)
	if err == nil {
		t.Fatal("the catalog loaded; every row here is a load that stops")
	}
	return dir, err
}

// TestARefusedDocumentIsStatedOnce holds the refusal of one document to one frame,
// naming the document once, over the failure a real strict load raises.
//
// The load reports the refusal as three frames — the directory it was walking,
// the step that read the file, the constraint — the last two naming the file
// again and the constraint naming it a third time inside its own sentence. What a
// reader needs is the document once and the fault once, so the one-line form is
// asserted by equality, and the frame is asserted to carry no categorised frame
// beneath it for a formatter to print again.
func TestARefusedDocumentIsStatedOnce(t *testing.T) {
	t.Parallel()
	code, ok := cerrors.ShortCodeOf(envelope.ErrEntityEnvelope())
	if !ok {
		t.Fatal("the envelope-validation condition must be registered with a short code")
	}
	dir, err := strictLoadFailure(t, map[string]string{"feature/nightly-backup.yaml": versionlessFeature})
	doc := filepath.Join(dir, "feature", "nightly-backup.yaml")

	refused, ok := RefusedCatalogDocument(err)
	if !ok {
		t.Fatalf("a strict load that stopped on one document was not recognised as one:\n%v", err)
	}
	if refused.Path != doc {
		t.Errorf("the refused document is %q; want %q", refused.Path, doc)
	}

	for _, tc := range []struct {
		name string
		path string
		want string
	}{
		{
			name: "named as the load read it",
			path: doc,
			want: "[" + string(cerrors.CatalogError) + " " + code + "] " + doc +
				": document envelope validation failed (Feature); 3:1: metadata.version: missing required property: version. " +
				"correct the document to match its kind's schema",
		},
		{
			name: "named as a surface reports it",
			path: "catalog/feature/nightly-backup.yaml",
			want: "[" + string(cerrors.CatalogError) + " " + code + "] catalog/feature/nightly-backup.yaml" +
				": document envelope validation failed (Feature); 3:1: metadata.version: missing required property: version. " +
				"correct the document to match its kind's schema",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := RefusedDocumentLine(tc.path, refused.Err); got != tc.want {
				t.Errorf("the refusal reads\n  %s\nwant\n  %s", got, tc.want)
			}
		})
	}

	frame := RefusedDocument(doc, refused.Err)
	if n := categorisedFrames(frame); n != 1 {
		t.Errorf("the refusal is %d categorised frames on its spine; want 1, or a formatter prints the fault again", n)
	}
	// Stating it once changes what a reader meets, not what a caller matches on.
	if !stderrors.Is(frame, envelope.ErrEntityEnvelope()) {
		t.Error("the stated refusal no longer matches the condition it raised")
	}
	var violation *envelope.Error
	if !stderrors.As(frame, &violation) || violation.Path != doc {
		t.Errorf("the stated refusal no longer carries the violation, at the path it was met at; got %+v", violation)
	}
}

// TestOnlyALoadStoppedOnOneDocumentIsStatedOnce pins the other failures a strict
// load raises to the rendering they always had: a directory the walk cannot read
// names no document, and neither does the pass that validates every entity's
// schema at the end of the load.
func TestOnlyALoadStoppedOnOneDocumentIsStatedOnce(t *testing.T) {
	t.Parallel()
	_, err := catalog.LoadAllWithCacheDir("", "", filepath.Join(t.TempDir(), "no-such-directory"))
	if err == nil {
		t.Fatal("a catalog directory that does not exist loaded")
	}
	if refused, ok := RefusedCatalogDocument(err); ok {
		t.Errorf("a directory the walk could not read was taken for a refused document %q", refused.Path)
	}
	if _, ok := RefusedCatalogDocument(stderrors.New("no categorised frame at all")); ok {
		t.Error("a failure with no categorised frame was taken for a refused document")
	}
}

// categorisedFrames counts the categorised frames on err's single-cause spine,
// which is the spine a formatter walks and prints a frame for each of.
func categorisedFrames(err error) int {
	n := 0
	for cur := err; cur != nil; cur = stderrors.Unwrap(cur) {
		if _, ok := cur.(*cerrors.CompilerError); ok {
			n++
		}
	}
	return n
}

// TestAFaultThatIsNotASchemaViolationKeepsEachFramesSentence pins the general
// rendering: each frame contributes its sentence, not its category and path, and
// a sentence an earlier frame already carries is not said twice.
func TestAFaultThatIsNotASchemaViolationKeepsEachFramesSentence(t *testing.T) {
	t.Parallel()
	const path = "/cat/recipe/helm/demo/1.0.0/recipe.yaml"
	refusal := cerrors.Wrap(
		cerrors.Wrap(stderrors.New("open chart/linked: too many levels of symbolic links"),
			cerrors.CatalogError, cerrors.SourceLocation{File: path}, "read the local source"),
		cerrors.CatalogError, cerrors.SourceLocation{File: path}, "read catalog file")
	got := RefusedDocumentLine("catalog/recipe/helm/demo/1.0.0/recipe.yaml", refusal)
	want := "[" + string(cerrors.CatalogError) + "] catalog/recipe/helm/demo/1.0.0/recipe.yaml: read catalog file: " +
		"read the local source: open chart/linked: too many levels of symbolic links"
	if got != want {
		t.Errorf("the refusal reads\n  %s\nwant\n  %s", got, want)
	}
	if strings.Count(got, "[") != 1 {
		t.Errorf("the refusal carries more than one category head: %s", got)
	}
}
