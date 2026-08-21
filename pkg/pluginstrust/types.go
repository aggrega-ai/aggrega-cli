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
// What the file deliberately does not carry:
//
//   - Rule inputs. A governance verdict attachment's external rule files are
//     recorded as content digests in the lockfile's verdict_inputs section; a
//     linter attachment's rule inputs are recorded in neither file.
//   - Signatures. Rotating a plugin is re-recording its identity here, not
//     managing an overlapping key window.
//   - Resolution provenance — no registry host, no resolution tier, and no
//     path on the machine that wrote the file. Recording where an artifact
//     came from would make the bytes machine-specific, which is the one thing
//     a committed record must not be.
//
// Which attachments are recorded is decided by the ATTACHMENT's role, through
// workspaceconfig.Config.VerdictAttachments — never by a property of the plugin
// itself. One plugin may be attached in a transform role at one site and a
// verdict role at another, so a per-plugin flag could not express the
// distinction and a lookup keyed by plugin identity alone would answer the
// wrong question.
package pluginstrust

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
