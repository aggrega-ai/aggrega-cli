// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// The two long-running commands resolve a workspace afresh for each call or
// request rather than reading the one this invocation resolved, so the global
// --offline flag reaches what they answer only if each command hands its server
// the layer the root command composed. The servers' own packages prove what they
// do with the layer once they hold it; these drive the real command tree and
// prove that each command hands it over, in both directions.

// serverOfflineSetting is one row of the proof below: what was typed, what the
// workspace's own configuration says, and what the server must then report for
// the offline setting and the layer that set it.
type serverOfflineSetting struct {
	name      string
	args      []string
	config    string
	wantValue string
	wantSrc   string // "" means the absolute path of the configuration file
}

var serverOfflineSettings = []serverOfflineSetting{
	{name: "no flag (the control)", wantValue: "false", wantSrc: "(built-in defaults)"},
	{name: "--offline", args: []string{"--offline"}, wantValue: "true", wantSrc: "--offline"},
	{
		name:      "--offline=false over a configuration that turns the posture on",
		args:      []string{"--offline=false"},
		config:    "offline: true\n",
		wantValue: "false",
		wantSrc:   "--offline",
	},
}

// loopbackRegistryWorkspace writes a workspace whose registry is an address on
// this machine that answers every request not-found and counts what reached it,
// carrying extra beneath it, and returns the root and the count.
//
// The registry is on this machine so that no row here can reach past it: the
// server for `aggrega mcp` acquires its documentation bundle from the
// workspace's registry as it starts, and a row whose posture is online makes
// that request.
func loopbackRegistryWorkspace(t *testing.T, extra string) (string, func() int64) {
	t.Helper()
	var hits atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		http.NotFound(w, nil)
	}))
	t.Cleanup(srv.Close)
	host := strings.TrimPrefix(srv.URL, "http://")
	return writeOfflineWorkspace(t, "default_registry: \""+host+"\"\n"+extra), hits.Load
}

// offlineSettingIn finds the offline setting in a list of (field, value,
// source) rows, failing when there is none.
func offlineSettingIn(t *testing.T, rows []struct{ Field, Value, Source string }) (string, string) {
	t.Helper()
	for _, r := range rows {
		if r.Field == workspaceconfig.OfflineSettingKey {
			return r.Value, r.Source
		}
	}
	t.Fatalf("the answer reports no %s setting", workspaceconfig.OfflineSettingKey)
	return "", ""
}

// assertOfflineSetting holds one reported offline setting to a row.
func assertOfflineSetting(t *testing.T, surface string, row serverOfflineSetting, value, src string) {
	t.Helper()
	if value != row.wantValue {
		t.Errorf("%s reports offline = %q; want %q", surface, value, row.wantValue)
	}
	switch {
	case row.wantSrc == "":
		if !filepath.IsAbs(src) {
			t.Errorf("%s attributes the posture to %q; want the configuration file that set it", surface, src)
		}
	case src != row.wantSrc:
		t.Errorf("%s attributes the posture to %q; want %q", surface, src, row.wantSrc)
	}
}

// TestTheMCPCommandHandsItsServerTheOfflineFlag runs `aggrega [flag] mcp` from
// the real command tree over its own streams and asks the configuration reading
// what is in force. The reading is resolved by the server, per call, so a flag
// it reports is a flag the command handed it.
//
// 🔴 PLANTED DEFECT: drop Flags from the mcp.Config literal runMCP builds and the
// --offline row reports the built-in default — and, being online, reaches the
// loopback registry for the documentation bundle, which the zero-reach check
// below also catches.
func TestTheMCPCommandHandsItsServerTheOfflineFlag(t *testing.T) {
	// Not parallel: it sets the process environment and drives the command tree,
	// whose pre-run writes the process-global display decision.
	for _, row := range serverOfflineSettings {
		t.Run(row.name, func(t *testing.T) {
			empty := t.TempDir()
			t.Setenv("HOME", empty)
			t.Setenv("XDG_CONFIG_HOME", empty)
			t.Setenv("AGGREGA_OFFLINE", "")
			root, reached := loopbackRegistryWorkspace(t, row.config)

			var stdin, stdout, stderr bytes.Buffer
			stdin.WriteString(rpcLine(1, "initialize", `{"protocolVersion":"2025-06-18"}`))
			stdin.WriteString(rpcLine(2, "tools/call", `{"name":"config-show","arguments":{}}`))
			cmd := newRootCmd()
			cmd.SetIn(&stdin)
			cmd.SetOut(&stdout)
			cmd.SetErr(&stderr)
			cmd.SetArgs(append(append([]string{}, row.args...), "mcp", "--workspace-root", root))
			if code := executeRoot(context.Background(), cmd); code != 0 {
				t.Fatalf("aggrega mcp exited %d\nstderr:\n%s", code, stderr.String())
			}

			answers := answersByID(t, stdout.String(), 1, 2)
			if answers[2].Error != nil {
				t.Fatalf("config-show was refused: %s", answers[2].Error.Message)
			}
			var call struct {
				StructuredContent struct {
					Settings []struct{ Field, Value, Source string } `json:"settings"`
				} `json:"structuredContent"`
				IsError bool `json:"isError"`
			}
			if err := json.Unmarshal(answers[2].Result, &call); err != nil || call.IsError {
				t.Fatalf("config-show did not answer with its reading (isError=%v): %v\n%s",
					call.IsError, err, answers[2].Result)
			}
			value, src := offlineSettingIn(t, call.StructuredContent.Settings)
			assertOfflineSetting(t, "config-show", row, value, src)

			if row.wantValue == "true" && reached() != 0 {
				t.Errorf("a server started with %v reached the registry %d time(s)", row.args, reached())
			}
		})
	}
}

// TestTheServeCommandHandsItsDaemonTheOfflineFlag runs `aggrega [flag] serve`
// from the real command tree and asks the daemon for the effective configuration
// of the workspace it was launched over. The daemon resolves that workspace
// itself, so a flag it reports is a flag the command handed it.
//
// 🔴 PLANTED DEFECT: drop Flags from the serve.RunConfig literal runServe builds
// and the --offline row reports the built-in default.
func TestTheServeCommandHandsItsDaemonTheOfflineFlag(t *testing.T) {
	// Not parallel: it sets the process environment and drives the command tree,
	// whose pre-run writes the process-global display decision.
	for _, row := range serverOfflineSettings {
		t.Run(row.name, func(t *testing.T) {
			empty := t.TempDir()
			t.Setenv("HOME", empty)
			t.Setenv("XDG_CONFIG_HOME", empty)
			t.Setenv("AGGREGA_OFFLINE", "")
			root, _ := loopbackRegistryWorkspace(t, row.config)

			body, err := json.Marshal(map[string]any{
				"jsonrpc": "2.0", "id": 1, "method": "aggrega/effectiveConfiguration",
				"params": map[string]any{"workspaceRoot": root},
			})
			if err != nil {
				t.Fatalf("marshal the request: %v", err)
			}
			var stdin, stdout, stderr bytes.Buffer
			fmt.Fprintf(&stdin, "Content-Length: %d\r\n\r\n", len(body))
			stdin.Write(body)
			cmd := newRootCmd()
			cmd.SetIn(&stdin)
			cmd.SetOut(&stdout)
			cmd.SetErr(&stderr)
			cmd.SetArgs(append(append([]string{}, row.args...), "serve", "--workspace-root", root))
			if code := executeRoot(context.Background(), cmd); code != 0 {
				t.Fatalf("aggrega serve exited %d\nstderr:\n%s", code, stderr.String())
			}

			frame := daemonFrameAnswering(t, stdout.Bytes(), 1)
			if raw, refused := frame["error"]; refused {
				t.Fatalf("aggrega/effectiveConfiguration was refused: %s", raw)
			}
			var result struct {
				Settings []struct{ Field, Value, Source string } `json:"settings"`
			}
			if err := json.Unmarshal(frame["result"], &result); err != nil {
				t.Fatalf("the answer is not the effective-configuration shape: %v\n%s", err, frame["result"])
			}
			value, src := offlineSettingIn(t, result.Settings)
			assertOfflineSetting(t, "aggrega/effectiveConfiguration", row, value, src)
		})
	}
}
