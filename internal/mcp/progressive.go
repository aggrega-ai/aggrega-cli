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
var deferredToolRegistry = []toolSpec{
	lintToolSpec,
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
	name: describeCapabilitiesName,
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
func (s *server) deferredTools() []toolSpec {
	out := make([]toolSpec, 0, len(deferredToolRegistry)+len(s.commandTools))
	out = append(out, deferredToolRegistry...)
	out = append(out, s.commandTools...)
	return out
}

func describeCapabilitiesInvoke(s *server, _ context.Context, args json.RawMessage) (*callToolResult, *rpcError) {
	var a describeCapabilitiesArgs
	if len(args) > 0 {
		if err := json.Unmarshal(args, &a); err != nil {
			return nil, invalidToolArguments(describeCapabilitiesName, err)
		}
	}
	kind := strings.ToLower(strings.TrimSpace(a.Kind))

	var caps []capabilityDescriptor
	if kind == "" {
		// No kind in play: return the long tail — every deferred tool's
		// definition — so a client discovers what the default list omits.
		deferred := s.deferredTools()
		for i := range deferred {
			caps = append(caps, descriptorFor(deferred[i], true))
		}
	} else {
		if !isKnownEntityKind(kind) {
			return errorResult(fmt.Sprintf("unknown entity kind %q. Valid kinds: %s",
				kind, strings.Join(knownEntityKinds(), ", ")))
		}
		caps = capabilitiesForKind(kind)
	}

	payload := describeCapabilitiesResult{Kind: kind, Capabilities: caps}
	return textResult(renderCapabilitiesText(payload), payload)
}

// capabilitiesForKind returns the tools that serve kind, walking the curated set
// then the deferred set so the surfaced order is deterministic. The capability
// tool itself is skipped — it never lists itself as a capability for a kind.
//
// The generated command tools are deliberately absent from a kind-scoped
// result, and reachable through the unscoped long tail instead. A command's
// relevance to an entity kind is recorded nowhere the derivation could read it,
// so every command tool is kind-agnostic — and surfacing the kind-agnostic set
// here would answer a request to narrow with the entire command surface, which
// is the opposite of narrowing. Claiming a subset serves a kind would mean this
// package deciding something about the commands rather than reading it.
func capabilitiesForKind(kind string) []capabilityDescriptor {
	var out []capabilityDescriptor
	collect := func(specs []toolSpec, deferred bool) {
		for i := range specs {
			if specs[i].name == describeCapabilitiesName {
				continue
			}
			if toolServesKind(specs[i], kind) {
				out = append(out, descriptorFor(specs[i], deferred))
			}
		}
	}
	collect(toolRegistry, false)
	collect(deferredToolRegistry, true)
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
func descriptorFor(spec toolSpec, deferred bool) capabilityDescriptor {
	return capabilityDescriptor{
		Name:        spec.name,
		Description: spec.description,
		InputSchema: json.RawMessage(spec.inputSchema),
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

// firstSentence returns the leading sentence of a tool description for the
// one-line human summary, so the text rendering stays compact while the
// structured payload carries every field in full.
func firstSentence(desc string) string {
	if i := strings.IndexByte(desc, '.'); i >= 0 {
		return desc[:i+1]
	}
	return desc
}
