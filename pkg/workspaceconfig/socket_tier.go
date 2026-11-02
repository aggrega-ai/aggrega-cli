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
//
// The match is on the socket NAME, so a registry entry that attaches by some other
// mechanism and therefore carries no socket name is never a match — not even for an
// empty socket, which names no socket either. Reading such an entry's tier here
// would answer a socket question with a type-keyed answer.
func (s Socket) Tier() SocketTier {
	for _, e := range plugintype.Entries() {
		if e.Socket == "" || e.Socket != string(s) {
			continue
		}
		return socketTierFor(e.Tier)
	}
	return SocketTierOrthogonal
}

// RefTier reports the reproducibility tier of one attachment at this socket.
// It is the per-attachment refinement of Tier: for the governance socket the
// tier is a property of the attachment's role, not of the socket (a
// label-transform attachment shapes output bytes and pins; a verdict attachment
// shapes nothing and does not), so RefTier reads the role through the canonical
// role→tier classification. For every other socket the tier is a pure property
// of the socket and RefTier delegates to Tier. Because the governance role→tier
// classification lives in one place (plugintype.GovernanceRoleTier), pull and
// build read the same tier for the same attachment and can never disagree on
// which attachments are verdict attachments. A governance attachment whose role
// does not resolve falls back to the socket's tier — the conservative pinning
// default that never breaks deterministic output.
func (s Socket) RefTier(ref PluginRef) SocketTier {
	if s == SocketGovernance {
		if tier, ok := plugintype.GovernanceRoleTier(ref.Role); ok {
			return socketTierFor(tier)
		}
	}
	return s.Tier()
}

// socketTierFor translates a registry-level plugin tier into the
// workspaceconfig-facing SocketTier, so the mapping lives in one place.
func socketTierFor(t plugintype.Tier) SocketTier {
	switch t {
	case plugintype.TierMutating:
		return SocketTierMutating
	case plugintype.TierVerdict:
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

// TransformGovernanceRefs returns the governance attachments bound to a
// transform role — the byte-determining label-transform attachments the build
// pins in and reconstructs from the lockfile. It is the pull/walk side of the
// pinned axis: these refs shape output bytes, so their set / order / export
// function is a build-critical input.
func (p PluginsConfig) TransformGovernanceRefs() []PluginRef {
	return p.governanceRefsAtTier(plugintype.TierMutating)
}

// VerdictGovernanceRefs returns the governance attachments bound to a verdict
// role (ir-verdict / render-verdict), in authored order. These shape no output
// and are read live (cache-not-pin), so build composes the lockfile-pinned
// transform set with these live verdict refs rather than overwriting them.
func (p PluginsConfig) VerdictGovernanceRefs() []PluginRef {
	return p.governanceRefsAtTier(plugintype.TierVerdict)
}

// GovernanceRefsForRole returns the governance attachments bound to exactly
// role, in authored order. The dispatch routes by role: each pipeline runner
// reads the attachments for the one role it serves, so the role — not the plugin
// type — determines which runner (and therefore which facet) an attachment feeds.
func (p PluginsConfig) GovernanceRefsForRole(role plugintype.Role) []PluginRef {
	var out []PluginRef
	for _, ref := range p.Governance {
		if ref.Role == role {
			out = append(out, ref)
		}
	}
	return out
}

func (p PluginsConfig) governanceRefsAtTier(t plugintype.Tier) []PluginRef {
	var out []PluginRef
	for _, ref := range p.Governance {
		if tier, ok := plugintype.GovernanceRoleTier(ref.Role); ok && tier == t {
			out = append(out, ref)
		}
	}
	return out
}

// RefsByTier returns this socket's attachments whose per-attachment tier equals
// t, preserving authored order. For a single-tier socket the result is
// all-or-nothing (every attachment shares the socket's tier); for the governance
// socket, whose tier is a property of each attachment's role, it selects exactly
// the matching-tier subset — the label-transform attachments at the mutating tier,
// the verdict attachments at the verdict tier.
func (ps PluginSocket) RefsByTier(t SocketTier) []PluginRef {
	var out []PluginRef
	for _, ref := range ps.Refs {
		if ps.Socket.RefTier(ref) == t {
			out = append(out, ref)
		}
	}
	return out
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
		{SocketGovernance, c.Plugins.Governance},
		{SocketLinter, c.Plugins.Linter},
		{SocketImporter, c.Plugins.Importer},
	}
}

// VerdictAttachment is one verdict attachment together with the socket it is
// attached at: the socket is carried rather than re-derived because a consumer
// that needs to name the attachment's socket must not re-answer the tier
// question to get it.
type VerdictAttachment struct {
	Socket Socket
	Ref    PluginRef
}

// VerdictAttachments returns every attachment this config binds at the verdict
// tier, across every plugin socket, in socket order and then authored order —
// the verdict governance roles (ir-verdict / render-verdict) and the linter
// socket alike, because the verdict tier is a property of the attachment rather
// than of one socket. An attachment carrying no plugin URI is omitted: it has
// nothing to fetch and nothing to identify. In a configuration Resolve produced,
// such an attachment names no entry function either: one at this tier naming a
// function and no URI is refused as the file carrying it is read.
//
// It is the one spelling of "the verdict attachment set", so the pull's plugin
// caching and the recorded provenance of what shaped a verdict read the same
// answer and cannot disagree about which attachments are verdict attachments. A
// consumer deriving the set from one socket's attachment list answers a narrower
// question than the name states, which is why the set is read here rather than
// re-derived.
func (c Config) VerdictAttachments() []VerdictAttachment {
	var out []VerdictAttachment
	for _, ps := range c.PluginSockets() {
		for _, ref := range ps.RefsByTier(SocketTierVerdict) {
			if ref.URI == "" {
				continue
			}
			out = append(out, VerdictAttachment{Socket: ps.Socket, Ref: ref})
		}
	}
	return out
}
