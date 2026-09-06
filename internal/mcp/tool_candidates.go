// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/pipeline"
	"github.com/aggrega-ai/aggrega-cli/internal/compilerops"
	"github.com/aggrega-ai/aggrega-cli/internal/serve"
)

// attachCandidatesArgs is the attach-candidates tool's argument shape.
//
// The three coordinates address three different things and exactly one is
// given. Node names a workspace document by its metadata.name. GraphNode names a
// node on the compiled graph of a Release, by the id that graph keys it under,
// and Release says which Release's graph to build. Labels carries a label set
// outright, and names nothing at all.
//
// The first two are separate arguments because their identifier spaces OVERLAP.
// A graph node id is constrained to a pattern; a document's metadata.name
// carries no pattern at all, so a name legal as one is frequently legal as the
// other, and a single argument carrying both would be ambiguous by
// construction. Deciding which space to search by inspecting the value would be
// the compiler guessing meaning out of a string, which it does nowhere else. So
// the caller says which question it is asking.
//
// Labels is the shape underneath both of them: the match is a function of a
// label set and the Feature declarations and of nothing else, so a caller that
// already holds a label set has nothing to name and nothing to look up. That is
// what makes it the coordinate-free form rather than an advanced one.
type attachCandidatesArgs struct {
	Node          string            `json:"node"`
	GraphNode     string            `json:"graph_node"`
	Release       string            `json:"release"`
	Labels        map[string]string `json:"labels"`
	WorkspaceRoot string            `json:"workspace_root"`
	CatalogDirs   []string          `json:"catalog_dirs"`
}

// coordinates names the addressing arguments the call carried, in the order the
// shape declares them, so a refusal can say which ones arrived rather than
// describing the shape back at a caller that already has it.
//
// A label set counts as given when the key is present at all, the empty object
// included. An empty label set is a subject — a node carrying no labels, which
// every Feature declaring no requirement admits — so reading it as "no
// coordinate" would refuse a legitimate question. Absent and null both decode to
// a nil map and neither is a coordinate; that is the distinction this rests on,
// and it is why the field is a map rather than a count or a flag.
func (a attachCandidatesArgs) coordinates() []string {
	var given []string
	if a.Node != "" {
		given = append(given, "node")
	}
	if a.GraphNode != "" {
		given = append(given, "graph_node")
	}
	if a.Labels != nil {
		given = append(given, "labels")
	}
	return given
}

// attachCandidatesPayload is the attach-candidates tool's structured payload:
// the workspace the answer was computed over, and the candidacy answer itself.
//
// The answer is embedded rather than restated field by field, so the keys a
// caller reads here are the keys every surface reports the same answer under,
// by construction rather than by agreement. What this surface adds is the
// workspace coordinate, which the caller supplied and the other surface does
// not have to.
type attachCandidatesPayload struct {
	WorkspaceRoot string `json:"workspace_root"`
	serve.AttachCandidatesResult
}

var attachCandidatesToolSpec = toolSpec{
	kinds: []string{"aggregate", "component", "feature"},
	description: "Report which Features may attach to a node: the Features whose declared applicability the node's labels " +
		"satisfy, evaluated through the same label match a build performs — so what you offer is what the compiler would " +
		"match. Address the node in exactly one of three ways. `node` names a workspace entity by its `metadata.name`. " +
		"`graph_node` names a node on a compiled graph, alongside the `release` whose graph it is. `labels` supplies a " +
		"label set outright and names nothing at all — it needs no coordinate, so pass the labels you already hold and " +
		"skip the lookup; this is the form to reach for whenever you have them. The answer carries the labels the match " +
		"was evaluated against and which of the three sets they came from — `declared` (what a document declares), " +
		"`resolved` (a compiled graph node's finalised set, which is what a build evaluates) or `supplied` (yours, which " +
		"nothing here vouches for) — so you can explain any candidate from the answer alone instead of re-deriving it, " +
		"which is how your answer and the compiler's come apart. It ANSWERS AND STOPS: it creates no attachment, writes " +
		"nothing to any file and proposes no text; composing and applying any edit that follows is yours. Candidates come " +
		"from the workspace and the configured catalog directories; join each on kind, name and file against list-entities " +
		"for anything more to display. A `node` or `graph_node` that does not resolve is refused rather than answered with " +
		"an empty list, so a typo never reads as \"nothing may attach here\"; a `labels` call has no name to mistype, so an " +
		"empty list there means what it says.",
	inputSchema: `{
  "type": "object",
  "properties": {
    "node": {
      "type": "string",
      "minLength": 1,
      "description": "A workspace entity's metadata.name, as list-entities reports it. The match then runs over the labels that document declares."
    },
    "graph_node": {
      "type": "string",
      "minLength": 1,
      "description": "A node id on the compiled graph of a Release, as the graph keys it. Requires release. The match then runs over that node's finalised labels."
    },
    "release": {
      "type": "string",
      "minLength": 1,
      "description": "Path to the Release whose graph carries graph_node, absolute or relative to the workspace root. Meaningful only alongside graph_node."
    },
    "labels": {
      "type": "object",
      "additionalProperties": { "type": "string" },
      "description": "A label set to match against directly, naming no node. An empty object is a subject in its own right — a node carrying no labels — not an absent argument."
    },
    "workspace_root": {
      "type": "string",
      "description": "Optional workspace root override. Defaults to discovery from the server's working directory."
    },
    "catalog_dirs": {
      "type": "array",
      "items": { "type": "string" },
      "description": "Optional catalog directories, prepended (first-match-wins) to the workspace catalog search stack. Features they declare are candidates alongside the workspace's own."
    }
  },
  "oneOf": [
    {"required":["node"],"not":{"anyOf":[{"required":["graph_node"]},{"required":["labels"]},{"required":["release"]}]}},
    {"required":["graph_node","release"],"not":{"anyOf":[{"required":["node"]},{"required":["labels"]}]}},
    {"required":["labels"],"not":{"anyOf":[{"required":["node"]},{"required":["graph_node"]},{"required":["release"]}]}}
  ]
}`,
	invoke: attachCandidatesInvoke,
}

func attachCandidatesInvoke(s *server, ctx context.Context, args json.RawMessage) (*callToolResult, *rpcError) {
	var a attachCandidatesArgs
	if len(args) > 0 {
		if err := json.Unmarshal(args, &a); err != nil {
			return invalidToolArguments("attach-candidates", err)
		}
	}

	// The coordinates are counted rather than compared in pairs, and they are
	// refused together rather than silently ranked. A caller that sent two has a
	// bug whichever one it meant, and picking one for it would answer a question
	// it did not ask — on a surface where two of the spaces overlap, so the wrong
	// pick can still produce a plausible-looking answer.
	given := a.coordinates()
	switch {
	case len(given) == 0:
		return errorResult("attach-candidates addresses a subject with exactly one of 'node' (a workspace entity), " +
			"'graph_node' with 'release' (a node on a compiled graph), or 'labels' (a label set). Send one of the three.")
	case len(given) > 1:
		return errorResult("attach-candidates: " + strings.Join(given, ", ") + " address different things; send exactly one.")
	case a.Release != "" && a.GraphNode == "":
		return errorResult("attach-candidates: 'release' applies to 'graph_node'; a call sending '" + given[0] +
			"' builds no graph. Drop 'release', or address a graph node with it.")
	case a.GraphNode != "" && a.Release == "":
		return errorResult("attach-candidates: 'graph_node' names a node on a Release's compiled graph, so it needs " +
			"the 'release' whose graph to build. Add 'release', or address a workspace entity with 'node'.")
	}

	tw, err := s.resolveToolWorkspace(a.WorkspaceRoot, a.CatalogDirs)
	if err != nil {
		return errorResult(explainToolError("Could not resolve the workspace to report attach candidates.", err, "feature"))
	}
	view := tw.newView(nil)

	switch {
	case a.Labels != nil:
		result, answered := serve.AttachCandidatesForLabels(ctx, tw.root, view, a.Labels)
		if !answered {
			return errorResult("attach-candidates: the workspace read was interrupted before it completed, so there " +
				"is no answer to report. Call it again.")
		}
		return attachCandidatesResultFor(tw.root, result)
	case a.GraphNode != "":
		return s.attachCandidatesForGraphNode(ctx, tw, a)
	default:
		result, found := serve.AttachCandidatesForNode(ctx, tw.root, view, a.Node)
		if !found {
			return errorResult(explainDiagnostic(fmt.Sprintf(
				"No workspace entity is named %q, so there is nothing to report attach candidates for. Call "+
					"list-entities to see the names this workspace declares.", a.Node), "component"))
		}
		return attachCandidatesResultFor(tw.root, result)
	}
}

// attachCandidatesForGraphNode builds the named Release's graph and reports the
// candidacy for one of its nodes.
//
// # Which preview it runs, and why it needs no lockfile
//
// It runs the operation shaped for a surface that neither requires a lockfile
// nor fills one — the same operation this server's build tool runs — so a
// Release carrying no lockfile is answered from whatever pins the workspace
// already holds rather than refused. That is a property of the operation this
// server performs, not a relaxation of anything: a lockfile contract belongs to
// the operation rather than being a degree of strictness a surface picks. A
// surface that draws a graph for a person to reason about takes a stricter
// contract, and it keeps it.
//
// The render is skipped. This reads the node's labels out of the graph and
// discards everything else, and the graph is byte-identical whether or not the
// render ran, so skipping it keeps a question about label matching from loading
// a rendering plugin to answer itself.
func (s *server) attachCandidatesForGraphNode(ctx context.Context, tw *toolWorkspace, a attachCandidatesArgs) (*callToolResult, *rpcError) {
	releasePath := tw.absWorkspacePath(a.Release)
	lf := loadReleaseLockfile(releasePath)
	view := tw.newView(lf)

	preview, err := compilerops.PreviewForArtifactFromAvailablePins(
		ctx,
		compilerops.NewPreviewInputs(
			compilerops.FrozenExternalInputs(tw.cacheDir, lf),
			view,
			releasePath,
			tw.root,
			tw.cfg,
			s.serverVersion,
			compilerops.WithLockfile(lf, lf != nil),
		),
		pipeline.Options{SkipRender: true},
	)
	if err != nil {
		return errorResult(explainToolError(
			"Could not build the Release's graph to report attach candidates for a node on it.",
			err, previewFailureKind(preview, err)))
	}

	result, found := serve.AttachCandidatesForGraphNode(ctx, tw.root, view, preview.Pipeline.IRGraph, a.GraphNode)
	if !found {
		// Named the graph rather than the workspace, because that is what the
		// caller addressed. A caller told "no workspace entity is named X" after
		// asking about a graph node would look for a document it never meant.
		return errorResult(explainDiagnostic(fmt.Sprintf(
			"The compiled graph of %s has no node with id %q. Node ids are the graph's own keys — read them from "+
				"the graph a build or an inspect returns, and note that a node contributed by a sub-aggregate "+
				"carries a qualified id.", a.Release, a.GraphNode), "release"))
	}
	return attachCandidatesResultFor(tw.root, result)
}

func attachCandidatesResultFor(root string, result serve.AttachCandidatesResult) (*callToolResult, *rpcError) {
	payload := attachCandidatesPayload{WorkspaceRoot: root, AttachCandidatesResult: result}
	return textResult(renderAttachCandidatesText(payload), payload)
}

// renderAttachCandidatesText renders the human-readable candidacy answer: what
// the match was evaluated over, where those labels came from, and one line per
// candidate giving the coordinates a caller joins it by.
func renderAttachCandidatesText(p attachCandidatesPayload) string {
	var b strings.Builder
	if p.Node != "" {
		fmt.Fprintf(&b, "Features that may attach to %s (%d):\n", p.Node, len(p.Candidates))
	} else {
		fmt.Fprintf(&b, "Features that may attach to the supplied label set (%d):\n", len(p.Candidates))
	}
	for _, c := range p.Candidates {
		fmt.Fprintf(&b, "  %s %s", c.Kind, c.Name)
		if c.File != "" {
			fmt.Fprintf(&b, " [%s]", c.File)
		} else {
			fmt.Fprintf(&b, " [%s]", c.Origin)
		}
		b.WriteByte('\n')
	}
	if len(p.Candidates) == 0 {
		b.WriteString("  none — no Feature this workspace can see declares an applicability these labels satisfy\n")
	}
	fmt.Fprintf(&b, "\nMatched against the %s labels: %s", p.LabelSource, renderLabelPairs(p.NodeLabels))
	b.WriteString("\nThis reports a match and creates nothing: no attachment was made and no file was written.")
	return b.String()
}

// renderLabelPairs renders a label set in a stable order, so the text block is a
// function of the labels rather than of map iteration.
func renderLabelPairs(labels map[string]string) string {
	if len(labels) == 0 {
		return "(none)"
	}
	keys := make([]string, 0, len(labels))
	for k := range labels {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	pairs := make([]string, 0, len(keys))
	for _, k := range keys {
		pairs = append(pairs, k+"="+labels[k])
	}
	return strings.Join(pairs, ", ")
}

// connectionCandidatesArgs is the connection-candidates tool's argument shape.
//
// Node names the entity whose sockets are reported, by its metadata.name.
// Socket is optional and selects between the two questions a caller asks:
// omitted, the answer reports every socket the entity declares; named, it
// reports that one socket and only it — which is the only way to ask about a
// socket the entity does NOT declare, since such a socket appears in no
// declaration to enumerate.
//
// There is deliberately no catalog_dirs. The targets are the workspace-local
// Components and the sockets are read from the addressed document's own
// usesSchema, so a catalog search stack would change nothing about the answer
// and declaring one would advertise an argument that does not work.
type connectionCandidatesArgs struct {
	Node          string `json:"node"`
	Socket        string `json:"socket"`
	WorkspaceRoot string `json:"workspace_root"`
}

// connectionCandidatesPayload is the connection-candidates tool's structured
// payload: the workspace the answer was computed over, and the candidacy answer
// itself, embedded so the keys are the ones every surface reports it under.
type connectionCandidatesPayload struct {
	WorkspaceRoot string `json:"workspace_root"`
	serve.ConnectionCandidatesResult
}

var connectionCandidatesToolSpec = toolSpec{
	kinds: []string{"aggregate", "component", "feature"},
	description: "Report what an entity may connect to: the dependency sockets it declares, and per socket the workspace " +
		"Components whose declared labels satisfy the constraint that socket declares over its targets — evaluated through " +
		"the same match a build performs. Pass `node`, the entity's `metadata.name`. THE TWO HALVES ARE NOT EQUALLY " +
		"COMPLETE and the answer says which is which, so do not present them as one filtered list. The target half is " +
		"complete: those are the components the same constraint admits during a build. The socket half is not and cannot " +
		"be — an author may legitimately write a socket the catalog entity never declared — so the declared sockets are a " +
		"helpful start rather than the set of legal names. Each socket therefore reports whether it is `declared` or " +
		"`ad-hoc`, and the answer reports whether an undeclared socket is accepted on this entity at all: a Component's own " +
		"dependency surface accepts one, a Feature's attachment surface does not. Pass `socket` to ask about a single " +
		"socket by name, which is the only way to ask about one the entity does not declare. Unlike attach-candidates " +
		"there is NO coordinate-free form here: this answers about a named entity and takes no label set. It ANSWERS AND " +
		"STOPS: it creates no edge, writes nothing to any file and proposes no text; composing and applying any edit is " +
		"yours. Join each target on kind, name and file against list-entities for anything more to display. A name that " +
		"does not resolve is refused rather than answered with an empty socket list, so a typo never reads as an entity " +
		"with nothing to offer.",
	inputSchema: `{
  "type": "object",
  "properties": {
    "node": {
      "type": "string",
      "minLength": 1,
      "description": "The entity's metadata.name, as list-entities reports it."
    },
    "socket": {
      "type": "string",
      "minLength": 1,
      "description": "Optional single dependency socket to report. Omitted, every socket the entity declares is reported. Named, only that one — which is the only way to ask about a socket the entity does not declare."
    },
    "workspace_root": {
      "type": "string",
      "description": "Optional workspace root override. Defaults to discovery from the server's working directory."
    }
  },
  "required": ["node"]
}`,
	invoke: connectionCandidatesInvoke,
}

func connectionCandidatesInvoke(s *server, ctx context.Context, args json.RawMessage) (*callToolResult, *rpcError) {
	var a connectionCandidatesArgs
	if len(args) > 0 {
		if err := json.Unmarshal(args, &a); err != nil {
			return invalidToolArguments("connection-candidates", err)
		}
	}
	if strings.TrimSpace(a.Node) == "" {
		return errorResult("connection-candidates requires a 'node': the metadata.name of the entity whose sockets " +
			"to report. Call list-entities to see the names this workspace declares.")
	}

	tw, err := s.resolveToolWorkspace(a.WorkspaceRoot, nil)
	if err != nil {
		return errorResult(explainToolError("Could not resolve the workspace to report connection candidates.", err, "component"))
	}

	result, found := serve.ConnectionCandidates(ctx, tw.root, a.Node, a.Socket)
	if !found {
		return errorResult(explainDiagnostic(fmt.Sprintf(
			"No workspace entity is named %q, so there is nothing to report connection candidates for. Call "+
				"list-entities to see the names this workspace declares.", a.Node), "component"))
	}
	payload := connectionCandidatesPayload{WorkspaceRoot: tw.root, ConnectionCandidatesResult: result}
	return textResult(renderConnectionCandidatesText(payload), payload)
}

// renderConnectionCandidatesText renders the human-readable connection answer:
// one block per socket carrying where the socket came from, the constraint it
// declares and its valid targets, then the statement that the socket list is
// open rather than closed where the entity's surface accepts an ad-hoc name.
//
// The openness is stated in the text and not left to the structured field alone,
// because the text block is what an assistant reads when it does not parse the
// result, and a list of declared sockets read as a closed set is exactly the
// flattening the two fields exist to prevent.
func renderConnectionCandidatesText(p connectionCandidatesPayload) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Dependency sockets on %s (%d):\n", p.Node, len(p.Sockets))
	for _, sock := range p.Sockets {
		fmt.Fprintf(&b, "  %s [%s]", sock.Socket, sock.Declaration)
		if sock.Required {
			b.WriteString(" (required)")
		}
		if sock.Selector != "" {
			fmt.Fprintf(&b, " selector: %s", sock.Selector)
		}
		b.WriteByte('\n')
		if len(sock.Targets) == 0 {
			b.WriteString("      no valid target in this workspace\n")
			continue
		}
		for _, t := range sock.Targets {
			fmt.Fprintf(&b, "      %s %s [%s]\n", t.Kind, t.Name, t.File)
		}
	}
	if len(p.Sockets) == 0 {
		b.WriteString("  none declared\n")
	}
	if p.AdHocSocketsAccepted {
		b.WriteString("\nThis entity accepts a socket its own declaration does not list, so the sockets above are a " +
			"starting point and not the set of legal names. Name a socket in the 'socket' argument to ask about one " +
			"that is not listed.")
	} else {
		b.WriteString("\nThis entity's socket surface is closed: its own declaration is the whole of what its sockets " +
			"may be, and a socket absent from it halts the build.")
	}
	b.WriteString("\nThe targets are complete for each socket's constraint. This reports and creates nothing: no edge " +
		"was created and no file was written.")
	return b.String()
}
