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

// describeCapabilitiesArgs is the capability tool's argument shape. An empty
// kind returns the full long tail; a kind narrows the surfaced set to the tools
// that serve that entity kind.
type describeCapabilitiesArgs struct {
	Kind string `json:"kind"`
}

// capabilityDescriptor is one tool the capability tool surfaces: its name, its
// description, the JSON Schema for its arguments — the same definition a
// tools/list entry carries — plus whether the tool is deferred (absent from the
// default list) and the entity kinds it serves.
type capabilityDescriptor struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"inputSchema"`
	Deferred    bool            `json:"deferred"`
	Kinds       []string        `json:"kinds,omitempty"`
}

// describeCapabilitiesResult is the capability tool's structured payload: the
// kind the result was scoped to (empty for the unscoped long tail) and the
// surfaced tool definitions.
type describeCapabilitiesResult struct {
	Kind         string                 `json:"kind,omitempty"`
	Capabilities []capabilityDescriptor `json:"capabilities"`
}

var describeCapabilitiesToolSpec = toolSpec{
	description: "Discover the tools not shown in the default list, on demand. The default tool set is kept small; this " +
		"tool surfaces the long tail so an assistant loads their definitions only when it needs them. Call it with no " +
		"arguments to get every deferred tool's full definition — name, description, and argument schema — which you can " +
		"then invoke directly by name. Pass `kind` (an entity kind such as release, component, feature, recipe, plugin) to " +
		"narrow the result to the tools that serve that kind, including deferred ones you would not otherwise have loaded, " +
		"so you see the working set for the entity you are authoring. Every surfaced tool is invocable immediately; this " +
		"tool only reveals definitions, it does not run them.",
	inputSchema: `{
  "type": "object",
  "properties": {
    "kind": {
      "type": "string",
      "description": "Optional entity kind to scope the result to. When set, only tools that serve that kind are surfaced. When absent, the full deferred long tail is returned.",
      "enum": ["aggregate", "component", "feature", "featurerecipe", "manifest", "mappinglibrary", "plugin", "recipe", "release", "schemalibrary", "workload"]
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

	var caps []capabilityDescriptor
	if kind == "" {
		// No kind in play: return the long tail — every deferred tool's
		// definition — so a client discovers what the default list omits.
		deferred := s.deferredTools(ctx)
		for i := range deferred {
			caps = append(caps, s.descriptorFor(deferred[i], true))
		}
	} else {
		if !isKnownEntityKind(kind) {
			return errorResult(fmt.Sprintf("unknown entity kind %q. Valid kinds: %s",
				kind, strings.Join(knownEntityKinds(), ", ")))
		}
		caps = s.capabilitiesForKind(ctx, kind)
	}

	payload := describeCapabilitiesResult{Kind: kind, Capabilities: caps}
	return textResult(renderCapabilitiesText(payload), payload)
}

// capabilitiesForKind returns the tools that serve kind, walking the curated set
// then the deferred set so the surfaced order is deterministic. The capability
// tool itself is skipped — it never lists itself as a capability for a kind.
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
func (s *server) capabilitiesForKind(ctx context.Context, kind string) []capabilityDescriptor {
	admission := s.admittedReach(ctx)
	var out []capabilityDescriptor
	collect := func(specs []toolSpec, deferred bool) {
		for i := range specs {
			if specs[i].name == describeCapabilitiesName {
				continue
			}
			if toolServesKind(specs[i], kind) {
				out = append(out, s.descriptorFor(specs[i], deferred))
			}
		}
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

// renderCapabilitiesText renders the human-readable capability listing: a
// heading naming the scope and one line per surfaced tool.
func renderCapabilitiesText(p describeCapabilitiesResult) string {
	var b strings.Builder
	if p.Kind == "" {
		fmt.Fprintf(&b, "Deferred capabilities not in the default tool list (%d):\n", len(p.Capabilities))
	} else {
		fmt.Fprintf(&b, "Capabilities for kind %q (%d):\n", p.Kind, len(p.Capabilities))
	}
	for _, c := range p.Capabilities {
		marker := "curated"
		if c.Deferred {
			marker = "deferred"
		}
		fmt.Fprintf(&b, "  %s [%s]: %s\n", c.Name, marker, firstSentence(c.Description))
	}
	return strings.TrimRight(b.String(), "\n")
}

// The one-line human summary uses firstSentence, so the text rendering stays
// compact while the structured payload carries every field in full. It is
// declared beside the pair disclosure that shares it.
