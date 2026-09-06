// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package serve

import (
	"context"
	"encoding/json"
	"strings"

	schemas "github.com/aggrega-ai/aggrega-schemas"

	"github.com/aggrega-ai/aggrega-cli/internal/scaffoldops"
)

// getSchemaParams is the request shape for `aggrega/getSchema`.
//
// Kind is the lowercase-hyphenated schema token naming the kind to report, and it
// is optional: naming one asks for that kind's schema, and omitting it asks for
// the set of kinds that can be named. One parameter carries both questions
// because the second is how a caller learns what to send for the first, and a
// client that had to know the vocabulary before it could ask for it would have
// nowhere to learn it from.
type getSchemaParams struct {
	Kind string `json:"kind"`
}

// getSchemaResult is the aggrega/getSchema response, and it answers one of the
// two questions the request asked rather than both.
//
// Kind and Schema carry the answer to a named kind: the token as it was resolved,
// and that kind's schema as a JSON document. Kinds carries the answer to a
// request that named none: every token this binary can be asked for, in the
// enumeration's own order.
//
// The two halves are never both filled, and which one arrived is readable from
// the response itself — a schema and a list of kind tokens cannot be mistaken for
// each other — so nothing here restates which question was asked.
//
// Schema is the schema as a document rather than as text, so a client reads it
// without decoding a string that happens to hold JSON. It is the same document
// the command line prints and the same one the tool protocol returns; all three
// read the one copy compiled into this binary.
type getSchemaResult struct {
	Kind   string          `json:"kind,omitempty"`
	Schema json.RawMessage `json:"schema,omitempty"`
	Kinds  []string        `json:"kinds,omitempty"`
}

// dispatchGetSchema answers with the JSON Schema for the entity or contract kind
// the request names, or — when it names none — with every kind that can be named.
//
// Its subject is the schema set compiled into this binary, which is the same
// subject the drafting method answers from: a skeleton is derived from these very
// bytes. So the session contributes nothing to producing either answer, and both
// are carried on the same ground — the reason is about who is on the other end
// rather than about where the subject lives. A client wiring an editor's schema
// association, or checking a document against the contract the running binary
// actually holds, is asking the one process that can answer for THIS binary
// rather than for whichever copy is installed elsewhere.
//
// It reaches no registry and no network, writes nothing, and needs nothing of the
// workspace, so it answers in a workspace nobody has prepared yet — where every
// method anchored to a document would first want one.
//
// A kind the schema set does not answer to is refused with the corrective text
// naming the whole valid set, as an invalid-params envelope: what needs
// correcting is the request rather than the workspace, so it belongs in the band
// a client repairs and not the band it asks a person to repair. That text comes
// from the shared drafting layer, which is where every surface resolving a kind
// token phrases the rejection, so two of them cannot phrase it differently.
func (s *Server) dispatchGetSchema(ctx context.Context, req *jsonRPCRequest) error {
	var params getSchemaParams
	if len(req.Params) > 0 {
		if err := json.Unmarshal(req.Params, &params); err != nil {
			return s.writeError(ctx, req.ID, jsonRPCInvalidParams, "invalid params: "+err.Error())
		}
	}
	kind := strings.TrimSpace(params.Kind)
	if kind == "" {
		return s.writeResult(ctx, req.ID, getSchemaResult{Kinds: schemas.Kinds()})
	}
	schema, ok := schemas.SchemaByKind(kind)
	if !ok {
		return s.writeError(ctx, req.ID, jsonRPCInvalidParams, scaffoldops.UnknownKindGuidance(kind))
	}
	return s.writeResult(ctx, req.ID, getSchemaResult{Kind: kind, Schema: json.RawMessage(schema)})
}
