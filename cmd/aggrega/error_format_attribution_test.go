// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package main

import (
	"strings"
	"testing"

	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
)

// The head of a printed diagnostic answers two independent questions, and the
// tests below are about the first one: which surface the reader must fix. A
// category is an answer to that question, so a category printed for a failure
// no authored surface caused is an answer to a question that has none — and the
// reader acts on it, because acting on the head is what the head is for.
//
// These tests exercise the terminal formatter rather than the classifier, and
// the distinction is the point: a classifier returning the right value proves
// nothing a reader can see. What a reader meets is the bracketed head, so the
// assertions are over rendered text reached through the same function the
// command's failure path calls.

// TestHead_InternalDisagreementNamesNoAuthoringSurface holds the property that
// a fault in the compiler is not filed against the author.
//
// The failure is produced by the production raise site rather than by a
// hand-built envelope, so the test cannot pass on a shape no build produces.
// The raise site names no document deliberately — the condition has no
// authoring document to name — and what this asserts is that the head does not
// supply one anyway.
//
// The short code is asserted present in the same breath, and that pairing is
// what stops the test passing vacuously: a head that printed nothing at all
// would satisfy "does not accuse the author" while telling the reader nothing.
// The reader still has to be able to name the condition they are reporting.
func TestHead_InternalDisagreementNamesNoAuthoringSurface(t *testing.T) {
	t.Parallel()

	const typeRef = "core/absent:1.0.0"

	finding := resolver.GuardEntityViewInconsistency(typeRef)
	if finding == nil {
		t.Fatalf("the raise site read %q as a legitimate skip; a bare catalog reference is the loud case", typeRef)
	}

	rendered := formatCompilerError(finding)
	head := firstLine(rendered)
	t.Logf("the head this condition prints: %s", head)

	for _, category := range everyCategoryTheProductDeclares() {
		if strings.Contains(head, string(category)) {
			t.Errorf("the head %q files a compiler fault under %q.\n"+
				"INVARIANT: a category answers \"which surface do I fix\", so a failure no authored "+
				"surface caused carries none.\n"+
				"CONSEQUENCE: the reader goes and reads their own document looking for a mistake "+
				"that is not there, because the head told them to.",
				head, category)
		}
	}

	code, ok := cerrors.ShortCodeOf(finding)
	if !ok {
		t.Fatal("the condition reports no short code, so this test cannot show the head still names it")
	}
	if !strings.Contains(head, code) {
		t.Errorf("the head %q does not name the condition (%s).\n"+
			"INVARIANT: dropping the category leaves the head naming what halted the build.\n"+
			"CONSEQUENCE: a reader asked to report a compiler fault has nothing to quote, which "+
			"is the one thing the condition's own wording asks them for.",
			head, code)
	}
}

// TestHead_ConditionThatNamedNoEntityCarriesNoCategory is the planted case for
// the general form: a condition-shaped finding whose raise site set no entity.
//
// The condition here is chosen for having nothing to do with catalog views. It
// stands in for any future condition whose raise site leaves the subject empty
// — which is the failure mode worth pinning, because such a raise site compiles,
// renders and halts the build correctly, and the only thing wrong with it is who
// the head blames. Nothing else in the suite would fail.
func TestHead_ConditionThatNamedNoEntityCarriesNoCategory(t *testing.T) {
	t.Parallel()

	finding := &resolver.ResolverError{
		Err: resolver.ErrFeatureMergeConflict(),
	}

	head := firstLine(formatCompilerError(finding))
	t.Logf("the head a no-entity finding prints: %s", head)

	for _, category := range everyCategoryTheProductDeclares() {
		if strings.Contains(head, string(category)) {
			t.Errorf("a finding that named no entity prints %q, filing it under %q.\n"+
				"INVARIANT: the classifier reports what it knows; it does not guess a surface "+
				"from the absence of one.\n"+
				"CONSEQUENCE: every raise site that forgets to set an entity accuses the author, "+
				"and no gate notices, because the build fails exactly as it should.",
				head, category)
		}
	}
}

// TestHead_EntityOutsideTheMappingStillFilesAtTheDeveloperSurface is the
// negative control for the two tests above, and it is what keeps them from
// being satisfied by a formatter that simply stopped printing categories.
//
// A reference whose leading word the mapping does not cover is a different fact
// from a reference that named nothing: something WAS named, and the references
// reaching this shape are the phase labels the topology and resolution walks
// supply for faults in the developer's own document. Those keep their category,
// so the reader of a real placement fault is still told which surface to edit.
func TestHead_EntityOutsideTheMappingStillFilesAtTheDeveloperSurface(t *testing.T) {
	t.Parallel()

	finding := &resolver.ResolverError{
		Entity:  "component placement",
		Field:   ".spec.components.web",
		Message: "no boundary holds the component",
		Err:     resolver.ErrFeatureMergeConflict(),
	}

	head := firstLine(formatCompilerError(finding))
	t.Logf("the head an unmapped-kind finding prints: %s", head)

	if !strings.Contains(head, string(cerrors.DeveloperError)) {
		t.Errorf("the head %q drops the category for a finding that named a subject.\n"+
			"INVARIANT: refusing to guess covers the reference that named NOTHING; a reference "+
			"naming something the mapping does not cover is still filed where its raise sites sit.\n"+
			"CONSEQUENCE: real placement and resolution faults in the author's own document stop "+
			"telling the author it is theirs, which is the opposite of the repair.",
			head)
	}
}

// everyCategoryTheProductDeclares lists the categories the head could print, so
// the assertions above can say "none of them" rather than naming one.
//
// It is written out rather than counted, and it is deliberately not asserted to
// be exhaustive: a category added beside these does not make an assertion here
// wrong, only narrower, and a test that failed whenever the set grew would
// oppose exactly the extension the set is designed for. What matters is that the
// value a wrong head would most plausibly carry is in the list.
func everyCategoryTheProductDeclares() []cerrors.Category {
	return []cerrors.Category{
		cerrors.DeveloperError,
		cerrors.OpsError,
		cerrors.CatalogError,
		cerrors.TranslationError,
		cerrors.FetchError,
		cerrors.PolicyError,
	}
}

// firstLine returns the head line of a rendered diagnostic. The head is the
// only line these tests are about: the message beneath it legitimately names
// the Aggregate, the Release and the catalog, because it is telling the reader
// there is nothing to correct in any of them.
func firstLine(rendered string) string {
	if i := strings.IndexByte(rendered, '\n'); i >= 0 {
		return rendered[:i]
	}
	return rendered
}
