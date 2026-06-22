// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"oras.land/oras-go/v2"

	"github.com/aggrega-ai/aggrega-cli/pkg/closure"
	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/pullverify"
	"github.com/aggrega-ai/aggrega-cli/pkg/registryhost"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// verifyClosureSignatures runs the pull-time signature verification pass
// over the closure's registry-resolved entities and folds the verified
// facts into the lockfile entities, caching each verified signature bundle
// as a content-addressed blob for air-gapped re-verification. It is called
// only on the pull write path — never on build, which reads the recorded
// facts from the lockfile and never re-verifies signatures (the AOT
// boundary).
//
// A registry-resolved entity is one whose URI carries a registry host. A
// host-less (local / catalog) entity is skipped: it never carries a
// registry signature. A fatal verification failure (a required signature
// that is missing, untrusted, or invalid for a non-advisory registry)
// returns a non-nil error and halts the pull.
func verifyClosureSignatures(ctx context.Context, lf *lockfile.Lockfile, cl closure.Closure, wsCfg workspaceconfig.Config, cacheDir string) error {
	entities := make([]pullverify.Entity, 0, len(cl.Entities))
	for _, e := range cl.Entities {
		// Verify only entities freshly resolved from a registry (Tier-3).
		// A Tier-1 catalog or Tier-2 cache hit was already verified on the
		// pull that first fetched it (its lockfile entry carries the fact);
		// a local / external entity never carries a registry signature.
		if !isRegistryResolved(e.ResolutionPath) {
			continue
		}
		host := registryhost.Of(e.URI)
		if !isRegistryHost(host) {
			continue
		}
		entities = append(entities, pullverify.Entity{URI: e.URI, Digest: e.Digest, Host: host})
	}
	if len(entities) == 0 {
		return nil
	}

	results, err := pullverify.Verify(ctx, entities, pullverify.Options{
		Config: wsCfg,
		Target: remoteTargetFor,
		Warn:   func(notice string) { fmt.Fprintf(os.Stderr, "  verify: %s\n", notice) },
	})
	if err != nil {
		return err
	}

	factByURI := make(map[string]pullverify.Result, len(results))
	for _, r := range results {
		factByURI[r.URI] = r
	}
	sigDir := filepath.Join(cacheDir, "signatures")
	for i := range lf.Entities {
		r, ok := factByURI[lf.Entities[i].URI]
		if !ok {
			continue
		}
		if err := cacheBundleBlob(sigDir, r.BundleDigest, r.Bundle); err != nil {
			return fmt.Errorf("cache signature bundle for %s: %w", r.URI, err)
		}
		lf.Entities[i].Signature = &lockfile.VerifiedSignature{
			Mode:         r.Mode,
			Identity:     r.Identity,
			Issuer:       r.Issuer,
			BundleDigest: r.BundleDigest,
		}
	}
	return nil
}

// remoteTargetFor resolves a real remote OCI repository for an entity's
// registry, returning the entity's version tag as the subject reference to
// discover signature referrers against. Authentication delegates to the
// shared OCI credential mechanisms.
func remoteTargetFor(e pullverify.Entity) (oras.ReadOnlyTarget, string, error) {
	repoRef := e.URI
	// Strip any version suffix from the URI to form the repository
	// reference; the subject ref is the content digest the signature
	// commits to.
	if at := strings.LastIndex(repoRef, "@"); at >= 0 {
		repoRef = repoRef[:at]
	}
	repo, err := oci.NewRemoteRepository(repoRef)
	if err != nil {
		return nil, "", err
	}
	return repo, e.Digest, nil
}

// cacheBundleBlob writes bundle to a content-addressed blob named by its
// digest under dir, creating dir as needed. The write is idempotent: a
// blob already present (a re-pull of the same signature) is left untouched
// so the cache stays byte-stable.
func cacheBundleBlob(dir, bundleDigest string, bundle []byte) error {
	if bundleDigest == "" || len(bundle) == 0 {
		return nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	name := strings.ReplaceAll(bundleDigest, ":", "-") + ".bundle"
	path := filepath.Join(dir, name)
	if _, statErr := os.Stat(path); statErr == nil {
		return nil
	}
	return os.WriteFile(path, bundle, 0o644)
}

// isRegistryResolved reports whether a closure entity's resolution path
// indicates a fresh registry (Tier-3) fetch — the only case where a
// signature is verified at pull time. A Tier-1 / Tier-2 / external path is
// not re-verified here.
func isRegistryResolved(resolutionPath string) bool {
	return strings.Contains(resolutionPath, "tier-3")
}

// isRegistryHost reports whether host looks like a real registry authority
// — a dotted host (with an optional port) or a host:port — rather than a
// bare host-less catalog name or a relative-path artifact (a leading "."
// from a `./file` reference). A host-less or path-like entity carries no
// registry signature and is skipped by the verify pass.
func isRegistryHost(host string) bool {
	if host == "" || strings.HasPrefix(host, ".") || strings.HasPrefix(host, "/") {
		return false
	}
	if i := strings.IndexByte(host, ':'); i > 0 {
		return true // host:port
	}
	dot := strings.IndexByte(host, '.')
	return dot > 0 && dot < len(host)-1
}
