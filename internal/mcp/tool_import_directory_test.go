// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package mcp

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/importops"
	"github.com/aggrega-ai/aggrega-cli/internal/importops/importopstest"
	"github.com/aggrega-ai/aggrega-cli/internal/wasm"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/readscope"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// What the import tool does with a source that is a directory, on both
// transports: it hands the path it resolved to the import, which reads the files
// the importer's patterns select, and a link inside the directory leading out of
// it is refused — over a listener in this surface's own words, naming the link by
// its place in the served workspace and nothing of where it leads.

// filesProbeResolver stands in for the catalog's resolution with the files-probe
// importer, which declares file patterns, so a run drives the real import socket
// and the real guest without a catalog on disk.
type filesProbeResolver struct {
	probe types.Plugin
}

func (r filesProbeResolver) ImporterPluginForFormat(format string, _ []string) (types.Plugin, int, error) {
	if format != importopstest.FilesProbeFormat {
		return types.Plugin{}, -1, fmt.Errorf("this fixture serves no importer of the %q format", format)
	}
	return r.probe, -1, nil
}

// pointImportSeamAtTheFilesProbe points the shared layer's seam at the built
// files-probe importer for the duration of one case: its entity, generated from
// its module, is the one importer the recognition reads, and the binary's own
// parsers read the files it selects.
//
// 🔴 It swaps a package-level variable, so no case using it may be made parallel.
func pointImportSeamAtTheFilesProbe(t *testing.T) {
	t.Helper()
	module := importopstest.FilesProbeModule(t)
	probe := importopstest.FilesProbeEntity(t)
	parserRes, parserLoad := importopstest.EmbeddedParsers(t)
	prev := importops.BuildSeam
	t.Cleanup(func() { importops.BuildSeam = prev })
	importops.BuildSeam = func(*workspaceconfig.ResolvedWorkspace, importops.PluginBytesResolver) (importops.Seam, error) {
		return importops.Seam{
			ImporterRes: filesProbeResolver{probe: probe},
			ImporterLoad: func(p types.Plugin) (wasm.PluginSpec, error) {
				return wasm.PluginSpec{Name: p.Coordinate(), Path: module}, nil
			},
			ParserRes:  parserRes,
			ParserLoad: parserLoad,
			Importers:  []types.Plugin{probe},
		}, nil
	}
}

// layProbeFlows lays a directory named flows beneath root holding two files the
// probe's patterns select, of one base name, and a third no pattern selects, made
// unreadable — so an import that opened it would be refused. It returns the names
// the importer is to be handed, in order.
func layProbeFlows(t *testing.T, root string) []string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("Windows reads a file whatever its permission bits say, so no file here can be made unreadable")
	}
	dir := filepath.Join(root, "flows")
	importopstest.WriteSource(t, filepath.Join(dir, "a", "x.probe.json"), importopstest.FilesProbeBody("a"))
	importopstest.WriteSource(t, filepath.Join(dir, "b", "x.probe.json"), importopstest.FilesProbeBody("b"))
	notes := importopstest.WriteSource(t, filepath.Join(dir, "notes.txt"), "notes")
	if err := os.Chmod(notes, 0); err != nil {
		t.Fatalf("take the permissions off %s: %v", notes, err)
	}
	t.Cleanup(func() { _ = os.Chmod(notes, 0o644) })
	if _, err := os.ReadFile(notes); err == nil {
		t.Fatalf("%s is still readable with no permissions, so nothing here can show it was never opened", notes)
	}
	return []string{"a/x.probe.json", "b/x.probe.json"}
}

// layProbeLinkOut lays a directory named flows beneath root whose b/x.probe.json
// is a link to a file outside the workspace, and returns where the link leads.
func layProbeLinkOut(t *testing.T, root string) string {
	t.Helper()
	target := importopstest.WriteSource(t, filepath.Join(t.TempDir(), "outside.probe.json"),
		importopstest.FilesProbeBody("outside"))
	link := filepath.Join(root, "flows", "b", "x.probe.json")
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		t.Fatalf("create the link's directory: %v", err)
	}
	if err := os.Symlink(target, link); err != nil {
		if runtime.GOOS == "windows" {
			t.Skipf("this Windows account may not make a symbolic link: %v", err)
		}
		t.Fatalf("make the link: %v", err)
	}
	return target
}

// probeHandedNames reads the names of the sources the probe reported it was
// handed off a tool answer's diagnostics.
func probeHandedNames(t *testing.T, res callToolResult) []string {
	t.Helper()
	if res.IsError {
		t.Fatalf("the import was refused: %s", res.Content[0].Text)
	}
	var p importPayload
	if err := json.Unmarshal(res.StructuredContent, &p); err != nil {
		t.Fatalf("decode the structured answer: %v (body %s)", err, res.StructuredContent)
	}
	messages := make([]string, 0, len(p.Diagnostics))
	for _, d := range p.Diagnostics {
		messages = append(messages, d.Message)
	}
	report := importopstest.ReadFilesProbeReport(t, messages)
	names := make([]string, 0, len(report.Sources))
	for _, s := range report.Sources {
		names = append(names, s.Name)
	}
	return names
}

// TestTheImportToolReadsADirectoryOnEitherTransport drives a directory named
// relative to the served workspace through the tool over the process's own streams
// and over a listener: the importer is handed exactly the two files its patterns
// select, in order, and never the third.
func TestTheImportToolReadsADirectoryOnEitherTransport(t *testing.T) {
	pointImportSeamAtTheFilesProbe(t)

	t.Run("over the process's own streams", func(t *testing.T) {
		root, _ := importSourceWorkspace(t, "unused.txt", nil)
		want := layProbeFlows(t, root)
		res := callTool(t, importToolServer(t, root), importToolName, map[string]any{"source": "flows", "dry_run": true})
		if got := probeHandedNames(t, *res); !slices.Equal(got, want) {
			t.Errorf("the importer was handed %v; want exactly %v", got, want)
		}
	})

	t.Run("over a listener", func(t *testing.T) {
		f := newScopeGridFixture(t, false)
		f.srv.resolveImportPlugin = importToolResolver
		want := layProbeFlows(t, f.root)
		res := toolResultOf(t, importToolName, f.callTool(t, f.grantOf(t, scopeWorkspaceWrite), importToolName,
			map[string]any{"source": "flows", "dry_run": true}))
		if got := probeHandedNames(t, res); !slices.Equal(got, want) {
			t.Errorf("the importer was handed %v; want exactly %v", got, want)
		}
	})
}

// TestADirectoryLinkLeadingOutIsRefusedInEachTransportsWords holds the link-out
// refusal to what each caller may be told. Both open with the condition's short
// code and its sentence. Over the process's own streams the caller is the owner,
// told where the link leads as the command line tells it; over a listener the
// refusal names the link by its place in the served workspace and says nothing of
// where it leads — neither the place outside, nor the served root.
//
// 🔴 PLANTED DEFECT: have importLinkRefusal (internal/mcp/tool_import.go) return
// "", false at its head, and the listener row reddens: the condition's own
// occurrence reaches the caller, naming the place outside the workspace the link
// leads to.
func TestADirectoryLinkLeadingOutIsRefusedInEachTransportsWords(t *testing.T) {
	pointImportSeamAtTheFilesProbe(t)
	short, ok := cerrors.ShortCodeOf(readscope.ErrReadLeadsOutOfWorkspace())
	if !ok {
		t.Fatal("the link-out condition carries no short code")
	}
	sentence, ok := cerrors.MessageOf(readscope.ErrReadLeadsOutOfWorkspace())
	if !ok {
		t.Fatal("the link-out condition carries no registered sentence")
	}
	args := map[string]any{"source": "flows", "dry_run": true, "format": importopstest.FilesProbeFormat}

	t.Run("over the process's own streams", func(t *testing.T) {
		root, _ := importSourceWorkspace(t, "unused.txt", nil)
		target := layProbeLinkOut(t, root)
		res := callTool(t, importToolServer(t, root), importToolName, args)
		if !res.IsError {
			t.Fatalf("a link leading out of the directory was read through: %s", res.Content[0].Text)
		}
		text := res.Content[0].Text
		for _, want := range []string{short + ": ", sentence, target} {
			if !strings.Contains(text, want) {
				t.Errorf("the refusal does not carry %q: %s", want, text)
			}
		}
	})

	t.Run("over a listener", func(t *testing.T) {
		f := newScopeGridFixture(t, false)
		f.srv.resolveImportPlugin = importToolResolver
		target := layProbeLinkOut(t, f.root)
		res := toolResultOf(t, importToolName, f.callTool(t, f.grantOf(t, scopeWorkspaceWrite), importToolName, args))
		if !res.IsError {
			t.Fatalf("a link leading out of the directory was read through: %s", res.Content[0].Text)
		}
		text := res.Content[0].Text
		for _, want := range []string{short + ": ", sentence, `"flows/b/x.probe.json"`} {
			if !strings.Contains(text, want) {
				t.Errorf("the refusal does not carry %q: %s", want, text)
			}
		}
		for _, kept := range []string{target, filepath.Dir(target), f.root} {
			if strings.Contains(text, kept) {
				t.Errorf("the refusal names %q, a place on the host a listener caller is not told: %s", kept, text)
			}
		}
	})
}
