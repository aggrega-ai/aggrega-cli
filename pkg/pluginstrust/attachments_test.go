// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package pluginstrust

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// recordOf builds a record holding one plugin at the given coordinate.
func recordOf(coordinates ...string) *Trust {
	t := &Trust{Version: CurrentVersion}
	for _, c := range coordinates {
		t.Upsert(TrustedPlugin{
			Plugin:          c,
			ResolvedVersion: "1.0.0",
			Digest:          "sha256:1111111111111111111111111111111111111111111111111111111111111111",
		})
	}
	return t
}

// attach builds the verdict attachment set for the authored references given.
func attach(uris ...string) []workspaceconfig.VerdictAttachment {
	out := make([]workspaceconfig.VerdictAttachment, 0, len(uris))
	for _, uri := range uris {
		out = append(out, workspaceconfig.VerdictAttachment{
			Socket: workspaceconfig.SocketGovernance,
			Ref:    workspaceconfig.PluginRef{URI: uri},
		})
	}
	return out
}

// resolvesTo answers the coordinate question from a fixed authored-reference →
// coordinate table, so the tests exercise the predicate rather than a catalog.
// A reference absent from the table is undetermined, which is the case a real
// catalog read reports when it cannot serve the entity.
func resolvesTo(table map[string]string) func(string) (string, bool) {
	return func(uri string) (string, bool) {
		coordinate, ok := table[uri]
		return coordinate, ok
	}
}

// releaseFixture is the Release path the record sits beside in these tests. It is
// nested in a directory so the record path the diagnostic prints is derived rather
// than coincidentally equal to the Release path.
var releaseFixture = filepath.Join("env", "prod.yaml")

// TestHoldConfigurationToRecord_RefusesAGateTheConfigurationDropped is the
// security core of the asymmetry: a record holding a plugin that no configured
// attachment names refuses. Dropping a gate changes no rendered byte, so this
// refusal is the only thing that distinguishes a build with the gate removed from
// a build the gate approved.
func TestHoldConfigurationToRecord_RefusesAGateTheConfigurationDropped(t *testing.T) {
	record := recordOf("governance/opa-gate")

	err := record.HoldConfigurationToRecord(releaseFixture, nil, resolvesTo(nil))
	if err == nil {
		t.Fatal("a record holding a gate no attachment names must refuse; got a clean pass")
	}
	if !errors.Is(err, ErrVerdictAttachmentNotConfigured()) {
		t.Errorf("the refusal must name the not-configured condition; got %v", err)
	}
	// The diagnostic has to be actionable: the plugin, and the file the reader
	// should open.
	if !strings.Contains(err.Error(), "governance/opa-gate") {
		t.Errorf("the refusal must name the plugin; got %v", err)
	}
	if !strings.Contains(err.Error(), PathFor(releaseFixture)) {
		t.Errorf("the refusal must name the record beside the Release; got %v", err)
	}
}

// TestHoldConfigurationToRecord_OffersTheExplicitUpgradeAndNotADeletion reads the
// remedy the diagnostic prints, because the text is the only part of a diagnostic
// a reader ever sees.
//
// It asserts three things and each one is a way the remedy has been wrong:
//
//   - The command is printed verbatim and runnable, naming the Release (`--release`
//     is required on the pull command) and the dropped plugin by the coordinate the
//     same sentence just named. A remedy the binary would refuse is not one.
//   - The two remedies stay distinct: re-attach if the removal was a mistake, run
//     the explicit upgrade if it was intended. Only the reader knows which.
//   - It does NOT tell the reader to delete or hand-edit the record. The file's own
//     opening lines say it is committed and not hand-edited, so an instruction to
//     remove it would put two of our own surfaces in contradiction.
func TestHoldConfigurationToRecord_OffersTheExplicitUpgradeAndNotADeletion(t *testing.T) {
	record := recordOf("governance/opa-gate")

	err := record.HoldConfigurationToRecord(releaseFixture, nil, resolvesTo(nil))
	if err == nil {
		t.Fatal("a record holding a gate no attachment names must refuse; got a clean pass")
	}
	msg := err.Error()

	wantCommand := "aggrega pull --upgrade --release " + releaseFixture + " governance/opa-gate"
	if !strings.Contains(msg, wantCommand) {
		t.Errorf("the remedy must print the explicit upgrade verbatim and runnable;\nwant it to contain %q\ngot %v", wantCommand, err)
	}
	// The re-attach remedy, named as its own alternative rather than folded in.
	for _, want := range []string{"Attach the plugin again", "plugins.governance", "plugins.linter"} {
		if !strings.Contains(msg, want) {
			t.Errorf("the remedy must keep re-attaching as a distinct option and say where; missing %q in %v", want, err)
		}
	}
	// The removal instruction that must be gone: the record is committed and not
	// hand-edited, so nothing here may tell a reader to delete or edit it.
	for _, forbidden := range []string{"remove " + PathFor(releaseFixture), "delete", "edit " + PathFor(releaseFixture)} {
		if strings.Contains(msg, forbidden) {
			t.Errorf("the remedy must not tell the reader to remove or edit the record; found %q in %v", forbidden, err)
		}
	}
	// And it says why a plain pull is not the remedy, so a reader who tries one
	// first is not left guessing.
	if !strings.Contains(msg, "only an explicit upgrade removes an entry") {
		t.Errorf("the remedy must say that a plain pull leaves the record alone; got %v", err)
	}
}

// TestHoldConfigurationToRecord_NamesEveryDroppedGateOnOneUpgradeCommand: with
// several gates dropped, the printed upgrade names all of them, so one run of one
// command clears the record rather than one command per plugin — and a targeted
// upgrade naming them together still holds every pin it did not name, which the
// full refresh a reader would otherwise reach for does not.
func TestHoldConfigurationToRecord_NamesEveryDroppedGateOnOneUpgradeCommand(t *testing.T) {
	record := canonicalise(recordOf("linter/house-style", "governance/opa-gate"))

	err := record.HoldConfigurationToRecord(releaseFixture, nil, resolvesTo(nil))
	if err == nil {
		t.Fatal("two dropped gates must refuse")
	}
	wantCommand := "aggrega pull --upgrade --release " + releaseFixture + " governance/opa-gate linter/house-style"
	if !strings.Contains(err.Error(), wantCommand) {
		t.Errorf("the remedy must name every dropped gate on one command;\nwant it to contain %q\ngot %v", wantCommand, err)
	}
}

// TestHoldConfigurationToRecord_AcceptsAnAttachedGate is the discriminating half
// of the test above, over the identical record: with the recorded plugin still
// attached, nothing refuses. Without it, a predicate that refused unconditionally
// would pass the test above and be indistinguishable from a correct one.
func TestHoldConfigurationToRecord_AcceptsAnAttachedGate(t *testing.T) {
	record := recordOf("governance/opa-gate")

	// The authored reference deliberately differs from the coordinate by more than
	// a version suffix — the namespaced form a real workspace authors — so a
	// predicate comparing the two as strings fails here.
	table := map[string]string{"core/governance/opa-gate:1.2.0": "governance/opa-gate"}
	if err := record.HoldConfigurationToRecord(releaseFixture,
		attach("core/governance/opa-gate:1.2.0"), resolvesTo(table)); err != nil {
		t.Fatalf("a recorded gate that is still attached must not refuse; got %v", err)
	}
}

// TestHoldConfigurationToRecord_SaysNothingAboutAnAttachmentTheRecordDoesNotHold
// holds the benign direction of the asymmetry. A newly attached gate is a first
// sighting, filled by the resolution that acquires it; refusing here would make
// adding a gate impossible.
func TestHoldConfigurationToRecord_SaysNothingAboutAnAttachmentTheRecordDoesNotHold(t *testing.T) {
	record := recordOf("governance/opa-gate")
	table := map[string]string{
		"governance/opa-gate:1.2.0": "governance/opa-gate",
		"governance/new-gate:0.1.0": "governance/new-gate",
	}
	if err := record.HoldConfigurationToRecord(releaseFixture,
		attach("governance/opa-gate:1.2.0", "governance/new-gate:0.1.0"), resolvesTo(table)); err != nil {
		t.Fatalf("an attachment the record does not hold is a first sighting, not a fault; got %v", err)
	}
}

// TestHoldConfigurationToRecord_NamesEveryDroppedGateInRecordOrder: several
// dropped gates are reported together, in the record's own order, so one run
// tells the reader everything that went missing rather than one thing per run.
func TestHoldConfigurationToRecord_NamesEveryDroppedGateInRecordOrder(t *testing.T) {
	// canonicalise sorts by coordinate; assert against that order.
	record := canonicalise(recordOf("linter/house-style", "governance/opa-gate"))

	err := record.HoldConfigurationToRecord(releaseFixture, nil, resolvesTo(nil))
	if err == nil {
		t.Fatal("two dropped gates must refuse")
	}
	msg := err.Error()
	first := strings.Index(msg, "governance/opa-gate")
	second := strings.Index(msg, "linter/house-style")
	if first < 0 || second < 0 {
		t.Fatalf("both dropped gates must be named; got %v", err)
	}
	if first > second {
		t.Errorf("dropped gates must be named in record order (coordinate-sorted); got %v", err)
	}
}

// TestHoldConfigurationToRecord_StaysSilentWhenAnAttachmentCannotBeIdentified
// holds the suppression the godoc states, and it matters because the alternative
// is a refusal that names the wrong cause: an attachment whose plugin cannot be
// read might be exactly the one accounting for the record, so refusing would
// report a dropped gate on the strength of a failed read. The build does not
// proceed regardless — that attachment halts at resolution.
func TestHoldConfigurationToRecord_StaysSilentWhenAnAttachmentCannotBeIdentified(t *testing.T) {
	record := recordOf("governance/opa-gate")

	if err := record.HoldConfigurationToRecord(releaseFixture,
		attach("governance/unreadable:1.0.0"), resolvesTo(nil)); err != nil {
		t.Fatalf("an attachment whose plugin cannot be identified must suppress the refusal; got %v", err)
	}
}

// TestHoldConfigurationToRecord_NoRecordRefusesNothing covers the two shapes of
// "nothing has been recorded" — a nil receiver and an empty record — and both
// must be silent. An absent record is a first-run state, and a refusal there
// would make every workspace that has never resolved a verdict attachment
// unbuildable.
func TestHoldConfigurationToRecord_NoRecordRefusesNothing(t *testing.T) {
	var absent *Trust
	if err := absent.HoldConfigurationToRecord(releaseFixture, nil, resolvesTo(nil)); err != nil {
		t.Errorf("a nil record refuses nothing; got %v", err)
	}
	empty := &Trust{Version: CurrentVersion}
	if err := empty.HoldConfigurationToRecord(releaseFixture, nil, resolvesTo(nil)); err != nil {
		t.Errorf("an empty record refuses nothing; got %v", err)
	}
}

// TestLoadOrStart_AbsenceStartsEmptyAndDamageHalts pins the one distinction this
// loader owns. Absence is a first-run state; a file that exists and does not
// satisfy its schema is a fault. Collapsing the two would make emptying the file
// a way to switch every comparison against it off.
func TestLoadOrStart_AbsenceStartsEmptyAndDamageHalts(t *testing.T) {
	dir := t.TempDir()

	absent := filepath.Join(dir, FileName)
	trust, started, err := LoadOrStart(absent)
	if err != nil {
		t.Fatalf("an absent record is a first-run state, not a fault; got %v", err)
	}
	if !started {
		t.Error("an absent record must report that it was started rather than read")
	}
	if trust == nil || len(trust.Plugins) != 0 || trust.Version != CurrentVersion {
		t.Errorf("a started record must be empty at the current version; got %+v", trust)
	}

	damaged := filepath.Join(dir, "damaged-"+FileName)
	if err := os.WriteFile(damaged, []byte("version: \"not-a-number\"\n"), 0o644); err != nil {
		t.Fatalf("write damaged record: %v", err)
	}
	if _, _, err := LoadOrStart(damaged); err == nil {
		t.Error("a record that exists and does not satisfy its schema must halt, never read as absent")
	}
}
