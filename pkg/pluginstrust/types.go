// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Package pluginstrust parses and writes `plugins-trust.yaml`, the committed
// record of the plugin identity trusted for each attachment that returns an
// accept-or-reject verdict — a `plugins.governance` attachment bound to an
// `ir-verdict` or `render-verdict` role, and every `plugins.linter` attachment.
// The file sits beside the lockfile and is committed with it.
//
// The record is addressed by the plugin's identity COORDINATE
// (types.PluginCoordinate — `<pluginType>/<name>`), with the selected version
// and the artifact digest carried as fields of the record. That split is
// load-bearing rather than cosmetic. The coordinate carries no version, so
// re-versioning an attachment edits a field of the record that already exists;
// were the record addressed by the authored reference instead, a version edit
// would produce a DIFFERENT address, miss the existing record entirely, and be
// written as a new one — leaving a changed version indistinguishable from a
// first sighting. Addressing by coordinate is what makes the two
// distinguishable at all.
//
// Besides the identity, a record carries two facts about the artifact that
// identity names, each recorded only when it was verified: the withdrawal a
// publisher has attached to it, and the verified signature fact the pull that
// cached the plugin recorded — the same lockfile.VerifiedSignature a lockfile row
// carries, under the same key. Each is a fact about the recorded artifact rather
// than a second identity, which is what lets it be recorded without weakening the
// hold the identity fields place on a resolution — see Deprecation and Signature.
//
// What the file deliberately does not carry:
//
//   - Rule inputs. A governance verdict attachment's external rule files are
//     recorded as content digests in the lockfile's verdict_inputs section; a
//     linter attachment's rule inputs are recorded in neither file.
//   - A signature over the record itself, or a key. Rotating a plugin is
//     re-recording its identity here, not managing an overlapping key window. The
//     signature fact above records which signer a pull verified for the artifact;
//     it signs nothing in this file. That extends to the signer of a withdrawal:
//     only a signature-verified withdrawal is recorded at all, so recording who
//     signed it would add provenance without adding trust.
//   - Resolution provenance — no registry host, no resolution tier, and no
//     path on the machine that wrote the file. Recording where an artifact
//     came from would make the bytes machine-specific, which is the one thing
//     a committed record must not be.
//   - Any wall-clock moment. The file carries no stamp at all, so an unchanged
//     resolution produces the same bytes over time as well as across machines.
//     A withdrawal is therefore reported as recorded at pull without naming when,
//     which is a narrower claim than the lockfile's and a deliberate one.
//
// Which attachments are recorded is decided by the ATTACHMENT's role, through
// workspaceconfig.Config.VerdictAttachments — never by a property of the plugin
// itself. One plugin may be attached in a transform role at one site and a
// verdict role at another, so a per-plugin flag could not express the
// distinction and a lookup keyed by plugin identity alone would answer the
// wrong question.
package pluginstrust

import "github.com/aggrega-ai/aggrega-cli/pkg/lockfile"

// Trust is the typed Go representation of `plugins-trust.yaml`.
//
// Field declaration order drives yaml.v3's canonical key emission and is
// alphabetical, matching the discipline the lockfile beside this file follows. A
// field added later goes in its alphabetical position.
type Trust struct {
	Plugins []TrustedPlugin `yaml:"plugins"`
	Version int             `yaml:"version"`
}

// TrustedPlugin is one record: the plugin identity trusted for a
// verdict-returning attachment. Plugin is the address; ResolvedVersion and
// Digest are the recorded facts a later resolution is compared against.
//
// A plugin attached at two verdict roles at once contributes ONE record, because
// the recorded subject is the plugin's identity rather than the attachment: both
// attachments read the same artifact.
//
// Field declaration order is alphabetical to drive yaml.v3's canonical key
// emission, matching the Lockfile-wide discipline.
type TrustedPlugin struct {
	// Deprecation is the verified withdrawal marker discovered for the artifact
	// Digest names, or nil when none was. It is optional and omitted from the
	// written bytes when absent, so a record for an artifact nobody has withdrawn
	// is byte-identical to one written before this field existed.
	Deprecation *Deprecation `yaml:"deprecation,omitempty"`
	// Digest is the content digest of the resolved plugin artifact
	// (`sha256:...`).
	Digest string `yaml:"digest"`
	// Plugin is the plugin's identity coordinate, `<pluginType>/<name>`, derived
	// through the single types.PluginCoordinate rule — the same identity
	// lockfile.MutatorAttachment.Plugin and lockfile.ProviderBinding.Plugin
	// record. It deliberately carries no version.
	Plugin string `yaml:"plugin"`
	// ResolvedVersion is the concrete version selected for this plugin when the
	// record was written.
	ResolvedVersion string `yaml:"resolved_version"`
	// Signature is the verified signature fact the pull that cached the plugin
	// recorded for the artifact Digest names, or nil when that pull verified no
	// signature. It is the lockfile row's own fact type, so the two records cannot
	// come to describe one verified signature in two shapes. A build reads it before
	// the plugin runs: it re-hashes the cached bundle the fact names, and where the
	// trust configuration requires a signature for a plugin the content cache
	// serves, a record carrying none refuses the build. It is optional and omitted
	// from the written bytes when absent, so a record for an unsigned artifact is
	// byte-identical to one written before this field existed.
	Signature *lockfile.VerifiedSignature `yaml:"signature,omitempty"`
}

// Deprecation is a verified withdrawal a publisher attached to a trusted plugin's
// artifact after publishing it: the severity in the ordered set
// warning < error < retracted, and the marker's own message.
//
// It carries no signer, and that is the record's own contract rather than an
// omission: a withdrawal is recorded only when its signature verified, so the
// signer adds provenance to a fact whose trust was already established, and this
// file records no provenance of any kind. It also carries no observation moment,
// because the file carries no wall-clock stamp at all — which is what makes an
// unchanged resolution byte-identical over time.
//
// Field declaration order is alphabetical to drive yaml.v3's canonical key
// emission, matching the discipline the rest of this file follows.
type Deprecation struct {
	// Message is the withdrawal's recorded human-readable message, which is the
	// marker's own message or, where it carried none, its reason.
	Message string `yaml:"message"`
	// Severity is the recorded severity: warning, error or retracted.
	Severity string `yaml:"severity"`
}

// Equal reports whether p and q record the same thing.
//
// It exists because TrustedPlugin stopped being comparable with `==` the moment it
// gained a pointer field: `==` still compiles and still type-checks, but it compares
// the POINTER rather than what it addresses, so two records carrying identical
// withdrawals would be reported as differing. Every caller asking "has this record
// moved" asks here, so that trap has one answer rather than one per call site. Each
// pointer field is compared by what it addresses, and a field added later owes the
// same arm here.
func (p TrustedPlugin) Equal(q TrustedPlugin) bool {
	if p.Plugin != q.Plugin || p.ResolvedVersion != q.ResolvedVersion || p.Digest != q.Digest {
		return false
	}
	return sameAddressed(p.Deprecation, q.Deprecation) && sameAddressed(p.Signature, q.Signature)
}

// sameAddressed reports whether two optional facts record the same thing: both
// absent, or both present and equal.
func sameAddressed[T comparable](a, b *T) bool {
	switch {
	case a == nil && b == nil:
		return true
	case a == nil || b == nil:
		return false
	default:
		return *a == *b
	}
}

// CurrentVersion is the trust-record schema version this package reads and
// writes. It matches the `const` the schema pins at the document's `version`
// key, so a record this package writes validates against the schema it was
// written for.
const CurrentVersion = 1

// Find returns the record addressed by coordinate, and whether one exists.
//
// It is the single home of the address rule: a caller comparing a freshly
// resolved plugin against the record asks here rather than scanning the slice
// itself, so "addressed by coordinate" has one implementation and cannot be
// re-answered as a scan over some other field.
//
// A nil receiver holds no records and reports none, so a caller that has not yet
// loaded a file need not branch before asking.
func (t *Trust) Find(coordinate string) (TrustedPlugin, bool) {
	if t == nil {
		return TrustedPlugin{}, false
	}
	for _, p := range t.Plugins {
		if p.Plugin == coordinate {
			return p, true
		}
	}
	return TrustedPlugin{}, false
}

// Upsert records p, replacing any record already held at p.Plugin.
//
// Replacement rather than append is the address rule's write side: two records
// sharing a coordinate would make Find's answer depend on slice order, and a
// record that disagreed with its own duplicate could never be compared against
// anything. A record whose coordinate is empty addresses nothing and is
// dropped — an empty address would collide with every other empty one.
func (t *Trust) Upsert(p TrustedPlugin) {
	if p.Plugin == "" {
		return
	}
	for i := range t.Plugins {
		if t.Plugins[i].Plugin == p.Plugin {
			t.Plugins[i] = p
			return
		}
	}
	t.Plugins = append(t.Plugins, p)
}

// RecordDeprecation records d as the withdrawal of the artifact digest names, on
// the entry addressed by coordinate, and reports whether the record changed.
//
// It is the single home of the rule that a withdrawal follows ARTIFACT IDENTITY
// rather than the pin decision, and every part of that rule is here rather than at
// a call site:
//
//   - The digest must match the one the entry already records. A withdrawal is a
//     fact about one artifact, so recording one discovered for a different artifact
//     against this entry would attach it to bytes it says nothing about. A resolution
//     reporting a different digest is a change of IDENTITY, which is the entry's own
//     business and governed by the entry's own rule — a run that did not ask for it
//     is refused rather than reaching here.
//   - Recording is therefore not adoption, which is what lets it happen on an
//     ordinary pull. The identity fields are untouched; only a new fact about the
//     artifact those fields already name is added. Gating this on the pin decision
//     instead would leave the common case unreachable: a publisher withdraws an
//     artifact a workspace already trusts, so the entry is always already held by
//     the time there is anything to record.
//   - A nil d records nothing and clears nothing. A withdrawal that has stopped
//     being published is not chased: the record says what was seen for these bytes,
//     and the only way a withdrawal leaves is with its entry, on the explicit
//     upgrade that replaces or prunes it.
//
// A nil receiver, an empty coordinate and an unheld coordinate all record nothing
// and report false, the same contract Find, Upsert and Remove offer at their edges.
func (t *Trust) RecordDeprecation(coordinate, digest string, d *Deprecation) bool {
	if t == nil || coordinate == "" || d == nil {
		return false
	}
	for i := range t.Plugins {
		if t.Plugins[i].Plugin != coordinate || t.Plugins[i].Digest != digest {
			continue
		}
		if held := t.Plugins[i].Deprecation; held != nil && *held == *d {
			return false
		}
		recorded := *d
		t.Plugins[i].Deprecation = &recorded
		return true
	}
	return false
}

// RecordSignature records sig as the verified signature fact of the artifact
// digest names, on the entry addressed by coordinate, and reports whether the
// record changed.
//
// It follows ARTIFACT IDENTITY for the reason RecordDeprecation does, and holds to
// the same digest match: a signature fact is about one artifact's bytes, so it is
// recorded only on the entry already naming those bytes, and recording it adopts
// nothing — the identity fields are untouched, which is what lets an ordinary pull
// record it.
//
// Unlike a withdrawal, a nil sig CLEARS the recorded fact, and that is the
// difference between how the two facts are discovered. A pull verifies the
// signature of every verdict-role plugin it resolves, on every run, so its answer
// — including finding no signature to record, or being told by the configuration
// not to look — is the current answer for those bytes and replaces whatever the
// entry held. Keeping an earlier answer instead would leave the entry naming a
// bundle nothing re-caches, which the build then refuses as missing.
//
// A nil receiver, an empty coordinate and an unheld coordinate all record nothing
// and report false, the same contract RecordDeprecation offers at its edges.
func (t *Trust) RecordSignature(coordinate, digest string, sig *lockfile.VerifiedSignature) bool {
	if t == nil || coordinate == "" {
		return false
	}
	for i := range t.Plugins {
		if t.Plugins[i].Plugin != coordinate || t.Plugins[i].Digest != digest {
			continue
		}
		if sameAddressed(t.Plugins[i].Signature, sig) {
			return false
		}
		if sig == nil {
			t.Plugins[i].Signature = nil
			return true
		}
		recorded := *sig
		t.Plugins[i].Signature = &recorded
		return true
	}
	return false
}

// Remove drops the record addressed by coordinate and reports whether one was
// held. It is the only removal this type offers.
//
// It is separate from Upsert rather than an Upsert of an absent value, because
// the two are different acts on the file: Upsert says what a plugin's trusted
// identity now is, and Remove says the file no longer speaks about that plugin
// at all. Folding removal into the write side would make "record nothing for
// this coordinate" and "record this coordinate as having no identity"
// indistinguishable at the call site, and the second of those is not a state the
// record can hold — every field of a record is required.
//
// Removal is by coordinate alone, with no version or digest to match, because
// the coordinate IS the address: Upsert admits at most one record per
// coordinate, so the first match is the only one and stopping there is complete
// rather than a shortcut.
//
// A nil receiver holds nothing and removes nothing, and an empty coordinate
// addresses nothing, so both report false without touching the records — the
// same contract Find and Upsert offer at their own edges.
func (t *Trust) Remove(coordinate string) bool {
	if t == nil || coordinate == "" {
		return false
	}
	for i := range t.Plugins {
		if t.Plugins[i].Plugin != coordinate {
			continue
		}
		t.Plugins = append(t.Plugins[:i], t.Plugins[i+1:]...)
		return true
	}
	return false
}
