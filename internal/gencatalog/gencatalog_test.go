// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package gencatalog

import (
	"bytes"
	"context"
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

// writeFixture writes content to path, creating parent directories as needed.
func writeFixture(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

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
// out/<pluginType>/<name>/<version>/plugin.yaml. Its name argument carries the
// slashed <pluginType>/<name> coordinate rather than a bare name — which is why
// the path is assembled through filepath.FromSlash.
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

	opts := Options{FromEmbedded: true, OutputDir: outDir, OverlayPath: overlayDir}
	if err := Run(context.Background(), opts, &bytes.Buffer{}); err != nil {
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
		if strings.Join(entity.DeclaredCapabilities(), ",") != strings.Join(desc.Capabilities, ",") {
			t.Errorf("%s: capabilities = %v, want %v", name, entity.DeclaredCapabilities(), desc.Capabilities)
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
		if !wasm.HostFunctionsEqual(gotHF, wantHF) {
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

	opts := Options{FromEmbedded: true, OverlayPath: overlayDir}
	optsA := opts
	optsA.OutputDir = outA
	optsB := opts
	optsB.OutputDir = outB
	if err := Run(context.Background(), optsA, &bytes.Buffer{}); err != nil {
		t.Fatalf("first run: %v", err)
	}
	if err := Run(context.Background(), optsB, &bytes.Buffer{}); err != nil {
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

	opts := Options{FromEmbedded: true, OutputDir: outDir, OverlayPath: overlayDir}
	if err := Run(context.Background(), opts, &bytes.Buffer{}); err != nil {
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
	opts := Options{FromEmbedded: true, OutputDir: outDir, OverlayPath: overlayDir}
	err := Run(context.Background(), opts, &bytes.Buffer{})
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

	opts := Options{FromEmbedded: true, OutputDir: outDir, OverlayPath: overlayDir}
	err := Run(context.Background(), opts, &bytes.Buffer{})
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
		opts Options
		want string
	}{
		{"both modes", Options{FromEmbedded: true, PluginPath: "x.wasm", OutputDir: "o", OverlayPath: "v"}, "mutually exclusive"},
		{"no mode", Options{OutputDir: "o", OverlayPath: "v"}, "one of --from-embedded or --plugin"},
		{"no output", Options{FromEmbedded: true, OverlayPath: "v"}, "--output"},
		{"no overlay", Options{FromEmbedded: true, OutputDir: "o"}, "--overlay"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := Run(context.Background(), tc.opts, &bytes.Buffer{})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("err = %v, want containing %q", err, tc.want)
			}
		})
	}
}
