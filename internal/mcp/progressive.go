// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/pkg/entitykind"
)

// deferredToolRegistry is the tail of the tool surface: the statically declared
// tools a caller arriving cold can reach the rest of the surface without. Every
// one of them is advertised in tools/list beside the set above, and the split
// decides two things only — the order the listing is composed in, and which
// tools a kind-scoped walk reads after the curated set.
//
// Membership is by ONE criterion, and it is the criterion each entry below was
// tested against rather than a place to file a tool for room: a tool belongs
// here when it answers at one moment in authoring and needs a subject some other
// tool already had to supply. The enumeration tools are the counter-case and sit
// in the set above, because until a caller has named a subject it can call none
// of these — a caller arriving cold could otherwise name nothing and have no way
// to find out what to name.
//
// So candidacy answers when a caller is about to propose an attachment or an
// edge; the attachment container answers once it has settled what to attach; the
// insertion point answers when it is about to write the text; the bounded edit
// answers when it has settled what to write and where, and applies it; the cache
// inventory answers when a caller is deciding whether to reclaim space; the
// configured-registry listing and the index search answer about what lies
// outside the workspace, which is a question asked after what is inside it is
// known; and the four publication verbs answer when a caller has finished
// authoring and is ready to put something where other people will resolve it.
//
// Here or above, an entry names the operation it answers and takes its
// advertised name from that operation's row, so which registry a tool sits in
// changes where it appears in a listing and never whether the operation table
// accounts for it. The order is fixed so the listing and its wire bytes are
// deterministic.
var deferredToolRegistry = []toolSpec{
	curated("lint", lintToolSpec),
	curated("feature-attach-candidates", attachCandidatesToolSpec),
	curated("connection-candidates", connectionCandidatesToolSpec),
	curated("document-insertion-point", insertionPointToolSpec),
	curated("attachment-container", attachmentContainerToolSpec),
	curated("document-edit", editDocumentToolSpec),
	curated("cache-status", cacheStatusToolSpec),
	curated("registry-list", registryListToolSpec),
	curated("search", searchToolSpec),
	curated("push", publishEntityToolSpec),
	curated("push-artifact", publishArtifactToolSpec),
	curated("sign", signArtifactsToolSpec),
	curated("deprecate", deprecateArtifactToolSpec),
}

// describeCapabilitiesName is the meta capability tool's name. It is referenced
// where dispatch guidance points a caller at capability discovery and where the
// kind-relevance walk skips the meta tool so it never lists itself.
const describeCapabilitiesName = "describe-capabilities"

// Discovery and delivery are two questions, and this tool answers them in two
// shapes.
//
// "Which tools serve the kind I am authoring?" needs a name, a purpose, the
// kinds a tool serves and whether it writes — for a whole set of them, a few
// kilobytes. "How exactly do I call this one?" needs one argument schema.
// Answering the first by paying the second once per tool produces a result
// larger than a client will accept, so an index carries no argument schema at
// all and there is nowhere for one to grow back.
//
// So an argumentless call, and a call narrowed to an entity kind, return an
// INDEX; a call naming tools returns those tools' full definitions and nothing
// else. Paging the definitions instead would still pay for them, in instalments,
// and would leave a caller unable to see the whole space at once — which is the
// one thing the first question needs.
//
// The kind-scoped answer is what this tool is FOR. Every tool declares the
// entity kinds it serves, so narrowing to a kind reports what those declarations
// say rather than what a description happens to be worded like — which is the
// one thing a retrieval pass over tool text cannot reproduce, because authoring
// a Recipe implying a particular working set is a fact about the entity model
// and not about the words beside each tool. The unscoped index is the same
// projection with nothing narrowed, so a caller that has not settled on a kind
// can still read the whole space in one answer.
//
// 🔴 The index is PROJECTED from the registries, never authored beside them. A
// hand-maintained list would be a second copy of every tool's purpose, agreeing
// on the day it was written and drifting at the first edit of either copy — and
// it would satisfy every size measurement perfectly while doing so.

// describeCapabilitiesArgs is the capability tool's argument shape. No argument
// returns the index of every tool this server announces to the caller; a kind
// returns that index narrowed to the tools that serve that entity kind; naming
// tools returns those tools' full definitions.
type describeCapabilitiesArgs struct {
	Kind  string   `json:"kind"`
	Tools []string `json:"tools"`
}

// capabilityProjection is one tool as the index carries it: its name, the
// one-line purpose declared on its spec, the entity kinds it serves, and whether
// a call to it can leave something behind.
//
// It carries no argument schema, and that is the point of it being its own type
// rather than a descriptor with fields left empty. The index exists because the
// schemas are what makes the full answer undeliverable, so a shape that has
// nowhere to put one cannot regrow them by a later edit adding a field "while
// we are here".
type capabilityProjection struct {
	Name    string   `json:"name"`
	Summary string   `json:"summary"`
	Kinds   []string `json:"kinds,omitempty"`
	Writes  bool     `json:"writes"`
}

// capabilityDescriptor is one tool's full definition: its name, its description,
// the JSON Schema for its arguments — the same definition a tools/list entry
// carries — and the entity kinds it serves.
type capabilityDescriptor struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"inputSchema"`
	Kinds       []string        `json:"kinds,omitempty"`
}

// describeCapabilitiesResult is the capability tool's structured payload, and it
// carries one of two payloads rather than both: Index for a discovery call —
// argumentless, or narrowed to Kind — and Capabilities for a call that named
// Tools.
//
// Which fields are set is what tells a caller which question it asked, so the
// two never appear together and an empty one is absent from the wire rather than
// present and empty.
type describeCapabilitiesResult struct {
	Kind         string                 `json:"kind,omitempty"`
	Tools        []string               `json:"tools,omitempty"`
	Index        []capabilityProjection `json:"index,omitempty"`
	Capabilities []capabilityDescriptor `json:"capabilities,omitempty"`
}

// kindScopeCommandToolNote is the one bound on a kind-scoped answer, written
// once because the tool's own description and the heading over a narrowed answer
// both carry it. A caller that does not know the bound reads a narrowed answer
// as a complete one, and the moment it matters is the moment the narrowed answer
// arrives — so the sentence has to reach both surfaces, and a second wording of
// it would agree only until one of the two was edited.
//
// Both surfaces carry it only where this session derived those tools, on the one
// predicate [server.offersCommandTools]. A session that derived none has no such
// family for a narrowed answer to leave out, so the sentence would be a bound on
// tools that caller cannot reach — which is also why it states the bound plainly
// rather than hedging about whether the tools are there: where it is read, they
// are.
const kindScopeCommandToolNote = "The `cli-` tools, which run an `aggrega` command in a separate process, " +
	"are never narrowed by kind: nothing records which entity kind a command serves. A kind-scoped answer " +
	"leaves them out, and the argumentless index is where you read them."

var describeCapabilitiesToolSpec = toolSpec{
	summary: "Narrows this server's tools to the ones that serve one entity kind, and returns any tool's full definition by name.",
	description: "Find the tools that serve the entity kind you are authoring, then load full definitions only " +
		"for the ones you decide to call. Pass `kind` (an entity kind such as release, component, feature, " +
		"recipe, plugin) for the working set for authoring that kind: every tool here declares the entity kinds " +
		"it serves, so a narrowed answer is what those declarations say rather than a match on the words a " +
		"description happens to use. With no arguments you get that same index over every tool this server " +
		"announces to you — each one's name, a one-line purpose, the entity kinds it serves, and whether it " +
		"writes, and no argument schemas, so the whole surface arrives in one readable " +
		"answer. Pass `tools` naming one or more of them for those tools' full definitions, argument schemas " +
		"included, and nothing else. It answers about this server's own tools and no others. Every tool the " +
		"index names is one this server will answer a call to; this tool reads definitions, it does not run them.",
	commandSurfaceNote: kindScopeCommandToolNote,
	inputSchema: `{
  "type": "object",
  "properties": {
    "kind": {
      "type": "string",
      "description": "Entity kind to narrow the index to, so it lists only the tools serving that kind. Cannot be combined with tools.",
      "enum": ["aggregate", "component", "feature", "featurerecipe", "manifest", "mappinglibrary", "plugin", "recipe", "release", "schemalibrary", "workload"]
    },
    "tools": {
      "type": "array",
      "items": {"type": "string"},
      "description": "Tool names, as the index reports them, to return complete definitions for. The result then carries exactly those tools and nothing else.",
      "minItems": 1
    }
  }
}`,
	invoke: describeCapabilitiesInvoke,
}

// deferredTools returns the deferred long tail this session serves: the
// statically declared deferred registry followed by the command tools derived
// from the supplied command surface.
//
// The two are concatenated in a fixed order, each already in its own fixed
// order, so the surfaced long tail and its wire bytes are stable across runs.
//
// The statically declared half is served as this session advertises it, since a
// deferred tool answering a capability a command also carries discloses the
// generated tool that reaches it. The generated half needs no such pass: each of
// those tools already carries its curated sibling's name, derived when the tool
// was.
//
// Both halves are then narrowed to what this request's credential admits, in one
// pass over the concatenation rather than one per half: the generated half is
// empty on the transport where a credential bounds anything, so filtering it
// separately would be a second call to the same predicate that could only ever
// agree with the first.
func (s *server) deferredTools(ctx context.Context) []toolSpec {
	out := make([]toolSpec, 0, len(deferredToolRegistry)+len(s.commandTools))
	out = append(out, s.withSessionNotes(deferredToolRegistry)...)
	out = append(out, s.commandTools...)
	return s.admittedReach(ctx).admitted(out)
}

// describeCapabilitiesInvoke answers the capability tool.
//
// ctx is the request's own and is consulted for one thing: which tools this
// request's credential admits. The answer is therefore composed per request, on
// the same predicate the tool listing is composed on, so the two surfaces agree
// about one request rather than agreeing because nothing narrowed either.
func describeCapabilitiesInvoke(s *server, ctx context.Context, args json.RawMessage) (*callToolResult, *rpcError) {
	var a describeCapabilitiesArgs
	if len(args) > 0 {
		if err := json.Unmarshal(args, &a); err != nil {
			return invalidToolArguments(describeCapabilitiesName, err)
		}
	}
	kind := strings.ToLower(strings.TrimSpace(a.Kind))
	named := namedTools(a.Tools)

	if kind != "" && len(named) > 0 {
		return errorResult("describe-capabilities takes either 'kind' or 'tools', not both: 'kind' narrows the " +
			"index to the tools that serve one entity kind, and 'tools' returns the full definitions of the " +
			"tools you name. Call it with 'kind' to find what serves that kind, then again with 'tools' " +
			"naming what you found.")
	}

	if len(named) > 0 {
		return s.definitionsFor(ctx, named)
	}

	if kind != "" {
		if !isKnownEntityKind(kind) {
			return errorResult(fmt.Sprintf("unknown entity kind %q. Valid kinds: %s",
				kind, strings.Join(knownEntityKinds(), ", ")))
		}
		payload := describeCapabilitiesResult{Kind: kind, Index: s.indexForKind(ctx, kind)}
		return textResult(s.renderCapabilityIndex(payload), payload)
	}

	// No argument: the index of every tool this request is offered, so a client
	// sees the whole space before it loads any of it.
	//
	// It reads the composition tools/list answers from rather than walking the
	// registries again. That is what fixes the order — each registry in its own
	// declared order, the three joined in one, nothing iterated from a map — and
	// it is also what keeps the index and the listing describing one surface: a
	// second walk would agree with the first until either was edited.
	payload := describeCapabilitiesResult{Index: projectTools(s.servedTools(ctx))}
	return textResult(s.renderCapabilityIndex(payload), payload)
}

// namedTools reads the tool names a call asked for, dropping blanks and
// repeats while keeping the order the caller wrote them in.
//
// A repeated name is dropped rather than answered twice: the caller asked for
// that tool's definition and one copy is the answer to that, while two would
// make a result's length depend on how the request was spelled.
func namedTools(raw []string) []string {
	out := make([]string, 0, len(raw))
	seen := make(map[string]bool, len(raw))
	for _, name := range raw {
		name = strings.TrimSpace(name)
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		out = append(out, name)
	}
	return out
}

// definitionsFor answers a call that named tools: those tools' full definitions,
// in the order they were named, and nothing else.
//
// It resolves each name against what this session serves this request rather
// than against the registries directly, so a name the request's credential does
// not admit is answered exactly as a name that does not exist — the index that
// request reads does not carry it either, and a definition handed back for a
// tool the same credential would be refused at the call is worse than no answer.
//
// The set it resolves against is the one the index was projected from, so a name
// read out of an index resolves here by construction rather than by the two
// happening to be composed from the same registries.
//
// An unresolved name fails the whole call rather than being skipped. A result
// quietly short of what was asked for is read as an answer, and the caller then
// believes it holds a definition it never received.
func (s *server) definitionsFor(ctx context.Context, names []string) (*callToolResult, *rpcError) {
	served := s.servedTools(ctx)

	caps := make([]capabilityDescriptor, 0, len(names))
	var unresolved []string
	for _, name := range names {
		if spec, ok := lookupToolIn(name, served); ok {
			caps = append(caps, s.descriptorFor(*spec))
			continue
		}
		unresolved = append(unresolved, name)
	}
	if len(unresolved) > 0 {
		return errorResult(fmt.Sprintf("no tool here is named %s. Call %s with no arguments for the index of "+
			"what this server offers you, and name the tools you want from it.",
			strings.Join(quoteAll(unresolved), ", "), describeCapabilitiesName))
	}

	payload := describeCapabilitiesResult{Tools: names, Capabilities: caps}
	return textResult(renderCapabilityDefinitions(payload), payload)
}

// projectTools projects specs into index entries, in the order given.
//
// It is the one route every index takes — the tool's own answer and the
// connect-time guidance alike — so the line a client reads on connecting and the
// line it reads from a call are the same derivation of the same registry. An
// index composed twice would agree until one of the two was edited.
func projectTools(specs []toolSpec) []capabilityProjection {
	out := make([]capabilityProjection, 0, len(specs))
	for i := range specs {
		out = append(out, capabilityProjection{
			Name:    specs[i].name,
			Summary: specs[i].summary,
			Kinds:   specs[i].kinds,
			Writes:  specs[i].writes(),
		})
	}
	return out
}

// indexForKind returns the index of the tools that serve kind, walking the
// curated set then the deferred set so the order is deterministic.
//
// The capability tool itself is skipped here and only here. It declares no kinds,
// so it serves every one of them, and a tool that appeared under each kind an
// author asked about would be noise in the one answer that exists to narrow. The
// unscoped index does carry it, because that index is what this server announces
// and a caller can check it against tools/list entry for entry.
//
// It answers with an index rather than with definitions for the same reason the
// argumentless call does: narrowing to a kind is still discovery, and a narrowed
// answer that carried every matching tool's argument schema would be the same
// undeliverable shape one step smaller. A caller that has found what serves its
// kind names those tools and receives their definitions.
//
// Both sets are taken as this session advertises them to this request, which is
// the same definition tools/list and the unscoped index carry: a narrowed
// listing that dropped a tool's disclosure of its generated twin, or that
// surfaced a tool the request's credential does not admit, would hand an
// assistant asking about one entity kind a different answer from the one it
// would have read on either other surface.
//
// The generated command tools are deliberately absent from a kind-scoped
// result, and reachable through the unscoped index instead. A command's
// relevance to an entity kind is recorded nowhere the derivation could read it,
// so every command tool is kind-agnostic — and surfacing the kind-agnostic set
// here would answer a request to narrow with the entire command surface, which
// is the opposite of narrowing. Claiming a subset serves a kind would mean this
// package deciding something about the commands rather than reading it. That
// bound is stated to the caller in kindScopeCommandToolNote wherever this session
// derived any of those tools, because a narrowed answer that silently omits a
// family reads as a complete one — and where it derived none there is no family
// here to omit, so the bound is withheld rather than asserted over nothing.
func (s *server) indexForKind(ctx context.Context, kind string) []capabilityProjection {
	admission := s.admittedReach(ctx)
	var out []capabilityProjection
	collect := func(specs []toolSpec) {
		serving := make([]toolSpec, 0, len(specs))
		for i := range specs {
			if specs[i].name == describeCapabilitiesName {
				continue
			}
			if toolServesKind(specs[i], kind) {
				serving = append(serving, specs[i])
			}
		}
		out = append(out, projectTools(serving)...)
	}
	collect(s.curatedTools(ctx))
	collect(admission.admitted(s.withSessionNotes(deferredToolRegistry)))
	return out
}

// toolServesKind reports whether spec serves the given entity kind. A spec with
// no declared kinds is kind-agnostic and serves every kind.
func toolServesKind(spec toolSpec, kind string) bool {
	if len(spec.kinds) == 0 {
		return true
	}
	for _, k := range spec.kinds {
		if k == kind {
			return true
		}
	}
	return false
}

// descriptorFor renders a tool's definition into the capability descriptor the
// capability tool surfaces.
//
// It is the one route every capability listing takes, scoped to a kind or not,
// so the definition surfaced here is the definition the tool listing carries
// — the arguments this session lets a caller send included. A listing that
// composed its own descriptor would be free to surface a tool under arguments
// the call surface refuses.
func (s *server) descriptorFor(spec toolSpec) capabilityDescriptor {
	return capabilityDescriptor{
		Name:        spec.name,
		Description: s.advertisedDescription(spec),
		InputSchema: s.advertisedSchema(spec),
		Kinds:       spec.kinds,
	}
}

// knownEntityKinds returns the entity-kind vocabulary the capability tool's kind
// filter accepts, sorted for a deterministic diagnostic. It reads the declared
// entity-kind set, so the filter admits every kind an author can write.
//
// It deliberately does not read the set of kinds that have a reference page. Which
// kinds exist and which of them are documented are different questions with
// different answers, and deriving the first from the second made a kind whose page
// had not been written yet indistinguishable from a kind that does not exist —
// which is what the author was then told.
func knownEntityKinds() []string {
	kinds := entitykind.Segments()
	sort.Strings(kinds)
	return kinds
}

// isKnownEntityKind reports whether kind is a recognized entity kind, named by its
// lowercase path segment.
func isKnownEntityKind(kind string) bool {
	_, ok := entitykind.BySegment(kind)
	return ok
}

// renderCapabilityIndex renders the human-readable index: a heading naming the
// scope and what to do next, then one line per tool.
//
// The heading over a narrowed answer states the bound that answer carries, and
// states it only where this session derived the tools it is a bound about — the
// same predicate and the same sentence the tool's own description is scoped on,
// so a caller reading the two is told one thing. Over a session that derived none
// the heading is written without it rather than with an emptied gap, because a
// bound on a family that session cannot reach is a claim about tools the caller
// was never offered.
func (s *server) renderCapabilityIndex(p describeCapabilitiesResult) string {
	var b strings.Builder
	if p.Kind == "" {
		fmt.Fprintf(&b, "Every tool this server offers you (%d). "+
			"Names and purposes only — call %s again with `tools` naming the ones you want, "+
			"for their full definitions and argument schemas.\n", len(p.Index), describeCapabilitiesName)
	} else {
		fmt.Fprintf(&b, "The tools that serve the %s kind (%d).", p.Kind, len(p.Index))
		if s.offersCommandTools() {
			fmt.Fprintf(&b, " %s", kindScopeCommandToolNote)
		}
		fmt.Fprintf(&b, " Names and purposes only — call %s again with `tools` naming the ones you want, "+
			"for their full definitions and argument schemas.\n", describeCapabilitiesName)
	}
	for _, line := range capabilityIndexLines(p.Index) {
		b.WriteString(line)
		b.WriteString("\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

// capabilityIndexLines renders an index as the one line per tool every index
// carries it as: the name, what is true of the tool, and its purpose.
//
// It is one function because the tool's own answer and the connect-time guidance
// both show these lines, and a caller that read the guidance and then called the
// tool would otherwise be comparing two renderings of one fact.
//
// A line carries what a caller chooses on — whether the tool writes, and which
// entity kinds it serves. Which registry a tool was declared in is not one of
// those: it decides the order this listing is composed in and nothing a caller
// can act on, so it is not written here.
func capabilityIndexLines(index []capabilityProjection) []string {
	out := make([]string, 0, len(index))
	for _, e := range index {
		traits := make([]string, 0, 2)
		if e.Writes {
			traits = append(traits, "writes")
		} else {
			traits = append(traits, "reads only")
		}
		if len(e.Kinds) == 0 {
			traits = append(traits, "any kind")
		} else {
			traits = append(traits, "kinds: "+strings.Join(e.Kinds, ", "))
		}
		out = append(out, fmt.Sprintf("  %s [%s]: %s", e.Name, strings.Join(traits, "; "), e.Summary))
	}
	return out
}

// renderCapabilityDefinitions renders the human-readable answer to a call that
// named tools: each tool's full description and the JSON Schema for its
// arguments.
//
// The schema is written into the text block as well as into the structured
// payload, because a caller that named a tool asked for the one thing it needs
// in order to compose a call, and a client that shows a model only the text
// block would hand it a description and no way to act on it.
func renderCapabilityDefinitions(p describeCapabilitiesResult) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Full definitions for %d tool(s).\n", len(p.Capabilities))
	for _, c := range p.Capabilities {
		b.WriteString("\n")
		fmt.Fprintf(&b, "%s\n", c.Name)
		fmt.Fprintf(&b, "%s\n", c.Description)
		fmt.Fprintf(&b, "Arguments (JSON Schema): %s\n", string(c.InputSchema))
	}
	return strings.TrimRight(b.String(), "\n")
}
