// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package serve

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/pluginops"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// pluginWriteFixture is a Server over a scratch workspace carrying one governance
// attachment, with the machine-wide layer redirected inside the scratch tree.
//
// It reuses the registry fixture rather than standing up a second one: both need
// exactly a resolution whose global layer is redirected, and a run that did not
// redirect it would mutate the machine. It returns the workspace file so a test
// can read back what a write actually did.
func pluginWriteFixture(t *testing.T) (*Server, string) {
	t.Helper()
	srv, _, wsPath := registryWriteFixture(t, `workspace:
  catalog_dirs:
    - catalog
plugins:
  governance:
    - uri: governance/tag-injector:9.9.9
      role: label-transform
`)
	return srv, wsPath
}

// TestPluginRemoveOnTheDaemonReportsItsScopeAndChangesTheFile is the
// invocation-level proof for the removal: the method is driven, its response
// read, and the file read back — because a handler that answered correctly and
// wrote nothing would satisfy a response-only assertion.
//
// The scope is asserted alongside, since a caller told a machine-wide change was
// local would act on it more freely than it should.
func TestPluginRemoveOnTheDaemonReportsItsScopeAndChangesTheFile(t *testing.T) {
	t.Parallel()
	srv, wsPath := pluginWriteFixture(t)

	before, err := os.ReadFile(wsPath)
	if err != nil {
		t.Fatalf("read the workspace layer: %v", err)
	}
	if !strings.Contains(string(before), "tag-injector") {
		t.Fatalf("the fixture does not carry the attachment the removal is meant to find:\n%s", before)
	}

	resp := callDaemonMethod(t, srv, "aggrega/pluginRemove", map[string]any{
		"type":  "governance",
		"query": "tag-injector",
	})
	var got pluginRemoveResult
	decodeDaemonResult(t, resp, &got)

	if got.Type != "governance" || got.Query != "tag-injector" {
		t.Errorf("the response does not describe what was removed: %+v", got)
	}
	if got.Scope.ConfigFile != wsPath {
		t.Errorf("scope.configFile = %q; want the workspace layer %q", got.Scope.ConfigFile, wsPath)
	}
	if got.Scope.MachineWide {
		t.Error("a socket removal reported a machine-wide reach")
	}
	if got.Scope.WorkspaceRoot == "" {
		t.Error("the response does not name the workspace the session was opened over")
	}

	after, err := os.ReadFile(wsPath)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if strings.Contains(string(after), "tag-injector") {
		t.Errorf("the attachment survived a removal that reported success:\n%s", after)
	}
}

// TestPluginRemoveOnTheDaemonRefusesAnAbsentSubject pins that a query matching
// nothing fails rather than reporting success over an unchanged configuration, so
// a caller is never told a plugin stopped participating when it did not.
func TestPluginRemoveOnTheDaemonRefusesAnAbsentSubject(t *testing.T) {
	t.Parallel()
	srv, wsPath := pluginWriteFixture(t)

	before, err := os.ReadFile(wsPath)
	if err != nil {
		t.Fatalf("read the workspace layer: %v", err)
	}

	resp := callDaemonMethod(t, srv, "aggrega/pluginRemove", map[string]any{
		"type":  "governance",
		"query": "no-such-plugin",
	})
	if resp.Error == nil {
		t.Fatal("a removal matching nothing reported success")
	}
	if !strings.Contains(resp.Error.Message, "no-such-plugin") {
		t.Errorf("the refusal does not name the query that matched nothing: %s", resp.Error.Message)
	}

	after, err := os.ReadFile(wsPath)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if string(after) != string(before) {
		t.Errorf("a refused removal changed the configuration file:\nbefore:\n%s\nafter:\n%s", before, after)
	}
}

// TestThePluginMethodsRefuseAMissingRequiredField drives each method's own
// required-field refusals, on THIS surface's spellings.
//
// The spellings matter and are the reason these checks live in the handler rather
// than being left to the shared layer: that layer names the command line's flags,
// and a refusal naming a flag a caller never sent tells them to fix something
// they cannot reach from a request.
func TestThePluginMethodsRefuseAMissingRequiredField(t *testing.T) {
	t.Parallel()
	srv, _ := pluginWriteFixture(t)

	for _, tc := range []struct {
		name   string
		method string
		params map[string]any
		want   string
	}{
		{"add with no type", "aggrega/pluginAdd", map[string]any{"query": "x"}, "type is required"},
		{"add with no query", "aggrega/pluginAdd", map[string]any{"type": "linter"}, "query is required"},
		{"add: a provider with no capability", "aggrega/pluginAdd",
			map[string]any{"type": "provider", "query": "x"}, "requires capability"},
		{"add: governance with no role", "aggrega/pluginAdd",
			map[string]any{"type": "governance", "query": "x"}, "requires role"},
		{"remove with no type", "aggrega/pluginRemove", map[string]any{"query": "x"}, "type is required"},
		{"remove with no query", "aggrega/pluginRemove", map[string]any{"type": "linter"}, "query is required"},
		{"sync with no entity", "aggrega/pluginSync", map[string]any{"plugin": "p.wasm"}, "entity is required"},
		{"sync with no plugin", "aggrega/pluginSync", map[string]any{"entity": "e.yaml"}, "plugin is required"},
		{"gen-catalog with no selection", "aggrega/pluginGenCatalog",
			map[string]any{"overlay": "o", "outputDir": "g"}, "name exactly one selection"},
		{"gen-catalog with both selections", "aggrega/pluginGenCatalog",
			map[string]any{"fromEmbedded": true, "plugin": "p.wasm", "overlay": "o", "outputDir": "g"},
			"name exactly one selection"},
		{"gen-catalog with no overlay", "aggrega/pluginGenCatalog",
			map[string]any{"fromEmbedded": true, "outputDir": "g"}, "overlay is required"},
		{"gen-catalog reporting still needs an output directory", "aggrega/pluginGenCatalog",
			map[string]any{"fromEmbedded": true, "overlay": "o", "report": true}, "outputDir is required"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resp := callDaemonMethod(t, srv, tc.method, tc.params)
			if resp.Error == nil {
				t.Fatalf("%s accepted a request missing a required field", tc.method)
			}
			if !strings.Contains(resp.Error.Message, tc.want) {
				t.Errorf("refusal = %q; want it to contain %q", resp.Error.Message, tc.want)
			}
		})
	}
}

// TestThePluginMethodsRefuseAPathOutsideTheWorkspace pins the confinement, over
// the parameters that still carry it.
//
// Two things are confined here and they are confined for different reasons. A
// generation's output directory is WRITTEN to, and no request may name a
// destination outside the workspace the session was opened over — that one holds
// on every method, and widening a read never widens it. The alignment method's
// two paths are the other reason: both stay confined because a selection has not
// been asked for on that request, so the confining rule is still the right answer
// for them rather than the only available one.
//
// 🔴 It is the CONTRAST case for the sibling test below, and the pair is the
// point. Read alone, an acceptance test is consistent with a surface that stopped
// confining anything, and a refusal test is consistent with one that accepts
// nothing; only the two over the same fixture say which parameter took which
// rule.
func TestThePluginMethodsRefuseAPathOutsideTheWorkspace(t *testing.T) {
	t.Parallel()
	srv, _ := pluginWriteFixture(t)

	outside := filepath.Join(t.TempDir(), "elsewhere.yaml")
	if err := os.WriteFile(outside, []byte("apiVersion: aggrega.ai/v1alpha1\n"), 0o644); err != nil {
		t.Fatalf("seed the outside file: %v", err)
	}

	for _, tc := range []struct {
		name   string
		method string
		params map[string]any
	}{
		{"an absolute entity path", "aggrega/pluginSync",
			map[string]any{"entity": outside, "plugin": "plugins/greet.wasm"}},
		{"an absolute module path", "aggrega/pluginSync",
			map[string]any{"entity": "plugins/greet.yaml", "plugin": outside}},
		{"a relative path climbing out", "aggrega/pluginSync",
			map[string]any{"entity": "../escape.yaml", "plugin": "plugins/greet.wasm"}},
		{"an absolute output directory", "aggrega/pluginGenCatalog",
			map[string]any{"fromEmbedded": true, "overlay": "overlays", "outputDir": filepath.Dir(outside)}},
		{"an output directory climbing out", "aggrega/pluginGenCatalog",
			map[string]any{"fromEmbedded": true, "overlay": "overlays", "outputDir": "../generated"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resp := callDaemonMethod(t, srv, tc.method, tc.params)
			if resp.Error == nil {
				t.Fatalf("%s accepted a path outside the workspace", tc.method)
			}
			if !strings.Contains(resp.Error.Message, "inside the workspace this session opened") {
				t.Errorf("refusal = %q; want it to name the workspace bound", resp.Error.Message)
			}
		})
	}
}

// TestPluginGenCatalogReadsAModuleAndOverlayTheAuthorSelected is the acceptance
// half, and it is driven with BOTH read paths absolute and outside the workspace
// at once.
//
// That is the shape the operation exists to serve: a module lands under whatever
// directory its toolchain builds into, the overlay describing it is authored
// beside that module, and neither sits in the workspace an editor has open. An
// accepted module with a refused overlay would move the operation without making
// it work, so the pair travels together.
//
// What is asserted is that the module was OPENED, not merely that the request got
// past a gate. The generation reads the module's own bytes to parse its import
// section, so a refusal naming that parse is proof the file at the absolute path
// was read — a fixture module that is not a real module is what makes the read
// observable without standing up a guest.
func TestPluginGenCatalogReadsAModuleAndOverlayTheAuthorSelected(t *testing.T) {
	t.Parallel()
	srv, _ := pluginWriteFixture(t)

	built := filepath.Join(t.TempDir(), "built.wasm")
	if err := os.WriteFile(built, []byte("not a module"), 0o644); err != nil {
		t.Fatalf("seed the selected module: %v", err)
	}
	overlay := filepath.Join(t.TempDir(), "overlay.yaml")
	if err := os.WriteFile(overlay, []byte("metadata: {}\n"), 0o644); err != nil {
		t.Fatalf("seed the selected overlay: %v", err)
	}

	resp := callDaemonMethod(t, srv, "aggrega/pluginGenCatalog", map[string]any{
		"plugin":    built,
		"overlay":   overlay,
		"outputDir": "generated",
	})
	if resp.Error == nil {
		t.Fatal("the fixture module is not a real module, so the generation cannot have completed")
	}
	if strings.Contains(resp.Error.Message, "inside the workspace this session opened") {
		t.Fatalf("an absolute module or overlay the author selected was refused as a workspace-scoped path: %q", resp.Error.Message)
	}
	if !strings.Contains(resp.Error.Message, "wasm binary magic mismatch") {
		t.Fatalf("the generation did not reach the module's bytes, so nothing here shows the selected path was read: %q", resp.Error.Message)
	}
	if !strings.Contains(resp.Error.Message, built) {
		t.Errorf("the answer does not name the module that was read: %q, want it to name %q", resp.Error.Message, built)
	}
}

// TestTheCommandExtensionTypeRequiresConsentAndAnInstaller pins the two things
// that stand between a request and a command-extension install, and each covers a
// distinct failure.
//
// Consent is required because the command line obtains it by asking at a terminal
// and this surface has none: the decision is the caller's to obtain and send, and
// installing without it would take an acceptance nobody gave. The installer is
// required because a process wired without one cannot perform the install at all,
// and reporting success there would leave the caller believing a command exists.
func TestTheCommandExtensionTypeRequiresConsentAndAnInstaller(t *testing.T) {
	t.Parallel()

	t.Run("consent is refused when absent", func(t *testing.T) {
		srv, _ := pluginWriteFixture(t)
		// An installer IS wired here, so a refusal can only be about the consent.
		srv.installCLIPlugin = func(context.Context, *workspaceconfig.ResolvedWorkspace, pluginops.CLIInstallRequest) error {
			t.Error("the installer ran without the caller's consent")
			return nil
		}
		resp := callDaemonMethod(t, srv, "aggrega/pluginAdd", map[string]any{
			"type": "cli", "query": "greet",
		})
		if resp.Error == nil {
			t.Fatal("a command-extension install ran with no consent")
		}
		if !strings.Contains(resp.Error.Message, "acceptCapabilities") {
			t.Errorf("the refusal does not name the field the caller must send: %s", resp.Error.Message)
		}
	})

	t.Run("a daemon with no installer says so", func(t *testing.T) {
		srv, _ := pluginWriteFixture(t)
		srv.installCLIPlugin = nil
		resp := callDaemonMethod(t, srv, "aggrega/pluginAdd", map[string]any{
			"type": "cli", "query": "greet", "acceptCapabilities": true,
		})
		if resp.Error == nil {
			t.Fatal("a command-extension install ran with no installer wired")
		}
		if !strings.Contains(resp.Error.Message, "installer") {
			t.Errorf("the refusal does not name the missing installer: %s", resp.Error.Message)
		}
	})
}

// TestTheGeneratedDocumentWireShapeIsThisSurfacesOwn pins the projection, because
// the shared document type carries only the reported form's field tags: emitting
// it directly serializes its Go field names, which arrive capitalised where every
// other field on this surface is lowercase.
//
// The empty case is asserted too, since the writing delivery reports no documents
// and an empty list beside the notices would read as "generated nothing".
func TestTheGeneratedDocumentWireShapeIsThisSurfacesOwn(t *testing.T) {
	t.Parallel()

	if got := generatedDocuments(nil); got != nil {
		t.Errorf("an empty set projected to %#v; want nil so the field is omitted", got)
	}
}
