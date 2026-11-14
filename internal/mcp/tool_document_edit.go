// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package mcp

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/internal/authoredcontent"
	"github.com/aggrega-ai/aggrega-cli/internal/editops"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
)

// editDocumentToolName is the tool's advertised name, referenced where a path is
// resolved and where a refusal names the tool it is about, so the name has one
// home on this surface rather than being spelled again at each site.
const editDocumentToolName = "edit-document"

// documentEditArgs is the edit tool's argument shape.
//
// It is the request the three surfaces carrying this capability all send, in
// this protocol's own spelling. File names the document, relative to the
// workspace root or by absolute path, as every other path argument here is
// named. Edit names which of the bounded edits to apply and is the request's
// whole statement of what is being done. Node is the structural path to the node
// the edit is about, from the document root, spelled as its segments — the same
// breadcrumb vocabulary insertion-point takes and answers in, so a caller that
// asked where an entry belongs and a caller that applies one speak about the
// document in one language. Socket is the dependency socket a connection is
// wired inside. Name is the key the new entry is written under and Value is what
// that entry carries.
//
// Value is a JSON value rather than a string, and that is what keeps this
// surface from narrowing the capability. The command line resolves its value
// through the document's own language because a terminal carries only
// characters; a tool call carries JSON and already holds the type, so the value
// is passed through as it arrived. A caller that sends no value at all and a
// caller that sends an empty string are different requests and stay different
// here: the first leaves this field nil, which is the request that carries
// nothing, and the second carries the empty string. A caller that writes the
// JSON null is making the first request in the other spelling, and the two mean
// one thing to the layer below, so nothing is lost by their arriving as the same
// value.
//
// DryRun does everything the plain request does except write. Which of the two a
// caller wants is a property of the caller rather than of this surface, so it is
// a request field rather than a second tool, and it defaults to writing because
// the operation's product is an edited document.
type documentEditArgs struct {
	File          string   `json:"file"`
	Edit          string   `json:"edit"`
	Node          []string `json:"node"`
	Socket        string   `json:"socket"`
	Name          string   `json:"name"`
	Value         any      `json:"value"`
	DryRun        bool     `json:"dry_run"`
	WorkspaceRoot string   `json:"workspace_root"`
}

// documentEditPayload is the edit tool's structured payload: the workspace the
// edit was applied in, what was asked for, where the entry landed, and what
// reached disk.
//
// It is projected from the shared layer's own result rather than embedded, and
// the keys are this protocol's own. The two locating tools beside it embed the
// result type a session answers with, so their keys are that surface's; there is
// no such shared type for an edit, and inventing one here to be embedded would
// put a second shape between this tool and the layer it drives. What must not be
// restated is the layer's result, and nothing here does: every field below is
// read off it.
//
// Content carries the edited document under a reporting delivery and is absent
// otherwise. Under a write the document is on disk and Written names it, so
// returning its whole text as well would spend a caller's budget on bytes it
// asked to have written rather than handed back; under a dry run the content is
// the entire answer, and withholding it would leave the delivery answering
// nothing.
//
// WorkspaceRoot is absent, and marked by WorkspaceRootWithheld, where the caller is
// not told where the workspace sits on this host. File is the document as the
// caller itself named it, and Written is in the workspace-relative coordinate a
// caller names a document by.
type documentEditPayload struct {
	WorkspaceRoot         *string `json:"workspace_root"`
	WorkspaceRootWithheld bool    `json:"workspace_root_withheld,omitempty"`

	File              string   `json:"file"`
	Edit              string   `json:"edit"`
	Container         []string `json:"container"`
	CreatedContainers []string `json:"created_containers"`
	Written           []string `json:"written"`
	Content           string   `json:"content,omitempty"`
}

var editDocumentToolSpec = toolSpec{
	summary: "Applies one bounded structured edit to an authored document, preserving every byte it does not touch.",
	description: "Apply one bounded structured edit to an authored document and write the document back, " +
		"preserving every byte the edit does not touch. One call adds one entry under one container, or sets " +
		"one scalar, and it can do nothing else: `edit` names which of a closed set to apply, and a name " +
		"outside that set is refused rather than interpreted. THERE IS NO EDIT THAT DELETES, replaces a block " +
		"an author already wrote, reorders anything, or writes bytes you supply verbatim — so this is not a " +
		"file writer and cannot be used as one. Pass `file`, the document; `node`, the structural path from " +
		"the document root as a list of segments, which is the same shape insertion-point takes and " +
		"attachment-container answers in; `name`, the key the entry is written under; and `value`, what that " +
		"entry carries, whose meaning follows from `edit` — the entity the entry requests, the node a " +
		"connection points at, or the scalar a value is set to. `value` is a JSON value, so a number stays a " +
		"number and a quoted digit stays text, and leaving it out sets nothing at all. `socket` is read where " +
		"the edit wires a connection and ignored otherwise. THE NODE PATH IS YOURS TO SUPPLY: nothing here " +
		"searches for a name, because a name appearing at two levels would leave this deciding which one you " +
		"meant. The result has to be a document you could have typed, and that is what the write is gated on " +
		"— the composed document is checked against its own kind's schema and reaches disk only if that " +
		"passes, and a refused request leaves the file byte-for-byte as it was. An entry already there " +
		"carrying what you asked for writes nothing; one carrying something else is refused rather than " +
		"replaced. Send `dry_run` to be handed the edited document as `content` instead, with nothing " +
		"created: every other behaviour is the write's, the schema gate included, so a request the write " +
		"would refuse is refused under it too. Whether what you wire is semantically admissible — whether a " +
		"socket is one the producer declares, whether a Feature's applicability is satisfied — is what " +
		"validate answers, not this.",
	inputSchema: `{
  "type": "object",
  "properties": {
    "edit": {
      "type": "string",
      "enum": ["add-component", "attach-feature", "wire-edge", "set-value"],
      "description": "Which bounded edit to apply. add-component adds a component instance under a components container; attach-feature attaches a Feature to the node you name; wire-edge wires a connection inside one of that node's sockets; set-value sets one value on that node."
    },
    "file": {
      "type": "string",
      "minLength": 1,
      "description": "The document to edit, relative to the workspace root or absolute. It must lie inside the workspace."
    },
    "node": {
      "type": "array",
      "minItems": 1,
      "items": {"type": "string", "minLength": 1},
      "description": "The structural path to the node the edit is about, from the document root, one element per segment. For add-component it names the components container itself, because the entry being added is the node; for the other three it names the node the edit is about and the container follows from the edit."
    },
    "name": {
      "type": "string",
      "minLength": 1,
      "description": "The key the new entry is written under: the component instance name, the attachment's name, the connection's alias, or the value's key."
    },
    "value": {
      "anyOf": [{"type": "string"}, {"type": "number"}, {"type": "boolean"}, {"type": "null"}],
      "description": "What the entry carries, as a JSON value of its own type: a string, a number, a boolean or nothing. Its meaning follows from edit: the entity the entry requests, the node an edge points at, or the scalar a value is set to. A mapping or a sequence is refused, because writing a block is not one of these edits. Only set-value accepts a value that is not a non-empty string, and only there does omitting it carry nothing at all."
    },
    "socket": {
      "type": "string",
      "description": "The dependency socket a connection is wired inside. Required for wire-edge and ignored otherwise."
    },
    "dry_run": {
      "type": "boolean",
      "description": "Do everything except write, and hand back the edited document as content. The schema gate the write is conditioned on runs identically, so a request the write would refuse is refused under it too."
    },
    "workspace_root": {
      "type": "string",
      "description": "Optional workspace root override. ` + workspaceRootArgumentRule + `"
    }
  },
  "required": ["edit", "file", "node", "name"],
  "allOf": [
    {
      "if": {"properties": {"edit": {"const": "wire-edge"}}, "required": ["edit"]},
      "then": {"required": ["socket"], "properties": {"socket": {"minLength": 1}}}
    },
    {
      "if": {"properties": {"edit": {"enum": ["add-component", "attach-feature", "wire-edge"]}}, "required": ["edit"]},
      "then": {"required": ["value"], "properties": {"value": {"type": "string", "minLength": 1}}}
    }
  ]
}`,
	invoke: editDocumentInvoke,
}

func editDocumentInvoke(s *server, ctx context.Context, args json.RawMessage) (*callToolResult, *rpcError) {
	var a documentEditArgs
	if len(args) > 0 {
		if err := json.Unmarshal(args, &a); err != nil {
			return invalidToolArguments(editDocumentToolName, err)
		}
	}
	if strings.TrimSpace(a.File) == "" {
		return errorResult(editDocumentToolName + " requires a 'file': the document to edit, relative to the " +
			"workspace root or absolute. Call list-entities with response_format detailed to see the documents " +
			"this workspace declares.")
	}
	if strings.TrimSpace(a.Edit) == "" {
		return errorResult(editDocumentToolName + " requires an 'edit': which of the bounded edits to apply, " +
			"one of " + editDocumentKindList() + ". The set is closed, and a name outside it is refused rather " +
			"than interpreted.")
	}
	if err := ctx.Err(); err != nil {
		return errorResult(explainToolError("Could not apply the edit.", err, ""))
	}

	tw, err := s.resolveToolWorkspace(a.WorkspaceRoot, nil)
	if err != nil {
		return errorResult(explainToolError("Could not resolve the workspace to apply the edit in.", err, ""))
	}

	// The document is resolved here, through the one helper, before the layer
	// that opens it is reached. That layer confines the file to the root as well
	// — it must, since it is reached from two other surfaces — but a caller
	// refused there is answered in that layer's vocabulary rather than in this
	// surface's, and over a listener the sentence a caller needs is the one
	// naming the workspace this server was started over.
	file, refusal := tw.workspacePath(editDocumentToolName, "file", a.File)
	if refusal != "" {
		return errorResult(refusal)
	}
	if tw, err = s.subjectWorkspace(tw, a.File); err != nil {
		return errorResult(explainToolError("Could not resolve the workspace to apply the edit in.", err, ""))
	}

	delivery := authoredcontent.DeliveryFor(a.DryRun)
	res, err := editops.Apply(ctx, tw.root, editops.Options{
		File:     file,
		Kind:     editops.Kind(a.Edit),
		Node:     a.Node,
		Socket:   a.Socket,
		Name:     a.Name,
		Value:    a.Value,
		Delivery: delivery,
	})
	if err != nil {
		return errorResult(explainDiagnostic(editDocumentRefusal(a, err), ""))
	}

	payload := documentEditPayload{
		File:              a.File,
		Edit:              a.Edit,
		Container:         editDocumentList(res.Container),
		CreatedContainers: editDocumentList(res.CreatedContainers),
		Written:           editDocumentList(res.Written),
	}
	payload.WorkspaceRoot, payload.WorkspaceRootWithheld = tw.reportedLocationField(tw.root)
	if delivery.Reports() && len(res.Documents) != 0 {
		payload.Content = res.Documents[0].Content
	}
	return tw.answer(textResult(renderDocumentEditText(payload, delivery), payload))
}

// editDocumentRefusal composes this surface's sentence for an edit the shared
// layer would not apply, opening with the short code where the failure names a
// registered condition.
//
// The code is read off the error rather than written here, so this surface
// cannot come to report a code the condition was not registered under, and the
// guidance route this feeds then resolves that code into the descriptive one a
// caller matches on. Where the chain names none the sentence stands on its own:
// every refusal that carries no code says one request's arguments do not address
// a place an entry can go, which needs no identifier for a caller to act on it.
//
// The sentence is this surface's own while the BOUND it reports is the layer's,
// which is the division the layer's own refusals are written for: each surface
// says the same thing in its own voice, and none of them restates what may be
// asked for.
func editDocumentRefusal(a documentEditArgs, err error) string {
	var b strings.Builder
	b.WriteString(editDocumentToolName)
	b.WriteString(" applied no edit to ")
	b.WriteString(a.File)
	b.WriteString(", and the document is byte-for-byte as it was. ")
	if short, ok := cerrors.ShortCodeOf(err); ok {
		b.WriteString(short)
		b.WriteString(": ")
	}
	b.WriteString(err.Error())
	return b.String()
}

// editDocumentKindList renders the admissible edits for the refusal a caller
// that named none reads, so the sentence stating the bound names the set the
// shared layer applies.
//
// The names come from that layer's own enumeration rather than from a literal
// here: a restatement would be a second statement of the bound, and the two
// would agree until an edit was added to one of them. The sentence around them
// is this surface's, which is why the joining is done here rather than borrowed.
func editDocumentKindList() string {
	kinds := editops.Kinds()
	names := make([]string, 0, len(kinds))
	for _, k := range kinds {
		names = append(names, string(k))
	}
	if len(names) < 2 {
		return strings.Join(names, "")
	}
	return strings.Join(names[:len(names)-1], ", ") + " or " + names[len(names)-1]
}

// editDocumentList returns a non-nil slice, so a field a caller reads as a list
// arrives empty rather than absent.
//
// It is named for this answer rather than generically, and deliberately: every
// other tool here keeps such a normalisation local to the answer it serves, so a
// generically-named one would read as a package utility the other sites had
// failed to use.
func editDocumentList(in []string) []string {
	if in == nil {
		return []string{}
	}
	return in
}

// renderDocumentEditText renders the human-readable answer: what happened to the
// document, where the entry sits, and which container levels this edit had to
// bring into being.
//
// The three outcomes are reported differently on purpose. A write names the file
// that reached disk. An edit whose entry was already there, carrying what was
// asked, wrote nothing — and saying nothing at all would leave that
// indistinguishable from a write that failed quietly. A reporting delivery wrote
// nothing by design, and says so rather than reading as the second case.
//
// The created containers are stated in the text and not left to the structured
// field alone, because the text block is what an assistant reads when it does
// not parse the result, and an edit that quietly brought two nested blocks into
// being is otherwise indistinguishable from one that added a single entry to a
// block the author wrote.
func renderDocumentEditText(p documentEditPayload, delivery authoredcontent.Delivery) string {
	var b strings.Builder
	switch {
	case delivery.Reports():
		b.WriteString("Nothing was written: the edited " + p.File + " is in this result's content.\n")
	case len(p.Written) == 0:
		b.WriteString(p.File + " already carries that entry; nothing was written\n")
	default:
		b.WriteString("wrote " + strings.Join(p.Written, ", ") + "\n")
	}
	if len(p.Container) != 0 {
		b.WriteString("  the entry sits under " + renderContainerPath(p.Container) + "\n")
	}
	if len(p.CreatedContainers) != 0 {
		b.WriteString("  this edit created " + renderContainerPath(p.CreatedContainers) +
			", which the document did not carry\n")
	}
	return strings.TrimRight(b.String(), "\n")
}
