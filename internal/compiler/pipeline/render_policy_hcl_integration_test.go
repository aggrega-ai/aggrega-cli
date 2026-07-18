// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

//go:build integration

package pipeline

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/analyzer"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/parsersocket"
	"github.com/aggrega-ai/aggrega-cli/internal/wasmtest"
	"github.com/aggrega-ai/aggrega-cli/pkg/pluginpath"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// hclArtifact is the Terraform source the render-policy HCL tests drive through
// the render-policy stage. It declares one attribute and one resource block, so
// the parsed tree carries a top-level attribute plus a resource block a policy
// can navigate.
const hclArtifact = `region = "eu-west-1"
resource "aws_s3_bucket" "logs" {
  bucket = "acme-logs"
}
`

// hclParserEntity is the parser Plugin entity a format resolver returns for
// "hcl": a pluginType: parser Plugin advertising parse:hcl whose coordinate
// (parser/hcl) resolves the embedded HCL parser bytes through the entity-fronted
// cascade's embed tier.
func hclParserEntity() types.Plugin {
	return types.Plugin{
		APIVersion: "aggrega.ai/v1alpha1",
		Kind:       "Plugin",
		Metadata:   types.PluginMetadata{Name: "hcl", Version: "0.0.0-test", PluginType: "parser"},
		Spec: types.PluginSpec{
			AbiVersion:   "v1alpha1",
			Capabilities: []string{"parse:hcl"},
			Exports:      []types.PluginExport{{Name: "parse"}},
			Runtime:      &types.PluginRuntime{MaxMemoryPages: 4096},
		},
	}
}

// hclParserCatalogLookup returns a CatalogLookup whose parser seam resolves
// "hcl" to the embedded HCL parser and every other format to a loud miss.
func hclParserCatalogLookup() resolver.CatalogLookup {
	return resolver.CatalogLookup{
		ParserPlugin: func(format string) (types.Plugin, error) {
			if format != "hcl" {
				return types.Plugin{}, errors.New("no parser for format " + format)
			}
			return hclParserEntity(), nil
		},
	}
}

// renderPolicyHCLInputs builds the render-policy inputs for a run attaching the
// tree-reading policy fixture with the given required resource type, resolving
// the fixture's bytes through the plugin-path override.
func renderPolicyHCLInputs(t *testing.T, requireResourceType string) Inputs {
	t.Helper()
	guest := wasmtest.EnsureFixtureBytes(t, "policy-render-hcl-go.wasm")
	inputs := Inputs{
		WorkspaceConfig: workspaceconfig.Default(),
	}
	inputs.WorkspaceConfig.Plugins.Policy = []workspaceconfig.PluginRef{{
		URI: "proof://policy-render-hcl",
		With: &workspaceconfig.PluginRefWith{
			Settings: map[string]any{"require_resource_type": requireResourceType},
		},
	}}
	inputs.PluginPathLookup = func(uri string) (pluginpath.ResolvedPlugin, error) {
		if uri == "proof://policy-render-hcl" {
			return pluginpath.ResolvedPlugin{Bytes: guest, URI: uri}, nil
		}
		return pluginpath.ResolvedPlugin{}, errors.New("unexpected plugin lookup " + uri)
	}
	return inputs
}

// renderPolicyHCLFiles is the render output the tests hand the render-policy
// stage: one HCL artifact the host parses and one YAML artifact the guest reads
// natively (its tree must stay empty).
func renderPolicyHCLFiles() map[string][]byte {
	return map[string][]byte{
		"main.tf":     []byte(hclArtifact),
		"values.yaml": []byte("replicas: 2\n"),
	}
}

// TestRenderPolicyHCL_TreeOnlyForNonNative proves the render-policy projection
// parses the HCL artifact into a tree while leaving the natively-read YAML
// artifact's tree empty — the byte-identity guarantee for the YAML/JSON paths.
func TestRenderPolicyHCL_TreeOnlyForNonNative(t *testing.T) {
	ctx := context.Background()
	arts, err := sortedRenderedArtifacts(ctx, Inputs{WorkspaceConfig: workspaceconfig.Default()}, hclParserCatalogLookup(), renderPolicyHCLFiles())
	if err != nil {
		t.Fatalf("sortedRenderedArtifacts: %v", err)
	}
	byPath := map[string]analyzer.RenderedArtifact{}
	for _, a := range arts {
		byPath[a.Path] = a
	}

	tf, ok := byPath["main.tf"]
	if !ok {
		t.Fatalf("main.tf absent from projected artifacts")
	}
	if len(tf.Tree) == 0 {
		t.Fatalf("main.tf carries no parsed tree; the host did not parse the HCL artifact")
	}
	// The tree the policy guest receives, verbatim.
	t.Logf("main.tf parsed HCL tree: %s", string(tf.Tree))

	var root map[string]any
	if err := json.Unmarshal(tf.Tree, &root); err != nil {
		t.Fatalf("parsed tree is not a JSON object: %v", err)
	}
	if _, ok := root["resource"]; !ok {
		t.Errorf("parsed tree missing the resource block: %s", tf.Tree)
	}

	yaml, ok := byPath["values.yaml"]
	if !ok {
		t.Fatalf("values.yaml absent from projected artifacts")
	}
	if len(yaml.Tree) != 0 {
		t.Errorf("values.yaml carries a parsed tree %s; a native format must leave the tree empty", yaml.Tree)
	}
	if yaml.Content != "replicas: 2\n" {
		t.Errorf("values.yaml content changed: %q", yaml.Content)
	}
}

// TestRenderPolicyHCL_DenyReadsTree proves a render-stage policy guest inspects
// the parsed HCL structure and halts the build when a required resource type is
// absent from the tree. The halt carries ErrPolicyViolation and the resource
// types the guest read out of the tree, proving the verdict reflects the parsed
// structure.
func TestRenderPolicyHCL_DenyReadsTree(t *testing.T) {
	ctx := context.Background()
	inputs := renderPolicyHCLInputs(t, "aws_kms_key")

	err := runRenderPolicySocket(ctx, inputs, hclParserCatalogLookup(), renderPolicyHCLFiles())
	if err == nil {
		t.Fatalf("expected a policy halt for an absent required resource type, got nil")
	}
	if !errors.Is(err, analyzer.ErrPolicyViolation) {
		t.Fatalf("render-policy HCL deny not errors.Is ErrPolicyViolation: %v", err)
	}
	msg := err.Error()
	for _, want := range []string{"absent from parsed HCL tree", "aws_s3_bucket"} {
		if !strings.Contains(msg, want) {
			t.Errorf("deny message missing %q (guest did not read the tree structure):\n%v", want, msg)
		}
	}
	if strings.Contains(msg, "saw no parsed HCL tree") {
		t.Fatalf("guest received no tree; the render-policy seam did not populate it:\n%v", msg)
	}
	t.Logf("render-policy HCL deny verdict: %s", msg)
}

// TestRenderPolicyHCL_AllowReadsTree proves the same guest lets the build proceed
// when the required resource type is present in the parsed tree, and that the
// audit verdict it records echoes the resource types it read out of the tree —
// the pass-side proof that the guest genuinely consumed the parsed structure.
func TestRenderPolicyHCL_AllowReadsTree(t *testing.T) {
	ctx := context.Background()
	inputs := renderPolicyHCLInputs(t, "aws_s3_bucket")

	var err error
	stderr := captureRenderPolicyStderr(t, func() {
		err = runRenderPolicySocket(ctx, inputs, hclParserCatalogLookup(), renderPolicyHCLFiles())
	})
	if err != nil {
		t.Fatalf("expected the build to proceed for a present required resource type, got halt: %v", err)
	}
	for _, want := range []string{"audit:", "aws_s3_bucket", "present"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("audit verdict missing %q (guest did not read the tree structure):\n%s", want, stderr)
		}
	}
	t.Logf("render-policy HCL audit verdict (stderr): %s", strings.TrimSpace(stderr))
}

// TestRenderPolicyHCL_ParsedTreeVerbatim shows the exact tree the render-policy
// seam hands the guest for the test HCL artifact, resolved through the same
// embedded HCL parser the seam uses. It also proves the parse is byte-identical
// across two independent hermetic invocations.
func TestRenderPolicyHCL_ParsedTreeVerbatim(t *testing.T) {
	ctx := context.Background()
	res, load := parserSeam(Inputs{WorkspaceConfig: workspaceconfig.Default()}, hclParserCatalogLookup())

	first, err := parsersocket.ParseArtifact(ctx, res, load, "hcl", []byte(hclArtifact), nil)
	if err != nil {
		t.Fatalf("ParseArtifact: %v", err)
	}
	if len(first.Diagnostics) != 0 {
		t.Fatalf("unexpected diagnostics: %+v", first.Diagnostics)
	}
	t.Logf("verbatim HCL tree handed to the guest: %s", string(first.Tree))

	second, err := parsersocket.ParseArtifact(ctx, res, load, "hcl", []byte(hclArtifact), nil)
	if err != nil {
		t.Fatalf("second ParseArtifact: %v", err)
	}
	if !bytes.Equal(first.Tree, second.Tree) {
		t.Fatalf("non-deterministic parsed tree:\n first: %s\nsecond: %s", first.Tree, second.Tree)
	}
}

// captureRenderPolicyStderr redirects os.Stderr for the duration of fn and
// returns everything written to it. The render-policy runner emits audit
// verdicts to os.Stderr, so a proof that the guest recorded a tree-derived audit
// reads the captured text. It mutates a process global, so its callers do not run
// in parallel.
func captureRenderPolicyStderr(t *testing.T, fn func()) string {
	t.Helper()
	orig := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stderr = w
	done := make(chan string, 1)
	go func() {
		var buf bytes.Buffer
		_, _ = io.Copy(&buf, r)
		done <- buf.String()
	}()
	fn()
	_ = w.Close()
	os.Stderr = orig
	return <-done
}
