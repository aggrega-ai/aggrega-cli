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
// inside any one of them. That placement is the point: the frozen posture is one
// description, so two surfaces cannot come to hold two copies of it that agree
// today and drift later.
//
// Acquisition is OFF, and that is the whole of the doctrine here: a pinned
// reference is served from the content-addressed store, an unpinned one refuses
// loudly, and nothing is written. Serving a pinned reference from a local
// content-addressed store is a read of content already on disk, not a fetch, so
// rendering the same bytes a build renders and never reaching the network at
// build time are not in tension — the store read is what satisfies both at once.
//
// The transport client is nil for the same reason acquisition is off. With no
// client, a fetch of any kind refuses rather than proceeding, so the seam cannot
// reach the network even if a later edit turned acquisition on by mistake: the
// two are independent switches and both are closed here.
//
// Returning the recorder rather than the service is deliberate — the caller
// needs to know which references were declined in order to say so, and the
// service does not keep that list.
func FrozenExternalInputs(cacheDir string, lf *lockfile.Lockfile) *externalinputs.DeclineRecorder {
	var pinned []lockfile.ExternalInput
	if lf != nil {
		pinned = lf.ExternalInputs
	}
	return externalinputs.NewDeclineRecorder(
		externalinputs.NewService(externalinputs.StoreRoot(cacheDir), false, nil, pinned),
	)
}
