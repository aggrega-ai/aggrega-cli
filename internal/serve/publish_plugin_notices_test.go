// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package serve

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2/content"
	"oras.land/oras-go/v2/content/memory"

	"github.com/aggrega-ai/aggrega-cli/internal/publishops"
	"github.com/aggrega-ai/aggrega-cli/internal/wasmtest"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
)

// The fixture bodies below are a deliberate near-duplicate of the ones in the sibling
// test package (internal/mcp): the two packages share no test helper, and a helper package
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
func pluginNoticesFixture(t *testing.T) (*Server, string) {
	t.Helper()
	srv, root := publishFixture(t, defaultRegistryWorkspace)
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
	return srv, root
}

// TestPublishEntityOnTheDaemonPublishesAPluginsNoticesAndSBOM publishes a Plugin
// through the daemon's request projection with both files: the manifest carries
// the notices layer after the executable and one SPDX referrer carries the file.
func TestPublishEntityOnTheDaemonPublishesAPluginsNoticesAndSBOM(t *testing.T) {
	srv, _ := pluginNoticesFixture(t)
	store := memory.New()
	cfg, paramErr := srv.publishEntityConfig(publishEntityParams{
		Entity: "1.0.0/entity.yaml", Plugin: "guest.wasm",
		PluginNotices: "NOTICES.md", PluginSBOM: "sbom.spdx.json",
	}, io.Discard)
	if paramErr != nil {
		t.Fatalf("publishEntityConfig: %s", paramErr.Message)
	}
	cfg.PushTarget = store
	if _, runErr := runPublishEntity(t.Context(), cfg); runErr != nil {
		t.Fatalf("publish: %s", runErr.Message)
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

// TestPublishEntityOnTheDaemonRefusesAPluginsUnpublishableNoticesAndSBOM gives one
// fault per row and asserts the core's refusal, naming the flag, is the cause the
// request's error carries.
func TestPublishEntityOnTheDaemonRefusesAPluginsUnpublishableNoticesAndSBOM(t *testing.T) {
	srv, _ := pluginNoticesFixture(t)
	rows := []struct {
		name   string
		params publishEntityParams
		flag   string
	}{
		{"notices without an executable", publishEntityParams{PluginNotices: "NOTICES.md"}, "--plugin-notices"},
		{"sbom without an executable", publishEntityParams{PluginSBOM: "sbom.spdx.json"}, "--plugin-sbom"},
		{"missing notices", publishEntityParams{Plugin: "guest.wasm", PluginNotices: "absent"}, "--plugin-notices"},
		{"missing sbom", publishEntityParams{Plugin: "guest.wasm", PluginSBOM: "absent"}, "--plugin-sbom"},
		{"empty notices", publishEntityParams{Plugin: "guest.wasm", PluginNotices: "empty"}, "--plugin-notices"},
		{"empty sbom", publishEntityParams{Plugin: "guest.wasm", PluginSBOM: "empty"}, "--plugin-sbom"},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			store := memory.New()
			row.params.Entity = "1.0.0/entity.yaml"
			cfg, paramErr := srv.publishEntityConfig(row.params, io.Discard)
			if paramErr != nil {
				t.Fatalf("publishEntityConfig: %s", paramErr.Message)
			}
			cfg.PushTarget = store
			_, runErr := runPublishEntity(t.Context(), cfg)
			if runErr == nil {
				t.Fatal("expected a refusal")
			}
			var pie *publishops.PluginInputError
			if !errors.As(runErr.cause, &pie) {
				t.Fatalf("cause = %v, want a PluginInputError", runErr.cause)
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

// TestPublishEntityOnTheDaemonConfinesNoticesAndSBOMAsItConfinesThePlugin proves a
// path leading out of the workspace is refused for each new parameter exactly as
// it is for plugin, naming that parameter.
func TestPublishEntityOnTheDaemonConfinesNoticesAndSBOMAsItConfinesThePlugin(t *testing.T) {
	srv, _ := pluginNoticesFixture(t)
	for _, field := range []string{"plugin", "pluginNotices", "pluginSbom"} {
		params := map[string]any{"entity": "1.0.0/entity.yaml", field: "../elsewhere/file"}
		if field != "plugin" {
			params["plugin"] = "guest.wasm"
			params[field] = "../elsewhere/file"
		}
		resp := callDaemonMethod(t, srv, "aggrega/publishEntity", params)
		if resp.Error == nil {
			t.Fatalf("%s: expected a refusal; got result %s", field, resp.Result)
		}
		want := "invalid params: " + field + " must be a path relative to the workspace root, naming "
		if len(resp.Error.Message) < len(want) || resp.Error.Message[:len(want)] != want {
			t.Errorf("%s refusal = %q, want it to begin %q", field, resp.Error.Message, want)
		}
	}
}
