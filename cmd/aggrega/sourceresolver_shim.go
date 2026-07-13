// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"github.com/aggrega-ai/aggrega-cli/pkg/closure"
	"github.com/aggrega-ai/aggrega-cli/pkg/sourceresolver"
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
type sourceResolverShim struct{}

func (sourceResolverShim) Resolve(spec closure.SourceSpec) (*closure.ResolvedSource, error) {
	rs, err := sourceresolver.Resolve(sourceresolver.SourceSpec{
		Kind:           spec.Kind,
		URL:            spec.URL,
		Version:        spec.Version,
		Chart:          spec.Chart,
		ModuleSubdir:   spec.ModuleSubdir,
		Ref:            spec.Ref,
		WorkspaceRoot:  spec.WorkspaceRoot,
		CacheDir:       spec.CacheDir,
		OriginalSource: spec.OriginalSource,
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
