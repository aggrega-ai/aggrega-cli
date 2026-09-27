// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package configops_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/authoredcontent"
	"github.com/aggrega-ai/aggrega-cli/internal/configops"
	"github.com/aggrega-ai/aggrega-cli/internal/registryops"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// frozenLookup answers environment reads from a fixed map, so a resolution in a
// test never consults the process environment and can never reach the machine's
// own configuration.
func frozenLookup(vars map[string]string) func(string) string {
	return func(k string) string { return vars[k] }
}

// workspaceAt writes cfg as the workspace configuration under a fresh directory
// and resolves it with the global layer pointed at globalPath, returning the
// resolution both operations take.
func workspaceAt(t *testing.T, cfg, globalCfg string) (*workspaceconfig.ResolvedWorkspace, string, string) {
	t.Helper()
	root := t.TempDir()
	wsPath := filepath.Join(root, "ws", workspaceconfig.ConfigFilename)
	if err := os.MkdirAll(filepath.Dir(wsPath), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(wsPath, []byte(cfg), 0o644); err != nil {
		t.Fatalf("write workspace config: %v", err)
	}
	globalPath := filepath.Join(root, "global", workspaceconfig.ConfigFilename)
	if err := os.MkdirAll(filepath.Dir(globalPath), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if globalCfg != "" {
		if err := os.WriteFile(globalPath, []byte(globalCfg), 0o644); err != nil {
			t.Fatalf("write global config: %v", err)
		}
	}
	lookup := frozenLookup(map[string]string{workspaceconfig.EnvConfigOverride: globalPath})
	rw, err := workspaceconfig.Resolve(filepath.Dir(wsPath), "", lookup)
	if err != nil {
		t.Fatalf("resolve workspace: %v", err)
	}
	return rw, wsPath, globalPath
}

// TestBothOperationsRefuseAnAbsentWorkspace drives the nil-workspace refusal on
// each entry point.
//
// It is the one shape a caller cannot get right by accident: the workspace sits
// at a named parameter precisely so a nil is a wiring error rather than a value
// the operation quietly repairs by resolving from the current directory, which
// would make the answer depend on a directory no caller named.
func TestBothOperationsRefuseAnAbsentWorkspace(t *testing.T) {
	if _, err := configops.Init(t.Context(), nil, configops.InitOptions{Registry: "example.test"}); err == nil {
		t.Error("Init with no resolved workspace: got nil error, want a refusal")
	} else if !strings.Contains(err.Error(), "no resolved workspace supplied") {
		t.Errorf("Init refusal = %q; want it to name the missing resolution", err)
	}
	if _, err := configops.Capture(nil, configops.CaptureOptions{}); err == nil {
		t.Error("Capture with no resolved workspace: got nil error, want a refusal")
	} else if !strings.Contains(err.Error(), "no resolved workspace supplied") {
		t.Errorf("Capture refusal = %q; want it to name the missing resolution", err)
	}
}

const captureWorkspaceConfig = `offline: true
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

// TestCaptureReportsTheSocketsItFroze pins the tier axis, which is the whole of
// what the default narrows.
//
// The governance socket is the row that matters: its tier is a property of each
// attachment's role, so a default freeze takes the label-transform attachment and
// leaves the verdict one — and a freeze that took both would carry a gate meant to
// vary by environment into every checkout of the project.
func TestCaptureReportsTheSocketsItFroze(t *testing.T) {
	for _, tc := range []struct {
		name       string
		allPlugins bool
		// wantSockets is field name → the attachment URIs expected under it, in
		// order, so a missing or extra attachment is named rather than counted.
		wantSockets [][2]string
	}{
		{
			name:       "the default freezes only what determines the output bytes",
			allPlugins: false,
			wantSockets: [][2]string{
				{"providers", "oci://example.test/plugin/provider/helm-chart:1.0.0"},
				{"governance", "oci://example.test/plugin/governance/label-injector:1.0.0"},
			},
		},
		{
			name:       "widening adds the verdict gate and the linter",
			allPlugins: true,
			wantSockets: [][2]string{
				{"providers", "oci://example.test/plugin/provider/helm-chart:1.0.0"},
				{"governance", "oci://example.test/plugin/governance/label-injector:1.0.0"},
				{"governance", "oci://example.test/plugin/governance/gate:1.0.0"},
				{"linter", "oci://example.test/plugin/linter/publishing:1.0.0"},
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rw, wsPath, _ := workspaceAt(t, captureWorkspaceConfig, "offline: true\n")
			res, err := configops.Capture(rw, configops.CaptureOptions{AllPlugins: tc.allPlugins})
			if err != nil {
				t.Fatalf("Capture: %v", err)
			}
			if res.Path != wsPath {
				t.Errorf("Path = %q; want this workspace's own file %q", res.Path, wsPath)
			}
			if !res.Captured() {
				t.Error("Captured() = false; want true, the workspace carries build-critical bindings")
			}
			var got [][2]string
			for _, cs := range res.Sockets {
				for _, ref := range cs.Refs {
					got = append(got, [2]string{cs.Field, ref.URI})
				}
			}
			if len(got) != len(tc.wantSockets) {
				t.Fatalf("froze %d attachment(s), want %d:\n got %v\nwant %v",
					len(got), len(tc.wantSockets), got, tc.wantSockets)
			}
			for i := range got {
				if got[i] != tc.wantSockets[i] {
					t.Errorf("attachment %d = %v; want %v", i, got[i], tc.wantSockets[i])
				}
			}
		})
	}
}

// TestCaptureNamesTheSocketAndTheKeySeparately pins the one place the two come
// apart. Every other socket is written under its own name; the provider socket is
// written under a plural key, so a consumer told only one of the two would either
// name a socket the file does not mention or a key the socket vocabulary does not.
func TestCaptureNamesTheSocketAndTheKeySeparately(t *testing.T) {
	rw, _, _ := workspaceAt(t, captureWorkspaceConfig, "offline: true\n")
	res, err := configops.Capture(rw, configops.CaptureOptions{})
	if err != nil {
		t.Fatalf("Capture: %v", err)
	}
	found := false
	for _, cs := range res.Sockets {
		if cs.Socket != workspaceconfig.SocketProvider {
			if string(cs.Socket) != cs.Field {
				t.Errorf("socket %q: field = %q; want them equal for every socket but the provider one",
					cs.Socket, cs.Field)
			}
			continue
		}
		found = true
		if cs.Field != "providers" {
			t.Errorf("the provider socket's field = %q; want %q, the key the file carries", cs.Field, "providers")
		}
	}
	if !found {
		t.Fatal("no provider socket in the frozen set; the fixture was meant to carry one")
	}
}

// TestCaptureMaterializesAGlobalOnlyBinding is what the operation exists for: a
// binding a build depends on that only the machine's configuration supplies is
// written into the workspace, so the project builds the same elsewhere.
//
// It asserts against the FILE rather than only the result, because a result that
// named the binding while the write missed it would be the one failure a report
// cannot show.
func TestCaptureMaterializesAGlobalOnlyBinding(t *testing.T) {
	const globalOnly = `offline: true
plugins:
    providers:
        - capability: terraform
          uri: oci://example.test/plugin/provider/terraform:2.0.0
`
	// 🔴 The workspace layer declares no providers on purpose. A socket list is
	// REPLACED by a nearer layer rather than concatenated with it — concatenation
	// is what the separate append keys are for — so a workspace declaring its own
	// providers would override the global list entirely and leave nothing
	// global-only for this test to be about.
	const workspaceWithoutProviders = `offline: true
workspace:
    catalog_dirs:
        - ./catalog
plugins:
    governance:
        - uri: oci://example.test/plugin/governance/label-injector:1.0.0
          role: label-transform
`
	rw, wsPath, _ := workspaceAt(t, workspaceWithoutProviders, globalOnly)
	res, err := configops.Capture(rw, configops.CaptureOptions{})
	if err != nil {
		t.Fatalf("Capture: %v", err)
	}
	written, err := os.ReadFile(wsPath)
	if err != nil {
		t.Fatalf("read the frozen file: %v", err)
	}
	if !strings.Contains(string(written), "terraform:2.0.0") {
		t.Errorf("the frozen file does not carry the global-only provider binding:\n%s", written)
	}
	reported := false
	for _, cs := range res.Sockets {
		for _, ref := range cs.Refs {
			if ref.Capability == "terraform" {
				reported = true
			}
		}
	}
	if !reported {
		t.Error("the result does not name the global-only binding the write froze")
	}
}

// TestCaptureIsIdempotent pins the byte-for-byte claim the operation's own
// documentation makes, and it is asserted on the bytes rather than on a
// changed flag, because a second write producing different bytes is exactly what
// a client reloading the document would see.
func TestCaptureIsIdempotent(t *testing.T) {
	rw, wsPath, _ := workspaceAt(t, captureWorkspaceConfig, "offline: true\n")
	if _, err := configops.Capture(rw, configops.CaptureOptions{}); err != nil {
		t.Fatalf("first Capture: %v", err)
	}
	first, err := os.ReadFile(wsPath)
	if err != nil {
		t.Fatalf("read after the first freeze: %v", err)
	}
	if _, err := configops.Capture(rw, configops.CaptureOptions{}); err != nil {
		t.Fatalf("second Capture: %v", err)
	}
	second, err := os.ReadFile(wsPath)
	if err != nil {
		t.Fatalf("read after the second freeze: %v", err)
	}
	if string(first) != string(second) {
		t.Errorf("freezing twice moved the bytes:\nfirst:\n%s\nsecond:\n%s", first, second)
	}
}

// TestCaptureSaysSoWhenThereIsNothingBuildCritical covers the empty answer, which
// is a real answer rather than a failure — and the distinction is the whole point,
// because a caller that read it as a failure would report a healthy workspace as
// broken.
func TestCaptureSaysSoWhenThereIsNothingBuildCritical(t *testing.T) {
	rw, _, _ := workspaceAt(t, "offline: true\nworkspace: {}\n", "offline: true\n")
	res, err := configops.Capture(rw, configops.CaptureOptions{})
	if err != nil {
		t.Fatalf("Capture: %v", err)
	}
	if res.Captured() {
		t.Error("Captured() = true; want false, nothing in this workspace is build-critical")
	}
	if len(res.Sockets) != 0 || len(res.CatalogDirs) != 0 {
		t.Errorf("froze something after all: sockets=%v catalogDirs=%v", res.Sockets, res.CatalogDirs)
	}
	if res.Path == "" {
		t.Error("Path is empty; a caller is owed the file even where nothing was frozen")
	}
}

// TestInitRefusesAnExistingConfigurationFile drives the guard that is the whole of
// what seeding decides on top of the registration it delegates to. The shared
// writer merges into an existing document by design, so without this guard seeding
// would silently edit a configuration somebody is already working from.
func TestInitRefusesAnExistingConfigurationFile(t *testing.T) {
	rw, _, globalPath := workspaceAt(t, "offline: true\nworkspace: {}\n", "offline: true\n")
	before, err := os.ReadFile(globalPath)
	if err != nil {
		t.Fatalf("read the seeded global file: %v", err)
	}

	// The refusal is held in its own variable: an intervening read that reassigned
	// err would leave the assertions below inspecting that read's nil instead.
	_, refusal := configops.Init(t.Context(), rw, configops.InitOptions{Registry: "example.test"})
	if refusal == nil {
		t.Fatal("Init over an existing file: got nil error, want a refusal")
	}
	if !strings.Contains(refusal.Error(), "already exists") || !strings.Contains(refusal.Error(), globalPath) {
		t.Errorf("refusal = %q; want it to name the file and what to pass", refusal)
	}

	after, err := os.ReadFile(globalPath)
	if err != nil {
		t.Fatalf("read the global file after the refusal: %v", err)
	}
	if string(before) != string(after) {
		t.Errorf("a refused seeding changed the file:\nbefore:\n%s\nafter:\n%s", before, after)
	}

	// 🔴 The condition is recognisable without reading the sentence, which is what
	// lets a surface with another vocabulary re-word the refusal instead of keeping
	// a second existence guard of its own. Matching the message instead would keep
	// compiling and silently stop matching the first time the wording moved.
	var existing *configops.ConfigFileExistsError
	if !errors.As(refusal, &existing) {
		t.Fatalf("the refusal is not recognisable as the file-exists condition: %#v", refusal)
	}
	if existing.Path() != globalPath {
		t.Errorf("Path() = %q; want the file the guard protected, %q", existing.Path(), globalPath)
	}
}

// TestInitReportsWithoutWriting drives the reporting delivery to its own effect:
// nothing is created, and the content returned is the document a write would have
// produced.
//
// The absent-file assertion is the load-bearing half. A reporting delivery that
// created the file anyway would still return the right content, so a test reading
// only the result would pass over exactly the defect that matters.
func TestInitReportsWithoutWriting(t *testing.T) {
	root := t.TempDir()
	wsDir := filepath.Join(root, "ws")
	if err := os.MkdirAll(wsDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(wsDir, workspaceconfig.ConfigFilename),
		[]byte("offline: true\nworkspace: {}\n"), 0o644); err != nil {
		t.Fatalf("write workspace config: %v", err)
	}
	globalPath := filepath.Join(root, "global", workspaceconfig.ConfigFilename)
	if err := os.MkdirAll(filepath.Dir(globalPath), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	lookup := frozenLookup(map[string]string{workspaceconfig.EnvConfigOverride: globalPath})
	rw, err := workspaceconfig.ResolveWithoutGlobal("", wsDir, lookup)
	if err != nil {
		t.Fatalf("resolve workspace: %v", err)
	}

	// The hub is seeded because the workspace is offline: any other registry has
	// no policy to pin offline and is refused, which is not this test's subject.
	res, err := configops.Init(t.Context(), rw, configops.InitOptions{
		Registry: workspaceconfig.HubBaselineHost,
		Delivery: authoredcontent.DeliveryFor(true),
	})
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	if _, statErr := os.Stat(globalPath); !os.IsNotExist(statErr) {
		t.Errorf("a reporting seeding created %s; it must write nothing", globalPath)
	}
	if len(res.Registration.Content) == 0 {
		t.Error("no content reported; a reporting delivery owes the document a write would produce")
	}
	if !strings.Contains(string(res.Registration.Content), workspaceconfig.HubBaselineHost) {
		t.Errorf("reported content does not carry the seeded registry:\n%s", res.Registration.Content)
	}
	if res.Registration.Target.Path != globalPath {
		t.Errorf("Target.Path = %q; want the file the content is for, %q",
			res.Registration.Target.Path, globalPath)
	}
}

// TestInitWritesAndReportsItsReach covers the two write targets and the reach each
// resolves to. The reach is the fact a caller cannot recover for itself: seeding
// with no override writes the file every workspace on the machine reads through,
// and an answer that said nothing would be read as being about this workspace.
func TestInitWritesAndReportsItsReach(t *testing.T) {
	for _, tc := range []struct {
		name          string
		workspaceFlag bool
		wantMachine   bool
	}{
		{name: "with no override the reach is the whole machine", workspaceFlag: false, wantMachine: true},
		{name: "the workspace override keeps it local", workspaceFlag: true, wantMachine: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			wsDir := filepath.Join(root, "ws")
			if err := os.MkdirAll(wsDir, 0o755); err != nil {
				t.Fatalf("mkdir: %v", err)
			}
			wsPath := filepath.Join(wsDir, workspaceconfig.ConfigFilename)
			if err := os.WriteFile(wsPath, []byte("offline: true\nworkspace: {}\n"), 0o644); err != nil {
				t.Fatalf("write workspace config: %v", err)
			}
			globalPath := filepath.Join(root, "global", workspaceconfig.ConfigFilename)
			if err := os.MkdirAll(filepath.Dir(globalPath), 0o755); err != nil {
				t.Fatalf("mkdir: %v", err)
			}
			lookup := frozenLookup(map[string]string{workspaceconfig.EnvConfigOverride: globalPath})
			rw, err := workspaceconfig.ResolveWithoutGlobal("", wsDir, lookup)
			if err != nil {
				t.Fatalf("resolve workspace: %v", err)
			}

			res, err := configops.Init(t.Context(), rw, configops.InitOptions{
				// Offline, only the hub can be seeded; see TestInitReportsWithoutWriting.
				Registry:  workspaceconfig.HubBaselineHost,
				Workspace: tc.workspaceFlag,
				// The workspace file exists in both rows, so the local target needs
				// force; the guard itself is covered by its own test above.
				Force: true,
			})
			if err != nil {
				t.Fatalf("Init: %v", err)
			}
			gotMachine := res.Registration.Target.Scope == registryops.ScopeMachine
			if gotMachine != tc.wantMachine {
				t.Errorf("scope = %q; want machine-wide = %v",
					res.Registration.Target.Scope, tc.wantMachine)
			}
			wantPath := globalPath
			if tc.workspaceFlag {
				wantPath = wsPath
			}
			if res.Registration.Target.Path != wantPath {
				t.Errorf("Target.Path = %q; want %q", res.Registration.Target.Path, wantPath)
			}
			written, err := os.ReadFile(wantPath)
			if err != nil {
				t.Fatalf("read the seeded file: %v", err)
			}
			if !strings.Contains(string(written), workspaceconfig.HubBaselineHost) {
				t.Errorf("the seeded file does not carry the registry:\n%s", written)
			}
		})
	}
}

// TestInitRefusesBothWriteTargetOverrides pins that the usage error comes from the
// shared write-target policy rather than from a comparison written here, and that
// it fires before anything is written.
func TestInitRefusesBothWriteTargetOverrides(t *testing.T) {
	rw, _, _ := workspaceAt(t, "offline: true\nworkspace: {}\n", "offline: true\n")
	_, err := configops.Init(t.Context(), rw, configops.InitOptions{
		Registry:  "example.test",
		Global:    true,
		Workspace: true,
	})
	if err == nil {
		t.Fatal("Init with both overrides: got nil error, want a refusal")
	}
	if !strings.Contains(err.Error(), "cannot be combined") {
		t.Errorf("refusal = %q; want the write-target policy's own message", err)
	}
}
