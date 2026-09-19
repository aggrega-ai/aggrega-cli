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

// deferredToolRegistry is the long-tail tool set the default tools/list does
// NOT advertise. Keeping the default list to the curated set plus one capability
// tool holds the up-front definition budget a client loads small; a client
// discovers a deferred tool through the capability tool and then invokes it by
// name through the same tools/call path as any curated tool. The order is fixed
// so the surfaced list and its wire bytes are deterministic.
//
// Deferred or not, an entry names the operation it answers and takes its
// advertised name from that operation's row: withholding a tool from the default
// listing changes when a client learns of it, never whether the operation table
// accounts for it.
//
// The two candidacy tools sit here rather than in the default set, and the
// budget ceiling's own criterion is what puts them there. What that ceiling
// exists to catch is a tool serving a narrow case creeping into the list every
// client loads up front; the raise it once took was argued on the opposite
// ground — the enumeration tools are the entry point to the whole surface,
// because until they landed a caller arriving cold could name no subject at all.
// Candidacy is not that. It answers at one moment in authoring — when a caller
// is about to propose an attachment or an edge — and it needs a name the
// enumeration already had to supply, so a caller can reach every other tool here
// without it. Deferring them costs one discovery call at the moment they become
// relevant, and the kind filter surfaces both for the kinds an author is working
// in.
// The insertion-point tool sits here on that same criterion, one step further
// along the same moment: it answers when a caller has settled what to attach and
// is about to write it, and it needs a document the enumeration already had to
// supply. It is kind-agnostic rather than filed under the kinds candidacy serves,
// because what it reads is a document's own structure — so the kind filter
// surfaces it wherever an author is working rather than for a chosen few.
// The attachment-container tool sits between those two on the same criterion. It
// answers once a caller has settled what to attach and before it can ask where
// the text goes, and it needs a node the enumeration already had to supply. It
// carries the kinds candidacy carries rather than none, because it answers about
// a node of a compiled graph rather than about any document's structure.
// The two inventories sit here on the same criterion, and each was tested against
// it rather than filed here for room. The cache inventory answers at one moment —
// when a caller is deciding whether to reclaim space — and nothing else needs it
// first. The configured-registry listing and the index search answer about what
// lies outside the workspace, which is a question a caller asks after it knows
// what the workspace already holds; the enumeration tools that answer THAT are
// what a caller arriving cold cannot proceed without, and they remain in the
// default set for exactly that reason. Each of these three is reachable by name
// once the capability tool has surfaced it, and each is also reachable, for a
// caller at this machine's keyboard, as the tool generated from its own command,
// so deferring costs a discovery call rather than access.
// The four publication verbs sit here on the same criterion, tested against it
// rather than filed here for room. Each answers at one moment — when a caller has
// finished authoring and is ready to put something where other people will resolve
// it — and none of them is reachable-from-cold in the way the enumeration tools
// are: a publication needs an entity or a reference the caller already has to have
// found, so every one of them is downstream of a tool the default set already
// carries. Deferring them costs one discovery call at the moment they become
// relevant. It is also the conservative side of the only asymmetry here that is not
// about budget: these are the tools that write beyond this machine, and a set a
// client loads up front is a set an assistant reaches for without having asked
// what it does.
var deferredToolRegistry = []toolSpec{
	curated("lint", lintToolSpec),
	curated("feature-attach-candidates", attachCandidatesToolSpec),
	curated("connection-candidates", connectionCandidatesToolSpec),
	curated("document-insertion-point", insertionPointToolSpec),
	curated("attachment-container", attachmentContainerToolSpec),
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
// "Is what I am about to attempt possible here?" needs a name, a purpose, the
// kinds a tool serves and whether it writes — for the whole surface, a few
// kilobytes. "How exactly do I call this one?" needs one argument schema.
// Answering the first by paying the second once per tool produces a result
// larger than a client will accept, at which point the deferral saves nothing
// because the call that recovers it cannot be delivered.
//
// So an argumentless call, and a call narrowed to an entity kind, return an
// INDEX; a call naming tools returns those tools' full definitions and nothing
// else. Paging the definitions instead would still pay for them, in instalments,
// and would leave a caller unable to see the whole space at once — which is the
// one thing the first question needs.
//
// 🔴 The index is PROJECTED from the registries, never authored beside them. A
// hand-maintained list would be a second copy of every tool's purpose, agreeing
// on the day it was written and drifting at the first edit of either copy — and
// it would satisfy every size measurement perfectly while doing so.

// describeCapabilitiesArgs is the capability tool's argument shape. No argument
// returns the index of the deferred long tail; a kind returns the index narrowed
// to the tools that serve that entity kind; naming tools returns those tools'
// full definitions.
type describeCapabilitiesArgs struct {
	Kind  string   `json:"kind"`
	Tools []string `json:"tools"`
}

// capabilityProjection is one tool as the index carries it: its name, the
// one-line purpose declared on its spec, the entity kinds it serves, whether a
// call to it can leave something behind, and whether it is absent from the
// default list.
//
// It carries no argument schema, and that is the point of it being its own type
// rather than a descriptor with fields left empty. The index exists because the
// schemas are what makes the full answer undeliverable, so a shape that has
// nowhere to put one cannot regrow them by a later edit adding a field "while
// we are here".
type capabilityProjection struct {
	Name     string   `json:"name"`
	Summary  string   `json:"summary"`
	Kinds    []string `json:"kinds,omitempty"`
	Writes   bool     `json:"writes"`
	Deferred bool     `json:"deferred"`
}

// capabilityDescriptor is one tool's full definition: its name, its description,
// the JSON Schema for its arguments — the same definition a tools/list entry
// carries — plus whether the tool is deferred (absent from the default list) and
// the entity kinds it serves.
type capabilityDescriptor struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"inputSchema"`
	Deferred    bool            `json:"deferred"`
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

var describeCapabilitiesToolSpec = toolSpec{
	summary: "Indexes the tools the default list omits, and returns any of them in full by name.",
	description: "Discover the tools the default list omits, and load definitions only for the ones you decide to " +
		"call. With no arguments it returns an index: each deferred tool's name, a one-line purpose, the entity " +
		"kinds it serves, and whether it writes — and no argument schemas, so the whole surface arrives in one " +
		"readable answer. Pass `tools` naming one or more of them for those tools' full definitions, argument " +
		"schemas included, and nothing else. Pass `kind` (an entity kind such as release, component, feature, " +
		"recipe, plugin) for the index narrowed to the tools that serve that kind. Every tool the index names is " +
		"invocable immediately by name; this tool reveals definitions, it does not run them.",
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
	out = append(out, s.withTwinNotes(deferredToolRegistry)...)
	out = append(out, s.commandTools...)
	return s.admittedReach(ctx).admitted(out)
}

// describeCapabilitiesInvoke answers the capability tool.
//
// ctx is the request's own and is consulted for one thing: which tools this
// request's credential admits. The answer is therefore composed per request, on
// the same predicate the default listing is composed on, so the two surfaces
// agree about one request rather than agreeing because nothing narrowed either.
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
		return textResult(renderCapabilityIndex(payload), payload)
	}

	// No argument: the index of the long tail — what the default list omits —
	// so a client sees the whole space before it loads any of it.
	payload := describeCapabilitiesResult{Index: projectTools(s.deferredTools(ctx), true)}
	return textResult(renderCapabilityIndex(payload), payload)
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
// An unresolved name fails the whole call rather than being skipped. A result
// quietly short of what was asked for is read as an answer, and the caller then
// believes it holds a definition it never received.
func (s *server) definitionsFor(ctx context.Context, names []string) (*callToolResult, *rpcError) {
	curated := s.curatedTools(ctx)
	deferred := s.deferredTools(ctx)

	caps := make([]capabilityDescriptor, 0, len(names))
	var unresolved []string
	for _, name := range names {
		// The two sets are scanned separately rather than as one, because which of
		// them answered is what says whether the tool is absent from the default
		// list — the same fact the index reported for it.
		if spec, ok := lookupToolIn(name, curated); ok {
			caps = append(caps, s.descriptorFor(*spec, false))
			continue
		}
		if spec, ok := lookupToolIn(name, deferred); ok {
			caps = append(caps, s.descriptorFor(*spec, true))
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
func projectTools(specs []toolSpec, deferred bool) []capabilityProjection {
	out := make([]capabilityProjection, 0, len(specs))
	for i := range specs {
		out = append(out, capabilityProjection{
			Name:     specs[i].name,
			Summary:  specs[i].summary,
			Kinds:    specs[i].kinds,
			Writes:   specs[i].writes(),
			Deferred: deferred,
		})
	}
	return out
}

// indexForKind returns the index of the tools that serve kind, walking the
// curated set then the deferred set so the order is deterministic. The
// capability tool itself is skipped — it never lists itself as a capability for
// a kind.
//
// It answers with an index rather than with definitions for the same reason the
// argumentless call does: narrowing to a kind is still discovery, and a narrowed
// answer that carried every matching tool's argument schema would be the same
// undeliverable shape one step smaller. A caller that has found what serves its
// kind names those tools and receives their definitions.
//
// Both sets are taken as this session advertises them to this request, which is
// the same definition tools/list and the unscoped long tail carry: a narrowed
// listing that dropped a tool's disclosure of its generated twin, or that
// surfaced a tool the request's credential does not admit, would hand an
// assistant asking about one entity kind a different answer from the one it
// would have read on either other surface.
//
// The generated command tools are deliberately absent from a kind-scoped
// result, and reachable through the unscoped long tail instead. A command's
// relevance to an entity kind is recorded nowhere the derivation could read it,
// so every command tool is kind-agnostic — and surfacing the kind-agnostic set
// here would answer a request to narrow with the entire command surface, which
// is the opposite of narrowing. Claiming a subset serves a kind would mean this
// package deciding something about the commands rather than reading it.
func (s *server) indexForKind(ctx context.Context, kind string) []capabilityProjection {
	admission := s.admittedReach(ctx)
	var out []capabilityProjection
	collect := func(specs []toolSpec, deferred bool) {
		serving := make([]toolSpec, 0, len(specs))
		for i := range specs {
			if specs[i].name == describeCapabilitiesName {
				continue
			}
			if toolServesKind(specs[i], kind) {
				serving = append(serving, specs[i])
			}
		}
		out = append(out, projectTools(serving, deferred)...)
	}
	collect(s.curatedTools(ctx), false)
	collect(admission.admitted(s.withTwinNotes(deferredToolRegistry)), true)
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
// so the definition surfaced here is the definition the default listing carries
// — the arguments this session lets a caller send included. A listing that
// composed its own descriptor would be free to surface a tool under arguments
// the call surface refuses.
func (s *server) descriptorFor(spec toolSpec, deferred bool) capabilityDescriptor {
	return capabilityDescriptor{
		Name:        spec.name,
		Description: s.advertisedDescription(spec),
		InputSchema: s.advertisedSchema(spec),
		Deferred:    deferred,
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
func renderCapabilityIndex(p describeCapabilitiesResult) string {
	var b strings.Builder
	if p.Kind == "" {
		fmt.Fprintf(&b, "The tools this server offers you that the default list omits (%d). "+
			"Names and purposes only — call %s again with `tools` naming the ones you want, "+
			"for their full definitions and argument schemas.\n", len(p.Index), describeCapabilitiesName)
	} else {
		fmt.Fprintf(&b, "The tools that serve the %s kind (%d), the default list included. "+
			"Names and purposes only — call %s again with `tools` naming the ones you want, "+
			"for their full definitions and argument schemas.\n", p.Kind, len(p.Index), describeCapabilitiesName)
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
// Whether a tool is absent from the default list is written per line only where
// the index holds both sorts. An index that is entirely one or the other says so
// once, in the heading above it, and repeating it on every line would spend the
// width that carries what each tool actually does.
func capabilityIndexLines(index []capabilityProjection) []string {
	mixed := false
	for _, e := range index {
		if e.Deferred != index[0].Deferred {
			mixed = true
			break
		}
	}
	out := make([]string, 0, len(index))
	for _, e := range index {
		traits := make([]string, 0, 3)
		if mixed && e.Deferred {
			traits = append(traits, "not in the default list")
		}
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
		if c.Deferred {
			fmt.Fprintf(&b, "%s (not in the default tool list)\n", c.Name)
		} else {
			fmt.Fprintf(&b, "%s\n", c.Name)
		}
		fmt.Fprintf(&b, "%s\n", c.Description)
		fmt.Fprintf(&b, "Arguments (JSON Schema): %s\n", string(c.InputSchema))
	}
	return strings.TrimRight(b.String(), "\n")
}
