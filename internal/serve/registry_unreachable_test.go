// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package serve

import (
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/registryops"
)

// releasedLoopbackRegistry returns a loopback address nothing listens on: a port
// that was bound and released, so a registry configured there is refused by this
// machine and nothing leaves it.
func releasedLoopbackRegistry(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("bind a loopback port: %v", err)
	}
	addr := l.Addr().String()
	if err := l.Close(); err != nil {
		t.Fatalf("release the loopback port: %v", err)
	}
	return addr
}

// loopbackRegistry stands a loopback registry answering every request through
// handle. A loopback host is spoken to in plain HTTP by the index walk and the
// trust fetch alike, so this is the endpoint the daemon's own clients reach.
func loopbackRegistry(t *testing.T, handle http.HandlerFunc) string {
	t.Helper()
	srv := httptest.NewServer(handle)
	t.Cleanup(srv.Close)
	return strings.TrimPrefix(srv.URL, "http://")
}

// TestTheDaemonSaysWhichRegistryCouldNotBeReached drives the three answers that
// carry the could-not-be-reached key through the daemon's own dispatch, with no
// transport supplied by the test: the refresh of every configured registry, the
// refresh of the one a request names, and a registration.
//
// One fault per row. The control is a registry that answers — an empty listing
// and no advertised policy. The second answers every request with a server
// error: it was reached and failed, so the key must be absent everywhere. The
// third is a registry nothing listens for, so the key must be present
// everywhere. Each answer is read as the wire carries it, key for key, because
// absent and false decode alike and absence is what a reached registry is
// promised.
//
// The refusal's message is held to the shared walk's own error, so carrying the
// key changed what the refusal says nothing about; and a refusal the key does not
// apply to must carry no data at all, as before.
//
// Planted defects, each caught here: the refusal written without its data turns
// the last row's named refresh red; data written on every refusal turns the
// second row's red; the walk or the registration no longer reading the failure
// turns the last row's every-registry refresh or registration red; a key written
// as false rather than left absent turns the first two rows red.
func TestTheDaemonSaysWhichRegistryCouldNotBeReached(t *testing.T) {
	t.Parallel()

	for _, row := range []struct {
		name            string
		host            func(t *testing.T) string
		wantFailure     bool
		wantUnreachable bool
	}{
		{
			name: "control: a registry that answers was reached",
			host: func(t *testing.T) string {
				return loopbackRegistry(t, func(w http.ResponseWriter, r *http.Request) {
					if strings.HasPrefix(r.URL.Path, "/v2/_catalog") {
						w.Header().Set("Content-Type", "application/json")
						_, _ = io.WriteString(w, `{"repositories": []}`)
						return
					}
					http.NotFound(w, r)
				})
			},
		},
		{
			name: "a registry that answers every request with a server error was reached and failed",
			host: func(t *testing.T) string {
				return loopbackRegistry(t, func(w http.ResponseWriter, _ *http.Request) {
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusInternalServerError)
					_, _ = io.WriteString(w, `{"errors": [{"code": "UNKNOWN", "message": "internal"}]}`)
				})
			},
			wantFailure: true,
		},
		{
			name:            "a registry nothing listens for could not be reached",
			host:            releasedLoopbackRegistry,
			wantFailure:     true,
			wantUnreachable: true,
		},
	} {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			host := row.host(t)
			seed := "workspace: {}\nregistries:\n  - host: " + host + "\n"

			t.Run("refreshing every configured registry", func(t *testing.T) {
				srv, _, _ := registryWriteFixture(t, seed)
				resp := callDaemonMethod(t, srv, "aggrega/registryUpdate", map[string]any{})
				if resp.Error != nil {
					t.Fatalf("a refresh of every registry answers rather than failing; got error %d: %s",
						resp.Error.Code, resp.Error.Message)
				}
				var answer struct {
					Hosts []map[string]any `json:"hosts"`
				}
				if err := json.Unmarshal(resp.Result, &answer); err != nil {
					t.Fatalf("unmarshal the refresh: %v (raw: %s)", err, resp.Result)
				}
				if len(answer.Hosts) != 1 {
					t.Fatalf("one registry is configured; the answer carries %d host(s): %s", len(answer.Hosts), resp.Result)
				}
				hu := answer.Hosts[0]
				if _, failed := hu["error"]; failed != row.wantFailure {
					t.Errorf("the host carries an error: %v; want %v (%v)", failed, row.wantFailure, hu)
				}
				got, present := hu["unreachable"]
				if present != row.wantUnreachable || (present && got != true) {
					t.Errorf("the host's unreachable key = %v (present %v); want present and true: %v (%v)",
						got, present, row.wantUnreachable, hu)
				}
			})

			t.Run("refreshing the registry a request names", func(t *testing.T) {
				srv, _, _ := registryWriteFixture(t, seed)
				resp := callDaemonMethod(t, srv, "aggrega/registryUpdate", map[string]any{"registry": host})
				if !row.wantFailure {
					if resp.Error != nil {
						t.Fatalf("a named registry that answers is refreshed; got error %d: %s", resp.Error.Code, resp.Error.Message)
					}
					return
				}
				if resp.Error == nil {
					t.Fatalf("a named registry that failed must be refused; got a result: %s", resp.Result)
				}
				if resp.Error.Code != jsonRPCInternalError {
					t.Errorf("code = %d; want %d", resp.Error.Code, jsonRPCInternalError)
				}

				ref, _, _ := registryWriteFixture(t, seed)
				_, sharedErr := registryops.Update(t.Context(), ref.resolvedWorkspace,
					registryops.UpdateOptions{Registry: host}, io.Discard)
				if sharedErr == nil {
					t.Fatal("the shared walk naming the same registry succeeded, so the refusal has nothing to be held to")
				}
				// The shared walk's error, as every refusal carries the failure it
				// was rendered from: its condition, where it names one, identified in
				// the data and its short code ahead of the sentence.
				identified := conditionRefusal(&jsonRPCError{Message: sharedErr.Error(), cause: sharedErr})
				if resp.Error.Message != identified.Message {
					t.Errorf("the refusal's message is not the shared walk's error\n got: %q\nwant: %q",
						resp.Error.Message, identified.Message)
				}
				identity := errorData{}
				if identified.Data != nil {
					identity = *identified.Data
				}

				if !row.wantUnreachable {
					if d := resp.Error.Data; d != nil && *d != identity {
						t.Errorf("a registry that answered is refused with data %+v; want only the condition its failure names, %+v", *d, identity)
					}
					return
				}
				if resp.Error.Data == nil {
					t.Fatal("the refusal of a registry that could not be reached carries no data")
				}
				want := identity
				want.Unreachable = true
				if *resp.Error.Data != want {
					t.Errorf("the refusal's data = %+v; want exactly %+v — unreachable, and the condition the failure names", *resp.Error.Data, want)
				}
			})

			t.Run("registering it", func(t *testing.T) {
				srv, _, _ := registryWriteFixture(t, "workspace: {}\n")
				resp := callDaemonMethod(t, srv, "aggrega/registryAdd", map[string]any{"host": host, "workspace": true})
				trust := trustOnTheWire(t, resp)
				if trust["notAdvertised"] != true {
					t.Errorf("none of these registries advertises a policy, so each is reported as not advertising one: %v", trust)
				}
				got, present := trust["unreachable"]
				if present != row.wantUnreachable || (present && got != true) {
					t.Errorf("the trust outcome's unreachable key = %v (present %v); want present and true: %v (%v)",
						got, present, row.wantUnreachable, trust)
				}
				if !row.wantUnreachable && !row.wantFailure {
					want := map[string]any{
						"pinned":        false,
						"notAdvertised": true,
						"reason":        host + " publishes no trust policy (HTTP 404)",
					}
					if !reflect.DeepEqual(trust, want) {
						t.Errorf("the control's trust object = %v; want %v", trust, want)
					}
				}
			})
		})
	}
}
