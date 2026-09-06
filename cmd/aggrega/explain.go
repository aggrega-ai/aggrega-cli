// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/aggrega-ai/aggrega-cli/internal/docsbundle"
	"github.com/aggrega-ai/aggrega-cli/internal/haltref"
	"github.com/aggrega-ai/aggrega-cli/pkg/docscache"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
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
// documentation bundle this workspace caches.
//
// A code that resolves therefore always produces an answer, and the two ways the
// prose can be missing are reported as themselves: a condition the published page
// does not document yet, and a bundle this workspace has no copy of.
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
			"The descriptive code is accepted too — the qualified, hyphenated one a structured " +
			"diagnostic carries as a field of its own. A condition's two codes name the same " +
			"condition and reach different readers, so either is a code you may be holding, and the " +
			"condition above is registered under validator/recipe-values-mapping-requires-source:\n\n" +
			"  aggrega explain validator/recipe-values-mapping-requires-source\n\n" +
			"Most conditions are declared inside this binary, and for those the identifiers and the " +
			"sentence are reported without a network or a cache. The rest — the conditions a provider " +
			"declares for itself — are resolved against the published reference, as is the " +
			"walk-through of the authoring fix for every condition. Both are read from the " +
			"documentation bundle cached for this workspace, acquired on first use when it is absent. " +
			"Where it cannot be acquired, a condition this binary declares is still reported and the " +
			"reason the rest is missing is stated.\n\n" +
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
// Two conditions have to be met before anything is written, and they are reached
// in the order that costs the least. The compiler's own declarations answer first
// and answer offline; the published reference answers for the rest, which are the
// conditions a provider guest declares and the host that loads it therefore never
// registers. Only once neither resolves is the code refused.
//
// The condition is then written before the walk-through, so an unreachable bundle
// costs the walk-through rather than the whole reply.
//
// Three shapes report failure. A code neither side resolves is refused as a code
// that names nothing, which the reply may say outright because both sides were
// searched. A code this binary does not declare, where the reference could not be
// searched at all, is refused as undetermined instead — the reply states what it
// measured and leaves both repairs standing. And a number that named a condition
// once is refused with what it named, because what a reader needs is that the
// number is spent.
func runExplain(ctx context.Context, out, notes io.Writer, code string) error {
	condition, declaredErr := haltref.Resolve(code)

	// A retired number is answered without reading anything. It is absent from
	// the published reference by construction — the reference refuses to carry a
	// number that is both retired and registered — so consulting it could only
	// spend a round trip to learn what the retirement record already said.
	var retired *haltref.RetiredCodeError
	if errors.As(declaredErr, &retired) {
		return declaredErr
	}

	page, pageErr := haltPage(ctx, notes)

	if declaredErr != nil {
		if pageErr != nil {
			// Nothing here declares the code and the published reference could not
			// be consulted, so this cannot tell a code that names nothing from one
			// a guest declares. Both halves are reported, because the repair
			// differs: one is a correction to the code, the other is getting the
			// documentation.
			return fmt.Errorf("%w\n\n%v", withoutTheReference(declaredErr), pageErr)
		}
		published, perr := haltref.ResolveIn(page.reference, code)
		if perr != nil {
			var unknown *haltref.UnknownCodeError
			if errors.As(perr, &unknown) {
				// The reference was read and carries no such code. Every place a
				// condition is declared has now been searched, so the refusal is
				// entitled to the definitive negative it states.
				return declaredErr
			}
			// The reference arrived and could not be read as the page it is, which
			// leaves the code as undetermined as an unreachable bundle does. Both
			// halves are reported for the reason the branch above reports both.
			return fmt.Errorf("%w\n\n%v", withoutTheReference(declaredErr), perr)
		}
		condition = published
	}

	if err := writeCondition(out, condition); err != nil {
		return err
	}
	if pageErr != nil {
		return note(notes, pageErr)
	}

	entries, err := haltref.Entries(page.halts, condition.ShortCode)
	if err != nil {
		// A condition the page does not document is an ordinary fact about the
		// published reference and leaves the reply above standing. Anything else
		// from this read means the page is not the one it resolves against, which
		// is a defect rather than an answer.
		var undocumented *haltref.UndocumentedConditionError
		if errors.As(err, &undocumented) {
			return note(notes, err)
		}
		return err
	}
	for _, e := range entries {
		if _, werr := fmt.Fprintf(out, "\n%s\n", e.Body); werr != nil {
			return werr
		}
	}
	return nil
}

// withoutTheReference narrows a refusal to what was actually measured, for the
// paths where the published reference could not be consulted.
//
// The narrowing is the whole point. A code missing from this binary's own
// declarations is not thereby a code that names nothing: a provider's conditions
// are declared inside the guest that raises them, so the host registers none of
// them, and only the reference tells a mistyped code from one of those. Carrying
// the definitive negative into this path would assert something no read
// established, and it would assert it hardest against a provider author: a
// provider's conditions are precisely the ones the host cannot see for itself.
//
// A refusal of any other kind passes through unchanged, because it already
// reports what it measured.
func withoutTheReference(declaredErr error) error {
	var unknown *haltref.UnknownCodeError
	if errors.As(declaredErr, &unknown) {
		return &haltref.UnresolvedCodeError{Code: unknown.Code}
	}
	return declaredErr
}

// writeCondition writes the part of the answer the binary declares: the two codes
// the condition carries and the sentence a diagnostic naming it opens with.
func writeCondition(out io.Writer, c cerrors.Condition) error {
	_, err := fmt.Fprintf(out, "%s  %s\n\nA diagnostic naming this condition opens with:\n  %s\n",
		c.ShortCode, c.Code, c.Message)
	return err
}

// note writes a reason the walk-through is missing, and reports success: the
// reply above it is the answer, and the reason is a note about what could not be
// added to it.
func note(notes io.Writer, reason error) error {
	_, err := fmt.Fprintf(notes, "\n%s\n", reason)
	return err
}

// explainPages are the two published pages an explanation is read from: the one
// that resolves every declared condition, and the one that documents the
// authoring fix for the conditions it covers.
//
// They travel together because they come from one bundle and one read: fetching
// them separately would let a reply resolve a code against one version of the
// documentation and take its prose from another.
type explainPages struct {
	reference []byte
	halts     []byte
}

// haltPage returns the published pages for this workspace, acquiring the
// documentation bundle when the workspace has no copy.
//
// Acquisition happens here rather than at process start because this is the only
// command that needs the bundle: a fetch wired earlier would put a registry
// round-trip in front of every other command. The reads that follow it are
// offline by construction — they take no target — so a warm cache reaches for
// nothing.
//
// Every failure it reports names the artifact and the version it looked for, so a
// reader who cannot get the documentation is told what was missing rather than
// that something was not found.
func haltPage(ctx context.Context, notes io.Writer) (explainPages, error) {
	ws, err := workspaceFromContext(ctx)
	if err != nil {
		return explainPages{}, fmt.Errorf("the published documentation is not available: %w", err)
	}
	cfg := ws.Config()
	root := ws.Root()
	cacheDir := cfg.ResolvedArtifactCacheDir()
	ref := docsbundle.Ref(cfg.DefaultRegistry)
	version := provenanceCompilerVersion()

	warn := func(notice string) { fmt.Fprintf(notes, "warning: %s\n", notice) }
	if _, aerr := docsbundle.EnsureCached(ctx, cfg, root, cacheDir, ref, version, nil, warn); aerr != nil {
		return explainPages{}, fmt.Errorf("the published documentation is not available: the documentation "+
			"bundle %s@%s is not cached for this workspace and could not be acquired (%v). The conditions "+
			"this binary declares are still answered; every condition, including the ones a provider "+
			"declares, is published at %s, and the walk-through of each authoring fix at %s",
			ref, version, aerr,
			haltref.PublishedPath(haltref.ReferencePagePath), haltref.PublishedPath(haltref.PagePath))
	}

	var pages explainPages
	for _, p := range []struct {
		path string
		into *[]byte
	}{
		{haltref.ReferencePagePath, &pages.reference},
		{haltref.PagePath, &pages.halts},
	} {
		data, _, rerr := docscache.ReadPage(root, cacheDir, ref, version, p.path)
		if rerr != nil {
			return explainPages{}, fmt.Errorf("the published documentation is not available: the "+
				"documentation bundle %s@%s carries no readable %s (%v)", ref, version, p.path, rerr)
		}
		*p.into = data
	}
	return pages, nil
}
