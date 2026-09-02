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

// TestCloudFormationAttachmentOverlappingName_Builds is the direction that
// changed, and the comment says why rather than leaving the inversion to be
// guessed at.
//
// The shape is one component whose own artifact declares a resource logical id,
// carrying one attachment whose fragment declares the same id. On a stack
// provider the component's own output is folded into the one assembled stack and
// the attachment's is written as discrete files under the attachment's own
// position — which the sibling case below reads directly out of the emitted bytes
// — so the two ids stand in two documents that never meet. There is no rename
// available to an author here, because nothing is displacing anything, which is
// why refusing this build was a wall rather than a guardrail.
//
// 🔴 This does NOT weaken the condition, and the two cases that prove it are in
// this file: the two-host build below still halts on its genuine findings, and
// the Kubernetes corpus, whose provider folds nothing, still halts on the same
// topology.
func TestCloudFormationAttachmentOverlappingName_Builds(t *testing.T) {
	t.Parallel()

	res := pipelinetest.Build(t, cfnOverlapInput("overlapping"))

	paths := make([]string, 0, len(res.Artifacts))
	for p := range res.Artifacts {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	t.Logf("emitted output paths (%d):\n  %s", len(paths), strings.Join(paths, "\n  "))

	// The build having produced output is the half a clean exit does not state:
	// the two contributors both rendered, and their two HostQueue ids stand in two
	// separate documents.
	stack, _ := findCfnStack(t, res.Artifacts, "stack")
	resources := mustCfnMap(t, stack, "Resources")
	if _, present := resources["HostQueue"]; !present {
		t.Errorf("the assembled stack does not carry the host component's own HostQueue; it carries %v", mapKeys(resources))
	}
	var fragmentCarriers []string
	for _, p := range paths {
		if strings.Contains(p, "features/") && strings.Contains(string(res.Artifacts[p]), "HostQueue") {
			fragmentCarriers = append(fragmentCarriers, p)
		}
	}
	if len(fragmentCarriers) == 0 {
		t.Errorf("no discrete attachment file carries HostQueue; the two ids are not in two documents after all: %v", paths)
	}
	t.Logf("the attachment's own file(s) carrying HostQueue: %v", fragmentCarriers)
}

// TestCloudFormationAttachmentOnTwoHosts_HaltsOnTheGenuineFindingsOnly is the
// single build that carries both directions, which is what makes it sharper than
// two builds could be: one dispatch group, one key space, and three names whose
// contributors differ only in where their output landed.
//
//   - HostBucket and HostQueue are each declared by the two host components, whose
//     own output really is folded into the one assembled stack. They must still
//     halt, one finding per contributor, so four findings.
//   - AuxAlarm is declared by the one attachment as rendered on each of the two
//     hosts. Each of those renders is written under its own host's position, so the
//     two ids stand in two file sets that never meet and must raise nothing.
//
// Both halves are asserted — presence of the four and absence of the two — because
// a count alone would pass on the wrong four.
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
	if byName["AuxAlarm"] != 0 {
		t.Errorf("AuxAlarm is derived by one attachment rendered on two hosts, each written under its own position: expected 0 findings, got %d\nfindings:\n%s",
			byName["AuxAlarm"], strings.Join(findings, "\n"))
	}
	if len(findings) != 4 {
		t.Errorf("expected exactly the four genuine findings, got %d\nfindings:\n%s",
			len(findings), strings.Join(findings, "\n"))
	}
}

// TestCloudFormationAttachmentRepeatingOneName_Halts is the half of the condition
// the narrowing does not touch, driven end to end on a contributor whose output is
// placed standalone — which is the axis it would be lost on.
//
// The attachment's own single fragment names one resource twice inside one
// document of its own. That is its own derivation failing to be injective rather
// than a meeting between two contributors, so it halts wherever its output landed,
// and the author's remedy is real: rename one of the two resources it declares.
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
	// And it is the attachment, whose output the render placed standalone, that is
	// named — which is the axis this case exists to cover.
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
	if inStack {
		t.Errorf("the attachment's resource AuxAlarm IS carried by the assembled stack; the two names would genuinely meet in one merged document")
	} else {
		t.Logf("the attachment's resource AuxAlarm is absent from the assembled stack")
	}

	// Where the attachment's resource actually landed, read from the emitted bytes
	// rather than from any log line.
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
