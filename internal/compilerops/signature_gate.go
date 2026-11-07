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
//     must have been made for the artifact this resolution read — one of the
//     hashes it names for what the signature was made for, in either form a pull
//     accepts, must be the hash of the digest of the manifest the build's own
//     read held the reference's bytes to — or the reference is
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
	hold := newSignatureHold(in.workspaceConfig, in.workspaceRoot, in.releasePath)
	refusals := hold.lockfileRows(in.lockfile, cl)
	verdict, err := hold.verdictPlugins(in.view, in.composedTrust)
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
	wsCfg         workspaceconfig.Config
	workspaceRoot string
	releasePath   string
	registry      string
	cacheDir      string
}

// newSignatureHold is the hold over the Release at releasePath in the workspace at
// workspaceRoot under wsCfg. The registry a host-less reference is bound under and
// the cache directory a recorded fact's bundle is read from are both read from wsCfg
// here, once, so every walk built through it reads them alike.
func newSignatureHold(wsCfg workspaceconfig.Config, workspaceRoot, releasePath string) signatureHold {
	return signatureHold{wsCfg: wsCfg, workspaceRoot: workspaceRoot, releasePath: releasePath,
		registry: wsCfg.DefaultRegistryLocation().Prefix(), cacheDir: wsCfg.ResolvedArtifactCacheDir()}
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
	if _, err := oci.HoldRecordedSignature(h.workspaceRoot, h.cacheDir, recorded, madeFor); err != nil {
		if fromCache {
			err = oci.WithPullRemedy(err, h.releasePath, recordedIn)
		}
		return fmt.Errorf("artifact %q: %w", name, err)
	}
	return nil
}

// requiredBy reports, for a reference the content cache served that records no
// fact, the settings that make its missing signature halt — the halt decision a
// pull makes over the same reference — and false where nothing does.
func (h signatureHold) requiredBy(subject catalog.SignatureSubject) (string, bool) {
	return signatureRequiredBy(h.wsCfg, subject)
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

// unrecordedVerdictPluginRequirement is the one rule for a plugin attached at a
// verdict role whose record beside the lockfile carries no signature for it: whether
// a build refuses it, and the settings that make it so. It is held only where its
// entity was served by the content cache a pull filled from a registry, the binary
// does not carry it, it binds to a registry (catalog.PluginSignatureSubjectOf over
// the attachment's reference, under the workspace's default registry — the pull's
// binding), and a missing signature halts there.
//
// Every reader of that question reaches it through here: the build's walk of its
// verdict attachments, which a surface outside this package reads too
// (VerdictPluginSignatureRefusals), and every load of such a plugin through the hold
// the governance composition hands the pipeline and the linter socket
// (verdictPluginSignatureHold), both by unrecordedVerdictPluginRefusal. So a plugin
// the build would refuse is refused, or reported, by whatever surface asks, by the
// same rule.
func unrecordedVerdictPluginRequirement(wsCfg workspaceconfig.Config, read verdictPluginRead, attachmentURI string) (string, bool) {
	if !read.fromCache {
		return "", false
	}
	if _, carried := embedded.Lookup(read.coordinate); carried {
		return "", false
	}
	subject, ok := catalog.PluginSignatureSubjectOf(wsCfg.DefaultRegistryLocation().Prefix(), attachmentURI)
	if !ok {
		return "", false
	}
	return signatureRequiredBy(wsCfg, subject)
}

// unrecordedVerdictPluginRefusal is the refusal unrecordedVerdictPluginRequirement
// earns for a plugin whose record carries no signature for it, naming the plugin,
// recordPath — the record that records nothing for it — the settings that require
// the signature and the pull of releasePath that records it; or nil where the rule
// does not hold the plugin.
func unrecordedVerdictPluginRefusal(wsCfg workspaceconfig.Config, read verdictPluginRead, attachmentURI, recordPath, releasePath string) error {
	requiredBy, held := unrecordedVerdictPluginRequirement(wsCfg, read, attachmentURI)
	if !held {
		return nil
	}
	return lockfile.PluginSignatureNotRecorded(read.coordinate, recordPath, requiredBy, releasePath)
}

// VerdictPluginSignatureRefusal is the build's refusal of one plugin attached at a
// verdict role over the signature the record beside the lockfile carries for it:
// the plugin's identity coordinate — the address that record keeps — and the
// refusal as the build's check returns it.
type VerdictPluginSignatureRefusal struct {
	Plugin  string
	Refusal error
}

// VerdictPluginSignatureRefusals is the build's check of the second store, the
// record beside the lockfile: for each plugin the workspace's attachments that
// return an accept-or-reject verdict name, read as the build reads them
// (attachedVerdictPlugins over view: which plugin each names, and which tier serves
// it), the build's refusal of the signature record carries for it, in attachment
// order — one for each plugin the build refuses, and none for a plugin it admits.
//
//   - An entry carrying a signature is held through holdFact: the cached bundle to
//     the digest the entry records, refused with oci.ErrSignatureBundleDigestMismatch
//     where it is missing, unreadable or altered; and, for a plugin the content cache
//     serves, the bundle to the manifest the build's read held the plugin to
//     (verdictPluginRead.manifestDigest), refused with
//     oci.ErrSignatureForAnotherArtifact where it was made for another artifact.
//   - A plugin whose entry carries none, or which the record holds no entry for, is
//     held to the build's rule for such a plugin (unrecordedVerdictPluginRefusal),
//     refused with lockfile.ErrSignatureNotRecorded.
//
// It is the build's own walk rather than a reading beside it: the check before a
// render reaches the record through here (signatureHold.verdictPlugins), so a
// surface outside this package asking which plugins a build refuses over a record,
// and for what, meets the build's answer rather than a second one. A caller tells
// the refusals apart by their conditions (errors.Is), never by their text.
//
// view is the catalog view a build of the Release reads the attachments through.
// Its catalog directories and pins decide which tier serves each plugin, and only a
// plugin the content cache serves comes with a manifest or is held to a
// requirement: one the view serves — from a catalog directory, or pinned in the
// lockfile — is held to the re-hash alone, even where its bytes are in the cache
// too. record is the record the check reads — the file, or the record a resolution
// composed and has not yet written — and a nil record holds no entry. workspaceRoot
// and releasePath locate the cached bundles and the record a refusal names, as the
// build's own inputs do.
func VerdictPluginSignatureRefusals(view *catalog.LazyLookup, wsCfg workspaceconfig.Config, workspaceRoot, releasePath string,
	record *pluginstrust.Trust) []VerdictPluginSignatureRefusal {
	hold := newSignatureHold(wsCfg, workspaceRoot, releasePath)
	recordPath := pluginstrust.PathFor(releasePath)
	var refusals []VerdictPluginSignatureRefusal
	for _, plugin := range attachedVerdictPlugins(view, wsCfg.VerdictAttachments()) {
		read := plugin.read
		var err error
		if entry, held := record.Find(read.coordinate); held && entry.Signature != nil {
			err = hold.holdFact(read.coordinate, recordPath, entry.Signature, oci.MadeForManifest(read.manifestDigest), read.fromCache)
		} else {
			err = unrecordedVerdictPluginRefusal(wsCfg, read, plugin.attachmentURI, recordPath, releasePath)
		}
		if err != nil {
			refusals = append(refusals, VerdictPluginSignatureRefusal{Plugin: read.coordinate, Refusal: err})
		}
	}
	return refusals
}

// attachedVerdictPlugin is the plugin one attachment that returns an
// accept-or-reject verdict names, as the build reads it, and the reference that
// attachment was authored as — the reference the plugin's signature subject is
// bound from.
type attachedVerdictPlugin struct {
	read          verdictPluginRead
	attachmentURI string
}

// attachedVerdictPlugins reads the plugin each of attachments names
// (readVerdictPlugin over view), once per plugin, in attachment order: a plugin
// two attachments name is read, and its subject bound, through the first. A
// reading that cannot identify the plugin at all is left out, for the resolution
// that halts on it with its own diagnostic.
func attachedVerdictPlugins(view *catalog.LazyLookup, attachments []workspaceconfig.VerdictAttachment) []attachedVerdictPlugin {
	plugins := make([]attachedVerdictPlugin, 0, len(attachments))
	walked := make(map[string]bool, len(attachments))
	for _, attachment := range attachments {
		read, identified := readVerdictPlugin(view, attachment.Ref.URI)
		if !identified || walked[read.coordinate] {
			continue
		}
		walked[read.coordinate] = true
		plugins = append(plugins, attachedVerdictPlugin{read: read, attachmentURI: attachment.Ref.URI})
	}
	return plugins
}

// lockfileRows walks the first store: every reference the closure enumerates,
// joined to the row of its own section in lf, the Release's loaded lockfile.
func (h signatureHold) lockfileRows(lf *lockfile.Lockfile, cl closure.Closure) []error {
	// A section is named the same on both sides — the Lockfile field and the
	// closure's axis — so a closure reference finds its own row and never the row of
	// another section recording the same reference.
	recorded := lf.RecordedSignatures()
	lockfilePath := lockfile.PathFor(h.releasePath)
	var refusals []error
	for _, axis := range cl.VerifiableAxes() {
		for _, ref := range axis.Refs {
			if fact := recorded[lockfile.RowKeyFor(axis, ref)]; fact != nil {
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
				refusals = append(refusals, lockfile.SignatureNotRecorded(ref.URI, lockfilePath, requiredBy, h.releasePath))
			}
		}
	}
	return refusals
}

// verdictPlugins walks the second store: the plugin each attachment that returns
// an accept-or-reject verdict names, joined by its identity coordinate to its
// entry in the record beside the lockfile — the address that record keeps. The
// walk is VerdictPluginSignatureRefusals over view, the one a surface outside this
// package reads too; what this adds is which record it reads.
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
func (h signatureHold) verdictPlugins(view *catalog.LazyLookup, composed *pluginstrust.Trust) ([]error, error) {
	if len(h.wsCfg.VerdictAttachments()) == 0 {
		return nil, nil
	}
	record := composed
	if record == nil {
		read, _, err := pluginstrust.LoadOrStart(pluginstrust.PathFor(h.releasePath))
		if err != nil {
			return nil, err
		}
		record = read
	}
	var refusals []error
	for _, refused := range VerdictPluginSignatureRefusals(view, h.wsCfg, h.workspaceRoot, h.releasePath, record) {
		refusals = append(refusals, refused.Refusal)
	}
	return refusals, nil
}
