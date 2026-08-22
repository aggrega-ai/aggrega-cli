// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"errors"
	"fmt"
	"io"
	"io/fs"

	"github.com/aggrega-ai/aggrega-cli/internal/wasm/embedded"
	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/pluginstrust"
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
// An empty Coordinate means the plugin was not in the content cache, so there was
// nothing to identify.
type verdictPluginResolution struct {
	BareURI    string
	Coordinate string
	Version    string
	Digest     string
}

// recordVerdictPluginTrust writes the plugin identities resolved for a Release's
// verdict-returning attachments into `plugins-trust.yaml` beside that Release's
// lockfile, and returns how many identities it recorded.
//
// Nothing to record means no file. A workspace that attaches no governance
// verdict plugin and no linter plugin resolves no identities, and this writes no
// file and says nothing — an empty record would assert that there is something to
// compare against when there is not. The same holds for a workspace whose only
// attachments resolve from the binary, for the reason below.
//
// A record already present for a plugin is left exactly as it is, unless this pull
// re-resolved that plugin — a full refresh, or a targeted upgrade naming it. Those
// are the two invocations where the user asked for fresh resolution, so replacing
// the record is the answer to what they asked; every other pull holds the recorded
// identity, because silently adopting whatever was resolved this time would make
// the record follow the resolution instead of governing it.
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
func recordVerdictPluginTrust(releasePath string, resolutions []verdictPluginResolution, mode catalog.PinReuseMode, diagOut io.Writer) (int, error) {
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
	if len(recordable) == 0 {
		return 0, nil
	}

	path := pluginstrust.PathFor(releasePath)
	trust, created, err := loadOrStartTrustRecord(path)
	if err != nil {
		return 0, err
	}
	for _, r := range recordable {
		if _, held := trust.Find(r.Coordinate); held && !reresolvedByThisPull(mode, r.BareURI) {
			continue
		}
		trust.Upsert(pluginstrust.TrustedPlugin{
			Plugin:          r.Coordinate,
			ResolvedVersion: r.Version,
			Digest:          r.Digest,
		})
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

// loadOrStartTrustRecord reads the record at path, or starts an empty one when the
// file is not there yet. It reports whether the record is new, which is what the
// creation notice keys on.
//
// Absence is a first-run state rather than a fault: the file is created by the
// resolution that first has something to record. Any other read failure — an
// unreadable file, or one that does not satisfy its schema — is returned, because
// replacing a record nobody could read would discard exactly the identities a
// later resolution is meant to be held to.
func loadOrStartTrustRecord(path string) (*pluginstrust.Trust, bool, error) {
	trust, err := pluginstrust.Load(path)
	if err == nil {
		return trust, false, nil
	}
	if errors.Is(err, fs.ErrNotExist) {
		return &pluginstrust.Trust{Version: pluginstrust.CurrentVersion}, true, nil
	}
	return nil, false, err
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
// `--upgrade` documents matching against. Note that a target naming a plugin
// attached ONLY at a verdict role halts before this is ever consulted: such a
// plugin is deliberately absent from the lockfile, and a target absent from the
// lockfile has no pin to drop. The case this arm serves is the plugin attached at
// a verdict role at one site and an output-shaping role at another — pinned there,
// recorded here, and re-resolved by naming it.
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

// trustedPluginsSummary renders the trailing field the pull's completion line
// carries when plugin identities were recorded, and nothing at all when none
// were.
//
// Nothing rather than a zero: a workspace that attaches no verdict-returning
// plugin has no such identities to have, so reporting `=0` would answer a
// question that was never asked of that workspace.
func trustedPluginsSummary(recorded int) string {
	if recorded == 0 {
		return ""
	}
	return fmt.Sprintf(", trusted_plugins=%d", recorded)
}
