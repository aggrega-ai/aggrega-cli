// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"context"
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/aggrega-ai/aggrega-cli/internal/clisurface"
	"github.com/aggrega-ai/aggrega-cli/internal/opprogress"
)

// operationObserver draws the steps an operation reports it waits on
// (opprogress) through the progress reporter build, pull and registry update
// draw through, so a command whose operation waits on a registry, a download or
// a plugin compile shows it the way they do: on a terminal, a row per step with
// a spinner and its elapsed time; on a terminal that cannot redraw, or under
// --plain, one line per step as it ends; and nothing where standard error is not
// a terminal.
type operationObserver struct {
	p *progressReporter
}

func (o operationObserver) Begin(step string) opprogress.End {
	i := o.p.beginStep(step)
	return func(err error) { o.p.endStep(i, err != nil) }
}

func (o operationObserver) Yield() { o.p.hold() }

func (o operationObserver) Beside(w io.Writer) io.Writer { return o.p.beside(w) }

// stepWaitObserver is the observer a command that draws its own steps gives its
// operation (newOwnProgress): it shows the operation's waits on someone else
// (opprogress.Wait) — a registry that asked to slow down — beside the step in
// flight, through the command's one reporter (beginWait), and draws none of the
// operation's own steps (opprogress.Begin). The command's spine already draws its
// steps, so a step the operation reports as well — the plugin loader's compile,
// beside build's own compile row — would be drawn a second time. It draws on the
// reporter's terms: beside the row on a terminal, one line on a terminal that
// cannot redraw or under --plain, and nothing where standard error is not a
// terminal.
type stepWaitObserver struct {
	p *progressReporter
}

func (stepWaitObserver) Begin(string) opprogress.End { return func(error) {} }

func (o stepWaitObserver) Wait(step string) opprogress.End {
	end := o.p.beginWait(step)
	return func(error) { end() }
}

func (o stepWaitObserver) Yield() { o.p.hold() }

func (o stepWaitObserver) Beside(w io.Writer) io.Writer { return o.p.beside(w) }

// newOwnProgress is the one place a command that draws its own progress for its
// whole run (clisurface.DrawsOwnProgress) makes its reporter: it returns the
// reporter for spine on out, and ctx with the waits its operation reports drawn
// through that reporter (stepWaitObserver). operation names the operation whose
// row says the command draws its own progress, and an operation whose row says
// otherwise is refused as the programming error it is, as the attach site refuses
// a command the table does not account for (mustAttach). A test holds the set of
// operations passed here to the set of those rows, so no such command is left
// without its waits drawn, and holds newProgressReporter to being called nowhere
// else but for the shared surface (showsOperationProgress).
//
// Where the reporter draws nothing, ctx is returned as it is: a caller that
// reached the command's run with an observer of its own — a daemon request, an
// assistant tool running the operation in process — keeps having the waits
// reported to it, rather than to a reporter that shows nothing.
func newOwnProgress(ctx context.Context, operation string, out io.Writer, opts buildDiagOptions, spine progressSpine) (*progressReporter, context.Context) {
	if display, _ := clisurface.OperationWaitDisplay(operation); display != clisurface.DrawsOwnProgress {
		panic(fmt.Sprintf("internal: %q is not an operation whose command draws its own progress", operation))
	}
	p := newProgressReporter(out, opts, spine)
	if !p.draws() {
		return p, ctx
	}
	return p, opprogress.With(ctx, stepWaitObserver{p: p})
}

// operationProgressSpine is the progress spine of a command whose steps are the
// ones its operation reports as it reaches them: none known up front, each added
// as it begins (beginStep), with no render phase and no bar.
func operationProgressSpine() progressSpine {
	return progressSpine{renderIdx: -1}
}

// showsOperationProgress makes c show the steps its operation reports waiting on,
// and returns c.
//
// Its run installs an operationObserver on the context the command body runs
// under, drawing on the command's error stream, so every step the operation
// reports — at whatever depth it waits — is drawn by the one reporter. A run that
// waits on nothing reports no step and draws nothing at all.
//
// A step's block is left standing once the step ends, so whatever the command
// writes after it lands beneath it. That is why an operation ends a step before
// it writes anything, or writes through opprogress.Beside where its lines fall
// within a step, and why a command that draws progress of its own is not given
// one: the two surfaces would draw over each other. Which commands are given it
// is the operation table's to say (clisurface.OperationWaitDisplay), and the
// attach site applies it from there (mustAttach), never a command's own file.
func showsOperationProgress(c *cobra.Command) *cobra.Command {
	run := c.RunE
	if run == nil {
		return c
	}
	c.RunE = func(cmd *cobra.Command, args []string) error {
		progress := newProgressReporter(cmd.ErrOrStderr(),
			buildDiagOptions{noColor: globalDisplay.colorOff, plain: globalDisplay.plain}, operationProgressSpine())
		defer progress.finish()
		cmd.SetContext(opprogress.With(cmd.Context(), operationObserver{p: progress}))
		return run(cmd, args)
	}
	return c
}
