// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package pullops

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"

	"github.com/aggrega-ai/aggrega-cli/internal/pluginfetchverify"
	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/closure"
	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/pullverify"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
	"github.com/aggrega-ai/aggrega-cli/pkg/writescope"
)

// VerifyClosureSignatures runs the pull-time signature verification pass over
// every registry-resolved reference in the closure — catalog entities, the four
// library sections, and Wasm plugins — and folds the verified facts into the
// lockfile, caching each verified signature bundle as a content-addressed blob for
// air-gapped re-verification. It is called only on the pull write path — never on
// build, which reads the recorded facts from the lockfile and never
// re-verifies signatures (the AOT boundary).
//
// A library and a Wasm plugin are OCI artifacts like any other: each verifies
// through the same Verify call, against the same per-registry trust policy, as a
// catalog entity — there is no separate trust path per kind. Every axis is
// combined into one verification run so the per-registry policy resolution is
// provably identical for all of them.
//
// The axes are read from the closure's own enumeration rather than named here, so
// a section added to the closure cannot quietly bypass verification: it is either
// in that enumeration or recorded there as carrying no verifiable reference.
//
// A registry-resolved item is one freshly fetched from a registry (Tier-3).
// A catalog entity or a library carries a host-less bare identity — its registry
// host and path are bound at pull time from the workspace registry and the kind,
// recovered here from the same binding the puller used — while a content-pinned
// .wasm plugin already carries its host in the URI. All are verified against
// the resolved registry's trust policy. A Tier-1 / local / external item is not
// asked about: it never carries a registry signature. Where those bytes were
// fetched by this very pull, under another reference to the same artifact, the
// answer this pass got for them is recorded on the item's row too (see
// verifiedJoin), so every row pinning bytes this pull verified records that it
// did. A fatal verification failure (a required signature that is missing,
// untrusted, or invalid for a non-advisory registry) returns a non-nil error and
// halts the pull.
//
// A Tier-2 item — one the content cache served — was verified by the pull that
// fetched its bytes, and its row carries that pull's fact forward, so it is
// normally not asked about. It is asked about exactly where its row records no
// fact and the one decision a pull makes over a missing signature
// (workspaceconfig.SignatureCheck.HaltsWithoutSignature) says one is required:
// the row a build would refuse (lockfile.ErrSignatureNotRecorded). Such an item
// joins the fetched ones and is verified by the same call, by the manifest digest
// its resolution reports — the one its row records — so its fact is recorded and
// its bundle cached exactly as a fetched item's are, and the remedy that refusal
// names repairs the row. Asking means reaching the registry, so under the offline
// posture the network-egress gate refuses it, and the pull halts with that
// refusal rather than writing a row the build would refuse. A row that records a
// fact, and a row nothing requires a signature for, are not asked about, so a
// re-pull over a lockfile that records every required fact asks the registry
// nothing.
//
// Where an artifact carries several signatures that verify, the fact a row
// records is the earliest of them (pullverify.EarlierSignature) — the signature
// the registry index dates the version by and names as its signer — and the
// deprecation discovery below honors markers from that same signer as the
// original one. Which signature the registry happens to list first decides
// neither.
//
// Verification warning notices are written to diagOut (os.Stderr in production).
//
// targetFor resolves each verifiable item's OCI target for referrer discovery;
// production passes remoteTargetFor (a real remote repository per registry).
//
// Each verified bundle is cached beneath workspaceRoot, in the cacheDir the
// workspace configures, and that write is held to the workspace: see
// cacheBundleBlob.
//
// It is the one home of this pass, and it stays the one home now that it is
// reachable by name: the primitives it composes are not exported, so a caller
// outside this package can run the pass and cannot assemble a second copy of it
// from the parts.
func VerifyClosureSignatures(ctx context.Context, lf *lockfile.Lockfile, cl closure.Closure, wsCfg workspaceconfig.Config, workspaceRoot, cacheDir string, diagOut io.Writer, targetFor pullverify.TargetFor) error {
	axes := cl.VerifiableAxes()
	total := 0
	for _, axis := range axes {
		total += len(axis.Refs)
	}
	// The facts the rows record after the carry-forward, read by the same key the
	// build's check reads them by, so the rows asked about here are exactly the rows
	// that check would refuse.
	recorded := lf.RecordedSignatures()
	verifiable := make([]pullverify.Entity, 0, total)
	// The cache-served items to ask about are held apart and joined after the
	// fetched ones, below.
	var reasked []pullverify.Entity
	for _, axis := range axes {
		for _, e := range axis.Refs {
			// Only an item whose bytes were acquired from a registry carries a
			// registry signature; a local or external item never does.
			if !closure.AcquiredFromRegistry(e.ResolutionPath) {
				continue
			}
			// An item fetched during this run is always asked about. One the
			// content cache served is asked about only where its row records no
			// fact — a row recording one carries the fact the fetching pull
			// verified — and, below, only where a signature is required for it.
			fetched := isRegistryResolved(e.ResolutionPath)
			if !fetched && recorded[lockfile.RowKey{Section: axis.Axis, URI: e.URI}] != nil {
				continue
			}
			// A catalog entity's or library's URI is a host-less bare identity; the
			// registry host and path are bound at pull time from the workspace
			// registry and the kind, not carried in the URI. The binding is the one
			// the build's check of recorded signatures reads too, through the SAME
			// location rule the puller used, so the verify target is the location the
			// artifact was fetched from and the host whose policy a build later holds
			// the row to is the host verified here. A reference it binds to no
			// registry — an unbindable library, a reference with no registry host —
			// carries no registry signature and is skipped.
			subject, ok := catalog.SignatureSubjectOf(wsCfg.DefaultRegistryLocation().Prefix(), axis, e)
			if !ok {
				continue
			}
			// The cache-served row is asked about where its missing fact would halt:
			// the decision Verify makes over a missing signature, read for the same
			// host and kind, so a row the skips or an advisory registry admit is left
			// as it is and costs no registry request.
			if !fetched && !wsCfg.SignatureCheckFor(subject.Host, subject.Kind).HaltsWithoutSignature() {
				continue
			}
			// A registry-resolved item is verified against its OCI manifest digest,
			// the subject referrer discovery attaches signatures to. A registry item
			// that reached here with no manifest digest means the resolution tier
			// failed to record one — referrer discovery would resolve an empty
			// subject and fail cryptically. Halting loudly here (rather than skipping,
			// which would leave the item unverified under a require-signatures policy)
			// names the item and points at the missing digest.
			if e.ManifestDigest == "" {
				return fmt.Errorf("verify: registry artifact %q has no manifest digest to verify against; the resolution tier did not record one", e.URI)
			}
			item := pullverify.Entity{
				URI:            e.URI,
				ManifestDigest: e.ManifestDigest,
				Host:           subject.Host,
				Location:       subject.Location,
				Kind:           subject.Kind,
			}
			if fetched {
				verifiable = append(verifiable, item)
			} else {
				reasked = append(reasked, item)
			}
		}
	}
	// A cache-served item is asked about only where this pass does not already ask
	// the same question under another reference — one this run fetched, or a second
	// cache-served reference to the same artifact. The same question is one whose
	// answer cannot differ: the same manifest, asked at the same location under the
	// same host's policy for the same kind. Only the reference differs, and the fold
	// below records an answer on every row pinning that manifest (verifiedJoin), so
	// asking again would only repeat the registry request.
	type question struct{ manifest, host, location, kind string }
	asked := make(map[question]struct{}, len(verifiable)+len(reasked))
	for _, e := range verifiable {
		asked[question{e.ManifestDigest, e.Host, e.Location, e.Kind}] = struct{}{}
	}
	for _, e := range reasked {
		q := question{e.ManifestDigest, e.Host, e.Location, e.Kind}
		if _, held := asked[q]; held {
			continue
		}
		asked[q] = struct{}{}
		verifiable = append(verifiable, e)
	}
	if len(verifiable) == 0 {
		return nil
	}

	results, err := pullverify.Verify(ctx, verifiable, pullverify.Options{
		Config: wsCfg,
		Target: targetFor,
		Warn:   func(notice string) { fmt.Fprintf(diagOut, "  verify: %s\n", notice) },
	})
	if err != nil {
		return err
	}

	factByURI := make(map[string]pullverify.Result, len(results))
	for _, r := range results {
		factByURI[r.URI] = r
	}
	// The fold walks the lockfile's own enumeration of marker-bearing rows, so a
	// verified fact reaches every section that can record one without this pass
	// naming the sections, and it reaches every row pinning an artifact this pass
	// asked about, whichever reference the row records: see verifiedJoin.
	//
	// For a row whose artifact this pass asked the registry about, the answer it got
	// is the row's fact, and that includes getting none: a signature carried forward
	// from the prior lockfile is replaced, and cleared where this run found no
	// signature to record. A row whose artifact this pass did not ask about — one the
	// content cache or a local catalog served, pinned by no reference this run fetched
	// or asked about — keeps whatever was carried onto it.
	facts := joinVerified(verifiable, factByURI)
	for _, row := range lf.VerifiedFactRows() {
		if !facts.askedAbout(row) {
			continue
		}
		r, ok := facts.answer(row)
		if !ok {
			row.RecordSignature(nil)
			continue
		}
		if err := cacheBundleBlob(workspaceRoot, cacheDir, r.BundleDigest, r.Bundle); err != nil {
			return fmt.Errorf("cache signature bundle for %s: %w", r.URI, err)
		}
		row.RecordSignature(pluginfetchverify.SignatureFact(r))
	}

	// Retroactive deprecation discovery: at the same registry-touching moment,
	// discover a signed deprecation marker attached to each verifiable artifact,
	// verify the marker's OWN signature against the original signer OR the
	// registry's trusted deprecation identities, and record the verified marker
	// into the lockfile. Build reads the recorded fact and never re-discovers
	// (the ahead-of-time boundary).
	if err := discoverClosureDeprecations(ctx, lf, verifiable, factByURI, wsCfg, targetFor, diagOut); err != nil {
		return err
	}
	return nil
}

// verifiedJoin carries the answers one registry-touching pass got onto the lockfile
// rows they belong to. Both passes over the closure — the signature verification and
// the deprecation-marker discovery — fold through it, so the two facts reach the
// same rows by the same rule.
//
// A pass asks the registry once per reference it fetched, but what it learns is a
// fact about the artifact that reference resolved to, read off its manifest: a
// signature and a deprecation marker are both referrers of the manifest. One
// artifact can be pinned under more than one reference — written with its version in
// one place and without it in another — and only a reference fetched from the
// registry is asked about. Another reference to the same artifact is served from the
// content the first fetch cached, in the same pull, so the registry is never asked
// about it under that reference. The answer belongs to it all the same: it is an
// answer about the bytes the row pins. So a row takes the answer for its manifest
// digest whatever reference the row records; a join on the reference alone would
// leave a row pinned to bytes this pull verified with no record that it did.
//
// Where the pass holds an answer for the row's own reference, that one is taken, so a
// row keeps the answer for the reference that fetched it; otherwise the row takes the
// answer held for its manifest digest, the first in the order the pass asked.
type verifiedJoin[T any] struct {
	byURI         map[string]T
	byManifest    map[string]T
	askedURI      map[string]struct{}
	askedManifest map[string]struct{}
}

// joinVerified builds the join for one pass from the references it asked the
// registry about, in the order it asked, and its answers keyed by reference. A
// reference the pass asked about and holds no answer for is still asked: getting
// nothing is an answer.
func joinVerified[T any](asked []pullverify.Entity, answers map[string]T) verifiedJoin[T] {
	j := verifiedJoin[T]{
		byURI:         answers,
		byManifest:    make(map[string]T, len(asked)),
		askedURI:      make(map[string]struct{}, len(asked)),
		askedManifest: make(map[string]struct{}, len(asked)),
	}
	for _, e := range asked {
		j.askedURI[e.URI] = struct{}{}
		j.askedManifest[e.ManifestDigest] = struct{}{}
		a, ok := answers[e.URI]
		if !ok {
			continue
		}
		if _, held := j.byManifest[e.ManifestDigest]; !held {
			j.byManifest[e.ManifestDigest] = a
		}
	}
	return j
}

// askedAbout reports whether the pass asked the registry about the artifact the row
// pins, under the row's own reference or under any other.
func (j verifiedJoin[T]) askedAbout(row lockfile.VerifiedFactRow) bool {
	if _, ok := j.askedURI[row.URI]; ok {
		return true
	}
	if row.ManifestDigest == "" {
		return false
	}
	_, ok := j.askedManifest[row.ManifestDigest]
	return ok
}

// answer returns the pass's answer for the artifact the row pins, and false where it
// holds none.
func (j verifiedJoin[T]) answer(row lockfile.VerifiedFactRow) (T, bool) {
	if a, ok := j.byURI[row.URI]; ok {
		return a, true
	}
	if row.ManifestDigest != "" {
		if a, ok := j.byManifest[row.ManifestDigest]; ok {
			return a, true
		}
	}
	var none T
	return none, false
}

// verifyPluginHostFunctions re-derives each in-closure plugin's host functions
// from its freshly-cached, content-digest-verified .wasm and verifies the result
// matches the machine-owned record committed in the entity's
// spec.runtime.hostFunctions. It is called only on the pull path (write and
// verify), never on build, which reads the recorded closure and never re-derives
// (the ahead-of-time boundary).
//
// The check is defense-in-depth over the content-digest pin: the digest pin
// guarantees the bytes are the bytes the entity committed to, but it does not
// catch a binary published by something other than the aggrega publish flow
// (which would carry no record, or one that does not match its own imports) or a
// bug in the tooling that records it. Re-deriving from the verified bytes and
// comparing to the record catches both, and HALTS the pull on any divergence
// (ErrWasmHostFunctionsStampMismatch).
//
// The two sides of the lifecycle answer a divergence the same way and for
// different reasons. At publish there is an author present and a file to correct,
// so the refusal names the sync command that re-records the set. At pull there is
// neither: the registry artifact is immutable to the consumer, and a record that
// disagrees with the binary it ships with is evidence the pair was not produced
// by the publish flow at all. Both refuse; only the remedy differs.
//
// It iterates the artifact axis (registry-resolved .wasm pins), the same scope
// the signature verify covers: a host-less / local plugin carries no cached
// artifact and no registry record to re-derive against. The .wasm bytes are read
// once from the cache slot the artifact descent populated, keyed by the artifact
// URI and its content-digest pin. The single derivation primitive
// (wasm.DeriveHostFunctions) is the same one the recording tooling wrote with,
// publish verified against, and the loader registers from, so no two of those
// surfaces can drift.
//
// diagOut receives a per-plugin verification notice (os.Stderr in production).
func verifyPluginHostFunctions(ctx context.Context, cl *closure.Closure, loader closure.RecipeLoader, workspaceRoot, cacheDir string, diagOut io.Writer) error {
	_ = ctx
	// Collect the artifact URIs sorted so the verify order is deterministic and
	// independent of closure order.
	artifactURIs := make([]string, 0, len(cl.Plugins))
	entityByArtifact := make(map[string]string, len(cl.Plugins))
	pinByArtifact := make(map[string]string, len(cl.Plugins))
	for _, art := range cl.Plugins {
		if len(art.ReferencedFrom) == 0 {
			continue
		}
		artifactURIs = append(artifactURIs, art.URI)
		// The artifact is referenced from its Plugin entity; the first
		// referencing entity carries the stamp to verify against (every entity
		// pinning the same .wasm content shares its derived imports).
		entityByArtifact[art.URI] = art.ReferencedFrom[0]
		// The content pin is the closure ref's Digest — the cache slot key the
		// artifact descent wrote under. It is read from the recorded pin, not
		// re-parsed from the (digest-free) artifact URI.
		pinByArtifact[art.URI] = art.Digest
	}
	sort.Strings(artifactURIs)

	for _, artifactURI := range artifactURIs {
		entityURI := entityByArtifact[artifactURI]
		pinDigest := pinByArtifact[artifactURI]
		if pinDigest == "" {
			// No content pin recorded (a host-less / local plugin carries no cached
			// artifact); skip it, mirroring the signature verify's host-less skip.
			continue
		}

		entity, err := loader.LoadPlugin(entityURI)
		if err != nil {
			return fmt.Errorf("load plugin entity %q for host-function verify: %w", entityURI, err)
		}

		wasmBytes, err := oci.ReadCached(workspaceRoot, cacheDir, artifactURI, pinDigest)
		if err != nil {
			if errors.Is(err, oci.ErrFetchNotFound()) {
				// No cached artifact (offline / hermetic): nothing to re-derive
				// against, so there is no stamp to contradict.
				continue
			}
			return fmt.Errorf("read cached .wasm %s for host-function verify: %w", artifactURI, err)
		}

		if err := pluginfetchverify.VerifyHostFunctionStamp(entity, entityURI, artifactURI, wasmBytes, diagOut); err != nil {
			return err
		}
	}
	return nil
}

// cacheBundleBlob writes bundle to the content-addressed signature-bundle blob
// named by its digest under the workspace's cache, creating the `signatures/`
// subtree as needed. The blob location is spelled once in
// oci.SignatureBundlePath so the build path re-hashes the same slot this write
// populated. The write is idempotent: a blob already present (a re-pull of the
// same signature) is left untouched so the cache stays byte-stable.
//
// The blob is held, before anything is created, to the root the cache directory
// picks (writescope.RootForCache): the cache directory itself. A directory on the
// way to the blob, or the blob, that is a link leading out of the cache directory —
// or a cache directory leading out of the workspace — is refused with the halt the
// write-scope rule declares, rather than written through.
func cacheBundleBlob(workspaceRoot, cacheDir, bundleDigest string, bundle []byte) error {
	if bundleDigest == "" || len(bundle) == 0 {
		return nil
	}
	path := oci.SignatureBundlePath(workspaceRoot, cacheDir, bundleDigest)
	if err := writescope.CheckWrite(writescope.RootForCache(workspaceRoot, cacheDir), path); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if _, statErr := os.Stat(path); statErr == nil {
		return nil
	}
	return os.WriteFile(path, bundle, 0o644)
}

// isRegistryResolved reports whether a closure entity resolved by fetching from
// a registry during this run — the case where a signature is always verified at
// pull time. A cache resolution is verified only where its row records no fact a
// signature requirement asks for (VerifyClosureSignatures), and a local-catalog or
// external resolution never is.
//
// It delegates to the shared predicate rather than testing the tag itself: a
// string comparison here would keep compiling after the tag was respelled and
// then silently stop matching, leaving signatures unverified with nothing failed
// and nothing logged.
func isRegistryResolved(resolutionPath string) bool {
	return closure.ResolvedFromRegistry(resolutionPath)
}
