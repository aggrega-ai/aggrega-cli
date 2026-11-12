// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package serve

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/internal/exampleops"
	"github.com/aggrega-ai/aggrega-cli/internal/scaffoldops"
)

// scaffoldEntityParams is the request shape for `aggrega/scaffoldEntity`, which
// carries the scaffold capability's two forms: drafting a starter document for an
// entity kind, and writing — or listing — the documentation's runnable examples.
//
// Kind names the entity kind to draft, as its lowercase-hyphenated schema token.
// Name is optional and seeds metadata.name. A client that already knows what the
// document will be called sends it and hands the author one placeholder fewer;
// a client that does not omits it and the field arrives as a placeholder like
// every other required value.
//
// Example names a runnable example to write instead, and List asks for the list
// of examples; Dir names the new directory the example is written into, relative
// to the workspace root, and defaults to the example's name. Which fields go
// together is the shared example layer's rule (exampleops.RequestForm), so the
// assistant tool refuses a mixed request in the same words.
//
// WorkspaceRoot is declared here rather than read at the routing boundary. The
// method's route needs no workspace, because drafting reads none; the example
// form does, and reads the root the request names itself, through the one
// resolution every request naming a workspace is answered against.
type scaffoldEntityParams struct {
	WorkspaceRoot string `json:"workspaceRoot"`
	Kind          string `json:"kind"`
	Name          string `json:"name"`
	Example       string `json:"example"`
	List          bool   `json:"list"`
	Dir           string `json:"dir"`
}

// scaffoldExample is one runnable example in the list aggrega/scaffoldEntity
// answers with: its name, its level and the first sentence of its page.
type scaffoldExample struct {
	Name    string `json:"name"`
	Level   string `json:"level"`
	Summary string `json:"summary"`
}

// scaffoldEntityResult is the aggrega/scaffoldEntity response, one shape for the
// three forms, each answering with its own fields and leaving the others absent.
//
// For a draft: the kind the skeleton was derived for, and the skeleton itself as
// YAML text. Kind is echoed rather than left for the caller to remember, so a
// client that has several drafting requests outstanding attributes an answer
// from the answer itself. Skeleton is a document to write, not a document that
// was written: where the text goes is the client's decision.
//
// For a list: Examples, each example once, in the order the documentation lists them.
//
// For a write: the example written, the directory it went into as the request
// named it, the files written relative to that directory, the commands the
// example's page gives for building it, and the version of the documentation
// the example was read from.
type scaffoldEntityResult struct {
	Kind     string            `json:"kind,omitempty"`
	Skeleton string            `json:"skeleton,omitempty"`
	Examples []scaffoldExample `json:"examples,omitempty"`
	Example  string            `json:"example,omitempty"`
	Dir      string            `json:"dir,omitempty"`
	Files    []string          `json:"files,omitempty"`
	Next     []string          `json:"next,omitempty"`
	Version  string            `json:"version,omitempty"`
}

// dispatchScaffoldEntity answers with a starter document for the entity kind the
// request names, or — for a request naming an example or asking for the list —
// writes one of the documentation's runnable examples into a new directory of the
// workspace, or lists them.
//
// A request mixing the fields of two forms is refused before anything is read,
// in the shared example layer's words, as an invalid-params envelope.
func (s *Server) dispatchScaffoldEntity(ctx context.Context, req *jsonRPCRequest) error {
	var params scaffoldEntityParams
	if len(req.Params) > 0 {
		if err := decodeParams(req, &params); err != nil {
			return s.writeRefusal(ctx, req.ID, jsonRPCInvalidParams, "invalid params: ", err)
		}
	}
	form, err := exampleops.RequestForm(params.Kind, params.Name, params.Example, params.Dir, params.List)
	if err != nil {
		return s.writeRefusal(ctx, req.ID, jsonRPCInvalidParams, "", err)
	}
	if form != exampleops.FormDraft {
		return s.answerExample(ctx, req, form, params)
	}
	return s.answerDraft(ctx, req, params)
}

// answerDraft answers the drafting form: exactly the fields the kind's schema
// requires, with the fixed identifying fields filled in and a placeholder
// everywhere a value is needed.
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
func (s *Server) answerDraft(ctx context.Context, req *jsonRPCRequest, params scaffoldEntityParams) error {
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

// answerExample answers the example form: the list of the documentation's
// runnable examples, or one of them written into a new directory of the
// workspace the request names.
//
// 🔴 A write WRITES, and it is admitted on the rule every writing method here runs
// under: a caller that sent it has asked for the example. It writes only into a
// directory beneath the workspace root that does not exist yet or is empty. The
// directory is judged before anything is read by the one judgement every path a
// request writes is held to (workspacePath) — an absolute value, one climbing out
// of the root, and one leading out of it through a link are refused — and the
// write itself is the shared example layer's, which refuses a directory holding
// anything and a bundle carrying a link or a member that would land outside it,
// with nothing written. Those are the refusals the command line gives.
//
// The examples are read from the documentation bundle the binary's own version
// names, through the acquisition every documentation surface shares: a copy the
// machine-wide cache holds that the workspace's signature check admits is read
// without reaching a registry, and otherwise the bundle is fetched through the
// workspace's network settings and verified against its signature policy before
// it is kept. A bundle that does not verify is refused, never used; offline, only
// a cached copy is read. Acquisition warnings go to the daemon's log stream.
func (s *Server) answerExample(ctx context.Context, req *jsonRPCRequest, form exampleops.Form, params scaffoldEntityParams) error {
	ws, err := s.session.workspaceFor(params.WorkspaceRoot)
	if err != nil {
		return s.writeRefusal(ctx, req.ID, jsonRPCInvalidParams, "invalid params: ", err)
	}

	var name, named, dest string
	if form == exampleops.FormWrite {
		name = strings.TrimSpace(params.Example)
		if err := exampleops.CheckName(name); err != nil {
			return s.writeRefusal(ctx, req.ID, jsonRPCInvalidParams, "", err)
		}
		named = strings.TrimSpace(params.Dir)
		if named == "" {
			named = name
		}
		if dest, err = ws.workspacePath("dir", named); err != nil {
			return s.writeRefusal(ctx, req.ID, jsonRPCInvalidParams, "invalid params: ", err)
		}
	}

	warn := func(notice string) { fmt.Fprintf(s.stderr, "  scaffoldEntity: %s\n", notice) }
	// The request's context does not descend from the one the process was
	// launched with, so what the launch stated about the documentation bundle is
	// stated on it again here (session.docs).
	examples, err := exampleops.Open(s.docs.Onto(ctx), ws.wsConfig, s.compilerVersion, warn)
	if err != nil {
		return s.writeRefusal(ctx, req.ID, jsonRPCInvalidParams, "", err)
	}
	if note := examples.Note(); note != "" {
		warn(note)
	}

	if form == exampleops.FormList {
		list, err := examples.List()
		if err != nil {
			return s.writeRefusal(ctx, req.ID, jsonRPCInvalidParams, "", fmt.Errorf(
				"the documentation bundle %s@%s carries no readable list of examples: %w",
				examples.Ref, examples.Version, err))
		}
		out := make([]scaffoldExample, 0, len(list))
		for _, e := range list {
			out = append(out, scaffoldExample{Name: e.Name, Level: e.Level, Summary: e.Summary})
		}
		return s.writeResult(ctx, req.ID, scaffoldEntityResult{Examples: out})
	}

	written, err := examples.Write(name, dest)
	if err != nil {
		return s.writeRefusal(ctx, req.ID, jsonRPCInvalidParams, "", err)
	}
	return s.writeResult(ctx, req.ID, scaffoldEntityResult{
		Example: written.Name,
		Dir:     named,
		Files:   written.Files,
		Next:    written.Next,
		Version: examples.Version,
	})
}
