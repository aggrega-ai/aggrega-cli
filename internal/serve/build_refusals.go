// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package serve

import (
	"context"
	"reflect"
)

// This file holds what the daemon knows about the refusals a build reports on a
// document, which the composition reads so a document's own validation does not
// report the same fault a second time. See snapshotDiagnostics for the rule and
// restatesRefusal for what counts as the same fault.

// operationRefusal is the refusal one answer to a build, a preview or a graph
// request carried because a dependency of its Release does not resolve: the
// document a client files that refusal on, and the refusal as an entry there.
type operationRefusal struct {
	file       string
	diagnostic Diagnostic
}

// buildRefusalsOnLocked returns every build refusal standing on the file keyed
// key: the refusal findings on the build layer there, and each Release's
// latest dependency refusal filed there. diagMu must be held.
func (s *Server) buildRefusalsOnLocked(key string) []Diagnostic {
	out := append([]Diagnostic(nil), s.renderRefusals[key]...)
	for _, refusal := range s.operationRefusals {
		if refusal.file == key {
			out = append(out, refusal.diagnostic)
		}
	}
	return out
}

// withoutRestatedFaults returns semantic less every entry that restates one of
// refusals, in order. It returns semantic itself where nothing is omitted, and
// never writes to it.
func withoutRestatedFaults(semantic, refusals []Diagnostic) []Diagnostic {
	if len(refusals) == 0 {
		return semantic
	}
	var kept []Diagnostic
	omitted := false
	for _, entry := range semantic {
		restated := false
		for _, refusal := range refusals {
			if restatesRefusal(entry, refusal) {
				restated = true
				break
			}
		}
		if restated {
			omitted = true
			continue
		}
		kept = append(kept, entry)
	}
	if !omitted {
		return semantic
	}
	return kept
}

// restatesRefusal reports whether a document's own finding reports the fault a
// build refusal on the same document reports.
//
// Both must name the same registered condition: the code is the condition's own
// identity, read off each one's chain where it was raised, so two entries naming
// different conditions are two faults whatever they say, and an entry naming no
// condition restates nothing. And the finding must be placed no differently from
// the refusal: either at the very span the refusal sits at, or at no key at all —
// which is how a validation reports a fault it can place no more precisely than
// its document, such as a single-entity check's finding whose chain names no key
// in the entity, or a closure that does not resolve. A finding the validation places at a
// key the refusal does not sit at is kept: it may be a second fault of the same
// condition the build has not reported, and two entries for one fault cost a
// reader less than none for a second one.
//
// A finding at no key is read off the entry rather than off its position alone.
// One drawn through keyPathSpan's fall-back sits on its document's kind: key or
// its metadata.name, which are positions a finding can also genuinely point at, so
// it carries the fact itself; one a producer draws at the head of the file as a
// literal is still recognised by that position.
func restatesRefusal(finding, refusal Diagnostic) bool {
	if finding.Code == "" || finding.Code != refusal.Code {
		return false
	}
	if finding.unplaced {
		return true
	}
	if finding.EndLine == 0 && finding.EndColumn == 0 && finding.Line <= 1 && finding.Column <= 1 {
		return true
	}
	return finding.Line == refusal.Line && finding.Column == refusal.Column &&
		finding.EndLine == refusal.EndLine && finding.EndColumn == refusal.EndColumn
}

// noteOperationAnswer records what the answer to a build, a preview or a graph
// request for release says about a dependency refusal, and publishes each
// document whose composed view that changes.
//
// It is called with every answer one of those requests is given — a result, or
// any refusal — and never for a request withdrawn before it was answered, which
// tells nothing about the Release. A refusal coded entity-not-resolvable is
// recorded, on the document a client files it on: the file its data names, else
// the lockfile its data names, else the Release; any other answer clears what the
// Release's earlier answers recorded. The record is the latest answer's alone,
// because a client shows the latest answer's refusal and no earlier one.
//
// A Release the request names unreadably records nothing: no refusal of that
// request could have been recorded under it either.
func (s *Server) noteOperationAnswer(ctx context.Context, release string, answer *jsonRPCError) {
	releasePath, paramErr := resolveReleaseParam(s.workspaceRoot, s.wsConfig, release)
	if paramErr != nil {
		return
	}
	key := s.canonicalFile(releasePath)
	var next *operationRefusal
	if answer != nil && answer.Code == errEntityNotResolvable {
		file := releasePath
		diagnostic := Diagnostic{Line: 1, Column: 1, ConditionCodes: conditionCodesOf(answer.cause)}
		if d := answer.Data; d != nil {
			switch {
			case d.File != "":
				file = d.File
				if d.Line > 0 {
					diagnostic.Line, diagnostic.Column = d.Line, d.Column
					diagnostic.EndLine, diagnostic.EndColumn = d.EndLine, d.EndColumn
				}
			case d.Lockfile != "":
				file = d.Lockfile
			}
		}
		next = &operationRefusal{file: s.canonicalFile(file), diagnostic: diagnostic}
	}

	s.diagMu.Lock()
	prev, had := s.operationRefusals[key]
	// The documents this change can reach are the one the previous answer's
	// refusal was filed on and the one this answer's is filed on. Each is composed
	// on either side of the change under this one hold, so what the comparison sees
	// moving is this change and nothing else.
	var reach []string
	if had {
		reach = append(reach, prev.file)
	}
	if next != nil && (!had || next.file != prev.file) {
		reach = append(reach, next.file)
	}
	before := make([][]Diagnostic, len(reach))
	for i, file := range reach {
		before[i] = s.composedLocked(file)
	}
	if next == nil {
		delete(s.operationRefusals, key)
	} else {
		if s.operationRefusals == nil {
			s.operationRefusals = map[string]operationRefusal{}
		}
		s.operationRefusals[key] = *next
	}
	var moved []string
	for i, file := range reach {
		if !reflect.DeepEqual(before[i], s.composedLocked(file)) {
			moved = append(moved, file)
		}
	}
	s.diagMu.Unlock()

	// 🔴 A CHANGED RECORD IS NOT A CHANGED DOCUMENT. The record is never an entry
	// of its own: it only leaves out a document's own finding it restates. So over
	// a document reporting nothing it restates — a Release no validation has
	// reported on, or a Release file that could not be loaded — recording or
	// clearing it moves nothing, and a publication there would tell a client about
	// a document nothing reported on. Only a document whose composition moved is
	// published. Whether that frame is news to the client is publishDiagnostics'
	// question, as for every publication; this asks only what the change moved.
	for _, file := range moved {
		_ = s.publishDiagnostics(ctx, file)
	}
}

// forgetOperationRefusals drops every Release's recorded dependency refusal. It is
// called at the handshake beside forgetPublished, for the same reason: the record
// describes what a client was answered, and a client that connects holds no
// refusal it was answered before.
func (s *Server) forgetOperationRefusals() {
	s.diagMu.Lock()
	defer s.diagMu.Unlock()
	s.operationRefusals = nil
}
