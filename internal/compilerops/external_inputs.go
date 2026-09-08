// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package compilerops

import (
	"github.com/aggrega-ai/aggrega-cli/internal/wasm"
	"github.com/aggrega-ai/aggrega-cli/pkg/externalinputs"
	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
)

// The frozen seam this package builds is the recorder over the store, so it owes
// the loader's fetch contract; the assertion holds the two packages' contracts
// together at compile time.
var _ wasm.FetchService = (*externalinputs.DeclineRecorder)(nil)

// FrozenExternalInputs builds the external-input seam a surface renders through
// when it must produce the bytes a build produces without acquiring anything,
// over the workspace artifact cache's inputs store and seeded with the Release
// lockfile's pinned view.
//
// It lives beside the computation every rendering surface calls rather than
// inside any one of them. That placement is the point: the seam a rendering
// surface attaches is one description, so two surfaces cannot come to hold two
// copies of it that agree today and drift later.
//
// The frozen posture itself is not described here. The store package composes
// it, so what acquisition, transport and the pinned view are set to is decided
// once, for every surface that renders under it, rather than restated at each
// place a service is built. Rendering the same bytes an acquiring run produces
// and never reaching the network at build time are not in tension under that
// posture: a pinned reference is served from a local content-addressed store,
// which is a read of content already on disk rather than a fetch.
//
// Returning the recorder rather than the service is deliberate — the caller
// needs to know which references were declined in order to say so, and the
// service does not keep that list.
func FrozenExternalInputs(cacheDir string, lf *lockfile.Lockfile) *externalinputs.DeclineRecorder {
	return externalinputs.NewDeclineRecorder(externalinputs.NewFrozen(cacheDir, lf))
}
