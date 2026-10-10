// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package compilerops

import (
	stderrors "errors"
	"fmt"

	"github.com/aggrega-ai/aggrega-cli/internal/wasm/embedded"
	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/closure"
	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/pluginstrust"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// enforceRecordedSignatures is the build's one check of the signature facts a
// pull records, over the two stores those facts live in. It walks every
// reference the build's resolution reads, across the closure's own enumeration of
// its verifiable axes — catalog entities of every kind, the four library sections
// and the Wasm plugins alike — and holds each to the fact its lockfile row
// records; and it walks every plugin the build runs for an attachment that returns
// an accept-or-reject verdict, which no lockfile row records because it shapes no
// rendered byte, and holds each to the fact its entry in the record beside the
// lockfile carries (pluginstrust.TrustedPlugin.Signature). Both stores are held by
// the same two rules:
//
//   - Where the store records a verified signature, the fact is held through the
//     one decision whether a recorded fact is accepted (oci.HoldRecordedSignature):
//     the cached signature bundle is re-hashed against the recorded digest, and a
//     missing, unreadable or drifted bundle refuses the reference; and the bundle
//     must have been made for the artifact this resolution read — the hash it
//     carries of what was signed must be the hash of the digest of the manifest
//     the build's own read held the reference's bytes to — or the reference is
//     refused as carrying a signature made for a different artifact, whatever the
//     signing requirement says. That manifest is the resolution's, never a field
//     of either store: for a lockfile row the closure reference's manifest digest,
//     which every read of the content cache reports from the manifest it held; for
//     a verdict plugin the manifest the cache read of its entity held that entity
//     to (catalog.LazyLookup.CachedPluginByURI). A reference read from a catalog
//     directory, from the copy inside the binary or from beside the Release comes
//     with no manifest, and its fact is held to the re-hash alone. For a reference
//     the content cache served, either refusal names the pull of this Release as its
//     remedy, because a pull verifies again the signature of such an artifact whose
//     recorded fact this decision refuses (holdFact).
//   - Where the store records none, and the reference was served by the content
//     cache a pull filled from a registry, the reference is held to the trust
//     configuration through the one decision a pull makes over a missing
//     signature (workspaceconfig.SignatureCheck.HaltsWithoutSignature), read for
//     the host and kind the pull's own binding gives the reference
//     (catalog.SignatureSubjectOf, and catalog.PluginSignatureSubjectOf for a
//     plugin no closure axis holds). A reference that decision covers refuses,
//     naming the reference, the store's file that records nothing for it, and
//     the settings that require the signature.
//
// Where a reference came from is read from this resolution — the content cache,
// a catalog directory, the copy inside the compiler binary, the Release's own
// documents — and never from a field of either store, so a row edited to drop its
// manifest digest is held exactly as one that kept it, and a verdict plugin whose
// entry was deleted, or whose record was, is held exactly as one whose entry lost
// its signature. A reference read from a catalog directory, the binary or beside
// the Release is never held to the requirement, because no registry signature
// exists for bytes that did not come from one; where its store nonetheless records
// a fact, the fact's bundle is still re-hashed.
//
// A copied fact is what the binding catches that the re-hash cannot: a `signature`
// moved whole from one row, or one entry, onto another names a bundle that is in
// the cache and hashes to its name, and is refused only because it was made for
// the other artifact.
//
// It reads the lockfile, the record beside it, the resolved closure, the catalog
// view, the workspace configuration and files already in the content cache, and
// nothing else: no registry is asked anything and no signature is verified again,
// so a build stays offline and lockfile-only. Every refusal is collected before
// any is returned, so a reader meets every reference in one run rather than one
// per run.
func enforceRecordedSignatures(in PreviewInputs, cl closure.Closure) error {
	hold := signatureHold{in: in, registry: in.workspaceConfig.DefaultRegistryLocation().Prefix(),
		cacheDir: in.workspaceConfig.ResolvedArtifactCacheDir()}
	refusals := hold.lockfileRows(cl)
	verdict, err := hold.verdictPlugins()
	if err != nil {
		return err
	}
	refusals = append(refusals, verdict...)
	switch len(refusals) {
	case 0:
		return nil
	case 1:
		return refusals[0]
	default:
		return stderrors.Join(refusals...)
	}
}

// signatureHold carries what the two store walks of enforceRecordedSignatures
// share, and the two rules they share are its methods, so the stores cannot come
// to be held by different rules.
type signatureHold struct {
	in       PreviewInputs
	registry string
	cacheDir string
}

// holdFact holds a recorded fact through the one decision whether a recorded fact
// is accepted (oci.HoldRecordedSignature): its cached bundle to the digest the fact
// records, and to the artifact madeFor names — the manifest this resolution read
// for the reference — naming the reference a refusal belongs to and recordedIn, the
// file that records the fact.
//
// fromCache reports that the content cache a pull filled from a registry served the
// reference. A refusal of such a reference names the pull of this Release as its
// remedy (oci.WithPullRemedy), because the next pull verifies again the signature
// of exactly such an artifact whose fact the decision refuses. A reference read
// from a catalog directory, the binary or beside the Release is never asked about
// at a registry, so a pull leaves a fact recorded on it as it is, and its refusal
// names no pull.
func (h signatureHold) holdFact(name, recordedIn string, fact *lockfile.VerifiedSignature, madeFor oci.MadeFor, fromCache bool) error {
	recorded := oci.RecordedBundle{Digest: fact.BundleDigest, RecordedIn: recordedIn}
	if _, err := oci.HoldRecordedSignature(h.in.workspaceRoot, h.cacheDir, recorded, madeFor); err != nil {
		if fromCache {
			err = oci.WithPullRemedy(err, h.in.releasePath, recordedIn)
		}
		return fmt.Errorf("artifact %q: %w", name, err)
	}
	return nil
}

// requiredBy reports, for a reference the content cache served that records no
// fact, the settings that make its missing signature halt — the halt decision a
// pull makes over the same reference — and false where nothing does.
func (h signatureHold) requiredBy(subject catalog.SignatureSubject) (string, bool) {
	return signatureRequiredBy(h.in.workspaceConfig, subject)
}

// signatureRequiredBy is the one reading, for either store, of whether a missing
// signature halts for a subject: the decision a pull makes over the same subject
// (workspaceconfig.SignatureCheck.HaltsWithoutSignature), and the settings that
// make it halt.
func signatureRequiredBy(wsCfg workspaceconfig.Config, subject catalog.SignatureSubject) (string, bool) {
	check := wsCfg.SignatureCheckFor(subject.Host, subject.Kind)
	if !check.HaltsWithoutSignature() {
		return "", false
	}
	return check.RequirementSource(), true
}

// unrecordedVerdictPluginRefusal is the one rule for a plugin attached at a verdict
// role whose record beside the lockfile carries no signature for it: the refusal it
// earns, or nil. It is held only where its entity was served by the content cache a
// pull filled from a registry, the binary does not carry it, it binds to a registry
// (catalog.PluginSignatureSubjectOf over the attachment's reference — the pull's
// binding), and a missing signature halts there.
//
// The build's walk of its verdict attachments asks it, and so does every load of
// such a plugin through the hold the governance composition hands the pipeline and
// the linter socket (verdictPluginSignatureHold), so a plugin the build would refuse
// is refused by whatever surface loads it, by the same rule.
func unrecordedVerdictPluginRefusal(wsCfg workspaceconfig.Config, registry string, read verdictPluginRead, attachmentURI, recordPath, releasePath string) error {
	if !read.fromCache {
		return nil
	}
	if _, carried := embedded.Lookup(read.coordinate); carried {
		return nil
	}
	subject, ok := catalog.PluginSignatureSubjectOf(registry, attachmentURI)
	if !ok {
		return nil
	}
	requiredBy, held := signatureRequiredBy(wsCfg, subject)
	if !held {
		return nil
	}
	return lockfile.PluginSignatureNotRecorded(read.coordinate, recordPath, requiredBy, releasePath)
}

// lockfileRows walks the first store: every reference the closure enumerates,
// joined to the lockfile row of its own section.
func (h signatureHold) lockfileRows(cl closure.Closure) []error {
	// A section is named the same on both sides — the Lockfile field and the
	// closure's axis — so a closure reference finds its own row and never the row of
	// another section recording the same reference.
	recorded := h.in.lockfile.RecordedSignatures()
	lockfilePath := lockfile.PathFor(h.in.releasePath)
	var refusals []error
	for _, axis := range cl.VerifiableAxes() {
		for _, ref := range axis.Refs {
			if fact := recorded[lockfile.RowKey{Section: axis.Axis, URI: ref.URI}]; fact != nil {
				// The manifest the build's own read held the reference's bytes to, which
				// the closure reference reports from the read rather than from the row.
				if err := h.holdFact(ref.URI, lockfilePath, fact, oci.MadeForManifest(ref.ManifestDigest),
					closure.AcquiredFromRegistry(ref.ResolutionPath)); err != nil {
					refusals = append(refusals, err)
				}
				continue
			}
			if !closure.AcquiredFromRegistry(ref.ResolutionPath) {
				continue
			}
			subject, ok := catalog.SignatureSubjectOf(h.registry, axis, ref)
			if !ok {
				continue
			}
			if requiredBy, held := h.requiredBy(subject); held {
				refusals = append(refusals, lockfile.SignatureNotRecorded(ref.URI, lockfilePath, requiredBy, h.in.releasePath))
			}
		}
	}
	return refusals
}

// verdictPlugins walks the second store: the plugin each attachment that returns
// an accept-or-reject verdict names, joined by its identity coordinate to its
// entry in the record beside the lockfile — the address that record keeps.
//
// The walk is over the ATTACHMENTS rather than over the record's entries, because
// the record says nothing about where a plugin came from: which tier serves it is
// read from the build's own reading of the attachment (readVerdictPlugin), and a
// plugin the record holds no entry for is read and held exactly as one whose entry
// carries no signature. An entry no attachment accounts for any more is not walked
// here; the composition that holds the configuration to the record refuses it.
//
// A plugin the catalog view serves is not held: that view holds the catalog
// directories and the lockfile's pins, and a pinned plugin's own rows are walked
// by the first store. Nor is a plugin whose bytes the binary carries, which the
// cascade serves from the binary whatever tier served its entity, and which a pull
// therefore never records. A reading that cannot identify the plugin at all is
// left to the resolution that halts on it with its own diagnostic.
//
// The record is the one the caller composed where it supplied one
// (WithComposedTrustRecord) — the resolution that is about to write it — and the
// file otherwise. A read failure of the file is returned as it is: the record
// cannot say whether anything is recorded, which is not the same as saying nothing
// is.
func (h signatureHold) verdictPlugins() ([]error, error) {
	attachments := h.in.workspaceConfig.VerdictAttachments()
	if len(attachments) == 0 {
		return nil, nil
	}
	recordPath := pluginstrust.PathFor(h.in.releasePath)
	record := h.in.composedTrust
	if record == nil {
		read, _, err := pluginstrust.LoadOrStart(recordPath)
		if err != nil {
			return nil, err
		}
		record = read
	}
	var refusals []error
	walked := make(map[string]bool, len(attachments))
	for _, attachment := range attachments {
		read, identified := readVerdictPlugin(h.in.view, attachment.Ref.URI)
		if !identified || walked[read.coordinate] {
			continue
		}
		walked[read.coordinate] = true
		if entry, held := record.Find(read.coordinate); held && entry.Signature != nil {
			if err := h.holdFact(read.coordinate, recordPath, entry.Signature, oci.MadeForManifest(read.manifestDigest), read.fromCache); err != nil {
				refusals = append(refusals, err)
			}
			continue
		}
		if err := unrecordedVerdictPluginRefusal(h.in.workspaceConfig, h.registry, read, attachment.Ref.URI, recordPath, h.in.releasePath); err != nil {
			refusals = append(refusals, err)
		}
	}
	return refusals, nil
}
