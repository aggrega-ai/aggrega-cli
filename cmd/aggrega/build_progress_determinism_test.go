// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"sort"
	"testing"
)

// TestBuild_StdoutAndDistByteIdenticalAcrossProgressModes is the determinism
// guard for the progress surface (the Deterministic Output invariant): the
// per-phase progress checklist writes only to the diagnostic sink (stderr), so a
// build's stdout summary and its rendered dist/ tree must be byte-identical no
// matter which progress mode is selected — auto (silent on a pipe), the
// interactive checklist (TTY), forced static lines (--progress on a pipe),
// silent (--quiet), verbose (--debug), and the cold profile (--disable-plugin-
// cache). If any mode perturbs stdout or the artifacts by one byte, this test
// fails.
func TestBuild_StdoutAndDistByteIdenticalAcrossProgressModes(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	writeProgressBuildFixture(t, root)

	catalogRoot := filepath.Join(root, "catalog")
	releaseYAML := filepath.Join(root, "release.yaml")

	type mode struct {
		name string
		tty  bool
		diag buildDiagOptions
		run  buildRunOptions
	}
	modes := []mode{
		{name: "auto-pipe", tty: false, diag: buildDiagOptions{}},
		{name: "checklist-tty", tty: true, diag: buildDiagOptions{}},
		{name: "progress-forced-pipe", tty: false, diag: buildDiagOptions{progress: true}},
		{name: "quiet", tty: true, diag: buildDiagOptions{quiet: true}},
		{name: "debug", tty: true, diag: buildDiagOptions{debug: true}},
		{name: "cold-disable-cache", tty: true, diag: buildDiagOptions{}, run: buildRunOptions{disablePluginCache: true}},
	}

	origTerm := fileTerminal
	t.Cleanup(func() { fileTerminal = origTerm })

	var wantStdout string
	var wantDist string
	for i, m := range modes {
		fileTerminal = func(io.Writer) bool { return m.tty }

		// Isolate each run's artifacts; the physical outDir does not appear in the
		// stdout summary path we compare below because we hold it constant.
		outDir := filepath.Join(root, "dist")
		if err := os.RemoveAll(outDir); err != nil {
			t.Fatalf("%s: clean dist: %v", m.name, err)
		}
		run := m.run
		run.outDir = outDir

		var stdout, stderr bytes.Buffer
		if err := dispatchBuildWithDiag(nil, releaseYAML, []string{catalogRoot}, false, m.diag, run, &stderr, &stdout); err != nil {
			t.Fatalf("%s: build failed: %v (stderr=%q)", m.name, err, stderr.String())
		}

		gotStdout := stdout.String()
		gotDist := hashDistTree(t, outDir)

		if i == 0 {
			wantStdout = gotStdout
			wantDist = gotDist
			// Sanity: the build actually produced artifacts.
			if gotDist == hashOfNothing() {
				t.Fatalf("%s: build produced no dist artifacts to compare", m.name)
			}
			t.Logf("stdout sha256=%s", sha256Hex([]byte(gotStdout)))
			t.Logf("dist   sha256=%s", gotDist)
			continue
		}
		if gotStdout != wantStdout {
			t.Errorf("%s: stdout differs from auto-pipe.\n auto=%q\n %s=%q", m.name, wantStdout, m.name, gotStdout)
		}
		if gotDist != wantDist {
			t.Errorf("%s: dist tree sha differs from auto-pipe: want %s got %s", m.name, wantDist, gotDist)
		}
	}
}

// hashDistTree returns a sha256 over the sorted (relpath, content) pairs of every
// file under dir, so a single hex string identifies the whole rendered tree.
func hashDistTree(t *testing.T, dir string) string {
	t.Helper()
	type entry struct {
		rel     string
		content []byte
	}
	var entries []entry
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		rel, rerr := filepath.Rel(dir, path)
		if rerr != nil {
			return rerr
		}
		b, rerr := os.ReadFile(path)
		if rerr != nil {
			return rerr
		}
		entries = append(entries, entry{rel: filepath.ToSlash(rel), content: b})
		return nil
	})
	if err != nil {
		t.Fatalf("walk dist %s: %v", dir, err)
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].rel < entries[j].rel })
	h := sha256.New()
	for _, e := range entries {
		h.Write([]byte(e.rel))
		h.Write([]byte{0})
		h.Write(e.content)
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}

func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func hashOfNothing() string {
	h := sha256.New()
	return hex.EncodeToString(h.Sum(nil))
}

// writeProgressBuildFixture writes a minimal but complete, network-free build
// fixture (a single core/server component rendered through the built-in helm
// engine) that builds green for the determinism matrix above.
func writeProgressBuildFixture(t *testing.T, root string) {
	t.Helper()
	writeFixture(t, filepath.Join(root, "aggregate.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Aggregate
metadata:
  name: progress-smoke
  version: "1.0.0"
spec:
  components:
    api:
      type: core/server
      values:
        port: 8080
`)
	writeFixture(t, filepath.Join(root, "release.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Release
metadata:
  name: progress-smoke-rel
  environment: testing
  version: "1.0.0"
spec:
  aggregate: ./aggregate.yaml
  manifest: ./manifest.yaml
  boundaries:
    main:
      directory: ./dist/main
      components:
        api:
          infra:
            replicas: 1
`)
	writeFixture(t, filepath.Join(root, "manifest.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Manifest
metadata:
  name: progress-smoke-manifest
  version: "1.0.0"
spec:
  mappings:
    components:
      type:
        core/server: server-recipe:1.0.0
`)
	catalogRoot := filepath.Join(root, "catalog")
	writeFixture(t, filepath.Join(catalogRoot, "components", "server.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Component
metadata:
  name: core/server
  version: "1.0.0"
  labels:
    component.aggrega.ai/type: "server"
spec:
  schema:
    values:
      port: { type: integer, default: 8080 }
`)
	writeFixture(t, filepath.Join(catalogRoot, "providers", "helm.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Provider
metadata:
  name: helm
  version: "1.0.0"
spec:
  plugin: "builtin:helm"
`)
	writeFixture(t, filepath.Join(catalogRoot, "recipes", "server.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Recipe
metadata:
  name: server-recipe
  version: "1.0.0"
spec:
  provider: helm
  implements: core/server
  workload: |
    apiVersion: aggrega.ai/v1alpha1
    kind: Workload
    replicas: {{ .infra.replicas | default 1 }}
    containers:
      server:
        image: test-server:latest
        ports:
          web: {{ .values.port }}
    services:
      svc:
        type: ClusterIP
        ports:
          web: {{ .values.port }}
`)
}
