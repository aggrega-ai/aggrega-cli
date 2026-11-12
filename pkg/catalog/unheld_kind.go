// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package catalog

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/pkg/entitykind"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"go.yaml.in/yaml/v3"
)

// ErrUnknownCatalogDocumentKind is the sentinel for a document under the
// catalog directories that declares the Aggrega envelope and a kind no catalog
// holds — a misspelled kind such as `Componnent`, a retired one, or none at all.
//
// A catalog indexes a document under the kind it declares, so such a document
// enters no catalog: every load passes over it, and a reference to the entity it
// meant to declare resolves to nothing. Whole-catalog validate and lint raise
// this for it, beside any document they cannot load, so one run names every such
// file. The tolerant load behind a build records it as a skipped file, and a
// build's unresolvable-reference failure lists it when it declares the name that
// reference asks for.
//
// Three kinds a catalog does not hold are legitimately found in its directories,
// and every load passes over them without a word: a Manifest and a Release,
// which are deployment-side documents a workspace may keep beside its entities,
// and a Workload, which a Recipe or FeatureRecipe imports from a file inside its
// own directory. None of them is reported here.
func ErrUnknownCatalogDocumentKind() error { return errUnknownCatalogDocumentKind }

var errUnknownCatalogDocumentKind = cerrors.Register("unknown-catalog-document-kind", cerrors.Meta{
	ShortCode: "E0371",
	Message:   "catalog document declares a kind a catalog does not hold",
	RulePlace: cerrors.RuleNoStageReaches("A whole-catalog check evaluates the rule as it reads each document under " +
		"the catalog directories before it loads them, and the tolerant load behind a build records it as it indexes " +
		"them. Neither moment is a stage of a compilation, and no stage holds the documents a catalog passed over."),
})

// heldKinds is the kinds a catalog holds, one per arm of indexKind's dispatch,
// in the vocabulary's order. It is a statement of that dispatch rather than a
// second answer to it: a coverage test holds every entity kind to it, so a kind
// gaining or losing an arm there fails until this follows.
var heldKinds = []entitykind.Kind{
	entitykind.Component,
	entitykind.Feature,
	entitykind.Recipe,
	entitykind.FeatureRecipe,
	entitykind.SchemaLibrary,
	entitykind.MappingLibrary,
	entitykind.WorkloadLibrary,
	entitykind.ArtifactLibrary,
	entitykind.Aggregate,
	entitykind.Plugin,
}

// residentKinds is the kinds a catalog does not hold that its directories
// legitimately carry: the deployment-side Manifest and Release, and the Workload
// a Recipe or FeatureRecipe imports from a file beside it.
var residentKinds = []entitykind.Kind{
	entitykind.Workload,
	entitykind.Manifest,
	entitykind.Release,
}

// kindNotHeld reports whether a document declaring kind is one a catalog holds
// no place for and does not pass over by design: every kind outside heldKinds,
// the empty kind included, except residentKinds.
func kindNotHeld(kind string) bool {
	k := entitykind.Kind(kind)
	return !slices.Contains(heldKinds, k) && !slices.Contains(residentKinds, k)
}

// unheldKindError is the occurrence naming the document at path, the kind it
// declares and the kinds a catalog holds.
func unheldKindError(path, kind string) error {
	written := fmt.Sprintf("kind %q", kind)
	if kind == "" {
		written = "no kind"
	}
	return cerrors.Wrap(
		ErrUnknownCatalogDocumentKind(),
		cerrors.CatalogError,
		cerrors.SourceLocation{File: path},
		cerrors.ComposeMessage(ErrUnknownCatalogDocumentKind(), []string{written}, heldKindsAdvice()),
	)
}

// heldKindsAdvice names the kinds a catalog holds and what the author can do.
func heldKindsAdvice() string {
	names := make([]string, len(heldKinds))
	for i, k := range heldKinds {
		names[i] = string(k)
	}
	list := strings.Join(names[:len(names)-1], ", ") + " and " + names[len(names)-1]
	return "a catalog holds " + list + " documents, and passes over a Workload, a Manifest or a Release; " +
		"correct the kind, or move the file out of the catalog directories"
}

// unheldKindDocument returns the occurrence for a document that declares the
// Aggrega envelope and a kind a catalog does not hold, and nil for every other
// document. header is the value the document's text was already decoded into,
// whose kind is read without a second parse where its type is one this package
// declares.
func unheldKindDocument(path string, data []byte, header any) error {
	var kind string
	switch h := header.(type) {
	case *documentHeader:
		kind = h.Kind
	case *entityHeader:
		kind = h.Kind
	default:
		var k entityHeader
		if yaml.Unmarshal(data, &k) != nil {
			return nil
		}
		kind = k.Kind
	}
	if !kindNotHeld(kind) {
		return nil
	}
	return unheldKindError(path, kind)
}

// unheldKindRefusal is the refusal a tolerant walk records for the document read
// from path whose header declares kind: the [ErrUnknownCatalogDocumentKind]
// occurrence where kind is one no catalog holds and the text declares the Aggrega
// envelope, and nil for every other document — YAML that is not an Aggrega
// document among them, whatever kind it names. The envelope is looked for only
// once the kind is known to be one no catalog holds, so a document of a held kind
// costs a walk nothing more.
//
// It is the one answer both tolerant walks give — the eager load's and the lazy
// view's — so the build that reads a lockfile and the one that fills it are told
// about the same file in the same words.
func unheldKindRefusal(path string, data []byte, kind string) error {
	if !kindNotHeld(kind) || !declaresAggregaEnvelope(data) {
		return nil
	}
	return unheldKindError(path, kind)
}

// KindNotHeld reports whether the file was skipped because it declares the
// Aggrega envelope and a kind a catalog does not hold, rather than refused while
// the load indexed it.
//
// The difference decides how a record is reported. A refused file declares a
// kind a catalog holds and failed as that kind, so its declared identity is the
// one a reference to it names. A file of a kind no catalog holds was never a
// candidate for the catalog, so the kind it declares names no identity; only its
// name can say which reference it was meant to serve.
func (s SkippedEntity) KindNotHeld() bool {
	return errors.Is(s.Err, ErrUnknownCatalogDocumentKind())
}
