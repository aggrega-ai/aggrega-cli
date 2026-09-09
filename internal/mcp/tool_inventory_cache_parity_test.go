// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package mcp

// What the cache inventory hands a caller, read against the shared inventory it
// was projected from.
//
// The expectation is a cacheops.Report this file supplies, not an answer read
// off the machine. Two of the six cache classes are shared by every process on
// the machine, so a reading that had to enumerate them in order to know what to
// expect would be comparing two answers taken at two moments; handed the report,
// the projection is checkable exactly, and every field it carries is covered
// rather than whichever ones the machine happened to hold.
//
// Assertions are made against the ENCODED payload rather than the struct, so a
// field whose tag is wrong fails here instead of passing a struct comparison and
// reaching a caller under a name it cannot find.

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/cacheops"
)

// cacheInventoryFixture is the shared inventory this surface's projection is
// held to.
//
// It spans what the projection has to carry: a workspace-reach class and two
// machine-reach ones, a prunable class and a non-prunable one, an element with a
// digest and a note and one with neither, and a provenance entry for a digest
// one identity resolved plus one several share. The sizes straddle the
// formatter's rounding decision rather than sitting on tidy multiples, so a
// second implementation of the rounding rule would disagree with at least one.
func cacheInventoryFixture() cacheops.Report {
	classes := []cacheops.Class{
		{
			Name: "workspace entity cache", Root: "/seeded/workspace/entities",
			Reach: cacheops.ReachWorkspace, Prunable: true,
			Elements: []cacheops.Element{
				{Label: "hub.example.test/component/core/api@1.0.0", Path: "/seeded/a", Size: 1536, DigestHex: "aa11"},
				{Label: "hub.example.test/component/core/web@2.0.0", Path: "/seeded/b", Size: 512},
			},
		},
		{
			Name: cacheops.CompiledModuleClassName, Root: "/seeded/machine/compiled",
			Reach: cacheops.ReachMachine, Prunable: true,
			Elements: []cacheops.Element{
				{Label: "wazero-1.0/leaf0001", Path: "/seeded/c", Size: 3<<20 + 1, Note: "unattributed"},
			},
		},
		{
			Name: "plugin-describe cache", Root: "/seeded/machine/describe",
			Reach: cacheops.ReachMachine, Prunable: false,
			Elements: []cacheops.Element{
				{Label: "sha256-abcdef.json", Path: "/seeded/d", Size: 1023, DigestHex: "abcdef"},
			},
		},
	}
	report := cacheops.Report{
		Classes: classes,
		Provenance: []cacheops.ProvenanceEntry{
			{Identities: []string{"oci://hub.example.test/plugins/core/helm@1.0.0"}},
			{Identities: []string{
				"oci://hub.example.test/plugins/core/kustomize@3.1.0",
				"oci://hub.example.test/plugins/team/kustomize-fork@3.1.0",
			}, SharedBy: 2},
		},
	}
	for _, cl := range classes {
		for _, el := range cl.Elements {
			report.TotalCount++
			report.TotalSize += el.Size
		}
	}
	return report
}

// cacheInventoryComplaints reads an encoded cache-status payload against the
// shared inventory it should have been projected from, and returns one complaint
// per fact the payload does not carry.
//
// It returns complaints rather than failing, so the same reading can be run over
// a deliberately damaged payload. An assertion nobody has watched reject
// anything says nothing about what it would catch.
func cacheInventoryComplaints(report cacheops.Report, encoded []byte) []string {
	var complaints []string
	say := func(format string, args ...any) {
		complaints = append(complaints, fmt.Sprintf(format, args...))
	}

	var wire map[string]any
	if err := json.Unmarshal(encoded, &wire); err != nil {
		return []string{fmt.Sprintf("the payload does not decode: %v (bytes: %s)", err, encoded)}
	}

	number := func(key string) (float64, bool) {
		raw, present := wire[key]
		if !present {
			say("the payload carries no %q key at all", key)
			return 0, false
		}
		n, isNumber := raw.(float64)
		if !isNumber {
			say("%q is not a number: %#v", key, raw)
			return 0, false
		}
		return n, true
	}

	if n, ok := number("total_count"); ok && int(n) != report.TotalCount {
		say("total_count is %d, and the shared inventory counted %d", int(n), report.TotalCount)
	}
	if n, ok := number("total_size"); ok && int64(n) != report.TotalSize {
		say("total_size is %d, and the shared inventory totalled %d", int64(n), report.TotalSize)
	}
	// The rendered total is held to the shared formatter's own output, never to a
	// spelling written here: a copy that rounded differently on one input is the
	// defect the rendered field exists to prevent, and a comparison against a
	// literal would follow such a copy instead of catching it.
	if text, ok := wire["total_size_text"].(string); !ok {
		say("total_size crosses the wire as a bare number: no total_size_text beside it")
	} else if want := cacheops.HumanSize(report.TotalSize); text != want {
		say("total_size_text is %q, and the shared formatter writes %q", text, want)
	}

	wantMachineWide := cacheops.MachineWideClasses(report.Classes)
	gotMachineWide, present := wire["machine_wide_classes"].([]any)
	switch {
	case !present:
		say("the payload names no machine_wide_classes, which the shared inventory computes from the classes")
	case len(gotMachineWide) != len(wantMachineWide):
		say("machine_wide_classes holds %d name(s), and the shared inventory computes %d: %v",
			len(gotMachineWide), len(wantMachineWide), wantMachineWide)
	default:
		for i, want := range wantMachineWide {
			if got, _ := gotMachineWide[i].(string); got != want {
				say("machine_wide_classes[%d] is %q, want %q", i, got, want)
			}
		}
	}

	gotProvenance, present := wire["provenance"].([]any)
	switch {
	case !present:
		say("the payload carries no provenance, which the shared inventory records and hands over")
	case len(gotProvenance) != len(report.Provenance):
		say("provenance holds %d entr(y/ies), and the shared inventory recorded %d",
			len(gotProvenance), len(report.Provenance))
	default:
		for i, want := range report.Provenance {
			entry, _ := gotProvenance[i].(map[string]any)
			identities, _ := entry["identities"].([]any)
			if len(identities) != len(want.Identities) {
				say("provenance[%d] names %d identit(y/ies), want %d", i, len(identities), len(want.Identities))
				continue
			}
			for j, wantIdentity := range want.Identities {
				if got, _ := identities[j].(string); got != wantIdentity {
					say("provenance[%d].identities[%d] is %q, want %q", i, j, got, wantIdentity)
				}
			}
			shared, carried := entry["shared_by"]
			if want.SharedBy == 0 && carried {
				say("provenance[%d] carries shared_by for a digest no second identity resolves to", i)
			}
			if want.SharedBy != 0 {
				if n, isNumber := shared.(float64); !carried || !isNumber || int(n) != want.SharedBy {
					say("provenance[%d].shared_by is %#v, want %d", i, shared, want.SharedBy)
				}
			}
		}
	}

	gotClasses, present := wire["classes"].([]any)
	if !present || len(gotClasses) != len(report.Classes) {
		say("the payload carries %d class(es), and the shared inventory enumerated %d",
			len(gotClasses), len(report.Classes))
		return complaints
	}
	for i, wantClass := range report.Classes {
		class, _ := gotClasses[i].(map[string]any)
		if got, _ := class["name"].(string); got != wantClass.Name {
			say("classes[%d].name is %q, want %q", i, got, wantClass.Name)
		}
		if got, _ := class["reach"].(string); got != string(wantClass.Reach) {
			say("classes[%d] (%s) reports reach %q, and the shared inventory declares %q",
				i, wantClass.Name, got, wantClass.Reach)
		}
		if got, _ := class["prunable"].(bool); got != wantClass.Prunable {
			say("classes[%d] (%s) reports prunable %v, want %v", i, wantClass.Name, got, wantClass.Prunable)
		}
		elements, _ := class["elements"].([]any)
		if len(elements) != len(wantClass.Elements) {
			say("classes[%d] (%s) carries %d element(s), want %d",
				i, wantClass.Name, len(elements), len(wantClass.Elements))
			continue
		}
		for j, wantElement := range wantClass.Elements {
			element, _ := elements[j].(map[string]any)
			if got, _ := element["label"].(string); got != wantElement.Label {
				say("classes[%d].elements[%d].label is %q, want %q", i, j, got, wantElement.Label)
			}
			if got, _ := element["size"].(float64); int64(got) != wantElement.Size {
				say("classes[%d].elements[%d].size is %d, want %d", i, j, int64(got), wantElement.Size)
			}
			if text, ok := element["size_text"].(string); !ok {
				say("classes[%d].elements[%d] (%s) crosses the wire as a bare number: no size_text beside it",
					i, j, wantElement.Label)
			} else if want := cacheops.HumanSize(wantElement.Size); text != want {
				say("classes[%d].elements[%d].size_text is %q, and the shared formatter writes %q", i, j, text, want)
			}
			if got, _ := element["digest"].(string); got != wantElement.DigestHex {
				say("classes[%d].elements[%d].digest is %q, want %q", i, j, got, wantElement.DigestHex)
			}
			if got, _ := element["note"].(string); got != wantElement.Note {
				say("classes[%d].elements[%d].note is %q, want %q", i, j, got, wantElement.Note)
			}
		}
	}
	return complaints
}

// TestTheCacheInventoryCarriesEveryFactTheSharedInventoryComputed is the parity
// proof for this surface: the projection is driven over a supplied inventory and
// every fact that inventory holds is required on the wire.
func TestTheCacheInventoryCarriesEveryFactTheSharedInventoryComputed(t *testing.T) {
	t.Parallel()

	report := cacheInventoryFixture()
	if report.TotalCount == 0 || len(report.Provenance) == 0 || len(cacheops.MachineWideClasses(report.Classes)) == 0 {
		t.Fatalf("the fixture inventory is missing a half this reading covers, so it would hold vacuously: %+v", report)
	}

	encoded, err := json.Marshal(cacheStatusPayload("/seeded/workspace", report))
	if err != nil {
		t.Fatalf("marshal the projected inventory: %v", err)
	}
	t.Logf("cache-status payload:\n%s", encoded)

	for _, complaint := range cacheInventoryComplaints(report, encoded) {
		t.Errorf("%s", complaint)
	}
}

// TestTheCacheInventoryReadingRejectsADroppedFact is the negative control, and
// it is what makes the reading above worth running.
//
// Each row removes one fact from an otherwise-correct payload and requires the
// reading to name that fact. A reading that stayed silent here would be silent
// over the surface this change repaired.
func TestTheCacheInventoryReadingRejectsADroppedFact(t *testing.T) {
	t.Parallel()

	report := cacheInventoryFixture()
	for _, tc := range []struct {
		name string
		// drop damages an otherwise-correct payload.
		drop func(p *cacheStatusResult)
		// names is what the complaint must mention.
		names string
	}{
		{
			name:  "an element's rendered size",
			drop:  func(p *cacheStatusResult) { p.Classes[0].Elements[0].SizeText = "" },
			names: "size_text",
		},
		{
			name:  "the rendered total",
			drop:  func(p *cacheStatusResult) { p.TotalSizeText = "" },
			names: "total_size_text",
		},
		{
			name:  "the recorded plugin provenance",
			drop:  func(p *cacheStatusResult) { p.Provenance = nil },
			names: "provenance",
		},
		{
			name:  "a class's reach",
			drop:  func(p *cacheStatusResult) { p.Classes[1].Reach = "" },
			names: "reach",
		},
		{
			name:  "the machine-wide class list",
			drop:  func(p *cacheStatusResult) { p.MachineWideClasses = nil },
			names: "machine_wide_classes",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			payload := cacheStatusPayload("/seeded/workspace", report)
			undamaged, err := json.Marshal(payload)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			if got := cacheInventoryComplaints(report, undamaged); len(got) != 0 {
				t.Fatalf("the undamaged payload already draws complaints, so nothing below is attributable: %v", got)
			}

			tc.drop(&payload)
			damaged, err := json.Marshal(payload)
			if err != nil {
				t.Fatalf("marshal the damaged payload: %v", err)
			}
			complaints := cacheInventoryComplaints(report, damaged)
			if len(complaints) == 0 {
				t.Fatalf("dropping %s drew no complaint, so this reading would not notice it: %s", tc.name, damaged)
			}
			var named bool
			for _, complaint := range complaints {
				if strings.Contains(complaint, tc.names) {
					named = true
					break
				}
			}
			if !named {
				t.Errorf("dropping %s complained without naming %q: %v", tc.name, tc.names, complaints)
			}
			t.Logf("dropping %s produced: %v", tc.name, complaints)
		})
	}
}

// TestTheWiredCacheInventoryReportsItsRenderedSizes drives the real tool rather
// than the projection alone, so the repair is proved on the path a caller
// actually reaches rather than only on the function under it.
//
// It asserts presence and internal consistency rather than content. The machine
// -wide classes are shared by every process on the machine, so what they hold
// while this runs is not a fixed answer — and the content half is covered
// exactly by the reading above. The operation only reads, so nothing here
// removes anything from any cache.
func TestTheWiredCacheInventoryReportsItsRenderedSizes(t *testing.T) {
	t.Parallel()

	res, rpcErr := cacheStatusInvoke(newTestServer(t), context.Background(), nil)
	if rpcErr != nil {
		t.Fatalf("the cache inventory refused: %+v", rpcErr)
	}
	if len(res.StructuredContent) == 0 {
		t.Fatal("the cache inventory answered with no structured payload at all")
	}
	// The payload itself is deliberately not logged. It enumerates the
	// machine-wide caches, which on a machine that has built anything runs to
	// megabytes, and a log that large is noise every later reader pays for. What
	// a failure needs is the offending object, which each assertion below prints
	// on its own.

	var wire map[string]any
	if err := json.Unmarshal(res.StructuredContent, &wire); err != nil {
		t.Fatalf("the wired payload does not decode: %v", err)
	}
	for _, key := range []string{"classes", "provenance", "total_count", "total_size", "total_size_text", "machine_wide_classes"} {
		if _, present := wire[key]; !present {
			t.Errorf("the wired payload carries no %q key", key)
		}
	}
	total, _ := wire["total_size"].(float64)
	if text, _ := wire["total_size_text"].(string); text != cacheops.HumanSize(int64(total)) {
		t.Errorf("the wired total is %d spelled %q; the shared formatter writes %q",
			int64(total), text, cacheops.HumanSize(int64(total)))
	}
	classes, _ := wire["classes"].([]any)
	for _, rawClass := range classes {
		class, _ := rawClass.(map[string]any)
		if _, present := class["reach"]; !present {
			t.Errorf("a wired class carries no reach: %+v", class)
		}
		elements, _ := class["elements"].([]any)
		for _, rawElement := range elements {
			element, _ := rawElement.(map[string]any)
			size, _ := element["size"].(float64)
			text, ok := element["size_text"].(string)
			if !ok {
				t.Errorf("a wired element crosses as a bare number, with no size_text beside it: %+v", element)
				continue
			}
			if want := cacheops.HumanSize(int64(size)); text != want {
				t.Errorf("a wired element of %d bytes is spelled %q; the shared formatter writes %q",
					int64(size), text, want)
			}
		}
	}
}
