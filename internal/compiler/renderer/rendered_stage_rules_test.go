// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

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
// catalog document, and that the enumeration is built from the attribution the
// assembly recorded.

// The cancellation and counting fixture. Three boundaries, each with one provider
// writing two files, so a walk stopped after the first boundary's turn is a
// different number from a walk that ran to the end.
const linkBoundaryCount, linkPathsPerBoundary = 3, 2

func linkBoundaryIDs() []string { return []string{"prod/eu", "prod/us", "stage"} }

// assembledOutputHolding presents a completed render whose attribution names the
// given boundaries, one provider each, holding one entry per path with the same
// bytes.
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
	coordinates := newRenderedStageCoordinates(res, "release.yaml")
	if coordinates == nil {
		t.Fatal("the enumeration refused to build over a render carrying attribution")
	}
	subjects := 0
	for _, boundary := range coordinates.Boundaries() {
		for _, provider := range coordinates.Providers(boundary.Value) {
			subjects += len(coordinates.EmittedPaths(boundary.Value, provider))
		}
	}
	if subjects == 0 {
		t.Fatal("the enumeration presented no output path; a verdict from it would certify nothing")
	}
	return runRenderedStageRules(context.Background(), view, coordinates)
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
	// The rendered diagnostic, position included: the deployment document, the
	// boundary inside it, and the provider and path the detail names.
	text := err.Error()
	for _, want := range []string{"cloudformation", "prod/cloudformation-stack.json", ".spec.boundaries.prod"} {
		if !strings.Contains(text, want) {
			t.Errorf("the halt omits %q, so the offending file cannot be identified: %s", want, text)
		}
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
	if diag.Category != cerrors.TranslationError {
		t.Errorf("halt category = %q, want %q — the fault is on the translation side and no document is at fault",
			diag.Category, cerrors.TranslationError)
	}
}

// TestTheAssembledLinkPlaceholderRuleReportsEveryOffendingFile is the
// diagnostic-count proof: an input carrying more than one offending file produces
// one finding per file.
func TestTheAssembledLinkPlaceholderRuleReportsEveryOffendingFile(t *testing.T) {
	t.Parallel()

	sentinel := rendercore.CrossBoundaryLinkSentinel("api", "db", "database", "host")
	marked := []byte(`{"host":"` + sentinel + `"}`)
	// Two boundaries, two files each, three of the four carrying a placeholder.
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
	t.Logf("three offending files across two boundaries -> %d diagnostic(s)", len(leaves))
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
// The walk is outermost by boundary, then by provider, then by path, and the
// signal is read once per boundary — as the first statement of the outer loop's
// body. So a withdrawal inside the FIRST boundary's first read is seen at the
// second boundary's signal read: the first boundary's remaining paths are read,
// and no later boundary is entered at all. The reached count is therefore exactly
// one boundary's worth of paths.
//
// # Why the count and not the error
//
// A walk that ran to the end and reported the withdrawal afterwards returns the
// same error as one that stopped partway. The exact equality below is what
// separates them, and the control beside it is what stops the pair passing on a
// walk that reaches nothing.
func TestTheAssembledLinkPlaceholderWalkStopsOnTheIterationAfterTheSignalTurns(t *testing.T) {
	t.Parallel()

	total := linkBoundaryCount * linkPathsPerBoundary
	if linkBoundaryCount < 2 || linkPathsPerBoundary < 1 {
		t.Fatalf("the fixture holds %d boundary(ies) of %d path(s); one boundary's worth and the whole walk are the same number",
			linkBoundaryCount, linkPathsPerBoundary)
	}
	files := map[string][]byte{}
	for i := range linkPathsPerBoundary {
		files["f"+strconv.Itoa(i)+".json"] = []byte(`{}`)
	}
	res := assembledOutputHolding(linkBoundaryIDs(), "cloudformation", files)
	coordinates := newRenderedStageCoordinates(res, "release.yaml")
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
		assembledContentReads{
			RenderedView: base, content: marked, reads: &reads, withdraw: cancel, triggerAt: 1,
		},
		coordinates,
	)
	cancel()

	if reads != linkPathsPerBoundary {
		t.Errorf("the stopped walk reached %d path(s), want exactly one boundary's worth (%d) out of %d; "+
			"reaching all %d would mean the signal is read after the walk rather than inside it",
			reads, linkPathsPerBoundary, total, total)
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
// that the path axis is the render's own reader rather than a second walk.
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

	coordinates := newRenderedStageCoordinates(res, "release.yaml")
	if coordinates == nil {
		t.Fatal("the enumeration refused to build over a render carrying attribution")
	}

	// Every boundary once, in the order the attribution recorded them.
	var boundaries []string
	for _, b := range coordinates.Boundaries() {
		boundaries = append(boundaries, b.Value)
		if b.SourceFile != "release.yaml" {
			t.Errorf("boundary %q is positioned at %q, want the deployment document", b.Value, b.SourceFile)
		}
	}
	if want := []string{"prod/eu", "prod/us"}; !equalStrings(boundaries, want) {
		t.Errorf("boundary axis = %v, want %v", boundaries, want)
	}

	// A boundary reaching two providers yields both, in group order.
	if got, want := coordinates.Providers("prod/eu"), []string{"cloudformation", "terraform"}; !equalStrings(got, want) {
		t.Errorf("provider axis for prod/eu = %v, want %v", got, want)
	}
	if got, want := coordinates.Providers("prod/us"), []string{"cloudformation"}; !equalStrings(got, want) {
		t.Errorf("provider axis for prod/us = %v, want %v", got, want)
	}

	// The path axis is the render's own reader, so the two agree by construction
	// rather than by comparison — asserted here so a second walk introduced later
	// fails rather than diverging quietly.
	for _, boundary := range boundaries {
		for _, provider := range coordinates.Providers(boundary) {
			if got, want := coordinates.EmittedPaths(boundary, provider), res.EmittedPaths(boundary, provider); !equalStrings(got, want) {
				t.Errorf("path axis for %q/%q = %v, want the render's own reader's %v", boundary, provider, got, want)
			}
		}
	}

	// A render that recorded no attribution presents no enumeration at all, rather
	// than a value that answers emptily.
	if newRenderedStageCoordinates(&RenderResult{}, "release.yaml") != nil {
		t.Error("the enumeration built over a render that recorded no attribution")
	}
	if newRenderedStageCoordinates(nil, "release.yaml") != nil {
		t.Error("the enumeration built over no render at all")
	}
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
