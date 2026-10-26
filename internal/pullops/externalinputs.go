// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package pullops

import (
	"context"
	"fmt"
	"io"

	"github.com/aggrega-ai/aggrega-cli/internal/compilerops"
	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/externalinputs"
	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
	"github.com/aggrega-ai/aggrega-cli/pkg/pluginstrust"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// discoverExternalInputs renders the Release with acquisition on and returns the
// external-input pins the lockfile should carry afterwards. trust is the record of
// verdict-role plugin identities as this run will write it, or nil where the
// caller composed none.
//
// # Why this renders instead of reading the documents
//
// A external input is asked for by a provider plugin while it renders, and which
// URLs it asks for follows from the values it is rendering. Nothing in the
// authored documents enumerates them: a values entry that is absent can leave a
// whole subtree of a chart's schema uncompiled, and with it every remote
// reference that subtree would have resolved. So the set is a product of the
// render, and the only way to learn it is to run one.
//
// # Why it renders through the shared computation rather than from the closure
//
// It renders the operation a build renders, which is what makes the pins it
// writes the ones the build will look for. That computation merges each entity's
// inheritance chain before it renders; the resolution above does not, and does
// not need to. A pass that rendered from that closure directly would therefore
// render different values, ask for a different set of URLs, and pin a set the
// build then disagrees with — silently, because both sides would look complete
// and the difference would surface only as a build refusing an input the
// resolution reported as pinned.
//
// # Which pins it is held to
//
// It installs the view's pins from the lockfile it is handed, so the set the
// render verifies against is the set that lockfile records. Doing it here rather
// than leaving the view as the resolution left it is what keeps the two from
// drifting apart: the resolution pins from the closure it walked, while the
// lockfile is what the write records, and only one of the two is read here.
//
// # The build's own drift verify, before the lockfile is written
//
// The render holds the lockfile to the drift verify a build of this Release runs,
// over the closure that build reconstructs, before the caller writes it. The
// resolution above and a build compute what the Release depends on through
// different walks, and a lockfile the one records and the other refuses is a
// lockfile no build can use — so the render refuses it (see
// compilerops.WithComposedLockfile) and the caller writes neither it nor anything
// after it. That refusal is the one render failure the caller does not report and
// write past.
//
// # What it writes
//
// Nothing here writes a file. What it does write is content — each acquired
// input lands in the content-addressed store under its digest — and the records
// travel back to the caller, which folds them into the one lockfile write.
//
// The render's own result travels back with them, whole rather than reduced to
// the records the discovery was after. A caller holding it holds the artifacts,
// the dependency graph, the trace map and the advisories of a render that has
// already happened, so a caller needing any of them has it without rendering the
// same Release a second time. Handing over the whole result rather than a chosen
// projection of it is what keeps that open: a projection settles here, on behalf
// of every caller, which parts of a render a caller is permitted to want.
//
// The records are returned on the failure path too. A render that stopped
// part-way has already fetched, verified and stored everything it asked for
// ahead of the stop, and those pins are correct; discarding them would make the
// next run refetch content this one has already accepted while telling the
// reader nothing extra. The render's result comes back on that path for a
// reason of its own: it carries its own verdict, reporting no completed payload
// for a run that did not finish, so a caller reaching the payload through that
// verdict cannot read a stopped render as a finished one.
//
// ctx is the caller's own, carried into the render rather than replaced by one
// this layer invented. A render is where the fetching happens, so a withdrawal
// that could not reach it would leave the one acquiring step in the whole
// operation unable to be stopped.
func discoverExternalInputs(
	ctx context.Context,
	cfg Config,
	wsCfg workspaceconfig.Config,
	workspaceRoot string,
	cacheDir string,
	view *catalog.LazyLookup,
	lf *lockfile.Lockfile,
	trust *pluginstrust.Trust,
) ([]lockfile.ExternalInput, *compilerops.PreviewResult, error) {
	// The pins this render verifies against are derived from the lockfile handed
	// to this call, in this call, rather than left as whatever the resolution
	// above last installed on the view.
	//
	// The resolution computes its pin set from the closure it walked, before the
	// lockfile handed here was composed, so a view still carrying that set would
	// hold the render to a record the write does not make wherever the two differ.
	// Installing the lockfile's own pins here removes the
	// disagreement by removing the second set: what the render is held to and what
	// the write records are the same object, so no ordering between them exists to
	// get wrong.
	//
	// This also reaches every axis the lockfile pins rather than the catalog
	// entities alone, so a render is held to the same set on every axis the
	// resolution recorded.
	view.SetPins(catalog.PinsFromLockfile(lf))

	// The acquisition posture, named rather than assembled: whether an unpinned
	// reference may be fetched, what transports it, and where the pinned view is
	// read from are settled once, in the store package, for every surface that
	// acquires. An offline workspace arrives here as a refusal from the
	// network-egress gate, which leaves the service able to serve what is pinned
	// and unable to fetch what is not — so offline needs no separate consult and
	// cannot be answered differently here than it is at a build.
	svc := externalinputs.NewAcquiring(workspaceRoot, cacheDir, wsCfg, lf)

	preview, err := compilerops.PreviewForArtifact(
		ctx,
		compilerops.NewPreviewInputs(
			svc,
			view,
			cfg.ReleasePath,
			workspaceRoot,
			wsCfg,
			cfg.CompilerVersion,
			// The lockfile goes in as the one this run composed and has not yet
			// written: the source of the render's pins, and the subject of the
			// drift verify a build of this Release will run. The two were computed
			// by different walks — this run's resolution and the build's own
			// reconstruction — so their agreement is checked here rather than
			// assumed, and a disagreement refuses the render with the composed-
			// lockfile refusal, which the caller treats as fatal and writes nothing
			// on. That check is what lets a build that filled its own lockfile
			// through this operation skip the verify afterwards.
			compilerops.WithComposedLockfile(lf),
			// The record of verdict-role plugin identities goes in the same way, as
			// this run composed it: the render holds each such plugin to the
			// signature fact this run verified, and this run writes the record only
			// after the render.
			compilerops.WithComposedTrustRecord(trust),
			// The pass that drew the graph this run pins resolved the same Release
			// before this render and showed the reader every advisory that
			// resolution raises, so the render's own resolution records them and
			// shows none again. The render still records them on its result, so a
			// surface reading that result reads the same list it would without this.
			compilerops.WithResolutionAdvisoriesShown(),
		),
		cfg.RenderOptions,
	)
	return svc.Records(), preview, err
}

// reportExternalInputDiscoveryFailure writes the diagnostic a reader needs when
// the discovery render could not finish: which stage of the resolution stopped,
// what is on disk regardless, and what remains unknown.
//
// Naming the stage is the load-bearing part. Without it a reader sees a render
// failure from a command they ran to resolve dependencies and cannot tell
// whether they now have a lockfile, a partial one, or nothing — and the answer
// is that the dependency pins are complete and only the external-input pins may
// not be.
//
// The whole cause chain is written rather than the outermost message, and that
// is not verbosity. A render failure arrives here wrapped by the node that was
// rendering, and the outermost wrapper alone says which node stopped without
// saying why — which for the case a reader will most often meet, a workspace
// with no reachable network, leaves the one fact that explains everything
// unstated.
//
// reportOnly changes what the middle sentence can claim, and it is a parameter
// rather than a fixed sentence because the claim would otherwise be false half
// the time: a run that reported rather than wrote has written no pins at all, so
// telling its reader that the dependency pins "were written" would name an act
// that did not happen and a file that did not move.
func reportExternalInputDiscoveryFailure(out io.Writer, lockfilePath string, pinned int, reportOnly bool, err error) {
	if out == nil {
		return
	}
	fmt.Fprintf(out, "  external inputs: could not finish rendering %s to discover them: %s\n", lockfilePath, cerrors.CauseChain(err))
	if reportOnly {
		fmt.Fprintf(out, "  external inputs: the reported lockfile's dependency pins are complete; it carries %d external input(s), and any this Release also needs are absent from it\n", pinned)
		fmt.Fprintf(out, "  external inputs: so the reported lockfile is short of what a pull would write on this one axis. A pull rewrites the recorded digests in each Recipe before it renders, and a report leaves those files alone — so a render held to a digest an unwritten file does not yet carry stops here. Resolving for real pins what this render could not reach\n")
		return
	}
	fmt.Fprintf(out, "  external inputs: dependency pins were written and are complete; %d external input(s) pinned, and any this Release also needs are still unpinned\n", pinned)
	fmt.Fprintf(out, "  external inputs: a build of this Release halts naming the unpinned URL; fixing what stopped the render and resolving again pins it\n")
}
