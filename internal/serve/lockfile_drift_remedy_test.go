// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package serve

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compilerops"
	"github.com/aggrega-ai/aggrega-cli/internal/wasmtest"
	"github.com/aggrega-ai/aggrega-cli/pkg/pluginpath"
)

// driftAxesWorkspace copies the shared fixture whose one Release reaches every axis a
// lockfile pins into a fresh workspace, lays the transform guest beside its Plugin
// entity under the name the plugin cascade reads, and returns the root. The copy is
// what every request reads and writes, so the tracked fixture is never driven in
// place.
func driftAxesWorkspace(t *testing.T) string {
	t.Helper()
	guest, err := os.ReadFile(wasmtest.EnsureFixtureBuilt(t, "mutator-proof-go.wasm"))
	if err != nil {
		t.Fatalf("read the guest fixture: %v", err)
	}
	src, err := filepath.Abs(filepath.Join("..", "compiler", "testdata", "lockfile-drift-axes"))
	if err != nil {
		t.Fatalf("locate the fixture workspace: %v", err)
	}
	root := t.TempDir()
	if err := os.CopyFS(root, os.DirFS(src)); err != nil {
		t.Fatalf("copy the fixture workspace: %v", err)
	}
	guestPath := filepath.Join(root, "catalog", "plugin", "governance", "tag-injector", "1.0.0",
		pluginpath.PluginLeaf("governance/tag-injector"))
	if err := os.WriteFile(guestPath, guest, 0o644); err != nil {
		t.Fatalf("lay the guest beside its entity: %v", err)
	}
	return root
}

// driftEveryAxis makes one edit on each axis the daemon reports as drift, failing
// the test when a file does not carry what its edit expects, so an edit the fixture
// no longer admits cannot pass as a drift that was never made.
func driftEveryAxis(t *testing.T, root string) {
	t.Helper()
	const edited = "\n# edited after the lockfile was written\n"
	// Not the plugin's bytes: the daemon's graph request refuses those at the render's pin check, before any drift warning.
	for _, e := range []struct{ rel, from, to string }{
		{"catalog/component/demo/web/1.0.0/component.yaml", "", edited},
		{"aggregate.yaml", "", edited},
		{"catalog/recipe/helm/svc/1.0.0/recipe.yaml", "", edited},
		{"catalog/libraries/base-types.yaml", "default: 8080", "default: 9090"},
		{"catalog/libraries/base-mappings.yaml", `"replicas": 2`, `"replicas": 3`},
		{"catalog/libraries/base-workloads.yaml", "image: svc:latest", "image: svc:2"},
		{"catalog/libraries/platform-artifacts.yaml", "owner: platform", "owner: ops"},
		{"r.yaml", "", edited},
	} {
		path := filepath.Join(root, e.rel)
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s to drift it: %v", e.rel, err)
		}
		next := string(body) + e.to
		if e.from != "" {
			if n := strings.Count(string(body), e.from); n != 1 {
				t.Fatalf("%s: want exactly one %q to rewrite, found %d", e.rel, e.from, n)
			}
			next = strings.Replace(string(body), e.from, e.to, 1)
		}
		if err := os.WriteFile(path, []byte(next), 0o644); err != nil {
			t.Fatalf("write the drifted %s: %v", e.rel, err)
		}
	}
}

// TestTheDriftWarningNamesTheSharedRemedyAndAPlainPullWithdrawsIt pins the editor's
// half of the drift remedy: the warning the daemon publishes on a drifted lockfile
// carries the command every other surface names, and running that command — a
// dependency resolution with no upgrade — withdraws the warning.
//
// The lockfile is written by one session and the edits are made before a second
// one opens the workspace, the way an editor started over a workspace that changed
// meets it, so what the second session reads is what is on disk rather than what a
// warm cache held. Every axis the daemon reports as drift is drifted at once — every
// axis the lockfile pins but the plugin's bytes, for the reason driftEveryAxis gives:
// the warning has to name each of them, and one resolution has to clear all of them.
//
// The two graph requests are each other's control. The first must publish a
// finding on the lockfile and the second an empty list for the same file, so a
// daemon that published nothing, or published the finding forever, fails one of
// them.
func TestTheDriftWarningNamesTheSharedRemedyAndAPlainPullWithdrawsIt(t *testing.T) {
	t.Parallel()
	root := driftAxesWorkspace(t)
	lockfilePath := filepath.Join(root, "r.lock")
	const release = "r.yaml"

	resolve := func(srv *Server, pinSources *bool) {
		t.Helper()
		var in, out bytes.Buffer
		in.Write(framedRequest(1, "aggrega/resolveDependencies", resolveDependenciesParams{Release: release, PinSources: pinSources}))
		if resp, _ := runOneRequestPastPushes(t, srv, &in, &out); resp.Error != nil {
			t.Fatalf("resolving the dependencies of %s was refused: %d %s", release, resp.Error.Code, resp.Error.Message)
		}
	}
	lockfileFindings := func(srv *Server) []Diagnostic {
		t.Helper()
		var in, out bytes.Buffer
		in.Write(framedRequest(2, "aggrega/irGraph", irGraphParams{Release: release}))
		resp, pushes := runOneRequestPastPushes(t, srv, &in, &out)
		if resp.Error != nil {
			t.Fatalf("the graph request for %s was refused: %d %s", release, resp.Error.Code, resp.Error.Message)
		}
		for _, p := range pushes {
			if p.File == lockfilePath {
				return p.Diagnostics
			}
		}
		t.Fatalf("the graph request published nothing on %s; publications: %+v", lockfilePath, pushes)
		return nil
	}

	writer, err := newTestSession(t, root, bytes.NewReader(nil), &bytes.Buffer{}).workspaceFor(root)
	if err != nil {
		t.Fatalf("open the workspace at %s: %v", root, err)
	}
	pin := true
	resolve(writer, &pin)

	driftEveryAxis(t, root)

	srv, err := newTestSession(t, root, bytes.NewReader(nil), &bytes.Buffer{}).workspaceFor(root)
	if err != nil {
		t.Fatalf("open the workspace at %s: %v", root, err)
	}
	findings := lockfileFindings(srv)
	if len(findings) != 1 {
		t.Fatalf("a lockfile drifted on every axis must carry one warning; got %d: %+v", len(findings), findings)
	}
	message := findings[0].Message
	if remedy := compilerops.LockfileDriftRemedy("<release>"); !strings.Contains(message, remedy) {
		t.Errorf("the warning does not carry the shared remedy %q:\n%s", remedy, message)
	}
	for _, named := range []string{`"demo/web"`, `"./aggregate.yaml"`, `"helm/svc:1.0.0"`, `"BaseTypes"`,
		`"BaseMappings"`, `"BaseWorkloads"`, `"PlatformArtifacts"`, `"r.yaml"`} {
		if !strings.Contains(message, named) {
			t.Errorf("the warning does not name %s, so that axis did not drift and this case covers less than it says:\n%s",
				named, message)
		}
	}

	resolve(srv, nil)
	if after := lockfileFindings(srv); len(after) != 0 {
		t.Errorf("after the resolution the warning names, the lockfile still carries %d finding(s): %+v", len(after), after)
	}
}
