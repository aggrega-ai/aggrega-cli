// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package lockfile

import (
	"errors"
	"fmt"
	"sort"

	"github.com/aggrega-ai/aggrega-cli/pkg/closure"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
)

// ErrLockfileDrift wraps every drift detection failure raised by
// Verify and by VerifySourceReferences. Errors satisfying
// errors.Is(err, ErrLockfileDrift()) carry a
// wrapped message naming every offending URI surfaced across the
// closure axes (entities, schema libraries, mapping libraries,
// workload libraries, artifact libraries, Wasm plugins), every Release
// file edited since the lockfile was written, or — from
// VerifySourceReferences — every source reference whose authored ref now
// resolves elsewhere. Each axis collects all of its drift entries before
// returning, so an operator with multiple drifts sees the full list
// in one diagnostic rather than one URI per iteration. The
// documented remediation is
// `aggrega pull --upgrade --release <release>`; an edited Release file
// needs only `aggrega pull --release <release>`, which its entry says.
//
// The two raise sites report one condition rather than two, because what an
// operator is told and what they do about it are the same in both: a pin
// recorded in the lockfile no longer describes what resolves now, and an
// upgrade is how it is adopted. Splitting them would give one remediation two
// identities.
//
// Cross-tier digest semantics: an entity row records one subject at
// every tier — what a publication of the entity carries, each file by
// its path, its bytes and whether it is executable. A catalog-directory
// entity is digested over its declared files and a fetched or cached one
// over the entries of the bytes it was published as, so identical content
// does not drift between tiers, between checkouts that differ only in
// permission bits, or between two compressions of the same entries; drift
// surfaces when the content changes (an edited entity, a chart edited
// under it, a file added to a source it declares, a flipped execute bit,
// a re-published tag). A library row records its document's bytes, the
// same at every tier.
func ErrLockfileDrift() error { return errLockfileDrift }

var errLockfileDrift = cerrors.Register("lockfile-drift", cerrors.Meta{
	ShortCode: "E0130",
	Message:   "lockfile: out of date; it no longer matches what its Release resolves to",
	RulePlace: cerrors.RuleNoStageReaches("Composing a build's inputs evaluates the rule, as a lockfile that " +
		"already existed is held to the closure just computed, across every axis it pins. The walk pins " +
		"what a Release reaches rather than compiling it, so no declared stage of a compilation holds " +
		"either side of that comparison."),
})

// Verify checks every URI in the freshly-computed closure against
// the loaded lockfile's pinned (version, digest). Returns nil when
// every closure URI has a matching lockfile entry; returns an error
// wrapping ErrLockfileDrift when any axis surfaces drift — every
// drift entry across every axis is reported in one joined error so
// operators see the full picture.
//
// The axes it walks are the calls below, which are the statement; a count
// written beside them would be a second copy that goes stale the moment an axis
// is added. One of them is not a pinned reference at all: the Release files the
// resolution read, compared by content, so a Release edited after the pull that
// wrote the lockfile is reported here — at the same moment, and as the same
// condition, as any other drift — rather than surfacing later as whatever the
// edit makes the render fail on.
//
// SourceArtifacts are not walked here, and the reason is the shape of the
// argument rather than a judgement about their importance: every axis above is
// compared against `expected`, which is a closure some caller computed. The
// source-artifact axis is compared by VerifySourceReferences instead, because
// the question it answers — does the authored reference still resolve to the
// recorded commit — cannot be answered from a closure at all. It needs a fresh
// answer from wherever the reference points, and a caller holding a closure may
// have obtained one or may not.
func Verify(lf *Lockfile, expected closure.Closure) error {
	var driftErrs []error
	driftErrs = append(driftErrs, collectDrift("entity",
		driftKeysOf(lf.Entities, entityKey), refKeys(expected.Entities))...)
	// The library axes report in declared order, each under its own operator-facing
	// label, so the labels here cannot disagree with the ones the rest of the
	// toolchain uses for the same axis.
	for _, sec := range librarySections() {
		driftErrs = append(driftErrs, collectDrift(sec.axis.Label,
			sec.keys(lf), libraryKeys(sec.axis.Libraries(&expected)))...)
	}
	driftErrs = append(driftErrs, collectDrift("wasm-plugin",
		driftKeysOf(lf.Plugins, pluginKey), refKeys(expected.Plugins))...)
	driftErrs = append(driftErrs, releaseDocumentDrift(lf.ReleaseDocuments, expected.ReleaseDocuments)...)
	if len(driftErrs) == 0 {
		return nil
	}
	return fmt.Errorf("%w: %w", ErrLockfileDrift(), errors.Join(driftErrs...))
}

// releaseDocumentDrift reports every way the Release files the lockfile records
// disagree with the files the Release is composed from now: a file edited since
// the lockfile was written, a file the lockfile does not record — including every
// file of a lockfile written before it recorded any — and a recorded file the
// Release no longer reads.
//
// It is not collectDrift, because what it compares is not the pinned pair that
// function compares: a Release file has no resolved version, only content, and its
// entries name a file the reader edits rather than a reference the catalog serves.
//
// The remediation rides on each entry, for the reason VerifySourceReferences gives:
// a joined chain is rendered element by element and the enclosing frame's text is
// not shown. It is a plain pull rather than an upgrade, because nothing needs to
// float: a pull reads the Release as it now stands, re-resolves what it asks for
// while honouring every other pin, and records the file again.
func releaseDocumentDrift(have []ReleaseDocument, want []closure.ReleaseDocument) []error {
	const remedy = "run `aggrega pull --release <release>` to resolve the Release as it now stands and record it"
	recorded := make(map[string]string, len(have))
	for _, d := range have {
		recorded[d.Path] = d.Digest
	}
	seen := make(map[string]struct{}, len(want))
	var out []error
	// want is walked in its own order — the Release, then each Release it
	// extends — so the entries come out in a fixed order for a given pair of inputs.
	for _, w := range want {
		seen[w.Path] = struct{}{}
		was, ok := recorded[w.Path]
		switch {
		case !ok:
			out = append(out, fmt.Errorf("release document %q is not recorded in the lockfile, so an edit to it "+
				"cannot be noticed; %s", w.Path, remedy))
		case was != w.Digest:
			out = append(out, fmt.Errorf("release document %q was edited after the lockfile was written: "+
				"its content no longer matches the recorded digest (lockfile=%s current=%s), so the pins may no "+
				"longer describe what it asks for; %s", w.Path, was, w.Digest, remedy))
		}
	}
	for _, path := range sortedExtras(recorded, seen) {
		out = append(out, fmt.Errorf("release document %q is recorded in the lockfile but the Release no longer "+
			"reads it; %s", path, remedy))
	}
	return out
}

// sourceRefKey joins a recorded source reference to a freshly-resolved one.
//
// It is the source-artifact identity MINUS the pinned version, plus the authored
// ref — and both halves of that are load-bearing. Dropping the pinned version is
// what makes the join possible at all: the pinned version is part of the identity
// a resolution computes, so a reference that moved produces a DIFFERENT identity
// rather than a changed one, and a comparison keyed on the full identity would
// find no prior row and read the moved reference as a brand-new artifact. Adding
// the authored ref is what keeps the comparison honest in the other direction: an
// author who edits `ref:` from one tag to another has changed what they asked
// for, which is not drift, and only the ref distinguishes that from the same ref
// resolving elsewhere.
type sourceRefKey struct {
	Kind         string
	URL          string
	Chart        string
	ModuleSubdir string
	RequestedRef string
}

// VerifySourceReferences reports every source reference whose authored ref now
// resolves to a different version than the one the lockfile pins for it.
//
// prior is the lockfile as it stands; resolved is what a fresh resolution of the
// same references produced. A nil prior — a run that read no prior lockfile, which
// is what a full refresh is — reports nothing, so `aggrega pull --upgrade` adopts a
// moved reference rather than refusing it. That is not a special case written
// here: it falls out of the prior lockfile being the only thing this compares
// against, which is why there is no upgrade flag in this signature.
//
// # Which rows participate, and why the scope is drawn on the recorded ref
//
// Only a row carrying a `requested_ref:` is compared, on both sides. That field
// records that the Recipe authored a reference — a name that points at content
// rather than naming it — which is exactly the condition under which the pinned
// version can move while the authored document stays byte-identical. A row
// without one pins what its author wrote, so a change there is an edit to the
// Recipe and not drift.
//
// The scope therefore follows what the lockfile records rather than naming a
// source kind, and it stays correct as kinds are added: whichever adapter
// resolves an authored reference records the reference it resolved, and is
// compared on that.
//
// # The bound, stated because it is a property of the record rather than of this
//
// A Recipe that authors no reference at all records no `requested_ref:`, so the
// lockfile carries no evidence that a reference was involved and there is nothing
// here to join on. That is a limit of what is recorded, not of this comparison,
// and widening the join to cover it would refuse a legitimate author edit: a row
// with no recorded ref is indistinguishable from one whose author simply wrote a
// different version.
//
// # What is compared, and what deliberately is not
//
// The pinned version alone. A git commit names immutable content, so comparing
// the content digest at an unchanged commit adds no signal and would turn a
// change in how an archive is encoded into reported drift.
func VerifySourceReferences(prior *Lockfile, resolved []closure.SourceArtifact) error {
	if prior == nil {
		return nil
	}
	pinned := make(map[sourceRefKey]string, len(prior.SourceArtifacts))
	for _, sa := range prior.SourceArtifacts {
		for _, ref := range sa.ReferencedBy {
			if ref.RequestedRef == "" {
				continue
			}
			k := sourceRefKey{sa.Kind, sa.URL, sa.Chart, sa.ModuleSubdir, ref.RequestedRef}
			if _, already := pinned[k]; !already {
				pinned[k] = sa.PinnedVersion
			}
		}
	}
	if len(pinned) == 0 {
		return nil
	}

	var moved []error
	reported := make(map[sourceRefKey]struct{}, len(pinned))
	// resolved is walked in its own order, which the descent already sorts, so the
	// entries below come out in a fixed order for a given pair of inputs.
	for _, sa := range resolved {
		for _, ref := range sa.ReferencedBy {
			if ref.RequestedRef == "" {
				continue
			}
			k := sourceRefKey{sa.Kind, sa.URL, sa.Chart, sa.ModuleSubdir, ref.RequestedRef}
			was, ok := pinned[k]
			if !ok || was == sa.PinnedVersion {
				continue
			}
			// One reference reached by two Recipes is one moved reference, not two.
			if _, done := reported[k]; done {
				continue
			}
			reported[k] = struct{}{}
			// The remediation rides on each entry rather than on the enclosing
			// error, and that is forced rather than stylistic: a joined chain is
			// rendered element by element and the enclosing frame's own text is
			// not shown, so advice written there reaches nobody. Each element is
			// an independently-actionable finding, which is what one moved
			// reference is.
			moved = append(moved, fmt.Errorf(
				"source-artifact %s: reference %q moved — the lockfile pins %s, and it now resolves to %s. "+
					"Adopt where it now points with `aggrega pull --upgrade --release <release>`",
				sourceArtifactLabel(sa), ref.RequestedRef, was, sa.PinnedVersion))
		}
	}
	if len(moved) == 0 {
		return nil
	}
	return fmt.Errorf("%w: %w", ErrLockfileDrift(), errors.Join(moved...))
}

// sourceArtifactLabel names a source artifact in the identity terms an author
// wrote, omitting the two selector terms where the kind does not use them, so the
// line a reader sees carries no empty fields to decode.
func sourceArtifactLabel(sa closure.SourceArtifact) string {
	label := fmt.Sprintf("kind=%s url=%s", sa.Kind, sa.URL)
	if sa.Chart != "" {
		label += " chart=" + sa.Chart
	}
	if sa.ModuleSubdir != "" {
		label += " module_subdir=" + sa.ModuleSubdir
	}
	return label
}

// driftKey is the identity triple every axis is compared on: the reference that
// joins a lockfile row to a closure member, and the pinned pair that must match.
type driftKey struct {
	URI             string
	ResolvedVersion string
	Digest          string
}

func entityKey(e Entity) driftKey {
	return driftKey{URI: e.URI, ResolvedVersion: e.ResolvedVersion, Digest: e.Digest}
}

func pluginKey(p Plugin) driftKey {
	return driftKey{URI: p.URI, ResolvedVersion: p.ResolvedVersion, Digest: p.Digest}
}

func refKeys(refs []closure.ResolvedRef) []driftKey {
	return driftKeysOf(refs, func(r closure.ResolvedRef) driftKey {
		return driftKey{URI: r.URI, ResolvedVersion: r.ResolvedVersion, Digest: r.Digest}
	})
}

func libraryKeys(libs []closure.ResolvedLibrary) []driftKey {
	return driftKeysOf(libs, func(l closure.ResolvedLibrary) driftKey {
		return driftKey{URI: l.URI, ResolvedVersion: l.ResolvedVersion, Digest: l.Digest}
	})
}

// collectDrift reports every way one axis's lockfile rows disagree with the
// closure just computed: a reference the lockfile does not pin, a reference whose
// pinned pair moved, and a pinned reference the release no longer reaches.
//
// It is written once and given the axis's label, so every axis reports the same
// three conditions in the same words. A per-axis copy is how one axis comes to
// report a condition its siblings do not — and drift the operator is never told
// about is drift they cannot act on.
func collectDrift(label string, have, want []driftKey) []error {
	haveByURI := make(map[string]driftKey, len(have))
	for _, h := range have {
		haveByURI[h.URI] = h
	}
	seen := make(map[string]struct{}, len(want))
	var out []error
	for _, w := range want {
		seen[w.URI] = struct{}{}
		h, ok := haveByURI[w.URI]
		if !ok {
			out = append(out, fmt.Errorf("%s %q not in lockfile", label, w.URI))
			continue
		}
		if h.ResolvedVersion != w.ResolvedVersion || h.Digest != w.Digest {
			out = append(out, fmt.Errorf("%s %q changed: lockfile=(%s,%s) current=(%s,%s)",
				label, w.URI, h.ResolvedVersion, h.Digest, w.ResolvedVersion, w.Digest))
		}
	}
	for _, uri := range sortedExtras(haveByURI, seen) {
		out = append(out, fmt.Errorf("%s %q no longer used by the release", label, uri))
	}
	return out
}

func sortedExtras[T any](have map[string]T, seen map[string]struct{}) []string {
	var extras []string
	for uri := range have {
		if _, ok := seen[uri]; !ok {
			extras = append(extras, uri)
		}
	}
	sort.Strings(extras)
	return extras
}
