// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package parse_test

import (
	"testing"

	"github.com/cognitio/aggrega-cli/internal/compiler/parse"
)

// TestTemplateOutput_AcceptsYAMLComments verifies the template-output decoder
// is yaml.v3-backed: a payload with a YAML-only feature (a # comment) decodes
// cleanly. Under json.Unmarshal the same input would fail.
func TestTemplateOutput_AcceptsYAMLComments(t *testing.T) {
	t.Parallel()
	in := []byte(`# top-of-file comment
image: demo:1.0
port: 8080  # inline comment
`)
	var got map[string]any
	if err := parse.TemplateOutput(in, &got); err != nil {
		t.Fatalf("parse.TemplateOutput: %v", err)
	}
	if got["image"] != "demo:1.0" {
		t.Errorf("image: %v", got["image"])
	}
	// yaml.v3 decodes integer literals as int.
	if _, ok := got["port"].(int); !ok {
		t.Errorf("port: expected int (yaml.v3 native), got %T (%v)", got["port"], got["port"])
	}
}

// TestTemplateOutput_AcceptsBlockScalar verifies multiline block scalars
// (a YAML-native feature inaccessible under json.Unmarshal) round-trip with
// newlines preserved.
func TestTemplateOutput_AcceptsBlockScalar(t *testing.T) {
	t.Parallel()
	in := []byte(`motd: |
  line1
  line2
`)
	var got map[string]any
	if err := parse.TemplateOutput(in, &got); err != nil {
		t.Fatalf("parse.TemplateOutput: %v", err)
	}
	if got["motd"] != "line1\nline2\n" {
		t.Errorf("motd: %q", got["motd"])
	}
}

// TestIRPayload_BareIntegerDecodesAsFloat64 verifies the IR-payload decoder
// is encoding/json-backed. encoding/json's documented behaviour when the
// destination is interface{} is to decode every JSON number as float64,
// which the IR consumers rely on. yaml.v3 would decode the same input as
// int — exposing a name mismatch between the two helpers protects against
// silent encoder swaps.
func TestIRPayload_BareIntegerDecodesAsFloat64(t *testing.T) {
	t.Parallel()
	in := []byte(`{"replicas": 3, "name": "demo"}`)
	var got map[string]any
	if err := parse.IRPayload(in, &got); err != nil {
		t.Fatalf("parse.IRPayload: %v", err)
	}
	if _, ok := got["replicas"].(float64); !ok {
		t.Errorf("replicas: expected float64 (encoding/json native), got %T (%v)", got["replicas"], got["replicas"])
	}
	if got["name"] != "demo" {
		t.Errorf("name: %v", got["name"])
	}
}

// TestIRPayload_RejectsYAMLOnlyFeatures verifies the IR decoder is strict
// JSON: a YAML-only feature (here, a # comment outside a string literal)
// fails to parse. This is the contract — IR storage is JSON, not YAML.
func TestIRPayload_RejectsYAMLOnlyFeatures(t *testing.T) {
	t.Parallel()
	in := []byte(`# this is a YAML comment, not legal JSON
{"k": "v"}
`)
	var got map[string]any
	if err := parse.IRPayload(in, &got); err == nil {
		t.Error("expected error decoding YAML-comment-prefixed JSON via parse.IRPayload")
	}
}
