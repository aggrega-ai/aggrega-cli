// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// referenceComponent is the standalone Component file the framework-proof tests
// render through the kindRenderer table. It carries a schema body and a
// usesSchema socket so list / default / yaml outputs each have substance.
const referenceComponent = `apiVersion: aggrega.ai/v1alpha1
kind: Component
metadata:
  name: core/server
  version: "1.0.0"
  labels:
    component.aggrega.ai/type: server
spec:
  schema: |
    values: { port?: int | *8080 }
  usesSchema:
    db:
      selector: 'component.aggrega.ai/type=stateful-server'
`

// writeReferenceComponent writes referenceComponent to a temp file and returns
// its path.
func writeReferenceComponent(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	path := filepath.Join(root, "component.yaml")
	if err := os.WriteFile(path, []byte(referenceComponent), 0o644); err != nil {
		t.Fatalf("write reference component: %v", err)
	}
	return path
}

// compareGoldenKind asserts got matches the committed golden at
// testdata/inspect_<kind>/<name>. Regenerate with AGGREGA_UPDATE_GOLDEN=1. The
// kind subdir is parameterized so each per-kind inspector pins its own golden
// tree.
func compareGoldenKind(t *testing.T, kind, name string, got []byte) {
	t.Helper()
	golden := filepath.Join("testdata", "inspect_"+kind, name)
	if os.Getenv("AGGREGA_UPDATE_GOLDEN") != "" {
		if err := os.MkdirAll(filepath.Dir(golden), 0o755); err != nil {
			t.Fatalf("mkdir golden dir: %v", err)
		}
		if err := os.WriteFile(golden, got, 0o644); err != nil {
			t.Fatalf("write golden: %v", err)
		}
		t.Logf("updated golden %s (%d bytes)", golden, len(got))
		return
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatalf("read golden %s (regenerate with AGGREGA_UPDATE_GOLDEN=1): %v", golden, err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("%s drifted from golden.\n--- got ---\n%s\n--- want ---\n%s", name, got, want)
	}
}

// TestInspectComponent_DefaultText_Golden pins the default-level text output of
// the reference Component byte-for-byte. This is the no-regression anchor: the
// default output must stay identical as the framework evolves.
func TestInspectComponent_DefaultText_Golden(t *testing.T) {
	t.Parallel()
	path := writeReferenceComponent(t)
	var buf bytes.Buffer
	opts := testInspectOptions(&buf)
	if err := dispatchInspect(resolveTempWorkspace(t), path, nil, opts); err != nil {
		t.Fatalf("inspect: %v", err)
	}
	// The header carries the temp path; mask it to the basename so the golden
	// is stable across machines.
	got := strings.ReplaceAll(buf.String(), path, "COMPONENT_PATH")
	compareGoldenKind(t, "component", "default.text.golden", []byte(got))
}

// TestInspectComponent_List_Golden pins the --list output: identifiers only
// (the Component name plus its usesSchema socket names).
func TestInspectComponent_List_Golden(t *testing.T) {
	t.Parallel()
	path := writeReferenceComponent(t)
	var buf bytes.Buffer
	opts := testInspectOptions(&buf)
	opts.level = levelList
	if err := dispatchInspect(resolveTempWorkspace(t), path, nil, opts); err != nil {
		t.Fatalf("inspect --list: %v", err)
	}
	compareGoldenKind(t, "component", "list.text.golden", buf.Bytes())
}

// TestInspectComponent_YAML_Golden pins the --output yaml dump of the reference
// Component: the parsed file re-emitted as YAML.
func TestInspectComponent_YAML_Golden(t *testing.T) {
	t.Parallel()
	path := writeReferenceComponent(t)
	var buf bytes.Buffer
	opts := testInspectOptions(&buf)
	opts.output = outputYAML
	if err := dispatchInspect(resolveTempWorkspace(t), path, nil, opts); err != nil {
		t.Fatalf("inspect --output yaml: %v", err)
	}
	compareGoldenKind(t, "component", "yaml.golden", buf.Bytes())
}

// TestInspectComponent_ColorOnHeader proves the colorizer is wired through the
// framework end-to-end: --color=always wraps the default-level header in ANSI.
func TestInspectComponent_ColorOnHeader(t *testing.T) {
	t.Parallel()
	path := writeReferenceComponent(t)
	var buf bytes.Buffer
	opts := inspectOptions{out: &buf, colorize: newColorizer(colorAlways, &buf, false)}
	if err := dispatchInspect(resolveTempWorkspace(t), path, nil, opts); err != nil {
		t.Fatalf("inspect --color=always: %v", err)
	}
	if !hasANSI(buf.String()) {
		t.Errorf("--color=always should wrap the header in ANSI; got:\n%s", buf.String())
	}
}

// TestInspectPath_NonStandaloneKind pins the A8 hard error: a Workload file is
// embedded-only and reports "not a standalone inspectable entity" naming the
// kind, distinct from the not-yet-implemented kinds.
func TestInspectPath_NonStandaloneKind(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	path := filepath.Join(root, "workload.yaml")
	if err := os.WriteFile(path, []byte("apiVersion: aggrega.ai/v1alpha1\nkind: Workload\nmetadata:\n  name: w\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	err := inspectPath(resolveTempWorkspace(t), path, nil, testInspectOptions(&bytes.Buffer{}))
	if err == nil {
		t.Fatal("Workload should hard-error as non-standalone")
	}
	if !strings.Contains(err.Error(), "not a standalone inspectable entity") {
		t.Errorf("A8 error should say not a standalone inspectable entity; got %v", err)
	}
	if !strings.Contains(err.Error(), "Workload") {
		t.Errorf("A8 error should name the kind; got %v", err)
	}
}

// TestInspectPath_UnknownKind pins the unrecognized-kind error.
func TestInspectPath_UnknownKind(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	path := filepath.Join(root, "x.yaml")
	if err := os.WriteFile(path, []byte("apiVersion: aggrega.ai/v1alpha1\nkind: Sprocket\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	err := inspectPath(resolveTempWorkspace(t), path, nil, testInspectOptions(&bytes.Buffer{}))
	if err == nil || !strings.Contains(err.Error(), "unknown entity kind") {
		t.Errorf("unknown kind should report unknown entity kind; got %v", err)
	}
}

// TestInspectCatalogPrecedence_FlagBeatsConfig pins the layered catalog
// resolution: the --catalog flag dir is searched before the configured
// catalog_dirs, so a Component present only in the flag dir resolves, and on a
// name collision the flag dir wins. With neither flag nor config, the fallback
// is the current directory. This rides the already-shipped ResolveCatalogDirs;
// the test is a regression pin, not a reimplementation.
func TestInspectCatalogPrecedence_FlagBeatsConfig(t *testing.T) {
	t.Parallel()

	// Two catalog dirs, each defining core/server with a distinguishable
	// usesSchema socket so the resolved winner is observable in the output.
	flagDir := t.TempDir()
	configDir := t.TempDir()
	writeComponentWithSocket(t, flagDir, "from-flag")
	writeComponentWithSocket(t, configDir, "from-config")

	// Workspace whose config carries configDir in catalog_dirs.
	wsRoot := t.TempDir()
	if err := os.WriteFile(filepath.Join(wsRoot, workspaceconfig.ConfigFilename),
		[]byte("workspace:\n  catalog_dirs:\n    - "+configDir+"\n"), 0o644); err != nil {
		t.Fatalf("seed config: %v", err)
	}
	rw, err := workspaceconfig.Resolve(wsRoot, "")
	if err != nil {
		t.Fatalf("resolve workspace: %v", err)
	}

	// --catalog flag dir prepended → flag wins on the colliding name.
	var buf bytes.Buffer
	if err := inspectCatalogEntity(rw, "core/server", []string{flagDir}, testInspectOptions(&buf)); err != nil {
		t.Fatalf("inspect with flag: %v", err)
	}
	if !strings.Contains(buf.String(), "from-flag") {
		t.Errorf("flag dir should win the collision; got:\n%s", buf.String())
	}

	// No flag → config catalog_dirs resolves.
	buf.Reset()
	if err := inspectCatalogEntity(rw, "core/server", nil, testInspectOptions(&buf)); err != nil {
		t.Fatalf("inspect without flag: %v", err)
	}
	if !strings.Contains(buf.String(), "from-config") {
		t.Errorf("config catalog_dirs should resolve when no flag; got:\n%s", buf.String())
	}
}

// writeComponentWithSocket writes a core/server Component into dir whose
// usesSchema socket name is socketName, making the resolved source observable.
func writeComponentWithSocket(t *testing.T, dir, socketName string) {
	t.Helper()
	body := `apiVersion: aggrega.ai/v1alpha1
kind: Component
metadata:
  name: core/server
  version: "1.0.0"
  labels:
    component.aggrega.ai/type: server
spec:
  schema:
    values:
      port: { type: integer }
  usesSchema:
    ` + socketName + `:
      selector: 'component.aggrega.ai/type=stateful-server'
`
	if err := os.WriteFile(filepath.Join(dir, "server.yaml"), []byte(body), 0o644); err != nil {
		t.Fatalf("write component into %s: %v", dir, err)
	}
}
