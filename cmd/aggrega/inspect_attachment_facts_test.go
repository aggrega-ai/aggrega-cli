// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Tests for the per-attachment facts clause the Release layout prints beside
// each attachment: the slot it fills, and whether it handles declared-sensitive
// material. Every positive assertion here sits beside a negative one over the
// same fixture shape, because a clause that never varies satisfies a
// positive-only assertion just as well as a correct one does.

package main

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// TestAttachmentFactsBracket_EveryBindingStateAndBothSensitivities enumerates
// the whole product of the two facts rather than sampling it: three binding
// states times marked/unmarked. A display that hard-coded either fact would
// pass some rows and fail others, which is the point of asserting the exact
// bracket text rather than its presence.
//
// The final row is deliberately hostile to a substring-based assertion: the
// slot is NAMED "sensitive-material" while the attachment is not marked
// sensitive, so a check that merely looked for "sensitive" anywhere on the line
// would call an unmarked attachment marked.
func TestAttachmentFactsBracket_EveryBindingStateAndBothSensitivities(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name      string
		binding   string
		sensitive bool
		want      string
	}{
		{"unresolved slot set, unmarked", "", false, ""},
		{"unresolved slot set, marked", "", true, "[sensitive]"},
		{"free, unmarked", types.AttachmentBindingFree, false, "[free]"},
		{"free, marked", types.AttachmentBindingFree, true, "[free, sensitive]"},
		{"slot-bound, unmarked", "credentials", false, "[slot: credentials]"},
		{"slot-bound, marked", "credentials", true, "[slot: credentials, sensitive]"},
		{"slot named for the axis, unmarked", "sensitive-material", false, "[slot: sensitive-material]"},
	} {
		got := attachmentFactsBracket(types.IRFeatureEntry{
			Binding:   tc.binding,
			Sensitive: tc.sensitive,
		})
		if got != tc.want {
			t.Errorf("%s: attachmentFactsBracket = %q, want %q", tc.name, got, tc.want)
		}
	}
}

// TestAttachmentFactsBracket_EmptyBindingIsNotFree pins the distinction the
// carried field makes and the display must not collapse: free is the positive
// finding that the resolved Component declares no slot of this name, and empty
// is the absence of a finding because the slot set did not resolve. Reporting
// the second as the first would assert something the compiler never determined.
func TestAttachmentFactsBracket_EmptyBindingIsNotFree(t *testing.T) {
	t.Parallel()
	got := attachmentFactsBracket(types.IRFeatureEntry{Binding: ""})
	if got != "" {
		t.Errorf("an unresolved slot set must print no binding clause; got %q", got)
	}
	// Named separately from the equality above so the two failure modes are
	// distinguishable in the output: printing the free sentinel, and printing a
	// slot clause with an empty name.
	if strings.Contains(got, types.AttachmentBindingFree) {
		t.Errorf("an unresolved slot set must not be reported as free; got %q", got)
	}
	if strings.Contains(got, "slot:") {
		t.Errorf("an unresolved slot set must not print an empty slot clause; got %q", got)
	}
}

// uniformAttachment builds an attachment whose per-leaf provenance is uniform,
// so renderFeatureAttachment takes the compact layout.
func uniformAttachment(binding string, sensitive bool) types.IRFeatureEntry {
	return types.IRFeatureEntry{
		Type:             "feature.test/db-credential",
		Binding:          binding,
		Sensitive:        sensitive,
		Origin:           "explicit",
		Values:           orderedMapAny("secretName", "db-creds"),
		ValuesProvenance: orderedMapProvenance("secretName", explicitProvenance()),
	}
}

// heterogeneousAttachment builds an attachment whose per-leaf provenance
// diverges across pillars, so renderFeatureAttachment takes the per-leaf
// layout. Same two facts, different layout — the pair is what proves the clause
// is not confined to one branch.
func heterogeneousAttachment(binding string, sensitive bool) types.IRFeatureEntry {
	return types.IRFeatureEntry{
		Type:      "feature.test/db-credential",
		Binding:   binding,
		Sensitive: sensitive,
		Values:    orderedMapAny("secretName", "db-creds"),
		Infra:     orderedMapAny("namespace", "prod"),
		ValuesProvenance: orderedMapProvenance("secretName",
			annotationProvenance("feature.test/name", "aggregate.yaml:x", "aggregate")),
		InfraProvenance: orderedMapProvenance("namespace", explicitProvenance()),
	}
}

func renderedAttachment(t *testing.T, entry types.IRFeatureEntry) string {
	t.Helper()
	var buf bytes.Buffer
	renderFeatureAttachment(&buf, "credentials", entry, resolver.SensitiveLeaves{})
	return buf.String()
}

// TestRenderFeatureAttachment_BothLayoutsCarryTheFacts is the branch-coverage
// assertion. The compact layout and the per-leaf layout are selected by
// provenance shape, which is unrelated to either fact, so an edit that reached
// only one branch would drop a security-adjacent fact on exactly the inputs
// hardest to reason about — and would still pass any test whose fixture
// happened to have uniform provenance. Both branches are asserted here on their
// real emitted text, with the layout confirmed independently so a fixture that
// silently stopped exercising a branch fails rather than passes.
func TestRenderFeatureAttachment_BothLayoutsCarryTheFacts(t *testing.T) {
	t.Parallel()

	compact := uniformAttachment("credentials", true)
	if !featureProvenanceLayout(compact) {
		t.Fatal("fixture must select the compact layout; it no longer does")
	}
	got := renderedAttachment(t, compact)
	wantCompact := "    - credentials (feature.test/db-credential)    [slot: credentials, sensitive]    [explicit]\n"
	if got != wantCompact {
		t.Errorf("compact layout line\n got: %q\nwant: %q", got, wantCompact)
	}

	perLeaf := heterogeneousAttachment(types.AttachmentBindingFree, true)
	if featureProvenanceLayout(perLeaf) {
		t.Fatal("fixture must select the per-leaf layout; it no longer does")
	}
	got = renderedAttachment(t, perLeaf)
	wantHeader := "    - credentials (feature.test/db-credential)    [free, sensitive]\n"
	if !strings.HasPrefix(got, wantHeader) {
		t.Errorf("per-leaf layout header line\n got: %q\nwant prefix: %q", got, wantHeader)
	}
	// The leaves must still print. Two things this catches: a clause that
	// swallowed the per-leaf body would satisfy the header assertion alone, and
	// this fixture is marked sensitive at the roll-up while declaring no
	// sensitive leaf — so it also proves the new marker did not turn the
	// roll-up into a redaction trigger, which would blank the public locators
	// the roll-up deliberately does not cover.
	for _, want := range []string{"values.secretName: db-creds", "infra.namespace: prod"} {
		if !strings.Contains(got, want) {
			t.Errorf("per-leaf layout dropped %q\n--- output ---\n%s", want, got)
		}
	}
}

// TestRenderFeatureAttachment_FactsVaryWithTheEntry moves exactly one fact at a
// time against a held control, in both layouts. A display that hard-coded
// either fact — a constant "free", a constant sensitive marker — passes a
// positive-only test and fails here.
func TestRenderFeatureAttachment_FactsVaryWithTheEntry(t *testing.T) {
	t.Parallel()
	for _, layout := range []struct {
		name  string
		build func(binding string, sensitive bool) types.IRFeatureEntry
	}{
		{"compact", uniformAttachment},
		{"per-leaf", heterogeneousAttachment},
	} {
		// Sensitivity is the only variable across this pair; the binding is held.
		marked := renderedAttachment(t, layout.build("credentials", true))
		unmarked := renderedAttachment(t, layout.build("credentials", false))
		if !strings.Contains(marked, "[slot: credentials, sensitive]") {
			t.Errorf("%s: marked attachment lost its sensitivity clause\n--- output ---\n%s", layout.name, marked)
		}
		if !strings.Contains(unmarked, "[slot: credentials]") {
			t.Errorf("%s: unmarked attachment lost its slot clause\n--- output ---\n%s", layout.name, unmarked)
		}
		if strings.Contains(unmarked, ", sensitive]") {
			t.Errorf("%s: unmarked attachment was reported sensitive\n--- output ---\n%s", layout.name, unmarked)
		}

		// The binding is the only variable across this pair; sensitivity is held.
		bound := renderedAttachment(t, layout.build("credentials", false))
		free := renderedAttachment(t, layout.build(types.AttachmentBindingFree, false))
		if !strings.Contains(free, "[free]") {
			t.Errorf("%s: free attachment lost its binding clause\n--- output ---\n%s", layout.name, free)
		}
		if strings.Contains(bound, "[free]") {
			t.Errorf("%s: slot-bound attachment was reported free\n--- output ---\n%s", layout.name, bound)
		}
		if bound == free {
			t.Errorf("%s: a slot-bound and a free attachment must not render alike\n--- output ---\n%s", layout.name, bound)
		}
	}
}

// TestInspectRelease_AttachmentFactsOverRealPipeline is the end-to-end half.
// The unit tests above hand a constructed entry to the formatter, which proves
// the formatter but not that the pipeline ever populates either fact — so this
// resolves a Release through the same funnel the command uses and asserts the
// clause over an IR the compiler actually derived.
//
// The fixture attaches a Feature under a name the host Component declares as an
// attachment slot, and the attached Feature declares one sensitive leaf, so both
// facts must be present and the marked case is reached from declarations rather
// than from a literal. The already-golden Release covers the complementary case
// on the same path: its attachment fills no declared slot, so it renders free
// and unmarked.
func TestInspectRelease_AttachmentFactsOverRealPipeline(t *testing.T) {
	t.Parallel()
	const (
		release = "../../internal/compiler/testdata/sensitive-redaction/release.yaml"
		catalog = "../../internal/compiler/testdata/sensitive-redaction/catalog/base"
	)
	var buf bytes.Buffer
	opts := testInspectOptions(&buf)
	if err := dispatchInspect(context.Background(), resolveTempWorkspace(t, tempWorkspaceOptions{}), release, []string{catalog}, opts); err != nil {
		t.Fatalf("dispatchInspect(%q): %v", release, err)
	}
	got := buf.String()

	want := "    - credentials (feature.test/db-credential:1.0.0)    [slot: credentials, sensitive]    [explicit]"
	if !strings.Contains(got, want) {
		t.Errorf("resolved Release view missing %q\n--- output ---\n%s", want, got)
	}
	// The slot is named, not merely reported as filled: a display that printed
	// the free sentinel or omitted the clause would pass a laxer assertion.
	if strings.Contains(got, "[free") {
		t.Errorf("a slot-filling attachment was reported free\n--- output ---\n%s", got)
	}
}

// TestRenderFeatureAttachment_NoFactsPrintsNoClause is the no-op control. An
// attachment on a node whose Component did not resolve, declaring nothing
// sensitive, must print the line it printed before either fact existed — no
// clause, and in particular no empty bracket and no doubled separator.
func TestRenderFeatureAttachment_NoFactsPrintsNoClause(t *testing.T) {
	t.Parallel()
	compact := renderedAttachment(t, uniformAttachment("", false))
	wantCompact := "    - credentials (feature.test/db-credential)    [explicit]\n"
	if compact != wantCompact {
		t.Errorf("compact no-facts line\n got: %q\nwant: %q", compact, wantCompact)
	}

	perLeaf := renderedAttachment(t, heterogeneousAttachment("", false))
	wantHeader := "    - credentials (feature.test/db-credential)\n"
	if !strings.HasPrefix(perLeaf, wantHeader) {
		t.Errorf("per-leaf no-facts header\n got: %q\nwant prefix: %q", perLeaf, wantHeader)
	}
	if strings.Contains(perLeaf, "[]") || strings.Contains(compact, "[]") {
		t.Error("an entry carrying neither fact must not print an empty bracket")
	}
}
