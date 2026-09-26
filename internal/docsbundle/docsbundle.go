// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Package docsbundle names the documentation bundle and acquires it into a
// workspace's cache.
//
// It is the one home for three things every surface that serves documentation
// needs and none of them should decide for itself: where the bundle is published,
// how it is fetched and verified, and how long one attempt may take. A surface
// that resolved its own reference would be a second spelling of the artifact's
// address, and two surfaces reading two addresses would disagree about what
// "the documentation" is on the same machine.
//
// The read half is deliberately not here. Reading a page out of the cache is
// pkg/docscache's job and takes no target, so a surface that only reads never
// links the acquisition path at all.
package docsbundle

import (
	"context"
	"fmt"
	"time"

	"oras.land/oras-go/v2"

	"github.com/aggrega-ai/aggrega-cli/pkg/docscache"
	"github.com/aggrega-ai/aggrega-cli/pkg/egress"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/pullverify"
	"github.com/aggrega-ai/aggrega-cli/pkg/registryhost"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

const (
	// Namespace is the reserved registry namespace standalone data artifacts
	// publish under.
	Namespace = "docs"

	// Name is the artifact name of the documentation bundle within that
	// namespace.
	Name = "aggrega-docs"
)

// AcquireTimeout bounds one acquisition attempt.
//
// The egress transport carries no deadline of its own, so without a bound an
// unreachable registry would hold the handshake open for as long as the operating
// system's connect timeout allows. It is generous enough for a cold pull over a
// slow link and short enough that a dead registry costs a pause rather than a
// session.
const AcquireTimeout = 30 * time.Second

// Ref returns the documentation bundle's reference within the given registry
// location prefix: a registry host, followed by the repository path the default
// registry carries, if any — where a data-artifact publication through the same
// default places it.
func Ref(prefix string) string {
	return prefix + "/" + Namespace + "/" + Name
}

// TargetFor resolves the OCI target the documentation bundle is pulled from and
// the pull-time signature check to run against it.
//
// It is deliberately the narrowest seam that removes the registry: everything
// after it — the cache-presence probe, the pull, the media-type check, the
// verification, the cache write and the read — is the production path, so a test
// substituting a target still exercises all of it. A seam one level higher, over
// the whole acquisition, would take the cache write out of the tested path and
// leave the trigger proven against nothing.
type TargetFor func(cfg workspaceconfig.Config, ref, version string, warn func(string)) (oras.ReadOnlyTarget, docscache.VerifyAtPull, error)

// RegistryTarget is the production resolution. The workspace's egress gate
// supplies the transport and refuses outright when the workspace is offline; the
// shared OCI constructor supplies the authenticated repository, so credentials
// and the loopback plain-HTTP decision are read from the one place that owns
// them.
//
// The verifier it returns is the same pull-time signature check a catalog entity
// or a plugin gets, run against the workspace's own per-registry trust policy
// through pullverify. Returning none would have been simpler and would have made
// a documentation bundle the one artifact class that bypasses that policy.
func RegistryTarget(cfg workspaceconfig.Config, ref, version string, warn func(string)) (oras.ReadOnlyTarget, docscache.VerifyAtPull, error) {
	httpClient, err := egress.HTTPClient(cfg)
	if err != nil {
		return nil, nil, err
	}
	repo, err := oci.NewRemoteRepository(ref, httpClient)
	if err != nil {
		return nil, nil, err
	}
	verify := func(ctx context.Context, manifestDigest string) error {
		_, verr := pullverify.Verify(ctx, []pullverify.Entity{{
			URI:            ref,
			ManifestDigest: manifestDigest,
			Host:           registryhost.Of(ref),
		}}, pullverify.Options{
			Config: cfg,
			Target: func(pullverify.Entity) (oras.ReadOnlyTarget, string, error) {
				return repo, version, nil
			},
			Warn: warn,
		})
		return verr
	}
	return repo, verify, nil
}

// EnsureCached acquires the documentation bundle for (ref, version) into the
// workspace's cache when it is not already there, and reports whether it
// acquired one.
//
// A bundle already cached is left exactly as it is and reported as not acquired,
// so a caller can distinguish a warm cache from a fetch without probing again.
// The presence check runs before the target is resolved, so a warm cache reaches
// for no registry at all.
//
// A nil target selects [RegistryTarget]. The attempt is bounded by
// [AcquireTimeout], and a failure leaves the cache exactly as it was — the pull
// verifies content and signature before anything is written, so a bundle that
// cannot be verified never populates a slot.
//
// The error is returned rather than swallowed, and what to do with it is the
// caller's decision: a session that serves documentation as one capability among
// many logs it and carries on, while a command whose whole answer is a page has
// to say so.
func EnsureCached(ctx context.Context, cfg workspaceconfig.Config, workspaceRoot, cacheDir, ref, version string,
	target TargetFor, warn func(string)) (bool, error) {
	if docscache.Cached(workspaceRoot, cacheDir, ref, version) {
		return false, nil
	}
	if target == nil {
		target = RegistryTarget
	}
	if warn == nil {
		warn = func(string) {}
	}
	repo, verify, err := target(cfg, ref, version, warn)
	if err != nil {
		return false, err
	}
	ctx, cancel := context.WithTimeout(ctx, AcquireTimeout)
	defer cancel()
	if _, err := docscache.ResolveAndCache(ctx, repo, ref, version, workspaceRoot, cacheDir, verify); err != nil {
		return false, fmt.Errorf("acquire documentation bundle %s@%s: %w", ref, version, err)
	}
	return true, nil
}
