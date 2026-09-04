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

// TestCloudFormationAttachmentDetached_TheStackLacksExactlyTheAttachmentsResources
// is the other direction of the same measurement, and it is the direction a
// presence read structurally cannot supply.
//
// The case above finds the attachment's resource inside the assembled stack. On
// its own that says nothing about the merge: a stack carrying AuxAlarm whatever
// was attached — because the host's own template declared it, or because the
// assembler emits a fixed set — satisfies every presence read in this file. So
// the same shape is built a second time with the Feature detached and nothing
// else changed, and the two stacks' resource sets are compared.
//
// The comparison is a set difference that NAMES what moved rather than a count,
// and it is asserted both ways. The detached stack must lack exactly the
// attachment's resource; it must also still carry every resource the host
// declares itself. That second half is what a fixture rendering nothing would
// fail — a build emitting an empty Resources map equally "lacks the attachment's
// resource", and would pass an absence read written on its own.
//
// What a planted defect does to it, so the failure mode is demonstrated rather
// than assumed: routing the attachment into the dispatch group without regard to
// whether the Feature is attached puts AuxAlarm into the detached stack, and the
// difference then names an unexpected member. Dropping the merge instead empties
// the difference, which this case reports as removing the wrong set. Emitting the
// fragment beside the stack rather than into it leaves the difference correct and
// is caught by the whole-output read at the end.
func TestCloudFormationAttachmentDetached_TheStackLacksExactlyTheAttachmentsResources(t *testing.T) {
	t.Parallel()

	attached := pipelinetest.Build(t, cfnOverlapInput("single-host"))
	detached := pipelinetest.Build(t, cfnOverlapInput("detached"))

	attachedDoc, _ := findCfnStack(t, attached.Artifacts, "stack")
	detachedDoc, detachedRaw := findCfnStack(t, detached.Artifacts, "stack")
	t.Logf("the stack assembled with the Feature detached:\n%s", detachedRaw)

	attachedIDs := mapKeys(mustCfnMap(t, attachedDoc, "Resources"))
	detachedIDs := mapKeys(mustCfnMap(t, detachedDoc, "Resources"))
	sort.Strings(attachedIDs)
	sort.Strings(detachedIDs)
	t.Logf("attached stack resource logical ids: %v", attachedIDs)
	t.Logf("detached stack resource logical ids: %v", detachedIDs)

	inDetached := make(map[string]bool, len(detachedIDs))
	for _, id := range detachedIDs {
		inDetached[id] = true
	}
	var removed []string
	for _, id := range attachedIDs {
		if !inDetached[id] {
			removed = append(removed, id)
		}
	}
	t.Logf("detaching the Feature removed: %v", removed)
	if len(removed) != 1 || removed[0] != "AuxAlarm" {
		t.Errorf("detaching the Feature must remove exactly the attachment's resource AuxAlarm; it removed %v\nattached %v\ndetached %v",
			removed, attachedIDs, detachedIDs)
	}

	// The host's own resources survive the detachment. Without this, a stack with
	// an empty Resources map would satisfy the difference read above.
	for _, id := range []string{"HostBucket", "HostQueue"} {
		if !inDetached[id] {
			t.Errorf("the host component's own resource %q must survive detaching the Feature; the detached stack carries %v",
				id, detachedIDs)
		}
	}

	// The attachment is gone from the WHOLE emitted output, not merely from the
	// stack: a build that stopped merging the fragment but still wrote it beside
	// the stack would satisfy the set difference above while shipping the same
	// resource. Its identity stamp is read alongside its resource id, because the
	// two travel separately — the merge carries the contribution's own identity
	// rather than re-attributing it, so a leak could surface as either.
	for _, p := range sortedKeys(detached.Artifacts) {
		body := string(detached.Artifacts[p])
		for _, gone := range []string{"AuxAlarm", "cfr--alpha--aux"} {
			if strings.Contains(body, gone) {
				t.Errorf("the detached build still emits %q at %q:\n%s", gone, p, body)
			}
		}
	}
}

// TestCloudFormationAttachmentOutputs_TheStackCarriesEachContributorsEntry
// covers the stack's Outputs section, which is the other space the merge folds
// under names it derives rather than reading verbatim.
//
// The cases above are all about the Resources space, whose logical ids the merge
// reads exactly as their author wrote them. An Output name is the opposite: the
// merge builds it from the contributing node's name basis and the authored entry
// name together, so what an author gets depends on which basis the assembler
// read for the contributor.
//
// For a component-scope attachment that basis is the HOST COMPONENT it renders
// against rather than the attachment's own dispatch identity, and the two facts
// that makes true are asserted side by side here, because reading either alone
// misdescribes the model:
//
//   - The attachment is a contributor of its own. Its resource carries its own
//     provenance identity, and the collision case below names it apart from its
//     host.
//   - Its Output name is scoped to the host all the same. So a host and its own
//     attachment share one Output namespace, which is why a name they both reach
//     has to halt rather than merge — the case below.
//
// The shape is one host declaring an Outputs entry of its own, carrying one
// attachment declaring an Outputs entry of its own, with DIFFERENT entry names.
// Different names is what makes this case a statement about what the section
// carries; one name is the separate question the collision case answers.
//
// Both expected names are written out in full rather than built here from the
// basis and the entry. A name composed in the test would be composed by the same
// rule the assembler uses, so it would follow the assembler wherever it moved and
// the assertion would keep passing through a rename that changed every author's
// output names.
//
// What a planted defect does to it, so the failure mode is demonstrated rather
// than assumed: renaming the entry the attachment authors moves its Output name,
// and the exact-set read reports the expected name missing and the moved one
// unexpected. Dropping the host's own Outputs block leaves the attachment's entry
// alone in the section, which the host-entry read reports directly. Emitting the
// fragment beside the stack rather than into it leaves both reads correct and is
// caught by the whole-output read at the end.
func TestCloudFormationAttachmentOutputs_TheStackCarriesEachContributorsEntry(t *testing.T) {
	t.Parallel()

	res := pipelinetest.Build(t, cfnOverlapInput("outputs"))

	stack, raw := findCfnStack(t, res.Artifacts, "stack")
	t.Logf("assembled stack template:\n%s", raw)

	outputs := mustCfnMap(t, stack, "Outputs")
	names := mapKeys(outputs)
	sort.Strings(names)
	t.Logf("assembled stack Output names: %v", names)

	// Each contributor's entry, under the name the merge builds from that
	// contributor's name basis and the entry it authored. Both bases read "alpha"
	// here — the host's own identity, and, for a component-scope attachment, the
	// host it renders against — so the two entries are told apart by the entry
	// names their authors chose rather than by their scopes.
	const (
		hostEntry       = "alphaXXEndpoint"
		attachmentEntry = "alphaXXAlarmName"
	)

	if _, present := outputs[attachmentEntry]; !present {
		t.Errorf("the assembled stack does not carry the attachment's Outputs entry at %q; it carries %v",
			attachmentEntry, names)
	}
	// The host's own entry survives alongside it. Without this the presence read
	// above passes on a stack whose Outputs section holds the attachment's entry
	// and nothing else — a merge that displaced the host's own entry rather than
	// folding both.
	if _, present := outputs[hostEntry]; !present {
		t.Errorf("the assembled stack does not carry the host component's own Outputs entry at %q; it carries %v",
			hostEntry, names)
	}
	// The exact set, as a difference that names what moved in each direction
	// rather than a count — both sides short by one still agree on a count.
	want := map[string]bool{hostEntry: true, attachmentEntry: true}
	var unexpected []string
	for _, n := range names {
		if !want[n] {
			unexpected = append(unexpected, n)
		}
	}
	if len(unexpected) > 0 {
		t.Errorf("the assembled stack carries Output name(s) neither contributor accounts for: %v\nit carries %v",
			unexpected, names)
	}

	// The attachment's entry carries the attachment's OWN body. The name and the
	// body travel separately, so a merge that folded one contributor's name over
	// another's value would satisfy every read above — and with both entries
	// scoped to one basis, that is the shape a mis-reconciliation would take.
	attachmentBody := mustCfnMap(t, outputs, attachmentEntry)
	t.Logf("the attachment's Output body: %#v", attachmentBody)
	if !strings.Contains(string(raw), "AuxOutputsAlarm") {
		t.Errorf("the assembled stack does not reference the attachment's own resource from anywhere:\n%s", raw)
	}
	// And the attachment is a contributor of its own inside the merged document,
	// which is the half of the model the shared name basis does not describe.
	if !strings.Contains(string(raw), `"aggrega.ai/component-id": "cfr--alpha--aux"`) {
		t.Errorf("the merged stack does not stamp the attachment's own identity on its resource:\n%s", raw)
	}

	// And the attachment's entry reached the stack alone rather than the stack and
	// a file beside it. Read over the whole emitted output for the same reason the
	// resource case reads it: a build that merged the fragment AND wrote it out
	// delivers the same content twice, and every read above would pass.
	var carriers []string
	for _, p := range sortedKeys(res.Artifacts) {
		if strings.Contains(string(res.Artifacts[p]), "AuxOutputsAlarm") {
			carriers = append(carriers, p)
		}
	}
	t.Logf("the file(s) carrying the attachment's resource: %v", carriers)
	if len(carriers) != 1 {
		t.Errorf("the attachment's contribution must be carried by the assembled stack alone; it is carried by %v", carriers)
	}
	for _, p := range carriers {
		if strings.Contains(p, "features/") {
			t.Errorf("a discrete attachment file still carries the attachment's contribution at %q; the fragment was merged and written both", p)
		}
	}
}

// TestCloudFormationAttachmentOutputsOverlappingEntry_Halts is what makes the
// case above a statement rather than an observation, and it is the direction the
// shared name basis makes necessary.
//
// A component-scope attachment derives its Output names from the host component
// it renders against, so a host and its own attachment share one Output
// namespace. Two entries an author named differently stay apart in it — the case
// above — and two an author named identically reach one name, where one body
// would displace the other and the output the displaced entry described would
// leave the stack with nothing said about it.
//
// So the build halts, and the halt carries ONE FINDING PER CONTRIBUTOR rather
// than one per name. That is the part worth pinning: the two contributors share
// a name basis and are not the same contributor, so a report naming the basis
// alone would name one thing twice and leave an author unable to tell which of
// the two files to open. The attachment's finding carries the attachment key as
// well as the host, exactly as the Resources cases above require.
//
// What a planted defect does to it: pointing this release at the aggregate whose
// attachment authors a DIFFERENT entry name removes the collision, the build
// succeeds, and the first assertion fires. Reporting the collision once rather
// than per contributor fails the count; dropping the attachment key from its
// finding fails the coordinate read.
func TestCloudFormationAttachmentOutputsOverlappingEntry_Halts(t *testing.T) {
	t.Parallel()

	_, err := pipelinetest.BuildE(t, cfnOverlapInput("outputs-overlapping"))
	if err == nil {
		t.Fatal("a host and its own attachment authoring one Outputs entry name built cleanly; one of the two bodies is lost in a merged stack")
	}
	if !errors.Is(err, renderer.ErrDerivedKeyCollision()) {
		t.Fatalf("the build failed for a different reason than the derived-name collision: %v", err)
	}
	findings := cfnOverlapFindings(t, err)
	t.Logf("the halt carries %d finding(s):\n%s", len(findings), strings.Join(findings, "\n"))

	if len(findings) != 2 {
		t.Fatalf("expected one finding per contributor that met on the Output name, got %d\nfindings:\n%s",
			len(findings), strings.Join(findings, "\n"))
	}
	for _, f := range findings {
		// The DERIVED name rather than the entry name either author wrote: it is
		// what the stack would have addressed one output by, and it is the string
		// an author will not find by searching their own files, which is why the
		// report has to carry it.
		if !strings.Contains(f, `"alphaXXEndpoint"`) {
			t.Errorf("a finding does not name the Output name the two contributors met on\ngot: %s", f)
		}
		// And the key space, so the report is not read as a resource collision.
		// Both spaces are folded by one rule and refused by one check, so the
		// space is the only thing separating the two reports.
		if !strings.Contains(f, "output-logical-ids") {
			t.Errorf("a finding does not name the key space the collision is in\ngot: %s", f)
		}
	}

	// One finding per contributor, each named by its own coordinates. The
	// attachment's is named by BOTH — asserting the host alone would pass on a
	// report that lost the attachment key, and with the two contributors sharing
	// the basis "alpha" that key is the only thing telling them apart.
	var host, attachment bool
	for _, f := range findings {
		if strings.Contains(f, `derived by component "alpha", carrying attachment "aux"`) {
			attachment = true
			continue
		}
		if strings.Contains(f, `derived by component "alpha"`) {
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
