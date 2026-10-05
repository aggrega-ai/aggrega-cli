// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package compilerops

import (
	stderrors "errors"
	"os"
	"path/filepath"

	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/pathscope"
)

// RenderRefusal is a refusal the pipeline run raised, reduced to what a surface
// that places a finding in an authored document needs: the document, the key
// within it, the refusal in the compiler's words, and the refusal itself.
//
// Every surface that carries such a refusal as a positioned finding reads it from
// here, so the language server's diagnostic, its error answer and the assistant
// build tool's entry name one document and one key for one refusal.
type RenderRefusal struct {
	// Document is the authored document the refusal points at: the document the
	// refusal's own chain names where the chain names one, as an absolute path, and
	// the Aggregate the run resolved where it names none, at the path the run
	// resolved it at — the document a finding that names no authored document is
	// attributed to on every positioned surface.
	Document string

	// KeyPath is the dotted key within Document the refusal points at, or empty
	// where its chain names a document and no key, or names no document at all.
	KeyPath string

	// Content is the authored bytes of Document — the bytes the run read where
	// Document is the Aggregate it resolved, the file on disk otherwise — or nil
	// where the document cannot be read. A surface resolves KeyPath to a position
	// against these, in whatever encoding its reader counts columns in.
	Content []byte

	// Message is the refusal with every cause beneath it, as [cerrors.CauseChain]
	// renders it: the same text the refusal raised, frame by frame, with nothing
	// derived from the data it was raised over. Whatever the raising sites withheld
	// from that text stays withheld here.
	Message string

	// Err is the refusal as raised, for the identifiers of the condition its
	// chain names.
	Err error
}

// RenderRefusal returns the refusal the pipeline run raised, and whether the
// preview stopped there. A preview that stopped at an earlier gate, or did not
// stop, reports false: what it stopped on, if anything, is a gate's own refusal
// with an answer of its own.
//
// # Where it points, and how a document is told from a name
//
// The document and key are read off the refusal's own chain, through one route
// for every condition the run can raise:
//
//  1. The innermost location a raising site stated for the surfaces that place
//     findings ([cerrors.LocatedAt]) — a site holding the document says which,
//     and the key within it where it has one.
//  2. Otherwise the innermost categorised frame whose pointer names a document:
//     one that carries a key beside it (a pointer with a key names a document by
//     the pointer's own contract), or one that is an absolute path. A pointer is
//     otherwise as often a node, an entity's name or an output's name as a file —
//     and a relative one cannot be told from a name at all, while no name is an
//     absolute path — so nothing else is read as a document.
//  3. Otherwise the Aggregate the run resolved, with no key.
//
// A candidate names a document only where it resolves to a regular file. One that
// names something that is not one — a directory, a path nothing is at — is passed
// over for the next candidate rather than reported as a place nobody can open.
func (p *PreviewResult) RenderRefusal() (RenderRefusal, bool) {
	if p == nil || p.FailedGate != GateRender || p.renderFailure == nil {
		return RenderRefusal{}, false
	}
	refusal := RenderRefusal{
		Message: cerrors.CauseChain(p.renderFailure),
		Err:     p.renderFailure,
	}
	refusal.Document, refusal.KeyPath = refusalLocation(p.renderFailure, p.workspaceRoot)
	if refusal.Document != "" {
		if content, err := os.ReadFile(refusal.Document); err == nil {
			refusal.Content = content
		}
		return refusal, true
	}
	if p.Resolved != nil {
		refusal.Document = p.Resolved.AggregatePath
		refusal.Content = p.Resolved.AggBytes
	}
	return refusal, true
}

// refusalLocation walks a refusal's chain for the document and key it points at,
// in the order RenderRefusal states. It returns empty strings where the chain names
// no document.
func refusalLocation(err error, workspaceRoot string) (document, keyPath string) {
	if at, ok := cerrors.LocatedAt(err); ok {
		if doc, ok := regularFile(at.File, workspaceRoot); ok {
			return doc, at.Locator
		}
	}
	var frames []*cerrors.CompilerError
	for cur := err; cur != nil; cur = stderrors.Unwrap(cur) {
		if ce, ok := cur.(*cerrors.CompilerError); ok && ce != nil {
			frames = append(frames, ce)
		}
	}
	for i := len(frames) - 1; i >= 0; i-- {
		src := frames[i].Source()
		if src.Locator == "" && !filepath.IsAbs(src.File) {
			continue
		}
		if doc, ok := regularFile(src.File, workspaceRoot); ok {
			return doc, src.Locator
		}
	}
	return "", ""
}

// regularFile resolves path — absolute, or relative to the workspace root and
// inside it — and reports it cleaned when it names a regular file.
func regularFile(path, workspaceRoot string) (string, bool) {
	if path == "" {
		return "", false
	}
	if filepath.IsAbs(path) {
		path = filepath.Clean(path)
	} else {
		inside, ok := pathscope.Resolve(workspaceRoot, path)
		if !ok {
			return "", false
		}
		path = inside
	}
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() {
		return "", false
	}
	return path, true
}
