// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// End-to-end coverage of what a CloudFormation dispatch group does with the
// names a component's own render and a component-carried attachment's render
// each declare.
//
// The fixture is self-contained: it carries its own Component, Recipe, sourced
// template, Feature, FeatureRecipe and provider Plugin entity, so these tests run
// wherever the module builds rather than only where the sibling catalog is
// checked out.
//
// The host Recipe declares BOTH a sourced template and an artifacts entry, so the
// host component reaches the guest twice — once with a source listing, once with
// an artifacts projection — and the attachment reaches it a third time with an
// artifacts projection of its own. The two artifacts crossings are the same
// payload shape, which is what makes a comparison between them a statement about
// the output's disposition rather than about the input kind.

package compiler_test

import (
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/pipeline/pipelinetest"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
)

const cfnOverlapCatalog = "testdata/cloudformation-attachment-name-overlap/catalog/base"

func cfnOverlapInput(variant string) pipelinetest.Input {
	return pipelinetest.Input{
		ReleasePath:  "testdata/cloudformation-attachment-name-overlap/release-" + variant + ".yaml",
		CatalogPaths: []string{cfnOverlapCatalog},
	}
}

// cfnOverlapFindings unwraps the halt into the individual findings the rule
// raised, read through the wrapped value rather than searched for as a string in
// the report's own text.
//
// A halt carrying more than one finding wraps a joined set, and each member
// carries its own detail. A halt carrying exactly one wraps the bare sentinel and
// puts that finding's detail in the categorised report's own message instead, so
// the single case is read from the report and the set case from its members. Both
// are read here rather than one, because a helper admitting only the set would
// fail a single-finding case for the shape of its wrapper rather than for its
// content — and the single-finding case is the one this file's
// contributor-own-derivation test is about.
func cfnOverlapFindings(t *testing.T, err error) []string {
	t.Helper()
	var envelope *cerrors.CompilerError
	if !errors.As(err, &envelope) {
		t.Fatalf("the halt is not a categorised diagnostic: %v", err)
	}
	if joined, isMulti := envelope.Wrap.(interface{ Unwrap() []error }); isMulti {
		members := joined.Unwrap()
		texts := make([]string, 0, len(members))
		for _, m := range members {
			texts = append(texts, m.Error())
		}
		return texts
	}
	return []string{envelope.Error()}
}

// TestCloudFormationAttachmentOverlappingName_Halts covers a component and one
// of its own attachments deriving the same resource logical id, on a provider
// that folds its dispatch group into one document.
//
// The shape is one component whose own artifact declares a resource logical id,
// carrying one attachment whose fragment declares the same id. On a stack
// provider BOTH contributions are folded into the one assembled stack, so the two
// ids genuinely meet: one of them would displace the other inside a single
// Resources map, and the author has a real remedy — rename the resource one of
// the two declares.
//
// The two contributors are named separately in the halt, one finding each, and
// the attachment's finding carries the attachment key as well as the host, so an
// author reading it knows which of the two files to edit.
func TestCloudFormationAttachmentOverlappingName_Halts(t *testing.T) {
	t.Parallel()

	_, err := pipelinetest.BuildE(t, cfnOverlapInput("overlapping"))
	if err == nil {
		t.Fatal("a component and its own attachment naming one resource in one merged stack built cleanly")
	}
	if !errors.Is(err, renderer.ErrDerivedKeyCollision()) {
		t.Fatalf("the build failed for a different reason than the derived-name collision: %v", err)
	}
	findings := cfnOverlapFindings(t, err)
	t.Logf("the halt carries %d finding(s):\n%s", len(findings), strings.Join(findings, "\n"))

	if len(findings) != 2 {
		t.Fatalf("expected one finding per contributor that met on the name, got %d\nfindings:\n%s",
			len(findings), strings.Join(findings, "\n"))
	}
	for _, f := range findings {
		if !strings.Contains(f, `"HostQueue"`) {
			t.Errorf("a finding does not name the resource id the two contributors met on\ngot: %s", f)
		}
	}
	// Both sides are named, and the attachment's is named by BOTH its coordinates
	// — asserting the host alone would pass on a report that lost the attachment
	// key, which is the coordinate telling an author which file to open.
	var host, attachment bool
	for _, f := range findings {
		if strings.Contains(f, `derived by component "store", carrying attachment "aux"`) {
			attachment = true
			continue
		}
		if strings.Contains(f, `derived by component "store"`) {
			host = true
		}
	}
	if !host {
		t.Errorf("the host component's own derivation is not named\nfindings:\n%s", strings.Join(findings, "\n"))
	}
	if !attachment {
		t.Errorf("the attachment's derivation is not named with both its coordinates\nfindings:\n%s", strings.Join(findings, "\n"))
	}
}

// TestCloudFormationAttachmentOnTwoHosts_HaltsOnTheGenuineFindingsOnly is the
// single build that carries both directions, which is what makes it sharper than
// two builds could be: one dispatch group, one key space, and three names whose
// contributors differ only in where their output landed.
//
//   - HostBucket and HostQueue are each declared by the two host components, whose
//     own output is folded into the one assembled stack. One finding per
//     contributor, so four.
//   - AuxAlarm is declared by the one attachment as rendered on each of the two
//     hosts. Each of those renders is a contributor of its own and BOTH are folded
//     into the same stack, so the two ids meet in one Resources map exactly as the
//     host components' do — two more findings, each naming its own host as well as
//     the attachment key, which is what separates them for the author.
//
// Every finding is counted by the name it is about as well as in total, because a
// count alone would pass on the wrong six.
func TestCloudFormationAttachmentOnTwoHosts_HaltsOnTheGenuineFindingsOnly(t *testing.T) {
	t.Parallel()

	_, err := pipelinetest.BuildE(t, cfnOverlapInput("two-hosts"))
	if err == nil {
		t.Fatal("two components merging into one stack and naming one resource built cleanly")
	}
	if !errors.Is(err, renderer.ErrDerivedKeyCollision()) {
		t.Fatalf("the build failed for a different reason than the derived-name collision: %v", err)
	}
	findings := cfnOverlapFindings(t, err)
	t.Logf("the halt carries %d finding(s):\n%s", len(findings), strings.Join(findings, "\n"))

	// Counted by the name each finding is about, so a finding that moved between
	// names is visible rather than absorbed into a total that still adds up.
	byName := map[string]int{}
	for _, f := range findings {
		for _, name := range []string{"HostBucket", "HostQueue", "AuxAlarm"} {
			if strings.Contains(f, `"`+name+`"`) {
				byName[name]++
			}
		}
	}
	for _, name := range []string{"HostBucket", "HostQueue"} {
		if byName[name] != 2 {
			t.Errorf("%q is derived by two components whose output merges into one stack: expected 2 findings, got %d\nfindings:\n%s",
				name, byName[name], strings.Join(findings, "\n"))
		}
	}
	if byName["AuxAlarm"] != 2 {
		t.Errorf("AuxAlarm is derived by one attachment rendered on two hosts, both folded into one stack: expected 2 findings, got %d\nfindings:\n%s",
			byName["AuxAlarm"], strings.Join(findings, "\n"))
	}
	if len(findings) != 6 {
		t.Errorf("expected exactly the six genuine findings, got %d\nfindings:\n%s",
			len(findings), strings.Join(findings, "\n"))
	}
	// The two AuxAlarm findings are told apart by their host, which is the
	// coordinate an author edits against — a report naming the attachment key
	// alone would name one thing twice.
	for _, host := range []string{"alpha", "beta"} {
		want := `derived by component "` + host + `", carrying attachment "aux"`
		found := false
		for _, f := range findings {
			if strings.Contains(f, want) {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("no finding names the attachment as rendered on host %q\nfindings:\n%s", host, strings.Join(findings, "\n"))
		}
	}
}

// TestCloudFormationAttachmentRepeatingOneName_Halts is the half of the condition
// that is INDEPENDENT of where a contributor's output landed, which is the axis it
// would be lost on.
//
// The attachment's own single fragment names one resource twice inside one
// declaration of its own. That is its own derivation failing to be injective
// rather than a meeting between two contributors, so it halts wherever its output
// landed — folded into an assembled document or written beside one — and the
// author's remedy is real either way: rename one of the two resources it declares.
func TestCloudFormationAttachmentRepeatingOneName_Halts(t *testing.T) {
	t.Parallel()

	_, err := pipelinetest.BuildE(t, cfnOverlapInput("self-repeating"))
	if err == nil {
		t.Fatal("an attachment declaring one logical id twice inside its own fragment built cleanly")
	}
	if !errors.Is(err, renderer.ErrDerivedKeyCollision()) {
		t.Fatalf("the build failed for a different reason than the derived-name collision: %v", err)
	}
	findings := cfnOverlapFindings(t, err)
	t.Logf("the halt carries %d finding(s):\n%s", len(findings), strings.Join(findings, "\n"))
	if len(findings) != 1 {
		t.Fatalf("expected one finding naming the one repeating contributor, got %d", len(findings))
	}
	// The caption is what separates this half of the condition from the other: it
	// names ONE contributor and says it derived the name more than once, rather
	// than naming two that met on it.
	if !strings.Contains(findings[0], `derived more than once by component "store", carrying attachment "aux"`) {
		t.Errorf("the finding does not read as one contributor's own derivation failing\ngot: %s", findings[0])
	}
	// And it is the ATTACHMENT that is named rather than its host, which is the
	// axis this case exists to cover: the repeat is inside one contributor's own
	// declaration, so the report has to reach the contributor rather than stop at
	// the component whose group assembled it.
	if !strings.Contains(findings[0], `"AuxAlarm"`) {
		t.Errorf("the finding does not name the repeated resource id\ngot: %s", findings[0])
	}
}

// TestCloudFormationAttachmentOnOneHost_ReadsTheEmittedOutput builds the same
// shape with the attachment on one host only, so nothing collides, and reads what
// the build actually emitted: every output path, the assembled stack's own
// Resources map, and which file carries the attachment's resource.
func TestCloudFormationAttachmentOnOneHost_ReadsTheEmittedOutput(t *testing.T) {
	t.Parallel()

	res := pipelinetest.Build(t, cfnOverlapInput("single-host"))

	paths := make([]string, 0, len(res.Artifacts))
	for p := range res.Artifacts {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	t.Logf("emitted output paths (%d):\n  %s", len(paths), strings.Join(paths, "\n  "))

	stack, raw := findCfnStack(t, res.Artifacts, "stack")
	t.Logf("assembled stack template:\n%s", raw)

	resources := mustCfnMap(t, stack, "Resources")
	stackIDs := mapKeys(resources)
	sort.Strings(stackIDs)
	t.Logf("assembled stack resource logical ids: %v", stackIDs)

	inStack := false
	for _, id := range stackIDs {
		if id == "AuxAlarm" {
			inStack = true
		}
	}
	if !inStack {
		t.Errorf("the attachment's resource AuxAlarm is NOT carried by the assembled stack; it carries %v", stackIDs)
	} else {
		t.Logf("the attachment's resource AuxAlarm is carried by the assembled stack")
	}

	// The attachment's resource is in the stack and NOWHERE ELSE. Asserted as
	// well as its presence, because a build that both merged the fragment and
	// left the discrete file beside it would satisfy the presence read alone —
	// and that build delivers the same resource twice.
	var carriers []string
	for _, p := range paths {
		if strings.Contains(string(res.Artifacts[p]), "AuxAlarm") {
			carriers = append(carriers, p)
		}
	}
	t.Logf("the file(s) carrying AuxAlarm: %v", carriers)
	for _, p := range carriers {
		t.Logf("--- %s ---\n%s", p, res.Artifacts[p])
	}
	if len(carriers) != 1 {
		t.Errorf("AuxAlarm must be carried by the assembled stack alone; it is carried by %v", carriers)
	}
	for _, p := range carriers {
		if strings.Contains(p, "features/") {
			t.Errorf("a discrete attachment file still carries AuxAlarm at %q; the fragment was merged and written both", p)
		}
	}

	// The attachment's resource keeps its own provenance identity inside the
	// merged stack — the merge carries the contribution, it does not re-attribute
	// it to the host whose group assembled it.
	if !strings.Contains(string(raw), `"aggrega.ai/component-id": "cfr--alpha--aux"`) {
		t.Errorf("the merged stack does not stamp the attachment's own identity on its resource:\n%s", raw)
	}

	// Same reading for the host component's own two resources, so the two
	// dispositions are stated side by side rather than one being inferred.
	for _, id := range []string{"HostBucket", "HostQueue"} {
		if _, present := resources[id]; !present {
			t.Errorf("the assembled stack does not carry the host component's own resource %q; it carries %v", id, stackIDs)
		}
	}

	// A second read of the stack bytes, decoded independently, so the count above
	// is not the only statement about what merged.
	var reread map[string]any
	if err := json.Unmarshal(raw, &reread); err != nil {
		t.Fatalf("the assembled stack is not valid JSON: %v", err)
	}
	t.Logf("assembled stack top-level sections: %v", mapKeys(reread))
}
