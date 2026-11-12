// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package pluginops

import (
	"context"

	"github.com/aggrega-ai/aggrega-cli/internal/wasm"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// The shape a plugin enumeration is reported in, and the seam a surface that
// cannot assemble one reaches it through.
//
// Only the SHAPE lives here. The enumeration itself — which URIs are looked at,
// how each is resolved, which descriptor cache serves it — stays in the one
// place that can build the resolution cascade, because that cascade's inputs are
// derived from the command tree and the workspace together. A second
// enumeration written against this type would agree with the first on the day it
// was written, and the agreement is what would hide the first divergence.

// ListedPlugin is one plugin's enumeration result: which group it was found in,
// the reference it was found under, where that reference was bound, and either
// the guest's own validated self-report or the error that producing it raised.
//
// Err is carried rather than returned, so one unresolvable plugin reports itself
// and the listing goes on. A caller renders it, reports it, or keeps it back;
// none of that is decided here.
type ListedPlugin struct {
	// Source is which tier this plugin was enumerated from — the embedded
	// built-ins, a workspace socket binding, or an installed command extension.
	// It is the enumeration's own discriminator: an embedded built-in has a
	// descriptor extracted at build time and is never loaded to be described.
	Source string

	// URI is the reference the plugin was enumerated under, as the resolved
	// configuration carries it. That is not always as an author typed it: the
	// cascade anchors a relative reference on some sockets against the file that
	// declared it, so a reference that named no path can reach here as one.
	URI string

	// Origin is where the reference was bound, in the vocabulary of whatever
	// bound it: the config keys a socket binding was written at, the pinned
	// digest an installed command extension carries, or nothing for a built-in.
	Origin string

	// Descriptor is the guest's validated self-report, zero when Err is set.
	Descriptor wasm.PluginDescriptor

	// ResolvedDigest is the content digest of the bytes the reference resolved
	// to, empty when resolution itself failed. It is what tells a resolve
	// failure from a describe failure, and what an installed pin is compared
	// against.
	ResolvedDigest string

	// Readiness is an installed command extension's one-word state. It is empty
	// for the other two tiers, which carry no pin to drift from.
	Readiness string

	// Err is the resolve or describe failure this plugin raised, nil when it
	// resolved and described.
	Err error
}

// Resolved reports whether this plugin's reference resolved to bytes.
//
// It is read off ResolvedDigest rather than off Err, because the two failures
// are different answers to a caller: a reference that resolved and then failed
// to describe is present and unusable, while one that did not resolve is not
// there at all. Deriving it keeps the distinction from being restated per
// surface as a second test that could disagree.
func (p ListedPlugin) Resolved() bool { return p.ResolvedDigest != "" }

// PluginListing is one enumeration's two groups, each already in its own
// deterministic order.
//
// The grouping is the structure rather than a label on each entry, so a surface
// reporting the two groups reads them apart without re-deriving which group an
// entry belongs to — a derivation that would be a second copy of the rule that
// an installed command extension is listed among the configured plugins.
type PluginListing struct {
	Embedded   []ListedPlugin
	Configured []ListedPlugin
}

// PluginLister enumerates the plugins resolvable for a workspace.
//
// It is a seam rather than a function this package implements, for the reason
// stated in the package documentation: the enumeration needs the plugin
// resolution cascade, whose inputs are derived in the command layer, so a
// surface that is not the command line takes the enumeration as something handed
// to it. A nil workspace enumerates the embedded built-ins alone, which is what
// a caller with no workspace resolved can honestly be told.
type PluginLister func(ctx context.Context, rw *workspaceconfig.ResolvedWorkspace) PluginListing
