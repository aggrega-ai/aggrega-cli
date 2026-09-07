// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package serve

import (
	"os"
	"strings"
	"testing"
)

// The workspace layer both freeze tests resolve over. The governance socket
// carries one attachment of each tier, which is what makes the default and the
// widened answer differ rather than merely being two spellings of one set.
const configFreezeWorkspaceYAML = `offline: true
workspace:
    catalog_dirs:
        - ./catalog
plugins:
    providers:
        - capability: helm-chart
          uri: oci://example.test/plugin/provider/helm-chart:1.0.0
    governance:
        - uri: oci://example.test/plugin/governance/label-injector:1.0.0
          role: label-transform
        - uri: oci://example.test/plugin/governance/gate:1.0.0
          role: ir-verdict
    linter:
        - uri: oci://example.test/plugin/linter/publishing:1.0.0
`

// TestConfigCaptureOnTheDaemonWritesTheDocumentItReports is the ruled-posture
// proof: the method WRITES the configuration and the answer names the file, so a
// client's next act is opening what was written rather than placing content the
// answer handed back.
//
// The file assertion is the load-bearing half. A method that reported the frozen
// set and wrote nothing would satisfy every field on the response, and that is
// exactly the posture this operation is ruled not to have.
func TestConfigCaptureOnTheDaemonWritesTheDocumentItReports(t *testing.T) {
	t.Parallel()
	srv, globalPath, wsPath := registryWriteFixture(t, configFreezeWorkspaceYAML)

	resp := callDaemonMethod(t, srv, "aggrega/configCapture", map[string]any{})
	var res configCaptureResult
	decodeDaemonResult(t, resp, &res)

	if res.ConfigFile != wsPath {
		t.Errorf("configFile: got %q, want this workspace's own file %q", res.ConfigFile, wsPath)
	}
	if !res.Captured {
		t.Error("captured: got false, want true — the workspace carries build-critical bindings")
	}
	if res.WorkspaceRoot == "" {
		t.Error("workspaceRoot is empty; a caller reading a workspace-scoped write is owed which workspace")
	}

	// The default takes the label-transform governance attachment and leaves the
	// verdict one, which is the whole of what the tier axis decides.
	var got []string
	for _, s := range res.Sockets {
		for _, a := range s.Attachments {
			got = append(got, s.Field+"="+a.URI)
		}
	}
	want := []string{
		"providers=oci://example.test/plugin/provider/helm-chart:1.0.0",
		"governance=oci://example.test/plugin/governance/label-injector:1.0.0",
	}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("frozen set:\n got %v\nwant %v", got, want)
	}

	// The write landed in the workspace file and nowhere else. Freezing resolves no
	// write target, so a request that reached the machine's own configuration would
	// be reaching past the workspace the session opened.
	written, err := os.ReadFile(wsPath)
	if err != nil {
		t.Fatalf("read the workspace layer: %v", err)
	}
	if !strings.Contains(string(written), "helm-chart:1.0.0") {
		t.Errorf("the workspace layer does not carry what the answer said was frozen:\n%s", written)
	}
	global, err := os.ReadFile(globalPath)
	if err != nil {
		t.Fatalf("read the global layer: %v", err)
	}
	if strings.Contains(string(global), "helm-chart") {
		t.Errorf("freezing reached the machine's own configuration, which it must never do:\n%s", global)
	}
}

// TestConfigCaptureOnTheDaemonWidensToEverySocket is the calibration for the test
// above: without it, a handler that ignored allPlugins and always froze the
// narrow set would pass that one and be wrong.
func TestConfigCaptureOnTheDaemonWidensToEverySocket(t *testing.T) {
	t.Parallel()
	srv, _, _ := registryWriteFixture(t, configFreezeWorkspaceYAML)

	resp := callDaemonMethod(t, srv, "aggrega/configCapture", map[string]any{"allPlugins": true})
	var res configCaptureResult
	decodeDaemonResult(t, resp, &res)

	var got []string
	for _, s := range res.Sockets {
		for _, a := range s.Attachments {
			got = append(got, s.Field+"="+a.URI)
		}
	}
	want := []string{
		"providers=oci://example.test/plugin/provider/helm-chart:1.0.0",
		"governance=oci://example.test/plugin/governance/label-injector:1.0.0",
		"governance=oci://example.test/plugin/governance/gate:1.0.0",
		"linter=oci://example.test/plugin/linter/publishing:1.0.0",
	}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("widened frozen set:\n got %v\nwant %v", got, want)
	}
}

// TestConfigCaptureOnTheDaemonSerializesLowercaseFieldNames pins the wire
// spellings against the raw response bytes rather than against a decode.
//
// 🔴 It exists because the configuration type these attachments come from carries
// field spellings for the configuration document's own form and none for this
// protocol, so handing it to the serializer directly answers with capitalised
// names while every other field on this surface is lowercase. Decoding into the
// result type cannot see that: the decode would succeed either way. Only the bytes
// show it.
func TestConfigCaptureOnTheDaemonSerializesLowercaseFieldNames(t *testing.T) {
	t.Parallel()
	srv, _, _ := registryWriteFixture(t, configFreezeWorkspaceYAML)

	resp := callDaemonMethod(t, srv, "aggrega/configCapture", map[string]any{})
	if resp.Error != nil {
		t.Fatalf("expected a result; got error %d: %s", resp.Error.Code, resp.Error.Message)
	}
	raw := string(resp.Result)
	for _, want := range []string{`"uri"`, `"capability"`, `"role"`, `"socket"`, `"field"`, `"attachments"`} {
		if !strings.Contains(raw, want) {
			t.Errorf("the response does not carry %s:\n%s", want, raw)
		}
	}
	for _, forbidden := range []string{`"URI"`, `"Capability"`, `"Role"`, `"Function"`, `"With"`} {
		if strings.Contains(raw, forbidden) {
			t.Errorf("the response carries %s, the configuration type's own spelling rather than this surface's:\n%s",
				forbidden, raw)
		}
	}
}

// TestConfigCaptureOnTheDaemonSaysSoWhenThereIsNothingToFreeze covers the empty
// answer as a success. Both content fields must be present and empty rather than
// absent, so a client reading a workspace with nothing build-critical meets an
// empty list instead of a missing key.
func TestConfigCaptureOnTheDaemonSaysSoWhenThereIsNothingToFreeze(t *testing.T) {
	t.Parallel()
	srv, _, wsPath := registryWriteFixture(t, "offline: true\nworkspace: {}\n")

	resp := callDaemonMethod(t, srv, "aggrega/configCapture", map[string]any{})
	if resp.Error != nil {
		t.Fatalf("a workspace with nothing build-critical must answer, not fail; got %d: %s",
			resp.Error.Code, resp.Error.Message)
	}
	var res configCaptureResult
	decodeDaemonResult(t, resp, &res)
	if res.Captured {
		t.Error("captured: got true, want false")
	}
	if res.ConfigFile != wsPath {
		t.Errorf("configFile: got %q, want %q even where nothing was frozen", res.ConfigFile, wsPath)
	}
	raw := string(resp.Result)
	for _, want := range []string{`"sockets":[]`, `"catalogDirs":[]`} {
		if !strings.Contains(raw, want) {
			t.Errorf("the response does not carry %s as an empty array:\n%s", want, raw)
		}
	}
}

// TestConfigInitOnTheDaemonReportsItsMachineWideScope is the scope proof for the
// seeding: it drives the method with NO scope flag — the shape a caller that has
// not thought about reach sends — and asserts the answer names the shared file and
// says the change is machine-wide.
//
// Asserting only that the entry landed would pass on a method that wrote the
// machine's configuration and described the change as this workspace's, which is
// the failure worth catching.
func TestConfigInitOnTheDaemonReportsItsMachineWideScope(t *testing.T) {
	t.Parallel()
	srv, globalPath, wsPath := registryWriteFixture(t, "offline: true\nworkspace: {}\n")

	resp := callDaemonMethod(t, srv, "aggrega/configInit", map[string]any{
		"registry": "seeded.example.test",
		// The fixture's global layer already exists, so a seeding that means to
		// write it says so; the guard itself has its own test below.
		"force": true,
	})
	var res configInitResult
	decodeDaemonResult(t, resp, &res)

	if res.Host != "seeded.example.test" {
		t.Errorf("host: got %q, want seeded.example.test", res.Host)
	}
	if !res.Written {
		t.Error("written: got false, want true — the default delivery writes")
	}
	if res.Content != "" {
		t.Errorf("content is present under a writing delivery: %q", res.Content)
	}
	if !res.Scope.MachineWide {
		t.Errorf("a seeding with no scope flag must report a machine-wide change; got scope %+v", res.Scope)
	}
	if res.Scope.ConfigFile != globalPath {
		t.Errorf("configFile: got %q, want the global layer %q", res.Scope.ConfigFile, globalPath)
	}

	global, err := os.ReadFile(globalPath)
	if err != nil {
		t.Fatalf("read the global layer: %v", err)
	}
	if !strings.Contains(string(global), "seeded.example.test") {
		t.Errorf("the global layer does not carry the entry the answer said it wrote:\n%s", global)
	}
	ws, err := os.ReadFile(wsPath)
	if err != nil {
		t.Fatalf("read the workspace layer: %v", err)
	}
	if strings.Contains(string(ws), "seeded.example.test") {
		t.Errorf("the entry reached the workspace layer, which the answer did not claim:\n%s", ws)
	}
}

// TestConfigInitOnTheDaemonHonoursTheWorkspaceOverride is the calibration for the
// test above: a method hard-coding machineWide to true would pass that one.
func TestConfigInitOnTheDaemonHonoursTheWorkspaceOverride(t *testing.T) {
	t.Parallel()
	srv, globalPath, wsPath := registryWriteFixture(t, "offline: true\nworkspace: {}\n")

	resp := callDaemonMethod(t, srv, "aggrega/configInit", map[string]any{
		"registry":  "local.example.test",
		"workspace": true,
		"force":     true,
	})
	var res configInitResult
	decodeDaemonResult(t, resp, &res)

	if res.Scope.MachineWide {
		t.Errorf("a workspace-targeted seeding must not report a machine-wide change; got scope %+v", res.Scope)
	}
	if res.Scope.ConfigFile != wsPath {
		t.Errorf("configFile: got %q, want the workspace layer %q", res.Scope.ConfigFile, wsPath)
	}
	ws, err := os.ReadFile(wsPath)
	if err != nil {
		t.Fatalf("read the workspace layer: %v", err)
	}
	if !strings.Contains(string(ws), "local.example.test") {
		t.Errorf("the workspace layer does not carry the entry:\n%s", ws)
	}
	global, err := os.ReadFile(globalPath)
	if err != nil {
		t.Fatalf("read the global layer: %v", err)
	}
	if strings.Contains(string(global), "local.example.test") {
		t.Errorf("the entry reached the machine's own configuration:\n%s", global)
	}
}

// TestConfigInitOnTheDaemonReportsWithoutWriting drives the reporting delivery to
// its own effect, and the absent-write assertion is the half that matters: a
// delivery that reported the content and wrote the file anyway would satisfy every
// other field.
func TestConfigInitOnTheDaemonReportsWithoutWriting(t *testing.T) {
	t.Parallel()
	srv, globalPath, _ := registryWriteFixture(t, "offline: true\nworkspace: {}\n")
	before, err := os.ReadFile(globalPath)
	if err != nil {
		t.Fatalf("read the global layer: %v", err)
	}

	resp := callDaemonMethod(t, srv, "aggrega/configInit", map[string]any{
		"registry": "reported.example.test",
		"report":   true,
	})
	var res configInitResult
	decodeDaemonResult(t, resp, &res)

	if res.Written {
		t.Error("written: got true under a reporting delivery, want false")
	}
	if !strings.Contains(res.Content, "reported.example.test") {
		t.Errorf("content does not carry the seeded registry: %q", res.Content)
	}
	if res.Scope.ConfigFile != globalPath {
		t.Errorf("configFile: got %q, want the file the content is for, %q", res.Scope.ConfigFile, globalPath)
	}

	after, err := os.ReadFile(globalPath)
	if err != nil {
		t.Fatalf("read the global layer after reporting: %v", err)
	}
	if string(before) != string(after) {
		t.Errorf("a reporting seeding changed the file:\nbefore:\n%s\nafter:\n%s", before, after)
	}
}

// TestConfigInitOnTheDaemonRefusesAnExistingFile drives the guard, and asserts the
// refused seeding left the file byte-identical — a guard that refused after writing
// would still produce the message.
func TestConfigInitOnTheDaemonRefusesAnExistingFile(t *testing.T) {
	t.Parallel()
	srv, globalPath, _ := registryWriteFixture(t, "offline: true\nworkspace: {}\n")
	before, err := os.ReadFile(globalPath)
	if err != nil {
		t.Fatalf("read the global layer: %v", err)
	}

	resp := callDaemonMethod(t, srv, "aggrega/configInit", map[string]any{
		"registry": "refused.example.test",
	})
	if resp.Error == nil {
		t.Fatalf("seeding over an existing file must be refused; got result %s", resp.Result)
	}
	if !strings.Contains(resp.Error.Message, "already exists") {
		t.Errorf("refusal = %q; want it to say the file already exists", resp.Error.Message)
	}
	if !strings.Contains(resp.Error.Message, globalPath) {
		t.Errorf("refusal = %q; want it to name the file", resp.Error.Message)
	}
	// 🔴 The remedy a caller here can act on is the request field, not the command
	// line's flag the shared layer's own sentence names.
	if strings.Contains(resp.Error.Message, "--force") {
		t.Errorf("the refusal names a command-line flag a request cannot send: %q", resp.Error.Message)
	}
	if !strings.Contains(resp.Error.Message, "send force") {
		t.Errorf("the refusal does not name the field that proceeds: %q", resp.Error.Message)
	}

	after, err := os.ReadFile(globalPath)
	if err != nil {
		t.Fatalf("read the global layer after the refusal: %v", err)
	}
	if string(before) != string(after) {
		t.Errorf("a refused seeding changed the file:\nbefore:\n%s\nafter:\n%s", before, after)
	}
}

// TestConfigInitOnTheDaemonRefusesBothOverridesInItsOwnSpellings pins that the
// refusal a daemon caller meets names the fields it sent.
//
// 🔴 The shared write-target policy refuses the same pair, naming the command
// line's flags — which a caller here never sent and cannot reach. So this guard is
// a duplicate of the CHECK and not of the logic: what differs is which vocabulary
// the refusal speaks.
func TestConfigInitOnTheDaemonRefusesBothOverridesInItsOwnSpellings(t *testing.T) {
	t.Parallel()
	srv, _, _ := registryWriteFixture(t, "offline: true\nworkspace: {}\n")

	resp := callDaemonMethod(t, srv, "aggrega/configInit", map[string]any{
		"registry":  "example.test",
		"global":    true,
		"workspace": true,
	})
	if resp.Error == nil {
		t.Fatalf("both overrides must be refused; got result %s", resp.Result)
	}
	if strings.Contains(resp.Error.Message, "--global") || strings.Contains(resp.Error.Message, "--workspace") {
		t.Errorf("the refusal names a command-line flag a request cannot send: %q", resp.Error.Message)
	}
	if !strings.Contains(resp.Error.Message, "global") || !strings.Contains(resp.Error.Message, "workspace") {
		t.Errorf("the refusal does not name the two fields that conflict: %q", resp.Error.Message)
	}
}

// TestConfigInitOnTheDaemonSeedsTheDefaultRegistry pins that an absent registry
// takes the command line's own default rather than the zero value, which the
// registration would refuse for naming no host. The two surfaces expose one
// capability, so the same request must mean the same thing on both.
func TestConfigInitOnTheDaemonSeedsTheDefaultRegistry(t *testing.T) {
	t.Parallel()
	srv, _, _ := registryWriteFixture(t, "offline: true\nworkspace: {}\n")

	resp := callDaemonMethod(t, srv, "aggrega/configInit", map[string]any{"force": true})
	var res configInitResult
	decodeDaemonResult(t, resp, &res)

	if res.Host == "" {
		t.Fatal("host is empty; an absent registry must take a default rather than reach the registration as none")
	}
	if !strings.Contains(res.Host, "aggrega.ai") {
		t.Errorf("host = %q; want the official hub the command line's flag defaults to", res.Host)
	}
}
