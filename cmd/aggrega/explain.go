// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"context"
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/aggrega-ai/aggrega-cli/internal/explainops"
)

// newExplainCmd builds the explain command, which turns a code a build printed
// into what the compiler declares about that condition and the published entry
// that teaches the authoring fix.
//
// The answer comes from two places, and the split is what the command is built
// around. The condition's own identifiers and the sentence its diagnostics open
// with are declared inside the binary, so that much is always available and needs
// no network and no cache. The walk-through — what the condition means and what
// to change — is prose written for a reader, so it is served from the
// documentation bundle kept in the machine-wide cache, read under this
// workspace's own signature check.
//
// A code that resolves therefore always produces an answer, and the two ways the
// prose can be missing are reported as themselves: a condition the published page
// does not document yet, and a bundle this workspace cannot read.
//
// The composition itself is not here. It is shared with every other surface that
// answers this question, so this command supplies the workspace and the streams
// and renders what comes back — which is what keeps two surfaces from answering
// one code two ways.
func newExplainCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "explain <code>",
		Short: "Explain the halting condition a code names",
		Long: "Explain the halting condition a code names.\n\n" +
			"A halting diagnostic opens with the category the failure is attributable to and the " +
			"condition's short code — the letter E and four digits, as in [CATALOG_ERROR E0209]. " +
			"That short code is the one to pass here; it is fixed once a condition is published, so " +
			"a code quoted out of an old report still names the same condition.\n\n" +
			"  aggrega explain E0209\n\n" +
			"The descriptive code is accepted too — the hyphenated one naming the condition in " +
			"words, which a structured diagnostic carries as a field of its own. A condition's two codes name the same " +
			"condition and reach different readers, so either is a code you may be holding, and the " +
			"condition above is registered under recipe-values-mapping-requires-source:\n\n" +
			"  aggrega explain recipe-values-mapping-requires-source\n\n" +
			"Every condition is declared inside this binary, so the identifiers and the " +
			"sentence are reported without a network or a cache. The published reference " +
			"supplies the walk-through of the authoring fix for every condition, and nothing " +
			"else is resolved against it. It is read from the " +
			"documentation bundle published for this version of aggrega on the default registry — or, " +
			"where none is published for it, the newest one published for an earlier version — kept in " +
			"the cache every project on this machine shares, acquired on " +
			"first use when that cache holds no copy this workspace's signature check admits. " +
			"Where it cannot be acquired, the condition is still reported and the " +
			"reason the walk-through is missing is stated.\n\n" +
			"A number that named a condition and no longer does is reported as retired, with what it " +
			"named, so a code quoted from an old report is never answered with a different condition.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runExplain(cmd.Context(), cmd.OutOrStdout(), cmd.ErrOrStderr(), args[0])
		},
	}
}

// runExplain writes the explanation for code to out, and whatever could not be
// expanded to notes.
//
// The condition is written before whatever the composition refused, so an
// unreachable bundle costs the walk-through rather than the whole reply, and a
// page that resolved a code and then could not be read still reports the
// condition it resolved.
func runExplain(ctx context.Context, out, notes io.Writer, code string) error {
	explanation, err := explainops.Compose(code, func() (explainops.Pages, error) {
		return explainPages(ctx, notes)
	})
	if werr := explainops.Write(out, notes, explanation); werr != nil {
		return werr
	}
	return err
}

// explainPages reads the published pages for the workspace this invocation
// resolved, reporting acquisition warnings on notes.
//
// It is here rather than in the shared composition because resolving the
// workspace is this surface's own act: the command line discovers a workspace per
// invocation, where a session opened over one already holds it.
func explainPages(ctx context.Context, notes io.Writer) (explainops.Pages, error) {
	ws, err := workspaceFromContext(ctx)
	if err != nil {
		return explainops.Pages{}, fmt.Errorf("the published documentation is not available: %w", err)
	}
	warn := func(notice string) { fmt.Fprintf(notes, "warning: %s\n", notice) }
	return explainops.LoadPages(ctx, ws.Config(), provenanceCompilerVersion(), warn)
}
