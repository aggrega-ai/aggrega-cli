// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package recipepin

import (
	"fmt"

	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// Unrecorded is one input an entity reads whose pin its lock does not record as
// the pin step would record it today.
//
// It is either missing — the lock holds no pin for the input at all — or stale,
// which is the lock holding a pin for the input at a value the input no longer
// has. Both are the same condition to the reader of the halt: the pin for the
// bytes the entity reads now is not the one on disk, and the same step records
// it. The two are told apart because the remedy reads differently for each: a
// missing pin was never recorded, while a stale one records an input that moved
// after it was pinned.
type Unrecorded struct {
	// Class names the kind of input, in the words a reader of the halt meets it
	// under: a remote source, an imported file.
	Class string

	// Input names the one input within its class — a source by the coordinates
	// its author wrote, an imported file by the artifacts key that consumes it.
	Input string

	// Stale reports that the lock records a pin for this input at a different
	// value, rather than none.
	Stale bool
}

// lockClass is one class of pin a lock holds: the field it is carried in, the
// noun a reader meets it under, and the comparison that finds the inputs of the
// class whose wanted pin the recorded one does not match.
//
// The unpinned-entity halt is built over [lockClasses] as a set rather than over
// the fields by name, so a class the lock gains is an added row here rather than
// a rewritten halt. A guard holds the table to [Lock] and [Pins] field for field:
// a pin list either struct carries with no row here is refused, because a class
// the halt did not know about would be a class no default pull ever stops on.
type lockClass struct {
	// field is the Go field name the class is carried under, on [Lock] and on
	// [Pins] alike — the two structs carry the same rows, so one name locates
	// the class in both.
	field string

	// noun is what a reader of the halt calls one input of this class.
	noun string

	// unrecorded lists, in the order want carries them, the inputs of this class
	// whose pin in want is absent from recorded or recorded at another value.
	unrecorded func(want, recorded Pins) []Unrecorded

	// count is how many pins of this class p carries.
	count func(p Pins) int
}

// ClassCount is how many pins of one class a lock holds, named in the words a
// reader meets the class under.
type ClassCount struct {
	Class string
	Pins  int
}

// CountPins reports how many pins of each class p carries, class by class in
// the order the lock declares its classes. A class with no pin is reported with
// a zero rather than left out, so a reader sees every class the lock can hold.
func CountPins(p Pins) []ClassCount {
	out := make([]ClassCount, 0, len(lockClasses))
	for _, class := range lockClasses {
		out = append(out, ClassCount{Class: class.noun, Pins: class.count(p)})
	}
	return out
}

// The nouns a reader of the halt meets each class under. They are constants
// rather than read back off the table, so a comparison naming its own class does
// not make the table's initialiser refer to itself.
const (
	classRemoteSource = "remote source"
	classImportedFile = "imported file"
)

// lockClasses is every class of pin a lock holds, in the order the lock
// document declares them.
var lockClasses = []lockClass{
	{
		field:      "SourceDigests",
		noun:       classRemoteSource,
		unrecorded: unrecordedSources,
		count:      func(p Pins) int { return len(p.SourceDigests) },
	},
	{
		field:      "ArtifactDigests",
		noun:       classImportedFile,
		unrecorded: unrecordedArtifacts,
		count:      func(p Pins) int { return len(p.ArtifactDigests) },
	},
}

// UnrecordedPins reports every input whose pin want carries and recorded does
// not carry at the same value, class by class in the order the lock declares
// its classes, and within a class in the order want carries its rows.
//
// want is what the pin step would record for the entity now, and recorded is
// what the lock beside it holds. A pin recorded holds for an input the entity no
// longer reads is not reported: the entity does not read that input, so nothing
// a build compares is missing, and the next pin simply drops the row.
func UnrecordedPins(want, recorded Pins) []Unrecorded {
	var out []Unrecorded
	for _, class := range lockClasses {
		out = append(out, class.unrecorded(want, recorded)...)
	}
	return out
}

func unrecordedSources(want, recorded Pins) []Unrecorded {
	var out []Unrecorded
	for _, w := range want.SourceDigests {
		r, present := MatchSourcePin(recorded.SourceDigests, w.Kind, w.URL, w.Chart, w.ModuleSubdir, w.Version)
		if present && r.Digest == w.Digest && r.ManifestDigest == w.ManifestDigest {
			continue
		}
		out = append(out, Unrecorded{Class: classRemoteSource, Input: sourceInputName(w), Stale: present})
	}
	return out
}

func unrecordedArtifacts(want, recorded Pins) []Unrecorded {
	var out []Unrecorded
	for _, w := range want.ArtifactDigests {
		r, present := MatchArtifactPin(recorded.ArtifactDigests, w.Name)
		if present && r.Digest == w.Digest {
			continue
		}
		out = append(out, Unrecorded{Class: classImportedFile, Input: w.Name, Stale: present})
	}
	return out
}

// sourceInputName names a remote source by the coordinates its author wrote: the
// url, the chart or the directory within it where the source names one, and the
// version it resolved to — for a git source, the commit its ref named.
func sourceInputName(p types.SourceDigest) string {
	name := p.URL
	if p.Chart != "" {
		name += " chart " + p.Chart
	}
	if p.ModuleSubdir != "" {
		name += " directory " + p.ModuleSubdir
	}
	if p.Version != "" {
		name += "@" + p.Version
	}
	return name
}

// ErrEntityNotPinned is the loud halt a dependency resolution raises, by
// default, over an entity in one of the workspace's catalog directories that
// reads an input the lock beside it records no pin for — or records at a value
// the input no longer has.
//
// Pinning is the entity author's step, run before the entity is consumed, so a
// resolution that meets an unpinned entity stops rather than recording pins in
// the author's directory on its own. It names the entity, every input whose pin
// is missing or stale and the class each belongs to, and the step that records
// them. A resolution the caller asked to pin — or one a workspace configured to
// pin — runs that step instead and never raises this. Category: CATALOG_ERROR.
// Match with errors.Is(err, ErrEntityNotPinned()).
func ErrEntityNotPinned() error { return errEntityNotPinned }

var errEntityNotPinned = cerrors.Register("entity-not-pinned", cerrors.Meta{
	ShortCode: "E0302",
	Message:   "an input the entity reads has no pin recorded for it in the entity.lock beside it",
	RulePlace: cerrors.RuleNoStageReaches("A dependency resolution evaluates the rule, once per Recipe or FeatureRecipe in a " +
		"workspace catalog directory that the resolution reaches, comparing the pins that entity's inputs " +
		"would record now against the lock file beside it. The inputs are acquired and digested during the " +
		"resolution rather than during a compilation, and the lock is a file beside the entity rather than a " +
		"document a compilation reads, so no declared stage holds the two values the rule compares."),
})

// UnpinnedEntity composes the halt for one entity whose lock does not record
// every pin its inputs need.
//
// entityPath positions the diagnostic at the entity document, which is the file
// the remedy names. displayPath is the same document as the reader would type it
// into the remedy — relative to the workspace root where it sits inside one. kind
// and name identify the entity, and missing is every input the halt reports, each
// naming its class so a reader knows whether a source or a file needs pinning.
//
// The composition lives here, beside the class table, so the sentence a reader
// is given for a class and the class the comparison found cannot come to name
// two different things.
func UnpinnedEntity(entityPath, displayPath, kind, name string, missing []Unrecorded) error {
	condition := ErrEntityNotPinned()
	values := []string{fmt.Sprintf("%s %q", kind, name)}
	for _, u := range missing {
		state := "no pin recorded"
		if u.Stale {
			state = "the recorded pin no longer matches it"
		}
		values = append(values, fmt.Sprintf("%s %s: %s", u.Class, u.Input, state))
	}
	return cerrors.Wrap(condition, cerrors.CatalogError,
		cerrors.SourceLocation{File: entityPath},
		cerrors.ComposeMessage(condition, values, unpinnedEntityRemediation(displayPath)))
}

// unpinnedEntityRemediation is what the unpinned-entity halt tells its reader to
// do. It names the entity author's own step first, because pinning is the
// author's duty and the lock it writes is the file they commit; and the
// workspace setting second, for a reader who consumes the entity rather than
// authoring it and wants every resolution in the workspace to record the pins
// instead of stopping.
func unpinnedEntityRemediation(displayPath string) string {
	return "remediation: run `aggrega pin " + displayPath + "` and commit the " + LockFileName +
		" it writes beside the entity; to have every pull in this workspace record the pins itself instead, " +
		"set `pin_sources_at_pull: true` in the workspace configuration"
}
