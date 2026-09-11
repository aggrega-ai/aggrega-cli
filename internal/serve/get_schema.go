// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package serve

import (
	"context"
	"encoding/json"
	"strings"

	schemas "github.com/aggrega-ai/aggrega-schemas"

	"github.com/aggrega-ai/aggrega-cli/internal/scaffoldops"
	"github.com/aggrega-ai/aggrega-cli/internal/serve/standalonekind"
	"github.com/aggrega-ai/aggrega-cli/pkg/entitykind"
)

// getSchemaParams is the request shape for `aggrega/getSchema`.
//
// Kind is the lowercase-hyphenated schema token naming the kind to report, and it
// is optional: naming one asks for that kind's schema, and omitting it asks which
// kinds can be named — every token this binary answers to, and the narrower set
// an author may write a document of. One parameter carries both questions because
// the second is how a caller learns what to send for the first, and a client that
// had to know the vocabulary before it could ask for it would have nowhere to
// learn it from.
type getSchemaParams struct {
	Kind string `json:"kind"`
}

// entityKindDescriptor names one kind an author may write, in both of the
// registers a client needs it in.
//
// Kind is the lowercase-hyphenated schema token — the value to send back as this
// method's own kind parameter, or as the drafting method's, to ask about this
// kind. It is the same spelling the enumeration of every schema token uses, so
// one word means one thing throughout this answer. DocumentKind is the value the
// document itself carries in its top-level `kind:` field.
//
// Both travel because neither register is derivable from the other by a client.
// A kind name of more than one word does not survive the round trip: nothing a
// client could do to featurerecipe arrives at FeatureRecipe. And the direction
// that does work today — lowercasing the document value — is a derivation this
// binary owns rather than one a client should perform, since a client performing
// it would be the one surface left to correct if a kind's token ever stopped
// being its name lowercased. Carrying both leaves the client no mapping to keep,
// which is the whole reason this answer exists.
type entityKindDescriptor struct {
	Kind         string `json:"kind"`
	DocumentKind string `json:"documentKind"`
}

// getSchemaResult is the aggrega/getSchema response, and it answers one of the
// two questions the request asked rather than both.
//
// Kind and Schema carry the answer to a named kind: the token as it was resolved,
// and that kind's schema as a JSON document. Kinds and EntityKinds carry the
// answer to a request that named none, and they answer it at two widths: Kinds is
// every token this binary can be asked for, in the enumeration's own order, and
// EntityKinds is the narrower set an author may write a document of.
//
// Both widths travel on that answer because a client needs a different one for a
// different job, and picking the wrong one is not a cosmetic mistake. A client
// wiring an editor's schema association wants every token, contract and generated
// documents included. A client offering a reader something to draft wants the
// narrow set: the wide one holds the intermediate representation, the lockfile,
// the workspace configuration and the plugin socket contracts, none of which is a
// document an author writes, and a chooser built from it would offer values no
// authoring schema accepts.
//
// The narrow set is narrower than the entity kinds as well, and by a
// distinction a client cannot make for itself: a kind whose content is written
// inside another kind's document fixes its own apiVersion and kind so the content
// can be validated where it sits, which makes it identifiable and not
// addressable. Its schema is as real as any other, so nothing in the bytes
// separates it, and a client narrowing the wide set by pattern would offer a
// document there is nowhere to put.
//
// The two HALVES — a named kind's schema, and the enumeration — are never both
// filled, and which one arrived is readable from the response itself, so nothing
// here restates which question was asked.
//
// Schema is the schema as a document rather than as text, so a client reads it
// without decoding a string that happens to hold JSON. It is the same document
// the command line prints and the same one the tool protocol returns; all three
// read the one copy compiled into this binary.
type getSchemaResult struct {
	Kind        string                 `json:"kind,omitempty"`
	Schema      json.RawMessage        `json:"schema,omitempty"`
	Kinds       []string               `json:"kinds,omitempty"`
	EntityKinds []entityKindDescriptor `json:"entityKinds,omitempty"`
}

// authorableEntityKinds enumerates the kinds an author writes a document of, in
// the entity vocabulary's own declared order, each in both registers.
//
// Membership follows the declared vocabulary, so what a client is offered follows
// this binary rather than a list kept anywhere beside it. That is the property
// worth preserving: a list written out on either side of the wire agrees on the
// day it is written and disagrees silently the day a kind is declared, and the
// reader who then cannot find their kind has no way to tell a missing kind from
// one that does not exist.
//
// It is the vocabulary narrowed to the kinds written as a document of their own,
// which is a narrower set than the kinds that exist: a kind whose content lives
// inside another kind's document is one an author never writes a file of, so
// offering it here would offer a document there is nowhere to put.
func authorableEntityKinds() []entityKindDescriptor {
	declared := standalonekind.Kinds()
	out := make([]entityKindDescriptor, 0, len(declared))
	for _, k := range declared {
		out = append(out, entityKindDescriptor{
			Kind:         entitykind.Segment(k),
			DocumentKind: string(k),
		})
	}
	return out
}

// dispatchGetSchema answers with the JSON Schema for the entity or contract kind
// the request names, or — when it names none — with every kind that can be named
// and, alongside it, the narrower set an author may write a document of.
//
// Both widths are answered together because a client cannot narrow the wide one
// for itself: which tokens name a document somebody writes, and which name a
// contract, a generated document or content embedded inside another one, is a
// fact about this binary rather than about the tokens, and a client deciding it
// by pattern or by a list of its own would be holding a second statement of the
// vocabulary — one that agrees until the day a kind is declared, at which point
// the reader is simply not offered their kind.
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
		if err := decodeParams(req, &params); err != nil {
			return s.writeError(ctx, req.ID, jsonRPCInvalidParams, "invalid params: "+err.Error())
		}
	}
	kind := strings.TrimSpace(params.Kind)
	if kind == "" {
		return s.writeResult(ctx, req.ID, getSchemaResult{
			Kinds:       schemas.Kinds(),
			EntityKinds: authorableEntityKinds(),
		})
	}
	schema, ok := schemas.SchemaByKind(kind)
	if !ok {
		return s.writeError(ctx, req.ID, jsonRPCInvalidParams, scaffoldops.UnknownKindGuidance(kind))
	}
	return s.writeResult(ctx, req.ID, getSchemaResult{Kind: kind, Schema: json.RawMessage(schema)})
}
