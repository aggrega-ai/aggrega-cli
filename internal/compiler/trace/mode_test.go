// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package trace

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/redact"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// modeSlots are the three distinct path shapes eventPath produces, one per
// branch: the default .yaml extension, the .log extension two passes carry, and
// the per-file layout a pass with a fileSuffix uses. The header assertions run
// over all three rather than over one, because the stamp is applied at
// serialization and a per-branch regression in the path layout would otherwise
// leave two of the three unproven.
var modeSlots = []struct {
	name       string
	pass       Pass
	fileSuffix string
	wantRel    string
}{
	{"default yaml slot", PassPhaseB0Locals, "", "boundaries/cluster/app/06-source-locals.yaml"},
	{"log slot", PassEmission, "", "boundaries/cluster/app/11-engine-emission.log"},
	{"per-file slot", PassPhaseB4, "app.compose-fragment.yaml", "boundaries/cluster/app/10-engine-synthesis/app.compose-fragment.yaml"},
}

func modePayload() map[string]any {
	return map[string]any{"leaf": "plaintext-value"}
}

// TestModeKeysWithholdsByDefaultAndEmptiesWhenUnredacted pins the withholding
// half of Mode. Each assertion is paired: the default must pass the caller's set
// through UNCHANGED (a mode that quietly narrowed it would redact less than the
// caller asked) and the unredacted mode must return a set that names nothing.
func TestModeKeysWithholdsByDefaultAndEmptiesWhenUnredacted(t *testing.T) {
	t.Parallel()

	// Assembled through the one assembler rather than as a struct literal: the
	// delivery marking is unreachable from outside its own package, so a set
	// carrying one can only come from there.
	in := redact.NodeKeys(
		map[string]bool{"dbPassword": true},
		map[string]bool{"apiKey": true},
		types.OrderedMap[types.IRFeatureEntry]{},
		redact.CrossNode{},
		redact.DeliveryKeys{"env": {"DATABASE_PASSWORD": true}},
		nil,
		redact.Derived{
			Locals: map[string]bool{"dsn": true},
			Source: map[string]bool{redact.WholeSourceKey: true},
		})

	kept := Mode{}.Keys(in)
	if kept.Empty() {
		t.Fatalf("the withholding default must pass the caller's set through; got an empty set")
	}
	for name, got := range map[string]bool{
		"values leaf":  kept.Leaf("values", "dbPassword"),
		"infra leaf":   kept.Leaf("infra", "apiKey"),
		"delivery key": kept.Delivery("env", "DATABASE_PASSWORD"),
		"local":        kept.Locals["dsn"],
		"source":       kept.Source[redact.WholeSourceKey],
	} {
		if !got {
			t.Errorf("the withholding default dropped the %s from the caller's set", name)
		}
	}

	emptied := Mode{Unredacted: true}.Keys(in)
	if !emptied.Empty() {
		t.Errorf("an unredacted capture must withhold nothing; got %+v", emptied)
	}
	// The caller's set is not mutated: the same value must still answer for a
	// second capture, which is what lets one assembled set serve every pass.
	if !in.Leaf("values", "dbPassword") {
		t.Errorf("Keys must not mutate the set it is given")
	}
}

// TestUnredactedCaptureStampsEveryWrittenFile is the announcement half. A file
// found later in a bug report or a shared directory has only its own contents to
// say what it is, so the header must be on every file an unredacted capture
// writes — not on a representative one.
//
// Both writers are asserted, and their bodies are compared to each other. That
// comparison is the evidence for the single serialization chokepoint: two
// independent marshal sites would be free to disagree, and here they cannot.
func TestUnredactedCaptureStampsEveryWrittenFile(t *testing.T) {
	t.Parallel()

	for _, slot := range modeSlots {
		t.Run(slot.name, func(t *testing.T) {
			t.Parallel()

			for _, mode := range []struct {
				label      string
				mode       Mode
				wantHeader bool
			}{
				{"withholding default", Mode{}, false},
				{"unredacted", Mode{Unredacted: true}, true},
			} {
				root := t.TempDir()
				fs := New(root, mode.mode)
				if err := fs.Write(slot.pass, "app", "cluster", slot.fileSuffix, modePayload()); err != nil {
					t.Fatalf("%s: filesystem write: %v", mode.label, err)
				}
				if err := fs.Close(); err != nil {
					t.Fatalf("%s: filesystem close: %v", mode.label, err)
				}
				onDisk, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(slot.wantRel)))
				if err != nil {
					t.Fatalf("%s: read %s: %v", mode.label, slot.wantRel, err)
				}

				mem := NewMemory(mode.mode)
				if err := mem.Write(slot.pass, "app", "cluster", slot.fileSuffix, modePayload()); err != nil {
					t.Fatalf("%s: memory write: %v", mode.label, err)
				}
				collected, ok := mem.Entries()[slot.wantRel]
				if !ok {
					t.Fatalf("%s: memory writer produced no entry at %s; got %v", mode.label, slot.wantRel, mem.Entries())
				}

				if string(onDisk) != string(collected) {
					t.Errorf("%s: the two writers must serialize one payload identically\n--- on disk ---\n%s\n--- collected ---\n%s", mode.label, onDisk, collected)
				}

				gotHeader := strings.HasPrefix(string(onDisk), "# WARNING:")
				if gotHeader != mode.wantHeader {
					t.Errorf("%s: header present = %v, want %v\n--- file ---\n%s", mode.label, gotHeader, mode.wantHeader, onDisk)
				}
				if mode.wantHeader && !strings.Contains(string(onDisk), "--trace-unredacted") {
					t.Errorf("%s: the header must name the flag that produced the file\n--- file ---\n%s", mode.label, onDisk)
				}
				// The payload survives the stamp in both modes: a header that
				// displaced the body would make the file useless for the debugging
				// it exists to serve.
				if !strings.Contains(string(onDisk), "leaf: plaintext-value") {
					t.Errorf("%s: the payload must follow the header verbatim\n--- file ---\n%s", mode.label, onDisk)
				}
			}
		})
	}
}

// TestWithholdingCaptureIsByteIdenticalToPreAxisOutput is the control for the
// whole axis at this layer: given a set that names nothing, the projection a
// capture serializes must be the one a build predating sensitivity produced. The
// assertion is on the serialized bytes rather than on the applier return values,
// because the bytes are what a reader diffs.
func TestWithholdingCaptureIsByteIdenticalToPreAxisOutput(t *testing.T) {
	t.Parallel()

	values := types.OrderedMap[any]{}
	values.Set("appName", "demo")
	values.Set("dbPassword", "hunter2-plaintext")
	payload := map[string]any{"values": values}

	withNoKeys, err := marshalEvent(Mode{}, "cluster", "app", payload)
	if err != nil {
		t.Fatalf("marshal with no keys: %v", err)
	}
	unredacted, err := marshalEvent(Mode{Unredacted: true}, "cluster", "app", payload)
	if err != nil {
		t.Fatalf("marshal unredacted: %v", err)
	}

	if got := string(unredacted); got != unredactedHeader+string(withNoKeys) {
		t.Errorf("an unredacted capture must be the header followed by the unchanged body\n--- got ---\n%s", got)
	}
	if !strings.Contains(string(withNoKeys), "hunter2-plaintext") {
		t.Errorf("a capture with an empty key set substitutes nothing\n--- got ---\n%s", withNoKeys)
	}
}
