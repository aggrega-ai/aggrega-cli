// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/aggrega-ai/aggrega-cli/internal/wasm/embedded"
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
  name: ` + uri + `
spec:
  runtime:
    maxMemoryPages: 4096
    timeoutMs: 300000
`
		writeFixture(t, filepath.Join(dir, leaf+".yaml"), overlay)
	}
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
// carried verbatim, and the emitted version equals the binary's own build
// version.
func TestGenCatalogFromEmbeddedContractFieldsMatchDescribe(t *testing.T) {
	overlayDir := t.TempDir()
	outDir := t.TempDir()
	writeEmbeddedOverlays(t, overlayDir)

	opts := genCatalogOptions{fromEmbedded: true, outputDir: outDir, overlayPath: overlayDir}
	if err := genCatalogRun(context.Background(), opts, &bytes.Buffer{}); err != nil {
		t.Fatalf("gen-catalog --from-embedded: %v", err)
	}

	wantVersion := collectVersionInfo().Version
	for _, uri := range embedded.URIs() {
		name := uri
		raw, ok := embedded.LookupDescriptor(uri)
		if !ok {
			t.Fatalf("no embedded descriptor for %s (build the guests first)", uri)
		}
		desc, _, err := parseDescriptorJSON(raw)
		if err != nil {
			t.Fatalf("parse descriptor for %s: %v", uri, err)
		}

		entity := readGeneratedEntity(t, outDir, name, wantVersion)

		if entity.Metadata.Version != wantVersion {
			t.Errorf("%s: metadata.version = %q, want binary version %q", name, entity.Metadata.Version, wantVersion)
		}
		if entity.Metadata.Name != uri {
			t.Errorf("%s: metadata.name = %q, want bare identity %q", name, entity.Metadata.Name, uri)
		}
		if entity.Spec.PluginType != string(desc.PluginType) {
			t.Errorf("%s: pluginType = %q, want %q", name, entity.Spec.PluginType, desc.PluginType)
		}
		if entity.Spec.AbiVersion != desc.ABIVersion {
			t.Errorf("%s: abiVersion = %q, want %q", name, entity.Spec.AbiVersion, desc.ABIVersion)
		}
		if strings.Join(entity.Spec.Targets, ",") != strings.Join(desc.CompatibleProviders, ",") {
			t.Errorf("%s: targets = %v, want %v", name, entity.Spec.Targets, desc.CompatibleProviders)
		}
		if len(entity.Spec.Exports) != len(desc.Exports) {
			t.Fatalf("%s: %d exports, want %d (from describe)", name, len(entity.Spec.Exports), len(desc.Exports))
		}
		for i, e := range desc.Exports {
			if entity.Spec.Exports[i].Name != e.Name {
				t.Errorf("%s: export[%d] = %q, want %q", name, i, entity.Spec.Exports[i].Name, e.Name)
			}
		}
		// Runtime limits are carried verbatim from the overlay; the machine-owned
		// host-function set is never authored by the generator.
		if entity.Spec.Runtime == nil || entity.Spec.Runtime.MaxMemoryPages != 4096 {
			t.Errorf("%s: runtime not carried from overlay: %+v", name, entity.Spec.Runtime)
		}
		if entity.Spec.Runtime != nil && len(entity.Spec.Runtime.HostFunctions) != 0 {
			t.Errorf("%s: generator must not author runtime.hostFunctions: %v", name, entity.Spec.Runtime.HostFunctions)
		}
		// Contract presence follows the guest self-report: providers carry a
		// render-behavior contract block on the provider block; the cross-cutting
		// linter carries none.
		var contract *types.ProviderContract
		if entity.Spec.Provider != nil {
			contract = entity.Spec.Provider.Contract
		}
		if !isZeroContract(desc.Contract) {
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

	version := collectVersionInfo().Version
	for _, uri := range embedded.URIs() {
		rel := filepath.Join(filepath.FromSlash(uri), version, genCatalogPluginFilename)
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
	version := collectVersionInfo().Version
	for _, uri := range embedded.URIs() {
		raw, _ := embedded.LookupDescriptor(uri)
		_, description, err := parseDescriptorJSON(raw)
		if err != nil {
			t.Fatalf("parse descriptor %s: %v", uri, err)
		}
		entity := readGeneratedEntity(t, outDir, uri, version)
		if description == "" && entity.Metadata.Description != "" {
			t.Errorf("%s: description emitted %q though guest reports none", uri, entity.Metadata.Description)
		}
		if description != "" && entity.Metadata.Description != description {
			t.Errorf("%s: description = %q, want guest value %q", uri, entity.Metadata.Description, description)
		}
	}
}

// TestGenCatalogRoundTripsThroughPushDryRun proves a generated entity survives
// the publish path's describe()-cross-check and that, when the overlay authors an
// identity consistent with the socket-encoded name, spec.plugin equals the .wasm
// push location. The small linter guest is used so the publish-time load stays
// within the default memory cap.
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
  name: linter/publishing-linter
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

	version := collectVersionInfo().Version
	entPath := filepath.Join(outDir, "linter", "publishing-linter", version, genCatalogPluginFilename)
	entityYAML, err := os.ReadFile(entPath)
	if err != nil {
		t.Fatalf("read generated entity: %v", err)
	}
	var entity types.Plugin
	if err := yaml.Unmarshal(entityYAML, &entity); err != nil {
		t.Fatalf("parse generated entity: %v", err)
	}

	// The .wasm push location is derived from the entity's bare identity
	// (metadata.name); the entity carries no spec.plugin field.
	wantLocation := buildPushReference(registry, "Plugin", "", entity.Metadata.Name)

	cfg := pushConfig{PluginPath: wasmPath, DryRun: true}
	var out bytes.Buffer
	if err := pushPlugin(&out, cfg, registry, false, entity.Metadata.Name, entity.Metadata.Version, entityYAML); err != nil {
		t.Fatalf("push --plugin --dry-run cross-check failed: %v", err)
	}
	if !strings.Contains(out.String(), wantLocation) {
		t.Errorf("dry-run output does not name the push location %q:\n%s", wantLocation, out.String())
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
  name: ` + uri + `
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
	version := collectVersionInfo().Version
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
