// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"fmt"
	"io"

	"github.com/aggrega-ai/aggrega-cli/internal/redact"
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
const (
	flagIRGraph = "ir-graph"

	helpIRGraph = "Also write the IR graph this run computed to stdout, as JSON, in addition to everything this command already produces. Values an author declared sensitive are withheld from it"
)

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
// affordance where it has one; a command with none passes false and withholds
// unconditionally.
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
