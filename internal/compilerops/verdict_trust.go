// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package compilerops

import (
	"errors"
	"fmt"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/pipeline"
	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
	"github.com/aggrega-ai/aggrega-cli/pkg/pluginstrust"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// pluginCoordinateReader returns the "which plugin does this attachment name"
// answer over a catalog view: the plugin's identity coordinate, and whether it
// could be determined at all.
//
// It is the single home of that question, and it is UNEXPORTED so it stays the
// only home: every surface that holds a configuration to the record of the plugin
// identities trusted for its verdict attachments reaches it through
// VerdictPluginTrust, and the build's check of recorded signatures reads the same
// reading beneath it, readVerdictPlugin, so no caller can assemble a second answer
// to "which plugin does this attachment name". It answers through the ONE existing
// derivation — the Plugin entity's own artifact reference — rather than by
// pattern-matching the authored reference. That distinction is not cosmetic: an
// attachment authored under a namespaced reference resolves to a coordinate that
// shares only its trailing segments, so a textual comparison would report a gate
// as dropped whenever it was merely authored in the longer form.
//
// The answer is readVerdictPlugin's coordinate, so which plugin an attachment
// names and which tier serves it are one reading.
func pluginCoordinateReader(view *catalog.LazyLookup) func(uri string) (string, bool) {
	return func(uri string) (string, bool) {
		read, ok := readVerdictPlugin(view, uri)
		return read.coordinate, ok
	}
}

// verdictPluginRead is what a build reads about the plugin one verdict
// attachment names: the plugin's identity coordinate, and whether its entity is
// served by the content cache a pull filled from a registry rather than by the
// catalog view.
type verdictPluginRead struct {
	coordinate string
	fromCache  bool
}

// readVerdictPlugin reads the plugin a verdict attachment names, through the ONE
// existing derivation — the Plugin entity's own artifact reference — and reports
// which tier served the entity.
//
// Both tiers a verdict attachment can be served from are consulted, in the order
// resolution consults them: the lazily-enumerated catalog set first, then the
// content cache the pull populates for a plugin that is cached but never pinned.
// A cache read that fails is reported as undetermined rather than as an answer —
// the failure is real and surfaces at resolution with its own diagnostic, and
// guessing here would let a failed read masquerade as a missing gate.
//
// Which tier served the entity is reported because it is where the plugin came
// from, which the record beside the lockfile deliberately does not say: the check
// of recorded signatures holds a plugin to the signature requirement only where a
// pull acquired it from a registry, and the cache tier is that acquisition.
func readVerdictPlugin(view *catalog.LazyLookup, uri string) (verdictPluginRead, bool) {
	if view == nil || uri == "" {
		return verdictPluginRead{}, false
	}
	if entity, found := view.PluginByURI(uri); found {
		return verdictPluginRead{coordinate: entity.ArtifactReference()}, true
	}
	entity, _, found, err := view.PluginFromCacheByURI(uri)
	if err != nil || !found {
		return verdictPluginRead{}, false
	}
	return verdictPluginRead{coordinate: entity.ArtifactReference(), fromCache: true}, true
}

// VerdictPluginTrust reads the record of the plugin identities trusted for this
// Release's verdict attachments, holds the workspace configuration to it, and
// returns what that record contributes to loading one of their plugins: the
// recorded digests as the pin maps a resolution verifies against, the digest of
// each recorded signature's cached bundle, which the load re-hashes, and the hold a
// plugin the record carries no signature for is put to when it loads
// (unrecordedVerdictPluginRefusal, the rule the build's check of recorded
// signatures applies to the same plugin). So every surface that composes through
// here — a build, a Release-scoped validation and a Release-scoped lint, from the
// command line, the daemon and the assistant tool alike — holds a verdict plugin to
// its recorded signature at the moment it loads it.
//
// It is the single home of the read side of that record. Both things it does
// belong to one act and are deliberately not separable by a caller: a surface
// that verified the recorded digests without refusing a dropped gate would check
// that the gates it still runs are the reviewed ones while saying nothing about a
// gate it no longer runs at all — which is the disagreement that changes no
// rendered byte and is therefore the one a reviewer of the output cannot see.
//
// releasePath locates the record: it sits beside that Release, where the pull that
// wrote it put it. An empty releasePath is refused rather than defaulted, because
// the record's path is derived from it — defaulting would read a file in whatever
// directory the process happens to be in, and finding none there would report a
// workspace as unrecorded when it is merely being asked from the wrong place.
//
// An absent record yields empty pins and no refusal here. That is a first-run state
// rather than a fault, and the empty result is honest about it: nothing is
// recorded, so no digest is held to anything. The signature requirement is the one
// hold an absent record does not escape: a cache-served plugin it requires a
// signature for is refused when it loads, as the build refuses it, so deleting the
// record is not a way to run it unverified. A record that exists and cannot be
// read halts — see pluginstrust.LoadOrStart, which owns that distinction.
//
// # What this establishes, and the one thing it does not
//
// Held to its record, a build runs every gate the record names and runs each one
// from the artifact the record names. It does NOT establish that a gate which ran
// actually produced a verdict: an attachment whose entry function names an export
// the plugin declares, which is not a verdict export and whose output carries no
// key outside the verdict envelope, runs and returns nothing to reject on — and
// the plugin that ran is exactly the recorded one, so there is nothing for a
// comparison against the record to disagree with. Recording an identity is
// therefore not on its own sufficient for a gate to have judged anything, and no
// surface should be written or documented as though it were.
// pluginstrust.ErrVerdictAttachmentNotConfigured carries the same bound, together
// with the neighbouring cases that do halt.
func VerdictPluginTrust(view *catalog.LazyLookup, wsCfg workspaceconfig.Config, releasePath string) (resolver.VerdictPluginTrust, error) {
	return verdictPluginTrust(view, wsCfg, releasePath, nil)
}

// verdictPluginTrust is VerdictPluginTrust with the record the caller composed and
// has not yet written, or nil, which reads the record from disk.
//
// The composed record serves the signature half alone — the bundle digests and the
// hold — the same half the build's check of recorded signatures reads it for
// (WithComposedTrustRecord): the resolution that composes it renders before it
// writes it, so a load in that render reading the file would hold a plugin to the
// record the run is about to replace, and on a first resolution to none. The
// identity half — the configuration held to the record, and the digests compared —
// reads the file, as it does on every surface.
func verdictPluginTrust(view *catalog.LazyLookup, wsCfg workspaceconfig.Config, releasePath string, composed *pluginstrust.Trust) (resolver.VerdictPluginTrust, error) {
	if releasePath == "" {
		return resolver.VerdictPluginTrust{}, fmt.Errorf("verdict-plugin trust record: no Release path supplied, so the record beside the Release cannot be located")
	}
	path := pluginstrust.PathFor(releasePath)
	trust, _, err := pluginstrust.LoadOrStart(path)
	if err != nil {
		return resolver.VerdictPluginTrust{}, err
	}
	if err := trust.HoldConfigurationToRecord(releasePath, wsCfg.VerdictAttachments(), pluginCoordinateReader(view)); err != nil {
		return resolver.VerdictPluginTrust{}, err
	}
	signatures := trust
	if composed != nil {
		signatures = composed
	}
	pins := trust.PinMaps()
	pins.BundleDigests = signatures.PinMaps().BundleDigests
	return resolver.VerdictPluginTrust{
		PinMaps:        pins,
		HoldUnrecorded: verdictPluginSignatureHold(wsCfg, releasePath, signatures),
	}, nil
}

// verdictPluginSignatureHold is the hold a verdict plugin's load puts a plugin
// read from the content cache to when the record carries no signature for it: the
// build's own rule for the same plugin (unrecordedVerdictPluginRefusal), over the
// same record and the same configuration. A plugin read from the content cache is
// what reaches the hold, so the entity is the cache's by construction.
func verdictPluginSignatureHold(wsCfg workspaceconfig.Config, releasePath string, record *pluginstrust.Trust) func(uri, coordinate string) error {
	registry := wsCfg.DefaultRegistryLocation().Prefix()
	recordPath := pluginstrust.PathFor(releasePath)
	return func(uri, coordinate string) error {
		if entry, held := record.Find(coordinate); held && entry.Signature != nil {
			return nil
		}
		return unrecordedVerdictPluginRefusal(wsCfg, registry, verdictPluginRead{coordinate: coordinate, fromCache: true},
			uri, recordPath, releasePath)
	}
}

// ApplyGovernanceComposition composes, onto pipeline inputs, both halves of what
// the governance and linter sockets need: the attachment list the pipeline
// dispatches, and the digests recorded as trusted for the attachments that return
// an accept-or-reject verdict.
//
// Every surface that runs those attachments calls this: a build — from the command
// line, from the daemon, and from the editor's build tool — and a Release-scoped
// validation, which runs the same pre-render pipeline and therefore dispatches the
// same guests, from each of those same three front ends. It exists as one call
// because the two halves are one act. Composing the attachment list without
// supplying the recorded digests produces a run that dispatches every configured
// gate and holds none of them to the identity that was reviewed, and it does so
// silently: the digests arrive as a map, an unsupplied map is an empty one, and an
// empty one means "nothing is recorded" rather than "nobody supplied this". No
// type checker and no test that does not specifically look can tell the two apart,
// which is why the assignment is not left to each surface to remember.
//
// What it composes, and why each half is shaped the way it is:
//
//   - The attachment list. Under a lockfile the label-transform attachments are
//     reconstructed from the lockfile and the live ones are overridden, so
//     rendered output is invariant to a `plugins.governance` transform change made
//     after the pull — a transform shapes output bytes and is pinned. The verdict
//     attachments shape no output and are cache-not-pin, so they are read live and
//     composed back on. With no lockfile there is nothing to reconstruct from and
//     the live list stands as authored.
//   - The record's contribution — the recorded digests, the recorded signatures'
//     bundle digests and the signature hold — which is supplied whether or not a
//     lockfile loaded. The record is a committed file of its own beside the
//     lockfile, so making its enforcement conditional on the lockfile would turn a
//     failed lockfile read into a way to run the gates unchecked.
//
// It mutates the inputs in place. The rewritten attachment list lands on a COPY
// of the workspace configuration, which the inputs are then pointed at: the
// configuration reaches the pipeline by pointer, and a long-lived surface hands
// the same one to every request it serves, so writing the list through that
// pointer would rewrite the caller's own configuration and leak this build's
// reconstructed attachments into the next request. Copying here is what keeps
// the rewrite scoped to this build, and it leaves the config on disk and every
// other request's untouched.
//
// A caller that supplied no configuration is refused: there is nothing to
// compose onto, and the graph entry refuses such a run anyway, so reporting it
// at the composition names the omission where it can still be fixed.
func ApplyGovernanceComposition(in *pipeline.Inputs, view *catalog.LazyLookup, releasePath string) error {
	return applyGovernanceComposition(in, view, releasePath, nil)
}

// applyGovernanceComposition is ApplyGovernanceComposition with the record of plugin
// identities the caller composed and has not yet written, or nil, which reads the
// record from disk; see verdictPluginTrust for the half it serves.
func applyGovernanceComposition(in *pipeline.Inputs, view *catalog.LazyLookup, releasePath string, composedTrust *pluginstrust.Trust) error {
	if in.WorkspaceConfig == nil {
		return errors.New("governance composition: no workspace configuration was supplied on the pipeline inputs")
	}
	trusted, err := verdictPluginTrust(view, *in.WorkspaceConfig, releasePath, composedTrust)
	if err != nil {
		return err
	}
	in.VerdictPluginTrust = trusted
	if in.Lockfile != nil {
		composed := *in.WorkspaceConfig
		composed.Plugins.Governance = append(
			lockfile.MutatorAttachments(in.Lockfile),
			in.WorkspaceConfig.Plugins.VerdictGovernanceRefs()...)
		in.WorkspaceConfig = &composed
	}
	return nil
}
