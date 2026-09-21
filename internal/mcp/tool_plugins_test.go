// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package mcp

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/opsurface"
	"github.com/aggrega-ai/aggrega-cli/internal/pluginops"
	"github.com/aggrega-ai/aggrega-cli/internal/wasm"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// What the plugin reading tells a caller, read on both transports over one
// enumeration.
//
// The enumeration is supplied rather than performed, and that is what makes the
// comparison exact. The projection is where the whole of the keeping-back rule
// lives, so driving it over a known listing says what each field does with a
// value nobody had to go and configure; whether the enumeration itself is the
// one the command line performs is a different claim, proved where that
// enumeration lives because this package cannot reach it.
//
// The two answers are compared rather than asserted apart. A rule that keeps
// something back is only testable against what would have been there without
// it: an assertion that read the listener's answer alone would pass against a
// reading that kept back everything, and one that read the terminal's alone
// would pass against a reading that kept back nothing.

// The references the fixture listing binds, one per structural form the
// classifier partitions references into, plus the two forms a cascade produces
// rather than an author.
//
// Each is distinctive enough that finding it anywhere in a listener's answer
// cannot be a coincidence, and each points at nothing: no case here loads a
// guest.
const (
	theEmbeddedReference = "provider/helm"
	thePublishedSocket   = "oci://example.com/plugins/lint:1.0.0"
	theAbsolutePlugin    = "/held/plugin-reading-9f31/helm.wasm"
	theRelativePlugin    = "./held-plugin-reading-9f31/compose.wasm"
	theAnchoredPlugin    = "/held/plugin-reading-9f31/ws/linter/publishing"
	theInstalledCommand  = "oci://example.com/plugins/cli/greet:1.0.0"
	thePublishedGovernor = "oci://example.com/plugins/redact:2.0.0"
)

// theExpectedWithholding says, for each reference the fixture below binds,
// whether this reading must keep it back over a listener.
//
// 🔴 DELIBERATE DUPLICATE, and it is the whole value of the comparison it feeds.
// It is written out rather than computed from the classifier under test, because
// a cross-check that shares its subject's implementation moves with it: measured,
// a plant that misclassified every reference left a walk whose expectation called
// that same classifier entirely silent, and only a raw search of the answer's
// bytes caught the leak. Unifying the two would restore exactly that blindness.
//
// A reference absent from this table fails the comparison rather than defaulting,
// so a fixture entry added later cannot pass unjudged.
var theExpectedWithholding = map[string]bool{
	theEmbeddedReference:        false, // a built-in coordinate, the same on every host
	thePublishedSocket:          false, // a published artifact reference
	theAbsolutePlugin:           true,  // an absolute path on this host
	theRelativePlugin:           true,  // anchored at the serving process's own directory
	theAnchoredPlugin:           true,  // an absolute path the cascade produced from a bare name
	theInstalledCommand:         false, // a published, version-pinned reference
	thePublishedGovernor:        false,
	thePublishedGovernor + "-b": false,
	thePublishedGovernor + "-c": false,
}

// aListingBindingALocalPath is the fixture enumeration: the built-ins this
// binary would report, and a configured group carrying one reference of every
// form the classifier tells apart.
//
// The anchored entry is the one that would be missed by reasoning about what an
// author writes. Its reference is an absolute path that nobody typed — the
// resolution cascade anchors a bare socket binding against the directory of the
// file that declared it, so a workspace whose configuration names no path at all
// still reaches this answer carrying one. A case that classified the authored
// value rather than the resolved one would publish it.
func aListingBindingALocalPath() pluginops.PluginListing {
	return pluginops.PluginListing{
		Embedded: []pluginops.ListedPlugin{
			{
				Source:         "embedded",
				URI:            theEmbeddedReference,
				ResolvedDigest: "sha256:embedded",
				Descriptor: wasm.PluginDescriptor{
					PluginType:   string(wasm.PluginTypeProvider),
					Capabilities: []string{"helm"},
					Exports:      []wasm.PluginExport{{Name: "render_node"}},
					Contract: &wasm.ProviderContract{
						InputKinds:       wasm.InputKinds{Sourced: true},
						BoundaryAssembly: "none",
					},
					Metadata: &wasm.PluginDescriptorMeta{Version: "0.4.0", Commit: "abc1234"},
				},
			},
		},
		Configured: []pluginops.ListedPlugin{
			{
				Source:         "configured",
				URI:            thePublishedSocket,
				Origin:         "plugins.linter[0]",
				ResolvedDigest: "sha256:published",
				Descriptor: wasm.PluginDescriptor{
					PluginType: string(wasm.PluginTypeLinter),
					Exports:    []wasm.PluginExport{{Name: "lint_rules"}},
					Metadata:   &wasm.PluginDescriptorMeta{Version: "1.0.0"},
				},
			},
			{
				Source: "configured",
				URI:    theAbsolutePlugin,
				Origin: "plugins.providers[0]",
				Err:    wasm.ErrWasmManifestRejected(),
			},
			{
				Source: "configured",
				URI:    theRelativePlugin,
				Origin: "plugins.providers[1]",
				Err:    wasm.ErrWasmManifestRejected(),
			},
			{
				// Resolved, then failed to describe: the one entry proving a
				// failure's text is kept back without the plugin reading as
				// unresolved.
				Source:         "configured",
				URI:            theAnchoredPlugin,
				Origin:         "plugins.linter[1]",
				ResolvedDigest: "sha256:anchored",
				Err:            wasm.ErrWasmManifestRejected(),
			},
			{
				Source:         "installed",
				URI:            theInstalledCommand,
				Origin:         "pinned abc1234",
				Readiness:      "ready",
				ResolvedDigest: "sha256:installed",
				Descriptor: wasm.PluginDescriptor{
					PluginType: string(wasm.PluginTypeCLI),
					Metadata:   &wasm.PluginDescriptorMeta{Version: "1.0.0"},
				},
			},
		},
	}
}

// aListingNamingNoLocalPath is the same shape with every reference published,
// so the two listener answers over the two listings differ only where the
// keeping-back rule fired. It is what turns clause four from a claim about one
// answer into a comparison.
func aListingNamingNoLocalPath() pluginops.PluginListing {
	listing := aListingBindingALocalPath()
	listing.Configured[1].URI = thePublishedGovernor
	listing.Configured[2].URI = thePublishedGovernor + "-b"
	listing.Configured[3].URI = thePublishedGovernor + "-c"
	return listing
}

// staticLister hands one listing to the reading however it is called.
func staticLister(listing pluginops.PluginListing) pluginops.PluginLister {
	return func(context.Context, *workspaceconfig.ResolvedWorkspace) pluginops.PluginListing {
		return listing
	}
}

// anEnumerationFindingNoPlugins is the seam a session carries when nothing about
// the case under test concerns which plugins are bound: present, and reporting
// none.
//
// It exists for the fixtures that drive every tool at once rather than one
// subject, where what has to be right is that the seam IS there — the shape the
// command layer composes — and not what it enumerates. A session left without
// one is a shape the product does not ship, and the reading answers it with the
// refusal that says so, which such a fixture would then measure as though it
// were the tool's ordinary behaviour.
func anEnumerationFindingNoPlugins() pluginops.PluginLister {
	return staticLister(pluginops.PluginListing{})
}

// pluginListenerOver starts one real listener over a workspace root with the
// enumeration seam installed, which is what a session composed by the command
// layer carries.
//
// The seam is installed on the server the handler already holds, which is the
// wiring the fixture's own documentation describes: the handler reads such a
// field per request, so installing one after construction is a session with that
// seam wired rather than a shape the product does not have.
func pluginListenerOver(t *testing.T, root string, listing pluginops.PluginListing) *scopeGridFixture {
	t.Helper()
	f := listenerOverWorkspace(t, root)
	f.srv.listPlugins = staticLister(listing)
	return f
}

// pluginsOverTheListener calls the plugin reading over a real listener with a
// credential carrying the read scope, and returns the structured answer.
func pluginsOverTheListener(t *testing.T, f *scopeGridFixture, args map[string]any) pluginListResult {
	t.Helper()
	got := f.callTool(t, f.grantOf(t, scopeRead), "plugin-list", args)
	if got.status != http.StatusOK {
		t.Fatalf("the plugin reading was answered %d over the listener (body %q)", got.status, got.raw)
	}
	if got.frame.Error != nil {
		t.Fatalf("the plugin reading was answered with an error envelope: %+v", got.frame.Error)
	}
	var result struct {
		Content           []toolTextContent `json:"content"`
		StructuredContent json.RawMessage   `json:"structuredContent"`
		IsError           bool              `json:"isError"`
	}
	if err := json.Unmarshal(got.frame.Result, &result); err != nil {
		t.Fatalf("the answer is not a tool result: %v (body %q)", err, got.raw)
	}
	if result.IsError {
		t.Fatalf("the plugin reading refused over the listener: %q", result.Content)
	}
	if len(result.StructuredContent) == 0 {
		t.Fatalf("the plugin reading answered over the listener with no structured payload (body %q)", got.raw)
	}
	var payload pluginListResult
	if err := json.Unmarshal(result.StructuredContent, &payload); err != nil {
		t.Fatalf("the structured payload does not decode: %v (bytes %s)", err, result.StructuredContent)
	}
	return payload
}

// pluginsOverTheProcessStreams calls the plugin reading the way a client that
// launched this process reaches it, over the same root and the same
// enumeration.
func pluginsOverTheProcessStreams(t *testing.T, root string, listing pluginops.PluginListing,
	args map[string]any) pluginListResult {

	t.Helper()
	s := newTestServer(t)
	s.startDir = root
	s.transport = transportStdio
	s.listPlugins = staticLister(listing)

	raw, err := json.Marshal(args)
	if err != nil {
		t.Fatalf("marshal the arguments: %v", err)
	}
	res, rpcErr := pluginListInvoke(s, context.Background(), raw)
	if rpcErr != nil {
		t.Fatalf("the plugin reading returned an error envelope: %+v", rpcErr)
	}
	if res.IsError {
		t.Fatalf("the plugin reading refused at the keyboard: %q", res.Content)
	}
	if len(res.StructuredContent) == 0 {
		t.Fatal("the plugin reading answered with no structured payload")
	}
	var payload pluginListResult
	if err := json.Unmarshal(res.StructuredContent, &payload); err != nil {
		t.Fatalf("the structured payload does not decode: %v", err)
	}
	return payload
}

// everyRecord is the argument set that asks for the records themselves rather
// than the summary, written once because every comparison below needs them.
func everyRecord() map[string]any {
	return map[string]any{"response_format": "detailed", "limit": 500}
}

// TestThePluginReadingAnswersAListenersReadCredential is clause one for this
// half: a token carrying the read scope is offered the tool and receives the
// structured answer, asserted over a real listener rather than read out of the
// registry.
func TestThePluginReadingAnswersAListenersReadCredential(t *testing.T) {
	t.Parallel()
	f := pluginListenerOver(t, t.TempDir(), aListingBindingALocalPath())

	credential := f.grantOf(t, scopeRead)
	if offered := f.listed(t, credential); !slices.Contains(offered, "plugin-list") {
		t.Fatalf("a credential carrying %s was not offered plugin-list; it was offered %v", scopeRead, offered)
	}

	got := pluginsOverTheListener(t, f, everyRecord())
	if got.Count != 6 {
		t.Errorf("the reading reports %d plugins, want the six the enumeration carries", got.Count)
	}
	if got.Returned != 6 {
		t.Errorf("the answer carries %d records, want all six", got.Returned)
	}
	wantGroups := []pluginGroupCount{{Group: "embedded", Count: 1}, {Group: "configured", Count: 5}}
	if !slices.Equal(got.Groups, wantGroups) {
		t.Errorf("the group breakdown is %+v, want %+v", got.Groups, wantGroups)
	}

	// The declared metadata, and whether each resolved, which is what clause one
	// asks the reading to carry.
	embedded := recordFor(t, got, theEmbeddedReference)
	if embedded.Type != "provider" || embedded.Version != "0.4.0" || embedded.Commit != "abc1234" {
		t.Errorf("the built-in's declared metadata did not survive: %+v", embedded)
	}
	if !slices.Equal(embedded.Capabilities, []string{"helm"}) ||
		!slices.Equal(embedded.Exports, []string{"render_node"}) ||
		!slices.Equal(embedded.InputKinds, []string{"sourced"}) || embedded.Boundary != "none" {
		t.Errorf("the built-in's contract did not survive: %+v", embedded)
	}
	if !embedded.Resolved {
		t.Error("the built-in is reported as not resolving")
	}
	installed := recordFor(t, got, theInstalledCommand)
	if installed.Readiness != "ready" || installed.Type != "cli" || !installed.Resolved {
		t.Errorf("the installed command's readiness and type did not survive: %+v", installed)
	}

	// A plugin that fails is reported rather than dropped, and the two failure
	// shapes stay apart.
	unresolved := withheldRecord(t, got, "plugins.providers[0]")
	if unresolved.Resolved {
		t.Error("a binding whose reference did not resolve is reported as having resolved")
	}
	describeFailed := withheldRecord(t, got, "plugins.linter[1]")
	if !describeFailed.Resolved {
		t.Error("a binding that resolved and then failed to describe is reported as not having resolved")
	}
}

// recordFor returns the one record carrying the named reference.
func recordFor(t *testing.T, p pluginListResult, uri string) pluginEntryResult {
	t.Helper()
	for _, e := range p.Plugins {
		if e.URI != nil && *e.URI == uri {
			return e
		}
	}
	t.Fatalf("no record carries the reference %q; the answer carries %s", uri, recordSummary(p))
	return pluginEntryResult{}
}

// withheldRecord returns the one record bound at the named config key, which is
// how a record whose reference was kept back is still addressable.
func withheldRecord(t *testing.T, p pluginListResult, origin string) pluginEntryResult {
	t.Helper()
	for _, e := range p.Plugins {
		if e.Origin == origin {
			return e
		}
	}
	t.Fatalf("no record is bound at %q; the answer carries %s", origin, recordSummary(p))
	return pluginEntryResult{}
}

// recordSummary renders an answer's records for a failure message.
func recordSummary(p pluginListResult) string {
	var b strings.Builder
	for _, e := range p.Plugins {
		if e.URI == nil {
			b.WriteString("[withheld @" + e.Origin + "] ")
			continue
		}
		b.WriteString(*e.URI + " ")
	}
	return strings.TrimSpace(b.String())
}

// TestThePluginReadingTakesItsReachClassFromTheOperationSet is clause two for
// this half.
//
// 🔴 PLANTED DEFECT, DEMONSTRATED: change the plugin-list row in the operation
// set to ReachMachine and this reading disappears from the listing a read
// credential is offered, with no edit to the tool — because the class is read
// where the tool enters the registry rather than declared beside it.
func TestThePluginReadingTakesItsReachClassFromTheOperationSet(t *testing.T) {
	t.Parallel()

	op, known := opsurface.Lookup("plugin-list")
	if !known {
		t.Fatal("the operation set declares no plugin-list operation, so nothing below measures a class")
	}
	if op.Reach != opsurface.ReachNone {
		t.Fatalf("plugin-list declares reach %v; this item adds no operation and changes no class, so a "+
			"value other than none means the operation set moved under it", op.Reach)
	}

	var spec *toolSpec
	for i := range deferredToolRegistry {
		if deferredToolRegistry[i].name == "plugin-list" {
			spec = &deferredToolRegistry[i]
		}
	}
	if spec == nil {
		t.Fatal("no tool in the deferred registry is named plugin-list")
	}
	if spec.reach != op.Reach {
		t.Errorf("the registered tool carries reach %v while its operation declares %v; the class was not "+
			"read from the operation set", spec.reach, op.Reach)
	}
	if spec.operation != "plugin-list" {
		t.Errorf("the registered tool answers operation %q, want plugin-list", spec.operation)
	}
}

// TestTheTwoTransportsDifferOnlyByThePluginLocationsTheListenerWithholds is
// clause three for this half: both transports are driven over one workspace and
// one enumeration, and the two answers are compared field by field.
func TestTheTwoTransportsDifferOnlyByThePluginLocationsTheListenerWithholds(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	listing := aListingBindingALocalPath()
	overTheListener := pluginsOverTheListener(t, pluginListenerOver(t, root, listing), everyRecord())
	atTheKeyboard := pluginsOverTheProcessStreams(t, root, listing, everyRecord())

	for _, complaint := range pluginTransportDifferenceComplaints(atTheKeyboard, overTheListener) {
		t.Error(complaint)
	}

	// The raw bytes, because a field-by-field walk can only compare the fields it
	// was written to walk. A path reaching the listener through some field this
	// case does not name would still be in here.
	encoded, err := json.Marshal(overTheListener)
	if err != nil {
		t.Fatalf("marshal the listener's answer: %v", err)
	}
	for _, location := range []string{theAbsolutePlugin, theRelativePlugin, theAnchoredPlugin, root} {
		if strings.Contains(string(encoded), location) {
			t.Errorf("the listener's answer carries the host location %q somewhere in its bytes: %s",
				location, encoded)
		}
	}
}

// pluginTransportDifferenceComplaints compares the two transports' answers and
// reports every way they differ that the keeping-back rule does not account for,
// and every way the rule should have made them differ and did not.
//
// 🔴 It complains when the two AGREE where they must not. Without that half, a
// value carried through to the listener reads as two answers agreeing, which is
// the shape the negative control below plants.
func pluginTransportDifferenceComplaints(atTheKeyboard, overTheListener pluginListResult) []string {
	var out []string

	if atTheKeyboard.WorkspaceRoot == nil {
		out = append(out, "the keyboard's answer names no workspace root, so there is nothing for the "+
			"listener's to be keeping back")
	}
	if overTheListener.WorkspaceRoot != nil {
		out = append(out, "the listener's answer names the workspace root "+*overTheListener.WorkspaceRoot)
	}
	if !overTheListener.WorkspaceRootWithheld {
		out = append(out, "the listener's answer drops the workspace root without saying it was kept back")
	}
	if atTheKeyboard.WorkspaceRootWithheld {
		out = append(out, "the keyboard's answer marks the workspace root as kept back")
	}
	if !overTheListener.HostLocationsWithheld {
		out = append(out, "the listener's answer does not state that host locations are kept back")
	}
	if atTheKeyboard.HostLocationsWithheld {
		out = append(out, "the keyboard's answer states that host locations are kept back")
	}

	if atTheKeyboard.Count != overTheListener.Count || len(atTheKeyboard.Plugins) != len(overTheListener.Plugins) {
		out = append(out, "the two answers carry different numbers of plugins, so the comparison below "+
			"would be comparing different populations")
		return out
	}
	if !slices.Equal(atTheKeyboard.Groups, overTheListener.Groups) {
		out = append(out, "the two answers report different group breakdowns")
	}

	keptBack := 0
	for i := range atTheKeyboard.Plugins {
		keyboard, listener := atTheKeyboard.Plugins[i], overTheListener.Plugins[i]
		if keyboard.Group != listener.Group || keyboard.Origin != listener.Origin {
			out = append(out, "record "+itoa(i)+" is a different plugin on the two transports")
			continue
		}
		where := " (record " + itoa(i) + ", bound at " + keyboard.Origin + ")"

		for _, same := range []struct {
			field    string
			a, b     string
			mismatch bool
		}{
			{"type", keyboard.Type, listener.Type, keyboard.Type != listener.Type},
			{"version", keyboard.Version, listener.Version, keyboard.Version != listener.Version},
			{"commit", keyboard.Commit, listener.Commit, keyboard.Commit != listener.Commit},
			{"boundary", keyboard.Boundary, listener.Boundary, keyboard.Boundary != listener.Boundary},
			{"readiness", keyboard.Readiness, listener.Readiness, keyboard.Readiness != listener.Readiness},
		} {
			if same.mismatch {
				out = append(out, "the "+same.field+" differs between the transports: "+
					same.a+" vs "+same.b+where)
			}
		}
		if !slices.Equal(keyboard.Capabilities, listener.Capabilities) ||
			!slices.Equal(keyboard.Exports, listener.Exports) ||
			!slices.Equal(keyboard.InputKinds, listener.InputKinds) {
			out = append(out, "a declared capability, export or input kind differs between the transports"+where)
		}
		if keyboard.Resolved != listener.Resolved {
			out = append(out, "whether the plugin resolved differs between the transports"+where)
		}

		// The reference. What must be kept back is read from the table declared
		// beside the fixture rather than from the classifier under test, for the
		// reason recorded there: an expectation that called that classifier would
		// move with it and see nothing.
		if keyboard.URI == nil {
			out = append(out, "the keyboard's answer keeps a reference back, so there is nothing for the "+
				"listener's to be compared against"+where)
			continue
		}
		wantKeptBack, judged := theExpectedWithholding[*keyboard.URI]
		if !judged {
			out = append(out, "the fixture binds "+*keyboard.URI+" and nothing says whether this reading "+
				"must keep it back, so that record passed unjudged"+where)
			continue
		}
		switch {
		case wantKeptBack && listener.URI != nil:
			out = append(out, "the listener's answer carries the host reference "+*listener.URI+where)
		case wantKeptBack && !listener.URIWithheld:
			out = append(out, "a reference was dropped without being marked as kept back"+where)
		case !wantKeptBack && listener.URIWithheld:
			out = append(out, "a reference naming no place on this host was kept back anyway"+where)
		case !wantKeptBack && (listener.URI == nil || keyboard.URI == nil || *listener.URI != *keyboard.URI):
			out = append(out, "a reference naming no place on this host did not survive the transport"+where)
		}
		if keyboard.URIWithheld {
			out = append(out, "the keyboard's answer marks a reference as kept back"+where)
		}
		if wantKeptBack {
			keptBack++
		}

		// The failure text, kept back whenever there was one.
		switch {
		case keyboard.Error != nil && listener.Error != nil:
			out = append(out, "the listener's answer carries a failure's text: "+*listener.Error+where)
		case keyboard.Error != nil && !listener.ErrorWithheld:
			out = append(out, "a failure's text was dropped without being marked as kept back"+where)
		case keyboard.Error == nil && listener.ErrorWithheld:
			out = append(out, "a record with no failure is marked as having its failure kept back"+where)
		}
		if keyboard.ErrorWithheld {
			out = append(out, "the keyboard's answer marks a failure's text as kept back"+where)
		}
	}

	// 🔴 The vacuity guard. Every assertion above is satisfied by a fixture in
	// which nothing names a place on this host, which would make a green mean
	// nothing at all.
	if keptBack == 0 {
		out = append(out, "no plugin's reference was kept back, so this comparison measured nothing: the "+
			"fixture no longer binds a reference that names a place on this host")
	}
	return out
}

// itoa renders a record index for a complaint.
func itoa(i int) string {
	return string(rune('0' + i%10))
}

// TestThePluginTransportComparisonRejectsALocationCarriedThrough is the
// negative control under the case above: it plants, in the compared answer, the
// very leak the rule exists to prevent and requires the comparison to object.
//
// Without it a green above is the absence of any complaint rather than the
// absence of THIS one, and a comparison written so that it says nothing when
// the two answers agree would pass a listener that kept nothing back.
func TestThePluginTransportComparisonRejectsALocationCarriedThrough(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	listing := aListingBindingALocalPath()
	overTheListener := pluginsOverTheListener(t, pluginListenerOver(t, root, listing), everyRecord())
	atTheKeyboard := pluginsOverTheProcessStreams(t, root, listing, everyRecord())

	if complaints := pluginTransportDifferenceComplaints(atTheKeyboard, overTheListener); len(complaints) > 0 {
		t.Fatalf("the unplanted comparison already objects, so nothing below measures the plant: %v",
			complaints)
	}

	for _, tc := range []struct {
		name  string
		plant func(p *pluginListResult)
	}{
		{"a kept-back reference carried through", func(p *pluginListResult) {
			for i := range p.Plugins {
				if p.Plugins[i].URIWithheld {
					p.Plugins[i].URI = stringPointer(theAbsolutePlugin)
					p.Plugins[i].URIWithheld = false
					return
				}
			}
			t.Fatal("no record was kept back, so there is nothing to carry through")
		}},
		{"a kept-back reference dropped with no mark", func(p *pluginListResult) {
			for i := range p.Plugins {
				if p.Plugins[i].URIWithheld {
					p.Plugins[i].URIWithheld = false
					return
				}
			}
			t.Fatal("no record was kept back, so there is no mark to drop")
		}},
		{"a failure's text carried through", func(p *pluginListResult) {
			for i := range p.Plugins {
				if p.Plugins[i].ErrorWithheld {
					p.Plugins[i].Error = stringPointer("read local plugin " + theAbsolutePlugin)
					p.Plugins[i].ErrorWithheld = false
					return
				}
			}
			t.Fatal("no failure was kept back, so there is nothing to carry through")
		}},
		{"the workspace root carried through", func(p *pluginListResult) {
			p.WorkspaceRoot = &root
			p.WorkspaceRootWithheld = false
		}},
		{"a published reference kept back anyway", func(p *pluginListResult) {
			for i := range p.Plugins {
				if !p.Plugins[i].URIWithheld {
					p.Plugins[i].URI = nil
					p.Plugins[i].URIWithheld = true
					return
				}
			}
		}},
		{"the rule no longer stated on the answer", func(p *pluginListResult) {
			p.HostLocationsWithheld = false
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			planted := clonePluginListResult(overTheListener)
			tc.plant(&planted)
			if complaints := pluginTransportDifferenceComplaints(atTheKeyboard, planted); len(complaints) == 0 {
				t.Errorf("the comparison accepts %s, so a green from it says nothing about that leak", tc.name)
			}
		})
	}
}

// clonePluginListResult copies an answer deeply enough that a planted defect in
// the copy cannot reach the original, which several subtests share.
func clonePluginListResult(p pluginListResult) pluginListResult {
	out := p
	out.Groups = slices.Clone(p.Groups)
	out.Plugins = slices.Clone(p.Plugins)
	for i := range out.Plugins {
		out.Plugins[i].Capabilities = slices.Clone(p.Plugins[i].Capabilities)
		out.Plugins[i].Exports = slices.Clone(p.Plugins[i].Exports)
		out.Plugins[i].InputKinds = slices.Clone(p.Plugins[i].InputKinds)
	}
	return out
}

// TestAKeptBackReferenceReadsAsKeptBackRatherThanAsNotThere is clause four for
// this half: two workspaces, one binding a plugin by a path on this host and one
// binding everything by published reference, are required to answer DIFFERENTLY
// over the listener.
//
// A reading that kept back every reference would answer the two identically and
// satisfy every other assertion in this file, which is what makes this the case
// that pins the classifier rather than the withholding.
func TestAKeptBackReferenceReadsAsKeptBackRatherThanAsNotThere(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	binding := pluginsOverTheListener(t, pluginListenerOver(t, root, aListingBindingALocalPath()), everyRecord())
	published := pluginsOverTheListener(t, pluginListenerOver(t, root, aListingNamingNoLocalPath()), everyRecord())

	bindingMarks, publishedMarks := 0, 0
	for _, e := range binding.Plugins {
		if e.URIWithheld {
			bindingMarks++
		}
	}
	for _, e := range published.Plugins {
		if e.URIWithheld {
			publishedMarks++
		}
	}
	if bindingMarks == 0 {
		t.Error("the workspace binding a plugin by a path on this host has no reference marked as kept back")
	}
	if publishedMarks != 0 {
		t.Errorf("the workspace binding everything by published reference has %d reference(s) marked as "+
			"kept back, so a caller cannot tell the two apart", publishedMarks)
	}

	// An absent reference always carries its mark, so it never reads as a record
	// that simply has none.
	for _, e := range binding.Plugins {
		if e.URI == nil && !e.URIWithheld {
			t.Errorf("a record bound at %q carries no reference and no mark, so it reads as a plugin with "+
				"no reference rather than as one this server is not naming", e.Origin)
		}
	}

	// The readable rendering makes the same distinction, because a person reading
	// it is making the same one a program reading the payload is.
	s := newTestServer(t)
	s.startDir = root
	s.transport = transportHTTP
	s.listPlugins = staticLister(aListingBindingALocalPath())
	res, rpcErr := pluginListInvoke(s, context.Background(), []byte(`{"response_format":"detailed"}`))
	if rpcErr != nil {
		t.Fatalf("the plugin reading returned an error envelope: %+v", rpcErr)
	}
	text := res.Content[0].Text
	for _, want := range []string{"(reference withheld)", "(reason withheld)"} {
		if !strings.Contains(text, want) {
			t.Errorf("the readable reading does not say %q, so a person reading it cannot tell a kept-back "+
				"value from a missing one:\n%s", want, text)
		}
	}
}

// TestThePluginReadingIsBounded is clause six for this half. The answer grows
// with what an operator binds, so it takes the bound its neighbours take rather
// than a second one invented here.
func TestThePluginReadingIsBounded(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	listing := aListingBindingALocalPath()
	f := pluginListenerOver(t, root, listing)

	// A caller that asked for nothing is handed the summary, not the records.
	summary := pluginsOverTheListener(t, f, map[string]any{})
	if summary.ResponseFormat != responseFormatConcise {
		t.Errorf("an argumentless call answered in the %q shape, want %q",
			summary.ResponseFormat, responseFormatConcise)
	}
	if len(summary.Plugins) != 0 || summary.Returned != 0 {
		t.Errorf("the summary carries %d records, want none", len(summary.Plugins))
	}
	if summary.Count != 6 || len(summary.Groups) != 2 {
		t.Errorf("the summary does not report the whole population: count %d, groups %+v",
			summary.Count, summary.Groups)
	}

	// A caller that asked for records without saying how many is not handed the
	// whole of it — the default page bounds it.
	page := pluginsOverTheListener(t, f, map[string]any{"response_format": "detailed", "limit": 2})
	if page.Returned != 2 || page.Offset != 0 || !page.MoreRemaining {
		t.Errorf("the first page is offset %d returned %d more %v, want 0/2/true",
			page.Offset, page.Returned, page.MoreRemaining)
	}
	next := pluginsOverTheListener(t, f,
		map[string]any{"response_format": "detailed", "limit": 2, "offset": page.Offset + page.Returned})
	if next.Offset != 2 || next.Returned != 2 || !next.MoreRemaining {
		t.Errorf("the second page is offset %d returned %d more %v, want 2/2/true",
			next.Offset, next.Returned, next.MoreRemaining)
	}
	last := pluginsOverTheListener(t, f,
		map[string]any{"response_format": "detailed", "limit": 2, "offset": 4})
	if last.Returned != 2 || last.MoreRemaining {
		t.Errorf("the last page is returned %d more %v, want 2/false", last.Returned, last.MoreRemaining)
	}
	if page.Plugins[0].Origin == next.Plugins[0].Origin {
		t.Error("the second page repeats the first page's first record")
	}

	// A window this surface does not serve is refused rather than clamped, by the
	// one resolver every bounded listing here reads.
	got := f.callTool(t, f.grantOf(t, scopeRead), "plugin-list",
		map[string]any{"response_format": "detailed", "limit": 5000})
	var refused struct {
		Content []toolTextContent `json:"content"`
		IsError bool              `json:"isError"`
	}
	if err := json.Unmarshal(got.frame.Result, &refused); err != nil {
		t.Fatalf("the answer is not a tool result: %v", err)
	}
	if !refused.IsError {
		t.Error("a limit outside the served range was not refused")
	}
}

// TestThePluginReadingRefusesWithoutTheEnumeration pins the sentence a session
// carrying no enumeration answers with, which is what every case above is
// asserting the absence of.
//
// A session that answered such a call with an empty listing would tell a caller
// this workspace binds no plugins, which is a different statement and the shape
// this refusal exists to keep off the wire.
func TestThePluginReadingRefusesWithoutTheEnumeration(t *testing.T) {
	t.Parallel()

	s := newTestServer(t)
	s.startDir = t.TempDir()
	s.transport = transportStdio

	res, rpcErr := pluginListInvoke(s, context.Background(), nil)
	if rpcErr != nil {
		t.Fatalf("the plugin reading returned an error envelope: %+v", rpcErr)
	}
	if !res.IsError {
		t.Fatal("a session carrying no enumeration answered the reading rather than refusing it")
	}
	if !strings.Contains(res.Content[0].Text, "without the plugin enumeration the reading is composed from") {
		t.Errorf("the refusal sentence has moved: %q", res.Content[0].Text)
	}
}

// TestThePluginOriginIsNeverAPlaceOnThisHost pins the measurement that decided
// which field this reading keeps back.
//
// Where a plugin was BOUND and where its guest SITS are different facts, and the
// first is answered whole on both transports: a config key is the same string on
// every host, and a pinned digest is a digest. The three admissible shapes are
// asserted here so a change that made a binding report its guest's location in
// this field fails rather than publishes it.
func TestThePluginOriginIsNeverAPlaceOnThisHost(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	got := pluginsOverTheListener(t, pluginListenerOver(t, root, aListingBindingALocalPath()), everyRecord())

	seen := 0
	for _, e := range got.Plugins {
		switch {
		case e.Origin == "":
			// A built-in is bound by nothing.
		case strings.HasPrefix(e.Origin, "plugins.") && strings.HasSuffix(e.Origin, "]"):
			seen++
		case strings.HasPrefix(e.Origin, "pinned "):
			seen++
		default:
			t.Errorf("a plugin reports its binding as %q, which is none of the shapes this reading answers "+
				"whole — a config key, a pinned digest, or nothing. If it is now a place on this host it "+
				"must be kept back like the reference beside it", e.Origin)
		}
		if referenceNamesAHostLocation(e.Origin) {
			t.Errorf("a plugin's binding %q classifies as a filesystem path and is being answered whole",
				e.Origin)
		}
	}
	if seen == 0 {
		t.Error("no record reported a binding at all, so this measured nothing")
	}
}

// TestTheCascadeAnchorsABareSocketReferenceIntoAnAbsolutePath pins the premise
// that decides WHICH value the classification is made against.
//
// A binding written as a bare name reaches the reading as an absolute path,
// because the resolution cascade anchors a relative reference on the socket
// lists against the directory of the file that declared it. So the reading
// classifies the reference the resolved configuration carries, never the one an
// author typed — and a case that classified the authored value would publish
// exactly these.
//
// It fails if that anchoring is ever removed, which is the moment the reasoning
// above stops being true and has to be looked at again.
func TestTheCascadeAnchorsABareSocketReferenceIntoAnAbsolutePath(t *testing.T) {
	// Not parallel: redirects the machine-wide layer through the environment.
	globalPath := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(globalPath, []byte("registries: []\n"), 0o644); err != nil {
		t.Fatalf("write the machine-wide layer: %v", err)
	}
	t.Setenv(workspaceconfig.EnvConfigOverride, globalPath)

	root := t.TempDir()
	body := "plugins:\n  linter:\n    - uri: linter/publishing\n"
	if err := os.WriteFile(filepath.Join(root, workspaceconfig.ConfigFilename), []byte(body), 0o644); err != nil {
		t.Fatalf("write the workspace's own configuration: %v", err)
	}
	rw, err := workspaceconfig.Resolve(root, root)
	if err != nil {
		t.Fatalf("resolve the fixture workspace: %v", err)
	}

	linter := rw.Config().Plugins.Linter
	if len(linter) != 1 {
		t.Fatalf("the fixture configuration resolved %d linter bindings, want 1", len(linter))
	}
	if !filepath.IsAbs(linter[0].URI) {
		t.Fatalf("the cascade left the bare reference %q unanchored; the reading classifies the resolved "+
			"value precisely because the cascade anchors it, so that reasoning needs re-reading",
			linter[0].URI)
	}
	if !referenceNamesAHostLocation(linter[0].URI) {
		t.Errorf("the anchored reference %q does not classify as a place on this host, so a workspace whose "+
			"configuration names no path at all would have one published", linter[0].URI)
	}
}
