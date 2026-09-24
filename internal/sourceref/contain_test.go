// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package sourceref

import (
	"errors"
	"strings"
	"testing"

	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/pathscope"
)

// containDocument is the declaring document every table below anchors against.
const containDocument = "/srv/catalog/recipe/helm/app/1.0.0/recipe.yaml"

// TestAnchorContentResolvesAPathInsideTheDocumentDirectory covers the accepted
// half of the bound, including the two spellings a token-based reading would get
// wrong: an interior climb that stays inside, and a name that merely begins with
// two dots.
func TestAnchorContentResolvesAPathInsideTheDocumentDirectory(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name     string
		authored string
		want     string
	}{
		{"a relative path", "./charts/app", "/srv/catalog/recipe/helm/app/1.0.0/charts/app"},
		{"a bare name", "values.tmpl", "/srv/catalog/recipe/helm/app/1.0.0/values.tmpl"},
		{"an interior climb that stays inside", "charts/../extras/netpol.yaml", "/srv/catalog/recipe/helm/app/1.0.0/extras/netpol.yaml"},
		{"a leading climb that stays inside", "./charts/../values.tmpl", "/srv/catalog/recipe/helm/app/1.0.0/values.tmpl"},
		{"a name beginning with two dots", "..config/values.tmpl", "/srv/catalog/recipe/helm/app/1.0.0/..config/values.tmpl"},
		{"the empty path names nothing", "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := AnchorContent(containDocument, tc.authored)
			if err != nil {
				t.Fatalf("AnchorContent(%q): %v", tc.authored, err)
			}
			if got != tc.want {
				t.Errorf("AnchorContent(%q) = %q, want %q", tc.authored, got, tc.want)
			}
		})
	}
}

// TestAnchorContentRefusesAPathThatLeavesTheDocumentDirectory covers the refused
// half. Each row is one way out; the last is the one a string-prefix comparison
// against the directory would admit, and the one before it re-enters the
// directory by its own name, which is refused because that name does not travel.
func TestAnchorContentRefusesAPathThatLeavesTheDocumentDirectory(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name     string
		document string
		authored string
	}{
		{"a climb out of the directory", containDocument, "../shared/values.tmpl"},
		{"a climb several levels out", containDocument, "../../../../../escaped/values.tmpl"},
		{"a climb out and back in by the directory's name", containDocument, "../1.0.0/values.tmpl"},
		{"an absolute path", containDocument, "/opt/charts/app"},
		{"an absolute path inside the directory itself", containDocument, "/srv/catalog/recipe/helm/app/1.0.0/charts/app"},
		{"a sibling whose name extends the directory's", containDocument, "../1.0.0-old/values.tmpl"},
		{"a climb out with no declaring document", "", "../values.tmpl"},
		{"an absolute path with no declaring document", "", "/opt/charts/app"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := AnchorContent(tc.document, tc.authored)
			assertContentRefusal(t, err, tc.authored)
			if got != "" {
				t.Errorf("a refusal returned a path %q; want none", got)
			}
		})
	}
}

// TestAnchorContentAgreesWithTheContainmentPredicate holds the verdict to the
// predicate it is reached from, over the union of both tables' inputs: a path is
// refused exactly when the predicate calls it outside. The helper adds a
// condition and a sentence and nothing else, so a second containment test grown
// inside it would show up here as a disagreement.
func TestAnchorContentAgreesWithTheContainmentPredicate(t *testing.T) {
	t.Parallel()
	const dir = "/srv/catalog/recipe/helm/app/1.0.0"
	for _, authored := range []string{
		"./charts/app", "values.tmpl", "charts/../extras/netpol.yaml", "./charts/../values.tmpl",
		"..config/values.tmpl", "../shared/values.tmpl", "../../../../../escaped/values.tmpl",
		"../1.0.0/values.tmpl", "/opt/charts/app", dir + "/charts/app", "../1.0.0-old/values.tmpl",
		"a/b/../../c", "a/../../c", ".", "./",
	} {
		_, inside := pathscope.Resolve(dir, authored)
		_, err := AnchorContent(containDocument, authored)
		if inside != (err == nil) {
			t.Errorf("%q: the containment predicate says inside=%v, the anchoring returned %v", authored, inside, err)
		}
	}
}

// TestAnchorToDocumentRefusesALocalPathOutsideTheDocumentDirectory is the source
// join's half of the bound, with and without a declaring document. A reference
// that is not local is never judged, because it names nothing on disk.
func TestAnchorToDocumentRefusesALocalPathOutsideTheDocumentDirectory(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name     string
		document string
		ref      string
	}{
		{"a climb out", containDocument, "../shared/charts/app"},
		{"an absolute path", containDocument, "/opt/charts/app"},
		{"a climb out with no declaring document", "", "../charts/app"},
		{"an absolute path with no declaring document", "", "/opt/charts/app"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := AnchorToDocument(tc.document, tc.ref)
			assertContentRefusal(t, err, tc.ref)
		})
	}
	// The negative control: a transport reference that happens to contain a climb is
	// carried through, since it names something fetched rather than a path.
	if got, err := AnchorToDocument(containDocument, "https://example.test/charts/../app.tgz"); err != nil || got != "https://example.test/charts/../app.tgz" {
		t.Errorf("a transport reference was judged as a path: got %q, %v", got, err)
	}
}

// TestAnchorSourceRefusesAStructuredURLOutsideTheDocumentDirectory covers the
// structured shape: the url is the one field that names where content is read
// from, so it is the field that is judged.
func TestAnchorSourceRefusesAStructuredURLOutsideTheDocumentDirectory(t *testing.T) {
	t.Parallel()
	for _, url := range []string{"../shared/charts/app", "/opt/charts/app"} {
		_, err := AnchorSource(containDocument, map[string]any{"kind": "helm-oci-chart", "url": url, "version": "1.0.0"})
		assertContentRefusal(t, err, url)
	}
}

// assertContentRefusal asserts the refusal every table here expects: the
// registered condition, reachable through its accessor, reported under its short
// code, naming the path the author wrote and telling them why it matters.
func assertContentRefusal(t *testing.T, err error, authored string) {
	t.Helper()
	if err == nil {
		t.Fatalf("%q: accepted; want it refused as leaving the entity's directory", authored)
	}
	if !errors.Is(err, ErrContentOutsideEntityDirectory()) {
		t.Fatalf("%q: refused for another reason: %v", authored, err)
	}
	if code, ok := cerrors.ShortCodeOf(err); !ok || code != "E0304" {
		t.Errorf("%q: short code = %q (%v), want E0304", authored, code, ok)
	}
	msg := err.Error()
	if !strings.Contains(msg, authored) {
		t.Errorf("%q: the refusal does not name the path the author wrote: %s", authored, msg)
	}
	if !strings.Contains(msg, "would not travel with the entity") {
		t.Errorf("%q: the refusal does not say why the path matters: %s", authored, msg)
	}
}
