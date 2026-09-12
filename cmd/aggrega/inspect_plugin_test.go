// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"bytes"
	"context"
	"os"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/wasmtest"
)

// TestInspectRawPlugin_DerivesFromBinary proves the pre-publish authoring view:
// inspect --plugin <path> reads a raw .wasm and prints the host functions it
// imports via the single derivation primitive, labelled as derived-from-binary
// and not-yet-stamped. The frp-step-go fixture imports no custom host functions,
// so the not-yet-stamped view reports none — exercising the derive path against
// a real toolchain-built binary.
func TestInspectRawPlugin_DerivesFromBinary(t *testing.T) {
	t.Parallel()
	pluginPath := wasmtest.EnsureFixtureBuilt(t, "frp-step-go.wasm")
	if _, err := os.Stat(pluginPath); err != nil {
		t.Fatalf("fixture missing: %v", err)
	}
	var buf bytes.Buffer
	if err := inspectRawPlugin(&buf, pluginPath); err != nil {
		t.Fatalf("inspectRawPlugin: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "derived from binary, not yet stamped") {
		t.Errorf("output should label the set as derived-from-binary and not-yet-stamped; got:\n%s", out)
	}
	if !strings.Contains(out, "(none)") {
		t.Errorf("the frp-step-go fixture imports no custom host functions; expected (none); got:\n%s", out)
	}
}

// TestInspectRawPlugin_RendersExportContract proves the author-facing "what do I
// pass / what do I get" view: inspect --plugin <path> reads a raw .wasm and renders,
// per export, the input/output JSON Schema the guest declares in its describe()
// self-report — and omits the line for an export that declares none. The three
// fixtures cover an input-only export, an output-only export, and an export that
// declares neither.
func TestInspectRawPlugin_RendersExportContract(t *testing.T) {
	t.Parallel()

	inspect := func(t *testing.T, artifact string) string {
		t.Helper()
		path := wasmtest.EnsureFixtureBuilt(t, artifact)
		var buf bytes.Buffer
		if err := inspectRawPlugin(&buf, path); err != nil {
			t.Fatalf("inspectRawPlugin(%s): %v", artifact, err)
		}
		return buf.String()
	}

	t.Run("input contract renders, output omitted", func(t *testing.T) {
		t.Parallel()
		out := inspect(t, "secret-injector-go.wasm")
		if !strings.Contains(out, "input: {") {
			t.Errorf("declaring guest should render an input schema; got:\n%s", out)
		}
		if !strings.Contains(out, `"secretName"`) {
			t.Errorf("input schema should carry the declared property; got:\n%s", out)
		}
		if strings.Contains(out, "output:") {
			t.Errorf("guest declares no output; the output line must be omitted; got:\n%s", out)
		}
	})

	t.Run("output contract renders, input omitted", func(t *testing.T) {
		t.Parallel()
		out := inspect(t, "secret-ref-producer-go.wasm")
		if !strings.Contains(out, "output: {") {
			t.Errorf("declaring guest should render an output schema; got:\n%s", out)
		}
		if strings.Contains(out, "input:") {
			t.Errorf("guest declares no input; the input line must be omitted; got:\n%s", out)
		}
	})

	t.Run("no contract omits both lines", func(t *testing.T) {
		t.Parallel()
		out := inspect(t, "sidecar-injector-go.wasm")
		if strings.Contains(out, "input:") || strings.Contains(out, "output:") {
			t.Errorf("guest declares neither input nor output; both lines must be omitted; got:\n%s", out)
		}
	})
}

// TestInspectRawPlugin_DescribeBestEffort proves the raw-.wasm view treats the
// describe() self-report as best-effort, not a validation gate. When describe()
// succeeds the export contract renders; when it is absent or not yet wired the
// command does not fail — it skips the export block, reports it unavailable, and
// still prints the host functions derived straight from the binary (the
// always-available part of this pre-publish diagnostic). The loudness for a guest
// that cannot describe itself belongs to validate, not inspect.
func TestInspectRawPlugin_DescribeBestEffort(t *testing.T) {
	t.Parallel()

	t.Run("describe succeeds: export contract renders", func(t *testing.T) {
		t.Parallel()
		path := wasmtest.EnsureFixtureBuilt(t, "secret-injector-go.wasm")
		var buf bytes.Buffer
		if err := inspectRawPlugin(&buf, path); err != nil {
			t.Fatalf("inspectRawPlugin: %v", err)
		}
		out := buf.String()
		if !strings.Contains(out, "input: {") {
			t.Errorf("a describing guest should render its export contract; got:\n%s", out)
		}
		if strings.Contains(out, "did not report a describe") {
			t.Errorf("a describing guest must not report the contract unavailable; got:\n%s", out)
		}
	})

	t.Run("describe absent: host functions still shown, no error", func(t *testing.T) {
		t.Parallel()
		// fetch-probe-go is a readable reactor whose describe() self-report is not
		// wired: DeriveHostFunctions reads its import section, but describe() is
		// absent. Best-effort means the command still succeeds and still prints the
		// derived host function.
		path := wasmtest.EnsureFixtureBuilt(t, "fetch-probe-go.wasm")
		var buf bytes.Buffer
		if err := inspectRawPlugin(&buf, path); err != nil {
			t.Fatalf("a non-describing but readable .wasm must not error; got: %v", err)
		}
		out := buf.String()
		if !strings.Contains(out, "Exports: (unavailable") {
			t.Errorf("an absent describe() should report the export contract unavailable; got:\n%s", out)
		}
		if !strings.Contains(out, "derived from binary, not yet stamped") {
			t.Errorf("host functions must still be shown when describe() is absent; got:\n%s", out)
		}
		if !strings.Contains(out, "aggrega_fetch_external_input") {
			t.Errorf("the derived host function should print; got:\n%s", out)
		}
	})
}

// pluginFixture declares a Plugin whose bare identity (metadata.name) plus its
// pluginType derive its artifact-reference coordinate, two exports carrying full
// capability claims (operates /
// scope / selector), and an explicit runtime block (both limits set, a stamped
// host-function record). It drives the golden harness across all four render levels.
const pluginFixture = `apiVersion: aggrega.ai/v1alpha1
kind: Plugin
metadata:
  name: helm-renderer
  pluginType: provider
  version: "1.0.0"
  description: Renders IR nodes into Helm chart artifacts.
spec:
  abiVersion: v1alpha1
  provider:
    capabilities:
      - helm
      - compose
  signature:
    type: sigstore-cosign
    signatureRef: oci://hub.aggrega.ai/plugins/helm-renderer:sha256-abc123.sig
    publicKeyRef: oci://hub.aggrega.ai/keys/release.pub
  runtime:
    maxMemoryPages: 256
    timeoutMs: 3000
    hostFunctions:
      - log_message
      - read_config
    configKV:
      render_mode: strict
      indent: "2"
  exports:
    - name: render_node
      operates: emit
      scope: own
      selector: workloads carrying a pod template
    - name: patch_labels
      operates: patch
      scope: host
      selector: all rendered documents
`

func TestInspectPlugin_Goldens(t *testing.T) {
	t.Parallel()
	runKindGoldens(t, "plugin", pluginFixture)
}

// TestInspectPlugin_SurfacesExportsAndRuntime asserts the default render
// surfaces the per-export capability claims and the explicit runtime limits and
// host-function names — the declared capability surface inspect exposes.
func TestInspectPlugin_SurfacesExportsAndRuntime(t *testing.T) {
	t.Parallel()
	path := writeKindFixture(t, "plugin_caps", pluginFixture)
	var buf bytes.Buffer
	if err := dispatchInspect(context.Background(), resolveTempWorkspace(t, tempWorkspaceOptions{}), path, nil, testInspectOptions(&buf)); err != nil {
		t.Fatalf("inspect plugin: %v", err)
	}
	out := buf.String()
	for _, want := range []string{
		"render_node", "patch_labels", // export names
		"operates: emit", "operates: patch", // operates claims
		"scope: own", "scope: host", // scope claims
		"maxMemoryPages: 256", "timeoutMs: 3000", // explicit limits
		"log_message", "read_config", // host functions
	} {
		if !strings.Contains(out, want) {
			t.Errorf("default render should surface %q; got:\n%s", want, out)
		}
	}
}

// pluginNoRuntimeFixture omits the runtime block entirely, so every cap
// falls back to the platform default.
const pluginNoRuntimeFixture = `apiVersion: aggrega.ai/v1alpha1
kind: Plugin
metadata:
  name: linter
  pluginType: linter
  version: "1.0.0"
spec:
  abiVersion: v1alpha1
  exports:
    - name: lint_naming
`

// TestInspectPlugin_NoRuntime_DefaultApplies asserts an entity with no
// runtime block reports that the platform default applies, and that the renderer
// never prints a hardcoded default cap value — the default is described
// qualitatively, single-sourced to the loader's (unexported) cap.
func TestInspectPlugin_NoRuntime_DefaultApplies(t *testing.T) {
	t.Parallel()
	path := writeKindFixture(t, "plugin_nort", pluginNoRuntimeFixture)
	var buf bytes.Buffer
	if err := dispatchInspect(context.Background(), resolveTempWorkspace(t, tempWorkspaceOptions{}), path, nil, testInspectOptions(&buf)); err != nil {
		t.Fatalf("inspect plugin no-runtime: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "platform default") {
		t.Errorf("no-runtime entity should report the platform default applies; got:\n%s", out)
	}
	for _, forbidden := range []string{"1024", "5000"} {
		if strings.Contains(out, forbidden) {
			t.Errorf("renderer must not print a hardcoded default cap value %q; got:\n%s", forbidden, out)
		}
	}
}

// pluginPartialRuntimeFixture sets one limit and leaves the other unset, so
// the rendered runtime mixes an authored value with a platform-default fallback.
const pluginPartialRuntimeFixture = `apiVersion: aggrega.ai/v1alpha1
kind: Plugin
metadata:
  name: step
  pluginType: pipeline-step
  version: "1.0.0"
spec:
  abiVersion: v1alpha1
  runtime:
    timeoutMs: 7000
  exports:
    - name: step
`

// TestInspectPlugin_PartialRuntime asserts a runtime with one limit set and
// the other unset (zero) prints the set limit's value and reports the platform
// default for the unset one.
func TestInspectPlugin_PartialRuntime(t *testing.T) {
	t.Parallel()
	path := writeKindFixture(t, "plugin_partial", pluginPartialRuntimeFixture)
	var buf bytes.Buffer
	if err := dispatchInspect(context.Background(), resolveTempWorkspace(t, tempWorkspaceOptions{}), path, nil, testInspectOptions(&buf)); err != nil {
		t.Fatalf("inspect plugin partial-runtime: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "timeoutMs: 7000") {
		t.Errorf("the set limit should print its authored value; got:\n%s", out)
	}
	if !strings.Contains(out, "maxMemoryPages: (platform default applies)") {
		t.Errorf("the unset limit should report the platform default; got:\n%s", out)
	}
}
