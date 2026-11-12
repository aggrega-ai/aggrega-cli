// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package serve

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// TestDispatchInitializeIncludesWorkspaceRootAndConfigSource verifies
// that the aggrega/initialize response carries workspaceRoot
// (absolute path) and configSource (one of "workspace" | "global" |
// "defaults") alongside the existing capabilities field.
func TestDispatchInitializeIncludesWorkspaceRootAndConfigSource(t *testing.T) {
	out := &bytes.Buffer{}
	srv := newTestServer(t, "/tmp/test-workspace", nil, out)
	srv.configSource = "workspace"

	req := framedRequest(1, "aggrega/initialize", map[string]any{
		"capabilities": map[string]any{
			"general": map[string]any{
				"positionEncodings": []string{"utf-8", "utf-16"},
			},
		},
	})
	var reqBody jsonRPCRequest
	bodyStart := bytes.Index(req, []byte("\r\n\r\n")) + 4
	if err := json.Unmarshal(req[bodyStart:], &reqBody); err != nil {
		t.Fatalf("unmarshal test request: %v", err)
	}
	driveHandle(t, srv, &reqBody, out)

	resp := decodeOneResponse(t, out)
	var result initializeResult
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		t.Fatalf("unmarshal result: %v (raw: %s)", err, resp.Result)
	}

	if result.WorkspaceRoot != "/tmp/test-workspace" {
		t.Errorf("WorkspaceRoot: got %q, want /tmp/test-workspace", result.WorkspaceRoot)
	}
	if result.ConfigSource != "workspace" {
		t.Errorf("ConfigSource: got %q, want workspace", result.ConfigSource)
	}
	if result.Capabilities.PositionEncoding != "utf-8" {
		t.Errorf("PositionEncoding: got %q, want utf-8 (server preference wins)",
			result.Capabilities.PositionEncoding)
	}
}

// TestDispatchInitializeConfigSourceReflectsPrecedence verifies that
// configSource on the wire reflects the layer that actually
// contributed. Server's configSource field is set by Run via
// workspaceconfig.LoadWithGlobal; this test pins the round-trip
// (Server field → wire field).
func TestDispatchInitializeConfigSourceReflectsPrecedence(t *testing.T) {
	cases := []struct {
		name        string
		field       string
		wantSurface string
	}{
		{"workspace overlay", "workspace", "workspace"},
		{"global overlay", "global", "global"},
		{"defaults only", "defaults", "defaults"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out := &bytes.Buffer{}
			srv := newTestServer(t, "/tmp/test-workspace", nil, out)
			srv.configSource = tc.field

			req := framedRequest(1, "aggrega/initialize", map[string]any{
				"capabilities": map[string]any{},
			})
			var reqBody jsonRPCRequest
			bodyStart := bytes.Index(req, []byte("\r\n\r\n")) + 4
			if err := json.Unmarshal(req[bodyStart:], &reqBody); err != nil {
				t.Fatalf("unmarshal test request: %v", err)
			}
			driveHandle(t, srv, &reqBody, out)
			resp := decodeOneResponse(t, out)
			var result initializeResult
			if err := json.Unmarshal(resp.Result, &result); err != nil {
				t.Fatalf("unmarshal result: %v", err)
			}
			if result.ConfigSource != tc.wantSurface {
				t.Errorf("ConfigSource: got %q, want %q", result.ConfigSource, tc.wantSurface)
			}
		})
	}
}

// TestDispatchInitializeCapabilitiesFieldUnchanged is a regression
// guard for the backwards-compatibility contract: the existing
// capabilities.positionEncoding field is the surface every existing
// extension reads. New WorkspaceRoot / ConfigSource fields are
// additive; the capabilities field must continue to land at
// result.capabilities.positionEncoding.
func TestDispatchInitializeCapabilitiesFieldUnchanged(t *testing.T) {
	out := &bytes.Buffer{}
	srv := newTestServer(t, "/tmp/test-workspace", nil, out)

	req := framedRequest(1, "aggrega/initialize", map[string]any{
		"capabilities": map[string]any{
			"general": map[string]any{
				"positionEncodings": []string{"utf-16"},
			},
		},
	})
	var reqBody jsonRPCRequest
	bodyStart := bytes.Index(req, []byte("\r\n\r\n")) + 4
	if err := json.Unmarshal(req[bodyStart:], &reqBody); err != nil {
		t.Fatalf("unmarshal test request: %v", err)
	}
	driveHandle(t, srv, &reqBody, out)

	resp := decodeOneResponse(t, out)
	// Decode into a permissive map so we can verify the field key
	// is exactly "capabilities.positionEncoding" — not a renamed
	// "serverCapabilities" wrapper or similar.
	var generic map[string]any
	if err := json.Unmarshal(resp.Result, &generic); err != nil {
		t.Fatalf("unmarshal generic: %v", err)
	}
	caps, ok := generic["capabilities"].(map[string]any)
	if !ok {
		t.Fatalf("result.capabilities not present or not an object: %v", generic)
	}
	pe, ok := caps["positionEncoding"].(string)
	if !ok {
		t.Fatalf("result.capabilities.positionEncoding not present or not a string: %v", caps)
	}
	if pe != "utf-16" {
		t.Errorf("positionEncoding: got %q, want utf-16", pe)
	}
}

// TestInitializeReportsWorkspaceMarkerAggregateGlob verifies that
// the initialize response carries workspaceMarker = "aggregate-glob"
// when the workspace was anchored by a `aggregate*.yaml` glob match.
func TestInitializeReportsWorkspaceMarkerAggregateGlob(t *testing.T) {
	out := &bytes.Buffer{}
	srv := newTestServer(t, "/tmp/test-workspace", nil, out)
	srv.workspaceMarker = workspaceconfig.MarkerAggregateGlob

	req := framedRequest(1, "aggrega/initialize", map[string]any{
		"capabilities": map[string]any{},
	})
	var reqBody jsonRPCRequest
	bodyStart := bytes.Index(req, []byte("\r\n\r\n")) + 4
	if err := json.Unmarshal(req[bodyStart:], &reqBody); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	driveHandle(t, srv, &reqBody, out)
	resp := decodeOneResponse(t, out)
	var result initializeResult
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if result.WorkspaceMarker != "aggregate-glob" {
		t.Errorf("WorkspaceMarker: got %q, want aggregate-glob", result.WorkspaceMarker)
	}
}

// TestInitializeReportsWorkspaceMarkerConfig verifies the symbolic
// "config" enum value for the .aggrega-config.yaml marker tier.
func TestInitializeReportsWorkspaceMarkerConfig(t *testing.T) {
	out := &bytes.Buffer{}
	srv := newTestServer(t, "/tmp/test-workspace", nil, out)
	srv.workspaceMarker = workspaceconfig.MarkerConfig

	req := framedRequest(1, "aggrega/initialize", map[string]any{
		"capabilities": map[string]any{},
	})
	var reqBody jsonRPCRequest
	bodyStart := bytes.Index(req, []byte("\r\n\r\n")) + 4
	if err := json.Unmarshal(req[bodyStart:], &reqBody); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	driveHandle(t, srv, &reqBody, out)
	resp := decodeOneResponse(t, out)
	var result initializeResult
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if result.WorkspaceMarker != "config" {
		t.Errorf("WorkspaceMarker: got %q, want config", result.WorkspaceMarker)
	}
}

// TestInitializeReportsTheInjectedBuildIdentityAsTheVersion pins that the
// handshake's version is the build identity the session was started with, and
// nothing the daemon works out for itself.
//
// The identity is a synthetic value no build ever carries, so a version the
// daemon derived on its own — a hard-coded string, or a second reading of the
// build information — cannot match it by coincidence, and an unset field reads
// as the empty string rather than the value. The key is asserted present in the
// generic decode as well, because a decode into the struct reports an absent key
// and an empty one identically.
func TestInitializeReportsTheInjectedBuildIdentityAsTheVersion(t *testing.T) {
	const injected = "0.0.0-handshake-version-probe"
	out := &bytes.Buffer{}
	srv := newTestServer(t, "/tmp/test-workspace", nil, out)
	srv.compilerVersion = injected

	req := framedRequest(1, "aggrega/initialize", map[string]any{
		"capabilities": map[string]any{},
	})
	var reqBody jsonRPCRequest
	bodyStart := bytes.Index(req, []byte("\r\n\r\n")) + 4
	if err := json.Unmarshal(req[bodyStart:], &reqBody); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	driveHandle(t, srv, &reqBody, out)
	resp := decodeOneResponse(t, out)

	var generic map[string]any
	if err := json.Unmarshal(resp.Result, &generic); err != nil {
		t.Fatalf("unmarshal generic: %v (raw: %s)", err, resp.Result)
	}
	got, present := generic["version"]
	if !present {
		t.Fatalf("the initialize answer carries no version key: %s", resp.Result)
	}
	if got != injected {
		t.Errorf("version = %#v; want the injected build identity %q", got, injected)
	}
}

// TestInitializeOmitsWorkspaceMarkerWhenExplicitRoot verifies that
// markerNone (the zero value, set when --workspace-root was used)
// produces an omitted field on the wire — the omitempty JSON tag
// keys off the empty-string WireName.
func TestInitializeOmitsWorkspaceMarkerWhenExplicitRoot(t *testing.T) {
	out := &bytes.Buffer{}
	srv := newTestServer(t, "/tmp/test-workspace", nil, out)
	// workspaceMarker left as markerNone (zero value).

	req := framedRequest(1, "aggrega/initialize", map[string]any{
		"capabilities": map[string]any{},
	})
	var reqBody jsonRPCRequest
	bodyStart := bytes.Index(req, []byte("\r\n\r\n")) + 4
	if err := json.Unmarshal(req[bodyStart:], &reqBody); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	driveHandle(t, srv, &reqBody, out)
	resp := decodeOneResponse(t, out)
	var generic map[string]any
	if err := json.Unmarshal(resp.Result, &generic); err != nil {
		t.Fatalf("unmarshal generic: %v", err)
	}
	if _, present := generic["workspaceMarker"]; present {
		t.Errorf("workspaceMarker should be absent for explicit-root; got present: %v", generic)
	}
}
