// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package main

import (
	"fmt"
	"io"

	"github.com/aggrega-ai/aggrega-cli/internal/redact"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// Three commands resolve a Release far enough to hold the IR graph, and each
// offers to hand that graph back. The flag's name and its help text are declared
// once here and shared by every registration, so the three cannot drift into
// three spellings or three descriptions of one capability.
//
// It is a bare-noun boolean — the shape --trace already has on the build — and
// it is additive: the command still produces whatever it produced without it.
// It is deliberately independent of the output-format flag the inspection
// command carries: that flag selects a format for the command's own document,
// while this one selects a second document, which has one format.
//
// The reveal is declared here for the same reason. Each of the three offers it
// on the same terms — withheld unless asked, asked for by one flag of one name
// — so a reader who learns it on one command has learnt it on all three. On the
// inspection command it also reaches the readable layouts; on the other two the
// graph is the one output it reaches, so there it needs the graph to have been
// asked for (refuseUnpairedUnredacted).
const (
	flagIRGraph = "ir-graph"

	helpIRGraph = "Also write the IR graph this run computed to stdout, as JSON, in addition to everything this command already produces. Values an author declared sensitive are withheld from it; pass --" + flagUnredacted + " to print them instead"

	flagUnredacted = "unredacted"

	helpUnredacted = "Print the values an author declared sensitive instead of withholding them. Anything the output reaches — a terminal, a pipe, a file, a screen recording — then carries those values in the clear."
)

// refuseUnpairedUnredacted refuses the reveal on a command whose only output
// that withholds is the graph, when the graph was not asked for.
//
// A reveal with nothing to reveal is refused rather than ignored, because an
// ignored request reads to the person who made it as one that was honoured: they
// asked to see the withheld values, the command ran, and nothing says the values
// are still withheld. The check precedes every side effect, so a refused
// invocation has loaded, rendered and written nothing.
func refuseUnpairedUnredacted(irGraph, unredacted bool) error {
	if !unredacted || irGraph {
		return nil
	}
	return cerrors.Unidentified(cerrors.OpsError, cerrors.SourceLocation{},
		fmt.Sprintf("--%s reveals the values withheld from the graph --%s writes, and no graph was asked for; pass --%s with it, or drop --%s",
			flagUnredacted, flagIRGraph, flagIRGraph, flagUnredacted),
		nil, nil)
}

// writeIRGraph is the one place the command line serializes an IR graph. Every
// command that offers the graph routes through it, so the three hand back one
// document in one shape rather than three emissions that agree only for as long
// as nobody edits one of them.
//
// The withholding is applied here rather than by each caller, for the same
// reason: a surface that a person runs in a terminal must not be the thing that
// puts a declared-sensitive value on screen, into a shell history or into a
// pasted bug report, and a decision taken at three call sites is a decision
// three future edits can each undo alone. unredacted is the caller's own
// --unredacted, passed through as the reader set it.
//
// cmdName is the command's invocation path without the binary name, which is
// what the shared structured writer prefixes a marshal failure with.
//
// A nil graph is refused rather than emitted as a null document. A caller
// reaching here without one asked for the graph and the run produced none, and
// an empty document would read as a Release that resolved to nothing.
func writeIRGraph(out io.Writer, cmdName string, graph *types.IRGraph, unredacted bool) error {
	if graph == nil {
		return fmt.Errorf("aggrega %s: --%s was requested and this run computed no graph", cmdName, flagIRGraph)
	}
	subject := graph
	if !unredacted {
		subject = redact.Graph(graph)
	}
	return writeStructured(out, cmdName, subject)
}
