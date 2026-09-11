// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package serve

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/aggrega-ai/aggrega-cli/pkg/pathscope"
	"github.com/aggrega-ai/aggrega-cli/pkg/yamlpos"
)

// InsertionPointResult is where a new entry under a named container belongs in
// an authored document, on every surface that carries one.
//
// File is workspace-relative, never the absolute host path the answer was
// computed from, for the same reason every other projection here reports a
// relative file: a caller addresses documents inside a workspace it named, and
// an absolute path is a fact about this machine rather than about the answer.
//
// Container echoes the path the request asked about, so a caller batching
// several questions can pair each answer with its question without keeping its
// own order.
//
// PositionEncoding names the units Position's character offset is measured in.
// It is reported rather than assumed because the two surfaces reach the encoding
// differently — a session negotiates one, a single call names one or takes the
// default — and a position whose units a caller has to guess is a position it
// cannot apply.
//
// The insertion itself is embedded rather than restated field by field, so the
// keys a caller reads are the keys every surface reports it under, by
// construction rather than by agreement.
type InsertionPointResult struct {
	File             string   `json:"file"`
	Container        []string `json:"container"`
	PositionEncoding string   `json:"positionEncoding"`
	yamlpos.Insertion
}

// The conditions on which no insertion point is reported.
//
// They are unexported values rather than registered halting conditions, and the
// distinction is the one the registry itself draws: a registered condition names
// a rule that halts a build and carries a code a user quotes off a diagnostic.
// None of these halts anything. Each says that one request's arguments do not
// address a place an entry can go in one document, which is what every surface
// here already has a refusal channel for — so they travel as the text of that
// refusal and nothing acquires a code it would then have to keep forever.
var (
	errInsertionNoFile = errors.New("no document was named, so there is nothing to locate a position in")

	errInsertionFileOutsideWorkspace = errors.New("the document named is not inside the workspace, and a " +
		"position is only reported for a document the workspace carries")

	errInsertionFileUnreadable = errors.New("the document named could not be read")

	errInsertionFileUnparseable = errors.New("the document named is not well-formed YAML, so it has no " +
		"structure to locate a position in")
)

// dispatchInsertionPoint reports where a new entry under the named container
// belongs in an authored document the workspace carries.
//
// # It reports and never writes
//
// Read-only in the strongest sense: it mutates nothing, resolves nothing,
// reaches no registry or network, and writes nothing to any document. It does
// not compose the text of an entry and does not apply one. It answers where, and
// the caller composes and applies whatever edit follows, because the caller is
// the surface that owns the buffer and the undo history the edit lands in.
//
// # What the answer is derived from
//
// The bytes the author wrote, and nothing else. The request names the container
// as a structural path, so which key an attachment belongs under is the caller's
// knowledge rather than something inferred here from a name, a type or a label.
// This reports which of that path the document already carries, the column its
// entries start at, the nesting step the document's own indentation exhibits,
// and the last line its innermost block reaches.
//
// # Where an unsaved buffer is read from
//
// The session's parse cache first, so a position is reported against what the
// editor is holding rather than against what was last written to disk, and the
// file itself when the cache carries nothing for it.
func (s *Server) dispatchInsertionPoint(ctx context.Context, req *jsonRPCRequest) error {
	var params insertionPointParams
	if len(req.Params) > 0 {
		if err := decodeParams(req, &params); err != nil {
			return s.writeError(ctx, req.ID, jsonRPCInvalidParams, "invalid params: "+err.Error())
		}
	}
	if err := ctx.Err(); err != nil {
		return s.writeError(ctx, req.ID, jsonRPCInternalError, err.Error())
	}
	result, err := s.insertionPoint(params.File, params.Container)
	if err != nil {
		// Every refusal here says the request's arguments do not address a
		// place an entry can go, which is what this code means: a retry
		// changes nothing until the file or the container path changes.
		return s.writeError(ctx, req.ID, jsonRPCInvalidParams, err.Error())
	}
	return s.writeResult(ctx, req.ID, result)
}

// insertionPointParams is the aggrega/insertionPoint request shape.
//
// File names the document, relative to the workspace root or by absolute path,
// as every other per-document request here is named.
//
// Container is the structural path to the container the entry goes inside,
// from the document root: each element a mapping key, or a sequence position
// written in decimal. It is the caller's statement of where such an entry
// belongs in a document of this kind; what this surface answers is where, in
// this particular document, that place is.
type insertionPointParams struct {
	File      string   `json:"file"`
	Container []string `json:"container"`
}

// insertionPoint answers over the session's own view of the workspace: the
// negotiated position encoding, and the parse cache as the fresher-than-disk
// overlay.
func (s *Server) insertionPoint(file string, container []string) (InsertionPointResult, error) {
	return insertionPointIn(s.workspaceRoot, file, container, s.positionEncoding(), func(abs string) ([]byte, bool) {
		entry, ok := s.parseCache.Get(abs)
		if !ok {
			return nil, false
		}
		return entry.content, true
	})
}

// InsertionPoint is the whole of insertionPoint with the root and the position
// encoding supplied rather than read off a session, so the session-bound method
// and a caller holding no session are one derivation rather than two that agree
// today.
//
// What a caller without a session does not supply is the overlay of documents an
// editor is holding unsaved; with none, the answer is read from what is on disk,
// which is what such a caller can see anyway.
func InsertionPoint(root, file string, container []string, enc yamlpos.PositionEncoding) (InsertionPointResult, error) {
	return insertionPointIn(root, file, container, enc, nil)
}

// insertionPointIn is the whole of InsertionPoint with the fresher-than-disk
// overlay supplied, which only a caller holding a session has.
func insertionPointIn(root, file string, container []string, enc yamlpos.PositionEncoding,
	overlay func(abs string) ([]byte, bool)) (InsertionPointResult, error) {

	if strings.TrimSpace(file) == "" {
		return InsertionPointResult{}, errInsertionNoFile
	}

	// The caller may name the file either way, so the containment judgement takes
	// it in whichever form it arrived and answers with the workspace-relative name.
	// The absolute form is rebuilt from that name rather than from the argument,
	// which is what makes it the cleaned path the parse cache is keyed by.
	rel, inside := pathscope.Relative(root, file)
	if !inside {
		return InsertionPointResult{}, errInsertionFileOutsideWorkspace
	}
	abs := filepath.Join(root, rel)

	var content []byte
	if overlay != nil {
		content, _ = overlay(abs)
	}
	if content == nil {
		read, err := os.ReadFile(abs)
		if err != nil {
			return InsertionPointResult{}, errInsertionFileUnreadable
		}
		content = read
	}

	var doc yaml.Node
	if err := yaml.Unmarshal(content, &doc); err != nil {
		return InsertionPointResult{}, errInsertionFileUnparseable
	}

	idx := yamlpos.BuildLineIndexWithEncoding(content, enc)
	insertion, refusal := yamlpos.InsertionPointIn(&doc, idx, container)
	if refusal != yamlpos.InsertionAdmitted {
		return InsertionPointResult{}, errors.New(refusal.Reason())
	}

	return InsertionPointResult{
		File:             rel,
		Container:        append([]string(nil), container...),
		PositionEncoding: string(idx.Encoding()),
		Insertion:        insertion,
	}, nil
}
