// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/constraint"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// The proofs in this file cover the four rules declared over the library imports a
// node's own artifacts block writes: that the production site reaches them, that
// they agree with the resolver they stand ahead of, that the traversal enumerates a
// non-zero subject set on a legal input, that each rule declines an entry whose
// earlier resolution step already failed, that they report every offending entry
// where the resolver reports the first, and that a finding carries the authored
// position and the captioned entry a block-level locator makes it owe.
//
// Two of them are load-bearing and neither is a formality.
//
// The AGREEMENT proof, because the rules decide from declarations alone what the
// resolver decides while resolving — so nothing but a measured comparison
// establishes that the two decide the same thing, and a rule that disagreed would
// either refuse a document the compiler admits today or admit one it refuses.
//
// The ENUMERATION proof, because this kind exists precisely to be reachable for an
// entry whose import fails. A rule invoked zero times and a rule that found nothing
// are indistinguishable from the outcome, so a verdict taken over a walk that
// reached no subject would certify nothing at all.

const (
	// artifactsEntryBundleBody is a bundle file body carrying a template
	// expression, so a fixture that reaches evaluation exercises it rather than
	// comparing two static strings.
	artifactsEntryBundleBody = `apiVersion: v1
kind: ConfigMap
metadata:
  name: {{ .context.instance_id }}-settings
`

	// artifactsEntryNestedBody is a bundle file body that is itself a field-root
	// import, which one file slot cannot expand into another bundle for.
	artifactsEntryNestedBody = "$import: Shared#/artifacts/Settings"
)

// artifactsEntryLibraries is the resolved ArtifactLibrary set every row below is
// driven against: one well-formed bundle, one whose file body imports another
// bundle, and one whose file name climbs out of the key it would be nested under.
func artifactsEntryLibraries() map[string]types.ArtifactLibrary {
	return map[string]types.ArtifactLibrary{
		"Shared": artifactLibraryFixtureOrdered("Shared", "Settings",
			[]string{"settings.yaml"}, []string{artifactsEntryBundleBody}),
		"Nested": artifactLibraryFixtureOrdered("Nested", "Bad",
			[]string{"one.yaml"}, []string{artifactsEntryNestedBody}),
		"Climbing": artifactLibraryFixtureOrdered("Climbing", "Escapes",
			[]string{"../outside.yaml"}, []string{artifactsEntryBundleBody}),
	}
}

// artifactsEntryRecipe is the authored document both paths are driven with, so the
// comparison is over one Recipe rather than over two fixtures that agree today.
//
// entries are key/reference pairs in author order.
func artifactsEntryRecipe(entries ...string) *types.Recipe {
	r := &types.Recipe{}
	r.Metadata.Name = "artifacts-entry-recipe"
	for i := 0; i+1 < len(entries); i += 2 {
		r.Spec.Artifacts.Set(entries[i], types.ArtifactEntry{Import: entries[i+1]})
	}
	return r
}

// declaredArtifactsEntryHalt drives the four declared rules through the PRODUCTION
// invocation site over one authored Recipe, with the real presentation and the
// real enumeration, and returns the halt.
//
// Both sides are production code: the presentation is the one the render builds
// and the axis is the one the render derives, so the verdict is the declared
// route's rather than a second reading of the Recipe assembled here.
//
// The subject count is measured before the verdict is taken, because a verdict
// from a walk that reached no subject reports exactly what an admitted block
// reports — and this kind exists to be reachable for the entries whose resolution
// fails, which is the claim that would otherwise go unproved.
func declaredArtifactsEntryHalt(
	t *testing.T, recipe *types.Recipe, libs map[string]types.ArtifactLibrary,
) error {
	t.Helper()
	const node = "api"
	recipes := map[string]*types.Recipe{node: recipe}

	nodes := types.OrderedMap[types.IRNode]{}
	nodes.Set(node, types.IRNode{})
	graph := &types.IRGraph{Spec: types.IRSpec{Nodes: nodes}}

	view := newDescribedView(
		context.Background(), graph, stubGraphFacts{},
		recipes, RecipeLookup{}, nil, newStepDescriptorMemo(), nil, nil,
		nil, libs, nil, nil,
	)
	if view == nil {
		t.Fatal("the view was not built; there is nothing for the rules to read")
	}
	axes := newDescribedStageCoordinates(graph, localsAxis{nodes: []string{node}}, recipes, RecipeLookup{}, nil)
	if axes == nil {
		t.Fatal("the enumeration refused to build over a graph and a bound Recipe")
	}
	if got, want := len(axes.ArtifactsEntries(node)), recipe.Spec.Artifacts.Len(); got != want {
		t.Fatalf("the axis presents %d artifacts entr(ies) for a Recipe declaring %d; the verdict below would certify nothing", got, want)
	}
	return runArtifactsEntryImportRule(context.Background(), view, axes)
}

// resolvedArtifactsEntryHalt drives the SAME authored Recipe through the artifacts
// walk the declared rules stand ahead of, and returns its halt.
//
// It is the other side of the agreement comparison, and it is the production walk
// rather than a direct call on the resolver: what the rules have to agree with is
// what a build does, and the walk is where the entry's authoring mode is dispatched
// on before the resolver is reached at all.
func resolvedArtifactsEntryHalt(
	t *testing.T, recipe *types.Recipe, libs map[string]types.ArtifactLibrary,
) error {
	t.Helper()
	tmplCtx, state, nodeCtx := newArtifactsCtx()
	resetArtifacts(state)
	for _, key := range recipe.Spec.Artifacts.Keys() {
		entry, _ := recipe.Spec.Artifacts.Get(key)
		state.Recipe.Spec.Artifacts.Set(key, entry)
	}
	state.ArtifactLibraries = libs
	_, err := buildArtifactsProjection(nil, tmplCtx, state, nodeCtx, "{{", "}}")
	return err
}

// artifactsEntryCase is one authored artifacts entry plus the condition both paths
// must reach over it. want is nil for an entry both admit.
//
// Every row carries AT MOST ONE offending entry, which is what makes the two
// readings comparable at all: the walk returns one error while the declared rules
// are invoked once per entry and report every one. The breadth difference is
// measured on its own, below.
type artifactsEntryCase struct {
	name string
	key  string
	ref  string
	want error
}

func artifactsEntryCases() []artifactsEntryCase {
	return []artifactsEntryCase{
		{
			name: "a bundle that resolves is admitted",
			key:  "settings",
			ref:  "Shared#/artifacts/Settings",
		},
		{
			name: "the retired version separator is the shape class",
			key:  "settings",
			ref:  "Shared@1.0.0#/artifacts/Settings",
			want: ErrArtifactImportShape(),
		},
		{
			name: "a version floor written with a colon resolves",
			key:  "settings",
			ref:  "Shared:1.0.0#/artifacts/Settings",
		},
		{
			name: "a library nothing declares",
			key:  "settings",
			ref:  "Absent#/artifacts/Settings",
			want: ErrArtifactLibraryNotFound(),
		},
		{
			name: "a bundle the library does not declare",
			key:  "settings",
			ref:  "Shared#/artifacts/Missing",
			want: ErrArtifactLibraryEntryNotFound(),
		},
		{
			name: "a bundle file body that imports another bundle is the shape class",
			key:  "settings",
			ref:  "Nested#/artifacts/Bad",
			want: ErrArtifactImportShape(),
		},
		{
			name: "a bundle file name climbing out of its key",
			key:  "settings",
			ref:  "Climbing#/artifacts/Escapes",
			want: ErrArtifactLibraryFileEscapesKey(),
		},
	}
}

// TestArtifactsEntryImportRules_AgreeWithTheWalkTheyStandAheadOf is the
// load-bearing proof: over one authored Recipe, the declared rules and the
// artifacts walk reach the same condition — the same sentinel where either
// refuses, and silence from both where the entry resolves.
//
// A disagreement in either direction is a defect the declaration introduced: the
// rules halt a build before the walk runs, so a rule refusing what the walk admits
// stops a build that renders today, and a rule admitting what the walk refuses
// leaves the walk's own arm doing the work the declaration claims to do.
func TestArtifactsEntryImportRules_AgreeWithTheWalkTheyStandAheadOf(t *testing.T) {
	t.Parallel()

	for _, tc := range artifactsEntryCases() {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			recipe := artifactsEntryRecipe(tc.key, tc.ref)
			libs := artifactsEntryLibraries()

			declared := declaredArtifactsEntryHalt(t, recipe, libs)
			walked := resolvedArtifactsEntryHalt(t, recipe, libs)

			if tc.want == nil {
				if declared != nil {
					t.Errorf("the declared rules refused an entry the walk admits: %v", declared)
				}
				if walked != nil {
					t.Errorf("the walk refused the entry the rules admit: %v", walked)
				}
				return
			}
			if declared == nil {
				t.Fatalf("the declared rules admitted an entry the walk refuses with %v", tc.want)
			}
			if !errors.Is(declared, tc.want) {
				t.Errorf("declared rules reported %v; want %v", declared, tc.want)
			}
			if walked == nil {
				t.Fatalf("the walk admitted %q, so the two paths cannot be compared over it", tc.ref)
			}
			if !errors.Is(walked, tc.want) {
				t.Errorf("the walk reported %v; want %v", walked, tc.want)
			}
		})
	}
}

// TestArtifactsEntryImportRules_CarryTheAuthoredPositionAndTheCaptionedEntry is
// clause-for-clause the difference the declaration makes, measured rather than
// asserted: the walk's own refusal names the Recipe and stops, while the declared
// finding names the artifacts block inside it and captions the entry the block
// holds.
//
// The locator is the block and not the entry, and that is the axis's property
// rather than this rule's shortfall: an artifacts key is the output's filename, so
// it carries the locator dialect's separator as one key that dialect cannot
// address. The captioned entry in the detail is what the rule owes because of it,
// which is exactly what this measures.
func TestArtifactsEntryImportRules_CarryTheAuthoredPositionAndTheCaptionedEntry(t *testing.T) {
	t.Parallel()

	const key = "settings.yaml"
	recipe := artifactsEntryRecipe(key, "Absent#/artifacts/Settings")
	libs := artifactsEntryLibraries()

	declared := declaredArtifactsEntryHalt(t, recipe, libs)
	if declared == nil {
		t.Fatal("the declared rules admitted a library nothing declares")
	}
	walked := resolvedArtifactsEntryHalt(t, recipe, libs)
	if walked == nil {
		t.Fatal("the walk admitted a library nothing declares")
	}

	before, after := walked.Error(), declared.Error()
	t.Logf("before (the walk's own refusal): %s", before)
	t.Logf("after  (the declared rule):      %s", after)

	// The position. The walk names a document and no key inside it; the
	// declaration names the artifacts block, which is the deepest addressable site
	// on this axis.
	if strings.Contains(before, " at .") {
		t.Errorf("the walk's refusal already carries an authored locator, so the declaration adds none: %s", before)
	}
	if !strings.Contains(after, "artifacts-entry-recipe at .spec.artifacts") {
		t.Errorf("the declared finding does not carry the framework-supplied position: %s", after)
	}
	// The key ends the locator rather than extending it, because it carries the
	// dialect's separator. A locator spelling it would name a path the document
	// does not contain.
	if strings.Contains(after, ".spec.artifacts."+key) {
		t.Errorf("the locator spelled a key containing the dialect separator: %s", after)
	}

	// The detail. The library the sentence is about stands bare; the entry the
	// locator could not reach is captioned.
	if !strings.Contains(after, `"Absent"`) {
		t.Errorf("the finding does not name the library its sentence is about: %s", after)
	}
	if !strings.Contains(after, `artifacts entry "`+key+`"`) {
		t.Errorf("the finding does not caption the entry the locator stops short of: %s", after)
	}
	// And the identity, which is read off the chain rather than off the rendering.
	// A single-line rendering is the category, the position and the sentence; no
	// condition's short code appears in it, before or after, because the code is
	// carried by the registered condition the chain names and is composed into the
	// head of a diagnostic by the surface that formats one for a terminal. A
	// substring assertion over this string would therefore pin a property the
	// rendering has for no condition, and pass only where the code happened to
	// occur inside the sentence.
	if code, ok := cerrors.ShortCodeOf(declared); !ok || code != "E0031" {
		t.Errorf("the finding carries short code %q (present=%v); want E0031 — the condition the "+
			"declaration registers is what a formatter names the diagnostic by", code, ok)
	}
}

// TestArtifactsEntryImportRules_EnumerateEveryAuthoredEntry is clause 3's
// refutation driven rather than argued: the traversal reaches a non-zero subject
// set on a LEGAL input, so a rule that found nothing is distinguishable from a rule
// nothing invoked.
//
// It also measures the point of the kind. The artifact axis is built from what the
// render projected, so it presents the bundle's expanded outputs and presents
// nothing at all for an entry whose import failed; this axis presents the authored
// keys either way, which is why the counts below differ in the stated direction.
func TestArtifactsEntryImportRules_EnumerateEveryAuthoredEntry(t *testing.T) {
	t.Parallel()

	const node = "api"
	recipe := artifactsEntryRecipe(
		"settings", "Shared#/artifacts/Settings",
		"plain", "",
	)
	// The second entry is authored inline rather than imported, so the axis has to
	// present an entry no rule below will judge: an axis narrowed to library
	// imports would make every "no finding" result unreadable.
	recipe.Spec.Artifacts.Set("plain", types.ArtifactEntry{Inline: artifactsEntryBundleBody})

	recipes := map[string]*types.Recipe{node: recipe}
	nodes := types.OrderedMap[types.IRNode]{}
	nodes.Set(node, types.IRNode{})
	graph := &types.IRGraph{Spec: types.IRSpec{Nodes: nodes}}

	axes := newDescribedStageCoordinates(graph, localsAxis{nodes: []string{node}}, recipes, RecipeLookup{}, nil)
	if axes == nil {
		t.Fatal("the enumeration refused to build over a graph and a bound Recipe")
	}
	entries := axes.ArtifactsEntries(node)
	if len(entries) != 2 {
		t.Fatalf("the axis presented %d entr(ies); the Recipe writes 2", len(entries))
	}
	// Author order is the fact, so the sequence is asserted rather than the set.
	if entries[0].Value != "settings" || entries[1].Value != "plain" {
		t.Errorf("the axis presented %q then %q; the Recipe declares settings then plain",
			entries[0].Value, entries[1].Value)
	}
	// Each coordinate is positioned at the Recipe, which is the document an author
	// edits to change the entry.
	for _, entry := range entries {
		if entry.SourceFile != recipe.Metadata.Name {
			t.Errorf("entry %q is positioned at %q; want the Recipe %q",
				entry.Value, entry.SourceFile, recipe.Metadata.Name)
		}
	}

	// And the legal input is admitted, so the non-zero count above is a statement
	// about a walk that ran rather than about one that raised.
	if err := declaredArtifactsEntryHalt(t, recipe, artifactsEntryLibraries()); err != nil {
		t.Errorf("a legal Recipe was refused: %v", err)
	}
}

// TestArtifactsEntryImportRules_ReportEveryOffendingEntry measures the breadth the
// declaration adds: the walk returns at its first offending entry, while the rules
// are invoked once per entry and report all of them, each at its own position.
func TestArtifactsEntryImportRules_ReportEveryOffendingEntry(t *testing.T) {
	t.Parallel()

	recipe := artifactsEntryRecipe(
		"first", "Absent#/artifacts/Settings",
		"second", "AlsoAbsent#/artifacts/Settings",
		"third", "Shared#/artifacts/Missing",
	)
	declared := declaredArtifactsEntryHalt(t, recipe, artifactsEntryLibraries())
	if declared == nil {
		t.Fatal("three offending entries were admitted")
	}
	text := declared.Error() + "\n" + strings.Join(causeLines(declared), "\n")
	for _, want := range []string{`artifacts entry "first"`, `artifacts entry "second"`, `artifacts entry "third"`} {
		if !strings.Contains(text, want) {
			t.Errorf("the halt does not name %s; an author would correct one entry per build:\n%s", want, text)
		}
	}
	// The walk beneath reports exactly one of them, which is what makes the line
	// above a measurement rather than a restatement.
	walked := resolvedArtifactsEntryHalt(t, recipe, artifactsEntryLibraries())
	if walked == nil {
		t.Fatal("the walk admitted three offending entries")
	}
	if strings.Contains(walked.Error(), "AlsoAbsent") {
		t.Errorf("the walk reported a later entry as well as the first: %v", walked)
	}
}

// TestArtifactsEntryImportRules_DeclineAnEntryWhoseEarlierStepFailed proves the
// cascade is respected: one authoring mistake produces one finding, not one per
// step of the resolution it prevented.
//
// Without it a reference naming an absent library would also be reported as a
// bundle the library does not declare, which is true and is not a separate fault —
// and a build reporting both would have the author correct one thing twice.
func TestArtifactsEntryImportRules_DeclineAnEntryWhoseEarlierStepFailed(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		ref     string
		want    error
		absent  []error
		wantOne bool
	}{
		{
			name:    "the retired separator is not also reported as an absent library",
			ref:     "Shared@1.0.0#/artifacts/Settings",
			want:    ErrArtifactImportShape(),
			absent:  []error{ErrArtifactLibraryNotFound(), ErrArtifactLibraryEntryNotFound()},
			wantOne: true,
		},
		{
			name:    "an absent library is not also reported as an absent bundle",
			ref:     "Absent#/artifacts/Settings",
			want:    ErrArtifactLibraryNotFound(),
			absent:  []error{ErrArtifactLibraryEntryNotFound(), ErrArtifactImportShape()},
			wantOne: true,
		},
		{
			name:    "an absent bundle is not also reported as an escaping file name",
			ref:     "Shared#/artifacts/Missing",
			want:    ErrArtifactLibraryEntryNotFound(),
			absent:  []error{ErrArtifactLibraryFileEscapesKey(), ErrArtifactLibraryNotFound()},
			wantOne: true,
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			declared := declaredArtifactsEntryHalt(t, artifactsEntryRecipe("settings", tc.ref), artifactsEntryLibraries())
			if declared == nil {
				t.Fatalf("%q was admitted", tc.ref)
			}
			if !errors.Is(declared, tc.want) {
				t.Errorf("reported %v; want %v", declared, tc.want)
			}
			for _, unwanted := range tc.absent {
				if errors.Is(declared, unwanted) {
					t.Errorf("one authoring mistake was also reported as %v: %v", unwanted, declared)
				}
			}
		})
	}
}

// TestArtifactsEntryImportRules_AreNotInvokedOverAnEntryCarryingNoLibraryReference
// states the bound rather than leaving it to be found: an entry written inline and
// an entry supplied by a file of the workspace carry no library reference, so every
// rule declines them — and a host-bearing reference is refused where it is consumed
// rather than read as a library one.
func TestArtifactsEntryImportRules_AreNotInvokedOverAnEntryCarryingNoLibraryReference(t *testing.T) {
	t.Parallel()

	recipe := &types.Recipe{}
	recipe.Metadata.Name = "artifacts-entry-recipe"
	recipe.Spec.Artifacts.Set("inline", types.ArtifactEntry{Inline: artifactsEntryBundleBody})
	recipe.Spec.Artifacts.Set("from-file", types.ArtifactEntry{Import: "/workspace/fragment.yaml"})
	recipe.Spec.Artifacts.Set("host-bearing", types.ArtifactEntry{Import: "oci://hub/Shared#/artifacts/Settings"})

	if err := declaredArtifactsEntryHalt(t, recipe, artifactsEntryLibraries()); err != nil {
		t.Errorf("a rule over library imports judged an entry that carries none: %v", err)
	}
}

// TestArtifactsEntryImportRules_ReadTheFactsTheirDeclarationNames drives the five
// facts through the real presentation, so the accessors a rule reads are measured
// against a bundle rather than trusted.
//
// A fact answering emptily would leave every rule above silent, and silence is
// indistinguishable from approval — which is why the facts are read here directly
// as well as through the rules.
func TestArtifactsEntryImportRules_ReadTheFactsTheirDeclarationNames(t *testing.T) {
	t.Parallel()

	const node = "api"
	recipe := artifactsEntryRecipe(
		"settings", "Shared#/artifacts/Settings",
		"nested", "Nested#/artifacts/Bad",
		"climbing", "Climbing#/artifacts/Escapes",
	)
	recipes := map[string]*types.Recipe{node: recipe}
	nodes := types.OrderedMap[types.IRNode]{}
	nodes.Set(node, types.IRNode{})
	graph := &types.IRGraph{Spec: types.IRSpec{Nodes: nodes}}

	view := newDescribedView(
		context.Background(), graph, stubGraphFacts{},
		recipes, RecipeLookup{}, nil, newStepDescriptorMemo(), nil, nil,
		nil, artifactsEntryLibraries(), nil, nil,
	)
	if view == nil {
		t.Fatal("the view was not built")
	}

	if got := view.ArtifactsEntryImportReference(node, "settings"); got != "Shared#/artifacts/Settings" {
		t.Errorf("the reference fact answered %q", got)
	}
	if !view.ArtifactsEntryLibraryDeclared(node, "settings") {
		t.Error("a library the set holds was reported absent")
	}
	if !view.ArtifactsEntryBundleDeclared(node, "settings") {
		t.Error("a bundle the library declares was reported absent")
	}
	if got := view.ArtifactsEntryBundleFileNames(node, "settings"); len(got) != 1 || got[0] != "settings.yaml" {
		t.Errorf("the bundle file names answered %v; want [settings.yaml]", got)
	}
	if got := view.ArtifactsEntryBundleNestedImportFiles(node, "settings"); len(got) != 0 {
		t.Errorf("a bundle of template bodies reported %v as field-root imports", got)
	}
	if got := view.ArtifactsEntryBundleNestedImportFiles(node, "nested"); len(got) != 1 || got[0] != "one.yaml" {
		t.Errorf("the nested-import fact answered %v; want [one.yaml]", got)
	}
	if got := view.ArtifactsEntryBundleFileNames(node, "climbing"); len(got) != 1 || got[0] != "../outside.yaml" {
		t.Errorf("the bundle file names answered %v; want [../outside.yaml]", got)
	}
	// A key the Recipe does not declare names nothing, which is the honest reading
	// of an enumerated coordinate rather than a looked-up one.
	if got := view.ArtifactsEntryImportReference(node, "absent"); got != "" {
		t.Errorf("a key the Recipe does not write answered %q", got)
	}
}

// causeLines flattens a joined halt into the text of every diagnostic beneath it,
// so a proof about several findings reads all of them rather than the envelope
// alone.
func causeLines(err error) []string {
	var out []string
	var walk func(error)
	walk = func(e error) {
		if e == nil {
			return
		}
		out = append(out, e.Error())
		switch t := e.(type) {
		case interface{ Unwrap() []error }:
			for _, inner := range t.Unwrap() {
				walk(inner)
			}
		case interface{ Unwrap() error }:
			walk(t.Unwrap())
		}
	}
	walk(err)
	return out
}

// The enumeration the render derives is held to the axis set these rules are
// invoked over, so an axis that stopped answering would fail to build here rather
// than leave the rules invoked over an empty subject set.
var _ artifactsEntryImportAxes = (*describedStageCoordinates)(nil)

// TestArtifactsEntryKindIsDerivedWithItsCoordinates reads the derived kind set for
// the coordinate pair these rules are keyed by.
//
// The set is derived from the facts rather than declared, so a kind disappears the
// moment the last fact keyed by it is removed — and a check whose subject type no
// traversal reaches would still compile. This asserts the derivation rather than
// the declaration.
func TestArtifactsEntryKindIsDerivedWithItsCoordinates(t *testing.T) {
	t.Parallel()

	for _, info := range constraint.SubjectKinds {
		if info.Kind != constraint.SubjectNodeArtifactsEntry {
			continue
		}
		if got := strings.Join(info.Coordinates, ", "); got != "node, artifacts-entry" {
			t.Errorf("the kind's coordinates are %q; want \"node, artifacts-entry\"", got)
		}
		if info.Traversal != constraint.TraversalPull {
			t.Errorf("the kind is reached by the %q model; a node-rooted kind is walked", info.Traversal)
		}
		return
	}
	t.Fatal("no derived kind is keyed by (node, artifacts-entry); the facts that open it are gone")
}
