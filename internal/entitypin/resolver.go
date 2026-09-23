// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package entitypin

import (
	"context"

	"github.com/aggrega-ai/aggrega-cli/pkg/closure"
	"github.com/aggrega-ai/aggrega-cli/pkg/egress"
	"github.com/aggrega-ai/aggrega-cli/pkg/sourceresolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// SourceResolver returns the production source resolver: the closure-package
// resolution contract, answered by the shipped per-kind adapters.
//
// It lives here because the pin step is the one caller every resolving surface
// shares — the author's own step, and a dependency resolution that pins or
// checks pins — so a single home here is reachable from each of them, where one
// inside the dependency resolution would not be reachable from the step at all.
//
// It is the single production seam where source resolution crosses the
// network-egress gate: it derives the HTTP client from the resolved workspace
// configuration and refuses an offline workspace loudly, because acquiring a
// source is a primary fetch verb. A source the workspace already holds never
// reaches it — the descent skips such a source before resolution — so a
// workspace holding only local sources is unaffected by the offline posture here.
//
// ctx is the caller's, and it rides on the returned value because the contract it
// satisfies takes no context: each resolution runs under it, so ending the run
// ends the download in progress. The value is built for one run and discarded,
// and never outlives the run whose context it holds.
func SourceResolver(ctx context.Context, wsCfg workspaceconfig.Config) closure.SourceResolver {
	return sourceResolverShim{ctx: ctx, wsCfg: wsCfg}
}

// sourceResolverShim adapts the closure-package SourceResolver interface to the
// production pkg/sourceresolver dispatcher. The two packages declare identical
// SourceSpec / ResolvedSource shapes; the shim does a mechanical field copy at
// each call boundary, because the two are deliberately separate surfaces.
type sourceResolverShim struct {
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
		ModuleSubdir:   rs.ModuleSubdir,
		PinnedVersion:  rs.PinnedVersion,
		Digest:         rs.Digest,
		ManifestDigest: rs.ManifestDigest,
		Mediatype:      rs.Mediatype,
		ResolvedURL:    rs.ResolvedURL,
		CachePath:      rs.CachePath,
		OriginalSource: rs.OriginalSource,
	}, nil
}
