// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package resolver

import (
	"fmt"
	"path/filepath"

	"github.com/aggrega-ai/aggrega-cli/pkg/closure"
)

// ReleaseDocuments returns every Release file a composition of child reads — child
// itself, then each Release along its extends chain, in the order the chain is
// walked — each recorded by the content digest of its bytes.
//
// It walks the chain through [ReleaseExtendsChain], the walk the composition runs,
// so the files it records and the files a composition folds are one set rather
// than two lists that agree today. It therefore fails exactly where
// [ResolveReleaseExtends] would, with the same failure.
//
// Each digest comes from the read the document was decoded from: child's from the
// caller's [LoadResolvedRelease], each parent's from the chain walk's own load. A
// child assembled from a Release already in hand carries no digest, and is refused
// rather than recorded with an empty one, because an empty digest would compare
// equal to nothing and report an unchanged file as edited.
//
// A path is written relative to releaseDir with forward slashes, so a lockfile
// written on one machine records the same value as one written on another whatever
// their directory layouts. releaseDir is the directory holding child, and with
// workspaceRoot it decides the root the chain's references are held to, as the
// composition holds them; each reference is resolved beside the link that writes
// it.
func ReleaseDocuments(workspaceRoot, releaseDir string, child ResolvedRelease) ([]closure.ReleaseDocument, error) {
	if child.Release == nil {
		return nil, nil
	}
	if child.Digest == "" {
		return nil, fmt.Errorf("internal: the Release at %s was not loaded with the digest of its bytes; "+
			"load it with LoadResolvedRelease", child.Path)
	}
	chain, err := ReleaseExtendsChain(workspaceRoot, releaseDir, child)
	if err != nil {
		return nil, err
	}
	out := make([]closure.ReleaseDocument, 0, len(chain))
	for _, link := range chain {
		rel, err := filepath.Rel(releaseDir, link.Path)
		if err != nil {
			return nil, fmt.Errorf("internal: Release %s does not resolve relative to %s: %w", link.Path, releaseDir, err)
		}
		out = append(out, closure.ReleaseDocument{Path: filepath.ToSlash(rel), Digest: link.Digest})
	}
	return out, nil
}
