// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package pullops

import (
	"context"

	"github.com/aggrega-ai/aggrega-cli/pkg/closure"
	"github.com/aggrega-ai/aggrega-cli/pkg/egress"
	"github.com/aggrega-ai/aggrega-cli/pkg/sourceresolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// sourceResolverShim adapts the closure-package SourceResolver
// interface to the production pkg/sourceresolver dispatcher. The two
// packages declare identical SourceSpec / ResolvedSource shapes; the
// shim does a mechanical field copy at each call boundary.
//
// Placement at the command level (cmd/aggrega) rather than in pkg/
// reflects that the shim crosses an intentionally decoupled package
// boundary — closure and sourceresolver are deliberately separate
// surfaces, and the wiring point is the orchestrator the CLI verb
// composes. The build command does NOT use this shim; it goes through
// pkg/sourcecache.LookupAndExtract directly on the build path. The
// shim's only caller is pull.go's DescendRecipeSources invocation.
//
// The shim is the single production seam where lock-time source resolution
// crosses the network-egress gate: it derives the HTTP client from the resolved
// workspace config and refuses an offline workspace loudly (source acquisition is
// a primary fetch verb). Local sources never reach the shim — the descent skips
// them before resolution — so a local-only closure is unaffected by an offline
// workspace here.
//
// It is also where the run's context crosses into the fetch, which is what makes
// the source descent interruptible: each source is resolved under that context,
// so stopping the run stops the download in progress and the descent reports the
// withdrawal rather than working through the sources that remain.
type sourceResolverShim struct {
	// ctx is the run's, and it sits on the shim because the interface the shim
	// satisfies takes no context: the descent calls Resolve per source with a
	// spec and nothing else, so this is the only place the run's context can be
	// carried across to the fetch that needs it. The shim is built inside the
	// function that owns the context, used for that one descent, and discarded —
	// it never outlives the run whose context it holds.
	ctx   context.Context
	wsCfg workspaceconfig.Config
}

func (s sourceResolverShim) Resolve(spec closure.SourceSpec) (*closure.ResolvedSource, error) {
	httpClient, err := egress.HTTPClient(s.wsCfg)
	if err != nil {
		return nil, err
	}
	rs, err := sourceresolver.Resolve(s.ctx, sourceresolver.SourceSpec{
		Kind:           spec.Kind,
		URL:            spec.URL,
		Version:        spec.Version,
		Chart:          spec.Chart,
		ModuleSubdir:   spec.ModuleSubdir,
		Ref:            spec.Ref,
		WorkspaceRoot:  spec.WorkspaceRoot,
		CacheDir:       spec.CacheDir,
		OriginalSource: spec.OriginalSource,
		Client:         httpClient,
	})
	if err != nil {
		return nil, err
	}
	return &closure.ResolvedSource{
		Kind:           rs.Kind,
		URL:            rs.URL,
		Chart:          rs.Chart,
		PinnedVersion:  rs.PinnedVersion,
		Digest:         rs.Digest,
		ManifestDigest: rs.ManifestDigest,
		Mediatype:      rs.Mediatype,
		ResolvedURL:    rs.ResolvedURL,
		CachePath:      rs.CachePath,
		OriginalSource: rs.OriginalSource,
	}, nil
}
