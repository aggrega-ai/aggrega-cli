// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package serve

import (
	"context"
	"errors"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/internal/scaffoldops"
)

// scaffoldEntityParams is the request shape for `aggrega/scaffoldEntity`.
//
// Kind is the lowercase-hyphenated schema token naming the entity kind to draft,
// and it is required: the answer is derived from that kind's schema, so there is
// nothing to fall back on and nothing sensible to guess.
//
// Name is optional and seeds metadata.name. A client that already knows what the
// document will be called sends it and hands the author one placeholder fewer;
// a client that does not omits it and the field arrives as a placeholder like
// every other required value.
type scaffoldEntityParams struct {
	Kind string `json:"kind"`
	Name string `json:"name"`
}

// scaffoldEntityResult is the aggrega/scaffoldEntity response: the kind the
// skeleton was derived for, and the skeleton itself as YAML text.
//
// Kind is echoed rather than left for the caller to remember, so a client that
// has several drafting requests outstanding attributes an answer from the answer
// itself instead of keeping a correlation of its own.
//
// Skeleton is a document to write, not a document that was written. Nothing is
// created, no path is chosen and no directory is touched: where the text goes,
// and whether it goes anywhere at all, is the client's decision.
type scaffoldEntityResult struct {
	Kind     string `json:"kind"`
	Skeleton string `json:"skeleton"`
}

// dispatchScaffoldEntity answers with a starter document for the entity kind the
// request names: exactly the fields that kind's schema requires, with the fixed
// identifying fields filled in and a placeholder everywhere a value is needed.
//
// The skeleton comes from the shared drafting layer, which is the same
// computation every other caller-facing surface answers this capability with, so
// an editor and an assistant asking for the same kind and the same name receive
// the same bytes. A second derivation here would agree on the day it was written
// and part company later, with nothing to notice that it had.
//
// It writes nothing, and that is the property to preserve rather than an
// incidental one. The answer is text the client puts where it wants it — into an
// unsaved buffer the author reads, edits or discards — so the moment a new
// document reaches disk stays where the buffer and the undo history are.
//
// It reaches no registry and no network, and it needs nothing of the workspace:
// the schema it derives from is compiled into the binary. So it answers in a
// workspace nobody has prepared yet, where every other method here would first
// want a document or a lockfile.
//
// A kind the schema set does not answer to is refused with the corrective text
// naming the whole valid set, as an invalid-params envelope: what needs
// correcting is the request rather than the workspace, so it belongs in the band
// a client repairs and not the band it asks a person to repair. A schema the
// walk cannot read is the other kind of failure entirely — nothing the caller
// sent is wrong — and travels as an internal error.
func (s *Server) dispatchScaffoldEntity(ctx context.Context, req *jsonRPCRequest) error {
	var params scaffoldEntityParams
	if len(req.Params) > 0 {
		if err := decodeParams(req, &params); err != nil {
			return s.writeRefusal(ctx, req.ID, jsonRPCInvalidParams, "invalid params: ", err)
		}
	}
	kind := strings.TrimSpace(params.Kind)
	if kind == "" {
		return s.writeError(ctx, req.ID, jsonRPCInvalidParams,
			"invalid params: kind must name the entity kind to draft, as a lowercase-hyphenated schema "+
				"token such as component, aggregate, release or recipe; the request named none")
	}
	skeleton, err := scaffoldops.Skeleton(kind, params.Name)
	if err != nil {
		var unknown *scaffoldops.UnknownKindError
		if errors.As(err, &unknown) {
			return s.writeRefusal(ctx, req.ID, jsonRPCInvalidParams, "", unknown)
		}
		return s.writeRefusal(ctx, req.ID, jsonRPCInternalError, "derive the skeleton: ", err)
	}
	return s.writeResult(ctx, req.ID, scaffoldEntityResult{Kind: kind, Skeleton: skeleton})
}
