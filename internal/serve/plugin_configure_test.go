// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package serve

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/pluginops"
	"github.com/aggrega-ai/aggrega-cli/internal/registryindex"
	"github.com/aggrega-ai/aggrega-cli/internal/registryops"
	"github.com/aggrega-ai/aggrega-cli/pkg/deprecation"
	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
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
			map[string]any{"fromEmbedded": true, "overlay": "o", "dryRun": true}, "outputDir is required"},
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

// withFakeRegistryForAttach points the plugin layer's registry-enumeration and
// version seams at fixed answers, so an attachment driven through this surface
// resolves an identity with no network.
//
// The version seams are pointed as a pair because the fallback resolution screens
// as well as selects: a test that fixed only the tags would leave the
// withdrawal-marker read pointing at a live registry.
// A command-extension repository is in the set as well as a governance one, and
// that is what makes the refusal test an ORDERING proof: without it, a disabled
// refusal would stop at "no cli plugin matches" and the installer would stay
// unreached for the wrong reason, so the assertion that it never ran would pass on
// a surface that had stopped refusing. Measured — that is exactly what a
// one-repository set did.
func withFakeRegistryForAttach(t *testing.T) {
	t.Helper()
	withFakeRegistryForAttachWithdrawing(t, []string{"1.0.0", "1.2.0"}, nil)
}

// withFakeRegistryForAttachWithdrawing is withFakeRegistryForAttach over a chosen
// published-version set, with a withdrawal recorded against each version named in
// withdrawn and mapped to the reason on its marker.
//
// Each recorded withdrawal carries a verified signer, because only a
// signature-verified record screens a version: an unsigned one is ignored by the
// same rule production applies, so a fixture without one would exercise an
// unscreened selection while reading as a screened one.
func withFakeRegistryForAttachWithdrawing(t *testing.T, tags []string, withdrawn map[string]string) {
	t.Helper()
	prevTarget := pluginops.RegistryTarget
	pluginops.RegistryTarget = func(workspaceconfig.Config) (registryops.RepoLister, string, error) {
		return fixedRepoLister{repos: []string{"plugin/governance/tag-injector", "plugin/cli/greet"}}, "hub.aggrega.ai", nil
	}
	prevTags := pluginops.RepoTagLister
	pluginops.RepoTagLister = func(context.Context, workspaceconfig.Config, string, string) ([]string, error) {
		return tags, nil
	}
	prevWithdrawals := pluginops.RepoWithdrawalLister
	pluginops.RepoWithdrawalLister = func(_ context.Context, _ workspaceconfig.Config, _, _ string, versions []string, _ io.Writer) ([]registryindex.VersionDeprecation, error) {
		var out []registryindex.VersionDeprecation
		for _, v := range versions {
			reason, hit := withdrawn[v]
			if !hit {
				continue
			}
			out = append(out, registryindex.VersionDeprecation{
				Version: v,
				Marker:  deprecation.Marker{Severity: deprecation.SeverityRetracted, Reason: reason},
				Signer:  &lockfile.VerifiedSignature{Identity: "publisher", Mode: "keyed"},
			})
		}
		return out, nil
	}
	t.Cleanup(func() {
		pluginops.RegistryTarget = prevTarget
		pluginops.RepoTagLister = prevTags
		pluginops.RepoWithdrawalLister = prevWithdrawals
	})
}

// fixedRepoLister hands the enumeration primitive a fixed repository set in one
// page, so an attachment runs without a network.
type fixedRepoLister struct{ repos []string }

func (f fixedRepoLister) Repositories(_ context.Context, _ string, fn func(repos []string) error) error {
	return fn(f.repos)
}

// TestPluginAddOnTheDaemonOffersBothDeliveries drives the method twice over one
// fixture and holds each delivery to what it promises: the report answers with the
// configuration document and leaves both configuration files byte-identical, and
// the write puts the file on disk and answers with no document.
//
// Both are driven over the SAME fixture because each is the other's control. A
// report that quietly wrote would pass an assertion about the answer alone, and a
// write that quietly stopped writing would pass an assertion about an unchanged
// file — only the pair says which delivery did which.
//
// 🔴 It reads both configuration files, not just the workspace one. The scope
// override sends the attachment to the machine-wide file, and a comparison scoped
// to the workspace would report an untouched tree for a report that had written
// the machine's own configuration.
func TestPluginAddOnTheDaemonOffersBothDeliveries(t *testing.T) {
	// Not parallel: the plugin layer's registry seams are process-global.
	withFakeRegistryForAttach(t)

	for _, tc := range []struct {
		name   string
		global bool
	}{
		{"the workspace default", false},
		{"the machine-wide override", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv, globalPath, wsPath := registryWriteFixture(t, `workspace:
  catalog_dirs:
    - catalog
default_registry: hub.aggrega.ai
`)
			target := wsPath
			if tc.global {
				target = globalPath
			}
			params := map[string]any{"type": "governance", "query": "tag-injector", "role": "label-transform", "dryRun": true}
			if tc.global {
				params["global"] = true
			}

			wsBefore := readConfigBytes(t, wsPath)
			globalBefore := readConfigBytes(t, globalPath)

			var reported pluginAddResult
			decodeDaemonResult(t, callDaemonMethod(t, srv, "aggrega/pluginAdd", params), &reported)

			if reported.Written {
				t.Error("the reporting delivery answered that it wrote")
			}
			if reported.Content == "" {
				t.Fatal("the reporting delivery answered with no document")
			}
			if reported.Scope.ConfigFile != target {
				t.Errorf("scope.configFile = %q; want %q — a client that trusted a wrong path here would write a machine-wide configuration into a workspace",
					reported.Scope.ConfigFile, target)
			}
			if reported.Scope.MachineWide != tc.global {
				t.Errorf("scope.machineWide = %v; want %v, so a client knows how far the content it now holds would reach",
					reported.Scope.MachineWide, tc.global)
			}
			if !strings.Contains(reported.Content, "governance/tag-injector:1.2.0") {
				t.Errorf("the reported document does not carry the pinned attachment a write would have made:\n%s", reported.Content)
			}
			// The MERGED WHOLE document: whatever the TARGET file already carried
			// survives in it. Composing against an empty base would drop those
			// settings, and writing that document back would erase them.
			carried := "catalog_dirs"
			if tc.global {
				carried = "registries"
			}
			if !strings.Contains(reported.Content, carried) {
				t.Errorf("the reported document dropped %q, which %s already carried:\n%s", carried, target, reported.Content)
			}
			if got := readConfigBytes(t, wsPath); got != wsBefore {
				t.Errorf("the reporting delivery modified the workspace configuration:\nbefore:\n%s\nafter:\n%s", wsBefore, got)
			}
			if got := readConfigBytes(t, globalPath); got != globalBefore {
				t.Errorf("the reporting delivery modified the machine-wide configuration:\nbefore:\n%s\nafter:\n%s", globalBefore, got)
			}

			params["dryRun"] = false
			var written pluginAddResult
			decodeDaemonResult(t, callDaemonMethod(t, srv, "aggrega/pluginAdd", params), &written)

			if !written.Written {
				t.Error("the writing delivery answered that it did not write")
			}
			if written.Content != "" {
				t.Errorf("the writing delivery carried the document as well as writing it; the file is on disk at %s:\n%s",
					written.Scope.ConfigFile, written.Content)
			}
			if got := readConfigBytes(t, target); got != reported.Content {
				t.Errorf("the written bytes differ from the reported content\nreported:\n%s\nwritten:\n%s", reported.Content, got)
			}
		})
	}
}

// readConfigBytes reads a configuration file, treating an absent file as empty so
// a before-and-after comparison spans a file the write is about to create.
func readConfigBytes(t *testing.T, path string) string {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return ""
		}
		t.Fatalf("read %s: %v", path, err)
	}
	return string(body)
}

// TestPluginAddOnTheDaemonRefusesAReportOnTheCommandExtensionType pins that the
// refusal is LOUD on this surface and that it precedes the install.
//
// 🔴 A silently ignored field is the failure worth catching, and it is not
// hypothetical: the neighbouring registry-registration method accepts a report
// field, ignores it and answers success, which reads to a caller as an argument
// that was honoured. An installer IS wired here, so a refusal can only be about
// the delivery — and the installer asserts it never ran, which makes this a proof
// that the refusal comes first rather than after the work. The fixture's
// repository set carries a command-extension plugin precisely so that claim is
// real: were the set governance-only, a disabled refusal would halt at "no cli
// plugin matches" and this assertion would pass for the wrong reason.
func TestPluginAddOnTheDaemonRefusesAReportOnTheCommandExtensionType(t *testing.T) {
	// Not parallel: the plugin layer's registry seams are process-global.
	withFakeRegistryForAttach(t)
	srv, _ := pluginWriteFixture(t)
	srv.installCLIPlugin = func(context.Context, *workspaceconfig.ResolvedWorkspace, pluginops.CLIInstallRequest) error {
		t.Error("the installer ran for a request the surface should have refused")
		return nil
	}

	resp := callDaemonMethod(t, srv, "aggrega/pluginAdd", map[string]any{
		"type": "cli", "query": "greet", "acceptCapabilities": true, "dryRun": true,
	})
	if resp.Error == nil {
		t.Fatal("a report on the command-extension type was accepted; its product is a record the machine maintains")
	}
	if !strings.Contains(resp.Error.Message, "cannot report what it would write") {
		t.Errorf("the refusal does not say why: %s", resp.Error.Message)
	}
	// The refusal must name no surface's own spelling for the delivery: a message
	// naming a command-line flag tells a request-driven caller to change something
	// it cannot reach.
	if strings.Contains(resp.Error.Message, "--dry-run") {
		t.Errorf("the refusal names the command line's flag, which no request-driven caller can send: %s", resp.Error.Message)
	}
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
