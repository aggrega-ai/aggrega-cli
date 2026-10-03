// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package mcp

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/egress"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// The offline posture is reachable from three layers — the configuration file,
// AGGREGA_OFFLINE and the --offline flag — and the flag is the top of that
// ladder. This server resolves a workspace afresh for every call, so the flag
// reaches its tools only if the layer the command line composed is handed to the
// server and applied on top of each of those resolutions. The proofs here hold
// both halves: every layer, in both directions, as a call's resolution reads it;
// and the layer handed in through the exported constructors, on both transports,
// read off what the server actually did on the network.

// pinOfflineEnvironment points the machine-wide configuration at an empty
// directory, so a resolution reads the workspace under test and nothing the
// machine running the test happens to hold, and sets AGGREGA_OFFLINE to value
// (the empty string leaves the layer unset).
func pinOfflineEnvironment(t *testing.T, value string) {
	t.Helper()
	empty := t.TempDir()
	t.Setenv("HOME", empty)
	t.Setenv("XDG_CONFIG_HOME", empty)
	t.Setenv("AGGREGA_OFFLINE", value)
}

// offlineLayerWorkspace writes a workspace root whose configuration file carries
// body beneath the root marker, and returns the root.
func offlineLayerWorkspace(t *testing.T, body string) string {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, workspaceconfig.ConfigFilename),
		[]byte("workspace: {}\n"+body), 0o644); err != nil {
		t.Fatalf("write the workspace configuration: %v", err)
	}
	return root
}

// offlineOrigin reports the effective offline posture a resolved workspace
// carries and the layer its provenance attributes the posture to.
func offlineOrigin(t *testing.T, rw *workspaceconfig.ResolvedWorkspace) (bool, string) {
	t.Helper()
	origins, err := rw.ValueOrigins()
	if err != nil {
		t.Fatalf("read the provenance: %v", err)
	}
	for _, o := range origins {
		if o.Field == workspaceconfig.OfflineSettingKey {
			return rw.Config().Offline, o.Source
		}
	}
	t.Fatalf("the provenance carries no %s setting", workspaceconfig.OfflineSettingKey)
	return false, ""
}

// TestEveryCallResolvesTheCommandLineOfflineLayer drives the one resolution
// every tool and the documentation acquisition read through, over each layer
// that can set the posture, in both directions, and over both workspaces a call
// can be answered in: the one the process was started over, and one the call
// names for itself.
//
// Three things are read per row, because each fails differently. The effective
// value is what a tool reads. The attributed layer is what a refusal names, so a
// flag that set the posture must be the layer named rather than a file the reader
// never touched. And the egress gate's own verdict is what actually decides
// whether a tool reaches the network, so it is asked rather than inferred from
// the value.
//
// The two `--offline=false` rows are the ones a layer that read the flag's value
// rather than whether it was typed would get wrong in the other direction, and
// the "nothing set" row is the control without which the flag rows would not
// separate a flag that is applied from a server that is always offline.
func TestEveryCallResolvesTheCommandLineOfflineLayer(t *testing.T) {
	on, off := true, false
	for _, tc := range []struct {
		name    string
		config  string
		env     string
		flags   workspaceconfig.FlagOverlay
		want    bool
		wantSrc string // "" means the absolute path of the configuration file
	}{
		{name: "nothing set anywhere", want: false, wantSrc: "(built-in defaults)"},
		{name: "the configuration key alone", config: "offline: true\n", want: true, wantSrc: ""},
		{name: "the environment alone", env: "true", want: true, wantSrc: "env:AGGREGA_OFFLINE"},
		{name: "the flag alone", flags: workspaceconfig.FlagOverlay{Offline: &on}, want: true, wantSrc: "--offline"},
		{
			name:    "the flag turns off what the configuration key turned on",
			config:  "offline: true\n",
			flags:   workspaceconfig.FlagOverlay{Offline: &off},
			want:    false,
			wantSrc: "--offline",
		},
		{
			name:    "the flag turns off what the environment turned on",
			env:     "true",
			flags:   workspaceconfig.FlagOverlay{Offline: &off},
			want:    false,
			wantSrc: "--offline",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pinOfflineEnvironment(t, tc.env)
			served := offlineLayerWorkspace(t, tc.config)
			named := offlineLayerWorkspace(t, tc.config)
			s := &server{out: io.Discard, logw: io.Discard, startDir: served, flags: tc.flags}

			for _, w := range []struct{ which, rootArg string }{
				{"the workspace the process was started over", ""},
				{"a workspace the call names for itself", named},
			} {
				tw, err := s.resolveToolWorkspace(w.rootArg, nil)
				if err != nil {
					t.Fatalf("%s: resolve: %v", w.which, err)
				}
				got, src := offlineOrigin(t, tw.rw)
				if got != tc.want || tw.cfg.Offline != tc.want {
					t.Errorf("%s: offline = %v (the configuration a tool reads: %v); want %v",
						w.which, got, tw.cfg.Offline, tc.want)
				}
				switch {
				case tc.wantSrc == "":
					if !filepath.IsAbs(src) {
						t.Errorf("%s: the posture is attributed to %q; want the configuration file that set it",
							w.which, src)
					}
				case src != tc.wantSrc:
					t.Errorf("%s: the posture is attributed to %q; want %q", w.which, src, tc.wantSrc)
				}
				_, gateErr := egress.RoundTripper(tw.cfg)
				if refused := stderrors.Is(gateErr, egress.ErrOfflineEgress()); refused != tc.want {
					t.Errorf("%s: the egress gate refused = %v (%v); want %v", w.which, refused, gateErr, tc.want)
				}
			}
		})
	}
}

// countingRegistry is a registry address on this machine that answers every
// request not-found and counts what reached it, so a run can be read for whether
// it reached a registry at all rather than for what one answered.
func countingRegistry(t *testing.T) (host string, reached func() int64) {
	t.Helper()
	var hits atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		http.NotFound(w, nil)
	}))
	t.Cleanup(srv.Close)
	return strings.TrimPrefix(srv.URL, "http://"), hits.Load
}

// offlineCallsOverStdio starts the server the way the command layer starts it on
// its own streams, handed flags, and sends it the two calls the proof below reads.
func offlineCallsOverStdio(t *testing.T, root string, flags workspaceconfig.FlagOverlay, calls []string) []string {
	t.Helper()
	var out strings.Builder
	if err := Run(context.Background(), Config{
		Stdin:         strings.NewReader(strings.Join(calls, "\n") + "\n"),
		Stdout:        &out,
		Stderr:        io.Discard,
		ServerVersion: "9.9.9-fixture",
		WorkspaceRoot: root,
		Flags:         flags,
	}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != len(calls) {
		t.Fatalf("the run framed %d lines for %d calls: %q", len(lines), len(calls), out.String())
	}
	answers := make([]string, len(calls))
	for _, line := range lines {
		var frame rpcResponse
		if err := json.Unmarshal([]byte(line), &frame); err != nil {
			t.Fatalf("an answer is not a JSON-RPC frame: %q: %v", line, err)
		}
		var id int
		if err := json.Unmarshal(frame.ID, &id); err != nil || id < 1 || id > len(calls) {
			t.Fatalf("an answer carries id %s, which no call sent", frame.ID)
		}
		answers[id-1] = line
	}
	return answers
}

// offlineCallsOverHTTP starts the listener's handler the way the command layer
// does — through the exported constructor, handed flags — and posts the same two
// calls to it. The test-only authenticator is installed on the handler the
// constructor returned, because what this proof is about sits behind the
// credential check.
func offlineCallsOverHTTP(t *testing.T, root string, flags workspaceconfig.FlagOverlay, calls []string) []string {
	t.Helper()
	handler, err := NewHTTPHandler(context.Background(),
		Config{Stderr: io.Discard, ServerVersion: "9.9.9-fixture", WorkspaceRoot: root, Flags: flags},
		defaultTransportConfig())
	if err != nil {
		t.Fatalf("NewHTTPHandler: %v", err)
	}
	h, ok := handler.(*httpHandler)
	if !ok {
		t.Fatalf("NewHTTPHandler returned %T", handler)
	}
	h.auth = acceptAllAuthenticator{}
	ts := httptest.NewServer(h)
	t.Cleanup(ts.Close)

	names := []string{"describe", "config-show"}
	answers := make([]string, len(calls))
	for i, call := range calls {
		req, err := http.NewRequest(http.MethodPost, ts.URL+endpointPath, strings.NewReader(call))
		if err != nil {
			t.Fatalf("build request: %v", err)
		}
		for name, value := range modernHeaders("tools/call", names[i]) {
			req.Header.Set(name, value)
		}
		resp, err := ts.Client().Do(req)
		if err != nil {
			t.Fatalf("post %s: %v", names[i], err)
		}
		raw, err := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if err != nil {
			t.Fatalf("read the %s answer: %v", names[i], err)
		}
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("%s over HTTP: status %d, body %q", names[i], resp.StatusCode, raw)
		}
		answers[i] = string(raw)
	}
	return answers
}

// toolAnswer reads one tools/call answer down to the tool result it carries.
func toolAnswer(t *testing.T, raw string) callToolResult {
	t.Helper()
	var frame rpcResponse
	if err := json.Unmarshal([]byte(raw), &frame); err != nil {
		t.Fatalf("the answer is not a JSON-RPC frame: %q: %v", raw, err)
	}
	if frame.Error != nil {
		t.Fatalf("the call was refused at the protocol level: %s", frame.Error.Message)
	}
	var call callToolResult
	if err := json.Unmarshal(frame.Result, &call); err != nil {
		t.Fatalf("the result is not a tool result: %v", err)
	}
	return call
}

// TestTheCommandLineOfflineLayerReachesEveryCallOnBothTransports is the proof
// read off the network rather than off a value. The served workspace names, as
// its registry, an address on this machine that counts what reaches it — so
// nothing in it leaves the machine whichever way a row goes — and sets no
// posture of its own. Each transport is started through its exported
// constructor, handed the flag layer exactly as the command layer hands it, and
// asked two things: to describe an entity that registry publishes — the one
// registry read a caller can ask for — and to report the configuration in force.
// The documentation acquisition runs before either is answered and reaches for
// the same registry.
//
// Handed --offline, nothing reaches the registry at all: neither the acquisition
// nor the description, which is refused by the egress gate naming the posture,
// and the configuration reading names the flag as the layer that set it. The
// control row is the same launch with no flag, and it is what makes the zero
// above evidence: there the registry is reached, so the address is one the
// server does reach when nothing stops it.
func TestTheCommandLineOfflineLayerReachesEveryCallOnBothTransports(t *testing.T) {
	on := true
	for _, transport := range []struct {
		name string
		run  func(*testing.T, string, workspaceconfig.FlagOverlay, []string) []string
	}{
		{"over standard input and output", offlineCallsOverStdio},
		{"over HTTP", offlineCallsOverHTTP},
	} {
		for _, posture := range []struct {
			name    string
			flags   workspaceconfig.FlagOverlay
			offline bool
		}{
			{"handed --offline", workspaceconfig.FlagOverlay{Offline: &on}, true},
			{"handed no flag (the control)", workspaceconfig.FlagOverlay{}, false},
		} {
			t.Run(transport.name+", "+posture.name, func(t *testing.T) {
				pinOfflineEnvironment(t, "")
				host, reached := countingRegistry(t)
				// Listed under registries as well as named the default, because a
				// listener reads a published entity only from a registry the
				// served workspace lists: without the entry, the HTTP rows would
				// be refused by that admission before the posture was consulted.
				root := offlineLayerWorkspace(t, "default_registry: \""+host+"\"\n"+
					"registries:\n  - host: \""+host+"\"\n")
				calls := []string{
					modernRequest(t, 1, "tools/call", map[string]any{
						"name":      "describe",
						"arguments": map[string]any{"name": "oci://" + host + "/component/core/api:1.0.0"},
					}),
					modernRequest(t, 2, "tools/call", map[string]any{
						"name":      "config-show",
						"arguments": map[string]any{},
					}),
				}
				answers := transport.run(t, root, posture.flags, calls)

				if hits := reached(); posture.offline && hits != 0 {
					t.Errorf("the registry was reached %d time(s) by a server handed --offline", hits)
				} else if !posture.offline && hits == 0 {
					t.Fatal("the control reached the registry zero times, so a zero under --offline would " +
						"measure the fixture rather than the flag")
				}

				described := toolAnswer(t, answers[0])
				if posture.offline {
					text := ""
					if len(described.Content) > 0 {
						text = described.Content[0].Text
					}
					if !described.IsError || !strings.Contains(text, "offline") {
						t.Errorf("describe under --offline was not refused for the posture (isError=%v): %s",
							described.IsError, text)
					}
				}

				shown := toolAnswer(t, answers[1])
				if shown.IsError {
					t.Fatalf("config-show failed: %+v", shown.Content)
				}
				var payload configShowResult
				if err := json.Unmarshal(shown.StructuredContent, &payload); err != nil {
					t.Fatalf("the configuration reading is not its structured shape: %v", err)
				}
				wantValue, wantSrc := "false", "(built-in defaults)"
				if posture.offline {
					wantValue, wantSrc = "true", "--offline"
				}
				found := false
				for _, s := range payload.Settings {
					if s.Field != workspaceconfig.OfflineSettingKey {
						continue
					}
					found = true
					if told(s.Value) != wantValue || told(s.Source) != wantSrc {
						t.Errorf("config-show reports offline = %q from %q; want %q from %q",
							told(s.Value), told(s.Source), wantValue, wantSrc)
					}
				}
				if !found {
					t.Errorf("config-show reports no %s setting", workspaceconfig.OfflineSettingKey)
				}
			})
		}
	}
}
