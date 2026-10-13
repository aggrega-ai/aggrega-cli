// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/aggrega-ai/aggrega-cli/internal/gencatalog"
	"github.com/aggrega-ai/aggrega-cli/internal/pluginops"
	"github.com/aggrega-ai/aggrega-cli/internal/publishops"
	"github.com/aggrega-ai/aggrega-cli/internal/wasm"
	"github.com/aggrega-ai/aggrega-cli/internal/wasm/embedded"
	"github.com/aggrega-ai/aggrega-cli/internal/wasm/entitysynth"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// embeddedGuestVersion returns the describe() self-report version an embedded
// guest carries — the single source the generator stamps onto its entity and
// uses in the output path.
func embeddedGuestVersion(t *testing.T, uri string) string {
	t.Helper()
	raw, ok := embedded.LookupDescriptor(uri)
	if !ok {
		t.Fatalf("no embedded descriptor for %s (build the guests first)", uri)
	}
	desc, _, err := entitysynth.ParseDescriptorJSON(raw)
	if err != nil {
		t.Fatalf("parse descriptor for %s: %v", uri, err)
	}
	if desc.Metadata == nil || desc.Metadata.Version == "" {
		t.Fatalf("embedded guest %s reports no describe() version", uri)
	}
	return desc.Metadata.Version
}

// TestGenCatalogRoundTripsThroughPushDryRun proves a generated entity survives
// the publish path's describe()-cross-check and that the .wasm push location is
// derived from the entity's bare identity (metadata.name). The small linter guest
// is used so the publish-time load stays within the default memory cap.
func TestGenCatalogRoundTripsThroughPushDryRun(t *testing.T) {
	wasmBytes, ok := embedded.Lookup(embedded.PublishingLinterURI)
	if !ok {
		t.Skip("publishing-linter guest not embedded in this build")
	}
	dir := t.TempDir()
	wasmPath := filepath.Join(dir, "linter.wasm")
	if err := os.WriteFile(wasmPath, wasmBytes, 0o644); err != nil {
		t.Fatalf("write wasm: %v", err)
	}
	const registry = "hub.aggrega.ai"
	overlayPath := filepath.Join(dir, "overlay.yaml")
	writeFixture(t, overlayPath, `# SPDX-License-Identifier: Apache-2.0
apiVersion: aggrega.ai/v1alpha1
kind: Plugin
metadata:
  name: publishing-linter
spec:
  runtime:
    maxMemoryPages: 4096
    timeoutMs: 300000
`)
	outDir := filepath.Join(dir, "out")
	opts := gencatalog.Options{PluginPath: wasmPath, OutputDir: outDir, OverlayPath: overlayPath}
	if err := gencatalog.Run(context.Background(), opts, &bytes.Buffer{}); err != nil {
		t.Fatalf("gen-catalog --plugin: %v", err)
	}

	version := embeddedGuestVersion(t, embedded.PublishingLinterURI)
	entPath := filepath.Join(outDir, "linter", "publishing-linter", version, "plugin.yaml")
	entityYAML, err := os.ReadFile(entPath)
	if err != nil {
		t.Fatalf("read generated entity: %v", err)
	}
	var entity types.Plugin
	if err := yaml.Unmarshal(entityYAML, &entity); err != nil {
		t.Fatalf("parse generated entity: %v", err)
	}

	// The .wasm push location is derived from the entity's identity coordinate —
	// the pluginType scope plus the bare metadata.name; the entity carries no
	// spec.plugin field.
	wantLocation := buildPushReference(registry, "Plugin", entity.Metadata.PluginType, entity.Metadata.Name)

	cfg := pushConfig{Workspace: resolveTempWorkspace(t, tempWorkspaceOptions{Root: dir}), PluginPath: wasmPath, DryRun: true}
	var out bytes.Buffer
	_, err = publishops.PushPlugin(t.Context(), &out, cfg, registry, false, entity.Metadata.Name, entity.Metadata.Version, entPath, entityYAML)
	// The generated entity is read against its schema and cross-checked against
	// the guest before the build-marker verdict, so reaching that verdict proves
	// the generated entity survived both.
	if !requirePublishedUnlessUncommitted(t, version, err) {
		return
	}
	if !strings.Contains(out.String(), wantLocation) {
		t.Errorf("dry-run output does not name the push location %q:\n%s", wantLocation, out.String())
	}
}

// TestGenCatalogProviderRoundTripsThroughPushDryRun proves a generated entity
// for a guest that DOES import host functions survives the publish path's
// host-function stamp check with no intervening recording step. The generator
// stamps the set the .wasm import section implies; push re-derives the set from
// the same binary and compares, so the dry-run cross-check passes rather than
// halting on a stamp mismatch.
func TestGenCatalogProviderRoundTripsThroughPushDryRun(t *testing.T) {
	const guestURI = "provider/helm"
	wasmBytes, ok := embedded.Lookup(guestURI)
	if !ok {
		t.Skipf("%s guest not embedded in this build", guestURI)
	}
	// Guard the premise: the chosen guest must actually import host functions,
	// otherwise the test would not exercise the path it targets.
	wantHF, err := wasm.DeriveHostFunctions(guestURI, wasmBytes)
	if err != nil {
		t.Fatalf("derive host functions for %s: %v", guestURI, err)
	}
	if len(wantHF) == 0 {
		t.Fatalf("%s imports no host functions; pick a guest that does", guestURI)
	}

	dir := t.TempDir()
	wasmPath := filepath.Join(dir, "helm.wasm")
	if err := os.WriteFile(wasmPath, wasmBytes, 0o644); err != nil {
		t.Fatalf("write wasm: %v", err)
	}
	const registry = "hub.aggrega.ai"
	overlayPath := filepath.Join(dir, "overlay.yaml")
	writeFixture(t, overlayPath, `# SPDX-License-Identifier: Apache-2.0
apiVersion: aggrega.ai/v1alpha1
kind: Plugin
metadata:
  name: helm
spec:
  runtime:
    maxMemoryPages: 4096
    timeoutMs: 300000
`)
	outDir := filepath.Join(dir, "out")
	opts := gencatalog.Options{PluginPath: wasmPath, OutputDir: outDir, OverlayPath: overlayPath}
	if err := gencatalog.Run(context.Background(), opts, &bytes.Buffer{}); err != nil {
		t.Fatalf("gen-catalog --plugin: %v", err)
	}

	version := embeddedGuestVersion(t, guestURI)
	entPath := filepath.Join(outDir, "provider", "helm", version, "plugin.yaml")
	entityYAML, err := os.ReadFile(entPath)
	if err != nil {
		t.Fatalf("read generated entity: %v", err)
	}
	var entity types.Plugin
	if err := yaml.Unmarshal(entityYAML, &entity); err != nil {
		t.Fatalf("parse generated entity: %v", err)
	}
	// The generator must have stamped the derived set — the exact record push
	// re-derives and compares against.
	if entity.Spec.Runtime == nil || !pluginops.CanonicalHostFunctionsEqual(entity.Spec.Runtime.HostFunctions, wantHF) {
		t.Fatalf("generated entity did not stamp the derived host functions: %+v, want %v", entity.Spec.Runtime, wantHF)
	}

	cfg := pushConfig{Workspace: resolveTempWorkspace(t, tempWorkspaceOptions{Root: dir}), PluginPath: wasmPath, DryRun: true}
	var out bytes.Buffer
	_, err = publishops.PushPlugin(t.Context(), &out, cfg, registry, false, entity.Metadata.Name, entity.Metadata.Version, entPath, entityYAML)
	if errors.Is(err, wasm.ErrWasmHostFunctionsStampMismatch()) {
		t.Fatalf("push re-derived a different host-function set than the generator stamped: %v", err)
	}
	// The host-function check runs ahead of the build-marker verdict, so reaching
	// that verdict proves the stamped set agreed.
	requirePublishedUnlessUncommitted(t, version, err)
}

// TestGenCatalogCommandMirrorRoundTrips proves the generator's command mapping
// is the exact reverse of the projection the publish verify re-derives. No
// embedded guest contributes commands today, so the mapping is exercised directly
// over a command-bearing descriptor: descriptorCommands maps the describe()
// command set onto the entity's machine-owned record in describe()-declared
// order, and pluginops.EntityCommandMirror maps it back; the two must agree under the same
// full-equality rule publish enforces, with order preserved element-wise.
func TestGenCatalogCommandMirrorRoundTrips(t *testing.T) {
	described := []wasm.PluginCLICommand{
		{Name: "zzz-first", Description: "declared first"},
		{Name: "aaa-second", Description: "declared second"},
		{Name: "mmm-third", Description: ""},
	}

	mapped := entitysynth.DescriptorCommands(described)
	if len(mapped) != len(described) {
		t.Fatalf("descriptorCommands mapped %d commands, want %d", len(mapped), len(described))
	}
	// Order and both fields must be preserved verbatim, not sorted or dropped.
	for i, c := range described {
		if mapped[i].Name != c.Name || mapped[i].Description != c.Description {
			t.Errorf("mapped[%d] = {%q,%q}, want {%q,%q}", i, mapped[i].Name, mapped[i].Description, c.Name, c.Description)
		}
	}

	// Stamp the mapping onto an entity and re-derive the mirror the way publish
	// does; the round-trip must be equal under the shared equality rule.
	entity := types.Plugin{Spec: types.PluginSpec{Commands: mapped}}
	if !wasm.CommandMirrorsEqual(pluginops.EntityCommandMirror(entity), described) {
		t.Errorf("round-trip mirror diverges: entity records %s, describe reports %s",
			wasm.FormatCommandMirror(pluginops.EntityCommandMirror(entity)), wasm.FormatCommandMirror(described))
	}

	// Empty describe() set yields an omitted (nil) field, which the mirror
	// comparison treats as equal to a plugin describing no commands.
	if got := entitysynth.DescriptorCommands(nil); got != nil {
		t.Errorf("entitysynth.DescriptorCommands(nil) = %v, want nil", got)
	}
	empty := types.Plugin{}
	if !wasm.CommandMirrorsEqual(pluginops.EntityCommandMirror(empty), nil) {
		t.Errorf("empty entity command mirror is not equal to an empty described set")
	}
}

// TestGenCatalogMergeStampsCommandMirror proves the merge stamps the command
// mirror from the descriptor onto the assembled entity, so a command-bearing
// guest yields a publish-ready record without a separate recording step.
func TestGenCatalogMergeStampsCommandMirror(t *testing.T) {
	desc := wasm.PluginDescriptor{
		PluginType: string(wasm.PluginTypeCLI),
		AbiVersion: wasm.ABIVersionV1Alpha1,
		Exports:    []wasm.PluginExport{{Name: wasm.CLIRunExport.Name()}},
		Commands: []wasm.PluginCLICommand{
			{Name: "greet", Description: "greet a target"},
			{Name: "farewell", Description: "bid a target goodbye"},
		},
		Metadata: &wasm.PluginDescriptorMeta{Version: "v0.0.0-test"},
	}
	overlay := types.Plugin{Metadata: types.PluginMetadata{Name: "cli-demo"}}

	entity, err := entitysynth.MergeForPublish(desc, "", nil, overlay, "cli/cli-demo")
	if err != nil {
		t.Fatalf("mergeGenCatalogEntity: %v", err)
	}
	// The entity version is the guest's describe() self-report version, carried verbatim.
	if entity.Metadata.Version != "v0.0.0-test" {
		t.Errorf("entity version = %q, want the guest's describe() version %q", entity.Metadata.Version, "v0.0.0-test")
	}
	if !wasm.CommandMirrorsEqual(pluginops.EntityCommandMirror(entity), desc.Commands) {
		t.Errorf("entity command mirror diverges from describe(): entity records %s, describe reports %s",
			wasm.FormatCommandMirror(pluginops.EntityCommandMirror(entity)), wasm.FormatCommandMirror(desc.Commands))
	}
	// Order preserved element-wise.
	for i, c := range desc.Commands {
		if entity.Spec.Commands[i].Name != c.Name || entity.Spec.Commands[i].Description != c.Description {
			t.Errorf("commands[%d] = {%q,%q}, want {%q,%q}", i,
				entity.Spec.Commands[i].Name, entity.Spec.Commands[i].Description, c.Name, c.Description)
		}
	}
}
