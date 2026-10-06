// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package serve

import (
	"bufio"
	"context"
	"encoding/json"
	stderrors "errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aggrega-ai/aggrega-cli/pkg/egress"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// The offline posture is reachable from three layers — the configuration file,
// AGGREGA_OFFLINE and the --offline flag — and the flag is the top of that
// ladder. The daemon resolves the workspace it was launched over and then each
// workspace a request names, so the flag reaches its requests only if the layer
// the command line composed is handed to it and applied on top of every one of
// those resolutions. The proofs here hold both halves: every layer, in both
// directions, as the daemon's one resolution reads it; and the layer handed in
// through the exported entry point, read off the answers and off the network.

// pinDaemonOfflineEnvironment points the machine-wide configuration at an empty
// directory, so a resolution reads the workspace under test and nothing the
// machine running the test happens to hold, and sets AGGREGA_OFFLINE to value
// (the empty string leaves the layer unset).
func pinDaemonOfflineEnvironment(t *testing.T, value string) {
	t.Helper()
	empty := t.TempDir()
	t.Setenv("HOME", empty)
	t.Setenv("XDG_CONFIG_HOME", empty)
	t.Setenv("AGGREGA_OFFLINE", value)
}

// daemonOfflineWorkspace writes a workspace root whose configuration file
// carries body beneath the root marker, and returns the root.
func daemonOfflineWorkspace(t *testing.T, body string) string {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, workspaceconfig.ConfigFilename),
		[]byte("workspace: {}\n"+body), 0o644); err != nil {
		t.Fatalf("write the workspace configuration: %v", err)
	}
	return root
}

// TestEveryWorkspaceTheDaemonResolvesCarriesTheCommandLineOfflineLayer drives
// the daemon's one resolution — the launch resolution and every workspace a
// request names both come through it — over each layer that can set the
// posture, in both directions.
//
// Three things are read per row, because each fails differently: the effective
// value every request reads; the layer the provenance attributes it to, which is
// what a refusal names and what the effective-configuration answer reports; and
// the egress gate's own verdict, which is what actually decides whether a
// request reaches the network. The "nothing set" row is the control without
// which the flag rows would not separate an applied flag from a daemon that is
// always offline.
func TestEveryWorkspaceTheDaemonResolvesCarriesTheCommandLineOfflineLayer(t *testing.T) {
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
			pinDaemonOfflineEnvironment(t, tc.env)
			root := daemonOfflineWorkspace(t, tc.config)

			rw, held, err := resolveWorkspace(root, tc.flags)
			if err != nil || held != nil {
				t.Fatalf("resolve: %v (held %v)", err, held)
			}
			origins, err := rw.ValueOrigins()
			if err != nil {
				t.Fatalf("read the provenance: %v", err)
			}
			src := ""
			for _, o := range origins {
				if o.Field == workspaceconfig.OfflineSettingKey {
					src = o.Source
				}
			}
			if got := rw.Config().Offline; got != tc.want {
				t.Errorf("offline = %v; want %v", got, tc.want)
			}
			switch {
			case tc.wantSrc == "":
				if !filepath.IsAbs(src) {
					t.Errorf("the posture is attributed to %q; want the configuration file that set it", src)
				}
			case src != tc.wantSrc:
				t.Errorf("the posture is attributed to %q; want %q", src, tc.wantSrc)
			}
			_, gateErr := egress.RoundTripper(rw.Config())
			if refused := stderrors.Is(gateErr, egress.ErrOfflineEgress()); refused != tc.want {
				t.Errorf("the egress gate refused = %v (%v); want %v", refused, gateErr, tc.want)
			}
		})
	}
}

// daemonCountingRegistry is a registry address on this machine that answers
// every request not-found and counts what reached it, so a run can be read for
// whether it reached a registry at all rather than for what one answered.
func daemonCountingRegistry(t *testing.T) (host string, reached func() int64) {
	t.Helper()
	var hits atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		http.NotFound(w, nil)
	}))
	t.Cleanup(srv.Close)
	return strings.TrimPrefix(srv.URL, "http://"), hits.Load
}

// daemonExchange runs the daemon through its exported entry point over pipes,
// launched over launchRoot with flags, sends each framed request in turn, reads
// each one's answer before sending the next, and then ends the input and waits
// for the daemon to return. Frames that carry no id are notifications and are
// passed over.
func daemonExchange(t *testing.T, launchRoot string, flags workspaceconfig.FlagOverlay, requests [][]byte) []jsonRPCResponse {
	t.Helper()
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	done := make(chan error, 1)
	go func() {
		err := Run(context.Background(), RunConfig{
			Stdin:         inR,
			Stdout:        outW,
			Stderr:        io.Discard,
			WorkspaceRoot: launchRoot,
			DrainTimeout:  50 * time.Millisecond,
			Flags:         flags,
		})
		_ = outW.CloseWithError(err)
		done <- err
	}()
	out := bufio.NewReader(outR)
	answers := make([]jsonRPCResponse, 0, len(requests))
	for i, req := range requests {
		if _, err := inW.Write(req); err != nil {
			t.Fatalf("send request %d: %v", i+1, err)
		}
		for {
			body, err := readMessage(out)
			if err != nil {
				t.Fatalf("read the answer to request %d: %v", i+1, err)
			}
			var resp jsonRPCResponse
			if err := json.Unmarshal(body, &resp); err != nil {
				t.Fatalf("an answer is not a JSON-RPC frame: %q: %v", body, err)
			}
			if len(resp.ID) == 0 {
				continue
			}
			answers = append(answers, resp)
			break
		}
	}
	_ = inW.Close()
	go func() { _, _ = io.Copy(io.Discard, out) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("the daemon did not return within the liveness ceiling after its input ended")
	}
	return answers
}

// TestTheLaunchCommandLineReachesEveryWorkspaceTheDaemonServes is the proof read
// off the daemon's answers and off the network. Two workspaces name, as their
// registry, an address on this machine that counts what reaches it — so nothing
// here leaves the machine whichever way a row goes — and set no posture of their
// own. The daemon is launched over the first and handed the flag layer exactly as
// the command layer hands it; it is asked for the effective configuration of the
// workspace it was launched over and of the second, which a request names for
// the first time, and asked to refresh the second's registry index — a request
// whose whole work is a registry round trip.
//
// Handed --offline, both workspaces report the posture as set by the flag, the
// refresh is refused naming that flag, and the registry is never reached. The
// control row is the same launch with no flag, and it is what makes the zero
// evidence: there the refresh reaches the registry.
func TestTheLaunchCommandLineReachesEveryWorkspaceTheDaemonServes(t *testing.T) {
	on := true
	for _, posture := range []struct {
		name    string
		flags   workspaceconfig.FlagOverlay
		offline bool
	}{
		{"handed --offline", workspaceconfig.FlagOverlay{Offline: &on}, true},
		{"handed no flag (the control)", workspaceconfig.FlagOverlay{}, false},
	} {
		t.Run(posture.name, func(t *testing.T) {
			pinDaemonOfflineEnvironment(t, "")
			host, reached := daemonCountingRegistry(t)
			body := "default_registry: \"" + host + "\"\nregistries:\n  - host: \"" + host + "\"\n"
			launched := daemonOfflineWorkspace(t, body)
			named := daemonOfflineWorkspace(t, body)

			answers := daemonExchange(t, launched, posture.flags, [][]byte{
				framedRequestAt(t, launched, 1, "aggrega/effectiveConfiguration", map[string]any{}),
				framedRequestAt(t, named, 2, "aggrega/effectiveConfiguration", map[string]any{}),
				framedRequestAt(t, named, 3, "aggrega/registryUpdate", map[string]any{"registry": host}),
			})

			wantValue, wantSrc := "false", "(built-in defaults)"
			if posture.offline {
				wantValue, wantSrc = "true", "--offline"
			}
			for i, which := range []string{"the workspace the daemon was launched over", "a workspace a request named"} {
				if answers[i].Error != nil {
					t.Fatalf("%s: aggrega/effectiveConfiguration refused: %s", which, answers[i].Error.Message)
				}
				var result effectiveConfigurationResult
				if err := json.Unmarshal(answers[i].Result, &result); err != nil {
					t.Fatalf("%s: the answer is not the effective-configuration shape: %v", which, err)
				}
				found := false
				for _, s := range result.Settings {
					if s.Field != workspaceconfig.OfflineSettingKey {
						continue
					}
					found = true
					if s.Value != wantValue || s.Source != wantSrc {
						t.Errorf("%s: offline = %q from %q; want %q from %q", which, s.Value, s.Source, wantValue, wantSrc)
					}
				}
				if !found {
					t.Errorf("%s: no %s setting is reported", which, workspaceconfig.OfflineSettingKey)
				}
				// A setting's source joins the contributing layers directly, so a
				// flag that set one is a layer that contributed.
				if posture.offline && !slices.Contains(result.Layers, "--offline") {
					t.Errorf("%s: the contributing layers %v do not carry the flag that set the posture", which, result.Layers)
				}
			}

			refresh := answers[2]
			hits := reached()
			if posture.offline {
				if refresh.Error == nil || !strings.Contains(refresh.Error.Message, "--offline") {
					t.Errorf("the registry refresh under --offline was not refused naming the flag: %+v", refresh.Error)
				}
				if hits != 0 {
					t.Errorf("the registry was reached %d time(s) by a daemon handed --offline", hits)
				}
			} else if hits == 0 {
				t.Fatal("the control reached the registry zero times, so a zero under --offline would measure the " +
					"fixture rather than the flag")
			}
		})
	}
}
