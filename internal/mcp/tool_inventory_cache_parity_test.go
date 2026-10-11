// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package mcp

// What the cache inventory hands a caller, read against the shared inventory it
// was projected from.
//
// The expectation is a cacheops.Report this file supplies, not an answer read
// off the machine. Some of the cache classes are shared by every process on the
// machine, so a reading that had to enumerate them in order to know what to
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
	"github.com/aggrega-ai/aggrega-cli/internal/wasm/provindex"
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

	encoded, err := json.Marshal(cacheStatusPayload("/seeded/workspace", report, false))
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

			payload := cacheStatusPayload("/seeded/workspace", report, false)
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

// TestTheCacheInventoryKeepsBackOnlyWhatNamesAPluginLoadedFromAFile drives the
// projection over an inventory that records a plugin of every reference form, for
// a caller that launched this process and for one reaching a listener.
//
// The first is told everything as the inventory composed it. The second is told
// every identity that names a registry reference or a Plugin entity whole, and
// nothing that names a plugin by a path on this host: such an identity is null in
// its place with its entry marked, and a compiled module's note naming one is kept
// back whole and marked. Each row states one reference form, so a projection that
// judged the composed text instead of the record, or kept back too much, fails on
// the row that names it.
func TestTheCacheInventoryKeepsBackOnlyWhatNamesAPluginLoadedFromAFile(t *testing.T) {
	t.Parallel()

	const (
		fromFile     = "/seeded/plugins/importer.wasm@sha256:aa11"
		fromRegistry = "oci://hub.example.test/plugins/core/helm@1.0.0"
		byEntity     = "importer/langflow@1.2.0"
	)
	recordedFromFile := provindex.Identity{URI: "/seeded/plugins/importer.wasm", Version: "sha256:aa11"}
	recordedFromRegistry := provindex.Identity{URI: "oci://hub.example.test/plugins/core/helm", Version: "1.0.0"}
	recordedByEntity := provindex.Identity{URI: "importer/langflow", Version: "1.2.0"}

	elements := []struct {
		scenario string
		element  cacheops.Element
		// keptBack says the listener is told nothing of the note.
		keptBack bool
	}{
		{"a module built for a plugin loaded from a file",
			cacheops.Element{Label: "wazero-1.0/leaf-file", Size: 10, DigestHex: "aa11", Note: fromFile,
				Recorded: []provindex.Identity{recordedFromFile}}, true},
		{"a module built for a registry reference",
			cacheops.Element{Label: "wazero-1.0/leaf-registry", Size: 10, DigestHex: "bb22", Note: fromRegistry,
				Recorded: []provindex.Identity{recordedFromRegistry}}, false},
		{"a module built for bytes a file and a registry reference share",
			cacheops.Element{Label: "wazero-1.0/leaf-shared", Size: 10, DigestHex: "cc33", Note: fromFile + ", " + fromRegistry,
				Recorded: []provindex.Identity{recordedFromFile, recordedFromRegistry}}, true},
		{"the shared runtime kernel",
			cacheops.Element{Label: "wazero-1.0/kernel", Size: 10, Note: "shared runtime kernel, shared by 2"}, false},
		{"a module nothing is recorded for",
			cacheops.Element{Label: "wazero-1.0/orphan", Size: 10, Note: "unattributed"}, false},
	}
	provenance := []struct {
		scenario string
		entry    cacheops.ProvenanceEntry
		// keptBack says which identities the listener is told nothing of.
		keptBack []bool
	}{
		{"a plugin loaded from a file",
			cacheops.ProvenanceEntry{Identities: []string{fromFile}, Recorded: []provindex.Identity{recordedFromFile}},
			[]bool{true}},
		{"a registry reference",
			cacheops.ProvenanceEntry{Identities: []string{fromRegistry}, Recorded: []provindex.Identity{recordedFromRegistry}},
			[]bool{false}},
		{"a Plugin entity",
			cacheops.ProvenanceEntry{Identities: []string{byEntity}, Recorded: []provindex.Identity{recordedByEntity}},
			[]bool{false}},
		{"bytes a file and a registry reference share",
			cacheops.ProvenanceEntry{Identities: []string{fromFile, fromRegistry}, SharedBy: 2,
				Recorded: []provindex.Identity{recordedFromFile, recordedFromRegistry}},
			[]bool{true, false}},
		{"identities whose records did not arrive",
			cacheops.ProvenanceEntry{Identities: []string{fromRegistry}},
			[]bool{true}},
	}

	report := cacheops.Report{Classes: []cacheops.Class{{Name: cacheops.CompiledModuleClassName,
		Reach: cacheops.ReachMachine, Prunable: true}}}
	for _, row := range elements {
		report.Classes[0].Elements = append(report.Classes[0].Elements, row.element)
	}
	for _, row := range provenance {
		report.Provenance = append(report.Provenance, row.entry)
	}

	atTheKeyboard := cacheStatusPayload("/seeded/workspace", report, false)
	overAListener := cacheStatusPayload("/seeded/workspace", report, true)

	for i, row := range elements {
		keyboard, listener := atTheKeyboard.Classes[0].Elements[i], overAListener.Classes[0].Elements[i]
		if keyboard.Note != row.element.Note || keyboard.NoteWithheld {
			t.Errorf("%s: at the keyboard the note is %q (withheld=%v), want %q as composed", row.scenario,
				keyboard.Note, keyboard.NoteWithheld, row.element.Note)
		}
		switch {
		case row.keptBack && (listener.Note != "" || !listener.NoteWithheld):
			t.Errorf("%s: over a listener the note is %q (withheld=%v), want it kept back whole and marked",
				row.scenario, listener.Note, listener.NoteWithheld)
		case !row.keptBack && (listener.Note != row.element.Note || listener.NoteWithheld):
			t.Errorf("%s: over a listener the note is %q (withheld=%v), want %q whole", row.scenario,
				listener.Note, listener.NoteWithheld, row.element.Note)
		}
		if listener.Digest != row.element.DigestHex {
			t.Errorf("%s: over a listener the digest is %q, want %q: it names no place, so it is answered whole",
				row.scenario, listener.Digest, row.element.DigestHex)
		}
	}
	for i, row := range provenance {
		keyboard, listener := atTheKeyboard.Provenance[i], overAListener.Provenance[i]
		if keyboard.IdentitiesWithheld || len(keyboard.Identities) != len(row.entry.Identities) {
			t.Errorf("%s: at the keyboard the entry is %v (withheld=%v), want every identity as composed",
				row.scenario, keyboard.Identities, keyboard.IdentitiesWithheld)
			continue
		}
		if len(listener.Identities) != len(row.entry.Identities) {
			t.Errorf("%s: over a listener the entry names %d identities, want %d, each in its place", row.scenario,
				len(listener.Identities), len(row.entry.Identities))
			continue
		}
		anyKeptBack := false
		for j, name := range row.entry.Identities {
			if told(keyboard.Identities[j]) != name {
				t.Errorf("%s: at the keyboard identity %d is %q, want %q", row.scenario, j, told(keyboard.Identities[j]), name)
			}
			switch got := listener.Identities[j]; {
			case row.keptBack[j] && got != nil:
				t.Errorf("%s: over a listener identity %d is %q, want it kept back as null in its place", row.scenario,
					j, *got)
			case !row.keptBack[j] && told(got) != name:
				t.Errorf("%s: over a listener identity %d is %q, want %q whole", row.scenario, j, told(got), name)
			}
			anyKeptBack = anyKeptBack || row.keptBack[j]
		}
		if listener.IdentitiesWithheld != anyKeptBack {
			t.Errorf("%s: over a listener the entry is marked withheld=%v, want %v", row.scenario,
				listener.IdentitiesWithheld, anyKeptBack)
		}
		if listener.SharedBy != row.entry.SharedBy {
			t.Errorf("%s: over a listener the entry is shared by %d, want %d", row.scenario, listener.SharedBy,
				row.entry.SharedBy)
		}
	}

	// What a caller that launched this process reads carries no mark at all: the
	// rule for a caller reaching a listener leaves no trace in its bytes.
	encoded, err := json.Marshal(atTheKeyboard)
	if err != nil {
		t.Fatalf("marshal the keyboard's payload: %v", err)
	}
	if strings.Contains(string(encoded), "_withheld") {
		t.Errorf("the keyboard's payload carries a mark: %s", encoded)
	}

	keyboardText, listenerText := renderCacheStatusText(atTheKeyboard), renderCacheStatusText(overAListener)
	if !strings.Contains(keyboardText, fromFile) {
		t.Errorf("the keyboard's text does not name the plugin loaded from a file:\n%s", keyboardText)
	}
	if strings.Contains(listenerText, "/seeded/plugins") {
		t.Errorf("the listener's text names the plugin loaded from a file:\n%s", listenerText)
	}
	for _, want := range []string{keptBackLocationWords + ", " + fromRegistry, "leaf-registry  10 B  " + fromRegistry,
		"leaf-file  10 B  " + keptBackLocationWords} {
		if !strings.Contains(listenerText, want) {
			t.Errorf("the listener's text does not read %q:\n%s", want, listenerText)
		}
	}
}

// TestTheCacheInventoryKeepsBackOnlyTheLabelOfASourceReadFromThisHost drives the
// projection over an inventory holding a source-artifact slot of every URL form
// its source can be resolved from, for a caller that launched this process and
// for one reaching a listener.
//
// A slot's label is its location under the cache, composed from the host and path
// of the URL the slot records. The first caller is told every label. The second is
// told the label of a slot fetched over the network whole, and nothing of the
// label of a slot read from a repository on this host, whether its URL is a file
// URL or a path written with no scheme; nor of one whose recorded URL could not
// be read. Such a label is absent and marked, and the slot's size and digest are
// answered. Each row states one form, and every row's label spells the same
// kind of text, so a projection that judged the label instead of the recorded URL
// fails on the rows that tell them apart.
func TestTheCacheInventoryKeepsBackOnlyTheLabelOfASourceReadFromThisHost(t *testing.T) {
	t.Parallel()

	const commit = "0123456789abcdef0123456789abcdef01234567"
	recorded := func(url string) *string { return &url }
	rows := []struct {
		scenario string
		element  cacheops.Element
		// keptBack says the listener is told nothing of the label.
		keptBack bool
	}{
		{"a repository read through a file URL",
			cacheops.Element{Label: "git-repository/seeded/repos/chart.git/" + commit + "/aa11aa11aa11", Size: 10,
				DigestHex: "aa11", SourceURL: recorded("file:///seeded/repos/chart.git")}, true},
		{"a repository read from a path written with no scheme",
			cacheops.Element{Label: "git-repository/seeded/repos/plain.git/" + commit + "/bb22bb22bb22", Size: 11,
				DigestHex: "bb22", SourceURL: recorded("/seeded/repos/plain.git")}, true},
		{"a repository cloned over HTTPS",
			cacheops.Element{Label: "git-repository/git.example.test/team/chart.git/" + commit + "/cc33cc33cc33", Size: 12,
				DigestHex: "cc33", SourceURL: recorded("https://git.example.test/team/chart.git")}, false},
		{"a repository cloned over SSH, written with a scheme",
			cacheops.Element{Label: "git-repository/git.example.test/team/ssh.git/" + commit + "/dd44dd44dd44", Size: 13,
				DigestHex: "dd44", SourceURL: recorded("ssh://git@git.example.test/team/ssh.git")}, false},
		{"a repository cloned over SSH, written as host and path",
			cacheops.Element{Label: "git-repository/git.example.test:team/scp.git/" + commit + "/ee55ee55ee55", Size: 14,
				DigestHex: "ee55", SourceURL: recorded("git@git.example.test:team/scp.git")}, false},
		{"a chart pulled from a registry",
			cacheops.Element{Label: "helm-oci-chart/hub.example.test/charts/web/1.0.0/ff66ff66ff66", Size: 15,
				DigestHex: "ff66", SourceURL: recorded("oci://hub.example.test/charts/web")}, false},
		{"a slot whose recorded URL could not be read",
			cacheops.Element{Label: "git-repository/seeded/repos/unread.git/" + commit + "/aa77aa77aa77", Size: 16,
				SourceURL: recorded("")}, true},
		{"an element of a class that records no source",
			cacheops.Element{Label: "hub.example.test/component/core/api@1.0.0", Size: 17, DigestHex: "bb88"}, false},
	}

	report := cacheops.Report{Classes: []cacheops.Class{{Name: "workspace source-artifact cache",
		Reach: cacheops.ReachWorkspace, Prunable: true}}}
	for _, row := range rows {
		report.Classes[0].Elements = append(report.Classes[0].Elements, row.element)
	}

	atTheKeyboard := cacheStatusPayload("/seeded/workspace", report, false)
	overAListener := cacheStatusPayload("/seeded/workspace", report, true)

	for i, row := range rows {
		keyboard, listener := atTheKeyboard.Classes[0].Elements[i], overAListener.Classes[0].Elements[i]
		if told(keyboard.Label) != row.element.Label || keyboard.LabelWithheld {
			t.Errorf("%s: at the keyboard the label is %q (withheld=%v), want %q", row.scenario, told(keyboard.Label),
				keyboard.LabelWithheld, row.element.Label)
		}
		switch {
		case row.keptBack && (listener.Label != nil || !listener.LabelWithheld):
			t.Errorf("%s: over a listener the label is %q (withheld=%v), want it kept back and marked", row.scenario,
				told(listener.Label), listener.LabelWithheld)
		case !row.keptBack && (told(listener.Label) != row.element.Label || listener.LabelWithheld):
			t.Errorf("%s: over a listener the label is %q (withheld=%v), want %q whole", row.scenario,
				told(listener.Label), listener.LabelWithheld, row.element.Label)
		}
		if listener.Size != row.element.Size || listener.Digest != row.element.DigestHex {
			t.Errorf("%s: over a listener the size is %d and the digest %q, want %d and %q: neither names a place",
				row.scenario, listener.Size, listener.Digest, row.element.Size, row.element.DigestHex)
		}
	}

	// What a caller that launched this process reads carries no mark, and every
	// element's label is text in its place, exactly as before any rule applied.
	encoded, err := json.Marshal(atTheKeyboard)
	if err != nil {
		t.Fatalf("marshal the keyboard's payload: %v", err)
	}
	if strings.Contains(string(encoded), "_withheld") {
		t.Errorf("the keyboard's payload carries a mark: %s", encoded)
	}
	for _, complaint := range cacheInventoryComplaints(report, encoded) {
		t.Errorf("at the keyboard: %s", complaint)
	}

	keyboardText, listenerText := renderCacheStatusText(atTheKeyboard), renderCacheStatusText(overAListener)
	for _, row := range rows {
		if !strings.Contains(keyboardText, row.element.Label+"  ") {
			t.Errorf("the keyboard's text does not list %q:\n%s", row.element.Label, keyboardText)
		}
	}
	if strings.Contains(listenerText, "seeded/repos") {
		t.Errorf("the listener's text spells a repository on this host:\n%s", listenerText)
	}
	for _, want := range []string{keptBackLocationWords + "  10 B", keptBackLocationWords + "  11 B",
		keptBackLocationWords + "  16 B", "git-repository/git.example.test/team/chart.git/" + commit + "/cc33cc33cc33  12 B"} {
		if !strings.Contains(listenerText, want) {
			t.Errorf("the listener's text does not read %q:\n%s", want, listenerText)
		}
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
