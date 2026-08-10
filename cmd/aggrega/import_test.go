// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/importersocket"
	"github.com/aggrega-ai/aggrega-cli/internal/wasm"
	"github.com/aggrega-ai/aggrega-cli/internal/wasmtest"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// TestDetectFormat exercises the format-selection contract: the Langflow
// fixture's nested data.nodes shape resolves to "langflow"; the Flowise
// fixture's flat nodes+edges shape resolves to "flowise"; a JSON shape that
// matches neither halts with a loud diagnostic rather than guessing; and an
// explicit --format wins over whatever the content would detect.
func TestDetectFormat(t *testing.T) {
	t.Parallel()

	langflow := mustReadFixture(t, "langflow-flow.json")
	flowise := mustReadFixture(t, "flowise-chatflow.json")

	cases := []struct {
		name     string
		explicit string
		path     string
		content  []byte
		want     string
		wantErr  bool
	}{
		{name: "langflow nested data.nodes", path: "flow.json", content: langflow, want: "langflow"},
		{name: "flowise flat nodes+edges", path: "chatflow.json", content: flowise, want: "flowise"},
		{name: "ambiguous json is a loud fall-through", path: "x.json", content: []byte(`{"foo":"bar"}`), wantErr: true},
		{name: "garbage is a loud fall-through", path: "x.json", content: []byte("not json at all"), wantErr: true},
		{name: "top-level array is a loud fall-through", path: "x.json", content: []byte(`[1,2,3]`), wantErr: true},
		{name: "explicit format wins over langflow content", explicit: "flowise", path: "flow.json", content: langflow, want: "flowise"},
		{name: "explicit format wins over garbage content", explicit: "langflow", path: "x.json", content: []byte("not json"), want: "langflow"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := selectImportFormat(tc.explicit, tc.path, tc.content)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("selectImportFormat(%q, %q, ...) = %q, nil; want error", tc.explicit, tc.path, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("selectImportFormat(%q, %q, ...): unexpected error: %v", tc.explicit, tc.path, err)
			}
			if got != tc.want {
				t.Fatalf("selectImportFormat(%q, %q, ...) = %q; want %q", tc.explicit, tc.path, got, tc.want)
			}
		})
	}
}

// stubImporterResolver is the direct-attach stand-in for the demo importer: it
// returns the demo importer entity for any requested format, mirroring the
// direct-attach production path (which attaches one importer without selecting
// by capability). It stands in for the workspace-config seam so the end-to-end
// test drives the real import socket with the demo guest.
type stubImporterResolver struct{}

func (stubImporterResolver) ImporterPluginForFormat(string) (types.Plugin, error) {
	return types.Plugin{
		Metadata: types.PluginMetadata{Name: "demo-importer", Version: "1.0.0", PluginType: "importer"},
		Spec:     types.PluginSpec{Importer: &types.PluginImporter{Capabilities: []string{"import:demo"}}},
	}, nil
}

// demoImporterLoader loads the demo importer guest from a built fixture. The
// demo guest echoes its source directly and never draws on the parser seam, so
// the command tests below pass nil parser resolver and loader; the parser seam
// is exercised at the import-socket level rather than through this guest.
func demoImporterLoader(fixturePath string) importersocket.ImporterLoader {
	return func(p types.Plugin) (wasm.PluginSpec, error) {
		return wasm.PluginSpec{Name: p.Coordinate(), Path: fixturePath}, nil
	}
}

// demoEchoBody is the body the demo importer echoes for the "demo" format — one
// drafted Component whose name is the requested format — and the content of the
// matching scaffold file. It mirrors the guest at aggrega-pdk/go/examples/import.
const demoEchoBody = "kind: Component\nmetadata:\n  name: demo\n"

// TestImportCommand_EndToEnd drives the command's inner path with the demo
// importer guest: resolve → load → run the import socket, then write the
// drafted files to a temp directory. It asserts the demo guest's echoed
// scaffold file lands on disk with the expected content.
func TestImportCommand_EndToEnd(t *testing.T) {
	t.Parallel()
	fixture := wasmtest.EnsureFixtureBuilt(t, "import-go.wasm")
	load := demoImporterLoader(fixture)

	tmp := t.TempDir()
	var out, errOut bytes.Buffer
	err := runImport(context.Background(), stubImporterResolver{}, load, nil, nil, mustReadFixture(t, "demo-source.txt"), importOptions{
		format:  "demo",
		stdout:  false,
		workDir: tmp,
		out:     &out,
		errOut:  &errOut,
	})
	if err != nil {
		t.Fatalf("runImport: %v", err)
	}

	got, readErr := os.ReadFile(filepath.Join(tmp, "demo.yaml"))
	if readErr != nil {
		t.Fatalf("read written demo.yaml: %v", readErr)
	}
	if string(got) != demoEchoBody {
		t.Fatalf("written demo.yaml = %q; want %q", string(got), demoEchoBody)
	}
	if !bytes.Contains(out.Bytes(), []byte("wrote demo.yaml")) {
		t.Fatalf("stdout = %q; want a 'wrote demo.yaml' summary line", out.String())
	}
}

// TestImportCommand_Stdout diverts the drafted files to the output sink with
// --stdout: nothing is written to disk, and the sink carries the deterministic
// "# path" header plus the file content.
func TestImportCommand_Stdout(t *testing.T) {
	t.Parallel()
	fixture := wasmtest.EnsureFixtureBuilt(t, "import-go.wasm")
	load := demoImporterLoader(fixture)

	tmp := t.TempDir()
	var out, errOut bytes.Buffer
	err := runImport(context.Background(), stubImporterResolver{}, load, nil, nil, mustReadFixture(t, "demo-source.txt"), importOptions{
		format:  "demo",
		stdout:  true,
		workDir: tmp,
		out:     &out,
		errOut:  &errOut,
	})
	if err != nil {
		t.Fatalf("runImport: %v", err)
	}

	if _, statErr := os.Stat(filepath.Join(tmp, "demo.yaml")); !os.IsNotExist(statErr) {
		t.Fatalf("demo.yaml must not be written to disk under --stdout; stat err = %v", statErr)
	}
	want := "# demo.yaml\n" + demoEchoBody
	if out.String() != want {
		t.Fatalf("stdout = %q; want %q", out.String(), want)
	}
}

// TestImportCommand_SettingsThreadedToGuest proves the with.settings path
// end-to-end: target_namespace flows from an attachment's with-config through
// runImport into the guest, which prefixes the emitted entity name with it — so
// the drafted file lands under the prefixed name rather than the bare format.
func TestImportCommand_SettingsThreadedToGuest(t *testing.T) {
	t.Parallel()
	fixture := wasmtest.EnsureFixtureBuilt(t, "import-go.wasm")
	load := demoImporterLoader(fixture)

	_, settings, err := importWithInputs(&workspaceconfig.PluginRefWith{
		Settings: map[string]any{"target_namespace": "prod"},
	})
	if err != nil {
		t.Fatalf("importWithInputs: %v", err)
	}

	tmp := t.TempDir()
	var out, errOut bytes.Buffer
	err = runImport(context.Background(), stubImporterResolver{}, load, nil, nil, mustReadFixture(t, "demo-source.txt"), importOptions{
		format:   "demo",
		workDir:  tmp,
		out:      &out,
		errOut:   &errOut,
		settings: settings,
	})
	if err != nil {
		t.Fatalf("runImport: %v", err)
	}

	want := "kind: Component\nmetadata:\n  name: prod-demo\n"
	got, rerr := os.ReadFile(filepath.Join(tmp, "prod-demo.yaml"))
	if rerr != nil {
		t.Fatalf("read prod-demo.yaml (target_namespace must prefix the entity name): %v", rerr)
	}
	if string(got) != want {
		t.Fatalf("written prod-demo.yaml = %q; want %q", string(got), want)
	}
	// The bare-format file must NOT be written when the namespace prefix applies.
	if _, statErr := os.Stat(filepath.Join(tmp, "demo.yaml")); !os.IsNotExist(statErr) {
		t.Fatalf("demo.yaml must not exist when target_namespace prefixes the name; stat err = %v", statErr)
	}
}

// TestImportCommand_SourcesThreadedToGuest proves the with.sources path
// end-to-end: multiple glob-matched files are read into name-sorted named
// sources and threaded into the guest, which reflects each source name back as a
// diagnostic — confirming the host delivered every source to the importer.
func TestImportCommand_SourcesThreadedToGuest(t *testing.T) {
	t.Parallel()
	fixture := wasmtest.EnsureFixtureBuilt(t, "import-go.wasm")
	load := demoImporterLoader(fixture)

	srcDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(srcDir, "beta.json"), []byte(`{"b":2}`), 0o644); err != nil {
		t.Fatalf("write beta.json: %v", err)
	}
	if err := os.WriteFile(filepath.Join(srcDir, "alpha.json"), []byte(`{"a":1}`), 0o644); err != nil {
		t.Fatalf("write alpha.json: %v", err)
	}

	sources, _, err := importWithInputs(&workspaceconfig.PluginRefWith{
		Sources: []string{filepath.Join(srcDir, "*.json")},
	})
	if err != nil {
		t.Fatalf("importWithInputs: %v", err)
	}
	if len(sources) != 2 {
		t.Fatalf("sources = %+v; want two named sources", sources)
	}
	if sources[0].Name != "alpha.json" || sources[1].Name != "beta.json" {
		t.Fatalf("sources must be base-name sorted; got %q, %q", sources[0].Name, sources[1].Name)
	}

	tmp := t.TempDir()
	var out, errOut bytes.Buffer
	err = runImport(context.Background(), stubImporterResolver{}, load, nil, nil, mustReadFixture(t, "demo-source.txt"), importOptions{
		format:  "demo",
		workDir: tmp,
		out:     &out,
		errOut:  &errOut,
		sources: sources,
	})
	if err != nil {
		t.Fatalf("runImport: %v", err)
	}
	if diag := errOut.String(); !strings.Contains(diag, "received source: alpha.json") || !strings.Contains(diag, "received source: beta.json") {
		t.Fatalf("guest did not reflect both sources; errOut = %q", diag)
	}
}

// TestImportAttachmentInputs_ConsumerScoped proves consumer-scoping: with two
// importer attachments serving different formats, the attachment selected for a
// format supplies only its own with-config — another attachment's settings never
// leak into the invoked importer's input.
func TestImportAttachmentInputs_ConsumerScoped(t *testing.T) {
	t.Parallel()
	refs := []workspaceconfig.PluginRef{
		{Capability: "import:demo", With: &workspaceconfig.PluginRefWith{Settings: map[string]any{"target_namespace": "demo-ns"}}},
		{Capability: "import:other", With: &workspaceconfig.PluginRefWith{Settings: map[string]any{"target_namespace": "other-ns"}}},
	}

	ref, ok := selectImporterAttachment(refs, "demo")
	if !ok {
		t.Fatal("selectImporterAttachment(demo): no attachment selected")
	}
	_, settings, err := importWithInputs(ref.With)
	if err != nil {
		t.Fatalf("importWithInputs: %v", err)
	}
	s := string(settings)
	if !strings.Contains(s, "demo-ns") {
		t.Fatalf("selected attachment's settings missing; settings = %q", s)
	}
	if strings.Contains(s, "other-ns") {
		t.Fatalf("another attachment's settings leaked into the demo invocation; settings = %q", s)
	}
}

func mustReadFixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	return b
}
