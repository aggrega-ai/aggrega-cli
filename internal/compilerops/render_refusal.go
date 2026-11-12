// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package compilerops

import (
	stderrors "errors"
	"os"
	"path/filepath"

	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/pathscope"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
)

// RenderRefusal is a refusal the pipeline run raised, reduced to what a surface
// that places a finding in an authored document needs: each finding the refusal
// states, where that finding points, and the refusal itself in the compiler's
// words.
//
// Every surface that carries such a refusal as positioned findings reads it from
// here, so the language server's diagnostics, its error answer and the assistant
// build tool's entry name one place for each finding of one refusal.
type RenderRefusal struct {
	// Findings is what the refusal states, in the order it states it: one entry
	// for a refusal that states one failure, and one per failure for a refusal that
	// joins several — the failures a terminal prints as diagnostics of their own,
	// each under its own head. It is never empty, and its first entry is where the
	// refusal as a whole points.
	Findings []RefusalFinding

	// Message is the refusal with every cause beneath it, as [cerrors.CauseChain]
	// renders it: the same text the refusal raised, frame by frame, with nothing
	// derived from the data it was raised over. Whatever the raising sites withheld
	// from that text stays withheld here. For a refusal joining several findings it
	// carries every one of them.
	Message string

	// Err is the refusal as raised, for the identifiers of the condition its
	// chain names.
	Err error

	// RaisedWhileRendering reports whether the run had reached its render step
	// when it refused, rather than refusing at a stage before it — the composition
	// of the graph, or the assertions a Component or a Feature declares.
	RaisedWhileRendering bool
}

// RefusalFinding is one finding a refusal states, placed where its own chain
// points.
type RefusalFinding struct {
	// Document is the authored document the finding points at: the document the
	// finding's own chain names where the chain names one, as an absolute path,
	// and the Aggregate the run resolved where it names none, at the path the run
	// resolved it at — the document a finding that names no authored document is
	// attributed to on every positioned surface.
	Document string

	// KeyPath is the dotted key within Document the finding points at, or empty
	// where its chain names a document and no key, or names no document at all.
	KeyPath string

	// Content is the authored bytes of Document — the bytes the run read where
	// Document is the Aggregate it resolved, the file on disk otherwise — or nil
	// where the document cannot be read. A surface resolves KeyPath to a position
	// against these, in whatever encoding its reader counts columns in.
	Content []byte

	// Message is the finding with every cause beneath it, as [cerrors.CauseChain]
	// renders it. For a refusal stating one finding it is the refusal's own
	// message.
	Message string

	// Err is the finding as raised, for the identifiers of the condition its
	// chain names.
	Err error
}

// RenderRefusal returns the refusal the pipeline run raised, and whether the
// preview stopped there. A preview that stopped at an earlier gate, or did not
// stop, reports false: what it stopped on, if anything, is a gate's own refusal
// with an answer of its own.
//
// # How many findings it states
//
// The findings are read the way the command line reads the same refusal when it
// prints it ([LeadingFrame]): a refusal whose leading frame joins several failures
// states each of them as a finding of its own — the composition of a graph, for
// one, refuses with every violation it found joined — and every other refusal
// states one. A join sitting beneath an envelope stays that envelope's detail,
// exactly as it is printed as that envelope's causes.
//
// # Where each finding points, and how a document is told from a name
//
// The document and key are read off the finding's own chain, through one route
// for every condition the run can raise:
//
//  1. The innermost location a raising site stated for the surfaces that place
//     findings ([cerrors.LocatedAt]) — a site holding the document says which,
//     and the key within it where it has one.
//  2. Otherwise the innermost frame whose pointer names a document. A resolver
//     finding names one wherever its subject recorded the document its locator
//     is a path inside ([resolver.ResolverError] File), with that locator as the
//     key: an attachment the Release wrote is a path inside the Release. A
//     categorised frame names one where its pointer carries a key beside it (a
//     pointer with a key names a document by the pointer's own contract), or is
//     an absolute path. A pointer is otherwise as often a node, an entity's name
//     or an output's name as a file — and a relative one cannot be told from a
//     name at all, while no name is an absolute path — so nothing else is read as
//     a document.
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
		Message:              cerrors.CauseChain(p.renderFailure),
		Err:                  p.renderFailure,
		RaisedWhileRendering: p.pipeline.ReachedRender(),
	}
	for _, failure := range leadingFailures(p.renderFailure) {
		refusal.Findings = append(refusal.Findings, p.refusalFinding(failure))
	}
	return refusal, true
}

// refusalFinding places one failure a refusal states, by the route RenderRefusal
// gives: where FindingLocation reads it, else on the Aggregate the run resolved.
func (p *PreviewResult) refusalFinding(failure error) RefusalFinding {
	finding := RefusalFinding{
		Message: cerrors.CauseChain(failure),
		Err:     failure,
	}
	finding.Document, finding.KeyPath = FindingLocation(failure, p.workspaceRoot)
	if finding.Document != "" {
		if content, err := os.ReadFile(finding.Document); err == nil {
			finding.Content = content
		}
		return finding
	}
	if p.Resolved != nil {
		finding.Document = p.Resolved.AggregatePath
		finding.Content = p.Resolved.AggBytes
	}
	return finding
}

// FindingLocation walks one finding's chain for the authored document and the key
// within it the finding points at, in the order RenderRefusal states: the
// innermost location a raising site stated, else the innermost frame whose pointer
// names a document. It returns empty strings where the chain names no document,
// and an empty key where it names a document and no key. A relative document is
// read against workspaceRoot and never outside it, and the document returned
// always names a regular file.
//
// It is the one reading of where a finding points. A refusal a preview raises is
// placed through it, and so is a finding any other surface reports about an
// authored document — the single-entity check's, for one — so the same finding
// cannot be placed at one key by one surface and another key by another. Unlike
// RenderRefusal, it attributes nothing to a document the chain does not name: a
// caller holding a document of its own decides what a finding that names none is
// drawn on.
func FindingLocation(err error, workspaceRoot string) (document, keyPath string) {
	if at, ok := cerrors.LocatedAt(err); ok {
		if doc, ok := regularFile(at.File, workspaceRoot); ok {
			return doc, at.Locator
		}
	}
	var pointers []framePointer
	for cur := err; cur != nil; cur = stderrors.Unwrap(cur) {
		if p, ok := pointerOf(cur); ok {
			pointers = append(pointers, p)
		}
	}
	for i := len(pointers) - 1; i >= 0; i-- {
		p := pointers[i]
		if !p.namesDocument && p.at.Locator == "" && !filepath.IsAbs(p.at.File) {
			continue
		}
		if doc, ok := regularFile(p.at.File, workspaceRoot); ok {
			return doc, p.at.Locator
		}
	}
	return "", ""
}

// framePointer is the pointer one frame of a refusal's chain states, and whether
// its File names a document by the frame's own contract rather than by its shape.
type framePointer struct {
	at            cerrors.SourceLocation
	namesDocument bool
}

// pointerOf returns the pointer frame states, and whether it states one.
//
// A resolver finding is read for the document its subject recorded and not for
// the envelope it converts to: that envelope carries the finding's locator in
// its File, the logical-identifier half of a pointer's dual meaning, so it names
// no document even where the finding knows which one its locator is a path
// inside. Reading the recorded document here is what places a finding about an
// attachment the Release wrote in the Release, as the validation of that document
// places it, rather than on the Aggregate a pointer naming no document falls back
// to.
func pointerOf(frame error) (framePointer, bool) {
	switch f := frame.(type) {
	case *cerrors.CompilerError:
		if f != nil {
			return framePointer{at: f.Source()}, true
		}
	case *resolver.ResolverError:
		if f != nil && f.File != "" {
			return framePointer{at: cerrors.SourceLocation{File: f.File, Locator: f.Field}, namesDocument: true}, true
		}
	}
	return framePointer{}, false
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
