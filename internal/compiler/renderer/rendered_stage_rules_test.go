// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package renderer

import (
	"context"
	"errors"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/constraint"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/rendercore"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
)

// The proofs in this file cover the site that invokes the rules declared over what
// a render assembled: that the rule about a surviving cross-boundary link
// placeholder is genuinely reached, that it reports every offending file rather
// than the first, that the walk observes the caller's cancellation signal per
// iteration, that the halt is attributed to the translation rather than to a
// catalog document, that the rule about two contributors writing one output path
// is reached from the same site and attributed to a catalog document instead, that
// one collision is reported once however many boundaries fed the group that wrote
// it, and that the enumeration is built from the attribution the assembly
// recorded.

// The cancellation and counting fixture. Three groups, each with one provider
// writing two files, so a walk stopped after the first group's turn is a
// different number from a walk that ran to the end.
const linkGroupCount, linkPathsPerGroup = 3, 2

func linkGroupIDs() []string { return []string{"prod/eu", "prod/us", "stage"} }

// assembledOutputHolding presents a completed render whose attribution records one
// group per named boundary, one provider each, holding one entry per path with the
// same bytes.
//
// It builds the attribution the flush records rather than the assembly's working
// groups, because that attribution is what the presentation and the enumeration
// both read: a fixture built from the working groups would exercise neither.
func assembledOutputHolding(boundaries []string, provider string, files map[string][]byte) *RenderResult {
	res := &RenderResult{Files: make(map[string]AssembledFile, len(files)*len(boundaries))}
	paths := make([]string, 0, len(files))
	for path := range files {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	for _, boundary := range boundaries {
		outputPaths := make([]string, 0, len(paths))
		for _, path := range paths {
			full := boundary + "/" + path
			res.Files[full] = AssembledFile{Content: files[path]}
			outputPaths = append(outputPaths, full)
		}
		res.Contributions = append(res.Contributions, AssemblyContribution{
			AssemblyPath: boundary,
			Provider:     provider,
			Boundaries:   []string{boundary},
			OutputPaths:  outputPaths,
		})
	}
	res.artifacts = newArtifactProjections()
	return res
}

// orphanCrossBoundaryLinkHalt asks the declared rule about a render's assembled
// output, through the production invocation site rather than by calling the rule's
// predicate directly, and returns the halt it produced.
//
// It asserts the walk reached something. A helper returning nil because the
// enumeration presented no path would report exactly what clean output reports.
func orphanCrossBoundaryLinkHalt(t *testing.T, res *RenderResult) error {
	t.Helper()
	view := presentationOverTheRender(t, res)
	coordinates := newAssemblyGroupCoordinates(res, "release.yaml")
	if coordinates == nil {
		t.Fatal("the enumeration refused to build over a render carrying attribution")
	}
	subjects := 0
	for _, path := range coordinates.AssemblyPaths() {
		for _, provider := range coordinates.AssemblyGroupProviders(path.Value) {
			subjects += len(res.EmittedPaths(path.Value, provider))
		}
	}
	if subjects == 0 {
		t.Fatal("the enumeration presented no output path; a verdict from it would certify nothing")
	}
	return runRenderedStageRules(context.Background(), true, view, coordinates)
}

// TestTheAssembledLinkPlaceholderRuleIsReachedFromTheRenderSite proves the
// invocation site reaches the rule, and that its finding carries the coordinates
// an operator needs to identify the file.
func TestTheAssembledLinkPlaceholderRuleIsReachedFromTheRenderSite(t *testing.T) {
	t.Parallel()

	clean := assembledOutputHolding([]string{"prod"}, "cloudformation", map[string][]byte{
		"cloudformation-stack.json": []byte(`{"Resources":{}}`),
	})
	if err := orphanCrossBoundaryLinkHalt(t, clean); err != nil {
		t.Fatalf("the site refused assembled output carrying no placeholder: %v", err)
	}

	sentinel := rendercore.CrossBoundaryLinkSentinel("api", "db", "database", "host")
	offending := assembledOutputHolding([]string{"prod"}, "cloudformation", map[string][]byte{
		"cloudformation-stack.json": []byte(`{"host":"` + sentinel + `"}`),
	})
	err := orphanCrossBoundaryLinkHalt(t, offending)
	if err == nil {
		t.Fatal("the site admitted assembled output retaining a link placeholder")
	}
	if !errors.Is(err, ErrOrphanCrossBoundaryLinkSentinel()) {
		t.Errorf("halt condition = %v, want the unresolved-placeholder condition", err)
	}
	// The rendered diagnostic: the provider and the path the detail names. It
	// carries no boundary locator, and that is the shape rather than a loss — the
	// finding is about a group, a group several boundaries fed has no single
	// authored site, and this fault is the translation's rather than any
	// document's.
	text := err.Error()
	for _, want := range []string{"cloudformation", "prod/cloudformation-stack.json"} {
		if !strings.Contains(text, want) {
			t.Errorf("the halt omits %q, so the offending file cannot be identified: %s", want, text)
		}
	}
	if strings.Contains(text, ".spec.boundaries.") {
		t.Errorf("the halt positions itself at a boundary, which a group-scoped finding must not claim: %s", text)
	}
}

// TestTheAssembledCollisionHaltIsACatalogFault pins the attribution of the other
// rule this site invokes, and that the collision reading is reached at all.
//
// Two contributors meeting on one output path IS something authors wrote: two
// documents chose output names that meet, and the remedy is an edit to one of
// them. So the fault is a catalog one, where a surviving link placeholder — a
// marker the host stamped and a provider failed to replace — is not. The category
// is part of the condition's published identity, so it is asserted rather than
// left to the shared rendering's default.
func TestTheAssembledCollisionHaltIsACatalogFault(t *testing.T) {
	t.Parallel()

	res := assembledOutputHolding([]string{"prod"}, "cloudformation", map[string][]byte{
		"stack.json": []byte(`{}`),
	})
	// CONTROL — the same attribution with one contributor per path admits, so a
	// halt below cannot come from the fixture's shape.
	res.Contributions[0].PathProducers = map[string][]string{
		"prod/stack.json": {"component api"},
	}
	if err := orphanCrossBoundaryLinkHalt(t, res); err != nil {
		t.Fatalf("the site refused output whose every path has one contributor: %v", err)
	}

	res.Contributions[0].PathProducers = map[string][]string{
		"prod/stack.json": {"component api", "the cloudformation assembly"},
	}
	err := orphanCrossBoundaryLinkHalt(t, res)
	if err == nil {
		t.Fatal("the site admitted an output path two contributors wrote")
	}
	if !errors.Is(err, ErrAssembledOutputCollision()) {
		t.Fatalf("halt condition = %v, want the assembled-output collision", err)
	}
	var diag *cerrors.CompilerError
	if !errors.As(err, &diag) {
		t.Fatalf("the halt is not a structured diagnostic: %v", err)
	}
	if diag.Category() != cerrors.CatalogError {
		t.Errorf("halt category = %q, want %q — two documents chose names that meet, and the remedy is an edit to one",
			diag.Category(), cerrors.CatalogError)
	}
	text := err.Error()
	for _, want := range []string{
		`output path "prod/stack.json"`,
		`written by "component api and the cloudformation assembly"`,
		`assembled within "prod"`,
	} {
		if !strings.Contains(text, want) {
			t.Errorf("the halt omits %q, so the offending path cannot be identified: %s", want, text)
		}
	}
	if strings.Contains(text, ".spec.boundaries.") {
		t.Errorf("the halt positions itself at a boundary rather than naming the group's boundaries in its detail: %s", text)
	}
}

// TestTheAssembledLinkPlaceholderHaltIsATranslationFault pins the attribution.
//
// A surviving placeholder is not something an author wrote — the host stamps the
// marker and a provider's link resolution is meant to replace it — so reporting it
// as a catalog fault would send an author looking through a document that is not
// at fault. The category is part of the condition's published identity, so it is
// asserted rather than left to the shared rendering's default.
func TestTheAssembledLinkPlaceholderHaltIsATranslationFault(t *testing.T) {
	t.Parallel()

	sentinel := rendercore.CrossBoundaryLinkSentinel("api", "db", "database", "host")
	res := assembledOutputHolding([]string{"prod"}, "cloudformation", map[string][]byte{
		"stack.json": []byte(`{"host":"` + sentinel + `"}`),
	})
	err := orphanCrossBoundaryLinkHalt(t, res)
	if err == nil {
		t.Fatal("the site admitted assembled output retaining a link placeholder")
	}
	var diag *cerrors.CompilerError
	if !errors.As(err, &diag) {
		t.Fatalf("the halt is not a structured diagnostic: %v", err)
	}
	if diag.Category() != cerrors.TranslationError {
		t.Errorf("halt category = %q, want %q — the fault is on the translation side and no document is at fault",
			diag.Category(), cerrors.TranslationError)
	}
}

// TestTheAssembledLinkPlaceholderRuleReportsEveryOffendingFile is the
// diagnostic-count proof: an input carrying more than one offending file produces
// one finding per file.
func TestTheAssembledLinkPlaceholderRuleReportsEveryOffendingFile(t *testing.T) {
	t.Parallel()

	sentinel := rendercore.CrossBoundaryLinkSentinel("api", "db", "database", "host")
	marked := []byte(`{"host":"` + sentinel + `"}`)
	// Two groups, two files each, three of the four carrying a placeholder.
	res := &RenderResult{Files: map[string]AssembledFile{
		"prod/a.json":  {Content: marked},
		"prod/b.json":  {Content: marked},
		"stage/a.json": {Content: marked},
		"stage/b.json": {Content: []byte(`{"host":"resolved"}`)},
	}}
	res.artifacts = newArtifactProjections()
	res.Contributions = []AssemblyContribution{
		{AssemblyPath: "prod", Provider: "cloudformation", Boundaries: []string{"prod"},
			OutputPaths: []string{"prod/a.json", "prod/b.json"}},
		{AssemblyPath: "stage", Provider: "cloudformation", Boundaries: []string{"stage"},
			OutputPaths: []string{"stage/a.json", "stage/b.json"}},
	}

	err := orphanCrossBoundaryLinkHalt(t, res)
	if err == nil {
		t.Fatal("the site admitted output carrying three placeholders")
	}
	leaves := haltLeafMessages(err)
	const offenders = 3
	if len(leaves) != offenders {
		t.Fatalf("the halt carries %d diagnostic(s) for %d offending files: %v", len(leaves), offenders, leaves)
	}
	joined := strings.Join(leaves, "\n")
	for _, want := range []string{"prod/a.json", "prod/b.json", "stage/a.json"} {
		if !strings.Contains(joined, want) {
			t.Errorf("no diagnostic names the offending file %q: %s", want, joined)
		}
	}
	if strings.Contains(joined, "stage/b.json") {
		t.Errorf("a diagnostic names stage/b.json, which carries no placeholder: %s", joined)
	}
	t.Logf("three offending files across two groups -> %d diagnostic(s)", len(leaves))
}

// assembledContentReads answers the one fact the rule reads and counts the reads,
// so a stopped walk is measured by the subjects it reached rather than by the
// error it returned.
type assembledContentReads struct {
	constraint.RenderedView

	content  []byte
	reads    *int
	withdraw func()
	// triggerAt is the 1-based read at which the caller withdraws, or 0 for a
	// caller that never does.
	triggerAt int
}

func (v assembledContentReads) AssembledFileContent(_ string, _ string, _ string) []byte {
	*v.reads++
	if v.triggerAt != 0 && *v.reads == v.triggerAt && v.withdraw != nil {
		v.withdraw()
	}
	return v.content
}

// TestTheAssembledLinkPlaceholderWalkStopsOnTheIterationAfterTheSignalTurns
// proves the invocation observes the caller's signal per iteration rather than
// reporting a withdrawal after a completed walk.
//
// # The trigger index is derived from THIS walk's order
//
// The walk is outermost by assembly path, then by provider, and the signal is read
// once per assembly path — as the first statement of the outer loop's body. Each
// rule then walks the paths of the group it was invoked for. So a withdrawal inside
// the FIRST group's first read is seen at the second group's signal read: the first
// group's remaining paths are read, and no later group is entered at all. The
// reached count is therefore exactly one group's worth of paths.
//
// # Why the count and not the error
//
// A walk that ran to the end and reported the withdrawal afterwards returns the
// same error as one that stopped partway. The exact equality below is what
// separates them, and the control beside it is what stops the pair passing on a
// walk that reaches nothing.
func TestTheAssembledLinkPlaceholderWalkStopsOnTheIterationAfterTheSignalTurns(t *testing.T) {
	t.Parallel()

	total := linkGroupCount * linkPathsPerGroup
	if linkGroupCount < 2 || linkPathsPerGroup < 1 {
		t.Fatalf("the fixture holds %d group(s) of %d path(s); one group's worth and the whole walk are the same number",
			linkGroupCount, linkPathsPerGroup)
	}
	files := map[string][]byte{}
	for i := range linkPathsPerGroup {
		files["f"+strconv.Itoa(i)+".json"] = []byte(`{}`)
	}
	res := assembledOutputHolding(linkGroupIDs(), "cloudformation", files)
	coordinates := newAssemblyGroupCoordinates(res, "release.yaml")
	if coordinates == nil {
		t.Fatal("the enumeration refused to build over a render carrying attribution")
	}
	sentinel := rendercore.CrossBoundaryLinkSentinel("api", "db", "database", "host")
	marked := []byte(`{"host":"` + sentinel + `"}`)
	base := presentationOverTheRender(t, res)

	// CONTROL — an uncancellable caller reaches every path and the rule raises one
	// objection per path. Without this the assertions below would pass on a walk
	// that reached nothing.
	controlReads := 0
	controlErr := runRenderedStageRules(
		context.Background(),
		true,
		assembledContentReads{RenderedView: base, content: marked, reads: &controlReads},
		coordinates,
	)
	if controlReads != total {
		t.Fatalf("the control walk reached %d path(s) of %d; the fixture does not present what these proofs assume",
			controlReads, total)
	}
	if leaves := haltLeafMessages(controlErr); controlErr == nil || len(leaves) != total {
		t.Fatalf("the control walk produced %d diagnostic(s) over %d marked paths: %v",
			len(haltLeafMessages(controlErr)), total, controlErr)
	}

	// The withdrawal, taken on the first read.
	ctx, cancel := context.WithCancel(context.Background())
	reads := 0
	err := runRenderedStageRules(
		ctx,
		true,
		assembledContentReads{
			RenderedView: base, content: marked, reads: &reads, withdraw: cancel, triggerAt: 1,
		},
		coordinates,
	)
	cancel()

	if reads != linkPathsPerGroup {
		t.Errorf("the stopped walk reached %d path(s), want exactly one group's worth (%d) out of %d; "+
			"reaching all %d would mean the signal is read after the walk rather than inside it",
			reads, linkPathsPerGroup, total, total)
	}
	if reads >= total {
		t.Errorf("the stopped walk reached %d path(s) of %d; it did not stop partway at all", reads, total)
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("the stopped walk returned %v, want the caller's own withdrawal", err)
	}
	if errors.Is(err, ErrOrphanCrossBoundaryLinkSentinel()) {
		t.Errorf("the stopped walk returned a rule finding as well as the withdrawal: %v", err)
	}
}

// TestTheAssembledOutputEnumerationIsTheAttributionsOwn proves the axes are read
// from the attribution the assembly recorded, in the order it recorded them, and
// that a group fed by several boundaries is ONE coordinate rather than one per
// boundary.
func TestTheAssembledOutputEnumerationIsTheAttributionsOwn(t *testing.T) {
	t.Parallel()

	// One group fed by two boundaries — the collapse that happens when two
	// boundaries' paths evaluate alike under one provider — plus a second group at
	// another provider fed by one of the same boundaries.
	res := &RenderResult{Files: map[string]AssembledFile{
		"shared/a.json": {Content: []byte(`{}`)},
		"tf/main.tf":    {Content: []byte(``)},
	}}
	res.artifacts = newArtifactProjections()
	res.Contributions = []AssemblyContribution{
		{AssemblyPath: "shared", Provider: "cloudformation",
			Boundaries: []string{"prod/eu", "prod/us"}, OutputPaths: []string{"shared/a.json"}},
		{AssemblyPath: "tf", Provider: "terraform",
			Boundaries: []string{"prod/eu"}, OutputPaths: []string{"tf/main.tf"}},
	}

	coordinates := newAssemblyGroupCoordinates(res, "release.yaml")
	if coordinates == nil {
		t.Fatal("the enumeration refused to build over a render carrying attribution")
	}

	// Every assembly path once, in the order the attribution recorded them, each
	// positioned at the Release document and at no path inside it.
	var paths []string
	for _, p := range coordinates.AssemblyPaths() {
		paths = append(paths, p.Value)
		if p.SourceFile != "release.yaml" {
			t.Errorf("assembly path %q is positioned at %q, want the Release document", p.Value, p.SourceFile)
		}
	}
	if want := []string{"shared", "tf"}; !equalStrings(paths, want) {
		t.Errorf("assembly-path axis = %v, want %v", paths, want)
	}

	// The two-boundary group is ONE coordinate. That is the whole reason the axis
	// is keyed here: enumerated per boundary this group would present twice, and a
	// rule over it would report the group's output twice.
	if got := len(coordinates.AssemblyPaths()); got != len(res.Contributions) {
		t.Errorf("the axis presents %d coordinate(s) for %d group(s); a collapsed group is being counted per boundary",
			got, len(res.Contributions))
	}

	// The provider axis is the render's own reader, so the coordinate a rule runs
	// over and the fact it reads cannot be derived two ways.
	for _, path := range paths {
		if got, want := coordinates.AssemblyGroupProviders(path), res.assemblyProviders(path); !equalStrings(got, want) {
			t.Errorf("provider axis for %q = %v, want the render's own reader's %v", path, got, want)
		}
	}
	if got, want := coordinates.AssemblyGroupProviders("shared"), []string{"cloudformation"}; !equalStrings(got, want) {
		t.Errorf("provider axis for shared = %v, want %v", got, want)
	}

	// A render that recorded no attribution presents no enumeration at all, rather
	// than a value that answers emptily.
	if newAssemblyGroupCoordinates(&RenderResult{}, "release.yaml") != nil {
		t.Error("the enumeration built over a render that recorded no attribution")
	}
	if newAssemblyGroupCoordinates(nil, "release.yaml") != nil {
		t.Error("the enumeration built over no render at all")
	}
}

// TestOneCollisionIsOneFindingHoweverManyBoundariesFedTheGroup is the proof for
// the shape this rule was moved onto its group key for.
//
// Three boundaries whose path declarations evaluate alike collapse into one
// group. Two of the group's contributors meet on one output path and the third
// contributes a file of its own, so exactly one path collides and one of the three
// boundaries contributed nothing to it.
//
// Three properties are asserted and none of them is implied by the others. The
// COUNT: one colliding path produces one finding, not one per boundary that fed
// the group. The CONTRIBUTORS: that one finding names every contributor that met,
// so the repair is not a deduplication that drops one of them. And the POSITIONS:
// no finding is positioned at a boundary at all, which is what stops a reader
// being sent to a document that is not the problem — a per-boundary keying
// positions one report at each boundary of the group, and cannot tell the innocent
// one from the rest.
func TestOneCollisionIsOneFindingHoweverManyBoundariesFedTheGroup(t *testing.T) {
	t.Parallel()

	res := &RenderResult{Files: map[string]AssembledFile{
		"shared/monitor.yaml": {Content: []byte(`{}`)},
		"shared/store.yaml":   {Content: []byte(`{}`)},
	}}
	res.artifacts = newArtifactProjections()
	res.Contributions = []AssemblyContribution{{
		AssemblyPath: "shared",
		Provider:     "helm",
		Boundaries:   []string{"alpha-net", "beta-net", "gamma-net"},
		OutputPaths:  []string{"shared/monitor.yaml", "shared/store.yaml"},
		PathProducers: map[string][]string{
			// alpha and beta met here; gamma wrote the other path and met nothing.
			"shared/monitor.yaml": {"component alpha", "component beta"},
			"shared/store.yaml":   {"component gamma"},
		},
	}}

	// CONTROL — one contributor per path admits, so a halt below cannot come from
	// the fixture's shape.
	clean := *res
	clean.Contributions = []AssemblyContribution{{
		AssemblyPath: "shared", Provider: "helm",
		Boundaries:  []string{"alpha-net", "beta-net", "gamma-net"},
		OutputPaths: []string{"shared/monitor.yaml", "shared/store.yaml"},
		PathProducers: map[string][]string{
			"shared/monitor.yaml": {"component alpha"},
			"shared/store.yaml":   {"component gamma"},
		},
	}}
	if err := orphanCrossBoundaryLinkHalt(t, &clean); err != nil {
		t.Fatalf("a three-boundary group whose every path has one contributor was refused: %v", err)
	}

	err := orphanCrossBoundaryLinkHalt(t, res)
	if err == nil {
		t.Fatal("the site admitted an output path two contributors wrote")
	}
	if !errors.Is(err, ErrAssembledOutputCollision()) {
		t.Fatalf("halt condition = %v, want the assembled-output collision", err)
	}

	leaves := haltLeafMessages(err)
	if len(leaves) != 1 {
		t.Fatalf("one colliding path produced %d finding(s), want 1 — a per-boundary keying produces one per boundary that fed the group: %v",
			len(leaves), leaves)
	}
	finding := leaves[0]
	for _, want := range []string{
		`output path "shared/monitor.yaml"`,
		`written by "component alpha and component beta"`,
		`assembled within "alpha-net, beta-net and gamma-net"`,
	} {
		if !strings.Contains(finding, want) {
			t.Errorf("the finding omits %q: %s", want, finding)
		}
	}
	// The innocent boundary is not a position, and neither is any other: the
	// finding names the group's boundaries as detail and claims none of them is at
	// fault.
	if strings.Contains(finding, ".spec.boundaries.") {
		t.Errorf("the finding is positioned at a boundary; one of the group's three contributed nothing to this path: %s", finding)
	}
	// The path that did not collide is not reported.
	if strings.Contains(finding, "shared/store.yaml") {
		t.Errorf("the finding names a path with one contributor: %s", finding)
	}
	t.Logf("one colliding path across a three-boundary group -> %d finding: %s", len(leaves), finding)
}

func equalStrings(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}
