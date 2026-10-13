// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

// Where an import lands its drafts, compared across every surface that carries
// the capability, each driven as a spawned process of the built binary over its
// own wire: the command line, the assistant's in-process import tool and its
// command tool over the tool protocol, and the editor's language server.
//
// Nothing here points a test seam. A spawned binary cannot take one, and the
// command tool re-runs the binary in a process of its own, so the importer is
// reached the way a workspace reaches one: the catalog advertises it, and its
// module is read from the workspace's content cache.

import (
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/aggrega-ai/aggrega-cli/internal/wasmtest"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// cachedImportLayout is one row's fixture: a workspace whose catalog advertises
// the demo importer and whose content cache holds its module, a sub-folder of
// it, and another folder beside it. Every path is absolute and sits beneath
// parent, so one fingerprint of parent sees everything a run could touch.
type cachedImportLayout struct {
	parent, root, sub, other string
}

// demoImporterEntity is the Plugin entity the fixture's catalog advertises the
// demo importer through. Its coordinate is what the module is cached under.
const demoImporterEntity = `apiVersion: aggrega.ai/v1alpha1
kind: Plugin
metadata:
  name: demo-importer
  version: 1.0.0
  pluginType: importer
spec:
  abiVersion: v1alpha1
  importer:
    capabilities:
      - import:demo
`

// layCachedImport builds the fixture a row runs in. The module is written
// through the content cache's own writer, so where it lands is the cache's one
// spelling of the slot rather than a second one written here.
func layCachedImport(t *testing.T) cachedImportLayout {
	t.Helper()
	parent := t.TempDir()
	l := cachedImportLayout{parent: parent, root: filepath.Join(parent, "ws"), other: filepath.Join(parent, "other")}
	l.sub = filepath.Join(l.root, "sub")
	for _, dir := range []string{l.sub, l.other} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("create %s: %v", dir, err)
		}
	}
	writeFixture(t, filepath.Join(l.root, workspaceconfig.ConfigFilename), "workspace: {}\n")
	writeFixture(t, filepath.Join(l.root, workspaceconfig.ConventionalCatalogDir, "demo-importer.yaml"), demoImporterEntity)
	writeFixture(t, filepath.Join(l.root, demoImportSourceName), demoSourceBody)
	module := wasmtest.EnsureFixtureBytes(t, "import-go.wasm")
	cacheDir := workspaceconfig.Config{}.ResolvedArtifactCacheDir()
	if err := oci.WriteCached(l.root, cacheDir, types.PluginCoordinate("importer", "demo-importer"), "", module); err != nil {
		t.Fatalf("cache the demo importer's module: %v", err)
	}
	return l
}

// importParityEnv is the environment a spawned binary runs under here: offline,
// since nothing concerns a registry, and with the workspace root named by the
// variable only where launchRoot is non-empty. An inherited value is dropped
// first, so a row naming no root runs as a launch naming none.
func importParityEnv(t *testing.T, launchRoot string) []string {
	t.Helper()
	var env []string
	for _, entry := range builtBinaryEnv(t, "AGGREGA_OFFLINE=1") {
		if name, _, _ := strings.Cut(entry, "="); name == workspaceconfig.EnvWorkspaceRoot {
			continue
		}
		env = append(env, entry)
	}
	if launchRoot != "" {
		env = append(env, workspaceconfig.EnvWorkspaceRoot+"="+launchRoot)
	}
	return env
}

// importViaCommandLine runs the built binary with args in dir.
func importViaCommandLine(t *testing.T, bin, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command(bin, args...)
	cmd.Dir = dir
	cmd.Env = importParityEnv(t, "")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("aggrega %s in %s: %v\n%s", strings.Join(args, " "), dir, err, out)
	}
}

// importViaToolProtocol launches the built binary as `aggrega mcp` in dir, with
// the workspace root named by the launch where launchRoot is non-empty, calls one
// tool and fails the test unless the call succeeded.
func importViaToolProtocol(t *testing.T, bin, dir, launchRoot, tool string, args map[string]any) {
	t.Helper()
	params, err := json.Marshal(map[string]any{"name": tool, "arguments": args})
	if err != nil {
		t.Fatalf("encode the call: %v", err)
	}
	cmd := exec.Command(bin, "mcp")
	cmd.Dir = dir
	cmd.Env = importParityEnv(t, launchRoot)
	cmd.Stdin = strings.NewReader(rpcLine(1, "initialize", `{"protocolVersion":"2025-06-18"}`) +
		rpcLine(2, "tools/call", string(params)))
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("aggrega mcp: %v (stderr: %s)", err, stderr.String())
	}
	frame := answersByID(t, stdout.String(), 1, 2)[2]
	if frame.Error != nil {
		t.Fatalf("tools/call %s: error %d: %s", tool, frame.Error.Code, frame.Error.Message)
	}
	var call struct {
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
		IsError bool `json:"isError"`
	}
	if err := json.Unmarshal(frame.Result, &call); err != nil {
		t.Fatalf("decode the %s answer: %v (%s)", tool, err, frame.Result)
	}
	if call.IsError {
		t.Fatalf("%s was refused: %+v", tool, call.Content)
	}
}

// importViaLanguageServer starts the built binary as `aggrega serve` at the
// workspace root and sends one aggrega/import naming the output directory ".",
// and holds the answer's decisions to an empty list.
func importViaLanguageServer(t *testing.T, bin, root string) {
	t.Helper()
	cmd := exec.Command(bin, "serve")
	cmd.Dir = root
	cmd.Env = importParityEnv(t, "")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatalf("language server input: %v", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("language server output: %v", err)
	}
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatalf("start the language server: %v", err)
	}
	if _, err := stdin.Write(framedFor(t, cmd, 1, "aggrega/import", map[string]any{
		"source":    demoImportSourceName,
		"outputDir": ".",
		"format":    "demo",
	})); err != nil {
		t.Fatalf("write the request: %v", err)
	}
	resp := readOneResponse(t, stdout, responseLivenessCeiling(t))
	_ = stdin.Close()
	// The rest of the output is drained before waiting, because Wait closes the
	// pipe it is read from; both run off this goroutine so the ceiling below
	// bounds a server that never exits.
	waited := make(chan error, 1)
	go func() {
		_, _ = io.Copy(io.Discard, stdout)
		waited <- cmd.Wait()
	}()
	select {
	case <-waited:
	case <-time.After(responseLivenessCeiling(t)):
		_ = cmd.Process.Kill()
		t.Fatalf("the language server did not exit once its input closed (stderr: %s)", stderr.String())
	}
	if failure, refused := resp["error"]; refused {
		t.Fatalf("aggrega/import was refused: %+v (stderr: %s)", failure, stderr.String())
	}
	// The demo importer reports no mapping decisions, so the answer is the case of
	// an importer with none: decisions is still there, as an empty list rather than
	// an absent key or a null.
	result, _ := resp["result"].(map[string]any)
	decisions, present := result["decisions"]
	if list, isList := decisions.([]any); !present || !isList || len(list) != 0 {
		t.Errorf("an importer reporting no decisions answered decisions as %#v (present=%v); want an empty list",
			decisions, present)
	}
}

// TestEverySurfaceLandsAnImportAtTheWorkspaceRoot drives one import over one
// source through every surface that carries it, each over a fresh copy of the
// fixture, and reads where the drafts landed off the content digests of the
// whole fixture: the command line from a sub-folder and from another folder
// naming the workspace with --workspace-root; the in-process import tool with
// the workspace named by the launch and by the call; the import command's tool,
// whose command starts in the root the launch names, or, with none named, in the
// folder the server was started in inside the workspace, where its own
// resolution finds the root; and the language server, sent the output directory
// ".". Each names no destination of its own except the language server, whose
// "." is the workspace root.
//
// Every one of them lands the same draft, byte for byte, at the workspace root,
// and changes nothing else. One more row passes the command tool its generated
// output_dir argument, which reaches the command's --output-dir flag and lands
// the draft beneath that directory, resolved in the folder the command runs in —
// here the root the launch names.
//
// 🔴 PLANTED DEFECTS:
//   - have outputDirFor (internal/importops/import.go) return "." for an empty
//     name: the command-line rows land in ws/sub and other, the import-tool rows
//     in other, and the command tool's no-root row in ws/sub, so each reddens.
//   - have the command hand OutputDir "." again (cmd/aggrega/import.go): the
//     command-line rows and the command tool's no-root row redden.
//   - have the import tool hand the layer
//     OutputDir: tw.root + string(os.PathSeparator) + "drafts"
//     (internal/mcp/tool_import.go, the importops.Options literal): both
//     import-tool rows redden, landing in ws/drafts.
func TestEverySurfaceLandsAnImportAtTheWorkspaceRoot(t *testing.T) {
	t.Parallel()
	bin := buildServeBinary(t)

	atTheRoot := []string{"ws/demo.yaml was created"}
	for _, row := range []struct {
		name        string
		run         func(t *testing.T, l cachedImportLayout)
		wantChanges []string
		wantDraft   string
	}{
		{
			name: "the command line from a sub-folder",
			run: func(t *testing.T, l cachedImportLayout) {
				importViaCommandLine(t, bin, l.sub, "import", filepath.Join(l.root, demoImportSourceName), "--format", "demo")
			},
			wantChanges: atTheRoot, wantDraft: "ws/demo.yaml",
		},
		{
			name: "the command line from another folder, the workspace named with --workspace-root",
			run: func(t *testing.T, l cachedImportLayout) {
				importViaCommandLine(t, bin, l.other, "--workspace-root", l.root,
					"import", filepath.Join(l.root, demoImportSourceName), "--format", "demo")
			},
			wantChanges: atTheRoot, wantDraft: "ws/demo.yaml",
		},
		{
			name: "the import tool, the workspace named by the launch",
			run: func(t *testing.T, l cachedImportLayout) {
				importViaToolProtocol(t, bin, l.other, l.root, "import",
					map[string]any{"source": demoImportSourceName, "format": "demo"})
			},
			wantChanges: atTheRoot, wantDraft: "ws/demo.yaml",
		},
		{
			name: "the import tool, the workspace named by the call",
			run: func(t *testing.T, l cachedImportLayout) {
				importViaToolProtocol(t, bin, l.other, "", "import", map[string]any{
					"source": filepath.Join(l.root, demoImportSourceName), "format": "demo", "workspace_root": l.root,
				})
			},
			wantChanges: atTheRoot, wantDraft: "ws/demo.yaml",
		},
		{
			name: "the import command's tool, its command started in the root the launch names",
			run: func(t *testing.T, l cachedImportLayout) {
				importViaToolProtocol(t, bin, l.other, l.root, "cli-import",
					map[string]any{"args": []string{demoImportSourceName}, "format": "demo"})
			},
			wantChanges: atTheRoot, wantDraft: "ws/demo.yaml",
		},
		{
			name: "the import command's tool, no root named, the server started in a sub-folder of the workspace",
			run: func(t *testing.T, l cachedImportLayout) {
				importViaToolProtocol(t, bin, l.sub, "", "cli-import",
					map[string]any{"args": []string{"../" + demoImportSourceName}, "format": "demo"})
			},
			wantChanges: atTheRoot, wantDraft: "ws/demo.yaml",
		},
		{
			name: "the language server, sent the output directory \".\"",
			run: func(t *testing.T, l cachedImportLayout) {
				importViaLanguageServer(t, bin, l.root)
			},
			wantChanges: atTheRoot, wantDraft: "ws/demo.yaml",
		},
		{
			name: "the import command's tool, its generated output_dir argument reaching --output-dir",
			run: func(t *testing.T, l cachedImportLayout) {
				importViaToolProtocol(t, bin, l.other, l.root, "cli-import", map[string]any{
					"args": []string{demoImportSourceName}, "format": "demo", "output_dir": "drafts",
				})
			},
			wantChanges: []string{"ws/drafts was created", "ws/drafts/demo.yaml was created"},
			wantDraft:   "ws/drafts/demo.yaml",
		},
	} {
		t.Run(row.name, func(t *testing.T) {
			l := layCachedImport(t)
			before := treeFingerprint(t, l.parent)
			row.run(t, l)
			changes := fingerprintDifferences(before, treeFingerprint(t, l.parent))
			if !slices.Equal(changes, row.wantChanges) {
				t.Errorf("the import changed the fixture as %v; want exactly %v", changes, row.wantChanges)
			}
			body, err := os.ReadFile(filepath.Join(l.parent, filepath.FromSlash(row.wantDraft)))
			if err != nil {
				t.Fatalf("the draft is not at %s: %v", row.wantDraft, err)
			}
			if string(body) != demoEchoBody {
				t.Errorf("%s holds %q; want the bytes every surface drafts, %q", row.wantDraft, body, demoEchoBody)
			}
		})
	}
}
