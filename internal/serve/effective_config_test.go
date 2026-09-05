// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package serve

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// effectiveConfigFixture writes a three-layer cascade and returns a Server
// holding the resolution of it, plus the two config-file paths.
//
// The layers are chosen so that one setting arrives from each of the three
// source shapes the attribution distinguishes: default_registry from the
// workspace file, offline from the global file, and signing.required from an
// environment override. Every other setting stays on the built-in defaults,
// which is the third shape.
//
// The workspace is resolved through workspaceconfig.Resolve, the same call
// serve.Run makes, because ResolvedWorkspace is opaque — no exported field, no
// exported constructor — so a test cannot fabricate one and is held to the
// production resolution path.
func effectiveConfigFixture(t *testing.T, env map[string]string) (*Server, string, string) {
	t.Helper()

	root := t.TempDir()
	globalPath := filepath.Join(t.TempDir(), "config.yaml")

	if err := os.WriteFile(globalPath, []byte("offline: true\n"), 0o644); err != nil {
		t.Fatalf("write the global layer: %v", err)
	}
	wsPath := filepath.Join(root, workspaceconfig.ConfigFilename)
	if err := os.WriteFile(wsPath, []byte("workspace:\n  catalog_dirs:\n    - ./catalog\ndefault_registry: workspace.example.test\n"), 0o644); err != nil {
		t.Fatalf("write the workspace layer: %v", err)
	}

	full := map[string]string{workspaceconfig.EnvConfigOverride: globalPath}
	for k, v := range env {
		full[k] = v
	}
	lookup := func(k string) string { return full[k] }

	// Both the start and the root are the fixture, so the cascade collects the
	// fixture's own file and nothing above it. Passing an empty start would walk
	// from the process working directory and could reach a real config file.
	rw, err := workspaceconfig.Resolve(root, root, lookup)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	srv := newTestServer(t, root, nil, nil)
	srv.resolvedWorkspace = rw
	return srv, globalPath, wsPath
}

// callEffectiveConfiguration drives one aggrega/effectiveConfiguration request
// through the production dispatch and serializer path and returns the decoded
// response.
func callEffectiveConfiguration(t *testing.T, srv *Server) jsonRPCResponse {
	t.Helper()
	var in, out bytes.Buffer
	in.Write(framedRequest(1, "aggrega/effectiveConfiguration", map[string]any{}))
	return runOneRequest(t, srv, &in, &out)
}

// TestEffectiveConfigurationAttributesEachSourceShape is the invocation proof:
// it drives the method over a real three-layer cascade and asserts a setting
// from EACH of the three source shapes is attributed to the right layer.
//
// Asserting the field is present would pass on a method that attributed
// everything to the built-in defaults, so every leg here names both the setting
// and the layer it must carry, and the three legs are deliberately different
// layers — a file, an environment override, and the defaults.
func TestEffectiveConfigurationAttributesEachSourceShape(t *testing.T) {
	srv, globalPath, wsPath := effectiveConfigFixture(t, map[string]string{
		"AGGREGA_SIGNING_REQUIRED": "true",
	})

	resp := callEffectiveConfiguration(t, srv)
	if resp.Error != nil {
		t.Fatalf("effectiveConfiguration returned an error envelope: %+v", resp.Error)
	}

	var result effectiveConfigurationResult
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		t.Fatalf("unmarshal result: %v (raw: %s)", err, resp.Result)
	}

	byField := map[string]effectiveConfigurationSetting{}
	for _, s := range result.Settings {
		byField[s.Field] = s
	}

	for _, tc := range []struct {
		name       string
		field      string
		wantValue  string
		wantSource string
	}{
		{"a workspace config file", "default_registry", "workspace.example.test", wsPath},
		{"a global config file", "offline", "true", globalPath},
		{"an environment override", "signing.required", "true", "env:AGGREGA_SIGNING_REQUIRED"},
		{"the built-in defaults", "parse_cache.max_entries", "1000", "(built-in defaults)"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := byField[tc.field]
			if !ok {
				t.Fatalf("the response carries no %q setting", tc.field)
			}
			if got.Source != tc.wantSource {
				t.Errorf("%s source = %q; want %q", tc.field, got.Source, tc.wantSource)
			}
			if got.Value != tc.wantValue {
				t.Errorf("%s value = %q; want %q", tc.field, got.Value, tc.wantValue)
			}
		})
	}
}

// TestEffectiveConfigurationAttributionFollowsASettingBetweenLayers is the
// discriminating proof. The same setting is placed in a different layer and the
// reported source must move with it.
//
// A method that always attributed to one layer would pass a fixed-expectation
// test on whichever layer it happened to name; it cannot pass this one, because
// the two runs demand two different answers for the same setting name.
func TestEffectiveConfigurationAttributionFollowsASettingBetweenLayers(t *testing.T) {
	sourceOf := func(t *testing.T, field string, srv *Server) string {
		t.Helper()
		resp := callEffectiveConfiguration(t, srv)
		if resp.Error != nil {
			t.Fatalf("effectiveConfiguration returned an error envelope: %+v", resp.Error)
		}
		var result effectiveConfigurationResult
		if err := json.Unmarshal(resp.Result, &result); err != nil {
			t.Fatalf("unmarshal result: %v", err)
		}
		for _, s := range result.Settings {
			if s.Field == field {
				return s.Source
			}
		}
		t.Fatalf("the response carries no %q setting", field)
		return ""
	}

	// default_registry sits in the workspace file.
	srv, _, wsPath := effectiveConfigFixture(t, nil)
	if got := sourceOf(t, "default_registry", srv); got != wsPath {
		t.Fatalf("default_registry source = %q; want the workspace file %q", got, wsPath)
	}

	// The same setting, moved to an environment override, which outranks the file.
	moved, _, wsPathMoved := effectiveConfigFixture(t, map[string]string{
		"AGGREGA_DEFAULT_REGISTRY": "env.example.test",
	})
	got := sourceOf(t, "default_registry", moved)
	if got == wsPathMoved {
		t.Fatalf("default_registry is still attributed to the workspace file %q; "+
			"the attribution did not follow the setting to the layer that now determines it", got)
	}
	if got != "env:AGGREGA_DEFAULT_REGISTRY" {
		t.Errorf("default_registry source = %q; want %q", got, "env:AGGREGA_DEFAULT_REGISTRY")
	}
}

// TestEffectiveConfigurationLayersJoinAgainstEachSource pins the contract that
// makes the two halves of the response readable together: every source naming a
// contributing layer appears verbatim in layers, and the defaults label does
// not, because the defaults are not a layer that contributed.
//
// Without it the two halves could spell the same layer differently — the
// cascade records an environment override one way and a source label another —
// and a client joining them would silently match nothing.
func TestEffectiveConfigurationLayersJoinAgainstEachSource(t *testing.T) {
	srv, globalPath, wsPath := effectiveConfigFixture(t, map[string]string{
		"AGGREGA_SIGNING_REQUIRED": "true",
	})

	resp := callEffectiveConfiguration(t, srv)
	if resp.Error != nil {
		t.Fatalf("effectiveConfiguration returned an error envelope: %+v", resp.Error)
	}
	var result effectiveConfigurationResult
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}

	inLayers := map[string]bool{}
	for _, l := range result.Layers {
		inLayers[l] = true
	}

	for _, want := range []string{globalPath, wsPath, "env:AGGREGA_SIGNING_REQUIRED"} {
		if !inLayers[want] {
			t.Errorf("layers does not carry %q; layers = %v", want, result.Layers)
		}
	}
	if inLayers["(built-in defaults)"] {
		t.Errorf("layers names the built-in defaults, which is not a contributing layer: %v", result.Layers)
	}

	// The cascade is lowest precedence first: the global layer precedes the
	// workspace file, which precedes the environment override.
	if got := indexOf(result.Layers, globalPath); got != 0 {
		t.Errorf("the global layer is at index %d; want 0 (lowest precedence first): %v", got, result.Layers)
	}
	if indexOf(result.Layers, wsPath) < indexOf(result.Layers, globalPath) {
		t.Errorf("the workspace file precedes the global layer: %v", result.Layers)
	}
	if indexOf(result.Layers, "env:AGGREGA_SIGNING_REQUIRED") != len(result.Layers)-1 {
		t.Errorf("the environment override is not the highest-precedence layer: %v", result.Layers)
	}

	// Every source that is not the defaults label names a layer in the list, so
	// the join holds for the whole response rather than for the sampled rows.
	for _, s := range result.Settings {
		if s.Source == "(built-in defaults)" {
			continue
		}
		if !inLayers[s.Source] {
			t.Errorf("setting %q names source %q, which appears in no layer: %v", s.Field, s.Source, result.Layers)
		}
	}
}

// indexOf returns the position of want in list, or -1.
func indexOf(list []string, want string) int {
	for i, v := range list {
		if v == want {
			return i
		}
	}
	return -1
}

// TestEffectiveConfigurationCarriesNoPosition pins that the wire object carries
// the source and not a position within it.
//
// The underlying provenance value does resolve a position for every setting a
// config file supplied, so this is a deliberate narrowing rather than a
// limitation, and it needs a test: adding the field would be a one-line change
// that no other assertion here would notice.
func TestEffectiveConfigurationCarriesNoPosition(t *testing.T) {
	srv, _, _ := effectiveConfigFixture(t, nil)

	resp := callEffectiveConfiguration(t, srv)
	if resp.Error != nil {
		t.Fatalf("effectiveConfiguration returned an error envelope: %+v", resp.Error)
	}

	// Read the raw objects rather than the decoded struct: decoding into the
	// struct would drop an extra key silently, which is the thing under test.
	var raw struct {
		Settings []map[string]json.RawMessage `json:"settings"`
	}
	if err := json.Unmarshal(resp.Result, &raw); err != nil {
		t.Fatalf("unmarshal raw result: %v", err)
	}
	if len(raw.Settings) == 0 {
		t.Fatal("the response carries no settings, so this test would prove nothing")
	}
	for _, s := range raw.Settings {
		for key := range s {
			switch key {
			case "field", "value", "source":
			default:
				t.Errorf("a setting carries the unexpected key %q; the wire object is field, value and source", key)
			}
		}
	}

	// The provenance the method reads does resolve a position for a
	// file-attributed setting, so the omission above is a narrowing this method
	// performs rather than an absence in what it was handed.
	origins, err := srv.resolvedWorkspace.ValueOrigins()
	if err != nil {
		t.Fatalf("ValueOrigins: %v", err)
	}
	positioned := false
	for _, o := range origins {
		if o.Position != nil {
			positioned = true
			break
		}
	}
	if !positioned {
		t.Fatal("no setting resolved a position, so the omission this test pins is not observable here")
	}
}

// TestEffectiveConfigurationRefusesWithoutAResolvedWorkspace is the negative
// control: the method can fail, and it fails as an error envelope rather than a
// panic or an empty success.
//
// An empty success would be the damaging answer — a client would read "this
// workspace has no configuration" from a daemon that simply could not report
// one.
func TestEffectiveConfigurationRefusesWithoutAResolvedWorkspace(t *testing.T) {
	srv := newTestServer(t, t.TempDir(), nil, nil)
	srv.resolvedWorkspace = nil

	resp := callEffectiveConfiguration(t, srv)
	if resp.Error == nil {
		t.Fatalf("a daemon holding no resolved workspace answered successfully: %s", resp.Result)
	}
	if resp.Error.Code != jsonRPCInternalError {
		t.Errorf("error code = %d; want %d (an internal condition, not a workspace state a user acts on)",
			resp.Error.Code, jsonRPCInternalError)
	}
	if !strings.Contains(resp.Error.Message, "resolved workspace") {
		t.Errorf("the refusal does not say what was missing: %q", resp.Error.Message)
	}
}

// TestEffectiveConfigurationReportsTheDaemonsOwnResolution pins that the method
// answers from the workspace the daemon resolved at startup rather than
// resolving again when asked.
//
// The two differ the moment a config file changes underneath a running session,
// and a client comparing this answer with the handshake's workspace root has to
// be able to trust that both describe one resolution. The proof is that the
// reported value survives a rewrite of the file it came from.
func TestEffectiveConfigurationReportsTheDaemonsOwnResolution(t *testing.T) {
	srv, _, wsPath := effectiveConfigFixture(t, nil)

	// Rewrite the workspace layer after the daemon resolved it.
	if err := os.WriteFile(wsPath, []byte("workspace:\n  catalog_dirs:\n    - ./catalog\ndefault_registry: rewritten.example.test\n"), 0o644); err != nil {
		t.Fatalf("rewrite the workspace layer: %v", err)
	}

	resp := callEffectiveConfiguration(t, srv)
	if resp.Error != nil {
		t.Fatalf("effectiveConfiguration returned an error envelope: %+v", resp.Error)
	}
	var result effectiveConfigurationResult
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	for _, s := range result.Settings {
		if s.Field != "default_registry" {
			continue
		}
		if s.Value != "workspace.example.test" {
			t.Errorf("default_registry value = %q; want the value resolved at startup, %q — "+
				"the method re-resolved instead of reporting the session's own configuration",
				s.Value, "workspace.example.test")
		}
		return
	}
	t.Fatal("the response carries no default_registry setting")
}

// TestEffectiveConfigurationValueIsFrozenWhileAttributionIsNot pins the seam
// between the two halves of the answer, which the documented contract states and
// which nothing else here would notice.
//
// A value is the session's resolution, read once at startup. An attribution is
// worked out when the request arrives, by replaying the cascade against the
// files as they stand. So an edit during a session can move a source while the
// value beside it does not move, and a client comparing the two has to know
// that. The proof removes a key from the layer that supplied it: the replay then
// finds no layer setting it and says so, while the value stays what the daemon
// resolved.
func TestEffectiveConfigurationValueIsFrozenWhileAttributionIsNot(t *testing.T) {
	srv, globalPath, _ := effectiveConfigFixture(t, nil)

	// Pre-condition: offline is true, and the global layer is what says so.
	before := settingsByField(t, callEffectiveConfiguration(t, srv))
	if got := before["offline"]; got.Value != "true" || got.Source != globalPath {
		t.Fatalf("offline = %q from %q; want %q from the global layer %q",
			got.Value, got.Source, "true", globalPath)
	}

	// Remove the key from the layer that supplied it, mid-session.
	if err := os.WriteFile(globalPath, []byte("auto_pull: false\n"), 0o644); err != nil {
		t.Fatalf("rewrite the global layer: %v", err)
	}

	after := settingsByField(t, callEffectiveConfiguration(t, srv))
	got := after["offline"]
	if got.Value != "true" {
		t.Errorf("offline value = %q; want %q — the value is the session's resolution and must not move",
			got.Value, "true")
	}
	if got.Source == globalPath {
		t.Errorf("offline source is still %q; the attribution did not re-read the file it replays", got.Source)
	}
	if got.Source != "(built-in defaults)" {
		t.Errorf("offline source = %q; want %q — no layer sets it any more",
			got.Source, "(built-in defaults)")
	}
}

// settingsByField decodes a response and indexes its settings by field name.
func settingsByField(t *testing.T, resp jsonRPCResponse) map[string]effectiveConfigurationSetting {
	t.Helper()
	if resp.Error != nil {
		t.Fatalf("effectiveConfiguration returned an error envelope: %+v", resp.Error)
	}
	var result effectiveConfigurationResult
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	out := make(map[string]effectiveConfigurationSetting, len(result.Settings))
	for _, s := range result.Settings {
		out[s.Field] = s
	}
	return out
}
