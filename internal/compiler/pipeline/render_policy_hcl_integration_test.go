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
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
	"github.com/aggrega-ai/aggrega-cli/internal/wasmtest"
	"github.com/aggrega-ai/aggrega-cli/pkg/pluginpath"
	"github.com/aggrega-ai/aggrega-cli/pkg/plugintype"
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
	inputs.WorkspaceConfig.Plugins.Governance = []workspaceconfig.PluginRef{{
		URI:  "proof://policy-render-hcl",
		Role: plugintype.RoleRenderVerdict,
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

// assembled zips a path→bytes map with an optional path→format map into the
// path→record shape the render-policy projection consumes, so a proof supplies
// its file bytes and producer formats as two literals and hands the seam one
// record map. A path absent from formats gets an empty Format (extension
// fallback).
func assembled(files map[string][]byte, formats map[string]string) map[string]renderer.AssembledFile {
	out := make(map[string]renderer.AssembledFile, len(files))
	for p, b := range files {
		out[p] = renderer.AssembledFile{Content: b, Format: formats[p]}
	}
	return out
}

// TestRenderPolicyHCL_TreeOnlyForNonNative proves the render-policy projection
// parses the HCL artifact into a tree while leaving the natively-read YAML
// artifact's tree empty — the byte-identity guarantee for the YAML/JSON paths.
func TestRenderPolicyHCL_TreeOnlyForNonNative(t *testing.T) {
	ctx := context.Background()
	arts, err := sortedRenderedArtifacts(ctx, Inputs{WorkspaceConfig: workspaceconfig.Default()}, hclParserCatalogLookup(), assembled(renderPolicyHCLFiles(), nil))
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

// TestRenderPolicyHCL_ProducerFormatSelectsParser proves the producer-declared
// format is the PRIMARY parser-selection tier: an artifact whose path extension
// maps to no format still parses through the HCL parser when its producing
// provider declared "hcl", and the projected artifact carries that producer
// format verbatim. Without the producer format the same extension-less path
// selects nothing and its tree stays empty — the control that isolates the
// producer tier from the extension fallback.
func TestRenderPolicyHCL_ProducerFormatSelectsParser(t *testing.T) {
	ctx := context.Background()
	// A path with no format-bearing extension: extension fallback yields nothing.
	files := map[string][]byte{"module": []byte(hclArtifact)}

	// With the producer-declared format, selection resolves to hcl and the host
	// parses the artifact into a tree.
	tagged, err := sortedRenderedArtifacts(ctx, Inputs{WorkspaceConfig: workspaceconfig.Default()}, hclParserCatalogLookup(), assembled(files, map[string]string{"module": "hcl"}))
	if err != nil {
		t.Fatalf("sortedRenderedArtifacts (producer-tagged): %v", err)
	}
	if len(tagged) != 1 {
		t.Fatalf("expected one artifact, got %d", len(tagged))
	}
	if tagged[0].Format != "hcl" {
		t.Errorf("producer-declared format not carried onto the artifact: got %q, want %q", tagged[0].Format, "hcl")
	}
	if len(tagged[0].Tree) == 0 {
		t.Fatalf("producer-declared format did not drive parser selection: extension-less %q carries no parsed tree", tagged[0].Path)
	}
	var root map[string]any
	if err := json.Unmarshal(tagged[0].Tree, &root); err != nil {
		t.Fatalf("parsed tree is not a JSON object: %v", err)
	}
	if _, ok := root["resource"]; !ok {
		t.Errorf("parsed tree missing the resource block: %s", tagged[0].Tree)
	}
	t.Logf("producer-format-selected HCL tree: %s", string(tagged[0].Tree))

	// Control: the same extension-less path with no producer format selects no
	// format, so its tree stays empty.
	untagged, err := sortedRenderedArtifacts(ctx, Inputs{WorkspaceConfig: workspaceconfig.Default()}, hclParserCatalogLookup(), assembled(files, nil))
	if err != nil {
		t.Fatalf("sortedRenderedArtifacts (untagged): %v", err)
	}
	if untagged[0].Format != "" {
		t.Errorf("untagged artifact carries a format %q; expected none", untagged[0].Format)
	}
	if len(untagged[0].Tree) != 0 {
		t.Errorf("untagged extension-less artifact carries a tree %s; extension fallback should select nothing", untagged[0].Tree)
	}
}

// TestRenderPolicyHCL_ProducerFormatOverridesExtension proves the producer-declared
// format outranks the extension fallback when the two disagree. The artifact path
// ends in ".json" — an extension the fixed table reads as the native JSON format,
// which the host leaves unparsed — yet its producing provider declared "hcl". The
// host parses the artifact as HCL and carries the parsed tree, proving the producer
// tag, not the ".json" extension, selected the parser. The untagged control on the
// same path takes the extension path to native JSON and leaves the tree empty,
// isolating the override.
func TestRenderPolicyHCL_ProducerFormatOverridesExtension(t *testing.T) {
	ctx := context.Background()
	// The path extension resolves to native JSON, but the producer declared HCL,
	// and the bytes are HCL. If the extension won, the artifact would be read as
	// native JSON and carry no tree.
	files := map[string][]byte{"resource.json": []byte(hclArtifact)}

	tagged, err := sortedRenderedArtifacts(ctx, Inputs{WorkspaceConfig: workspaceconfig.Default()}, hclParserCatalogLookup(), assembled(files, map[string]string{"resource.json": "hcl"}))
	if err != nil {
		t.Fatalf("sortedRenderedArtifacts (producer hcl over .json): %v", err)
	}
	if len(tagged) != 1 {
		t.Fatalf("expected one artifact, got %d", len(tagged))
	}
	if tagged[0].Format != "hcl" {
		t.Errorf("producer-declared format not carried onto the artifact: got %q, want %q", tagged[0].Format, "hcl")
	}
	if len(tagged[0].Tree) == 0 {
		t.Fatalf("producer format did not override the .json extension: %q carries no parsed tree", tagged[0].Path)
	}
	var root map[string]any
	if err := json.Unmarshal(tagged[0].Tree, &root); err != nil {
		t.Fatalf("parsed tree is not a JSON object: %v", err)
	}
	if _, ok := root["resource"]; !ok {
		t.Errorf("parsed tree missing the resource block (not parsed as HCL): %s", tagged[0].Tree)
	}
	t.Logf("producer hcl over .json extension — parsed HCL tree: %s", string(tagged[0].Tree))

	// Control: the same .json path with no producer format takes the extension
	// path to native JSON, which the host does not pre-parse, so the tree stays
	// empty. This is exactly what the producer tag overrode above.
	untagged, err := sortedRenderedArtifacts(ctx, Inputs{WorkspaceConfig: workspaceconfig.Default()}, hclParserCatalogLookup(), assembled(files, nil))
	if err != nil {
		t.Fatalf("sortedRenderedArtifacts (untagged .json): %v", err)
	}
	if untagged[0].Format != "" {
		t.Errorf("untagged artifact carries a format %q; expected none", untagged[0].Format)
	}
	if len(untagged[0].Tree) != 0 {
		t.Errorf("untagged .json carries a tree %s; the extension resolves native JSON, which stays unparsed", untagged[0].Tree)
	}
	t.Logf("control — untagged .json takes the extension to native JSON, tree empty (len=%d)", len(untagged[0].Tree))
}

// TestRenderPolicyHCL_MixedOutputRoutesPerFile proves a single render output that
// emits two files with different producer-declared formats routes each file to its
// own parser handling independently: a ".tf" file declared "hcl" is parsed through
// the HCL parser and carries a tree, while a ".tf.json" file declared "json" is a
// native format the host does not pre-parse, so its tree stays empty. Each file's
// handling follows its own producer format; neither leaks into the other.
func TestRenderPolicyHCL_MixedOutputRoutesPerFile(t *testing.T) {
	ctx := context.Background()
	jsonArtifact := `{"resource":{"aws_kms_key":{"main":{"description":"acme"}}}}`
	files := map[string][]byte{
		"main.tf":      []byte(hclArtifact),
		"main.tf.json": []byte(jsonArtifact),
	}
	formats := map[string]string{
		"main.tf":      "hcl",
		"main.tf.json": "json",
	}

	arts, err := sortedRenderedArtifacts(ctx, Inputs{WorkspaceConfig: workspaceconfig.Default()}, hclParserCatalogLookup(), assembled(files, formats))
	if err != nil {
		t.Fatalf("sortedRenderedArtifacts (mixed output): %v", err)
	}
	byPath := map[string]analyzer.RenderedArtifact{}
	for _, a := range arts {
		byPath[a.Path] = a
	}

	tf, ok := byPath["main.tf"]
	if !ok {
		t.Fatalf("main.tf absent from projected artifacts")
	}
	if tf.Format != "hcl" {
		t.Errorf("main.tf producer format: got %q, want %q", tf.Format, "hcl")
	}
	if len(tf.Tree) == 0 {
		t.Fatalf("main.tf carries no parsed tree; the host did not route it to the HCL parser")
	}
	var tfRoot map[string]any
	if err := json.Unmarshal(tf.Tree, &tfRoot); err != nil {
		t.Fatalf("main.tf tree is not a JSON object: %v", err)
	}
	if _, ok := tfRoot["resource"]; !ok {
		t.Errorf("main.tf tree missing the resource block (not HCL-parsed): %s", tf.Tree)
	}
	t.Logf("mixed output — main.tf routed to HCL, tree: %s", string(tf.Tree))

	tfjson, ok := byPath["main.tf.json"]
	if !ok {
		t.Fatalf("main.tf.json absent from projected artifacts")
	}
	if tfjson.Format != "json" {
		t.Errorf("main.tf.json producer format: got %q, want %q", tfjson.Format, "json")
	}
	if len(tfjson.Tree) != 0 {
		t.Errorf("main.tf.json carries a tree %s; the json producer format is native and stays unparsed", tfjson.Tree)
	}
	if tfjson.Content != jsonArtifact {
		t.Errorf("main.tf.json content changed: %q", tfjson.Content)
	}
	t.Logf("mixed output — main.tf.json routed to native json handling, tree empty (len=%d), content verbatim", len(tfjson.Tree))
}

// TestRenderPolicyHCL_UntaggedExtensionFallback proves the extension fallback still
// governs an artifact its producer did not tag: a ".tf" file with no producer
// format resolves to HCL through the fixed extension table alone and carries the
// parsed tree, exactly as before the producer tier existed. The artifact's Format
// stays empty because no producer declared one; the extension supplied the parser.
func TestRenderPolicyHCL_UntaggedExtensionFallback(t *testing.T) {
	ctx := context.Background()
	files := map[string][]byte{"main.tf": []byte(hclArtifact)}

	arts, err := sortedRenderedArtifacts(ctx, Inputs{WorkspaceConfig: workspaceconfig.Default()}, hclParserCatalogLookup(), assembled(files, nil))
	if err != nil {
		t.Fatalf("sortedRenderedArtifacts (untagged .tf): %v", err)
	}
	if len(arts) != 1 {
		t.Fatalf("expected one artifact, got %d", len(arts))
	}
	if arts[0].Format != "" {
		t.Errorf("untagged .tf carries a producer format %q; expected none — the extension supplies the parser", arts[0].Format)
	}
	if len(arts[0].Tree) == 0 {
		t.Fatalf("untagged .tf carries no parsed tree; the extension fallback did not resolve HCL")
	}
	var root map[string]any
	if err := json.Unmarshal(arts[0].Tree, &root); err != nil {
		t.Fatalf("parsed tree is not a JSON object: %v", err)
	}
	if _, ok := root["resource"]; !ok {
		t.Errorf("parsed tree missing the resource block (not parsed as HCL): %s", arts[0].Tree)
	}
	t.Logf("untagged .tf resolved HCL via the extension fallback — tree: %s", string(arts[0].Tree))
}

// TestRenderPolicyHCL_DenyReadsTree proves a render-stage policy guest inspects
// the parsed HCL structure and halts the build when a required resource type is
// absent from the tree. The halt carries ErrPolicyViolation and the resource
// types the guest read out of the tree, proving the verdict reflects the parsed
// structure.
func TestRenderPolicyHCL_DenyReadsTree(t *testing.T) {
	ctx := context.Background()
	inputs := renderPolicyHCLInputs(t, "aws_kms_key")

	err := runRenderPolicySocket(ctx, inputs, hclParserCatalogLookup(), assembled(renderPolicyHCLFiles(), nil))
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
		err = runRenderPolicySocket(ctx, inputs, hclParserCatalogLookup(), assembled(renderPolicyHCLFiles(), nil))
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
