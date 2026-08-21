// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package lintops

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/wasm/embedded"
	"github.com/aggrega-ai/aggrega-cli/pkg/pluginpath"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// The lint gate runs EVERY linter attachment the workspace configures, not only
// the first. These tests hold that behaviour at the product entry point
// (RunEntity), because a configured attachment that is silently dropped is
// invisible from every other angle: nothing halts, nothing warns, and the run
// reports a clean pass over a gate that never ran.
//
// The attachments resolve through the embed tier: each Plugin entity written
// below derives the built-in publishing linter's artifact reference from its own
// identity (pluginType/name), so the resolution needs no toolchain, no registry
// and no cache — only the guest that already ships in the binary. Two entities
// at different versions give two genuinely distinct attachments over that one
// guest.

// linterAttachmentEntity is a Plugin entity whose identity derives the built-in
// publishing linter's artifact reference, at the given version. Its declared
// export mirrors the built-in guest's own, so the load-gate cross-check passes.
func linterAttachmentEntity(version string) string {
	return `apiVersion: aggrega.ai/v1alpha1
kind: Plugin
metadata:
  name: publishing
  version: "` + version + `"
  pluginType: linter
spec:
  abiVersion: v1alpha1
  exports:
    - name: lint_rules
`
}

// twoAttachmentWorkspace writes a workspace whose catalog holds two Plugin
// entities for the built-in publishing linter (versions 1.0.0 and 2.0.0) and
// whose linter socket carries the attachments named by refs — a YAML fragment
// indented to sit under `plugins:`. It returns the resolved workspace.
func twoAttachmentWorkspace(t *testing.T, refs string) *workspaceconfig.ResolvedWorkspace {
	t.Helper()
	root := t.TempDir()
	catalogDir := filepath.Join(root, "catalog")
	for _, version := range []string{"1.0.0", "2.0.0"} {
		dir := filepath.Join(catalogDir, "plugin", "linter", "publishing", version)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir catalog: %v", err)
		}
		if err := os.WriteFile(filepath.Join(dir, "plugin.yaml"), []byte(linterAttachmentEntity(version)), 0o644); err != nil {
			t.Fatalf("write plugin entity: %v", err)
		}
	}
	cfg := "workspace:\n  catalog_dirs:\n    - catalog\nplugins:\n" + refs
	if err := os.WriteFile(filepath.Join(root, workspaceconfig.ConfigFilename), []byte(cfg), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	rw, err := workspaceconfig.Resolve(root, "")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	return rw
}

// componentWithOneFinding writes an entity that trips exactly the name-shape
// rule, so each dispatch of the rule set produces a recognisable finding.
func componentWithOneFinding(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "proposed.yaml")
	yaml := `apiVersion: aggrega.ai/v1alpha1
kind: Component
metadata:
  name: BadName
  version: 1.0.0
spec:
  capability: helm
  schema:
    values:
      type: object
`
	if err := os.WriteFile(path, []byte(yaml), 0o644); err != nil {
		t.Fatalf("write proposed: %v", err)
	}
	return path
}

// runWithAttachments lints componentWithOneFinding at the configured linter
// attachments and returns the findings the run reported.
func runWithAttachments(t *testing.T, refs string) []Finding {
	t.Helper()
	collect := &FindingCollector{}
	cfg := Config{
		EntityPath:     componentWithOneFinding(t),
		PolicyResolver: func(_ string, ws bool) bool { return ws },
		Workspace:      twoAttachmentWorkspace(t, refs),
		DiagOut:        io.Discard,
		Out:            io.Discard,
		Collect:        collect,
	}
	if err := RunEntity(cfg); err != nil {
		t.Fatalf("default-mode lint should not halt; got: %v", err)
	}
	if !collect.Reached() {
		t.Fatal("the run never reached reporting, so no dispatch happened")
	}
	return collect.Report().Findings
}

const (
	oneLinterAttachment  = "  linter:\n    - uri: linter/publishing:1.0.0\n"
	twoLinterAttachments = "  linter:\n" +
		"    - uri: linter/publishing:1.0.0\n" +
		"    - uri: linter/publishing:2.0.0\n      function: lint_rules\n"
)

// TestLintRunsEveryConfiguredLinterAttachment is the load-bearing proof that the
// gate consumes the whole attachment set. It is an A/B over one entity: the same
// lint runs against one configured attachment and then against two, and the
// second run's report carries BOTH dispatches' findings — the one-attachment
// sequence repeated, in authored order.
//
// The proof is the findings themselves, each one a diagnostic a Wasm guest
// actually produced and returned, rather than the size of any configuration
// list: a configured attachment that never ran contributes nothing to report, so
// the second half of the sequence cannot appear unless the second attachment was
// dispatched.
func TestLintRunsEveryConfiguredLinterAttachment(t *testing.T) {
	one := runWithAttachments(t, oneLinterAttachment)
	if len(one) == 0 {
		t.Fatal("the single-attachment run produced no findings, so this test cannot discriminate; the fixture entity must trip at least one rule")
	}
	two := runWithAttachments(t, twoLinterAttachments)

	if len(two) != 2*len(one) {
		t.Fatalf("two attachments must both be dispatched: one attachment reported %d finding(s), two reported %d (want %d)",
			len(one), len(two), 2*len(one))
	}
	for i, want := range one {
		if got := two[i]; got.RuleID != want.RuleID || got.Message != want.Message {
			t.Errorf("first attachment's finding %d changed: got rule %q %q; want rule %q %q",
				i, got.RuleID, got.Message, want.RuleID, want.Message)
		}
		if got := two[len(one)+i]; got.RuleID != want.RuleID || got.Message != want.Message {
			t.Errorf("second attachment's finding %d missing or wrong: got rule %q %q; want rule %q %q",
				i, got.RuleID, got.Message, want.RuleID, want.Message)
		}
	}
}

// TestLintDispatchesEachAttachmentAtItsOwnExport proves the pairing rather than
// the count: the export function is read off the attachment being dispatched, so
// a second attachment naming an export its guest does not have halts the run
// naming that export. Under a gate that read only the first attachment — plugin
// and export alike — this configuration would pass without ever mentioning it.
func TestLintDispatchesEachAttachmentAtItsOwnExport(t *testing.T) {
	refs := "  linter:\n" +
		"    - uri: linter/publishing:1.0.0\n" +
		"    - uri: linter/publishing:2.0.0\n      function: not_a_guest_export\n"
	cfg := Config{
		EntityPath:     componentWithOneFinding(t),
		PolicyResolver: func(_ string, ws bool) bool { return ws },
		Workspace:      twoAttachmentWorkspace(t, refs),
		DiagOut:        io.Discard,
		Out:            io.Discard,
	}
	err := RunEntity(cfg)
	if err == nil {
		t.Fatal("a second attachment naming a non-existent export must halt the run; got a clean pass")
	}
	if !strings.Contains(err.Error(), "not_a_guest_export") {
		t.Errorf("the halt must name the export the second attachment asked for; got: %v", err)
	}
}

// TestResolveLinterPluginsHaltsOnAnyAttachment holds the contract that a
// resolution failure at ANY attachment halts, not only at the first: the first
// attachment resolves and the second names no Plugin entity, and the whole
// resolution refuses. An attachment the operator configured and the gate cannot
// load is a loud guardrail rather than an attachment to quietly drop.
func TestResolveLinterPluginsHaltsOnAnyAttachment(t *testing.T) {
	wsCfg := workspaceconfig.Default()
	wsCfg.Plugins.Linter = []workspaceconfig.PluginRef{
		{URI: "linter/publishing:1.0.0"},
		{URI: "linter/absent:1.0.0"},
	}
	pluginByURI := func(uri string) (types.Plugin, bool) {
		if uri != "linter/publishing:1.0.0" {
			return types.Plugin{}, false
		}
		var wp types.Plugin
		wp.Metadata.Name = "publishing"
		wp.Metadata.PluginType = "linter"
		return wp, true
	}
	if _, err := ResolveLinterPlugins("", wsCfg, pluginpath.PinMaps{}, pluginByURI); err == nil {
		t.Fatal("a second attachment that resolves to nothing must halt the resolution; got nil")
	}
}

// TestResolveLinterPluginsSkipsAttachmentWithoutURI holds the set's own
// membership rule at the lint gate: an attachment that names no plugin URI
// identifies no plugin, so it is not in the set and its export function — which
// would name an entry point on a plugin nothing named — is not carried either.
// The socket is therefore unconfigured, and the built-in guest serves it under
// that guest's own default export.
func TestResolveLinterPluginsSkipsAttachmentWithoutURI(t *testing.T) {
	wsCfg := workspaceconfig.Default()
	wsCfg.Plugins.Linter = []workspaceconfig.PluginRef{{Function: "some_export"}}
	attachments, err := ResolveLinterPlugins("", wsCfg, pluginpath.PinMaps{}, nil)
	if err != nil {
		t.Fatalf("an attachment carrying no URI leaves the socket unconfigured, which resolves the built-in; got: %v", err)
	}
	if len(attachments) != 1 {
		t.Fatalf("want the single built-in fallback; got %d attachment(s)", len(attachments))
	}
	if attachments[0].Export != "" {
		t.Errorf("the export of a URI-less attachment must not be carried onto the built-in; got %q", attachments[0].Export)
	}
	if attachments[0].Plugin.URI != embedded.PublishingLinterURI {
		t.Errorf("want the built-in linter; got %q", attachments[0].Plugin.URI)
	}
}
