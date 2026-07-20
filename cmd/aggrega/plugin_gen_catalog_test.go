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

	"github.com/aggrega-ai/aggrega-cli/internal/wasm"
	"github.com/aggrega-ai/aggrega-cli/internal/wasm/embedded"
	"github.com/aggrega-ai/aggrega-cli/internal/wasm/entitysynth"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// writeEmbeddedOverlays authors one minimal overlay per embedded guest into dir,
// keyed to its guest by spec.plugin (the embedded URI). The overlay carries only
// what the generator does not derive — identity and runtime limits — so the test
// exercises the merge without hand-authoring provider blocks. The metadata.name
// is the guest's final URI segment, unique across the embedded set.
func writeEmbeddedOverlays(t *testing.T, dir string) {
	t.Helper()
	for _, uri := range embedded.URIs() {
		leaf := uri
		if i := strings.LastIndex(leaf, "/"); i >= 0 {
			leaf = leaf[i+1:]
		}
		overlay := `# SPDX-License-Identifier: Apache-2.0
apiVersion: aggrega.ai/v1alpha1
kind: Plugin
metadata:
  name: ` + leaf + `
spec:
  runtime:
    maxMemoryPages: 4096
    timeoutMs: 300000
`
		writeFixture(t, filepath.Join(dir, leaf+".yaml"), overlay)
	}
}

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

// readGeneratedEntity reads and parses a generated Plugin entity written under
// out/<name>/<version>/plugin.yaml.
func readGeneratedEntity(t *testing.T, outDir, name, version string) types.Plugin {
	t.Helper()
	path := filepath.Join(outDir, filepath.FromSlash(name), version, genCatalogPluginFilename)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read generated entity %s: %v", path, err)
	}
	if !bytes.HasPrefix(raw, []byte(genCatalogHeader)) {
		t.Errorf("generated entity %s missing generated-output banner", path)
	}
	var entity types.Plugin
	if err := yaml.Unmarshal(raw, &entity); err != nil {
		t.Fatalf("parse generated entity %s: %v", path, err)
	}
	return entity
}

// TestGenCatalogFromEmbeddedContractFieldsMatchDescribe drives --from-embedded
// over the real embedded guests and asserts each generated entity's contract
// fields (pluginType, abiVersion, targets, exports, capabilities) equal the
// guest's describe() self-report, the overlay-sourced identity and runtime are
// carried verbatim, and the emitted version equals the guest's describe()
// self-report version.
func TestGenCatalogFromEmbeddedContractFieldsMatchDescribe(t *testing.T) {
	overlayDir := t.TempDir()
	outDir := t.TempDir()
	writeEmbeddedOverlays(t, overlayDir)

	opts := genCatalogOptions{fromEmbedded: true, outputDir: outDir, overlayPath: overlayDir}
	if err := genCatalogRun(context.Background(), opts, &bytes.Buffer{}); err != nil {
		t.Fatalf("gen-catalog --from-embedded: %v", err)
	}

	for _, uri := range embedded.URIs() {
		name := uri
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
		wantVersion := desc.Metadata.Version

		entity := readGeneratedEntity(t, outDir, name, wantVersion)

		bareName := uri
		if i := strings.LastIndex(bareName, "/"); i >= 0 {
			bareName = bareName[i+1:]
		}
		if entity.Metadata.Version != wantVersion {
			t.Errorf("%s: metadata.version = %q, want describe() version %q", name, entity.Metadata.Version, wantVersion)
		}
		if entity.Metadata.Name != bareName {
			t.Errorf("%s: metadata.name = %q, want bare identity %q", name, entity.Metadata.Name, bareName)
		}
		if entity.Metadata.PluginType != string(desc.PluginType) {
			t.Errorf("%s: pluginType = %q, want %q", name, entity.Metadata.PluginType, desc.PluginType)
		}
		if entity.Spec.AbiVersion != desc.AbiVersion {
			t.Errorf("%s: abiVersion = %q, want %q", name, entity.Spec.AbiVersion, desc.AbiVersion)
		}
		if strings.Join(entity.Spec.Capabilities, ",") != strings.Join(desc.Capabilities, ",") {
			t.Errorf("%s: targets = %v, want %v", name, entity.Spec.Capabilities, desc.Capabilities)
		}
		if len(entity.Spec.Exports) != len(desc.Exports) {
			t.Fatalf("%s: %d exports, want %d (from describe)", name, len(entity.Spec.Exports), len(desc.Exports))
		}
		for i, e := range desc.Exports {
			if entity.Spec.Exports[i].Name != e.Name {
				t.Errorf("%s: export[%d] = %q, want %q", name, i, entity.Spec.Exports[i].Name, e.Name)
			}
		}
		// Runtime limits are carried verbatim from the overlay.
		if entity.Spec.Runtime == nil || entity.Spec.Runtime.MaxMemoryPages != 4096 {
			t.Errorf("%s: runtime not carried from overlay: %+v", name, entity.Spec.Runtime)
		}
		// The machine-owned host-function set is derived from the guest's own
		// .wasm import section and stamped by the generator, so the generated
		// record must equal a fresh derive over the same bytes. A guest that
		// imports nothing (the publishing linter) records an absent/empty set.
		wasmBytes, ok := embedded.Lookup(uri)
		if !ok {
			t.Fatalf("no embedded bytes for %s", uri)
		}
		wantHF, err := wasm.DeriveHostFunctions(uri, wasmBytes)
		if err != nil {
			t.Fatalf("derive host functions for %s: %v", uri, err)
		}
		var gotHF []string
		if entity.Spec.Runtime != nil {
			gotHF = entity.Spec.Runtime.HostFunctions
		}
		if !canonicalHostFunctionsEqual(gotHF, wantHF) {
			t.Errorf("%s: stamped hostFunctions = %v, want derived %v", name, gotHF, wantHF)
		}
		if len(wantHF) == 0 && len(gotHF) != 0 {
			t.Errorf("%s: guest imports no host functions but entity records %v", name, gotHF)
		}
		// Contract presence follows the guest self-report: providers carry a
		// render-behavior contract block on the provider block; the cross-cutting
		// linter carries none.
		var contract *types.ProviderContract
		if entity.Spec.Provider != nil {
			contract = entity.Spec.Provider.Contract
		}
		if desc.Contract != nil && !entitysynth.IsZeroContract(*desc.Contract) {
			if contract == nil {
				t.Errorf("%s: contract missing though describe() reports it", name)
			} else if contract.BoundaryAssembly != desc.Contract.BoundaryAssembly {
				t.Errorf("%s: boundaryAssembly = %q, want %q", name, contract.BoundaryAssembly, desc.Contract.BoundaryAssembly)
			}
		} else if contract != nil {
			t.Errorf("%s: contract emitted though describe() reports none", name)
		}
	}
}

// TestGenCatalogIdempotent asserts that regenerating over unchanged inputs
// produces byte-identical output — the deterministic-output contract GitOps and
// diff-based review rely on.
func TestGenCatalogIdempotent(t *testing.T) {
	overlayDir := t.TempDir()
	writeEmbeddedOverlays(t, overlayDir)
	outA := t.TempDir()
	outB := t.TempDir()

	opts := genCatalogOptions{fromEmbedded: true, overlayPath: overlayDir}
	optsA := opts
	optsA.outputDir = outA
	optsB := opts
	optsB.outputDir = outB
	if err := genCatalogRun(context.Background(), optsA, &bytes.Buffer{}); err != nil {
		t.Fatalf("first run: %v", err)
	}
	if err := genCatalogRun(context.Background(), optsB, &bytes.Buffer{}); err != nil {
		t.Fatalf("second run: %v", err)
	}

	for _, uri := range embedded.URIs() {
		rel := filepath.Join(filepath.FromSlash(uri), embeddedGuestVersion(t, uri), genCatalogPluginFilename)
		a, err := os.ReadFile(filepath.Join(outA, rel))
		if err != nil {
			t.Fatalf("read A %s: %v", rel, err)
		}
		b, err := os.ReadFile(filepath.Join(outB, rel))
		if err != nil {
			t.Fatalf("read B %s: %v", rel, err)
		}
		if !bytes.Equal(a, b) {
			t.Errorf("%s: re-run output differs", rel)
		}
	}
}

// TestGenCatalogDescriptionOmittedWhenAbsent asserts the plugin-level
// description is omitted cleanly when the guest reports none. No embedded guest
// reports one today, so every generated entity must carry no metadata.description
// — proving the command builds and runs correctly ahead of that field landing.
func TestGenCatalogDescriptionOmittedWhenAbsent(t *testing.T) {
	overlayDir := t.TempDir()
	outDir := t.TempDir()
	writeEmbeddedOverlays(t, overlayDir)

	opts := genCatalogOptions{fromEmbedded: true, outputDir: outDir, overlayPath: overlayDir}
	if err := genCatalogRun(context.Background(), opts, &bytes.Buffer{}); err != nil {
		t.Fatalf("gen-catalog: %v", err)
	}
	for _, uri := range embedded.URIs() {
		raw, _ := embedded.LookupDescriptor(uri)
		desc, description, err := entitysynth.ParseDescriptorJSON(raw)
		if err != nil {
			t.Fatalf("parse descriptor %s: %v", uri, err)
		}
		if desc.Metadata == nil || desc.Metadata.Version == "" {
			t.Fatalf("embedded guest %s reports no describe() version", uri)
		}
		entity := readGeneratedEntity(t, outDir, uri, desc.Metadata.Version)
		if description == "" && entity.Metadata.Description != "" {
			t.Errorf("%s: description emitted %q though guest reports none", uri, entity.Metadata.Description)
		}
		if description != "" && entity.Metadata.Description != description {
			t.Errorf("%s: description = %q, want guest value %q", uri, entity.Metadata.Description, description)
		}
	}
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
	opts := genCatalogOptions{pluginPath: wasmPath, outputDir: outDir, overlayPath: overlayPath}
	if err := genCatalogRun(context.Background(), opts, &bytes.Buffer{}); err != nil {
		t.Fatalf("gen-catalog --plugin: %v", err)
	}

	version := embeddedGuestVersion(t, embedded.PublishingLinterURI)
	entPath := filepath.Join(outDir, "linter", "publishing-linter", version, genCatalogPluginFilename)
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

	cfg := pushConfig{PluginPath: wasmPath, DryRun: true}
	var out bytes.Buffer
	if err := pushPlugin(&out, cfg, registry, false, entity.Metadata.Name, entity.Metadata.Version, entPath, entityYAML); err != nil {
		t.Fatalf("push --plugin --dry-run cross-check failed: %v", err)
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
	opts := genCatalogOptions{pluginPath: wasmPath, outputDir: outDir, overlayPath: overlayPath}
	if err := genCatalogRun(context.Background(), opts, &bytes.Buffer{}); err != nil {
		t.Fatalf("gen-catalog --plugin: %v", err)
	}

	version := embeddedGuestVersion(t, guestURI)
	entPath := filepath.Join(outDir, "provider", "helm", version, genCatalogPluginFilename)
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
	if entity.Spec.Runtime == nil || !canonicalHostFunctionsEqual(entity.Spec.Runtime.HostFunctions, wantHF) {
		t.Fatalf("generated entity did not stamp the derived host functions: %+v, want %v", entity.Spec.Runtime, wantHF)
	}

	cfg := pushConfig{PluginPath: wasmPath, DryRun: true}
	var out bytes.Buffer
	if err := pushPlugin(&out, cfg, registry, false, entity.Metadata.Name, entity.Metadata.Version, entPath, entityYAML); err != nil {
		if errors.Is(err, wasm.ErrWasmHostFunctionsStampMismatch) {
			t.Fatalf("push re-derived a different host-function set than the generator stamped: %v", err)
		}
		t.Fatalf("push --plugin --dry-run cross-check failed: %v", err)
	}
}

// TestGenCatalogCommandMirrorRoundTrips proves the generator's command mapping
// is the exact reverse of the projection the publish verify re-derives. No
// embedded guest contributes commands today, so the mapping is exercised directly
// over a command-bearing descriptor: descriptorCommands maps the describe()
// command set onto the entity's machine-owned record in describe()-declared
// order, and entityCommandMirror maps it back; the two must agree under the same
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
	if !wasm.CommandMirrorsEqual(entityCommandMirror(entity), described) {
		t.Errorf("round-trip mirror diverges: entity records %s, describe reports %s",
			wasm.FormatCommandMirror(entityCommandMirror(entity)), wasm.FormatCommandMirror(described))
	}

	// Empty describe() set yields an omitted (nil) field, which the mirror
	// comparison treats as equal to a plugin describing no commands.
	if got := entitysynth.DescriptorCommands(nil); got != nil {
		t.Errorf("entitysynth.DescriptorCommands(nil) = %v, want nil", got)
	}
	empty := types.Plugin{}
	if !wasm.CommandMirrorsEqual(entityCommandMirror(empty), nil) {
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
		Exports:    []wasm.PluginExport{{Name: wasm.CLIRunExportName}},
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
	if !wasm.CommandMirrorsEqual(entityCommandMirror(entity), desc.Commands) {
		t.Errorf("entity command mirror diverges from describe(): entity records %s, describe reports %s",
			wasm.FormatCommandMirror(entityCommandMirror(entity)), wasm.FormatCommandMirror(desc.Commands))
	}
	// Order preserved element-wise.
	for i, c := range desc.Commands {
		if entity.Spec.Commands[i].Name != c.Name || entity.Spec.Commands[i].Description != c.Description {
			t.Errorf("commands[%d] = {%q,%q}, want {%q,%q}", i,
				entity.Spec.Commands[i].Name, entity.Spec.Commands[i].Description, c.Name, c.Description)
		}
	}
}

// TestMergeForPublishRequiresGuestVersion asserts the merge halts when the
// descriptor reports no build-provenance version — proving the entity version has
// a single source (the guest's describe() self-report) with no fallback. A guest
// with no metadata and a guest with an empty version string both halt.
func TestMergeForPublishRequiresGuestVersion(t *testing.T) {
	base := wasm.PluginDescriptor{
		PluginType: string(wasm.PluginTypeCLI),
		AbiVersion: wasm.ABIVersionV1Alpha1,
		Exports:    []wasm.PluginExport{{Name: wasm.CLIRunExportName}},
	}
	overlay := types.Plugin{Metadata: types.PluginMetadata{Name: "cli-demo"}}

	cases := []struct {
		name string
		meta *wasm.PluginDescriptorMeta
	}{
		{"no metadata", nil},
		{"empty version", &wasm.PluginDescriptorMeta{Version: "  "}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			desc := base
			desc.Metadata = tc.meta
			_, err := entitysynth.MergeForPublish(desc, "", nil, overlay, "cli/cli-demo")
			if err == nil {
				t.Fatal("expected a halt when the descriptor reports no version")
			}
			if !strings.Contains(err.Error(), "version") {
				t.Errorf("error = %v, want a missing-version halt", err)
			}
		})
	}
}

// TestGenCatalogMissingOverlayHalts asserts an embedded guest with no matching
// overlay halts loudly rather than inventing an identity or silently skipping.
func TestGenCatalogMissingOverlayHalts(t *testing.T) {
	overlayDir := t.TempDir() // empty: no overlays
	outDir := t.TempDir()
	opts := genCatalogOptions{fromEmbedded: true, outputDir: outDir, overlayPath: overlayDir}
	err := genCatalogRun(context.Background(), opts, &bytes.Buffer{})
	if err == nil {
		t.Fatal("expected an error when no overlay matches an embedded guest")
	}
	if !strings.Contains(err.Error(), "no overlay") {
		t.Errorf("error = %v, want a missing-overlay halt", err)
	}
}

// TestGenCatalogRejectsInvalidWorkloadSchema asserts the emitter refuses to write
// a Plugin entity whose spec.provider.workloadSchema is not valid YAML — an
// unquoted colon-space inside a plain scalar — rather than shipping a schema the
// catalog loader would later reject. The guard reuses the loader's own validator,
// so the generator can never emit a workloadSchema the load-time gate rejects.
func TestGenCatalogRejectsInvalidWorkloadSchema(t *testing.T) {
	uris := embedded.URIs()
	if len(uris) == 0 {
		t.Skip("no embedded guests in this build")
	}
	overlayDir := t.TempDir()
	outDir := t.TempDir()
	writeEmbeddedOverlays(t, overlayDir)

	// Overwrite one guest's overlay with a provider block whose workloadSchema
	// carries an unquoted "form: stateful" — a colon-space that breaks the plain
	// scalar when the loader re-parses the extracted body as YAML.
	uri := uris[0]
	name := uri
	if i := strings.LastIndex(name, "/"); i >= 0 {
		name = name[i+1:]
	}
	invalid := `# SPDX-License-Identifier: Apache-2.0
apiVersion: aggrega.ai/v1alpha1
kind: Plugin
metadata:
  name: ` + name + `
spec:
  runtime:
    maxMemoryPages: 4096
    timeoutMs: 300000
  provider:
    workloadSchema: |
      volumes:
        type: object
        description: persistentTemplate requires form: stateful and renders templates.
`
	writeFixture(t, filepath.Join(overlayDir, name+".yaml"), invalid)

	opts := genCatalogOptions{fromEmbedded: true, outputDir: outDir, overlayPath: overlayDir}
	err := genCatalogRun(context.Background(), opts, &bytes.Buffer{})
	if err == nil {
		t.Fatal("expected gen-catalog to reject an invalid workloadSchema")
	}
	if !strings.Contains(err.Error(), "workloadSchema") {
		t.Errorf("error = %v, want a workloadSchema-invalid halt", err)
	}
	// The invalid entity must not have been written.
	version := embeddedGuestVersion(t, uri)
	path := filepath.Join(outDir, filepath.FromSlash(name), version, genCatalogPluginFilename)
	if _, statErr := os.Stat(path); statErr == nil {
		t.Errorf("invalid entity was written to %s despite the guard", path)
	}
}

// TestGenCatalogFlagValidation covers the mutually-exclusive and required-flag
// guards.
func TestGenCatalogFlagValidation(t *testing.T) {
	cases := []struct {
		name string
		opts genCatalogOptions
		want string
	}{
		{"both modes", genCatalogOptions{fromEmbedded: true, pluginPath: "x.wasm", outputDir: "o", overlayPath: "v"}, "mutually exclusive"},
		{"no mode", genCatalogOptions{outputDir: "o", overlayPath: "v"}, "one of --from-embedded or --plugin"},
		{"no output", genCatalogOptions{fromEmbedded: true, overlayPath: "v"}, "--output"},
		{"no overlay", genCatalogOptions{fromEmbedded: true, outputDir: "o"}, "--overlay"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := genCatalogRun(context.Background(), tc.opts, &bytes.Buffer{})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("err = %v, want containing %q", err, tc.want)
			}
		})
	}
}
