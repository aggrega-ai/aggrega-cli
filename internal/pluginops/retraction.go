// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package pluginops

import (
	"fmt"
	"io"

	"github.com/aggrega-ai/aggrega-cli/internal/registryindex"
	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
)

// RetractionAdvisory is what the withdrawal screen concluded about the version an
// addition pinned.
//
// A retraction is a withdrawal, frequently for a safety reason, so a caller given
// an older version than the newest published one is owed the reason it was given
// one. This is that reason as values, which is what every surface is handed: one
// answers with the values themselves, and one renders them through
// WriteRetractionNotice for a reader watching a terminal. So the same fact reaches
// a reader either way and only its presentation differs.
//
// It is absent on an ordinary addition. A screen that withdrew nothing announces
// nothing, so nothing here is filled and the result carries no advisory at all —
// which is what keeps an assertion about an advisory from passing over every
// addition ever made.
//
// The two conditions it carries are kept apart rather than flattened, because they
// say opposite things to a reader. Excluded with Selected is "you are being told
// something": the screen ran, withdrew the versions named, and the addition pinned
// Selected instead. Unscreened is "you did not get something": the withdrawal
// records could not be read at all, so selection ran unscreened and no claim is
// being made about whether the pinned version has since been withdrawn.
//
// Provenance qualifies whichever of those two an advisory carries rather than
// standing as a third alongside them. Where a screen ran it names the evidence that
// screen rested on; where none did it is empty, because a selection nothing screened
// rests on no evidence to name.
type RetractionAdvisory struct {
	// Reference is the repository reference selection ran over.
	Reference string

	// Selected is the version the addition pinned. It is filled only alongside
	// Excluded: an addition that pinned a version with nothing withdrawn carries no
	// advisory, and the version it pinned is already the identity on the result.
	Selected string

	// Excluded is every published version the screen withdrew from selection, with
	// the reason recorded against each. A record is not required to carry prose, so
	// a reason may be empty.
	Excluded []catalog.VersionExclusion

	// Unscreened states why selection was not screened, and is empty whenever it
	// was. It carries the fault the withdrawal read failed with, because what a
	// reader can act on is the fault rather than the bare fact that screening was
	// skipped.
	Unscreened string

	// Provenance says where the withdrawal facts behind this advisory came from and
	// how current they are.
	//
	// A reader handed an older version than the newest published one needs more than
	// the reason it was withdrawn: a reading taken from the registry while the
	// operation ran speaks to now, while a local snapshot can be older than the
	// withdrawal it is being asked about and a refresh is what settles the
	// difference. Carrying it as the value the screen itself holds, rather than as a
	// sentence, is what lets a surface answering with values say that in its own
	// words without falling out of step with the terminal.
	//
	// Its source is empty when no screen was formed, which is the case Unscreened
	// describes. Nothing was consulted, so there is no source to name, and naming one
	// would claim evidence this selection never had.
	Provenance registryindex.RetractionProvenance
}

// WriteRetractionNotice states, on out, what the withdrawal screen concluded about
// the version an addition pinned, and writes nothing when it concluded nothing.
//
// It is how a surface holding the advisory renders it for a reader watching a
// terminal. The advisory travels as values so each surface may answer in its own
// terms, and the terminal's terms are these — composed here rather than at the
// surface, so the sentence a reader meets has one home whichever command put it
// there.
//
// 🔴 It is the second half of a pair, and the other half is
// AddOptions.RetractionNotices. The operation announces a substitution on the
// writer that field resolves to as selection runs, so a caller rendering the
// advisory itself must have set that field to a discarding writer, or the reader
// is told twice. The reason it is a discarding writer rather than none is stated
// at the field.
//
// The two conditions an advisory carries are rendered apart because they say
// opposite things. A screened selection states what was withdrawn and what was
// taken instead; an unscreened one states that nothing was read, so nothing is
// claimed about the version that was pinned.
func WriteRetractionNotice(out io.Writer, advisory *RetractionAdvisory) {
	if out == nil || advisory == nil {
		return
	}
	if advisory.Unscreened != "" {
		writeUnscreenedNotice(out, advisory.Reference, advisory.Unscreened)
		return
	}
	registryindex.WriteSelectionNotice(out, advisory.Reference, advisory.Selected, advisory.Excluded, advisory.Provenance)
}

// writeUnscreenedNotice states on out that an artifact's withdrawal records could
// not be read, so its published versions were not screened. cause is the fault the
// read failed with, because what a reader can act on is the fault rather than the
// bare fact that screening was skipped.
//
// It is the one home of that wording, reached both as the read fails and again
// when a surface renders the advisory that failure produced — so a reader meets
// one sentence whichever of the two put it in front of them.
func writeUnscreenedNotice(out io.Writer, reference, cause string) {
	fmt.Fprintf(out, "  retraction: %s: markers could not be read (%s); published versions were not screened for retraction\n",
		reference, cause)
}

// retractionRecorder collects what a screened selection concluded, so an addition
// can answer with the substitution as values.
//
// A nil recorder records nothing and wraps nothing, which is the posture of every
// caller that only wants the prose: the command line passes a terminal and reads
// the announcement there.
type retractionRecorder struct {
	advisory RetractionAdvisory
	filled   bool
}

// watch returns a screen that behaves exactly as inner does, records what inner is
// told, and records where inner's own facts came from.
//
// The provenance is taken here rather than from anything the screen later
// announces, because here is where a screen is in hand. The decorator below
// observes only the conclusions a screen reports, and the evidence those rest on is
// not among them — a notice states it in prose and the recorder does not read prose.
//
// A nil inner is returned unchanged, because nil is how the layers below state
// that no screen could be formed and selection is to run unscreened — wrapping it
// would turn that into a screen whose every call reached nothing.
func (r *retractionRecorder) watch(inner catalog.RetractionScreen) catalog.RetractionScreen {
	if r == nil || inner == nil {
		return inner
	}
	// A screen built elsewhere answers no provenance, and the value reported then is
	// the empty one — which is the truth about such a screen. So the discarded verdict
	// drops nothing: it says only what the value already says.
	r.advisory.Provenance, _ = registryindex.ProvenanceOf(inner)
	return &recordingScreen{inner: inner, rec: r}
}

// recordUnscreened notes that the withdrawal records could not be read, so
// selection ran unscreened. cause is the fault the read failed with.
//
// It records no provenance, and that absence is the answer rather than an omission.
// No screen was formed, so there is nothing whose evidence could be named, and the
// empty source an advisory then carries is exactly what a reader must act on:
// nothing was screened, so nothing is claimed about the version that was pinned.
func (r *retractionRecorder) recordUnscreened(reference, cause string) {
	if r == nil {
		return
	}
	r.advisory.Reference = reference
	r.advisory.Unscreened = cause
	r.filled = true
}

// advisoryOrNil returns what was recorded, or nil when nothing was.
//
// The nil is the answer rather than an empty value, because an empty advisory and
// an absent one read identically on a surface that omits an empty field, while
// only the absent one is the truth about an addition that screened cleanly.
func (r *retractionRecorder) advisoryOrNil() *RetractionAdvisory {
	if r == nil || !r.filled {
		return nil
	}
	out := r.advisory
	return &out
}

// recordingScreen is a catalog.RetractionScreen that delegates every decision to
// the screen it wraps and records what that screen was told.
//
// 🔴 IT DECORATES RATHER THAN RECOMPUTES, AND THAT IS THE WHOLE DESIGN. Which
// versions are withdrawn, which of them the discovery step could have selected at
// all, and the wording of every notice stay in the screen this wraps. A recorder
// that re-derived the exclusions from the tag list would be a second copy of the
// screening rule — agreeing on the day it was written and free to disagree the
// first time that rule moves, with nothing failing while the two agree. So this
// observes and computes nothing, which is exactly what makes it unable to
// disagree with the screen whose conclusions it reports. Do not "unify" it into a
// derivation.
//
// Delegation is also what keeps the prose byte-identical: every call reaches the
// wrapped screen unchanged and in the same order, so a caller watching a terminal
// reads exactly what it read before, whether or not anything is recording.
type recordingScreen struct {
	inner catalog.RetractionScreen
	rec   *retractionRecorder
}

// ScreenVersions delegates the partition and records nothing.
//
// The partition is an intermediate: the versions it withdraws are reported to
// ReportSelection only when selection actually landed on a substitute, and
// recording them here would announce a substitution on a selection that then
// failed outright, where the halt carries the withdrawal as its own reason.
func (s *recordingScreen) ScreenVersions(reference string, tags []string) ([]string, []catalog.VersionExclusion) {
	return s.inner.ScreenVersions(reference, tags)
}

// ReportSelection delegates the announcement and records the substitution it
// announced.
//
// The excluded slice is copied rather than retained, because it belongs to the
// caller that built it and an advisory outliving this call must not alias a slice
// that caller may reuse.
func (s *recordingScreen) ReportSelection(reference, selected string, excluded []catalog.VersionExclusion) {
	s.inner.ReportSelection(reference, selected, excluded)
	s.rec.advisory.Reference = reference
	s.rec.advisory.Selected = selected
	s.rec.advisory.Excluded = append([]catalog.VersionExclusion(nil), excluded...)
	s.rec.filled = true
}

// ReportPinned delegates and records nothing.
//
// It announces a reference that named an exact version carrying a withdrawal
// record. A recording screen never meets that case: it wraps the screen the latest
// version is chosen through, and an addition naming its version does not choose
// one — it announces a withdrawal recorded against the version it names on a screen
// of its own (announceWithdrawnPin), whose finding reaches the result as
// AddResult.Withdrawn rather than through this recorder. The delegation is here
// because the screen contract has the method.
func (s *recordingScreen) ReportPinned(reference, version string) {
	s.inner.ReportPinned(reference, version)
}
