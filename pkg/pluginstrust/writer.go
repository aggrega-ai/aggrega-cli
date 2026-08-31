// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package pluginstrust

import (
	"fmt"
	"os"
	"sort"

	"go.yaml.in/yaml/v3"
)

// header is the comment block every written record opens with. It is part of the
// file's canonical bytes, so editing this text shows up as a diff on the next
// write that actually happens — which is the intended behaviour for a committed
// file: the reader of a pull request sees the explanation change alongside
// whatever else changed.
//
// That next write is not guaranteed to come. A pull writes only when it moves the
// record's content, so a committed record whose entries never change keeps the
// header it was written with, and two workspaces can hold different header text
// for the same record version. No consumer reads the header, so the cost is
// confined to the wording a reader sees; it is stated here because the
// diff-on-next-write behaviour above otherwise reads as a promise that every
// committed record catches up on its own.
//
// It states what the record carries, what it does not, where the rest lives, who
// writes it and who reads it, and why committing it matters. Each of those is
// there because its absence is a question the reader of an unfamiliar generated
// file has to answer from somewhere else.
const header = `# Aggrega plugin trust record — written by "aggrega pull", read by every
# command that runs one of those plugins.
# Commit this file. Do not edit it by hand.
#
# WHAT IT RECORDS. For every plugin attached in a role that returns an
# accept-or-reject verdict — a plugins.governance attachment bound to an
# ir-verdict or render-verdict role, and every plugins.linter attachment — the
# identity resolved for it: the plugin's coordinate, the version selected, and
# the content digest of the artifact behind that version. The coordinate carries
# no version, so re-versioning an attachment edits resolved_version on the
# record it already has rather than adding a second one. That is what lets a
# build tell a re-versioned plugin from one it has never seen.
#
# It also records a "deprecation" block on a plugin whose publisher has
# deprecated the artifact behind the recorded digest after publishing it, when
# the marker carried a signature that verified. That is a fact about the
# artifact already recorded rather than a second identity, which is why a plain
# "aggrega pull" adds one while still leaving the identity alone. Every command
# that reports on this file names such a plugin, because a deprecated gate is
# otherwise fetched, cached and run with nothing said. A recorded marker leaves
# only with its record.
#
# WHAT IT DOES NOT RECORD. The rule inputs a verdict plugin reads. A
# plugins.governance verdict attachment's external rule files are recorded as
# digests in the lockfile beside this file, under verdict_inputs; a
# plugins.linter attachment's rule inputs are recorded in neither file. This
# record also carries no signature — including the one on a deprecation marker,
# which is verified before the marker is recorded at all — and no resolution
# provenance, no registry host, no path on the machine that wrote it, and no
# clock reading, so the same resolution produces the same bytes wherever and
# whenever it runs.
#
# WHERE THE REST LIVES. Everything a build reproduces byte-for-byte is pinned in
# the lockfile beside this file. A verdict attachment shapes none of the
# rendered output, which is why its identity is recorded here instead.
#
# "aggrega pull" creates this file the first time it resolves one of these
# attachments, and adds a record per attachment as it resolves it — so a file
# covering only some of the configured attachments is an expected intermediate
# state rather than a damaged one. A command that runs one of these plugins
# compares what it resolves against these records and stops when the two
# disagree. Committing the file is
# what makes that comparison reviewable: a record kept outside version control
# is one nobody can inspect and nothing can compare against.
#
# To add an identity, attach the plugin under plugins.governance or
# plugins.linter in .aggrega-config.yaml and run "aggrega pull".
#
# To change or remove one, say so explicitly: "aggrega pull --upgrade" naming a
# plugin adopts the identity it resolves to now, or drops its entry when nothing
# attaches it any more. A plain "aggrega pull" never rewrites and never removes
# an entry — a build that found this record disagreeing with what it resolved
# would otherwise be fixed by the next pull instead of being reviewed.

`

// Write writes t to path in canonical (deterministic) form.
//
// Canonical form:
//   - The generated header block, verbatim, first.
//   - Top-level keys in alphabetical order (driven by struct field declaration
//     order in types.go).
//   - Records ordered by plugin coordinate.
//
// Determinism scope is stronger than the lockfile's: the record carries no
// wall-clock stamp, so the same resolution produces the same bytes over time as
// well as across two invocations. That is why there is no timestamp-preserving
// variant of this function — a re-write of an unchanged resolution is already
// byte-identical, so nothing has to be carried over from the prior file.
//
// Write deep-copies t before ordering it, so the caller's value is not mutated.
func Write(path string, t *Trust) error {
	if t == nil {
		return fmt.Errorf("pluginstrust: Write called with nil *Trust")
	}
	out, err := marshalCanonical(t)
	if err != nil {
		return fmt.Errorf("pluginstrust: marshal %s: %w", path, err)
	}
	if err := os.WriteFile(path, out, 0o644); err != nil {
		return fmt.Errorf("pluginstrust: write %s: %w", path, err)
	}
	return nil
}

// marshalCanonical returns the exact bytes Write writes. It is the single
// serialization path, so any caller comparing bytes against what is on disk
// compares against what would actually be written rather than against a second
// rendering that could drift from it.
func marshalCanonical(t *Trust) ([]byte, error) {
	body, err := yaml.Marshal(canonicalise(t))
	if err != nil {
		return nil, err
	}
	out := make([]byte, 0, len(header)+len(body))
	out = append(out, header...)
	return append(out, body...), nil
}

// canonicalise returns a deep copy of t with its records ordered by coordinate.
// The caller's value is not mutated: the records slice is reallocated, so the
// sort below cannot reach back into the input.
//
// Maintenance hazard, and it is the same one the lockfile writer beside this file
// documents: any field added to TrustedPlugin that holds a SLICE or a POINTER
// must be cloned here explicitly, because a struct copy clones scalar fields while
// SHARING slice headers and pointers — so without the clone a caller mutating the
// output would reach back into the input. TrustedPlugin holds one pointer field
// today, Deprecation, and the loop below clones it; a further composite field owes
// the same treatment. The same reaches a field added to Trust itself: the literal
// below copies Version by value and assigns Plugins explicitly, so a newly added
// composite field left out of that sequence is silently absent from the canonical
// form rather than shared with the caller.
func canonicalise(t *Trust) *Trust {
	out := &Trust{Version: t.Version}
	out.Plugins = make([]TrustedPlugin, len(t.Plugins))
	copy(out.Plugins, t.Plugins)
	for i := range out.Plugins {
		if d := out.Plugins[i].Deprecation; d != nil {
			cloned := *d
			out.Plugins[i].Deprecation = &cloned
		}
	}
	sort.Slice(out.Plugins, func(i, j int) bool {
		return out.Plugins[i].Plugin < out.Plugins[j].Plugin
	})
	return out
}
