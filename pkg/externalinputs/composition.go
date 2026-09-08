// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package externalinputs

import (
	"github.com/aggrega-ai/aggrega-cli/pkg/egress"
	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// NewFrozen constructs the service for the consumption moment: a pinned
// reference is served from the workspace's content-addressed store, and an
// unpinned one refuses loudly. Nothing is fetched and nothing is written.
//
// Serving a pinned reference from a local content-addressed store is a read of
// content already on disk, not a fetch, so producing the same bytes an
// acquiring run produces and never reaching the network are not in tension —
// the store read is what satisfies both at once.
//
// It takes no workspace configuration, and that absence is the guarantee rather
// than an omission: with no configuration there is nothing from which to build a
// transport, so this posture cannot reach the network by construction. A later
// edit cannot turn fetching on here without first widening the signature, which
// is a change a reader sees.
//
// The store is located from the workspace root and the workspace's configured
// artifact cache directory together, which is what makes it the same store for
// one workspace whatever directory the caller is standing in.
func NewFrozen(workspaceRoot, cacheDir string, lf *lockfile.Lockfile) *Service {
	return NewService(StoreRoot(workspaceRoot, cacheDir), false, nil, pinnedFrom(lf))
}

// NewAcquiring constructs the service for the acquisition moment: a pinned
// reference is served from the workspace's content-addressed store, and an
// unpinned one is fetched, pinned under its digest, and recorded for the
// lockfile.
//
// The transport comes from the network-egress gate, which refuses at
// construction rather than at the dial. A refusal of any kind leaves the service
// with no client, so pinned references are still served from the store while any
// fetch — an acquisition or a missing-blob refill — refuses loudly. That is the
// safe direction whatever the gate refused on: the closure can only ever be read
// from what is already pinned, never silently widened. An offline workspace is
// the refusal a reader will meet, and it means offline dominates acquisition
// mechanically, without a separate consult.
//
// The store is located the same way the consumption posture locates it, from the
// workspace root and the configured cache directory together — so what a run
// acquires into is the store a later build reads from, whatever directory either
// was run from.
func NewAcquiring(workspaceRoot, cacheDir string, wsCfg workspaceconfig.Config, lf *lockfile.Lockfile) *Service {
	client, _ := egress.HTTPClient(wsCfg)
	return NewService(StoreRoot(workspaceRoot, cacheDir), true, client, pinnedFrom(lf))
}

// pinnedFrom reads the pinned view out of a lockfile, tolerating the absence of
// one. A caller with no lockfile has nothing pinned, which is what an empty view
// already expresses, so the two answers are the same and neither caller needs to
// know that.
func pinnedFrom(lf *lockfile.Lockfile) []lockfile.ExternalInput {
	if lf == nil {
		return nil
	}
	return lf.ExternalInputs
}
