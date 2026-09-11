// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package compilerops

import (
	"github.com/aggrega-ai/aggrega-cli/internal/wasm"
	"github.com/aggrega-ai/aggrega-cli/pkg/externalinputs"
	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// The seam this package builds is the recorder over the store, so it owes
// the loader's fetch contract; the assertion holds the two packages' contracts
// together at compile time.
var _ wasm.FetchService = (*externalinputs.DeclineRecorder)(nil)

// PinnedExternalInputs builds the external-input seam a surface renders through
// when it must produce the bytes a build produces without acquiring anything,
// over the workspace artifact cache's inputs store and seeded with the Release
// lockfile's pinned view.
//
// It is named for what the render sees rather than for what the service may do:
// a render under it sees exactly what the lockfile pins, and acquisition is off
// so it cannot come to see more. Whether the store copy of a pinned reference is
// already on disk or has to be refetched by its recorded digest is invisible to
// the render, because the digest is checked before any byte is returned — so the
// repair changes what the store holds and never what the render reads.
//
// The workspace root travels with the cache directory because the store is
// anchored to the workspace rather than to the caller's working directory, and
// every surface here already holds the root it renders against. Passing the pair
// is what makes all three surfaces read one store: a surface that passed only
// the configured directory would render against whichever store happened to sit
// beneath where it was launched.
//
// The workspace configuration travels with them for one reason: it is what the
// network-egress gate reads to decide whether this workspace may reach the
// network at all. Passing it is therefore not a widening of what a render may
// consume — acquisition stays off — but the thing that lets an offline workspace
// arrive here as a refusal from the one place that decision is made.
//
// It lives beside the computation every rendering surface calls rather than
// inside any one of them. That placement is the point: the seam a rendering
// surface attaches is one description, so two surfaces cannot come to hold two
// copies of it that agree today and drift later.
//
// The posture itself is not described here. The store package composes it, so
// what acquisition, transport and the pinned view are set to is decided once,
// for every surface that renders under it, rather than restated at each place a
// service is built.
//
// Returning the recorder rather than the service is deliberate — the caller
// needs to know which references were declined in order to say so, and the
// service does not keep that list.
func PinnedExternalInputs(workspaceRoot, cacheDir string, wsCfg workspaceconfig.Config, lf *lockfile.Lockfile) *externalinputs.DeclineRecorder {
	return externalinputs.NewDeclineRecorder(externalinputs.NewRefilling(workspaceRoot, cacheDir, wsCfg, lf))
}
