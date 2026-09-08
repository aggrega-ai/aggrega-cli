// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package pullops

import (
	"context"
	"fmt"
	"io"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/pipeline"
	"github.com/aggrega-ai/aggrega-cli/internal/compilerops"
	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/externalinputs"
	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// discoverExternalInputs renders the Release with acquisition on and returns the
// external-input pins the lockfile should carry afterwards.
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
// # What it writes
//
// The rendered artifacts are returned in memory and dropped: nothing here writes
// a file. What it does write is content — each acquired input lands in the
// content-addressed store under its digest — and the records travel back to the
// caller, which folds them into the one lockfile write.
//
// The records are returned on the failure path too. A render that stopped
// part-way has already fetched, verified and stored everything it asked for
// ahead of the stop, and those pins are correct; discarding them would make the
// next run refetch content this one has already accepted while telling the
// reader nothing extra.
func discoverExternalInputs(
	cfg Config,
	wsCfg workspaceconfig.Config,
	workspaceRoot string,
	cacheDir string,
	view *catalog.LazyLookup,
	lf *lockfile.Lockfile,
) ([]lockfile.ExternalInput, error) {
	// The acquisition posture, named rather than assembled: whether an unpinned
	// reference may be fetched, what transports it, and where the pinned view is
	// read from are settled once, in the store package, for every surface that
	// acquires. An offline workspace arrives here as a refusal from the
	// network-egress gate, which leaves the service able to serve what is pinned
	// and unable to fetch what is not — so offline needs no separate consult and
	// cannot be answered differently here than it is at a build.
	svc := externalinputs.NewAcquiring(workspaceRoot, cacheDir, wsCfg, lf)

	_, err := compilerops.PreviewForArtifact(
		context.Background(),
		compilerops.NewPreviewInputs(
			svc,
			view,
			cfg.ReleasePath,
			workspaceRoot,
			wsCfg,
			cfg.CompilerVersion,
			// The lockfile goes in as a source of pins, not as something to be
			// held to: it was assembled from the closure this run has just
			// computed rather than read off disk, so a drift verify would compare
			// that closure against itself. Naming it as not pre-existing is what
			// says so, and it is the truthful answer rather than a way around the
			// gate.
			compilerops.WithLockfile(lf, false),
		),
		pipeline.Options{},
	)
	return svc.Records(), err
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
func reportExternalInputDiscoveryFailure(out io.Writer, lockfilePath string, pinned int, err error) {
	if out == nil {
		return
	}
	fmt.Fprintf(out, "  external inputs: could not finish rendering %s to discover them: %s\n", lockfilePath, cerrors.CauseChain(err))
	fmt.Fprintf(out, "  external inputs: dependency pins were written and are complete; %d external input(s) pinned, and any this Release also needs are still unpinned\n", pinned)
	fmt.Fprintf(out, "  external inputs: a build of this Release halts naming the unpinned URL; fixing what stopped the render and resolving again pins it\n")
}
