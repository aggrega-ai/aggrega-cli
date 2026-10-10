// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package compilerops

import (
	stderrors "errors"
	"fmt"

	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/closure"
	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
)

// enforceRecordedSignatures is the build's one check of the signature facts a
// pull records. It walks every reference the build's resolution reads, across
// the closure's own enumeration of its verifiable axes — catalog entities of every
// kind, the four library sections and the Wasm plugins alike — and holds each to
// the fact its lockfile row records:
//
//   - Where the row records a verified signature, the cached signature bundle is
//     re-hashed against the recorded digest, through the one implementation of
//     that check (oci.VerifyCachedSignatureBundle). A missing, unreadable or
//     drifted bundle refuses the row.
//   - Where the row records none, and the reference was served by the content
//     cache a pull filled from a registry, the row is held to the trust
//     configuration through the one decision a pull makes over a missing
//     signature (workspaceconfig.SignatureCheck.HaltsWithoutSignature), read for
//     the host and kind the pull's own binding gives the reference
//     (catalog.SignatureSubjectOf). A row that decision covers refuses, naming
//     the reference and the settings that require the signature.
//
// Where a reference came from is read from this resolution — the content cache,
// a catalog directory, the copy inside the compiler binary, the Release's own
// documents — and never from a field of the lockfile, so a row edited to drop its
// manifest digest is held exactly as one that kept it. A reference read from a
// catalog directory, the binary or beside the Release is never held to the
// requirement, because no registry signature exists for bytes that did not come
// from one; where its row nonetheless records a fact, the fact's bundle is still
// re-hashed.
//
// It reads the lockfile, the resolved closure, the workspace configuration and
// files already in the content cache, and nothing else: no registry is asked
// anything and no signature is verified again, so a build stays offline and
// lockfile-only. Every refusal is collected before any is returned, so a reader
// meets every row in one run rather than one per run.
func enforceRecordedSignatures(in PreviewInputs, cl closure.Closure) error {
	// A section is named the same on both sides — the Lockfile field and the
	// closure's axis — so a closure reference finds its own row and never the row of
	// another section recording the same reference.
	recorded := in.lockfile.RecordedSignatures()
	registry := in.workspaceConfig.DefaultRegistryLocation().Prefix()
	cacheDir := in.workspaceConfig.ResolvedArtifactCacheDir()

	var refusals []error
	for _, axis := range cl.VerifiableAxes() {
		for _, ref := range axis.Refs {
			if fact := recorded[lockfile.RowKey{Section: axis.Axis, URI: ref.URI}]; fact != nil {
				if fact.BundleDigest == "" {
					continue
				}
				if _, err := oci.VerifyCachedSignatureBundle(in.workspaceRoot, cacheDir, fact.BundleDigest); err != nil {
					refusals = append(refusals, fmt.Errorf("artifact %q: %w", ref.URI, err))
				}
				continue
			}
			if !closure.AcquiredFromRegistry(ref.ResolutionPath) {
				continue
			}
			subject, ok := catalog.SignatureSubjectOf(registry, axis, ref)
			if !ok {
				continue
			}
			check := in.workspaceConfig.SignatureCheckFor(subject.Host, subject.Kind)
			if !check.HaltsWithoutSignature() {
				continue
			}
			refusals = append(refusals, lockfile.SignatureNotRecorded(ref.URI, check.RequirementSource(), in.releasePath))
		}
	}
	switch len(refusals) {
	case 0:
		return nil
	case 1:
		return refusals[0]
	default:
		return stderrors.Join(refusals...)
	}
}
