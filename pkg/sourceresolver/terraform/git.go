// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Package terraform implements the terraform-module-git source-
// resolver adapter. The adapter clones a Git repository to a
// temporary directory (production path; bounded peak memory for
// large modules), resolves the user-supplied ref (tag, branch, or
// commit SHA) to an immutable commit SHA via ResolveRevision,
// checks out at that SHA, archives the working tree (or the
// optional ModuleSubdir) into a deterministic .tar.gz, computes
// the content digest, populates the cache slot, and returns a
// ResolvedSource with the commit SHA recorded as PinnedVersion.
//
// Ref normalisation is load-bearing: only commit SHAs are
// immutable; tags and branches may be re-pushed at the registry.
// The adapter resolves at lock time so the lockfile always pins a
// commit SHA. A re-pushed ref produces drift at the next pull;
// "aggrega pull --regenerate" is the remediation.
//
// Auth: go-git's default transports honour SSH keys, ~/.netrc, and
// gitconfig credential helpers. The adapter passes spec.URL through
// to git.CloneContext unchanged, so any URL shape go-git accepts
// works (https://, ssh://, git+ssh://, file://).
package terraform

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/pkg/sourceresolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/sourceresolver/internal/archive"
	"github.com/aggrega-ai/aggrega-cli/pkg/sourceresolver/cacheslot"
	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/transport"
)

// Kind names the terraform-module-git source kind.
const Kind = "terraform-module-git"

// MediatypeTarGz is the canonical media-type recorded for git-sourced
// terraform module archives. Generic application/gzip is correct;
// there is no terraform-specific OCI media-type at this stage.
const MediatypeTarGz = "application/gzip"

// cloneFunc is the seam between production go-git CloneContext and
// test-injected clone behaviour. Production wires this to a thin
// wrapper around git.PlainCloneContext (filesystem-backed). Tests
// inject their own implementation that clones from a local fixture
// repo.
type cloneFunc func(ctx context.Context, destDir, url string) (*git.Repository, error)

type adapter struct {
	clone cloneFunc
}

// NewAdapter returns a terraform-module-git adapter wired to
// git.PlainCloneContext in production.
func NewAdapter() sourceresolver.Adapter {
	return &adapter{clone: defaultClone}
}

func defaultClone(ctx context.Context, destDir, url string) (*git.Repository, error) {
	return git.PlainCloneContext(ctx, destDir, false, &git.CloneOptions{
		URL:        url,
		NoCheckout: true,
	})
}

// Kind returns the terraform-module-git kind string.
func (a *adapter) Kind() string { return Kind }

// Resolve clones the repository at spec.URL into a temp directory,
// resolves spec.Ref to a commit SHA, checks out at that SHA,
// archives the working tree (or spec.ModuleSubdir if set), and
// populates the cache slot. The temp directory is removed before
// return regardless of outcome.
//
// PinnedVersion in the returned ResolvedSource is the resolved
// commit SHA, not the original ref. The original ref is preserved
// in OriginalSource for lockfile-side `requested_ref:` recording.
func (a *adapter) Resolve(spec sourceresolver.SourceSpec) (*sourceresolver.ResolvedSource, error) {
	cloneDir, err := os.MkdirTemp("", "aggrega-tf-git-*")
	if err != nil {
		return nil, fmt.Errorf("terraform-module-git: tempdir: %w", err)
	}
	defer os.RemoveAll(cloneDir)

	ctx := context.Background()
	repo, err := a.clone(ctx, cloneDir, spec.URL)
	if err != nil {
		return nil, mapGitError(spec, err)
	}

	ref := spec.Ref
	if ref == "" {
		ref = "HEAD"
	}
	hash, err := repo.ResolveRevision(plumbing.Revision(ref))
	if err != nil {
		return nil, sourceresolver.Wrap(spec, sourceresolver.ErrFetchNotFound, err)
	}

	wt, err := repo.Worktree()
	if err != nil {
		return nil, fmt.Errorf("terraform-module-git: worktree: %w", err)
	}
	if err := wt.Checkout(&git.CheckoutOptions{Hash: *hash}); err != nil {
		return nil, fmt.Errorf("terraform-module-git: checkout %s: %w", hash.String(), err)
	}

	walkRoot := cloneDir
	if spec.ModuleSubdir != "" {
		walkRoot = filepath.Join(cloneDir, spec.ModuleSubdir)
	}
	entries, err := walkExcludingGit(walkRoot)
	if err != nil {
		return nil, fmt.Errorf("terraform-module-git: walk: %w", err)
	}

	archiveBytes, err := encodeTarGz(entries)
	if err != nil {
		return nil, fmt.Errorf("terraform-module-git: archive: %w", err)
	}
	digest := archive.Digest(archiveBytes)
	commitSHA := hash.String()
	slot, err := cacheslot.Slot(spec.WorkspaceRoot, spec.CacheDir, Kind, spec.URL, commitSHA, digest)
	if err != nil {
		return nil, fmt.Errorf("terraform-module-git: %w", err)
	}
	if err := cacheslot.WriteBlobAndMetadata(slot, archiveBytes,
		Kind, spec.URL, commitSHA, digest, MediatypeTarGz); err != nil {
		return nil, fmt.Errorf("terraform-module-git: %w", err)
	}

	return &sourceresolver.ResolvedSource{
		Kind:           Kind,
		URL:            spec.URL,
		PinnedVersion:  commitSHA,
		Digest:         digest,
		Mediatype:      MediatypeTarGz,
		CachePath:      slot + "/blob.tgz",
		OriginalSource: spec.OriginalSource,
	}, nil
}

// walkExcludingGit walks rootPath via internal/archive.WalkDirectory
// and filters out the .git/ subtree so the archive carries only the
// module's working-tree content.
func walkExcludingGit(rootPath string) ([]archive.Entry, error) {
	all, err := archive.WalkDirectory(rootPath)
	if err != nil {
		return nil, err
	}
	filtered := all[:0]
	for _, e := range all {
		if strings.HasPrefix(e.RelPath, ".git/") || e.RelPath == ".git/" {
			continue
		}
		filtered = append(filtered, e)
	}
	return filtered, nil
}

// encodeTarGz wraps internal/archive.WriteTarGz in a buffer so the
// adapter has a single-call helper for archive materialisation.
func encodeTarGz(entries []archive.Entry) ([]byte, error) {
	var buf tarBuf
	if err := archive.WriteTarGz(&buf, entries); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

type tarBuf struct {
	b []byte
}

func (t *tarBuf) Write(p []byte) (int, error) {
	t.b = append(t.b, p...)
	return len(p), nil
}

func (t *tarBuf) Bytes() []byte { return t.b }

func mapGitError(spec sourceresolver.SourceSpec, err error) error {
	if errors.Is(err, transport.ErrAuthenticationRequired) || errors.Is(err, transport.ErrAuthorizationFailed) {
		return sourceresolver.Wrap(spec, sourceresolver.ErrFetchUnauthorized, err)
	}
	if errors.Is(err, transport.ErrRepositoryNotFound) {
		return sourceresolver.Wrap(spec, sourceresolver.ErrFetchNotFound, err)
	}
	msg := err.Error()
	if strings.Contains(msg, "no such host") ||
		strings.Contains(msg, "connection refused") ||
		strings.Contains(msg, "i/o timeout") {
		return sourceresolver.Wrap(spec, sourceresolver.ErrFetchNetworkUnreachable, err)
	}
	return sourceresolver.Wrap(spec, sourceresolver.ErrFetchNetworkUnreachable, err)
}

func init() {
	sourceresolver.Register(NewAdapter())
}
