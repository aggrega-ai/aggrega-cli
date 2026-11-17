// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

// Package ociartifact implements the source-resolver adapter of the OCI module
// kinds: one adapter per kind sourcekind.OCIModuleKinds names, each carrying
// that kind's row (the media type of the one layer the kind selects, how the
// layer is laid out, whether the kind takes a module_subdir).
//
// An adapter pulls the artifact a tag or manifest digest names, selects the layer
// by the row's media type, and caches the layer's bytes exactly as published: it
// opens neither a document nor a package beyond checking that a zip package lays
// out, and it archives nothing again. The digest it records is the digest of those
// bytes and the manifest digest is the registry's own, so a build verifies the
// cached layer against the manifest the publisher signed over.
//
// The adapter judges only what a pull alone sees: a version that is neither a tag
// nor a digest, a module_subdir that climbs out of its slot, a module_subdir the
// package lacks, and the registry's answers.
package ociartifact

import (
	"context"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/sourcecache"
	"github.com/aggrega-ai/aggrega-cli/pkg/sourcekind"
	"github.com/aggrega-ai/aggrega-cli/pkg/sourceresolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/sourceresolver/cacheslot"
	"github.com/aggrega-ai/aggrega-cli/pkg/sourceresolver/internal/archive"
	"oras.land/oras-go/v2"
)

// targetFactory builds an OCI target from a repository reference. Production
// wires it to oci.NewRemoteRepository; tests inject a memory store so the adapter
// runs end to end without a network.
type targetFactory func(reference string, httpClient *http.Client) (oras.ReadOnlyTarget, error)

// adapter resolves sources of one OCI module kind.
type adapter struct {
	kind      string
	row       sourcekind.OCIArtifact
	newTarget targetFactory
}

func remoteTarget(reference string, httpClient *http.Client) (oras.ReadOnlyTarget, error) {
	repo, err := oci.NewRemoteRepository(reference, httpClient)
	if err != nil {
		return nil, err
	}
	return repo, nil
}

// Kind returns the source kind the adapter serves.
func (a *adapter) Kind() string { return a.kind }

// Resolve pulls the artifact spec names, caches the layer the kind selects and
// returns the ResolvedSource the lockfile writer records.
//
// spec.URL is the repository (`oci://<registry>/<repository>`) and spec.Version a
// tag or a manifest digest, recorded verbatim as the pinned version.
// spec.ModuleSubdir is read, and recorded, only by a kind that takes one.
//
// ctx reaches the registry pull, so ending it abandons the transfer rather than
// leaving it running behind a caller that has stopped waiting.
func (a *adapter) Resolve(ctx context.Context, spec sourceresolver.SourceSpec) (*sourceresolver.ResolvedSource, error) {
	if err := oci.TagOrDigest(spec.Version); err != nil {
		return nil, sourceresolver.Wrap(spec, sourceresolver.ErrFetchNotFound(), err)
	}
	moduleSubdir := ""
	if a.row.TakesModuleSubdir {
		moduleSubdir = spec.ModuleSubdir
		if moduleSubdir != "" {
			if err := cacheslot.CheckModuleSubdir(a.kind, spec.URL, moduleSubdir); err != nil {
				return nil, err
			}
		}
	}

	target, err := a.newTarget(strings.TrimPrefix(spec.URL, "oci://"), spec.Client)
	if err != nil {
		return nil, sourceresolver.WrapOCIPull(spec, err)
	}
	result, err := oci.Pull(ctx, target, spec.Version, a.row.ContentMediaType)
	if err != nil {
		return nil, sourceresolver.WrapOCIPull(spec, err)
	}
	if a.row.Layout == sourcekind.LayoutZip {
		if err := sourcecache.CheckZipLayout(result.Bytes, moduleSubdir); err != nil {
			return nil, sourceresolver.WrapOCIPull(spec, err)
		}
	}

	digest := archive.Digest(result.Bytes)
	slot, err := cacheslot.Slot(spec.WorkspaceRoot, spec.CacheDir, a.kind, spec.URL, "", moduleSubdir, spec.Version, digest)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", a.kind, err)
	}
	if err := cacheslot.WriteBlobAndMetadata(spec.WorkspaceRoot, spec.CacheDir, slot, result.Bytes,
		a.kind, spec.URL, spec.Version, digest, a.row.ContentMediaType, "", result.ManifestBytes); err != nil {
		return nil, fmt.Errorf("%s: %w", a.kind, err)
	}

	return &sourceresolver.ResolvedSource{
		Kind:           a.kind,
		URL:            spec.URL,
		ModuleSubdir:   moduleSubdir,
		PinnedVersion:  spec.Version,
		Digest:         digest,
		ManifestDigest: result.Digest,
		Mediatype:      a.row.ContentMediaType,
		CachePath:      filepath.Join(slot, cacheslot.BlobFile),
		OriginalSource: spec.OriginalSource,
	}, nil
}

func init() {
	for _, kind := range sourcekind.OCIModuleKinds() {
		row, _ := sourcekind.OCIArtifactOf(kind)
		sourceresolver.Register(&adapter{kind: kind, row: row, newTarget: remoteTarget})
	}
}
