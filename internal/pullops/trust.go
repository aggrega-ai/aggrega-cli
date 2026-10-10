// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package pullops

import (
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/internal/dryrunreport"
	"github.com/aggrega-ai/aggrega-cli/internal/wasm/embedded"
	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
	"github.com/aggrega-ai/aggrega-cli/pkg/pluginstrust"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
	"github.com/aggrega-ai/aggrega-cli/pkg/writescope"
)

// verdictPluginResolution is what one attachment that returns an accept-or-reject
// verdict resolved to during a pull: the plugin's identity coordinate, the version
// its entity declares, the content digest of its `.wasm`, and the authored
// reference the attachment named with any version suffix removed.
//
// Coordinate, Version and Digest all come from a single read of the plugin's
// cached entity and manifest, which is the same read a later resolution of that
// plugin performs. Reading them together is the point: a digest taken from one
// place and a version from another can each be right on their own while naming
// different artifacts between them.
//
// BareURI is carried separately from Coordinate because they answer different
// questions. Coordinate is the plugin's identity — its socket segment and its bare
// name — and is the address a record is written under. BareURI is what the
// attachment was authored as, which is the form `--upgrade` matches a named target
// against. For a bare-name attachment the two strings coincide; for a
// location-shaped reference they do not.
//
// Deprecation is the verified withdrawal discovered for that artifact during the
// same pull, or nil when none was found, and Signature the signature fact the same
// pull verified for it, or nil when it verified none. Both are carried alongside
// the identity rather than looked up later because each is a fact about the very
// bytes this resolution identified: discovered anywhere else it could describe a
// different artifact than the one recorded beside it.
//
// An empty Coordinate means the plugin was not in the content cache, so there was
// nothing to identify.
type verdictPluginResolution struct {
	BareURI     string
	Coordinate  string
	Version     string
	Digest      string
	Deprecation *pluginstrust.Deprecation
	Signature   *lockfile.VerifiedSignature
}

// ErrVerdictPluginIdentityChanged reports that a pull resolved an attachment
// returning an accept-or-reject verdict to a plugin identity the record beside
// the lockfile does not hold for it, without the run having asked for that
// identity to be re-resolved.
//
// One condition covers both axes — a moved version and moved bytes — because the
// record is addressed by the plugin's identity coordinate, which carries neither:
// the version and the digest are fields of one record, so comparing a resolution
// against it is a single comparison with a single outcome. Splitting it would give
// a caller two things to match for one disagreement, and would leave a
// version-only move (a re-publish whose bytes did not change) and a digest-only
// move (a re-push at the same version) looking like different kinds of event when
// they are the same one: the identity is no longer what was reviewed.
//
// It is deliberately not the condition a later resolution raises when the record
// disagrees with the artifact in the content cache. That one refuses to RUN a
// plugin whose bytes are not the reviewed ones, and its remediation is to restore
// or re-record; this one refuses to ADOPT a new identity silently, and its
// remediation is to name the plugin in an upgrade. Two remediations, two
// conditions.
//
// It is reached through this accessor, and it carries both of a halting
// condition's identifiers, because the condition has to be nameable wherever a
// halt is reported rather than only where it is raised. Every surface that
// resolves a build maps a halt to the diagnostic a caller reads — a terminal
// quotes the short code, and a structured diagnostic carries the descriptive
// code as a field of its own — so a condition registering neither is one those
// surfaces can report and cannot identify. Its sibling condition over the same
// record, which refuses a configuration that no longer attaches a recorded gate,
// is registered and reachable the same way; these two halts are read by the same
// consumers and a caller able to name one and not the other has no way to tell
// which of them stopped a run.
func ErrVerdictPluginIdentityChanged() error { return errVerdictPluginIdentityChanged }

var errVerdictPluginIdentityChanged = cerrors.Register("verdict-plugin-identity-changed", cerrors.Meta{
	ShortCode: "E0274",
	Message:   "the plugin identity recorded as trusted no longer matches what this resolution reports",
	RulePlace: cerrors.NotARule("A pull resolved a verdict attachment to a plugin identity the record beside the lockfile " +
		"does not hold for it. The identity is produced by the resolution the comparison is part of, " +
		"so it reports a recorded trust fact that no longer verifies rather than a rule refusing " +
		"authored content."),
})

// splitAttachmentRef splits an attachment's authored reference into its bare
// identity and the version suffix it named, which is empty when it named none.
//
// One home for the split because the two halves are read by sites that must agree
// on where the boundary falls: the bare form is what a targeted upgrade matches a
// named target against, and the version is what the resolution falls back from
// when the reference pinned one.
func splitAttachmentRef(uri string) (bare, version string) {
	bare, version, _ = strings.Cut(uri, ":")
	return bare, version
}

// verdictUpgradeTargets returns every string a targeted upgrade may name for a
// plugin the lockfile holds no pin for: the authored reference of each configured
// attachment that returns an accept-or-reject verdict, version suffix removed,
// and the identity coordinate of each entry the record beside the lockfile holds.
//
// Such a plugin shapes no rendered output, so it is recorded beside the lockfile
// rather than pinned in it, and a named target has no pin to drop. What naming it
// does instead is reach the run, and there are two different things it can do
// there — which is why the accepted set comes from two places rather than one:
//
//   - A CONFIGURED attachment's authored reference. Naming it re-resolves that
//     attachment and adopts the identity it resolves to. The authored reference is
//     the right form here because that is what the re-resolution matches against;
//     the reference and the identity coordinate coincide only for a bare-name
//     attachment.
//   - A RECORDED entry's identity coordinate. Naming it drops that entry when
//     nothing attaches the plugin any more. A detached plugin has no authored
//     reference left to name, so the coordinate is the only string the reader can
//     see — and it is exactly what the refusal over a stale entry prints.
//
// Both are needed and neither substitutes for the other. Without the attachments,
// adopting a newly attached plugin's identity would be unnameable; without the
// record, the command the stale-entry refusal prints would be rejected as naming
// nothing, and the entry could never be dropped.
//
// A target naming a configured attachment the record does not yet cover is
// accepted, and does nothing beyond the ordinary fill. That is right: it is not a
// mistyped name, the attachment is there.
//
// trust may be nil, which is the workspace that has recorded nothing yet: the
// accepted set is then the attachments alone.
func verdictUpgradeTargets(cfg workspaceconfig.Config, trust *pluginstrust.Trust) []string {
	attachments := cfg.VerdictAttachments()
	targets := make([]string, 0, len(attachments))
	for _, att := range attachments {
		bare, _ := splitAttachmentRef(att.Ref.URI)
		if bare == "" {
			continue
		}
		targets = append(targets, bare)
	}
	if trust == nil {
		return targets
	}
	for _, p := range trust.Plugins {
		if p.Plugin == "" {
			continue
		}
		targets = append(targets, p.Plugin)
	}
	return targets
}

// verdictAttachmentCoordinates answers, for the configuration this pull read,
// which plugin identity coordinates its verdict attachments account for — and
// whether every attachment could be identified.
//
// The answer is assembled from the resolutions the caching walk already
// performed, keyed by the authored reference each attachment was resolved under.
// It is deliberately not a second read of the Plugin entities: the walk's
// coordinate comes from the same read that produced the digest recorded beside
// it, and a second derivation could report a coordinate that disagreed with the
// recorded one while both looked right.
//
// # Why this is the attachment set and never the recordable set
//
// The reduction the record itself covers drops two classes of attachment that are
// still very much attached: one whose plugin the binary carries, which is declined
// deliberately, and one whose entity or cached artifact could not be read. An
// entry pruned against that reduction would be the record of a plugin the
// configuration still names — so what accounts for an entry is the ATTACHMENT,
// and the resolutions are consulted only to learn which plugin each attachment
// names.
//
// An attachment with no resolution makes the answer INCOMPLETE rather than
// silently narrowing it, which is what keeps a failed read from reading as a
// detached gate. What an incomplete answer then means is the record's own
// question, answered once in pluginstrust.Trust.Unaccounted.
func verdictAttachmentCoordinates(attachments []workspaceconfig.VerdictAttachment, resolutions []verdictPluginResolution) (accounted []string, complete bool) {
	byRef := make(map[string]string, len(resolutions))
	for _, r := range resolutions {
		if r.BareURI == "" || r.Coordinate == "" {
			continue
		}
		byRef[r.BareURI] = r.Coordinate
	}
	accounted = make([]string, 0, len(attachments))
	complete = true
	for _, att := range attachments {
		bare, _ := splitAttachmentRef(att.Ref.URI)
		coordinate, identified := byRef[bare]
		if !identified {
			complete = false
			continue
		}
		accounted = append(accounted, coordinate)
	}
	return accounted, complete
}

// recordableVerdictResolutions reduces the identities a pull resolved to the ones
// the record covers: an attachment that resolved to something identifiable, whose
// plugin the binary does not carry.
//
// Both consumers of the record — the comparison that holds a resolution to it and
// the write that fills it — read this one reduction, so neither can consider an
// identity the other ignores. That matters most for the binary's own copy: a
// coordinate the binary carries is never recorded (see recordVerdictPluginTrust
// for why), so holding a resolution of one against a record would compare against
// an entry that describes a different artifact and can never be replaced.
func recordableVerdictResolutions(resolutions []verdictPluginResolution) []verdictPluginResolution {
	recordable := make([]verdictPluginResolution, 0, len(resolutions))
	for _, r := range resolutions {
		if r.Coordinate == "" || r.Digest == "" {
			continue
		}
		if _, carriedByBinary := embedded.Lookup(r.Coordinate); carriedByBinary {
			continue
		}
		recordable = append(recordable, r)
	}
	return recordable
}

// holdVerdictPluginsToTheirRecord compares the identity each verdict-returning
// attachment resolved to against the identity recorded as trusted for it, and
// halts on a disagreement this run did not ask for.
//
// It runs on every pull — including a verify, which writes no record and is the
// surface a user reaches to be told whether the committed files still describe
// what a pull would resolve now. A verify that passed over the record while
// checking its sibling would report agreement it never looked for.
//
// The escape is the run itself: a full refresh re-resolves everything, and a
// targeted upgrade naming the attachment re-resolves that one, so both are read
// as the user having asked for the new identity and the write path adopts it. Any
// other pull holds the recorded identity and refuses, which is the whole point of
// recording it — a record that adopted whatever was resolved next would follow the
// resolution instead of governing it.
//
// An identity the record does not hold is not a disagreement. It is the first
// sighting of that plugin, and the write path fills it.
//
// releasePath locates the record and names the Release in the remediation, so a
// reader can run the command as printed.
func holdVerdictPluginsToTheirRecord(releasePath string, resolutions []verdictPluginResolution, mode catalog.PinReuseMode) error {
	recordable := recordableVerdictResolutions(resolutions)
	if len(recordable) == 0 {
		return nil
	}
	path := pluginstrust.PathFor(releasePath)
	trust, _, err := pluginstrust.LoadOrStart(path)
	if err != nil {
		return err
	}
	for _, r := range recordable {
		held, isHeld := trust.Find(r.Coordinate)
		if !isHeld {
			continue
		}
		if held.ResolvedVersion == r.Version && held.Digest == r.Digest {
			continue
		}
		if reresolvedByThisPull(mode, r.BareURI) {
			continue
		}
		return fmt.Errorf(
			"plugin %q: %s records version %s with digest %s as trusted for it, but this resolution reports version %s with digest %s; "+
				"review what changed and adopt it with `aggrega pull --upgrade --release %s %s`, or restore the recorded identity: %w",
			r.Coordinate, path, held.ResolvedVersion, held.Digest, r.Version, r.Digest,
			releasePath, r.BareURI, ErrVerdictPluginIdentityChanged())
	}
	return nil
}

// verdictTrustChangeKind names what a pull did to ONE entry of the record beside
// the lockfile.
//
// The kinds between them cover every way an entry can move, and an entry whose
// identity held moves once per fact that changed, so a reader is told which
// change happened rather than only that the file was touched. That distinction is
// the whole reason the classification exists: this record is the only place a
// swapped gate is written down, so a reader who is merely told the file moved has
// nowhere else to find out what moved in it.
type verdictTrustChangeKind int

const (
	// verdictTrustRecorded is a plugin the record did not hold: its identity is
	// written for the first time.
	verdictTrustRecorded verdictTrustChangeKind = iota
	// verdictTrustIdentityAdopted is a held entry whose version, digest or both
	// moved. ONE kind covers both axes because the record is addressed by the
	// coordinate, which carries neither: whichever field says so, the identity is
	// no longer the one that was reviewed, and the refusal that guards the same
	// record treats the two as one event for the same reason.
	verdictTrustIdentityAdopted
	// verdictTrustDeprecationRecorded is a held entry whose identity did not move
	// and which gained, or changed, the deprecation recorded against the artifact
	// it already names.
	verdictTrustDeprecationRecorded
	// verdictTrustDeprecationCleared is a held entry whose identity did not move
	// and whose recorded deprecation is gone, because a run that asked for the
	// plugin to be re-resolved found no marker on the bytes the entry records.
	//
	// It is reachable only from such a run, and that bound is the record's own
	// rule rather than a property of this classification: an ordinary pull adds a
	// marker to an entry it already holds and never clears one, so a deprecation
	// leaves only with the entry that carried it — either pruned, or replaced by
	// the re-resolution that asked for it.
	verdictTrustDeprecationCleared
	// verdictTrustSignatureRecorded is a held entry whose identity did not move and
	// which gained, or changed, the verified signature fact recorded against the
	// artifact it already names.
	verdictTrustSignatureRecorded
	// verdictTrustSignatureCleared is a held entry whose identity did not move and
	// whose recorded signature fact is gone, because this pull verified no
	// signature for those bytes. Unlike a deprecation, a signature fact is the
	// answer of the verification every pull runs, so an ordinary pull reaches this
	// kind too.
	verdictTrustSignatureCleared
	// verdictTrustEntryDropped is an entry nothing accounts for any more, on a run
	// that asked for it to go.
	verdictTrustEntryDropped
)

// verdictTrustChange is one entry's movement: which change it was, the plugin it
// happened to, and the identity the entry carries after it — plus the identity it
// carried before, on the one kind where something was replaced.
//
// Prior fields are populated only by verdictTrustIdentityAdopted, because they
// are the only kind where a reader needs both sides to see what was swapped. For
// every other kind the entry's identity did not move, so naming it once is
// naming all there is.
type verdictTrustChange struct {
	Kind         verdictTrustChangeKind
	Plugin       string
	Version      string
	Digest       string
	PriorVersion string
	PriorDigest  string
}

// verdictTrustOutcome is what a pull works out about the record beside the
// lockfile, before anything is put on disk: the record as this run would leave
// it, how many identities it covers, what moved in it, and whether there was no
// record before this run.
//
// Working it out and putting it there are separate steps, for the reason the
// entity pin step and the lockfile write are split the same way: a run asked to
// report what it would write has to reach the same answer the writing run
// reaches, and a second derivation of that answer would agree today and diverge
// the first time either moved. Carrying the per-entry classification here rather
// than at the write is the same rule one level down — the site that decides what
// the record becomes is the site that knows what changed, and a writer working it
// out again from the file would be a second answer to the same question.
type verdictTrustOutcome struct {
	Trust      *pluginstrust.Trust
	Recordable int
	Changes    []verdictTrustChange
	Created    bool
}

// Changed reports whether this pull moves the record's content at all, which is
// what decides whether anything is written.
//
// It is derived from the changes rather than tracked beside them. A flag kept in
// step by hand would be free to disagree with the list, and the disagreement a
// reader would meet is exactly the defect the list exists to remove: a write with
// nothing said about it, or a line about a write that did not happen.
func (o verdictTrustOutcome) Changed() bool { return len(o.Changes) > 0 }

// recordVerdictPluginTrust writes the plugin identities resolved for a Release's
// verdict-returning attachments into `plugins-trust.yaml` beside that Release's
// lockfile, and returns how many identities the record covers.
//
// It is [verdictTrustAfterPull] plus the write and the announcement, which is
// what [reportVerdictPluginTrust] is without them.
//
// Nothing to record and no record yet means no file. A workspace that attaches no
// governance verdict plugin and no linter plugin resolves no identities, so with
// no record already beside the lockfile this writes no file and says nothing — an
// empty record would assert that there is something to compare against when there
// is not. The same holds for a workspace whose only attachments resolve from the
// binary, for the reason below.
//
// A record already present for a plugin keeps its identity — only the facts verified
// about those bytes are recorded on it — unless this pull re-resolved that plugin:
// a full refresh, or a targeted upgrade naming it. Those
// are the two invocations where the user asked for fresh resolution, so replacing
// the record is the answer to what they asked; every other pull holds the recorded
// identity, because silently adopting whatever was resolved this time would make
// the record follow the resolution instead of governing it.
//
// # What happens to an entry the configuration no longer accounts for
//
// A recorded plugin that nothing attaches any more halts every build and every
// release-scoped lint, deliberately: dropping a gate changes no rendered byte, so
// output produced without a reviewed gate is indistinguishable from output that
// gate approved. That halt needs a way out, and this is it — but only on the two
// invocations that already carry the user's request for fresh resolution:
//
//   - A full refresh drops every entry the configuration no longer accounts for.
//     It discards the pin set and re-resolves everything, so holding a stale entry
//     back would be the one thing it carried forward.
//   - A targeted upgrade drops ONLY the entries it named, and only those nothing
//     accounts for. Holding every pin it did not name is what a targeted upgrade
//     means; removing more than was named would exceed the request.
//
// Every other invocation leaves the record exactly as it is, and each exclusion
// has its own reason. An ordinary pull must not drop an entry, because a silently
// removed gate is precisely what the halt exists to catch and adopting the removal
// here would reduce the halt to a message on one run. A verify-only pull writes
// nothing at all, so it cannot: a verification that mutated what it verifies is
// not one. And no build, validate, render or lint path reaches this at all — they
// hold themselves to the record and never write it, because a gate that clears
// itself on the next build is not a gate.
//
// A pull with nothing left to record still reaches the record for this reason: the
// workspace that removed its LAST verdict attachment is exactly the one that needs
// the entry dropped, and returning early on an empty resolution set would leave
// that workspace permanently halted with no command able to unstick it.
//
// A pull only reaches this after holdVerdictPluginsToTheirRecord has compared every
// resolved identity against the record, so a held entry reached here either agrees
// with what was resolved or belongs to a run that asked for it to be re-resolved.
// The hold-a-present-entry arm below is therefore the quiet half of one rule whose
// loud half is that comparison: nothing is adopted without being asked for, and
// nothing is dropped on the floor either.
//
// # Why an artifact the binary carries is not recorded
//
// A pull ACQUIRES: it fetches a plugin from a registry, or reads one from the
// workspace content cache. It never resolves a plugin from the copy compiled into
// the binary, because there is nothing to acquire there. Resolution at build and
// lint time runs a different cascade, and the FIRST tier of that cascade IS the
// binary — a plugin the binary carries wins ahead of every later tier.
//
// So for a coordinate the binary carries, the two sides are looking at two
// different artifacts: this side at what was fetched, the other side at what ships
// inside the binary. Recording the fetched artifact's digest would make every
// later resolution of that plugin compare the binary's bytes against a digest
// describing something else, and stop — on a plugin nobody changed. Declining to
// record it keeps the record about acquired artifacts, which is the only thing it
// can be about: the bytes inside the binary are identified by the compiler version
// that carries them, and no acquisition happened for a record to attest.
//
// That asymmetry between the two cascades is stated here, at the point where the
// records are written, because it is invisible from either side alone. Whoever
// adds a second source of these records inherits it: a source that records a
// coordinate the binary carries re-creates exactly that stop.
//
// A light binary carries no plugin bytes, so nothing is declined there and a
// configured attachment is recorded like any other. That falls out of asking the
// binary what it carries rather than deciding it from a build flag.
//
// root is the root the record is held to — the one the Release's directory picks
// (writescope.RootFor) — because the record is a file composed beside the Release.
func recordVerdictPluginTrust(root, releasePath string, attachments []workspaceconfig.VerdictAttachment, resolutions []verdictPluginResolution, mode catalog.PinReuseMode, diagOut io.Writer) (int, error) {
	outcome, err := verdictTrustAfterPull(releasePath, attachments, resolutions, mode)
	if err != nil {
		return 0, err
	}
	if !outcome.Changed() {
		return outcome.Recordable, nil
	}
	path := pluginstrust.PathFor(releasePath)
	// The record is a file this run composes beside the Release, so it is held to
	// root before it is written.
	if err := writescope.CheckWrite(root, path); err != nil {
		return 0, err
	}
	if err := pluginstrust.Write(path, outcome.Trust); err != nil {
		return 0, err
	}
	announceVerdictTrustChanges(diagOut, path, outcome)
	return outcome.Recordable, nil
}

// announceVerdictTrustChanges says what this pull did to the record beside the
// lockfile: the file itself, where this run created it, and then one line per
// entry that moved, naming the plugin and which change it was.
//
// # Why every change is said out loud and not only the first
//
// Everything a build reproduces byte-for-byte is pinned in the lockfile, so a
// reader always has a second place to notice one of those moving. A plugin that
// only returns an accept-or-reject verdict shapes none of those bytes, which is
// why its identity is recorded here instead — and that leaves this file as the
// ONLY place any of it is written down. A digest moving under the plugin that
// decides whether a build is allowed is the most consequential thing this command
// writes, and there is no second record for a reader to catch it in.
//
// # Why it says nothing when nothing moved
//
// It is reached only past the write, so a pull that leaves the record exactly as
// it was arrives here never. That silence is what keeps the line worth reading: a
// line that fired on every pull would train a reader to skip the one that
// mattered, which is the same silence this function exists to remove, arrived at
// from the other side.
//
// A run that REPORTS what it would write announces nothing, and that is not an
// omission either. Each line below states a change the record has undergone, and
// on a reporting run it has undergone none — the record's own path appears in the
// report instead, which is where a reader of a preview looks for it.
//
// # Why the creation line is not one of the entry kinds
//
// It is about the FILE and every other line is about an ENTRY, so a first
// resolution legitimately says both: this file is new and must be committed, and
// these are the identities it now holds. They are two events rather than two
// statements of one, which is why the creation line carries the commit
// instruction and no entry line repeats it.
func announceVerdictTrustChanges(diagOut io.Writer, path string, outcome verdictTrustOutcome) {
	if outcome.Created {
		fmt.Fprintf(diagOut, "  plugin trust: created %s, recording the plugin identity resolved for each attachment that returns an accept-or-reject verdict; "+
			"commit it alongside the lockfile, because one shared record is what lets every build hold those attachments to the same plugin identities\n", path)
	}
	for _, c := range outcome.Changes {
		switch c.Kind {
		case verdictTrustRecorded:
			fmt.Fprintf(diagOut, "  plugin trust: %s: recorded for the first time, at version %s with digest %s\n",
				c.Plugin, c.Version, c.Digest)
		case verdictTrustIdentityAdopted:
			fmt.Fprintf(diagOut, "  plugin trust: %s: identity adopted, version %s with digest %s replaced by version %s with digest %s\n",
				c.Plugin, c.PriorVersion, c.PriorDigest, c.Version, c.Digest)
		case verdictTrustDeprecationRecorded:
			fmt.Fprintf(diagOut, "  plugin trust: %s: deprecation recorded, against the artifact this entry already names at version %s with digest %s\n",
				c.Plugin, c.Version, c.Digest)
		case verdictTrustDeprecationCleared:
			fmt.Fprintf(diagOut, "  plugin trust: %s: deprecation cleared, from the entry at version %s with digest %s, because re-resolving it found no marker on those bytes\n",
				c.Plugin, c.Version, c.Digest)
		case verdictTrustSignatureRecorded:
			fmt.Fprintf(diagOut, "  plugin trust: %s: signature recorded, against the artifact this entry already names at version %s with digest %s\n",
				c.Plugin, c.Version, c.Digest)
		case verdictTrustSignatureCleared:
			fmt.Fprintf(diagOut, "  plugin trust: %s: signature cleared, from the entry at version %s with digest %s, because this pull verified no signature for those bytes\n",
				c.Plugin, c.Version, c.Digest)
		case verdictTrustEntryDropped:
			fmt.Fprintf(diagOut, "  plugin trust: %s: entry dropped, at version %s with digest %s, because nothing attaches it any more\n",
				c.Plugin, c.Version, c.Digest)
		}
	}
}

// reportVerdictPluginTrust is [recordVerdictPluginTrust] for a run that reports
// rather than writes: it returns the bytes the write would have put beside the
// lockfile, and nil where that write would not have happened at all — the same
// question the write asks itself, answered from the same outcome.
//
// Nothing is announced here, and that is not an omission: every line the write
// prints states a change the record has undergone — a file that now exists, an
// identity now recorded, an entry now gone — and on a reporting run it has
// undergone none. The record's own path appears in the report instead, which is
// where a reader of a preview looks for it. That this half holds no writer at all
// is what keeps the distinction from resting on anyone remembering it.
func reportVerdictPluginTrust(root, releasePath string, attachments []workspaceconfig.VerdictAttachment, resolutions []verdictPluginResolution, mode catalog.PinReuseMode) (int, *dryrunreport.File, error) {
	outcome, err := verdictTrustAfterPull(releasePath, attachments, resolutions, mode)
	if err != nil {
		return 0, nil, err
	}
	if !outcome.Changed() {
		return outcome.Recordable, nil, nil
	}
	if err := writescope.CheckWrite(root, pluginstrust.PathFor(releasePath)); err != nil {
		return 0, nil, err
	}
	out, merr := pluginstrust.CanonicalBytes(outcome.Trust)
	if merr != nil {
		return 0, nil, merr
	}
	return outcome.Recordable, &dryrunreport.File{Path: pluginstrust.PathFor(releasePath), Content: out}, nil
}

// verdictTrustAfterPull works out the record this pull leaves behind, and puts
// nothing on disk. Every rule the doc comment on [recordVerdictPluginTrust]
// states lives here; that function is this one plus the write.
func verdictTrustAfterPull(releasePath string, attachments []workspaceconfig.VerdictAttachment, resolutions []verdictPluginResolution, mode catalog.PinReuseMode) (verdictTrustOutcome, error) {
	recordable := recordableVerdictResolutions(resolutions)
	path := pluginstrust.PathFor(releasePath)
	trust, created, err := pluginstrust.LoadOrStart(path)
	if err != nil {
		return verdictTrustOutcome{}, err
	}

	// Whether anything is written at all is decided by whether the record's content
	// actually moves, not by whether there was anything to consider. That is what
	// keeps two cases apart which a single length check cannot: a workspace with no
	// record and nothing to record must end with no file, while a workspace whose
	// record still holds an entry nothing accounts for must be reached so the entry
	// can go.
	var changes []verdictTrustChange
	for _, r := range recordable {
		fresh := pluginstrust.TrustedPlugin{
			Plugin:          r.Coordinate,
			ResolvedVersion: r.Version,
			Digest:          r.Digest,
			Deprecation:     r.Deprecation,
			Signature:       r.Signature,
		}
		held, isHeld := trust.Find(r.Coordinate)
		if isHeld && !reresolvedByThisPull(mode, r.BareURI) {
			// The identity is held, which is this record's whole rule. A withdrawal
			// and a signature fact are not identities, so each is still recorded —
			// onto the entry whose recorded digest is the one just resolved, which is
			// what keeps a fact about one artifact from being written against
			// another. Without this arm the fields would be reachable only on a first
			// sighting, and the ordinary cases — a publisher withdrawing an artifact a
			// workspace already trusts, or a record that lost its signature fact —
			// would never reach them at all.
			//
			// The entry's own recorded identity is what the announcement names, not
			// the resolution's: the record accepts a fact only where the two digests
			// already agree, so naming the entry's own states what the reader will
			// find in the file rather than what was resolved.
			if trust.RecordDeprecation(r.Coordinate, r.Digest, r.Deprecation) {
				changes = append(changes, verdictTrustChange{
					Kind:    verdictTrustDeprecationRecorded,
					Plugin:  r.Coordinate,
					Version: held.ResolvedVersion,
					Digest:  held.Digest,
				})
			}
			if trust.RecordSignature(r.Coordinate, r.Digest, r.Signature) {
				kind := verdictTrustSignatureRecorded
				if r.Signature == nil {
					kind = verdictTrustSignatureCleared
				}
				changes = append(changes, verdictTrustChange{
					Kind:    kind,
					Plugin:  r.Coordinate,
					Version: held.ResolvedVersion,
					Digest:  held.Digest,
				})
			}
			continue
		}
		// Equal rather than ==, because the record stopped being comparable with ==
		// the moment it gained a pointer field: == still compiles and compares the
		// pointer, so two identical withdrawals would read as a change.
		if isHeld && held.Equal(fresh) {
			continue
		}
		trust.Upsert(fresh)
		changes = append(changes, classifyVerdictTrustUpsert(held, isHeld, fresh)...)
	}
	accounted, complete := verdictAttachmentCoordinates(attachments, resolutions)
	for _, coordinate := range trust.Unaccounted(accounted, complete) {
		if !prunedByThisPull(mode, coordinate) {
			continue
		}
		// Read before the removal, because the identity the entry carried is what
		// the announcement names and Remove is the last moment it exists.
		dropped, _ := trust.Find(coordinate)
		if trust.Remove(coordinate) {
			changes = append(changes, verdictTrustChange{
				Kind:    verdictTrustEntryDropped,
				Plugin:  coordinate,
				Version: dropped.ResolvedVersion,
				Digest:  dropped.Digest,
			})
		}
	}
	// Ordered by coordinate, which is the order the record itself is written in, so
	// the lines a reader sees line up with the diff they read next. The two loops
	// above reach entries in two different orders — the configuration's attachment
	// order and the record's own — and neither is the written file's, so leaving
	// them interleaved would put the announcement and the diff in different orders
	// for no reason a reader could see.
	sort.SliceStable(changes, func(i, j int) bool { return changes[i].Plugin < changes[j].Plugin })
	return verdictTrustOutcome{Trust: trust, Recordable: len(recordable), Changes: changes, Created: created}, nil
}

// classifyVerdictTrustUpsert names which change the replacement of the entry at
// fresh.Plugin is, given what the record held for that coordinate.
//
// It sits beside the decision that performs the replacement rather than beside
// the write that persists it, because the two sides of the comparison exist only
// here: once the entry is replaced the record holds one identity, and a writer
// asking "what moved" would have nothing left to compare against. Re-deriving the
// answer at the write would also be a second implementation of a question this
// file answers once.
//
// The caller has already ruled out an entry that did not move at all. A new or
// re-identified entry is one change. Where the identity did not move, what moved
// is one or both of the two facts recorded about the artifact those fields already
// name, and each fact that moved is a change of its own, deprecation first — so a
// reader is told about each rather than about whichever one happened to be
// compared first.
func classifyVerdictTrustUpsert(held pluginstrust.TrustedPlugin, isHeld bool, fresh pluginstrust.TrustedPlugin) []verdictTrustChange {
	change := verdictTrustChange{Plugin: fresh.Plugin, Version: fresh.ResolvedVersion, Digest: fresh.Digest}
	switch {
	case !isHeld:
		change.Kind = verdictTrustRecorded
		return []verdictTrustChange{change}
	case held.ResolvedVersion != fresh.ResolvedVersion || held.Digest != fresh.Digest:
		change.Kind = verdictTrustIdentityAdopted
		change.PriorVersion, change.PriorDigest = held.ResolvedVersion, held.Digest
		return []verdictTrustChange{change}
	}
	// Each fact is compared through Equal over a record carrying that fact alone, so
	// "has this pointer field moved" keeps its one answer — the record's own — rather
	// than gaining a second comparison here.
	var changes []verdictTrustChange
	if !(pluginstrust.TrustedPlugin{Deprecation: held.Deprecation}).Equal(pluginstrust.TrustedPlugin{Deprecation: fresh.Deprecation}) {
		deprecation := change
		deprecation.Kind = verdictTrustDeprecationRecorded
		if fresh.Deprecation == nil {
			// The identity did not move and the fresh resolution carries no marker, so
			// what the entry loses is the deprecation it held. Reachable only from a
			// run that asked for this plugin to be re-resolved: every other pull adds a
			// marker to the entry it holds and clears none.
			deprecation.Kind = verdictTrustDeprecationCleared
		}
		changes = append(changes, deprecation)
	}
	if !(pluginstrust.TrustedPlugin{Signature: held.Signature}).Equal(pluginstrust.TrustedPlugin{Signature: fresh.Signature}) {
		signature := change
		signature.Kind = verdictTrustSignatureRecorded
		if fresh.Signature == nil {
			signature.Kind = verdictTrustSignatureCleared
		}
		changes = append(changes, signature)
	}
	return changes
}

// reresolvedByThisPull reports whether this pull re-resolves the attachment
// authored as bareURI, so a record already held for it is replaced rather than
// kept.
//
// It reads the invocation the prior-pin reuse decision reads, through that
// decision's own questions, so "what kind of pull is this" is answered once for
// the whole run: a bare `--upgrade` floats every pin and so re-resolves
// everything, and an `--upgrade` naming targets re-resolves only the attachments
// it named. Every other pull re-resolves nothing.
//
// A named target is matched against the authored reference, which is the form
// `--upgrade` documents matching against — see verdictUpgradeTargets, which
// derives the accepted targets and carries why an attachment contributes its
// authored reference. Both kinds of attachment reach this arm: one attached only
// at a verdict role, whose identity is recorded beside the lockfile and pinned
// nowhere, and one attached at a verdict role at one site and an output-shaping
// role at another, pinned there and recorded here.
func reresolvedByThisPull(mode catalog.PinReuseMode, bareURI string) bool {
	if mode.FullRefresh() {
		return true
	}
	if !mode.Targeted() {
		return false
	}
	for _, target := range mode.UpgradeTargets {
		if target == bareURI {
			return true
		}
	}
	return false
}

// prunedByThisPull reports whether this pull may drop the recorded entry
// addressed by coordinate, given that nothing in the configuration accounts for
// it any more.
//
// It reads the same invocation reresolvedByThisPull reads, through the same two
// questions the prior-pin reuse decision asks, so the invocations that may change
// the record are one set rather than two that could drift apart: a bare
// `--upgrade` is a full refresh and drops every unaccounted-for entry, an
// `--upgrade` naming targets drops only the entries it named, and every other
// invocation drops nothing.
//
// # Why a named target is matched against the COORDINATE here
//
// Re-resolution matches a target against the attachment's authored reference,
// because the thing being re-resolved is an attachment. Pruning matches against
// the record's identity coordinate, because the thing being dropped is a record
// entry and its plugin is no longer attached anywhere — there is no authored
// reference left to match. The coordinate is also the only string the reader has
// seen: it is what the refusal over a stale entry prints, and what that refusal's
// remediation tells them to name. Two subjects, two keys, and neither is the other
// spelled differently.
func prunedByThisPull(mode catalog.PinReuseMode, coordinate string) bool {
	if mode.FullRefresh() {
		return true
	}
	if !mode.Targeted() {
		return false
	}
	for _, target := range mode.UpgradeTargets {
		if target == coordinate {
			return true
		}
	}
	return false
}
