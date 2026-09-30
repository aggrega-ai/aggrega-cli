// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package mcp

import (
	"context"
	"fmt"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/internal/clisurface"
	"github.com/aggrega-ai/aggrega-cli/internal/opsurface"
)

// One capability, two tools.
//
// A curated tool runs its operation inside this server and answers with a
// machine-readable result. The tool generated from the command that carries the
// same operation runs that command in a separate process and answers with the
// command line it ran and the text that command printed. Both are reachable by
// name, and they do not behave alike: the generated one does whatever the
// command does on the machine it runs on, which for some commands means writing
// to the caller's disk, and it returns no structure at all — no positions, no
// identities, no typed findings. That is inherent to running a command rather
// than a shortcoming of how the tool was built.
//
// The in-process form is the one to reach for; the generated one stays as the
// route to what only the command offers. What this file adds is that each says
// so. Every tool in such a pair carries, in the description a client shows an
// assistant, the other tool's name and how the two differ — because a caller
// choosing between two similarly-named tools is otherwise choosing blind, and
// one of the two choices can write to its disk.
//
// The pairing is derived and never listed. It is read from the two records that
// already decide it — which command carries an operation, and which curated tool
// answers one — so a command renamed, exempted, hidden or added moves the
// disclosure with it. A list of pairs beside those records would be a third copy
// of what they already say, and the copy is what goes stale.
//
// The same file carries the one predicate for whether this session derived any of
// those generated tools at all, and the per-session pass that composes every
// sentence scoped on it. A pairing disclosure and a sentence about the family as a
// whole are the same claim at two widths, so they are withheld together or not at
// all, and putting them behind two predicates is how they would come to disagree.

// commandTwin is the generated tool that reaches one operation by running its
// command in a separate process: the tool's advertised name, and the command
// behind it.
type commandTwin struct {
	tool    string
	command string
}

// commandTwins indexes generated tools by the operation each answers, so a
// curated tool can find the generated tool that reaches the same capability.
//
// It reads the specs the derivation already produced rather than walking the
// command surface a second time. A second walk would be a second derivation of
// which commands yield a tool, and it would agree with the first right up until
// one of them learned about a skip the other did not.
func commandTwins(specs []toolSpec) map[string]commandTwin {
	out := make(map[string]commandTwin, len(specs))
	for i := range specs {
		if specs[i].operation == "" {
			continue
		}
		out[specs[i].operation] = commandTwin{tool: specs[i].name, command: specs[i].commandPath}
	}
	return out
}

// commandOperation returns the operation the command at path CARRIES, empty when
// the command line records none for it.
//
// A path can be claimed by more than one operation, and the distinction the
// answer turns on is between the capability a command IS and a capability it
// merely OFFERS. A command is the sole way to reach the first, so the operation
// naming that path and no other is the one it carries; a capability reachable at
// several commands — a document any of them can hand back under a flag — is
// offered by each and is none of their identities. So a record naming exactly
// this path wins over one naming it among others.
//
// That rule is read from the availability records rather than from any ordering,
// which is what keeps the answer independent of the order the operation set is
// written in. The set's own contract says nothing derives behaviour from that
// order, and a tie broken by position would quietly falsify it: a capability
// declared before the command it is offered by would take the command's identity,
// and the only symptom would be a pairing disclosure silently going missing.
//
// The walk still follows the operation set's order, so among records that are
// equally specific the answer never depends on map iteration and the description
// bytes a client receives stay identical across runs.
func commandOperation(path string) string {
	table := clisurface.OperationAvailability()
	fallback := ""
	for _, op := range opsurface.Operations() {
		a, ok := table[op.ID]
		if !ok || !a.IsExposed() {
			continue
		}
		for _, p := range a.Names {
			if p != path {
				continue
			}
			if len(a.Names) == 1 {
				return op.ID
			}
			if fallback == "" {
				fallback = op.ID
			}
		}
	}
	return fallback
}

// curatedSibling returns the curated or deferred tool that answers the given
// operation inside this server, if one does.
//
// It matches on the operation each tool records rather than on a name. The two
// sides of a pair are named independently — one for the goal it serves, one for
// the command path it runs — so a name is exactly what cannot be compared here.
func curatedSibling(operation string) (toolSpec, bool) {
	if operation == "" {
		return toolSpec{}, false
	}
	for _, registry := range [][]toolSpec{toolRegistry, deferredToolRegistry} {
		for i := range registry {
			if registry[i].operation == operation {
				return registry[i], true
			}
		}
	}
	return toolSpec{}, false
}

// curatedSiblingNote is what a generated tool says about the curated tool
// answering the same capability: that tool's name, its own opening sentence, and
// what separates the two forms.
//
// The sibling's opening sentence is quoted rather than summarized, and that is
// what makes the disclosure per-pair rather than generic: a curated tool's first
// sentence is where it states what it produces and what it writes, which is the
// very thing a caller needs in order to choose between the two. Summarizing it
// here would be a second description of a tool that already has one, and the
// second copy is the one that stops being true.
//
// It follows that a curated tool's first sentence carries what that tool writes.
// Every one of them does today; a first sentence that stops saying so leaves the
// caller of its generated twin unable to tell the two apart on the axis that
// matters most.
func curatedSiblingNote(sibling toolSpec) string {
	return fmt.Sprintf(" The `%s` tool answers this same capability inside this server: %q "+
		"Prefer it — it returns a machine-readable result you can read field by field. This tool runs the "+
		"real command in a separate process instead, so it does whatever that command does on the machine "+
		"it runs on, anything it writes included, and returns only the command line it ran plus the text "+
		"that command wrote to its output and diagnostic streams.",
		sibling.name, firstSentence(sibling.description))
}

// commandTwinNote is what a curated tool says about the generated tool reaching
// the same capability by running its command: that tool's name, the command
// behind it, and when a caller would want it instead.
func commandTwinNote(twin commandTwin) string {
	return fmt.Sprintf(" Also reachable as `%s`, which runs `%s` in a separate process — it does whatever "+
		"that command does on the machine it runs on, anything it writes included, and returns that "+
		"command's text rather than a result you can read field by field. Prefer this tool; reach for `%s` "+
		"when you need a flag the command declares that this one does not accept.",
		twin.tool, twin.command, twin.tool)
}

// withCommandTwinNote returns spec with its generated twin disclosed in the
// description a client shows an assistant.
//
// It returns a copy rather than editing the spec in place. The registries are
// package-level and shared by every session, while which commands a session was
// handed is a property of that session alone, so a session writing into a
// registry would change what a concurrent one advertises.
func withCommandTwinNote(spec toolSpec, twin commandTwin) toolSpec {
	spec.description += commandTwinNote(twin)
	return spec
}

// withCommandSurfaceNote returns spec with what it says about the generated
// command tools disclosed in the description a client shows an assistant.
//
// It returns a copy for the reason the pairing disclosure does, and the joining
// space is written here rather than into the sentence: the same sentence is
// rendered into a heading where nothing precedes it, so a leading space carried
// on the declaration would reach that surface too.
func withCommandSurfaceNote(spec toolSpec) toolSpec {
	spec.description += " " + spec.commandSurfaceNote
	return spec
}

// offersCommandTools reports whether this session derived any tool that reaches
// a capability by running a command in a separate process.
//
// It is the ONE predicate every sentence about that family is scoped on, and it
// asks what this session holds rather than how its caller reached it. Those are
// different questions: a session reached over a listener derives none of those
// tools, and so does a session over the process's own streams that was handed no
// command surface. A second derivation from the transport would answer the first
// correctly and the second wrongly, and nothing would fail while no such session
// was being served.
func (s *server) offersCommandTools() bool {
	return len(s.commandTools) > 0
}

// withSessionNotes returns specs as this session advertises them: each tool
// carrying the disclosure of the generated tool that reaches its capability
// where this session derived one, each tool carrying what it says about the
// generated family where this session derived any, and otherwise exactly as
// declared.
//
// Both are read per call from the tools the session holds rather than stored
// beside them, so a sentence and the tools it describes cannot come to disagree.
// A session handed no command surface derives no generated tools and therefore
// makes no claim about one — which is what keeps either sentence honest rather
// than merely usually true.
func (s *server) withSessionNotes(specs []toolSpec) []toolSpec {
	twins := commandTwins(s.commandTools)
	offersCommands := s.offersCommandTools()
	out := make([]toolSpec, 0, len(specs))
	for i := range specs {
		spec := specs[i]
		if twin, ok := twins[spec.operation]; ok {
			spec = withCommandTwinNote(spec, twin)
		}
		if offersCommands && spec.commandSurfaceNote != "" {
			spec = withCommandSurfaceNote(spec)
		}
		out = append(out, spec)
	}
	return out
}

// curatedTools returns the curated, always-advertised set as this session serves
// it to this request, in registry order: each tool carrying whatever this session
// makes true of it, and only the tools a listing answered to this request names,
// which the request's own credential does not narrow.
func (s *server) curatedTools(ctx context.Context) []toolSpec {
	return s.admittedReach(ctx).listed(s.withSessionNotes(toolRegistry))
}

// firstSentence returns the leading sentence of a tool description, for the
// one-line human summary and for the disclosure one member of a pair carries
// about the other.
func firstSentence(desc string) string {
	if i := strings.IndexByte(desc, '.'); i >= 0 {
		return desc[:i+1]
	}
	return desc
}
