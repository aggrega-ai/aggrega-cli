// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package registryops

import (
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"oras.land/oras-go/v2/registry/remote/errcode"
)

// releasedLoopbackPort returns a loopback address nothing listens on: a port
// that was bound and released, so a registry configured there is refused by this
// machine and nothing leaves it.
func releasedLoopbackPort(t *testing.T) string {
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

// answeringRegistry stands a loopback registry whose every answer handle writes,
// and returns its address. A loopback host is spoken to in plain HTTP by both the
// index walk and the trust fetch, so this is the endpoint the production clients
// reach, with no certificate to arrange.
func answeringRegistry(t *testing.T, handle http.HandlerFunc) string {
	t.Helper()
	srv := httptest.NewServer(handle)
	t.Cleanup(srv.Close)
	return strings.TrimPrefix(srv.URL, "http://")
}

// answerJSON writes status and body as a JSON answer.
func answerJSON(w http.ResponseWriter, status int, body string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = io.WriteString(w, body)
}

// TestTheIndexWalkSaysWhichRegistryCouldNotBeReached drives the index build over
// the production registry client against loopback registries, one fault per row,
// down both paths that select hosts: every configured registry, and the one a
// caller names.
//
// The control answers its listing and is indexed. Two rows are registries that
// answered and failed — a listing that repeats itself, a listing refused with a
// server error — and each is asserted against its own fault, so a row cannot pass
// on another row's failure; neither may be marked unreachable. The last row is a
// registry nothing listens for, which must be. On the named path the failing
// host's row must still be on the result beside the returned error, because that
// row is what the daemon reads to tell a client the named registry was not
// reached.
//
// Planted defects, each caught here: the walk no longer reading the failure into
// the row leaves the last row unmarked on both paths; the single-registry path
// returning before the row is appended leaves the named path with no row to read;
// a reading that marks every host-level failure turns both answered-failure rows
// red.
func TestTheIndexWalkSaysWhichRegistryCouldNotBeReached(t *testing.T) {
	t.Parallel()

	for _, row := range []struct {
		name            string
		host            func(t *testing.T) string
		wantErr         bool
		wantUnreachable bool
		// fault pins the answered-failure rows to their own failure on the named
		// path, where the walk's error is returned whole.
		fault func(err error) bool
	}{
		{
			name: "control: a registry that answers its listing is indexed and was reached",
			host: func(t *testing.T) string {
				return answeringRegistry(t, func(w http.ResponseWriter, _ *http.Request) {
					answerJSON(w, http.StatusOK, `{"repositories": []}`)
				})
			},
		},
		{
			name: "a registry whose listing repeats itself failed after it answered",
			host: func(t *testing.T) string {
				return answeringRegistry(t, func(w http.ResponseWriter, _ *http.Request) {
					w.Header().Set("Link", `</v2/_catalog?last=a&n=100>; rel="next"`)
					answerJSON(w, http.StatusOK, `{"repositories": ["a"]}`)
				})
			},
			wantErr: true,
			fault:   func(err error) bool { return errors.Is(err, errRepositoryListingRepeated) },
		},
		{
			name: "a registry that refuses its listing with a server error failed after it answered",
			host: func(t *testing.T) string {
				return answeringRegistry(t, func(w http.ResponseWriter, _ *http.Request) {
					answerJSON(w, http.StatusInternalServerError, `{"errors": [{"code": "UNKNOWN", "message": "internal"}]}`)
				})
			},
			wantErr: true,
			fault: func(err error) bool {
				var resp *errcode.ErrorResponse
				return errors.As(err, &resp) && resp.StatusCode == http.StatusInternalServerError
			},
		},
		{
			name:            "a registry nothing listens for could not be reached",
			host:            releasedLoopbackPort,
			wantErr:         true,
			wantUnreachable: true,
		},
	} {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			host := row.host(t)

			t.Run("every configured registry", func(t *testing.T) {
				rw := indexProbeWorkspace(t, host)
				res, _ := Update(t.Context(), rw, UpdateOptions{}, io.Discard)
				if len(res.Hosts) != 1 {
					t.Fatalf("one registry is configured; the walk reported %d row(s): %+v", len(res.Hosts), res.Hosts)
				}
				got := res.Hosts[0]
				if (got.Err != "") != row.wantErr {
					t.Errorf("error = %q; want an error: %v", got.Err, row.wantErr)
				}
				if got.Unreachable != row.wantUnreachable {
					t.Errorf("unreachable = %v; want %v (row %+v)", got.Unreachable, row.wantUnreachable, got)
				}
			})

			t.Run("the registry a caller names", func(t *testing.T) {
				rw := indexProbeWorkspace(t, host)
				res, err := Update(t.Context(), rw, UpdateOptions{Registry: host}, io.Discard)
				if (err != nil) != row.wantErr {
					t.Fatalf("Update naming %s returned %v; want an error: %v", host, err, row.wantErr)
				}
				if row.fault != nil && !row.fault(err) {
					t.Errorf("the walk's error is not this row's own fault: %v", err)
				}
				if len(res.Hosts) != 1 {
					t.Fatalf("the named registry's row must be on the result beside the error; got %d row(s): %+v",
						len(res.Hosts), res.Hosts)
				}
				if got := res.Hosts[0].Unreachable; got != row.wantUnreachable {
					t.Errorf("unreachable = %v; want %v (row %+v)", got, row.wantUnreachable, res.Hosts[0])
				}
			})
		})
	}
}

// TestRegistrationSaysWhetherTheRegistryCouldBeReached drives a registration's
// advertised-trust fetch against loopback registries, one fault per row.
//
// The control advertises a policy and has it pinned. Two rows are registries that
// answered with no policy — a not-found, a server error — and are reported as not
// advertising one, reached. The last is a registry nothing listens for: it is
// reported as not advertising a policy too, because the registration proceeds the
// same way, and as unreachable beside that, which is the one thing that tells it
// apart from the two answered rows without reading the reason's words.
//
// Planted defects, each caught here: the outcome no longer reading the fetch's
// failure leaves the last row unmarked; a reading that marks every tolerated
// failure turns both answered rows red; dropping not-advertised for an unreachable
// registry — a change to what an existing field reports — turns the last row red.
func TestRegistrationSaysWhetherTheRegistryCouldBeReached(t *testing.T) {
	t.Parallel()

	for _, row := range []struct {
		name              string
		host              func(t *testing.T) string
		wantPinned        bool
		wantNotAdvertised bool
		wantUnreachable   bool
		// reason pins the answered rows to their own answer, which names the host
		// the row registered.
		reason func(host, reason string) bool
	}{
		{
			name: "control: a registry advertising a policy has it pinned",
			host: func(t *testing.T) string {
				return answeringRegistry(t, func(w http.ResponseWriter, _ *http.Request) {
					answerJSON(w, http.StatusOK, probeAdvertisedTrust)
				})
			},
			wantPinned: true,
		},
		{
			name: "a registry answering not-found advertised no policy and was reached",
			host: func(t *testing.T) string {
				return answeringRegistry(t, func(w http.ResponseWriter, r *http.Request) { http.NotFound(w, r) })
			},
			wantNotAdvertised: true,
			reason: func(host, reason string) bool {
				return reason == host+" publishes no trust policy (HTTP 404)"
			},
		},
		{
			name: "a registry answering a server error advertised no policy and was reached",
			host: func(t *testing.T) string {
				return answeringRegistry(t, func(w http.ResponseWriter, _ *http.Request) {
					answerJSON(w, http.StatusInternalServerError, `{}`)
				})
			},
			wantNotAdvertised: true,
			reason: func(host, reason string) bool {
				return reason == host+" answered the request for its trust policy with HTTP 500"
			},
		},
		{
			name:              "a registry nothing listens for advertised no policy because it could not be reached",
			host:              releasedLoopbackPort,
			wantNotAdvertised: true,
			wantUnreachable:   true,
		},
	} {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			host := row.host(t)
			rw, _ := registrationProbeWorkspace(t, "workspace: {}\n")

			res, err := Add(t.Context(), rw, AddOptions{
				Host:        host,
				FetchTrust:  true,
				Workspace:   true,
				TrustClient: &http.Client{},
			})
			if err != nil {
				t.Fatalf("registering %s: %v", host, err)
			}
			got := res.Trust
			if got.Pinned != row.wantPinned {
				t.Errorf("pinned = %v; want %v (trust %+v)", got.Pinned, row.wantPinned, got)
			}
			if got.NotAdvertised != row.wantNotAdvertised {
				t.Errorf("notAdvertised = %v; want %v (trust %+v)", got.NotAdvertised, row.wantNotAdvertised, got)
			}
			if got.Unreachable != row.wantUnreachable {
				t.Errorf("unreachable = %v; want %v (trust %+v)", got.Unreachable, row.wantUnreachable, got)
			}
			if row.reason != nil && !row.reason(host, got.Reason) {
				t.Errorf("reason = %q is not this row's own answer", got.Reason)
			}
		})
	}
}
