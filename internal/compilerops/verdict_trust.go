// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package compilerops

import (
	"errors"
	"fmt"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/pipeline"
	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
	"github.com/aggrega-ai/aggrega-cli/pkg/pluginpath"
	"github.com/aggrega-ai/aggrega-cli/pkg/pluginstrust"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// pluginCoordinateReader returns the "which plugin does this attachment name"
// answer over a catalog view: the plugin's identity coordinate, and whether it
// could be determined at all.
//
// It is the single home of that question, and it is UNEXPORTED so it stays the
// only home: every surface that holds a configuration to the record of the plugin
// identities trusted for its verdict attachments reaches it through
// VerdictPluginTrust, so no caller can assemble a second answer to "which plugin
// does this attachment name". It answers through the ONE existing derivation — the Plugin
// entity's own artifact reference — rather than by pattern-matching the authored
// reference. That distinction is not cosmetic: an attachment authored under a
// namespaced reference resolves to a coordinate that shares only its trailing
// segments, so a textual comparison would report a gate as dropped whenever it
// was merely authored in the longer form.
//
// Both tiers a verdict attachment can be served from are consulted, in the order
// resolution consults them: the lazily-enumerated catalog set first, then the
// content cache the pull populates for a plugin that is cached but never pinned.
// A cache read that fails is reported as undetermined rather than as an answer —
// the failure is real and surfaces at resolution with its own diagnostic, and
// guessing here would let a failed read masquerade as a missing gate.
func pluginCoordinateReader(view *catalog.LazyLookup) func(uri string) (string, bool) {
	return func(uri string) (string, bool) {
		if view == nil || uri == "" {
			return "", false
		}
		if entity, found := view.PluginByURI(uri); found {
			return entity.ArtifactReference(), true
		}
		entity, _, found, err := view.PluginFromCacheByURI(uri)
		if err != nil || !found {
			return "", false
		}
		return entity.ArtifactReference(), true
	}
}

// VerdictPluginTrust reads the record of the plugin identities trusted for this
// Release's verdict attachments, holds the workspace configuration to it, and
// returns the recorded digests as the pin maps a resolution verifies against.
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
// An absent record yields empty pins and no refusal. That is a first-run state
// rather than a fault, and the empty result is honest about it: nothing is
// recorded, so nothing is held to anything. A record that exists and cannot be
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
func VerdictPluginTrust(view *catalog.LazyLookup, wsCfg workspaceconfig.Config, releasePath string) (pluginpath.PinMaps, error) {
	if releasePath == "" {
		return pluginpath.PinMaps{}, fmt.Errorf("verdict-plugin trust record: no Release path supplied, so the record beside the Release cannot be located")
	}
	path := pluginstrust.PathFor(releasePath)
	trust, _, err := pluginstrust.LoadOrStart(path)
	if err != nil {
		return pluginpath.PinMaps{}, err
	}
	if err := trust.HoldConfigurationToRecord(releasePath, wsCfg.VerdictAttachments(), pluginCoordinateReader(view)); err != nil {
		return pluginpath.PinMaps{}, err
	}
	return trust.PinMaps(), nil
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
//   - The recorded digests, which are supplied whether or not a lockfile loaded.
//     The record is a committed file of its own beside the lockfile, so making its
//     enforcement conditional on the lockfile would turn a failed lockfile read
//     into a way to run the gates unchecked.
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
	if in.WorkspaceConfig == nil {
		return errors.New("governance composition: no workspace configuration was supplied on the pipeline inputs")
	}
	trusted, err := VerdictPluginTrust(view, *in.WorkspaceConfig, releasePath)
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
