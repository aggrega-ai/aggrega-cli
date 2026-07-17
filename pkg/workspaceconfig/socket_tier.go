// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package workspaceconfig

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

// Tier reports the reproducibility tier of a socket. It is the single home of the
// socket → tier mapping, so pull (which caches verdict-socket plugins) and build
// (which resolves them cache-not-pin) can never disagree on which sockets are
// verdict sockets.
func (s Socket) Tier() SocketTier {
	switch s {
	case SocketProvider, SocketMutator:
		return SocketTierMutating
	case SocketPolicy, SocketLinter:
		return SocketTierVerdict
	default:
		return SocketTierOrthogonal
	}
}

// PluginSocket pairs a workspace-level plugin socket with its effective (merged)
// attachment list.
type PluginSocket struct {
	Socket Socket
	Refs   []PluginRef
}

// PluginSockets enumerates the config's workspace-level plugin sockets with their
// socket, in a fixed order, reading the effective (base + append + overlay
// merged) attachment lists the pipeline consumes. It is the single home of the
// socket → attachment-list mapping, so a caller iterates sockets by tier without
// re-listing which field backs each socket.
func (c Config) PluginSockets() []PluginSocket {
	return []PluginSocket{
		{SocketProvider, c.Plugins.Providers},
		{SocketMutator, c.Plugins.Mutator},
		{SocketPolicy, c.Plugins.Policy},
		{SocketLinter, c.Plugins.Linter},
		{SocketImporter, c.Plugins.Importer},
	}
}
