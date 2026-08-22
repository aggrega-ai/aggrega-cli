// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package pluginstrust

import (
	"errors"
	"fmt"
	"io/fs"
	"strings"

	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// ErrVerdictAttachmentNotConfigured reports that the record holds a plugin
// trusted for an accept-or-reject verdict which the configuration no longer
// attaches at any verdict role.
//
// It is the loud half of an asymmetry, and the asymmetry is the whole reason the
// record exists. The two directions of a disagreement between the configuration
// and the record are NOT one condition:
//
//   - The configuration attaches a plugin the record does not hold. That is a
//     first sighting — a gate somebody has just added — and it is benign: the
//     resolution that acquires the plugin records it. Nothing halts.
//   - The record holds a plugin nothing attaches. That is a gate that was
//     reviewed and is now absent, and it halts. Dropping a gate changes no
//     rendered byte, so a build with the gate removed is indistinguishable from
//     a build the gate approved — which is precisely what a reviewer of the
//     output cannot see and what this condition exists to say out loud.
//
// It is distinct from the digest disagreement a resolution raises. That one
// refuses to RUN a plugin whose bytes are not the reviewed ones; this one refuses
// to build with a reviewed gate silently missing. The first is about identity,
// the second about presence, and their remediations differ: restore the artifact,
// versus restore the attachment.
//
// # What it cannot catch, so that nothing here is read as claiming more
//
// Holding the configuration to the record establishes that every recorded gate is
// still attached and that each one's artifact is the recorded one. It establishes
// nothing about whether an attached gate actually returns a verdict, and one case
// of that is not caught anywhere: an attachment whose entry function names an
// export the plugin declares, which is not a verdict export and whose output
// carries no key outside the verdict envelope — the empty object being the
// simplest — runs, returns no verdict, and lets the build through. The output that
// results is indistinguishable from an approving gate's, and no comparison against
// a record can notice it, because nothing about the record is wrong: the plugin
// that ran is exactly the recorded one, and it simply ran nothing.
//
// Two neighbouring cases DO halt, and they are named so this bound is not read as
// wider than it is: an entry function naming an export the plugin does not
// declare is refused when the plugin is loaded, and an output carrying a key the
// verdict envelope does not declare is refused rather than read as a verdict list
// that happens to be empty.
func ErrVerdictAttachmentNotConfigured() error { return errVerdictAttachmentNotConfigured }

var errVerdictAttachmentNotConfigured = cerrors.Register("pluginstrust/verdict-attachment-not-configured", cerrors.Meta{
	ShortCode: "E0247",
	Message:   "a plugin recorded as trusted for an accept-or-reject verdict is no longer attached at any verdict role",
})

// LoadOrStart reads the record at path, or starts an empty one when the file is
// not there yet. It reports whether the record was started rather than read,
// which is what a creation notice keys on.
//
// It is the single home of what an ABSENT record and a DAMAGED one mean, and the
// two answers are deliberately different:
//
//   - Absence is a first-run state, not a fault. The file is created by the
//     resolution that first has something to record, so a caller meets an absent
//     record on every workspace that has not resolved a verdict-returning
//     attachment yet. It holds no plugins, so it pins nothing and holds nothing
//     to a comparison — silence, never a halt.
//   - Any other read failure — an unreadable file, or one that does not satisfy
//     its schema — is returned. Treating a damaged record as an empty one would
//     discard exactly the identities a later resolution is meant to be held to,
//     and would make deleting the contents of the file a way to switch the
//     comparison off.
//
// Every reader of the record goes through here — the resolution that holds
// itself to it, the write that fills it, and the enforcement that refuses a
// dropped gate — so that distinction is answered once. Answered per caller it
// would be free to be a halt on one surface and a silent skip on another, which
// is the divergence a reader of either surface alone could not see.
func LoadOrStart(path string) (*Trust, bool, error) {
	trust, err := Load(path)
	if err == nil {
		return trust, false, nil
	}
	if errors.Is(err, fs.ErrNotExist) {
		return &Trust{Version: CurrentVersion}, true, nil
	}
	return nil, false, err
}

// HoldConfigurationToRecord refuses a configuration that no longer attaches a
// plugin the record holds as trusted, and says nothing at all in every other
// case.
//
// path is the record's own path, named in the diagnostic so a reader can open
// the file the halt is about. attachments is the workspace's verdict attachment
// set — read through workspaceconfig.Config.VerdictAttachments, the one spelling
// of that set, so this comparison and the resolution that acquires those plugins
// cannot disagree about which attachments are verdict attachments.
//
// coordinateFor answers "which plugin does this attachment name", returning the
// plugin's identity coordinate and whether it could be determined. The caller
// supplies it over its own catalog view, because the answer needs a read of the
// Plugin entity: the record is addressed by the plugin's identity coordinate
// while an attachment is authored as an entity reference, and the two are not the
// same string — an attachment authored under a namespaced reference resolves to a
// coordinate that shares only its trailing segments. Comparing the authored
// reference against the recorded address would therefore report a gate as dropped
// whenever it was merely authored in the longer form.
//
// # Why an attachment whose plugin cannot be identified suppresses the refusal
//
// If any configured attachment yields no coordinate, this returns nil. The reason
// is that the refusal is a statement about a record entry that NOTHING accounts
// for, and an attachment whose plugin cannot be identified might be the very one
// that accounts for it — so refusing would name a gate as dropped on the strength
// of a read that failed. Nothing is lost by staying quiet: an attachment the
// configuration names and the host cannot resolve halts at resolution, loudly and
// with the resolution's own diagnostic, so the build does not proceed either way.
//
// The case that matters most is the opposite one and it is covered: a
// configuration attaching NO verdict plugin at all yields no undeterminable
// attachment, so a record holding entries refuses. That is the whole-gate removal
// the condition exists for.
//
// A nil receiver holds no records and refuses nothing, so a caller that loaded no
// file need not branch before asking — the same contract Find and PinMaps offer.
func (t *Trust) HoldConfigurationToRecord(path string, attachments []workspaceconfig.VerdictAttachment, coordinateFor func(uri string) (string, bool)) error {
	if t == nil || len(t.Plugins) == 0 {
		return nil
	}
	attached := make(map[string]struct{}, len(attachments))
	for _, attachment := range attachments {
		coordinate, determined := coordinateFor(attachment.Ref.URI)
		if !determined {
			return nil
		}
		attached[coordinate] = struct{}{}
	}

	// Record order, which the canonical writer keeps sorted by coordinate, so a
	// configuration that drops several gates names them in one stable order.
	var dropped []string
	for _, p := range t.Plugins {
		if p.Plugin == "" {
			continue
		}
		if _, isAttached := attached[p.Plugin]; isAttached {
			continue
		}
		dropped = append(dropped, p.Plugin)
	}
	if len(dropped) == 0 {
		return nil
	}
	// The sentence names the dropped set once and then refers back to it, so the
	// pronoun follows the count: "names them" over a single entry reads as though
	// the diagnostic had lost count of what it just listed.
	them := "them"
	if len(dropped) == 1 {
		them = "it"
	}
	return fmt.Errorf(
		"%w: %s records %s as trusted, but no verdict attachment in the workspace configuration names %s; "+
			"a gate that was reviewed and is now absent leaves output indistinguishable from output that gate approved. "+
			"Attach the plugin again under `plugins.governance` at an `ir-verdict` or `render-verdict` role, or under `plugins.linter`; "+
			"or, if it was meant to go, remove %s and run `aggrega pull` to record the attachments configured now",
		ErrVerdictAttachmentNotConfigured(), path, strings.Join(dropped, ", "), them, path)
}
