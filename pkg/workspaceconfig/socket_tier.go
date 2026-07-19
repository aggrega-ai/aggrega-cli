// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package workspaceconfig

import "github.com/aggrega-ai/aggrega-cli/pkg/plugintype"

// SocketTier classifies a plugin socket by how its plugin participates in a
// build's reproducibility contract. The tier is a pure function of the socket
// type — the mechanism that keeps the classification legible and single-sourced,
// never a per-plugin flag.
type SocketTier int

const (
	// SocketTierMutating: the socket's plugin shapes the rendered output bytes,
	// so its plugin is pinned in the lockfile and drift-gated — a change to it is
	// adopted only on the next pull, keeping build output reproducible.
	SocketTierMutating SocketTier = iota
	// SocketTierVerdict: the socket's plugin renders an accept/reject verdict
	// without shaping output bytes, so its plugin is cached at pull (so a hermetic
	// build can run it) but not pinned and not drift-gated (cache-not-pin).
	SocketTierVerdict
	// SocketTierOrthogonal: the socket's plugin acts on authoring input and never
	// touches the rendered output, so it is neither cached in the build closure
	// nor pinned.
	SocketTierOrthogonal
)

// Tier reports the reproducibility tier of a socket, reading the classification
// from the canonical plugin-type registry — the single home of the tier
// classification — and translating the registry tier into the
// workspaceconfig-facing SocketTier. Because the classification lives in one
// place, pull (which caches verdict-socket plugins) and build (which resolves
// them cache-not-pin) can never disagree on which sockets are verdict sockets. A
// socket with no registry entry falls back to the orthogonal tier.
func (s Socket) Tier() SocketTier {
	for _, e := range plugintype.Entries() {
		if e.Socket != string(s) {
			continue
		}
		switch e.Tier {
		case plugintype.TierMutating:
			return SocketTierMutating
		case plugintype.TierVerdict:
			return SocketTierVerdict
		case plugintype.TierOrthogonal:
			return SocketTierOrthogonal
		}
	}
	return SocketTierOrthogonal
}

// PluginSocket pairs a workspace-level plugin socket with its effective (merged)
// attachment list.
type PluginSocket struct {
	Socket Socket
	Refs   []PluginRef
}

// PluginSockets enumerates the config's workspace-level plugin sockets with their
// effective (base + append + overlay merged) attachment lists, in a fixed order
// the compilation pipeline's deterministic iteration depends on. It is the single
// home of the socket → attachment-list mapping, so a caller iterates sockets by
// tier without re-listing which field backs each socket.
//
// The socket-name vocabulary is registry-sourced; only the inherent socket → Config
// field binding is local here, because the struct's named fields cannot come from
// the registry. The socket set matches the plugin-type registry's socket-attached
// subset, a correspondence held by a guard test rather than by registry iteration.
func (c Config) PluginSockets() []PluginSocket {
	return []PluginSocket{
		{SocketProvider, c.Plugins.Providers},
		{SocketMutator, c.Plugins.Mutator},
		{SocketPolicy, c.Plugins.Policy},
		{SocketLinter, c.Plugins.Linter},
		{SocketImporter, c.Plugins.Importer},
	}
}
