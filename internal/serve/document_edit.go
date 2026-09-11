// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package serve

import (
	"context"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/internal/authoredcontent"
	"github.com/aggrega-ai/aggrega-cli/internal/editops"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
)

// documentEditParams is what aggrega/editDocument takes.
//
// file names the document, relative to the workspace this session opened, as
// every other per-document request here is named.
//
// edit names which of the bounded edits to apply. It is the request's whole
// statement of what is being done: the set is closed, and a value outside it is
// refused rather than interpreted.
//
// node is the structural path to the node the edit is about, from the document
// root, spelled as its segments — the same breadcrumb vocabulary the
// position-reporting requests take and answer in, so a caller that asked where
// an entry belongs and a caller that applies one speak about the document in one
// language. It is a path rather than a name because a name would have to be
// searched for, and a name appearing at two levels would leave the compiler
// deciding which of them the author meant.
//
// socket is the dependency socket a connection is wired inside. It is read for
// the wiring edit and ignored otherwise.
//
// name is the key the new entry is written under, and value is what that entry
// carries. What value means follows from edit: the entity the entry requests,
// the node a connection points at, or the scalar a value is set to.
//
// dryRun does everything the plain request does except write: it answers with the
// edited document as content instead of putting it on disk. Which of the two a
// caller wants is a property of the caller rather than of this surface — a client
// holding its own buffer puts the result in front of the author before anything
// reaches disk — so it is a request field, and it defaults to writing because the
// operation's product is an edited document and a surface that produces one owes
// it. It changes nothing else: the schema gate the write is conditioned on runs
// identically, so an edit the write would refuse is refused here too.
type documentEditParams struct {
	File   string   `json:"file"`
	Edit   string   `json:"edit"`
	Node   []string `json:"node"`
	Socket string   `json:"socket"`
	Name   string   `json:"name"`
	Value  any      `json:"value"`
	DryRun bool     `json:"dryRun"`
}

// documentEditResult is the aggrega/editDocument response.
//
// file and edit echo what the request named, so a client with several requests
// outstanding pairs each answer with its question without keeping an order of
// its own.
//
// content carries the document the edit produced, whether or not it was written,
// and written names the file that reached disk — empty under a reporting
// delivery. The two answer different questions: what the edit came to, and what
// is now on the page. A caller that asked to be reported to is asking only the
// first.
//
// container is the structural path of the container the entry landed in, so a
// caller can locate what it just added without re-deriving that rule for
// itself, and createdContainers names the segments this edit wrote because the
// document did not carry them, outermost first. It is empty in the ordinary case
// where the whole path was already there, and reading it is how a client tells an
// entry added inside an existing block from one that brought its own block into
// being.
//
// written and createdContainers are always arrays, empty rather than absent, so
// a client reads an edit that created nothing as an empty answer rather than as
// a missing field.
type documentEditResult struct {
	File              string   `json:"file"`
	Edit              string   `json:"edit"`
	Container         []string `json:"container"`
	CreatedContainers []string `json:"createdContainers"`
	Written           []string `json:"written"`
	Content           string   `json:"content"`
}

// dispatchDocumentEdit applies one bounded structured edit to a document the
// workspace carries.
//
// 🔴 It WRITES, and what it writes is a document an author owns and goes on
// editing. It is admitted on the rule every writing method here runs under: a
// caller that sent this has asked for the edit, exactly as a caller sending a
// build has asked for artifacts. What stays out of bounds is a write performed
// while validating or inspecting, where nobody asked, and this is not that.
//
// # The bound is the edit set, and it is not restated here
//
// One call adds one entry under one container, or sets one scalar. It cannot
// delete, cannot replace a block an author wrote, cannot reorder, and cannot
// write bytes a caller supplies verbatim — because the shared layer this
// delegates to enumerates the admissible edits and has no path for a shape
// absent from that set. This method names the edit and passes the request
// through; it does not re-decide what may be applied.
//
// # What keeps the result inside what an author could type
//
// The composed document is validated against its own kind's schema and reaches
// disk only if that passes, so a surface cannot use this to write something the
// authoring schema refuses. A refused edit leaves the file byte-for-byte as it
// was and comes back naming the condition, so a refusal is indistinguishable
// from an edit never attempted.
//
// # The document is re-read after a write
//
// The session's per-document parse and finding caches for the edited file are
// dropped and the workspace-level catalog view is reset, so the next validation,
// hover or graph request is answered from the bytes this edit produced rather
// than from what the session was holding. Without that a client would apply an
// edit and be told about the document as it stood before it.
//
// It resolves no dependency, reaches no registry and touches no network under
// either delivery.
func (s *Server) dispatchDocumentEdit(ctx context.Context, req *jsonRPCRequest) error {
	var params documentEditParams
	if len(req.Params) > 0 {
		if err := decodeParams(req, &params); err != nil {
			return s.writeError(ctx, req.ID, jsonRPCInvalidParams, "invalid params: "+err.Error())
		}
	}
	if err := ctx.Err(); err != nil {
		return s.writeError(ctx, req.ID, jsonRPCInternalError, err.Error())
	}
	if params.File == "" {
		return s.writeError(ctx, req.ID, jsonRPCInvalidParams,
			"editDocument: file is required and must name the document to edit, "+
				"relative to the root of the workspace this session opened")
	}
	if params.Edit == "" {
		return s.writeError(ctx, req.ID, jsonRPCInvalidParams,
			"editDocument: edit is required and must name which edit to apply, one of "+
				editKindList())
	}
	if s.workspaceRoot == "" {
		return s.writeError(ctx, req.ID, jsonRPCInternalError,
			"the daemon holds no workspace root to resolve the document against")
	}

	delivery := authoredcontent.DeliveryFor(params.DryRun)
	res, err := editops.Apply(ctx, s.workspaceRoot, editops.Options{
		File:     params.File,
		Kind:     editops.Kind(params.Edit),
		Node:     params.Node,
		Socket:   params.Socket,
		Name:     params.Name,
		Value:    params.Value,
		Delivery: delivery,
	})
	if ctx.Err() != nil {
		return nil
	}
	if err != nil {
		return s.writeError(ctx, req.ID, jsonRPCInvalidParams, documentEditRefusal(err))
	}

	// A write makes what the session is holding for this document stale. The
	// per-file drop is what a subsequent validation or hover reads, and the
	// workspace-level reset is what a subsequent graph request reads, so both
	// are needed: a client that applied an edit and then asked about the
	// document would otherwise be answered about the version it replaced.
	if len(res.Written) != 0 {
		s.handleInvalidate(invalidateParams{File: params.File})
	}

	return s.writeResult(ctx, req.ID, documentEditResult{
		File:              params.File,
		Edit:              params.Edit,
		Container:         documentEditList(res.Container),
		CreatedContainers: documentEditList(res.CreatedContainers),
		Written:           documentEditList(res.Written),
		Content:           documentEditContent(res),
	})
}

// documentEditRefusal renders a refusal for the wire, opening with the short
// code where the failure names a registered condition.
//
// The code is read off the error rather than written here, so this surface
// cannot come to report a code the condition was not registered under. Where the
// chain names none the sentence stands on its own: every refusal that carries no
// code says one request's arguments do not address a place an entry can go,
// which needs no identifier for a caller to act on it. An error envelope's own
// code is the numeric one, which names a state of the workspace rather than a
// condition of the toolchain, so a condition a reader can quote and look up
// travels in the sentence — the same place a printed diagnostic carries it.
func documentEditRefusal(err error) string {
	if short, ok := cerrors.ShortCodeOf(err); ok {
		return "editDocument: " + short + ": " + err.Error()
	}
	return "editDocument: " + err.Error()
}

// documentEditContent returns the edited document's content.
//
// The shared layer answers with a document set because a capability may produce
// several; one edit produces one document, so the content is projected onto a
// single field rather than a list a caller would index into. An empty answer
// cannot arise from a successful edit, and the guard is here so a change to that
// shape reports nothing rather than panicking.
func documentEditContent(res editops.Result) string {
	if len(res.Documents) == 0 {
		return ""
	}
	return res.Documents[0].Content
}

// documentEditList returns a non-nil slice, so a field a client reads as a list
// arrives empty rather than absent.
//
// It is named for this answer rather than generically, and deliberately. Every
// other response here keeps such a normalisation local to the answer it serves —
// inline at the site, or in a helper named after that answer — so a
// generically-named one here would read as a package utility the other sites had
// failed to use, and would invite a caller from an unrelated answer. Three
// fields on one answer earn a helper; the name keeps it this answer's.
func documentEditList(in []string) []string {
	if in == nil {
		return []string{}
	}
	return in
}

// editKindList renders the admissible edits for a refusal, so a caller that
// named none is told what it may name.
//
// The list comes from the shared layer's own enumeration rather than from a
// literal here, so a surface cannot advertise a set the layer does not apply.
func editKindList() string {
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
