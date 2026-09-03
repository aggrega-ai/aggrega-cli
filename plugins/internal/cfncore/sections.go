// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package cfncore

import (
	"fmt"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/rendercore"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderctx"
)

// cloudformationTargetName is the render target's user-facing name, as it appears
// in the diagnostics this core raises about authored input the target cannot
// honor. It is the capability an author binds a Recipe to, so a diagnostic naming
// it names something the author wrote.
const cloudformationTargetName = "cloudformation"

// The top-level sections of a CloudFormation template. Each is the key an author
// writes, so anything naming one names something the author can search their own
// files for.
const (
	sectionAWSTemplateFormatVersion = "AWSTemplateFormatVersion"
	sectionDescription              = "Description"
	sectionMetadata                 = "Metadata"
	sectionParameters               = "Parameters"
	sectionRules                    = "Rules"
	sectionMappings                 = "Mappings"
	sectionConditions               = "Conditions"
	sectionTransform                = "Transform"
	sectionResources                = "Resources"
	sectionOutputs                  = "Outputs"
)

// topLevelSections enumerates every top-level section a CloudFormation template
// admits, in the order the template language documents them.
//
// It exists so the assembled stack's treatment of the sections is stated as one
// exhaustive accounting rather than discovered section by section: every entry
// here belongs to exactly one of the four dispositions below, and a section that
// belonged to none would be dropped in silence — which is the failure the
// accounting removes. The four groups are held to this list by a test, so adding a
// section to the list without deciding what becomes of it fails rather than
// passing quietly.
//
// What the list cannot check is itself: the template language is not ours, so a
// section the language gains and nobody adds here is invisible to the test. That
// is why the list is written out in the language's own order — it is meant to be
// read against the language's own documentation, which is the only thing that can
// confirm it is complete.
var topLevelSections = []string{
	sectionAWSTemplateFormatVersion,
	sectionDescription,
	sectionMetadata,
	sectionParameters,
	sectionRules,
	sectionMappings,
	sectionConditions,
	sectionTransform,
	sectionResources,
	sectionOutputs,
}

// stampedSections are the sections the assembler writes itself rather than
// reading from any contributor. The template format version is the only one: the
// template language admits a single value for it, so a contributor's own
// declaration carries nothing the stamp does not already say, and dropping it
// loses nothing an author could act on.
var stampedSections = []string{sectionAWSTemplateFormatVersion}

// mergedSections are the sections the merge folds under names it derives or scopes
// itself: the resources, under the logical ids their Recipes authored and this
// core reads verbatim, and the outputs, under names built from the contributing
// node and the entry together ([OutputLogicalID]).
var mergedSections = []string{sectionResources, sectionOutputs}

// carriedSections are the sections the assembled stack carries from its
// contributors under exactly the names those contributors authored. Each is a map
// keyed by names its author chose, which is what makes folding them a merge rather
// than a choice: two contributors declaring different names both land, and two
// declaring one name are reconciled by the same rule the resource and output
// spaces already use ([assembledSpace.insert]).
//
// Carrying a name verbatim is also what makes its alphabet this core's business:
// nothing between the author and the emitted bytes would repair a name the target
// cannot hold. Which of these sections are held to the logical-id alphabet, which
// are not, and why, is [alphabetCheckedSections] and [alphabetUncheckedSections] —
// a partition of this list rather than a condition written at the read.
//
// Each is carried on its own terms rather than as a set, because each is authored
// independently of the others: a rule asserts over a parameter of the same
// document, a condition may reference no parameter at all — a pseudo-parameter or
// another condition needs none — and a mapping may not reference a parameter,
// since the template language forbids it. So there is no group here that has to
// travel together, only five sections that each have to travel.
//
// 🔴 Carrying them REDUCES unresolved references and cannot introduce one, and the
// intuition runs the other way. Dropping a section while keeping the resources
// that reference it is what leaves an assembled stack naming something it does not
// declare; carrying the section is what supplies the declaration. So nothing here
// checks whether a reference resolves: a template whose references were already
// unresolved is not made worse by the carry, and refusing one would refuse exactly
// the input this carrying repairs.
//
// The names in these spaces are deliberately NOT declared to the host as derived
// keys. The host compares declared names for equality and halts on any repeat,
// reading nothing of the bodies — so declaring them would refuse two contributors
// that declare one parameter identically, which is a merge that displaces nothing.
// Reconciling them needs both bodies in hand, and the merge is the only place that
// holds them.
var carriedSections = []string{
	sectionConditions,
	sectionMappings,
	sectionMetadata,
	sectionParameters,
	sectionRules,
}

// alphabetCheckedSections are the carried sections whose authored names this core
// holds to the alphabet a CloudFormation logical id admits, refusing a name that
// carries anything else ([carriedSectionNameRefusal]).
//
// A name in one of these sections is a logical id: the template language admits
// letters and digits in it and requires it unique within the template, which is
// the same rule the resource logical ids follow. So a name outside that alphabet
// reaches a template the target will not accept, exactly as an authored resource
// logical id does — and these sections are carried verbatim, so nothing between
// the author and the emitted bytes would have repaired it.
//
// 🔴 Only the section's OWN keys are held to it, and never the keys nested inside
// an entry's body. A mapping's second-level keys are map keys rather than logical
// ids — a region name such as us-east-1 is ordinary and legitimate there — and a
// parameter's or a condition's body is keyed by attribute and intrinsic names the
// author does not choose. Descending would refuse legal input, which is the same
// failure excluding a section outright avoids.
//
// Together with [alphabetUncheckedSections] this partitions [carriedSections], and
// a test holds the partition to that list from both sides: a section added to the
// carried set without a name disposition fails rather than landing unchecked in
// silence, and a section named here that no longer halts fails too.
var alphabetCheckedSections = []string{
	sectionConditions,
	sectionMappings,
	sectionParameters,
}

// alphabetUncheckedSections are the carried sections whose authored names this
// core reads without asking anything about their characters. Each is here for its
// own reason, and neither reason carries to the other.
//
// Metadata is excluded because the template language does not name a logical id
// in it at all: the section stores additional information as objects of
// user-defined key-value pairs, so the names in it are free-form and a
// letters-and-digits check would refuse input the target accepts. That is the
// exclusion this list exists for.
//
// Rules is here because the alphabet its names lie in is NOT established — not by
// the template language's logical-id rule, which does not reach it, and not by
// anything in this core. Leaving it unchecked is the honest answer to an
// unmeasured alphabet: a check over one nobody established is how a refusal comes
// to reject legal input. It is deliberately not folded into the reason above
// either, since the metadata exclusion rests on a documented property of that one
// section and reading it across would be inventing a second exclusion out of an
// argument about the first.
var alphabetUncheckedSections = []string{
	sectionMetadata,
	sectionRules,
}

// namedSections are the sections a merge cannot fold, so the assembled stack
// carries neither and the contributor that authored one is told out of band
// instead.
//
// A description is one string for the whole template. Two contributors give two
// answers, neither of which is the stack's, so adopting one would publish one
// component's description as the description of all of them — a claim no
// contributor is in a position to make. A transform reinterprets the WHOLE
// template, so adopting one contributor's would change how every other
// contributor's content is read, which is a larger claim still. Neither has a
// defensible merge, and picking one silently is worse than carrying neither.
//
// Both are reported through the shared out-of-band class for authored input a
// render target cannot honor, which is warn-severity and never touches the
// emitted bytes: the assembled stack is byte-identical whether or not anything is
// listening. Warn rather than halt because a template carrying a description is
// legitimate input — it renders correctly, minus the description — and refusing it
// would reject a deployment that works.
var namedSections = []string{sectionDescription, sectionTransform}

// SectionNotices reports one warning per top-level section a node's fragments
// authored that the assembled stack cannot carry — the sections whose content is
// one statement about the whole template rather than a map of authored names
// ([namedSections]). An empty result means the node authored none of them.
//
// One warning per section per node, however many of the node's fragments authored
// it: the fact being reported is that this node's content did not reach the stack,
// which is one fact per section whether it was written once or twice. Sections are
// reported in a fixed order and fragments are read in the order supplied, so the
// result is a function of the node's fragments alone.
//
// It is raised at render rather than at assembly, where the drop actually happens,
// because the author edits one node's Recipe and the assembly speaks for a whole
// dispatch group. For a provider whose boundary assembly is a merge the two answers
// agree: the per-node fragment is never an output document of its own, so a section
// the merge will not carry has already reached nothing by the time the node's
// render is complete.
func SectionNotices(fragments [][]byte) ([]renderctx.Diagnostic, error) {
	var diags []renderctx.Diagnostic
	reported := make(map[string]bool, len(namedSections))
	for _, raw := range fragments {
		frag, err := decodeToValue(raw)
		if err != nil {
			return nil, err
		}
		if frag == nil {
			continue
		}
		m, ok := frag.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("cloudformation-provider: fragment is not a mapping")
		}
		for _, section := range namedSections {
			if _, present := m[section]; !present || reported[section] {
				continue
			}
			reported[section] = true
			diags = append(diags, rendercore.UnsupportedFieldDiagnostic(cloudformationTargetName, section))
		}
	}
	return diags, nil
}

// SectionNoticesSourced reports the same warnings for a sourced source's packed
// files, reading the same files [DeclaredNamesSourced] reads and skipping the same
// non-template content [NormalizeSourced] vendors verbatim — a directory source's
// README or parameters file declares no template section. A single-file source's
// lone template is read whole.
func SectionNoticesSourced(files []SourcedFile, rootIsDir bool) ([]renderctx.Diagnostic, error) {
	contents := make([][]byte, 0, len(files))
	for _, f := range files {
		if rootIsDir && !isTemplateFile(f.Path) {
			continue
		}
		contents = append(contents, f.Content)
	}
	diags, err := SectionNotices(contents)
	if err != nil {
		return nil, fmt.Errorf("cloudformation-provider: read template sections from sourced template: %w", err)
	}
	return diags, nil
}
