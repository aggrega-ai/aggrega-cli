// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"bytes"
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

// pluginFixture declares a Plugin with a content-addressed artifact,
// two exports carrying full capability claims (operates / scope / selector),
// and an explicit runtime block (both limits set, a stamped host-function
// record). It drives the golden harness across all four render levels.
const pluginFixture = `apiVersion: aggrega.ai/v1alpha1
kind: Plugin
metadata:
  name: core/helm-renderer
  version: "1.0.0"
  description: Renders IR nodes into Helm chart artifacts.
spec:
  plugin: oci://hub.aggrega.ai/plugins/helm-renderer@sha256:abc123
  pluginTypes:
    - provider
    - frp_step
  abiVersion: v1alpha1
  compatibleProviders:
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
	if err := dispatchInspect(resolveTempWorkspace(t), path, nil, testInspectOptions(&buf)); err != nil {
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
  name: core/linter
  version: "1.0.0"
spec:
  plugin: oci://hub.aggrega.ai/plugins/linter@sha256:def456
  pluginTypes:
    - linter
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
	if err := dispatchInspect(resolveTempWorkspace(t), path, nil, testInspectOptions(&buf)); err != nil {
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
  name: core/step
  version: "1.0.0"
spec:
  plugin: oci://hub.aggrega.ai/plugins/step@sha256:0a0a0a
  pluginTypes:
    - frp_step
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
	if err := dispatchInspect(resolveTempWorkspace(t), path, nil, testInspectOptions(&buf)); err != nil {
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
