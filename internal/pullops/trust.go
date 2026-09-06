// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package pullops

import (
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/internal/wasm/embedded"
	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/pluginstrust"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
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
// same pull, or nil when none was found. It is carried alongside the identity
// rather than looked up later because it is a fact about the very bytes this
// resolution identified: discovered anywhere else it could describe a different
// artifact than the one recorded beside it.
//
// An empty Coordinate means the plugin was not in the content cache, so there was
// nothing to identify.
type verdictPluginResolution struct {
	BareURI     string
	Coordinate  string
	Version     string
	Digest      string
	Deprecation *pluginstrust.Deprecation
}

// errVerdictPluginIdentityChanged reports that a pull resolved an attachment
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
var errVerdictPluginIdentityChanged = errors.New("the plugin identity recorded as trusted no longer matches what this resolution reports")

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
// It runs on every pull — including one that only verifies, which writes nothing
// and is exactly the surface a user reaches to be told whether the committed files
// still describe what resolves today. A verify that passed over the record while
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
			releasePath, r.BareURI, errVerdictPluginIdentityChanged)
	}
	return nil
}

// recordVerdictPluginTrust writes the plugin identities resolved for a Release's
// verdict-returning attachments into `plugins-trust.yaml` beside that Release's
// lockfile, and returns how many identities the record covers.
//
// Nothing to record and no record yet means no file. A workspace that attaches no
// governance verdict plugin and no linter plugin resolves no identities, so with
// no record already beside the lockfile this writes no file and says nothing — an
// empty record would assert that there is something to compare against when there
// is not. The same holds for a workspace whose only attachments resolve from the
// binary, for the reason below.
//
// A record already present for a plugin is left exactly as it is, unless this pull
// re-resolved that plugin — a full refresh, or a targeted upgrade naming it. Those
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
func recordVerdictPluginTrust(releasePath string, attachments []workspaceconfig.VerdictAttachment, resolutions []verdictPluginResolution, mode catalog.PinReuseMode, diagOut io.Writer) (int, error) {
	recordable := recordableVerdictResolutions(resolutions)
	path := pluginstrust.PathFor(releasePath)
	trust, created, err := pluginstrust.LoadOrStart(path)
	if err != nil {
		return 0, err
	}

	// Whether anything is written at all is decided by whether the record's content
	// actually moves, not by whether there was anything to consider. That is what
	// keeps two cases apart which a single length check cannot: a workspace with no
	// record and nothing to record must end with no file, while a workspace whose
	// record still holds an entry nothing accounts for must be reached so the entry
	// can go.
	changed := false
	for _, r := range recordable {
		fresh := pluginstrust.TrustedPlugin{
			Plugin:          r.Coordinate,
			ResolvedVersion: r.Version,
			Digest:          r.Digest,
			Deprecation:     r.Deprecation,
		}
		held, isHeld := trust.Find(r.Coordinate)
		if isHeld && !reresolvedByThisPull(mode, r.BareURI) {
			// The identity is held, which is this record's whole rule. A withdrawal
			// is not an identity, so it is still recorded — onto the entry whose
			// recorded digest is the one just resolved, which is what keeps a fact
			// about one artifact from being written against another. Without this
			// arm the field would be reachable only on a first sighting, and the
			// ordinary case — a publisher withdrawing an artifact a workspace
			// already trusts — would never reach it at all.
			if trust.RecordDeprecation(r.Coordinate, r.Digest, r.Deprecation) {
				changed = true
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
		changed = true
	}
	accounted, complete := verdictAttachmentCoordinates(attachments, resolutions)
	for _, coordinate := range trust.Unaccounted(accounted, complete) {
		if !prunedByThisPull(mode, coordinate) {
			continue
		}
		if trust.Remove(coordinate) {
			changed = true
		}
	}
	if !changed {
		return len(recordable), nil
	}
	if err := pluginstrust.Write(path, trust); err != nil {
		return 0, err
	}
	if created {
		fmt.Fprintf(diagOut, "  notice: created %s, recording the plugin identity resolved for each attachment that returns an accept-or-reject verdict; "+
			"commit it alongside the lockfile, because one shared record is what lets every build hold those attachments to the same plugin identities\n", path)
	}
	return len(recordable), nil
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
