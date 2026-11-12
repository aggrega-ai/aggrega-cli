// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package serve

import (
	"os"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// seededRegistry is the registry the seeding tests below seed. They run offline,
// so no seeding reaches the network, and offline only the official hub can be
// seeded: any other registry has no policy to pin and is refused, which is a test
// of its own. So the hub is the registry to seed wherever the registry is not the
// subject.
const seededRegistry = workspaceconfig.HubBaselineHost

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
          uri: provider/helm-chart:1.0.0
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
		"providers=provider/helm-chart:1.0.0",
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
		"providers=provider/helm-chart:1.0.0",
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
		"registry": seededRegistry,
		// The fixture's global layer already exists, so a seeding that means to
		// write it says so; the guard itself has its own test below.
		"force": true,
	})
	var res configInitResult
	decodeDaemonResult(t, resp, &res)

	if res.Host != seededRegistry {
		t.Errorf("host: got %q, want %s", res.Host, seededRegistry)
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
	if !strings.Contains(string(global), seededRegistry) {
		t.Errorf("the global layer does not carry the entry the answer said it wrote:\n%s", global)
	}
	ws, err := os.ReadFile(wsPath)
	if err != nil {
		t.Fatalf("read the workspace layer: %v", err)
	}
	if strings.Contains(string(ws), seededRegistry) {
		t.Errorf("the entry reached the workspace layer, which the answer did not claim:\n%s", ws)
	}
}

// TestConfigInitOnTheDaemonHonoursTheWorkspaceOverride is the calibration for the
// test above: a method hard-coding machineWide to true would pass that one.
func TestConfigInitOnTheDaemonHonoursTheWorkspaceOverride(t *testing.T) {
	t.Parallel()
	srv, globalPath, wsPath := registryWriteFixture(t, "offline: true\nworkspace: {}\n")

	resp := callDaemonMethod(t, srv, "aggrega/configInit", map[string]any{
		"registry":  seededRegistry,
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
	if !strings.Contains(string(ws), seededRegistry) {
		t.Errorf("the workspace layer does not carry the entry:\n%s", ws)
	}
	global, err := os.ReadFile(globalPath)
	if err != nil {
		t.Fatalf("read the global layer: %v", err)
	}
	if strings.Contains(string(global), seededRegistry) {
		t.Errorf("the entry reached the machine's own configuration:\n%s", global)
	}
}

// TestConfigInitOnTheDaemonDryRunsWithoutWriting drives the non-writing delivery
// to its own effect, and the absent-write assertion is the half that matters: a
// delivery that answered with the content and wrote the file anyway would satisfy
// every other field.
func TestConfigInitOnTheDaemonDryRunsWithoutWriting(t *testing.T) {
	t.Parallel()
	// Offline, so the seeding's trust fetch is refused before any dial rather than
	// sent to a name no resolver answers; this test is about the delivery alone.
	srv, globalPath, _ := registryWriteFixture(t, "offline: true\n")
	if err := os.Remove(globalPath); err != nil && !os.IsNotExist(err) {
		t.Fatalf("clear the global layer: %v", err)
	}

	resp := callDaemonMethod(t, srv, "aggrega/configInit", map[string]any{
		"registry": seededRegistry,
		"dryRun":   true,
	})
	var res configInitResult
	decodeDaemonResult(t, resp, &res)

	if res.Written {
		t.Error("written: got true under a dry run, want false")
	}
	if !strings.Contains(res.Content, seededRegistry) {
		t.Errorf("content does not carry the seeded registry: %q", res.Content)
	}
	if res.Scope.ConfigFile != globalPath {
		t.Errorf("configFile: got %q, want the file the content is for, %q", res.Scope.ConfigFile, globalPath)
	}
	if _, err := os.Stat(globalPath); !os.IsNotExist(err) {
		t.Errorf("a dry run created the configuration file at %s (stat err = %v)", globalPath, err)
	}
}

// TestADryRunOnTheDaemonRefusesEXACTLYWhereAWriteRefuses is the contract the
// non-writing delivery exists to be trustworthy about: it differs from the write
// in one respect only — it does not write — so every refusal the write performs it
// performs too.
//
// That is what lets a caller use it as a PREDICATE. If it refuses, the write would
// have refused, for the same reason and with the same message, so a client can ask
// before committing to the change. The two rows are the whole of the contract: the
// guard fires under a dry run, and force alongside it answers with the merge an
// overwriting write would produce while still writing nothing.
//
// 🔴 The byte-identity assertion in every row is the one that matters. A delivery
// that composed the merge and also wrote it would satisfy every other field here,
// and that is precisely the defect an honest name has to exclude.
//
// The third row is the predicate property asserted directly, rather than
// approximated by a substring: the bytes a dry run answers with are compared
// against the bytes a forced write actually puts on disk, over the same starting
// document. A "shows the merge" claim checked by looking for one host name would
// hold just as well for a document that had silently dropped every other setting.
func TestADryRunOnTheDaemonRefusesEXACTLYWhereAWriteRefuses(t *testing.T) {
	t.Parallel()

	// The GLOBAL layer is what config init writes; the fixture's own argument is
	// the workspace layer, which this operation never touches. So the pre-existing
	// document the merge has to preserve is seeded here rather than through the
	// fixture, and it carries a setting the registration knows nothing about.
	const existingGlobal = "offline: true\nregistries:\n    - host: already.example.test\n"

	seed := func(t *testing.T) (*Server, string) {
		t.Helper()
		// The workspace layer carries the offline posture, not the global layer
		// seeded below: the fixture resolves before that layer is rewritten, so
		// the posture it states is not the one in force, and a seeding resolved
		// online would send its fetch to the network.
		srv, globalPath, _ := registryWriteFixture(t, "offline: true\nworkspace: {}\n")
		if err := os.WriteFile(globalPath, []byte(existingGlobal), 0o644); err != nil {
			t.Fatalf("seed the global layer: %v", err)
		}
		return srv, globalPath
	}

	for _, tc := range []struct {
		name    string
		force   bool
		refused bool
	}{
		{"the guard fires under a dry run, exactly as it fires under a write", false, true},
		{"force alongside it answers with the merge and still writes nothing", true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv, globalPath := seed(t)

			params := map[string]any{"registry": seededRegistry, "dryRun": true}
			if tc.force {
				params["force"] = true
			}
			resp := callDaemonMethod(t, srv, "aggrega/configInit", params)

			if tc.refused {
				if resp.Error == nil {
					t.Fatalf("a dry run over an existing file must be refused; got result %s", resp.Result)
				}
				if !strings.Contains(resp.Error.Message, "already exists") {
					t.Errorf("refusal = %q; want the write's own reason", resp.Error.Message)
				}
			} else {
				var res configInitResult
				decodeDaemonResult(t, resp, &res)
				if res.Written {
					t.Error("written: got true under a dry run, want false")
				}
				for _, want := range []string{seededRegistry, "already.example.test", "offline"} {
					if !strings.Contains(res.Content, want) {
						t.Errorf("content is not the MERGE — %q is missing:\n%s", want, res.Content)
					}
				}
			}

			after, err := os.ReadFile(globalPath)
			if err != nil {
				t.Fatalf("read the global layer afterwards: %v", err)
			}
			if string(after) != existingGlobal {
				t.Errorf("a dry run changed the file:\nbefore:\n%s\nafter:\n%s", existingGlobal, after)
			}
		})
	}

	t.Run("the bytes a dry run answers with are the bytes a forced write lands", func(t *testing.T) {
		reportSrv, reportPath := seed(t)
		var reported configInitResult
		decodeDaemonResult(t, callDaemonMethod(t, reportSrv, "aggrega/configInit", map[string]any{
			"registry": seededRegistry, "force": true, "dryRun": true,
		}), &reported)

		writeSrv, writePath := seed(t)
		var written configInitResult
		decodeDaemonResult(t, callDaemonMethod(t, writeSrv, "aggrega/configInit", map[string]any{
			"registry": seededRegistry, "force": true,
		}), &written)

		onDisk, err := os.ReadFile(writePath)
		if err != nil {
			t.Fatalf("read the written global layer: %v", err)
		}
		if reported.Content != string(onDisk) {
			t.Errorf("a dry run does not show what a forced write lands\n--- dry run ---\n%s\n--- on disk ---\n%s",
				reported.Content, onDisk)
		}
		// The control: without it, two identically-empty answers would pass.
		if !strings.Contains(reported.Content, seededRegistry) {
			t.Errorf("the compared content carries nothing: %q", reported.Content)
		}
		untouched, err := os.ReadFile(reportPath)
		if err != nil {
			t.Fatalf("read the dry-run global layer: %v", err)
		}
		if string(untouched) != existingGlobal {
			t.Errorf("the dry run wrote to its own file:\n%s", untouched)
		}
	})
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

// TestConfigInitOnTheDaemonSaysWhetherTheFileWasAlreadyThere drives the seeding
// over a file that does not exist and over one that does, writing and dry-running
// each, and reads whether the answer says the file was already there.
//
// The command line says it — it creates the file, or uses the one it found — and
// the two surfaces carry one capability, so the answer here carries the same fact.
// It is read from the response bytes rather than from a decode: a decode into a
// struct whose field defaults to false reads an answer that never carried the key
// exactly as one that carried false, so only the bytes show the key was sent.
// A request that did not send force never proceeds over an existing file, so the
// key is absent from those answers and present and true wherever the file was
// there; the existing-file refusal itself has its own test above.
func TestConfigInitOnTheDaemonSaysWhetherTheFileWasAlreadyThere(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		scenario   string
		fileExists bool
		dryRun     bool
	}{
		{"a seeding that creates the file", false, false},
		{"a forced seeding over the file already there", true, false},
		{"a dry run for a file that does not exist", false, true},
		{"a forced dry run over the file already there", true, true},
	} {
		t.Run(tc.scenario, func(t *testing.T) {
			t.Parallel()
			srv, globalPath, _ := registryWriteFixture(t, "offline: true\nworkspace: {}\n")
			params := map[string]any{"registry": seededRegistry}
			if tc.fileExists {
				params["force"] = true
			} else if err := os.Remove(globalPath); err != nil {
				t.Fatalf("clear the global layer: %v", err)
			}
			if tc.dryRun {
				params["dryRun"] = true
			}

			resp := callDaemonMethod(t, srv, "aggrega/configInit", params)
			var res configInitResult
			decodeDaemonResult(t, resp, &res)
			raw := string(resp.Result)

			switch {
			case tc.fileExists && !strings.Contains(raw, `"existed":true`):
				t.Errorf("the file was already there and the answer does not say so:\n%s", raw)
			case !tc.fileExists && strings.Contains(raw, `"existed"`):
				t.Errorf("the file did not exist and the answer says it was there:\n%s", raw)
			}
			if res.Existed != tc.fileExists {
				t.Errorf("existed: got %v, want %v", res.Existed, tc.fileExists)
			}
			if res.Scope.ConfigFile != globalPath {
				t.Errorf("configFile: got %q, want the file the answer is about, %q", res.Scope.ConfigFile, globalPath)
			}
		})
	}
}
