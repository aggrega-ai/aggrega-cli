// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package mcp

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2/content"
	"oras.land/oras-go/v2/content/memory"

	"github.com/aggrega-ai/aggrega-cli/internal/publishops"
	"github.com/aggrega-ai/aggrega-cli/internal/wasmtest"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
)

// The fixture bodies below are a deliberate near-duplicate of the ones in the sibling
// test package (internal/serve): the two packages share no test helper, and a helper package
// for these constants would hold more than it saves.
const (
	pluginEntityBody = `apiVersion: aggrega.ai/v1beta1
kind: Plugin
metadata:
  name: patch
  pluginType: pipeline-step
  version: "1.0.0"
spec:
  abiVersion: v1beta1
  pipelineStep:
    capabilities:
      - helm
  exports:
    - name: step
      operates: patch
      scope: own
`
	noticesBody = "# Third-party notices\n\nexample-dependency 1.0 (MIT)\n"
	sbomBody    = `{"spdxVersion":"SPDX-2.3","name":"guest"}`
)

// pluginNoticesFixture stands up a workspace holding a Plugin entity, its
// executable, a notices file, a bill-of-materials file and an empty file.
func pluginNoticesFixture(t *testing.T) (*server, string) {
	t.Helper()
	s, root := publishFixture(t, defaultRegistryWorkspace)
	wasm, err := os.ReadFile(wasmtest.EnsureFixtureBuilt(t, "frp-step-go.wasm"))
	if err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string][]byte{
		filepath.Join("1.0.0", "entity.yaml"): []byte(pluginEntityBody),
		"guest.wasm":                          wasm,
		"NOTICES.md":                          []byte(noticesBody),
		"sbom.spdx.json":                      []byte(sbomBody),
		"empty":                               nil,
	} {
		if err := os.WriteFile(filepath.Join(root, name), body, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return s, root
}

// TestPublishEntityToolPublishesAPluginsNoticesAndSBOM publishes a Plugin through
// the tool's argument projection with both files: the manifest carries the notices
// layer after the executable and one SPDX referrer carries the file.
func TestPublishEntityToolPublishesAPluginsNoticesAndSBOM(t *testing.T) {
	s, root := pluginNoticesFixture(t)
	store := memory.New()
	cfg, refusal := s.publishEntityConfig(publishEntityArgs{
		Entity: "1.0.0/entity.yaml", Plugin: "guest.wasm",
		PluginNotices: "NOTICES.md", PluginSBOM: "sbom.spdx.json", WorkspaceRoot: root,
	})
	if refusal != "" {
		t.Fatalf("publishEntityConfig: %s", refusal)
	}
	cfg.PushTarget = store
	if _, err := runPublishEntity(t.Context(), s.servedWorkspace(), cfg); err != nil {
		t.Fatalf("publish: %v", err)
	}

	desc, err := store.Resolve(t.Context(), "1.0.0")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := content.FetchAll(t.Context(), store, desc)
	if err != nil {
		t.Fatal(err)
	}
	var m ocispec.Manifest
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	if len(m.Layers) != 2 || m.Layers[0].MediaType != oci.MediaTypeWasm || m.Layers[1].MediaType != oci.MediaTypePluginNotices {
		t.Fatalf("manifest layers = %+v, want [wasm, notices]", m.Layers)
	}
	boms, err := oci.ListReferrers(t.Context(), store, "1.0.0", oci.MediaTypeSPDXJSON)
	if err != nil || len(boms) != 1 {
		t.Fatalf("spdx referrers = %d, err %v; want exactly one", len(boms), err)
	}
	payload, err := oci.FetchReferrerPayload(t.Context(), store, boms[0])
	if err != nil || string(payload) != sbomBody {
		t.Fatalf("referrer payload = %q, err %v; want the file", payload, err)
	}
}

// TestPublishEntityToolRefusesAPluginsUnpublishableNoticesAndSBOM gives one fault
// per row and asserts the core's refusal, naming the flag, reaches the caller.
func TestPublishEntityToolRefusesAPluginsUnpublishableNoticesAndSBOM(t *testing.T) {
	s, root := pluginNoticesFixture(t)
	rows := []struct {
		name string
		args publishEntityArgs
		flag string
	}{
		{"notices without an executable", publishEntityArgs{PluginNotices: "NOTICES.md"}, "--plugin-notices"},
		{"sbom without an executable", publishEntityArgs{PluginSBOM: "sbom.spdx.json"}, "--plugin-sbom"},
		{"missing notices", publishEntityArgs{Plugin: "guest.wasm", PluginNotices: "absent"}, "--plugin-notices"},
		{"missing sbom", publishEntityArgs{Plugin: "guest.wasm", PluginSBOM: "absent"}, "--plugin-sbom"},
		{"empty notices", publishEntityArgs{Plugin: "guest.wasm", PluginNotices: "empty"}, "--plugin-notices"},
		{"empty sbom", publishEntityArgs{Plugin: "guest.wasm", PluginSBOM: "empty"}, "--plugin-sbom"},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			store := memory.New()
			row.args.Entity, row.args.WorkspaceRoot = "1.0.0/entity.yaml", root
			cfg, refusal := s.publishEntityConfig(row.args)
			if refusal != "" {
				t.Fatalf("publishEntityConfig: %s", refusal)
			}
			cfg.PushTarget = store
			_, err := runPublishEntity(t.Context(), s.servedWorkspace(), cfg)
			var pie *publishops.PluginInputError
			if !errors.As(err, &pie) {
				t.Fatalf("error = %v, want a PluginInputError", err)
			}
			if pie.Flag() != row.flag {
				t.Errorf("refusal names %q, want %q", pie.Flag(), row.flag)
			}
			if _, exists, _ := oci.Resolve(t.Context(), store, "1.0.0"); exists {
				t.Error("a refused publication reached the registry")
			}
		})
	}
}

// TestPublishEntityToolConfinesNoticesAndSBOMAsItConfinesThePlugin proves a path
// leading out of the workspace is refused for each new argument in the words it
// is refused for plugin, naming that argument.
func TestPublishEntityToolConfinesNoticesAndSBOMAsItConfinesThePlugin(t *testing.T) {
	s, root := pluginNoticesFixture(t)
	s.transport = transportHTTP
	_, want := s.publishEntityConfig(publishEntityArgs{
		Entity: "1.0.0/entity.yaml", Plugin: "../elsewhere", WorkspaceRoot: root,
	})
	if want == "" {
		t.Fatal("a plugin path leaving the workspace was not refused")
	}
	for _, field := range []string{"pluginNotices", "pluginSbom"} {
		args := publishEntityArgs{Entity: "1.0.0/entity.yaml", Plugin: "guest.wasm", WorkspaceRoot: root}
		if field == "pluginNotices" {
			args.PluginNotices = "../elsewhere"
		} else {
			args.PluginSBOM = "../elsewhere"
		}
		_, got := s.publishEntityConfig(args)
		if got != strings.ReplaceAll(want, "'plugin'", "'"+field+"'") {
			t.Errorf("%s refusal = %q, want plugin's refusal naming %s: %q", field, got, field, want)
		}
	}
}
