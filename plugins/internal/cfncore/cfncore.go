// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Package cfncore is the filesystem-free CloudFormation domain core the
// cloudformation-provider Wasm guest runs: it normalizes CloudFormation
// fragments and sourced CloudFormation templates to pure-JSON long-form
// intrinsics, structurally validates a sourced template, and merges a dispatch
// group's per-node fragments into a single CloudFormation stack template. It
// holds all the CloudFormation-specific intelligence — short-form intrinsic
// expansion, per-fragment resource merging, output-name scoping, the
// provides-to-Outputs projection, and sourced-template parse validation — so
// the Wasm host that orchestrates the guest stays blind to CloudFormation
// semantics.
//
// A logical id is unique within one template, and several nodes assemble into
// one stack, so every name a merged template addresses something by has to be
// distinct across the nodes that contributed it. Four mechanisms carry that, and
// each closes a case the others structurally cannot see.
//
// Output names are scoped by the contributing node, so a node's outputs cannot
// displace another node's ([OutputLogicalID]). Building such a name encodes what
// the alphabet refuses rather than dropping it, so the derivation is injective on
// its own: two entries reach one name only where the node and the entry both
// match. Each name is nevertheless built through a ledger that records the
// authored entry behind it and refuses a name two different authored entries
// reached ([outputNameLedger]). Nothing can reach that refusal while the
// derivation holds, which is why it is kept — it is the one layer still holding
// the authored entries, so it is the only place a derivation that stopped
// telling two of them apart could be reported rather than silently losing one.
//
// A logical id also has an alphabet — letters and digits, and nothing else — and
// the two properties are independent: a set of ids can be perfectly distinct and
// still name characters a template cannot hold. That alphabet is a character
// class rather than this target's invention, so the class lives outside this
// core, in [alphanumeric]; what this core states, and what is genuinely its own,
// is that a CloudFormation logical id admits exactly that class. Output names are
// legal by construction, because building one writes everything outside the
// alphabet as an escape spelled in characters inside it. Every name this core
// reads verbatim is not, so each is refused
// where it carries such a character rather than emitted into a template the
// target will not accept: an authored resource logical id
// ([resourceLogicalIDRefusal]), and an authored name in a carried section that
// holds a logical id ([carriedSectionNameRefusal], over
// [alphabetCheckedSections]). Both are refused and never repaired: a resource
// whose logical id changes is replaced rather than updated, so a silent rewrite
// would change the identity of a resource that may already be running.
//
// Resource names come from the Recipe that authored the fragment, which this core
// reads and never rewrites; [DeclaredNames] collects them across everything the
// node contributes, and the guest hands them to the host as the names it derived,
// which is what turns a duplicate across contributors into a halt naming both
// instead of a resource quietly displaced during the merge. And the merge refuses
// a name the assembled stack already carries with different content, which is
// what a declaration cannot do: one contributor claiming a name twice across its
// own fragments is recorded in that node's declaration as a repeated name, and
// refusing it is the merge's to do, since there is no second contributor to name.
//
// Underneath all of them, converting a parsed fragment to a value tree refuses a
// mapping key written twice ([ErrDuplicateMappingKey]). No name is built there
// and no contributor is compared: it is the case where a fragment already carries
// two bodies for one key it wrote itself, which every mechanism above would
// otherwise be handed as a single entry.
//
// Every top-level section a template admits is accounted for, and the accounting
// is written out as one list ([topLevelSections]) so a section cannot be left
// undecided. The format version is stamped; the resources and outputs are folded
// under names the merge derives or reads verbatim; five name-keyed sections are
// carried under exactly the names their authors wrote ([carriedSections]); and the
// two whose content is one statement about the whole template are carried by
// neither contributor and reported out of band instead ([namedSections],
// [SectionNotices]).
//
// The core carries no build constraint so it compiles for both the wasip1 guest
// and the host test binary: the guest's exports call into it, and its behavior
// is exercised directly by host tests without a Wasm runtime. Output is
// deterministic: every emitted object serializes with sorted keys, and every
// walk over a fragment's keys takes them in sorted order, so identical input
// yields byte-identical output across runs.
package cfncore

import (
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"sort"
	"strconv"
	"strings"

	abi "github.com/aggrega-ai/aggrega-abi-go/pkg/abi"
	"github.com/aggrega-ai/aggrega-cli/internal/alphanumeric"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/rendercore"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderctx"
	"github.com/aggrega-ai/aggrega-cli/pkg/anchorcycle"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/namesegment"
	"github.com/awslabs/goformation/v7/intrinsics"
	"go.yaml.in/yaml/v3"
)

// ErrOutputNameCollision is the loud failure raised when two different entries
// an author wrote arrive at one stack Output name. An Output name admits only
// letters and digits, and it is built by writing what that alphabet refuses as
// an escape spelled inside it rather than by dropping it, so two entries an
// author wrote apart stay apart and nothing an author can write reaches this.
// It is retained because a derivation that stopped telling two entries apart
// would otherwise emit one Output where two were written, silently; the build
// halts instead, and the halt names every entry behind the name.
func ErrOutputNameCollision() error { return errOutputNameCollision }

var errOutputNameCollision = cerrors.Register("cloudformation-provider-cfncore/output-name-collision", cerrors.Meta{
	ShortCode: "E0233",
	Message:   "cloudformation-provider: two entries an author wrote reach one stack Output name",
})

// ErrDuplicateMappingKey is the loud failure raised when a CloudFormation
// fragment writes one mapping key twice. A mapping holds one value per key, so
// converting such a mapping keeps whichever body came last and discards the
// other — and where the mapping is a Resources or Outputs block, the discarded
// body is a resource or an output that was written and would not be there. The
// build halts naming the key and both places it was written.
func ErrDuplicateMappingKey() error { return errDuplicateMappingKey }

var errDuplicateMappingKey = cerrors.Register("cloudformation-provider-cfncore/duplicate-mapping-key", cerrors.Meta{
	ShortCode: "E0230",
	Message:   "cloudformation-provider: one mapping key is written more than once",
})

// StackTemplateName is the file name the assembled CloudFormation stack template
// lands at within a dispatch group's output. A dispatch group folds its per-node
// fragments into this one template, so the name is fixed per group rather than
// derived from any node.
const StackTemplateName = "cloudformation-stack.json"

// templateFormatVersion is the CloudFormation template format version stamped on
// every assembled stack template.
const templateFormatVersion = "2010-09-09"

// NodeFragment is one node's contribution to a dispatch group's assembled stack:
// the node id, the identity the node's own names are derived from, the serialized
// bytes of each per-node fragment the render pass produced for it, and the
// deployment wave its boundary declared.
//
// NameBasis scopes the output names the fragment's provides and Outputs entries
// land under, so two nodes contributing the same entry name address separate
// outputs. It is empty for a node whose id is already that identity, which is the
// ordinary case; a contributor that is not one of the graph's nodes reaches the
// assembly under a dispatch identity no target's alphabet accepts, and carries the
// identity it derives names from here instead. The same value reached that
// contributor's render, so the names it declared while rendering and the names this
// assembly addresses its content by come from one identity — read through
// [ContributorNameIdentity], which is the one place the fallback is spelled.
//
// SyncWave travels per node rather than once per assembly because a dispatch
// group can merge contributors from several boundaries, each declaring its own
// wave — which is precisely the case where one assembled stack holds more than
// one. A node whose boundary declares no wave carries 0, the field's documented
// default, so a missing wave and a declared zero are the same state.
type NodeFragment struct {
	NodeID    string
	NameBasis string
	Files     [][]byte
	SyncWave  int
}

// ContributorNameIdentity is the identity one contributor's stack Output names are
// built from: its carried name basis, falling back to its node id.
//
// It exists so the render-time declaration and the assemble-time mint ask one
// question of one implementation. Those two derivations produce the name an author
// is told their content carries and the name the stack actually addresses it by; a
// contributor for whom they read different identities has both answers
// self-consistent and neither one true, which no assertion on either side alone can
// see.
func ContributorNameIdentity(n NodeFragment) string {
	return rendercore.ContributorNameBasis(n.NodeID, n.NameBasis)
}

// Key spaces the guest declares its derived names into. Each is a name this core
// chose for one kind of name a CloudFormation template addresses something by;
// the host compares names for equality within one space and reads no meaning
// into either the space or the names, so these strings only ever have to be
// distinct from each other and stable across runs.
const (
	// KeySpaceResourceLogicalIDs holds the logical ids of the resources the
	// assembled stack carries.
	KeySpaceResourceLogicalIDs = "resource-logical-ids"
	// KeySpaceOutputLogicalIDs holds the logical ids of the outputs the
	// assembled stack carries.
	KeySpaceOutputLogicalIDs = "output-logical-ids"
)

// NormalizeFragment parses one CloudFormation fragment — a YAML or JSON object
// carrying a Resources map and optionally provides/Outputs material — and
// re-emits it as pure-JSON long-form intrinsics. YAML short-form intrinsic tags
// (!Ref, !GetAtt, !Sub, and the rest) are expanded to their long-form object
// spellings ({"Ref": ...}, {"Fn::GetAtt": [...]}); a fragment already in JSON
// long form passes through unchanged in meaning. The output serializes with
// sorted object keys so it is byte-identical across runs of identical input.
func NormalizeFragment(content []byte) ([]byte, error) {
	v, err := decodeToValue(content)
	if err != nil {
		return nil, err
	}
	if v == nil {
		return nil, fmt.Errorf("cloudformation-provider: fragment is empty")
	}
	return json.Marshal(v)
}

// SourcedFile is one packed file of a sourced CloudFormation source: its
// root-relative path and raw bytes. It mirrors the shape the pack-source host
// function returns so the guest hands its packed projection straight to the core.
type SourcedFile struct {
	Path    string
	Content []byte
}

// NormalizeSourcedTemplate normalizes one sourced CloudFormation template to
// pure-JSON long-form intrinsics. The template's YAML short-form intrinsic tags
// (!Ref, !GetAtt, !Sub, and the rest) expand to their long-form object spellings
// ({"Ref": ...}, {"Fn::GetAtt": ...}) and the document re-emits as JSON with sorted
// keys, so identical input yields byte-identical output across runs. No intrinsic is
// evaluated: a Ref or GetAtt survives as its long-form pointer object rather than
// being resolved to a value, so the template's deterministic references carry
// through untouched and nothing depends on runtime state. A template that does not
// parse is a loud error naming the parse failure.
func NormalizeSourcedTemplate(content []byte) ([]byte, error) {
	out, err := normalizeTemplateBytes(content)
	if err != nil {
		return nil, fmt.Errorf("cloudformation-provider: parse sourced template: %w", err)
	}
	return out, nil
}

// NormalizeSourced normalizes a sourced CloudFormation source's packed files to
// pure-JSON long-form intrinsics, preserving the packed order and each file's path.
// A single-file source (rootIsDir false) is a lone CloudFormation template whose one
// file normalizes whole. A directory source normalizes every CloudFormation template
// file — identified by extension — and vendors any other file (a README, a
// parameters file, an unrelated asset) verbatim, so the source's non-template content
// survives untouched. A template file that does not parse halts loudly, naming the
// file.
func NormalizeSourced(files []SourcedFile, rootIsDir bool) ([]SourcedFile, error) {
	out := make([]SourcedFile, 0, len(files))
	for _, f := range files {
		if rootIsDir && !isTemplateFile(f.Path) {
			out = append(out, f)
			continue
		}
		normalized, err := normalizeTemplateBytes(f.Content)
		if err != nil {
			return nil, fmt.Errorf("cloudformation-provider: normalize sourced template %q: %w", f.Path, err)
		}
		out = append(out, SourcedFile{Path: f.Path, Content: normalized})
	}
	return out, nil
}

// ValidateSourced structurally parses a sourced CloudFormation source's packed files
// and returns one diagnostic per parse finding, in the packed order. A single-file
// source's lone template is parsed whole; a directory source parses every
// CloudFormation template file (by extension) and leaves any other file untouched. It
// interprets nothing beyond the template's syntactic shape — it does not resolve
// intrinsics, evaluate conditions, contact AWS, or run any CloudFormation toolchain;
// it confirms the template parses. A template that fails to parse yields an
// halt-severity diagnostic naming the file, which halts the build at the host's
// input gate. An empty result means every template parsed cleanly.
// templateParseCode is the machine-matchable code every diagnostic reporting a
// CloudFormation template that failed to parse carries.
const templateParseCode = "cloudformation-template-parse"

func ValidateSourced(files []SourcedFile, rootIsDir bool) []renderctx.Diagnostic {
	var diags []renderctx.Diagnostic
	for _, f := range files {
		if rootIsDir && !isTemplateFile(f.Path) {
			continue
		}
		if _, err := normalizeTemplateBytes(f.Content); err != nil {
			diags = append(diags, renderctx.Diagnostic{
				Severity: abi.SeverityHalt,
				Code:     templateParseCode,
				Message:  fmt.Sprintf("sourced CloudFormation template %q failed to parse: %v", f.Path, err),
				Location: &renderctx.DiagnosticLocation{Kind: "cloudformation", URI: f.Path},
			})
		}
	}
	return diags
}

// normalizeTemplateBytes converts one CloudFormation template's YAML or JSON bytes to
// pure-JSON long-form intrinsics without evaluating any intrinsic: short-form tags
// expand to their long-form object spellings and the document re-emits as sorted-key
// JSON with a trailing newline. Ref, GetAtt, and every other intrinsic pass through as
// their long-form pointer objects, never resolved, so the conversion is
// ahead-of-time-pure and deterministic across runs.
func normalizeTemplateBytes(content []byte) ([]byte, error) {
	out, err := intrinsics.ProcessYAML(content, &intrinsics.ProcessorOptions{NoProcess: true})
	if err != nil {
		return nil, err
	}
	return append(out, '\n'), nil
}

// isTemplateFile reports whether a packed file's path names a CloudFormation template
// by its extension. A directory source normalizes only these files and vendors any
// other verbatim.
func isTemplateFile(path string) bool {
	switch {
	case strings.HasSuffix(path, ".yaml"),
		strings.HasSuffix(path, ".yml"),
		strings.HasSuffix(path, ".json"),
		strings.HasSuffix(path, ".template"):
		return true
	default:
		return false
	}
}

// AssembleStack merges every node's fragments in dispatch-group order into one
// CloudFormation stack template and returns its pure-JSON long-form bytes. Each
// fragment's Resources fold into one Resources map under the logical ids the
// fragment authored, and an id carrying a character a logical id does not admit
// halts the assembly rather than reaching the emitted template
// ([resourceLogicalIDRefusal]). Each fragment's provides entries become stack Outputs (a
// provides value that is an intrinsic referencing a resource in the assembled
// stack rides through as its long-form intrinsic — the intra-stack
// cross-reference), and any explicit Outputs block merges in as well; both are
// named for the contributing node and the entry together ([OutputLogicalID]), so
// two nodes exporting the same entry name reach separate outputs. The assembled
// template is validated with the standard-library JSON decoder: it must be
// well-formed JSON carrying a non-empty Resources object, or the assembly halts
// loudly. Cross-stack wiring is not performed here; a provides value pointing
// outside the assembled stack is piped through as whatever intrinsic the fragment
// authored.
//
// Each fragment's Parameters, Mappings, Conditions, Metadata and Rules are carried
// into the assembled template under exactly the names the fragment authored, folded
// across contributors by the same rule the resource and output spaces use — see
// [carriedSections]. A section no contributor declared adds no key, so a dispatch
// group declaring none of them assembles the bytes it assembled before any of them
// were carried. A fragment's Description and Transform are carried by neither
// contributor and reported out of band at render instead ([namedSections]), and the
// format version is stamped rather than read from any contributor.
//
// Where the contributing nodes carry more than one deployment wave, the merge also
// records that ordering as DependsOn between the assembled stack's resources — see
// [applyWaveOrdering]. A group whose nodes all carry one wave records nothing, so a
// dispatch group where no boundary declared a wave assembles byte-identical bytes.
//
// Two nodes authoring one resource logical id never reach this merge: the guest
// declares each node's names ([DeclaredNames]) and the host halts on a duplicate
// before any assembly runs. What still reaches it is one contributor authoring a
// name twice across its own fragments, which the merge refuses itself — see
// [assembledSpace.insert].
//
// Every fragment is read before anything is inserted, and that ordering is the
// point rather than a convenience. The stack is the scope its Output names have
// to be distinct in, so whether the derivation was injective is not answerable
// until every fragment has been read ([outputNameLedger]); and the answer has to
// come before the insert, because the insert holds only the built name and could
// therefore refuse a collision without being able to say which two authored
// entries produced it.
func AssembleStack(nodes []NodeFragment) ([]byte, error) {
	// One claim run per fragment, in the order the fragments were supplied, so
	// the insert below walks them in exactly the order a single pass would have.
	type fragmentClaimSet struct {
		nodeID string
		wave   int
		claim  fragmentClaim
	}
	var mint outputNameLedger
	sets := make([]fragmentClaimSet, 0, len(nodes))

	for _, node := range nodes {
		// The identity this contributor's names are built from, which is the identity
		// its render declared them from. Read once per contributor rather than at each
		// use, so the names built here and the identity a refusal reports cannot come
		// from two readings.
		identity := ContributorNameIdentity(node)
		for _, raw := range node.Files {
			frag, err := decodeToValue(raw)
			if err != nil {
				return nil, err
			}
			m, ok := frag.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("cloudformation-provider: fragment from node %q is not a mapping", identity)
			}
			claim, err := fragmentEntries(identity, m, &mint)
			if err != nil {
				return nil, err
			}
			sets = append(sets, fragmentClaimSet{nodeID: identity, wave: node.SyncWave, claim: claim})
		}
	}

	if err := mint.err(); err != nil {
		return nil, err
	}

	resources := newAssembledSpace(KeySpaceResourceLogicalIDs)
	outputs := newAssembledSpace(KeySpaceOutputLogicalIDs)
	// One space per carried section, built up front so the insert loop below reads
	// them by section name without deciding whether a section has been seen yet.
	// The space is named for the section as the author wrote it, because that is
	// the scope those names have to be distinct in and the word the author can
	// search their own files for.
	carried := make(map[string]*assembledSpace, len(carriedSections))
	for _, section := range carriedSections {
		carried[section] = newAssembledSpace(section)
	}
	// waveByResource records the wave of the node that contributed each resource,
	// so the ordering pass reads a resource's wave without re-walking the input. A
	// node contributes every resource its fragments authored, so all of them share
	// its wave.
	waveByResource := map[string]int{}
	for _, set := range sets {
		for _, e := range set.claim.resources {
			if err := resources.insert(set.nodeID, e); err != nil {
				return nil, err
			}
			waveByResource[e.key] = set.wave
		}
		for _, e := range set.claim.outputs {
			if err := outputs.insert(set.nodeID, e); err != nil {
				return nil, err
			}
		}
		for _, section := range carriedSections {
			for _, e := range set.claim.sections[section] {
				if err := carried[section].insert(set.nodeID, e); err != nil {
					return nil, err
				}
			}
		}
	}

	if len(resources.entries) == 0 {
		return nil, fmt.Errorf("cloudformation-provider: assembled stack carries no Resources")
	}

	applyWaveOrdering(resources.entries, waveByResource)

	stack := map[string]any{
		sectionAWSTemplateFormatVersion: templateFormatVersion,
		sectionResources:                resources.entries,
	}
	// A section no contributor declared adds no key, so a dispatch group whose
	// contributors declare none of them assembles the same bytes it assembled
	// before any of them were carried.
	for _, section := range carriedSections {
		if len(carried[section].entries) > 0 {
			stack[section] = carried[section].entries
		}
	}
	if len(outputs.entries) > 0 {
		stack[sectionOutputs] = outputs.entries
	}

	out, err := json.MarshalIndent(stack, "", "  ")
	if err != nil {
		return nil, err
	}
	if err := validateStack(out); err != nil {
		return nil, err
	}
	out = append(out, '\n')
	return out, nil
}

// stackEntry is one name the assembled stack addresses a fragment's content by,
// paired with the content that lands under it.
type stackEntry struct {
	key   string
	value any
}

// fragmentClaim is what one fragment claims of the assembled stack, per key
// space: its resources, its outputs, and the entries of each section carried
// under the names the fragment authored.
//
// The three travel together because they come from one walk of one fragment.
// Sections holds one entry run per carried section the fragment declared, keyed by
// the section name as authored; a section the fragment did not declare is absent
// rather than empty, so a reader distinguishes "declared nothing" from "declared
// an empty block" without a second field.
type fragmentClaim struct {
	resources []stackEntry
	outputs   []stackEntry
	sections  map[string][]stackEntry
}

// fragmentEntries reads one decoded fragment and returns what the assembled
// stack carries for it, per key space: its resources under the logical ids the
// fragment authored, which this core reads and never rewrites — and refuses
// where one of them lies outside the alphabet a logical id admits
// ([resourceLogicalIDRefusal]), since reading it verbatim is what would carry it
// into a template the target will not accept; its outputs — an
// explicit Outputs block's entries, then the entries its provides keys project to
// — each named for the contributing node and the entry together
// ([OutputLogicalID]); and the entries of each carried section
// ([carriedSections]) under exactly the names the fragment authored — refused,
// for a section that carries a logical id, on the same alphabet and by the same
// reasoning ([carriedSectionNameRefusal], over [alphabetCheckedSections]). A
// provides value that is an intrinsic referencing a resource in the assembled
// stack rides through as its long-form intrinsic. A fragment carrying none of
// these sections claims nothing, which is a valid answer rather than an error.
//
// This is the single derivation of which names a fragment claims. The merge
// inserts these entries and [DeclaredNames] gathers them across a node's
// fragments and reports their keys, so the set a node declares to the host is by
// construction the set the merge writes. It is also why both alphabet refusals
// sit here rather than at the write into the assembled map: every path that reads
// a fragment's sections arrives through this one derivation, so a refusal reaches
// the declaration a node hands the host as well as the merge, and it reaches both
// at the earliest point either holds the contributor, the section and the
// authored name together. Two walks kept in step by eye could
// drift instead, and the host would then be checking one derivation for
// duplicates while the merge wrote another — a divergence nothing would fail on,
// because no comparison between the two exists to fail.
//
// The carried sections are part of the same derivation and are deliberately not
// part of the declaration: the declaration is a name-only check the host performs
// for spaces whose names must be globally distinct, and these are spaces where two
// contributors declaring one name identically is a merge rather than a collision.
// [carriedSections] states why that check cannot be the host's. Their entries are
// dropped from what the node declares and NOT from what it reads, so the alphabet
// refusal over them still reaches every path: it raises from inside this
// derivation, before any claim is returned for a caller to keep or discard.
//
// A fragment reaches one key space through more than one section, so it can name
// one key twice: an explicit Outputs block and the projected provides entries
// both land in the outputs space. Those repeats are folded here, on the terms the
// merge writes them ([collapseEntries]), which is what keeps the set exact rather
// than merely overlapping. A carried section is folded the same way, so a section
// restating one name with one body claims it once.
//
// Each section's keys are walked in sorted order, the explicit Outputs entries
// precede the projected provides entries, and the carried sections are walked in
// their declared order, so the returned claim is a function of the fragment and
// the node id alone.
//
// Every output name is built through the caller's ledger rather than by calling
// [OutputLogicalID] directly, so the authored entry behind each name is recorded
// as the name is built. The caller owns the ledger because the caller owns the
// scope the names have to be distinct in, and it asks the ledger for its verdict
// once that scope is complete — a fragment cannot answer for a scope wider than
// itself. Resource ids are not built from anything: they are the fragment's own
// keys, read verbatim, so two distinct authored ids stay distinct and there is
// nothing for a ledger to record. The carried sections' names are read verbatim
// for the same reason, so they need no ledger either.
func fragmentEntries(nodeID string, frag map[string]any, mint *outputNameLedger) (fragmentClaim, error) {
	var claim fragmentClaim
	if raw, present := frag[sectionResources]; present {
		rmap, ok := raw.(map[string]any)
		if !ok {
			return fragmentClaim{}, fmt.Errorf("cloudformation-provider: %s in node %q is not a mapping", sectionResources, nodeID)
		}
		ids := sortedKeys(rmap)
		if err := resourceLogicalIDRefusal(nodeID, ids); err != nil {
			return fragmentClaim{}, err
		}
		for _, id := range ids {
			claim.resources = append(claim.resources, stackEntry{key: id, value: rmap[id]})
		}
	}
	if raw, present := frag[sectionOutputs]; present {
		omap, ok := raw.(map[string]any)
		if !ok {
			return fragmentClaim{}, fmt.Errorf("cloudformation-provider: %s in node %q is not a mapping", sectionOutputs, nodeID)
		}
		for _, k := range sortedKeys(omap) {
			name := mint.mint(authoredOutputEntry{component: nodeID, section: sectionExplicitOutputs, entry: k})
			claim.outputs = append(claim.outputs, stackEntry{key: name, value: omap[k]})
		}
	}
	if raw, present := frag[sectionProvides]; present {
		pmap, ok := raw.(map[string]any)
		if !ok {
			return fragmentClaim{}, fmt.Errorf("cloudformation-provider: %s in node %q is not a mapping", sectionProvides, nodeID)
		}
		for _, k := range sortedKeys(pmap) {
			name := mint.mint(authoredOutputEntry{component: nodeID, section: sectionProvides, entry: k})
			claim.outputs = append(claim.outputs, stackEntry{key: name, value: map[string]any{"Value": pmap[k]}})
		}
	}
	for _, section := range carriedSections {
		raw, present := frag[section]
		if !present {
			continue
		}
		smap, ok := raw.(map[string]any)
		if !ok {
			return fragmentClaim{}, fmt.Errorf("cloudformation-provider: %s in node %q is not a mapping", section, nodeID)
		}
		names := sortedKeys(smap)
		// A section carrying a logical id under a name its author wrote is held to
		// the alphabet a logical id admits, and one carrying free-form or
		// unestablished names is not — [alphabetCheckedSections] states which is
		// which and why. Asked from the declared list rather than from a condition
		// written here, so the sections that are checked stay one statement.
		if slices.Contains(alphabetCheckedSections, section) {
			if err := carriedSectionNameRefusal(nodeID, section, names); err != nil {
				return fragmentClaim{}, err
			}
		}
		entries := make([]stackEntry, 0, len(smap))
		for _, k := range names {
			entries = append(entries, stackEntry{key: k, value: smap[k]})
		}
		if claim.sections == nil {
			claim.sections = make(map[string][]stackEntry, len(carriedSections))
		}
		claim.sections[section] = collapseEntries(entries)
	}
	claim.resources = collapseEntries(claim.resources)
	claim.outputs = collapseEntries(claim.outputs)
	return claim, nil
}

// collapseEntries folds a name stated more than once with the same body into one
// entry, keeping each surviving entry where it first appeared, and keeps a repeat
// whose body differs from one already recorded under that name.
//
// The merge holds one value per name, so a name restated with the same body
// reaches the assembled stack once. Folding it here is what makes the names a
// contributor claims exactly the names the stack carries for it, rather than
// merely a superset: a reader of the names alone counts what it is given, and a
// repeat that the stack does not carry reads as a collision that never happened.
//
// A repeat whose bodies differ is kept, and that is the whole reason the fold is
// scoped by body rather than by name. The merge refuses such a contributor
// ([assembledSpace.insert]) because one of the two bodies would be lost; the repeated name
// is what makes that loss visible to anyone holding the names without the bodies.
//
// Applied at both the scopes a name can repeat at — within one fragment, where
// two sections reach one key space, and across the fragments of one node, which
// is the scope a declaration speaks for — and to every key space rather than to
// the one that can repeat today, so the guarantee rests on the fold rather than
// on how many sections or fragments happen to feed a space.
func collapseEntries(in []stackEntry) []stackEntry {
	if len(in) < 2 {
		return in
	}
	out := make([]stackEntry, 0, len(in))
	kept := make(map[string][]any, len(in))
next:
	for _, e := range in {
		for _, body := range kept[e.key] {
			if sameBody(body, e.value) {
				continue next
			}
		}
		kept[e.key] = append(kept[e.key], e.value)
		out = append(out, e)
	}
	return out
}

// sameBody reports whether two bodies landing under one name are the same thing,
// so restating that name displaces nothing.
//
// One predicate answers that question for both the fold above and the merge's
// refusal, so what a fragment declares and what the assembled stack carries
// cannot come to disagree about which repeats are lossy. Two copies would agree
// today and drift the moment either side's notion of identical content moved.
func sameBody(a, b any) bool {
	return reflect.DeepEqual(a, b)
}

// assembledSpace is one key space of the assembled stack under construction: the
// entry landing under each name, and the node that contributed it.
//
// The contributor is recorded alongside the entry so a refusal names both sides of
// a collision. Holding only the entries would leave a refusal able to name the
// contributor that arrived and not the one already there, which is half of what
// the author has to look at to decide which of the two to rename.
//
// The space's own name is what a diagnostic calls the scope: for a space whose
// names this core derives, the guest's name for that space; for a section carried
// under the names an author wrote, the section itself, which is the word that
// author can search their own files for.
type assembledSpace struct {
	name    string
	entries map[string]any
	byNode  map[string]string
}

// newAssembledSpace returns an empty key space under the given name.
func newAssembledSpace(name string) *assembledSpace {
	return &assembledSpace{name: name, entries: map[string]any{}, byNode: map[string]string{}}
}

// insert lands one entry in the space, refusing a name the space already carries
// with different content.
//
// A name in one of these spaces addresses exactly one thing — a logical id names
// one resource, an output name one output, a parameter name one parameter — so the
// space holds one value per name. Writing a second, different value under a name
// already taken is not a merge: it displaces what was there, and the displaced
// resource leaves the stack with nothing said about it, so a deployment that
// declared it gets a template that does not carry it. Refusing is what turns that
// into something the author can see and fix.
//
// Restating a name with content identical to what the space already carries
// displaces nothing, so it proceeds. Both bodies are decoded and in hand here,
// which is what lets the refusal be scoped to the case that actually loses
// something rather than to every repeat of a name.
//
// Collecting many distinct names from many fragments is the ordinary case and is
// untouched: a fragment's every name lands, and several fragments per node is
// the design — a dispatch group folds all of them into one template.
//
// This is the only check that reads the BODIES, and that is what it catches that
// nothing above it can. Every other check for this class compares names: the
// output-name ledger asks whether two different authored entries reached one
// name, and the declaration the guest hands the host carries names alone. So a
// name arriving twice over two different bodies is a loss only this sees at the
// point the loss would happen — the write into the assembled map. It also holds
// without any of them: the declaration is an optional part of what a provider
// reports, so a caller assembling a boundary without having read one gets this
// refusal and nothing else.
//
// For the resource and output spaces the host's own duplicate check over the names
// a guest declares reaches a cross-node collision first, so this is their backstop.
// For a carried section there is no such check and there cannot be one — a name
// declared identically by two contributors is a merge, which a name-only comparison
// cannot tell from a collision — so this is the whole of the refusal for those
// spaces.
func (s *assembledSpace) insert(nodeID string, e stackEntry) error {
	if existing, taken := s.entries[e.key]; taken && !sameBody(existing, e.value) {
		return fmt.Errorf("cloudformation-provider: in key space %q node %q contributes the name %q, which node %q already contributed with different content; change what one of them is named so each name addresses one thing", s.name, nodeID, e.key, s.byNode[e.key])
	}
	s.entries[e.key] = e.value
	s.byNode[e.key] = nodeID
	return nil
}

// validateStack confirms the assembled bytes are well-formed JSON carrying a
// non-empty Resources object, using only the standard-library JSON decoder. A
// malformed document, a missing Resources key, or an empty Resources object is a
// loud failure.
func validateStack(b []byte) error {
	var top map[string]json.RawMessage
	if err := json.Unmarshal(b, &top); err != nil {
		return fmt.Errorf("cloudformation-provider: assembled stack is not valid JSON: %w", err)
	}
	res, ok := top["Resources"]
	if !ok {
		return fmt.Errorf("cloudformation-provider: assembled stack has no Resources")
	}
	var rm map[string]json.RawMessage
	if err := json.Unmarshal(res, &rm); err != nil {
		return fmt.Errorf("cloudformation-provider: assembled stack Resources is not a JSON object: %w", err)
	}
	if len(rm) == 0 {
		return fmt.Errorf("cloudformation-provider: assembled stack Resources is empty")
	}
	return nil
}

// decodeToValue parses YAML or JSON content into a plain Go value tree, expanding
// CloudFormation short-form intrinsic tags to their long-form object spellings on
// the way. Maps decode to map[string]any, sequences to []any, and scalars to
// their native Go types, so a later json.Marshal emits sorted-key deterministic
// JSON. Empty content decodes to a nil value.
func decodeToValue(content []byte) (any, error) {
	var root yaml.Node
	if err := yaml.Unmarshal(content, &root); err != nil {
		return nil, fmt.Errorf("cloudformation-provider: parse fragment: %w", err)
	}
	// Parsing does not expand aliases, so a fragment whose anchor is aliased
	// inside its own content parses cleanly here and only fails when nodeToValue
	// follows the alias links. The refusal therefore sits between the parse and
	// the walk. Depth alone is never refused: a deep fragment converts normally.
	if anchor, found := anchorcycle.Find(&root); found {
		return nil, fmt.Errorf("cloudformation-provider: parse fragment: %s", anchorcycle.Explain(anchor))
	}
	if root.Kind == 0 || len(root.Content) == 0 {
		return nil, nil
	}
	return nodeToValue(root.Content[0])
}

// nodeToValue converts one YAML node to a plain Go value, resolving aliases and
// expanding a CloudFormation short-form intrinsic tag into its long-form object
// spelling.
//
// It follows alias links without tracking where it has been, so it terminates
// only on a tree whose anchors are free of recursion. decodeToValue establishes
// that with anchorcycle.Find before calling; the check is not repeated here,
// because a guarantee that has one home cannot drift out of agreement with
// itself.
func nodeToValue(n *yaml.Node) (any, error) {
	if n.Kind == yaml.AliasNode {
		if n.Alias == nil {
			return nil, fmt.Errorf("cloudformation-provider: unresolved YAML alias")
		}
		return nodeToValue(n.Alias)
	}
	if isCustomTag(n.Tag) {
		return intrinsicFromNode(n)
	}
	switch n.Kind {
	case yaml.DocumentNode:
		if len(n.Content) == 0 {
			return nil, nil
		}
		return nodeToValue(n.Content[0])
	case yaml.MappingNode:
		return mappingToValue(n)
	case yaml.SequenceNode:
		return sequenceToValue(n)
	case yaml.ScalarNode:
		return scalarToValue(n), nil
	default:
		return nil, fmt.Errorf("cloudformation-provider: unsupported YAML node kind %d", n.Kind)
	}
}

// mappingToValue converts a YAML mapping node to a map[string]any, recursing into
// each value. Long-form intrinsic objects (a mapping whose sole key is "Ref" or
// "Fn::*") are ordinary mappings and pass through unchanged.
//
// A key written twice in one mapping is refused ([ErrDuplicateMappingKey]). This
// is the point at which such a repeat becomes a loss and the only point at which
// it is still cheap to describe: the parsed node tree carries a mapping's content
// as a flat run of key/value pairs, so both bodies are present and intact right
// here, while the map being built holds one value per key and the second write
// would displace the first. Where the mapping is a Resources or Outputs block
// that displacement is a resource or an output that was written and would be
// absent from the stack, with nothing said about it — so the refusal is what
// keeps a written-down resource from disappearing. Both lines are reported
// because a key written twice is usually two edits that were meant to be
// different keys, and the author has to find both to tell which.
func mappingToValue(n *yaml.Node) (map[string]any, error) {
	out := make(map[string]any, len(n.Content)/2)
	// The line each key was first written at, so a repeat can name where the two
	// sit. Read only by key, never walked, so it introduces no order.
	firstLine := make(map[string]int, len(n.Content)/2)
	for i := 0; i+1 < len(n.Content); i += 2 {
		keyNode := n.Content[i]
		key := keyNode.Value
		if earlier, taken := firstLine[key]; taken {
			return nil, fmt.Errorf("%w: the key %q is written at line %d and again at line %d; a mapping holds one value per key, so one of the two bodies would be dropped — remove one, or rename one so each key is written once", ErrDuplicateMappingKey(), key, earlier, keyNode.Line)
		}
		firstLine[key] = keyNode.Line
		v, err := nodeToValue(n.Content[i+1])
		if err != nil {
			return nil, err
		}
		out[key] = v
	}
	return out, nil
}

// sequenceToValue converts a YAML sequence node to a []any, recursing into each
// item.
func sequenceToValue(n *yaml.Node) ([]any, error) {
	out := make([]any, 0, len(n.Content))
	for _, item := range n.Content {
		v, err := nodeToValue(item)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, nil
}

// scalarToValue decodes a scalar YAML node to its native Go type per its resolved
// tag — null, bool, integer, or float — falling back to the raw string for any
// tag whose value does not parse as its type and for plain strings.
func scalarToValue(n *yaml.Node) any {
	switch n.Tag {
	case "!!null":
		return nil
	case "!!bool":
		if b, err := strconv.ParseBool(n.Value); err == nil {
			return b
		}
	case "!!int":
		if i, err := strconv.ParseInt(n.Value, 10, 64); err == nil {
			return i
		}
	case "!!float":
		if f, err := strconv.ParseFloat(n.Value, 64); err == nil {
			return f
		}
	}
	return n.Value
}

// intrinsicFromNode expands a CloudFormation short-form intrinsic node into its
// long-form object spelling. !Ref and !Condition keep their bare names; !GetAtt
// splits a dotted scalar into its [logicalId, attribute] pair (or passes a
// sequence form through); every other short-form tag !Xxx becomes {"Fn::Xxx":
// <value>}. The tagged node's value is read as its natural kind, ignoring the
// custom tag.
func intrinsicFromNode(n *yaml.Node) (any, error) {
	name := strings.TrimPrefix(n.Tag, "!")
	inner, err := innerValue(n)
	if err != nil {
		return nil, err
	}
	switch name {
	case "Ref":
		return map[string]any{"Ref": inner}, nil
	case "Condition":
		return map[string]any{"Condition": inner}, nil
	case "GetAtt":
		return map[string]any{"Fn::GetAtt": getAttValue(inner)}, nil
	default:
		return map[string]any{"Fn::" + name: inner}, nil
	}
}

// innerValue reads a custom-tagged node's value as its natural kind: a
// custom-tagged scalar yields its raw string (the intrinsic argument), while a
// sequence or mapping recurses through the normal conversion so nested intrinsics
// expand too.
func innerValue(n *yaml.Node) (any, error) {
	switch n.Kind {
	case yaml.ScalarNode:
		return n.Value, nil
	case yaml.SequenceNode:
		return sequenceToValue(n)
	case yaml.MappingNode:
		return mappingToValue(n)
	default:
		return nil, fmt.Errorf("cloudformation-provider: unsupported intrinsic node kind %d", n.Kind)
	}
}

// getAttValue shapes the argument of a !GetAtt intrinsic into the long-form
// [logicalId, attribute] pair. A string is split on its first dot; a value with
// no dot yields a single-element slice; an already-sequence argument passes
// through unchanged.
func getAttValue(inner any) any {
	switch v := inner.(type) {
	case string:
		if idx := strings.Index(v, "."); idx >= 0 {
			return []any{v[:idx], v[idx+1:]}
		}
		return []any{v}
	default:
		return inner
	}
}

// isCustomTag reports whether a YAML tag is a CloudFormation short-form intrinsic
// tag — a single-bang tag such as "!GetAtt" — as opposed to a resolved standard
// tag ("!!str", "!!map") or an untagged node.
func isCustomTag(tag string) bool {
	return tag != "" && strings.HasPrefix(tag, "!") && !strings.HasPrefix(tag, "!!")
}

// outputLogicalIDOpener is the character the Output-name encoding opens every
// escape with, and it is the one letter or digit that encoding does not carry
// through as authored.
//
// A target that refuses punctuation outright leaves a character sitting outside
// its alphabet to open an escape with. This one does not: a logical id admits
// letters and digits and nothing else, so the opener has to be spared from
// inside the alphabet, and whichever character is spared stops being carried as
// authored. Any letter or digit would be sound, so the choice is only about
// which names stay legible — an uppercase Q is the rarest character in an
// authored component or entry name, and sparing it leaves the lowercase q of
// queue and sqs carried as written.
const outputLogicalIDOpener = 'Q'

// outputLogicalIDEncoding is the segment encoding a stack Output's logical id is
// built with. Its admitted alphabet is the logical-id alphabet less the one
// character reserved to open an escape.
//
// The alphabet is read from [alphanumeric.IsRune] rather than restated here, so
// the characters this encoding carries through and the characters each refusal
// of a verbatim-read name admits cannot come to disagree about what a logical id
// accepts.
var outputLogicalIDEncoding = namesegment.NewEncoding(outputLogicalIDOpener, isOutputLogicalIDRune)

// isOutputLogicalIDRune reports whether a character is carried into a stack
// Output's logical id as itself. Every other character is escaped, including the
// one the alphabet admits and this encoding reserves.
func isOutputLogicalIDRune(r rune) bool {
	return r != outputLogicalIDOpener && alphanumeric.IsRune(r)
}

// OutputLogicalID names the stack Output one node's entry lands at: the node's
// identity and the entry name, each encoded into the logical-id alphabet and
// joined by a sequence no encoded segment can contain — node payments-db
// exporting host reaches paymentsQzhdbQQhost.
//
// The derivation is injective: two calls produce one name only where the node
// and the entry both match. A logical id admits only letters and digits, so a
// character an author wrote outside that alphabet is written as an escape rather
// than dropped, which keeps two names differing only in punctuation apart; a
// letter is carried in the case it was authored in, which keeps two names
// differing only in case apart; and the join is a sequence an encoded segment
// can never carry, so the split between the node and the entry is recoverable
// and one pair of inputs cannot reach the name a differently-split pair reaches.
//
// The cost is legibility, and it is real: a name an author would recognise at a
// glance becomes one they have to read. It is the price of a name that
// identifies exactly one thing, and the alternative is a name that identifies
// neither of the two entries that reached it.
//
// Scoping every output by its node, rather than only where two nodes happen to
// collide, is what makes an output's meaning independent of who else assembles
// into the stack: one node's connection identity cannot arrive under a name
// another node also exports, and a name does not change when a second node is
// added to the boundary.
//
// This is the guest's own naming of a node, and it is deliberately not the name
// any Recipe chose for its resources: a Recipe authors whatever logical ids it
// likes and this core reads them verbatim, so nothing here may depend on what a
// Recipe wrote. The cross-stack publish and resolve reach an output through this
// same function, which is what lets them address a producer's output from the
// node and entry name alone.
func OutputLogicalID(nodeID, entry string) string {
	return outputLogicalIDEncoding.Name(nodeID, entry)
}

// Fragment sections an author reaches the stack's Output names through. Each is
// the key the author writes, so a diagnostic naming one names something the
// author can search their own files for.
const (
	sectionExplicitOutputs = "Outputs"
	sectionProvides        = "provides"
)

// authoredOutputEntry is one entry an author wrote that a stack Output name is
// built from: the component whose fragment carried it, the fragment section it
// was written in, and the entry name exactly as authored.
//
// The authored strings are what it carries, not the name they produce. Building
// the name loses them — that is what makes the derivation lossy in the first
// place — so anything that has to describe a collision has to be holding these.
type authoredOutputEntry struct {
	component string
	section   string
	entry     string
}

// outputNameLedger builds stack Output names and records which authored entries
// each one came from, over one scope that shares a set of names.
//
// # It is retained as a backstop, and cannot fire while the derivation holds
//
// An Output name admits only letters and digits, and the transformation into
// that poorer alphabet escapes what the alphabet refuses rather than dropping it
// ([OutputLogicalID]), so it is injective: two entries reach one name only where
// the component and the entry both match, and two such entries are one thing the
// author named rather than a collision. No input can therefore reach this
// ledger's refusal.
//
// That is a reason to keep it rather than to remove it as dead. A derivation is
// injective because of how it is written, and nothing about a later edit
// announces that it stopped being — so the guard that cannot fire under correct
// behaviour is the one that reports the day the behaviour changes. Removing it
// would leave that regression silent: two entries would reach one name, the
// second would displace the first, and the assembled stack would carry one
// Output where the author wrote two.
//
// It sits here rather than downstream because of what each layer holds. Building
// one name is a function of one entry and cannot see a second. Downstream, the
// built name is all there is: the authored entry is gone, so a refusal there
// could name the collision but not the two things the author would have to edit.
// Recording the authored entry as the name is built is what keeps both in hand,
// and it is why this is the only layer that could report such a regression
// usefully.
//
// The scope is the caller's to choose, because the scope a name has to be unique
// in is a property of what assembles together rather than of the derivation: one
// component's fragments share names with each other, and every component
// assembling into one stack shares them with the rest.
type outputNameLedger struct {
	sources map[string][]authoredOutputEntry
}

// mint builds the stack Output name for one authored entry and records the entry
// behind it.
//
// One authored entry reaching here more than once records one source, not two.
// That is deliberate and it is the line between the two properties this file
// checks: an entry restated — the same entry name written in a fragment's
// Outputs block and in its provides block, or restated across two of a
// component's fragments — is one thing the author named, and whether the
// restatements agree about its body is the merge's question ([assembledSpace.insert]).
// Whether two *different* things the author named collapsed into one name is this
// ledger's, and only a repeat of the derived name from a different authored entry
// answers yes to it.
func (l *outputNameLedger) mint(e authoredOutputEntry) string {
	name := OutputLogicalID(e.component, e.entry)
	if l.sources == nil {
		l.sources = make(map[string][]authoredOutputEntry)
	}
	for _, recorded := range l.sources[name] {
		if recorded.component == e.component && recorded.entry == e.entry {
			return name
		}
	}
	l.sources[name] = append(l.sources[name], e)
	return name
}

// err reports every stack Output name that more than one authored entry reached,
// and nothing when the names the scope built are distinct.
//
// Every collision is reported rather than the first, so one build tells the
// author the whole picture instead of one collision per attempt.
//
// The message is sorted twice over — collisions by the name they landed on, and
// each collision's entries by component, section, and entry — so it is a total
// order over the authored strings alone. The map underneath is walked, and a Go
// map's iteration order is randomised per run, so without the sorts identical
// input would halt with the clauses in a different order on different runs. That
// would be a diagnostic that is not a function of its input, which the same
// reasoning that keeps emitted output byte-identical rules out.
func (l *outputNameLedger) err() error {
	collided := make([]string, 0, len(l.sources))
	for name, sources := range l.sources {
		if len(sources) > 1 {
			collided = append(collided, name)
		}
	}
	if len(collided) == 0 {
		return nil
	}
	sort.Strings(collided)

	clauses := make([]string, 0, len(collided))
	for _, name := range collided {
		sources := make([]authoredOutputEntry, len(l.sources[name]))
		copy(sources, l.sources[name])
		sort.Slice(sources, func(i, j int) bool {
			if sources[i].component != sources[j].component {
				return sources[i].component < sources[j].component
			}
			if sources[i].section != sources[j].section {
				return sources[i].section < sources[j].section
			}
			return sources[i].entry < sources[j].entry
		})
		phrases := make([]string, 0, len(sources))
		for _, s := range sources {
			phrases = append(phrases, fmt.Sprintf("the %s entry %q of component %q", s.section, s.entry, s.component))
		}
		clauses = append(clauses, fmt.Sprintf("%s reach the Output name %q", joinPhrases(phrases), name))
	}
	return fmt.Errorf("%w: %s; an Output name is built by encoding the component and the entry into the letters and digits a logical id admits, rather than by dropping what it does not, so two entries an author wrote apart stay apart — renaming either end clears the build", ErrOutputNameCollision(), strings.Join(clauses, "; "))
}

// joinPhrases joins already-ordered phrases into one English list.
//
// It is local rather than shared because the equivalent helper beside the
// compiler's own collision report is unexported there and this core compiles for
// the WebAssembly guest as well as the host, so there is no package either side
// could reach the other through. The two are read together only by eye, which is
// tolerable for a list separator and would not be for anything carrying meaning.
func joinPhrases(phrases []string) string {
	switch len(phrases) {
	case 0:
		return ""
	case 1:
		return phrases[0]
	default:
		return strings.Join(phrases[:len(phrases)-1], ", ") + " and " + phrases[len(phrases)-1]
	}
}

// resourceLogicalIDRefusal reports why a CloudFormation template cannot address
// one contributor's resources by the logical ids it authored, and reports nil
// where every one of them lies inside the alphabet a logical id admits
// ([alphanumeric.IsRune]).
//
// A logical id is what the emitted template addresses a resource by, and this
// core reads the ids a Recipe authored verbatim — so an id carrying a character
// the alphabet does not admit reaches the emitted template unchanged, and the
// template it lands in is one the target will not accept. Refusing it here is
// what turns that into something the author can act on at build time instead of
// at deploy time, by someone who did not author the name.
//
// It is a refusal and never a repair, and that is the load-bearing half. A
// resource whose logical id changes is REPLACED rather than updated, so quietly
// rewriting an id would change the identity of a resource that may already be
// running — a cost the author cannot see coming and cannot undo. A build that
// stops is the smaller failure.
//
// Every id the contributor authored is reported together rather than the first,
// so one build tells the author the whole picture instead of one name per
// attempt. The ids are read in the order supplied and each id's characters in
// the order they first appear, so the message is a function of its input alone.
// The scope is what one contributor authored in one fragment, which is what the
// caller holds; a contributor whose second fragment carries another such id is
// answered for it once the first is fixed.
func resourceLogicalIDRefusal(nodeID string, ids []string) error {
	clauses := alphabetClauses("resource", ids)
	if len(clauses) == 0 {
		return nil
	}
	return fmt.Errorf("cloudformation-provider: component %q names a resource the assembled stack cannot address: %s; a resource logical id admits only letters and digits, and this provider emits the id a Recipe authored rather than rewriting it — so name the resource in letters and digits alone, either by having the Recipe author a logical id of its own rather than passing a name straight through, or by naming whatever it does pass through in letters and digits alone",
		nodeID, strings.Join(clauses, "; "))
}

// carriedSectionNameRefusal reports why a CloudFormation template cannot address
// one contributor's entries in a carried section by the names it authored, and
// reports nil where every one of them lies inside the alphabet a logical id
// admits ([alphanumeric.IsRune]).
//
// It is the same rule the resource logical ids follow, on the sections that carry
// a logical id under a name their author wrote ([alphabetCheckedSections]) — so
// the alphabet, the character enumeration and the clause list are the resource
// refusal's, read from one place rather than restated. What differs is the
// remedy, and it differs because it has to: the resource remedy offers a Recipe
// the option of authoring a logical id instead of passing a name through, which
// is a statement about resource logical ids and would be wrong about a parameter.
// A carried name's remedy is to name the entry differently, because the name IS
// what the author wrote there.
//
// The section is named in the message, and that is the word the author acts on:
// these names are carried under exactly the section their author wrote them in,
// so the section is what they can search their own files for. [assembledSpace]
// names it for the same reason.
//
// The scope is the names one contributor authored in one section of one fragment,
// which is what the caller holds. A contributor whose second fragment, or whose
// second checked section, carries another such name is answered for it once the
// first is fixed.
func carriedSectionNameRefusal(nodeID, section string, names []string) error {
	clauses := alphabetClauses("entry", names)
	if len(clauses) == 0 {
		return nil
	}
	return fmt.Errorf("cloudformation-provider: component %q names a %s entry the assembled stack cannot address: %s; a name in the %s section admits only letters and digits, and this provider carries the section under exactly the names its author wrote — so name the entry in letters and digits alone",
		nodeID, section, strings.Join(clauses, "; "), section)
}

// alphabetClauses reports one clause per name a CloudFormation template cannot
// address, and nothing for a name that lies inside the alphabet a logical id
// admits ([alphanumeric.IsRune]). An empty result means every name is addressable.
//
// subject is the noun a clause calls the thing a name addresses — a resource, an
// entry of a carried section — so one derivation serves every space whose names
// are read verbatim while each still reads as what it is. It is the only thing
// that varies: the empty-name case, the distinct-characters case and their order
// are one rule, and a second copy of them would agree today and drift the moment
// either refusal's notion of what to report moved, with nothing failing while
// they still agreed.
//
// Every offending name is reported rather than the first, so one build tells the
// author the whole picture instead of one name per attempt. The names are read in
// the order supplied and each name's characters in the order they first appear,
// so the result is a function of its input alone.
func alphabetClauses(subject string, names []string) []string {
	clauses := make([]string, 0, len(names))
	for _, name := range names {
		if name == "" {
			clauses = append(clauses, fmt.Sprintf("one %s is named by the empty string, which addresses nothing", subject))
			continue
		}
		outside := charactersOutsideLogicalIDAlphabet(name)
		if len(outside) == 0 {
			continue
		}
		quoted := make([]string, 0, len(outside))
		for _, r := range outside {
			quoted = append(quoted, strconv.Quote(string(r)))
		}
		clauses = append(clauses, fmt.Sprintf("%q carries %s", name, joinPhrases(quoted)))
	}
	return clauses
}

// charactersOutsideLogicalIDAlphabet returns the distinct characters of a name
// that a CloudFormation logical id does not admit, in the order they first
// appear. An empty result means the whole name lies inside the alphabet.
//
// Distinct rather than one entry per occurrence, because what an author acts on
// is which characters to stop writing rather than how many times each was
// written; and in first-appearance order rather than sorted, so the answer is a
// function of the name as authored and needs no comparison over runes.
func charactersOutsideLogicalIDAlphabet(name string) []rune {
	var outside []rune
	seen := make(map[rune]bool)
	for _, r := range name {
		if alphanumeric.IsRune(r) || seen[r] {
			continue
		}
		seen[r] = true
		outside = append(outside, r)
	}
	return outside
}

// nodeDeclaration gathers what one node's fragments claim, across every fragment
// the node contributes, and projects it to the names the node declares.
//
// The scope is the node because that is the scope the declaration is read at: a
// node hands over one set of names per key space, however many fragments it
// contributed. Gathering entries and folding once at the end is what makes the
// declared set the set the node's assembled content carries; gathering names
// instead, and joining them per fragment, cannot — a name two fragments restate
// with one body reaches the assembled stack once and would be declared twice.
//
// This is the one place the node-scope fold happens, and both the fragment path
// and the sourced path reach it, so the two cannot come to disagree about which
// repeats a node's declaration keeps.
//
// It carries the node's own output-name ledger, so a node whose entries reach one
// Output name is refused while its fragments are being read — before the stack is
// assembled, and before any other node's content is involved. The node is a
// narrower scope than the stack, so this catches strictly less than the assembly
// does; what it buys is the earlier halt, and it is the only place the sourced
// path is reached at all.
type nodeDeclaration struct {
	resources []stackEntry
	outputs   []stackEntry
	mint      outputNameLedger
}

// addFragment reads one already-normalized fragment and gathers what the
// assembled stack will carry for it. A fragment carrying none of the sections a
// stack is addressed through adds nothing, which is a valid answer rather than an
// error: a fragment contributing only vendored content has no name to declare.
//
// Each fragment's outputs are ordered by name as they are gathered, and fragments
// are gathered in the order the caller supplied, so what the node declares is a
// function of its fragments and their order alone.
//
// It reads the same claim the merge inserts and keeps the two key spaces the guest
// declares to the host, dropping the carried sections' entries on the floor. That
// is deliberate rather than an omission: the host's check over a declaration
// compares names and halts on a repeat, which is the wrong verdict for a space
// where two contributors declaring one name identically is a merge — see
// [carriedSections].
func (d *nodeDeclaration) addFragment(nodeID string, fragment []byte) error {
	v, err := decodeToValue(fragment)
	if err != nil {
		return err
	}
	if v == nil {
		return nil
	}
	m, ok := v.(map[string]any)
	if !ok {
		return fmt.Errorf("cloudformation-provider: fragment from node %q is not a mapping", nodeID)
	}
	claim, err := fragmentEntries(nodeID, m, &d.mint)
	if err != nil {
		return err
	}
	outputs := claim.outputs
	sort.SliceStable(outputs, func(i, j int) bool { return outputs[i].key < outputs[j].key })
	d.resources = append(d.resources, claim.resources...)
	d.outputs = append(d.outputs, outputs...)
	return nil
}

// result reports the names the node declares, or the collision that means it
// declares nothing coherent.
//
// The ledger's verdict and the projection are read through one function so no
// caller can take the names while skipping the check. Two call sites reading the
// two halves themselves would each be one line, and one of them would eventually
// be written with only the half its author was thinking about — which would
// compile, pass, and silently stop checking for that path.
func (d *nodeDeclaration) result() (resourceIDs, outputIDs []string, err error) {
	if err := d.mint.err(); err != nil {
		return nil, nil, err
	}
	resourceIDs, outputIDs = d.names()
	return resourceIDs, outputIDs, nil
}

// names projects the gathered entries to the names the node declares per key
// space, folding a name restated with the same body ([collapseEntries]) and
// keeping a repeat whose bodies differ.
//
// The fold is by body and never by name, and that distinction is the whole of
// what the declaration is worth. A repeat the assembled stack carries once is
// noise to a reader holding names without bodies — it reads as a collision that
// never happened. A repeat whose bodies differ is a body about to be lost, and
// the repeated name is the only trace of it such a reader can act on, so folding
// by name would erase exactly the case the declaration exists to surface.
func (d *nodeDeclaration) names() (resourceIDs, outputIDs []string) {
	return entryKeys(collapseEntries(d.resources)), entryKeys(collapseEntries(d.outputs))
}

// entryKeys reads the names off a run of entries, in the order they stand.
func entryKeys(entries []stackEntry) []string {
	if len(entries) == 0 {
		return nil
	}
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		out = append(out, e.key)
	}
	return out
}

// DeclaredNames reads a node's already-normalized fragments and returns the names
// the assembled stack will address that node's content by: the logical ids of its
// resources, and the logical ids of the outputs its provides and explicit Outputs
// entries project to. The guest hands these to the host, which refuses a name two
// contributors to one assembled stack both claim.
//
// Resource ids come back exactly as the fragments authored them, because that is
// what the merge emits. Output ids come back already scoped by the node, because
// that is what the merge emits for them. Each fragment's names come back ordered
// by name, and the fragments in the order the caller supplied, so a declaration
// is a function of the node's fragments alone.
//
// The whole node is read at once because the whole node is what the declaration
// speaks for. Taking one fragment at a time would leave the caller to join the
// answers, and a join cannot see across fragments: two fragments restating one
// name with the same body reach the assembled stack once, so a joined declaration
// would offer a name the stack does not carry.
//
// The names are the keys of the entries [fragmentEntries] produces, which is the
// same derivation the merge inserts — so what is declared here cannot drift from
// what the assembled stack ends up addressing its content by. For a node that
// assembles, these are exactly the names the stack carries for it: no more and no
// fewer.
//
// Two differences from the merge's own walk, both deliberate. Order: a
// declaration orders each fragment's names by name, while the merge takes each
// section in turn. And multiplicity, for a node the merge refuses: two claims on
// one name with different bodies declare that name twice, because one of the two
// bodies would be lost, and the repetition is the only trace of that a reader
// holding the names without the bodies can act on.
//
// A node whose fragments carry none of those sections declares nothing, which is
// a valid answer rather than an error.
func DeclaredNames(nodeID string, fragments [][]byte) (resourceIDs, outputIDs []string, err error) {
	var d nodeDeclaration
	for _, fragment := range fragments {
		if err := d.addFragment(nodeID, fragment); err != nil {
			return nil, nil, err
		}
	}
	return d.result()
}

// DeclaredNamesSourced returns the names a sourced source's packed files declare
// for one node, reading the same files DeclaredNames would and skipping the same
// non-template content NormalizeSourced vendors verbatim — a directory source's
// README or parameters file carries no name the assembled stack addresses
// anything by. A single-file source's lone template is read whole.
//
// A source's files are the node's fragments, so they are gathered and folded as
// one declaration exactly as a fragment set is: two of a directory source's
// template files restating one name with the same body reach the assembled stack
// once, and the node declares it once.
func DeclaredNamesSourced(nodeID string, files []SourcedFile, rootIsDir bool) (resourceIDs, outputIDs []string, err error) {
	var d nodeDeclaration
	for _, f := range files {
		if rootIsDir && !isTemplateFile(f.Path) {
			continue
		}
		if err := d.addFragment(nodeID, f.Content); err != nil {
			return nil, nil, fmt.Errorf("cloudformation-provider: read declared names from sourced template %q: %w", f.Path, err)
		}
	}
	return d.result()
}

// sortedKeys returns a map's keys in lexical order so any iteration that must be
// deterministic (resource merging, Outputs merging, name declaration) walks a
// stable order.
func sortedKeys(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
