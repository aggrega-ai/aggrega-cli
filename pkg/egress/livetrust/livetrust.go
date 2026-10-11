// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Package livetrust is the network-egress gate for the live Sigstore trust
// root. It is a sibling of the egress core rather than part of it: the live
// trust-root fetch pulls in the sigstore TUF machinery, which is a lock-time
// verification concern that must never enter the build/render path — and the
// build/render path imports the egress core for the offline decision. Keeping
// the TUF import in this separate leaf keeps the core, and therefore the build
// path, free of verification machinery while still single-sourcing the live
// fetch behind the offline gate.
//
// It is the sole home of the live-trust-root fetch: the TUF client
// construction lives only here, so no caller can build its own live root and
// egress around the offline decision. A pinned trust root loaded from a local
// file performs no egress and does not pass through here.
//
// It is also the one home of where that fetch keeps what it fetched. A fetch
// leaves the trust root it obtained in the TUF client's on-disk cache, and
// [LastFetchedRootFile] names that file for the one read that verifies against
// it while offline. Naming it here, beside the fetch that writes it, keeps the
// location a single spelling and keeps the offline decision for both in one
// place.
package livetrust

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/sigstore/sigstore-go/pkg/root"
	"github.com/sigstore/sigstore-go/pkg/tuf"

	"github.com/aggrega-ai/aggrega-cli/pkg/egress"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// TrustedRoot fetches the live public-good Sigstore trust root over TUF,
// derived from the resolved workspace configuration. When the configuration is
// offline it returns (nil, egress.ErrOfflineEgress) before any dial, so an
// offline keyless sign or a no-pinned-root offline verify refuses early and
// clearly rather than failing deep inside a TUF request.
func TrustedRoot(cfg workspaceconfig.Config) (root.TrustedMaterial, error) {
	if cfg.Offline {
		return nil, egress.ErrOfflineEgress()
	}
	live, err := root.NewLiveTrustedRoot(tuf.DefaultOptions())
	if err != nil {
		return nil, fmt.Errorf("livetrust: live trust-root fetch: %w", err)
	}
	return live, nil
}

// trustedRootTarget is the name the public trust root is published under in the
// TUF repository, and so the name of the file the client keeps it in.
const trustedRootTarget = "trusted_root.json"

// LastFetchedRootFile returns the file holding the public trust root this
// machine's TUF client last fetched, for a read that verifies a kept signature
// under a keyless check pinning no trust root of its own.
//
// It is that read's side of the offline gate [TrustedRoot] takes, and it reads
// the offline decision the same way. Where cfg is not offline it returns "" and
// no error: the read obtains the live trust root through [TrustedRoot], exactly
// as before. Where cfg is offline the live root cannot be obtained, and it
// returns instead the trust root an earlier online fetch left in the client's
// cache, so the read verifies against that — the same file that fetch verified
// and wrote, used as a pinned trust root is used. It never fetches: it builds no
// TUF client, sends no request, and reads one file.
//
// A file held there is used whatever its age. The cache also holds the TUF
// metadata the fetch verified it under, and that metadata expires within days;
// it is not consulted, so a machine that has been offline for longer still
// verifies against the last trust root it fetched rather than against none.
//
// Where no usable trust root is held — nothing was ever fetched on this
// machine, the cache was removed, or what is there does not read as a trust
// root — offline it returns a refusal saying the check could not run offline,
// which matches [egress.ErrOfflineEgress].
//
// cacheDir is the TUF client's cache directory; empty selects the one the
// client keeps on this machine, the same directory the live fetch writes. Inside
// a test binary that default resolves to none, so a test that has not stated a
// cache directory of its own never reads the developer's.
func LastFetchedRootFile(cfg workspaceconfig.Config, cacheDir string) (string, error) {
	if !cfg.Offline {
		return "", nil
	}
	path, err := heldRootFile(cacheDir)
	if err != nil {
		return "", fmt.Errorf("the signature check could not run offline: it verifies against the public "+
			"Sigstore trust root, which cannot be fetched while the workspace is offline, and no usable copy "+
			"fetched earlier is held on this machine (%v): %w", err, egress.ErrOfflineEgress())
	}
	return path, nil
}

// heldRootFile returns the trust-root file the client keeps under cacheDir, or
// under its own cache directory where cacheDir is empty, once it has checked that
// the file reads as a trust root. What it reports carries no filesystem path:
// the reason reaches readers this machine's layout is not reported to.
func heldRootFile(cacheDir string) (string, error) {
	if cacheDir == "" {
		dir, err := clientCacheDir()
		if err != nil {
			return "", err
		}
		cacheDir = dir
	}
	path := CachedTrustedRootPath(cacheDir)
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return "", errors.New("none has been fetched here")
	}
	if err != nil {
		return "", errors.New("the one held here cannot be read")
	}
	if _, err := root.NewTrustedRootFromJSON(raw); err != nil {
		return "", errors.New("the one held here does not read as a trust root")
	}
	return path, nil
}

// clientCacheDir returns the cache directory the TUF client keeps on this
// machine: the one the options the live fetch is built from name, so the reader
// and the writer cannot name two places. Inside a test binary it resolves to
// none.
func clientCacheDir() (string, error) {
	if testing.Testing() {
		return "", errors.New("no cache directory was stated for this run")
	}
	return tuf.DefaultOptions().CachePath, nil
}

// CachedTrustedRootPath returns where the TUF client keeps the public trust root
// it last fetched, under the client cache directory cacheDir: the client keeps
// each repository it reads in a directory named for the repository's address,
// and each target it fetched under that directory's targets directory.
//
// It is the one spelling of that location. The offline read takes the file from
// here, and a test that stands in for an earlier fetch places one here.
func CachedTrustedRootPath(cacheDir string) string {
	return cachedTrustedRootPath(cacheDir, tuf.DefaultOptions().RepositoryBaseURL)
}

// cachedTrustedRootPath is where the client keeps the trust root it fetched from
// the repository at mirror, under the client cache directory cacheDir.
func cachedTrustedRootPath(cacheDir, mirror string) string {
	return filepath.Join(cacheDir, tuf.URLToPath(mirror), "targets", trustedRootTarget)
}
