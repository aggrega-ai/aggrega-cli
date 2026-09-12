// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package registryindex

import (
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/deprecation"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// screen answers the registry-side discovery step from withdrawal facts
// gathered before selection began: which published versions of an artifact carry
// a signature-verified marker at the withdrawal severity, and therefore must not
// be selected by a reference that did not name them.
//
// Its facts are always already in hand — it performs no lookup of its own, which
// is the whole reason the exclusion can happen at selection time at all. A screen
// that reached a registry from inside the partition would put a network
// dependency inside a selection primitive every dependency-resolution entrypoint
// shares, and would make the cost scale with the number of published versions at
// every one of those call sites.
//
// Two sources supply those facts, and they differ only in how current they are,
// so each screen carries a provenance clause naming its own source. The local
// index is a snapshot, and its staleness is not smoothed over: a version
// withdrawn since the snapshot was taken is not seen, so the clause states when
// the snapshot was taken and points at the command that refreshes it. A
// per-repository reading taken during the command has no such gap and says so.
// Either way the screen protects against selecting a version known to be
// withdrawn; only the second can speak to now.
//
// Only a signature-verified record screens a version. An unverified record
// carries no trust basis, and letting one change what the compiler resolves would
// make an unauthenticated registry-side claim load-bearing.
type screen struct {
	// withdrawnByURI maps an artifact's index URI to its withdrawn versions and
	// the reason recorded against each.
	withdrawnByURI map[string]map[string]string
	// provenance says where this screen's facts came from and how current they
	// are. It is fixed at construction rather than derived per notice, so one
	// screen cannot describe its own source two ways.
	provenance RetractionProvenance
	out        io.Writer
}

// Honored reports whether a recorded deprecation entry rests on a verified
// signature, and is therefore something a surface may act on at all.
//
// It is the one home of the trustworthiness half of the honored-withdrawal rule,
// and it is separate from the severity half deliberately. A display and a
// selection screen may legitimately differ on SEVERITY — an advisory deprecation
// belongs on a surface a reader is browsing and not in a screen that decides what
// a build resolves to — but they must not differ on whether a record is
// trustworthy. Keeping the trust test in one place is what makes the second half
// of that statement structural rather than a convention two call sites are asked
// to remember.
//
// The record type is what makes the test necessary. The index writer records a
// marker only once the marker's own signature verifies, so a freshly written
// record always carries a signer — but the signer is a pointer precisely so a
// record written before verification existed decodes without one. An unsigned
// record can then only fail to act rather than act on unauthenticated evidence,
// which is the safe direction on both surfaces.
func Honored(d VersionDeprecation) bool {
	return d.Signer != nil
}

// WithdrawnVersions reduces one artifact's recorded deprecations to the versions
// a screen must exclude, mapped to the reason recorded against each.
//
// It is the one home of the severity half of the honored-withdrawal rule, so
// every screen — whichever source supplied its facts — excludes on exactly the
// same grounds. A softer severity warns without changing what is selected. The
// trust half is Honored above, shared with the surfaces that report a record
// rather than act on it.
func WithdrawnVersions(deps []VersionDeprecation) map[string]string {
	var withdrawn map[string]string
	for _, d := range deps {
		if !Honored(d) || d.Marker.Severity != deprecation.SeverityRetracted {
			continue
		}
		if withdrawn == nil {
			withdrawn = map[string]string{}
		}
		withdrawn[d.Version] = d.Marker.Reason
	}
	return withdrawn
}

// NewScreen builds the discovery-step withdrawal screen for a registry host from
// that host's local index, or returns nil when no screen can be formed: an empty
// host, no writer to announce a skip on, no index built for the host, or an index
// recording no verified withdrawal.
//
// A nil screen leaves the discovery step unscreened, which is the honest posture
// rather than a degradation — with no local snapshot there is no verified fact to
// act on, and acting on an unverified one is exactly what this must not do.
func NewScreen(rw *workspaceconfig.ResolvedWorkspace, host string, out io.Writer) catalog.RetractionScreen {
	if host == "" || out == nil {
		return nil
	}
	loaded, err := LoadHost(rw.Root(), rw.Config().ResolvedArtifactCacheDir(), host)
	if err != nil {
		return nil
	}
	return NewScreenFromRecords(loaded.Records, IndexProvenance(loaded.LastUpdated, loaded.HasTimestamp), out)
}

// NewScreenFromRecords builds a withdrawal screen from index records the caller
// already holds, or returns nil when no screen can be formed: no writer to
// announce a skip on, or no verified withdrawal among the records.
//
// It is the one place a set of index records becomes a screen. A caller that has
// just loaded an index reaches it through NewScreen; a caller already holding the
// records reaches it directly rather than re-reading them off disk. Both
// therefore judge a version withdrawn on exactly the same grounds, because the
// honored-withdrawal rule is applied here once and nowhere else.
//
// provenance says where these records came from and how current they are. It is
// supplied rather than derived because the caller is what knows: the same records
// mean one thing read from a snapshot and another read from the registry moments
// ago.
func NewScreenFromRecords(records []Record, provenance RetractionProvenance, out io.Writer) catalog.RetractionScreen {
	if out == nil {
		return nil
	}
	withdrawnByURI := map[string]map[string]string{}
	for _, rec := range records {
		if withdrawn := WithdrawnVersions(rec.Deprecations); withdrawn != nil {
			withdrawnByURI[rec.URI] = withdrawn
		}
	}
	if len(withdrawnByURI) == 0 {
		return nil
	}
	return &screen{
		withdrawnByURI: withdrawnByURI,
		provenance:     provenance,
		out:            out,
	}
}

// ProvenanceSource names where a withdrawal screen's facts came from.
//
// The two sources differ only in how current they are, and that difference is
// what a reader acts on: a reading taken while the command ran speaks to now,
// while a snapshot can be older than the withdrawal it is being asked about. The
// values are the spellings a surface answering with values reports, so they name
// the source in the reader's own terms rather than in this package's.
type ProvenanceSource string

const (
	// ProvenanceRegistry is a reading taken from the registry while the command
	// ran, which has no staleness to warn about.
	ProvenanceRegistry ProvenanceSource = "registry"
	// ProvenanceLocalIndex is the local registry index, which is as current as the
	// last refresh of it and no more.
	ProvenanceLocalIndex ProvenanceSource = "local-index"
)

// RetractionProvenance says where a screen's withdrawal facts came from and how
// current they are.
//
// It is the fact rather than the sentence stating it. The clause a reader watching
// a terminal sees is rendered from this, and a surface answering with values
// carries these fields — so the fact has one home and its wording another, and
// neither surface can come to describe one screen's evidence differently from the
// other.
//
// ObservedAt is meaningful only alongside HasObservedAt, which separates an index
// carrying no build time from one whose build time happens to be the zero time. An
// empty Source is a screen whose provenance is not known, which is what a selection
// that was never screened has.
type RetractionProvenance struct {
	Source        ProvenanceSource
	ObservedAt    time.Time
	HasObservedAt bool
}

// LiveProvenance is the provenance of withdrawal facts read from the registry
// while the command ran.
//
// It carries no observation time, and that asymmetry is the point: facts read
// during the command have no staleness to report, so a time on them would invite a
// reader to weigh a currency question that does not arise.
func LiveProvenance() RetractionProvenance {
	return RetractionProvenance{Source: ProvenanceRegistry}
}

// IndexProvenance is the provenance of withdrawal facts read from the local
// registry index, carrying when that snapshot was taken so a reader can tell a
// fresh record from a stale one.
//
// It never claims currency: the record says what the registry reported when the
// index was last built, not what it reports now.
func IndexProvenance(observedAt time.Time, hasObservedAt bool) RetractionProvenance {
	return RetractionProvenance{Source: ProvenanceLocalIndex, ObservedAt: observedAt, HasObservedAt: hasObservedAt}
}

// ObservedAtStated renders when this provenance's facts were observed, or the
// empty string when the source records no observation time.
//
// It is the one home of that rendering, so the clause a reader watching a terminal
// sees and the value a surface answering with values carries name the same instant
// in the same words, and neither can come to state it differently from the other.
// The instant is stated in UTC, so one screen renders one text whatever zone the
// process runs in.
//
// The empty string is the absent case rather than a zero instant, which is the
// distinction HasObservedAt exists to draw: a source carrying no observation time
// and one whose observation time happens to be the zero instant must not read
// alike. A reader given the empty string learns that the question has no answer
// here, not that the answer is the epoch.
func (p RetractionProvenance) ObservedAtStated() string {
	if !p.HasObservedAt {
		return ""
	}
	return p.ObservedAt.UTC().Format(time.RFC3339)
}

// Clause renders the parenthesised suffix a screen's notices end in, naming where
// its facts came from and how current they are.
//
// It is the one home of that wording, so every notice about one screen's evidence
// describes it the same way. A provenance with no source renders nothing rather
// than guessing: a notice that cannot say where its evidence came from is better
// silent on the question than wrong about it.
//
// The observation time it states is the one ObservedAtStated renders, so the
// sentence and a surface answering with values quote the same instant rather than
// each formatting it for itself.
func (p RetractionProvenance) Clause() string {
	switch p.Source {
	case ProvenanceRegistry:
		return " (read from the registry just now)"
	case ProvenanceLocalIndex:
		observed := p.ObservedAtStated()
		if observed == "" {
			return " (recorded in the local registry index, which carries no build time; run `aggrega registry update` to refresh it)"
		}
		return fmt.Sprintf(" (recorded in the local registry index as of %s; run `aggrega registry update` to refresh it)", observed)
	default:
		return ""
	}
}

// ProvenanceOf reports the provenance of a screen's withdrawal facts, and reports
// false for a screen this package did not build.
//
// A caller recording what a screen concluded needs the screen's own evidence, and
// the screen contract is deliberately not widened to carry it: that contract is
// what a selection primitive calls, and a method there would oblige every
// implementation — test fakes included — to answer a question only a screen built
// from index or registry facts can answer. Reporting false instead leaves such a
// screen with no provenance, which is the truth about it.
func ProvenanceOf(s catalog.RetractionScreen) (RetractionProvenance, bool) {
	built, ok := s.(*screen)
	if !ok {
		return RetractionProvenance{}, false
	}
	return built.provenance, true
}

// NewLiveScreen builds the discovery-step withdrawal screen for ONE artifact
// from withdrawal facts read from the registry during this command, or returns
// nil when no screen can be formed: no writer to announce a skip on, or no
// verified withdrawal among the facts supplied.
//
// It is the counterpart to the index-backed screen for a reference the local
// snapshot does not carry. The facts arrive already gathered and already
// verified, so the screen itself still reaches no registry: the round trips were
// spent by the caller before selection began, which is what keeps the never-fetch
// contract every selection call site relies on.
//
// Its provenance clause carries no refresh hint, and that asymmetry is the point.
// Facts read during the command have no staleness to warn about, so pointing at
// the command that rebuilds the snapshot would misdescribe where this screen's
// evidence came from.
func NewLiveScreen(reference string, deps []VersionDeprecation, out io.Writer) catalog.RetractionScreen {
	if out == nil {
		return nil
	}
	withdrawn := WithdrawnVersions(deps)
	if withdrawn == nil {
		return nil
	}
	return &screen{
		withdrawnByURI: map[string]map[string]string{URIForReference(reference): withdrawn},
		provenance:     LiveProvenance(),
		out:            out,
	}
}

// ScreenVersions partitions an artifact's published tags into the versions the
// discovery step may select and the versions withdrawn from it.
//
// A withdrawn tag the discovery step could not have selected anyway — one that is
// not strict SemVer, or a pre-release, which the step skips — is left in the
// eligible list rather than reported as an exclusion. Screening it would change
// nothing about the outcome while turning a genuine no-conformant-version miss
// into a withdrawal that never happened. The eligibility test is the selection
// primitive itself rather than a re-derived rule, so the two cannot disagree
// about what "selectable" means.
func (s *screen) ScreenVersions(reference string, tags []string) ([]string, []catalog.VersionExclusion) {
	withdrawn, ok := s.withdrawnByURI[URIForReference(reference)]
	if !ok {
		return tags, nil
	}
	selectable := func(tag string) bool {
		return oci.SelectLatestVersion([]string{tag}, false /* includePreRelease */) != ""
	}
	eligible := make([]string, 0, len(tags))
	var excludedVersions []string
	for _, tag := range tags {
		if _, hit := withdrawn[tag]; hit && selectable(tag) {
			excludedVersions = append(excludedVersions, tag)
			continue
		}
		eligible = append(eligible, tag)
	}
	if len(excludedVersions) == 0 {
		return tags, nil
	}
	// Ascending published-version order, so the notice and any halt message are
	// byte-stable for the same registry state regardless of the order the registry
	// listed the tags in.
	excluded := make([]catalog.VersionExclusion, 0, len(excludedVersions))
	for _, v := range oci.SortedSemverTags(excludedVersions) {
		excluded = append(excluded, catalog.VersionExclusion{Version: v, Reason: withdrawn[v]})
	}
	return eligible, excluded
}

// ReportSelection names every version the screen withdrew, the reason recorded
// against it, and the version taken in its place. Announcing the substitution is
// what separates this from a quiet fallback: the reference did not name the
// version that was taken, and the evidence for skipping the higher one is not in
// the author's own files, so it is stated where they will read it.
func (s *screen) ReportSelection(reference, selected string, excluded []catalog.VersionExclusion) {
	WriteSelectionNotice(s.out, reference, selected, excluded, s.provenance)
}

// WriteSelectionNotice states the outcome of a screened selection on out: every
// published version the screen withdrew with the reason recorded against it, and
// the version taken in their place, qualified by where the withdrawal facts came
// from and how current they are.
//
// It is the one home of that wording, reached two ways. A screen announces through
// it while selection runs, which is the path a caller watching a terminal reads. A
// caller that took the same conclusion as values, and is now rendering it for a
// reader of its own, announces through it as well rather than composing a second
// sentence from the same fields — so one substitution reads the same however it
// reached the reader, and the two cannot drift apart.
//
// An empty selection writes nothing. A conclusion that pinned no version states no
// substitution, which is what a selection that was never screened reached: there is
// an explanation to give, and it is not this one.
func WriteSelectionNotice(out io.Writer, reference, selected string, excluded []catalog.VersionExclusion, provenance RetractionProvenance) {
	if out == nil || selected == "" {
		return
	}
	for _, x := range excluded {
		if x.Reason == "" {
			fmt.Fprintf(out, "  retraction: %s:%s is %s and was not selected\n", reference, x.Version, deprecation.SeverityRetracted.StatedAs())
			continue
		}
		fmt.Fprintf(out, "  retraction: %s:%s is %s and was not selected: %s\n", reference, x.Version, deprecation.SeverityRetracted.StatedAs(), x.Reason)
	}
	fmt.Fprintf(out, "  retraction: %s resolved to %s instead%s\n", reference, selected, provenance.Clause())
}

// ReportPinned announces that a reference named an exact version carrying a
// withdrawal record. Such a reference still resolves — naming the version is the
// override, and refusing it would break a build that already committed to that
// version — so this reports rather than refuses.
func (s *screen) ReportPinned(reference, version string) {
	withdrawn, ok := s.withdrawnByURI[URIForReference(reference)]
	if !ok {
		return
	}
	reason, hit := withdrawn[version]
	if !hit {
		return
	}
	if reason == "" {
		fmt.Fprintf(s.out, "  retraction: %s:%s is %s; resolving it because the reference names it explicitly%s\n",
			reference, version, deprecation.SeverityRetracted.StatedAs(), s.asOfSuffix())
		return
	}
	fmt.Fprintf(s.out, "  retraction: %s:%s is %s (%s); resolving it because the reference names it explicitly%s\n",
		reference, version, deprecation.SeverityRetracted.StatedAs(), reason, s.asOfSuffix())
}

// asOfSuffix renders where this screen's withdrawal facts came from and how
// current they are, so a reader can tell a fresh fact from a stale one. It is
// rendered once at construction: every notice a screen writes therefore names the
// same source, which is what stops one screen describing its own evidence two
// ways.
func (s *screen) asOfSuffix() string {
	return s.provenance.Clause()
}

// URIForReference maps a bound OCI repository reference (`<host>/<path>`, the
// form the entity pull binds and fetches from) to the artifact URI the local
// index keys its records by. The two differ only by the scheme prefix, and
// deriving one from the other here keeps the single mapping out of every call
// site.
func URIForReference(reference string) string {
	return "oci://" + reference
}

// ReferenceForURI is the inverse: it maps an artifact URI the local index keys
// its records by back to the bound OCI repository reference the screen is
// addressed with. It sits beside its inverse so the one mapping between the two
// forms is stated in a single place and cannot come to disagree with itself.
func ReferenceForURI(uri string) string {
	return strings.TrimPrefix(uri, "oci://")
}
