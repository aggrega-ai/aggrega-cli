// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	jose "github.com/go-jose/go-jose/v4"
)

// An authorization server is reached over TLS, except on the machine this
// process runs on. The proofs here drive each place that rule is applied: the
// startup refusal of a plaintext issuer, made in every mode before anything
// binds; the refusal of a plaintext key address an authorization server's
// metadata names; and the refusal of a redirect that would hand either read to a
// plaintext address. Every refused row has an admitted row beside it, because a
// refusal that also refused loopback, or https, would pass every refused row.

// refusedIssuers are plain http addresses on another machine, which a listener
// is refused for in every mode.
var refusedIssuers = []string{
	"http://auth.example.test/realms/probe",
	"HTTP://auth.example.test/realms/probe",
	"http://192.0.2.10:8080/realms/probe",
	"http://[2001:db8::1]:8080/realms/probe",
}

// admittedIssuers are the controls: plain http on this machine by the one name
// and by each loopback literal, and https on another machine.
var admittedIssuers = []string{
	"http://localhost:8080/realms/probe",
	"http://127.0.0.1:8080/realms/probe",
	"http://[::1]:8080/realms/probe",
	"https://auth.example.test/realms/probe",
}

// listenerMode is one of the ways a listener reads its keys and its callers.
// The startup refusal is asserted in each, because each publishes its issuer.
type listenerMode struct {
	name string
	// transport is the listener's configuration in this mode, declaring issuer.
	transport func(t *testing.T, issuer string) HTTPConfig
}

// everyListenerMode is the three modes. The two that read nothing from the
// issuer are handed a gate that refuses every outbound request, so what reaches
// them from the network is nothing, and what refuses them is the issuer alone.
func everyListenerMode() []listenerMode {
	offline := refusingEgress(errors.New("network access refused because the workspace is offline"))
	return []listenerMode{
		{
			name: "keys read from the issuer",
			transport: func(t *testing.T, issuer string) HTTPConfig {
				return transportFor(AuthorizationConfig{Issuer: issuer, Resource: testResource},
					func() (*http.Client, error) { return http.DefaultClient, nil })
			},
		},
		{
			name: "keys read from a file",
			transport: func(t *testing.T, issuer string) HTTPConfig {
				return transportFor(AuthorizationConfig{
					Issuer:   issuer,
					Resource: testResource,
					JWKSFile: writeKeySetFile(t, jose.JSONWebKeySet{
						Keys: []jose.JSONWebKey{publicJWK(newSigningKey(t), "a-published-key")},
					}),
				}, offline)
			},
		},
		{
			name: "callers asserted by a proxy in front",
			transport: func(t *testing.T, issuer string) HTTPConfig {
				return transportFor(AuthorizationConfig{
					Issuer:       issuer,
					Resource:     testResource,
					TrustedProxy: testTrustedProxy(),
				}, offline)
			},
		},
	}
}

// decodeLifecycleLines reads every lifecycle line the serving process wrote.
// Anything on the stream that is not one is passed over: what a row asserts is
// which events happened, and each is looked for by name.
func decodeLifecycleLines(written string) []lifecycleAudit {
	var lines []lifecycleAudit
	for _, raw := range strings.Split(written, "\n") {
		var line lifecycleAudit
		if json.Unmarshal([]byte(raw), &line) != nil || line.Event == "" {
			continue
		}
		lines = append(lines, line)
	}
	return lines
}

// linesFor is the lifecycle lines naming event.
func linesFor(lines []lifecycleAudit, event string) []lifecycleAudit {
	var out []lifecycleAudit
	for _, line := range lines {
		if line.Event == event {
			out = append(out, line)
		}
	}
	return out
}

// A plain http issuer on another machine is refused before anything binds, in
// every mode, and the refusal is the one a supervisor reads on the stream.
//
// Each row is a launch through the entry point the command layer calls, on a
// Unix socket, so "nothing bound" is read as the socket file being absent rather
// than inferred from an error having been returned: a refusal returned after the
// bind would satisfy an error-only assertion while the address was accepting.
// The stream is read for the startup-refused line carrying that same refusal,
// and for the absence of the line a listener writes once it is accepting.
//
// The admitted rows are the control that the refusal does not over-reach: plain
// http on this machine, by the one name and by each loopback literal, and https
// on another machine, each binding in every mode.
//
// Planted defect, demonstrated: remove the refusal from resolveProtectedResource
// and every refused row reads a listener that bound.
func TestAPlaintextIssuerOffThisMachineIsRefusedBeforeAnythingBindsInEveryMode(t *testing.T) {
	for _, mode := range everyListenerMode() {
		for _, issuer := range refusedIssuers {
			t.Run("refused: "+mode.name+": "+issuer, func(t *testing.T) {
				address, path := socketAddress(t)
				log := &lockedBuffer{}
				cfg := Config{WorkspaceRoot: rootedWorkspace(t), Stderr: log}
				transport := mode.transport(t, issuer)

				ctx, endRun := context.WithCancel(context.Background())
				t.Cleanup(endRun)
				launched := make(chan error, 1)
				go func() {
					launched <- ListenAndServe(ctx, cfg, transport, ListenConfig{Address: address})
				}()

				var err error
				select {
				case err = <-launched:
				case <-time.After(runReturnCeiling):
					t.Fatal("the launch neither refused nor returned within the liveness ceiling, so it is " +
						"serving although its authorization server is reached in plaintext off this machine")
				}
				if err == nil {
					t.Fatal("the listener served although its authorization server is reached in plaintext " +
						"off this machine")
				}
				for _, fragment := range []string{"mcp.authorization.issuer", "https"} {
					if !strings.Contains(err.Error(), fragment) {
						t.Errorf("the refusal does not name %q, so an operator is not told what to change; it "+
							"reads %q", fragment, err)
					}
				}
				if _, statErr := os.Stat(path); statErr == nil {
					t.Errorf("%s exists, so the address was bound before the refusal", path)
				}

				lines := decodeLifecycleLines(log.String())
				refusedLines := linesFor(lines, eventStartupRefused)
				if len(refusedLines) != 1 {
					t.Fatalf("the stream carries %d startup-refused lines, want 1: %q", len(refusedLines), log.String())
				}
				if refusedLines[0].Detail != err.Error() {
					t.Errorf("the startup-refused line reads %q, want the refusal the launch returned, %q",
						refusedLines[0].Detail, err.Error())
				}
				if listening := linesFor(lines, eventListening); len(listening) != 0 {
					t.Errorf("the stream says the listener was accepting: %+v", listening)
				}
			})
		}
		for _, issuer := range admittedIssuers {
			t.Run("admitted: "+mode.name+": "+issuer, func(t *testing.T) {
				address, path := socketAddress(t)
				log := &lockedBuffer{}
				cfg := Config{WorkspaceRoot: rootedWorkspace(t), Stderr: log}
				transport := mode.transport(t, issuer)

				ctx, endRun := context.WithCancel(context.Background())
				t.Cleanup(endRun)
				served := make(chan error, 1)
				go func() {
					served <- ListenAndServe(ctx, cfg, transport, ListenConfig{Address: address})
				}()
				awaitSocket(t, path, served)
				endRun()
				select {
				case err := <-served:
					if err != nil {
						t.Errorf("serving returned %v, want a clean end", err)
					}
				case <-time.After(runReturnCeiling):
					t.Fatal("the listener never stopped serving within the liveness ceiling")
				}

				lines := decodeLifecycleLines(log.String())
				if refused := linesFor(lines, eventStartupRefused); len(refused) != 0 {
					t.Errorf("an admitted issuer wrote a startup-refused line: %+v", refused)
				}
				if listening := linesFor(lines, eventListening); len(listening) != 1 {
					t.Errorf("the stream carries %d listening lines, want 1: %q", len(listening), log.String())
				}
			})
		}
	}
}

// A handler built without coming through the listener refuses the same issuers,
// because the refusal sits in the resolver both of them call rather than in the
// listener alone. The admitted issuers build a handler, as the control.
func TestAHandlerBuiltWithoutTheListenerRefusesAPlaintextIssuerOffThisMachine(t *testing.T) {
	for _, mode := range everyListenerMode() {
		for _, issuer := range refusedIssuers {
			t.Run("refused: "+mode.name+": "+issuer, func(t *testing.T) {
				_, err := newHTTPHandler(context.Background(), newCommandToolServer(t, nil),
					mode.transport(t, issuer))
				if err == nil {
					t.Fatal("a handler was built for an authorization server reached in plaintext off this machine")
				}
				if !strings.Contains(err.Error(), "mcp.authorization.issuer") {
					t.Errorf("the refusal does not name mcp.authorization.issuer; it reads %q", err)
				}
			})
		}
		for _, issuer := range admittedIssuers {
			t.Run("admitted: "+mode.name+": "+issuer, func(t *testing.T) {
				if _, err := newHTTPHandler(context.Background(), newCommandToolServer(t, nil),
					mode.transport(t, issuer)); err != nil {
					t.Fatalf("an admitted issuer was refused: %v", err)
				}
			})
		}
	}
}

// The one test every refusal here makes reads the scheme and the host and
// nothing else, and reads the host the way the listener's loopback test does:
// with the port and any IPv6 brackets stripped first.
func TestAnAddressIsPlaintextOffThisMachineOnlyWhereItsSchemeAndItsHostBothSaySo(t *testing.T) {
	for _, tc := range []struct {
		address string
		want    bool
	}{
		{"http://auth.example.test/realms/probe", true},
		{"HTTP://auth.example.test/realms/probe", true},
		{"http://192.0.2.10:8080/realms/probe", true},
		{"http://[2001:db8::1]:8080/realms/probe", true},
		{"http://0.0.0.0:8080/realms/probe", true},
		{"http://localhost:8080/realms/probe", false},
		{"http://127.0.0.1:8080/realms/probe", false},
		{"http://127.0.0.53/realms/probe", false},
		{"http://[::1]:8080/realms/probe", false},
		{"https://auth.example.test/realms/probe", false},
		{"https://192.0.2.10/realms/probe", false},
	} {
		t.Run(tc.address, func(t *testing.T) {
			parsed, err := url.Parse(tc.address)
			if err != nil {
				t.Fatalf("parse %q: %v", tc.address, err)
			}
			if got := plaintextOffLoopback(parsed); got != tc.want {
				t.Errorf("plaintextOffLoopback(%q) = %v, want %v", tc.address, got, tc.want)
			}
		})
	}
}

// recordingEgress is a gate whose client records every request it is asked to
// make, and dials nothing off this machine.
//
// The record is what the refusals are read off: a refused address is one no
// request was ever made to, and only something standing where the request would
// leave can say that. Dialling nothing beyond loopback keeps a proof whose
// refusal has been planted away from reaching the network it names — the
// attempt is recorded and answered with an error instead.
type recordingEgress struct {
	// client is the one the gate hands out, every time, so a proof can read
	// afterwards whether anything changed it.
	client *http.Client

	mu    sync.Mutex
	asked []*url.URL
}

// newRecordingEgress records every request made through inner's transport.
func newRecordingEgress(inner *http.Client) *recordingEgress {
	rec := &recordingEgress{}
	rec.client = &http.Client{Transport: &recordingTransport{rec: rec, inner: inner.Transport}}
	return rec
}

func (r *recordingEgress) gate() NetworkEgress {
	return func() (*http.Client, error) { return r.client, nil }
}

// requestsTo is how many requests were asked of host.
func (r *recordingEgress) requestsTo(host string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := 0
	for _, asked := range r.asked {
		if asked.Host == host {
			n++
		}
	}
	return n
}

// recordingTransport is the transport behind a recording gate's client.
type recordingTransport struct {
	rec   *recordingEgress
	inner http.RoundTripper
}

func (t *recordingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	t.rec.mu.Lock()
	t.rec.asked = append(t.rec.asked, req.URL)
	t.rec.mu.Unlock()
	if ip := net.ParseIP(req.URL.Hostname()); ip == nil || !ip.IsLoopback() {
		return nil, fmt.Errorf("this proof dials nothing off this machine, and %s was asked for", req.URL)
	}
	if t.inner == nil {
		return http.DefaultTransport.RoundTrip(req)
	}
	return t.inner.RoundTrip(req)
}

// hostOf is the host and port an address names, as a recorded request carries
// it.
func hostOf(t *testing.T, address string) string {
	t.Helper()
	parsed, err := url.Parse(address)
	if err != nil {
		t.Fatalf("parse %q: %v", address, err)
	}
	return parsed.Host
}

// wantKeysRefusedOverPlaintext asserts one token answered as keys that could not
// be read — 503, with no challenge, since the caller's token was not found
// wanting — and one line on the stream for it, naming this refusal rather than
// an unreachable authorization server.
func wantKeysRefusedOverPlaintext(t *testing.T, got answer, written, token string) {
	t.Helper()
	if got.status != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503 (body %q)", got.status, got.raw)
	}
	if challenge := got.header.Get(headerAuthenticate); challenge != "" {
		t.Errorf("the answer carries the challenge %q, so a caller holding a good token is sent to obtain "+
			"another one", challenge)
	}
	wantTokenAbsent(t, got, token)

	lines := strings.Split(strings.TrimSuffix(written, "\n"), "\n")
	if len(lines) != 1 || lines[0] == "" {
		t.Fatalf("the request wrote %d lines, want exactly 1: %q", len(lines), written)
	}
	var line requestAudit
	if err := json.Unmarshal([]byte(lines[0]), &line); err != nil {
		t.Fatalf("the line is not a request record: %v (%q)", err, lines[0])
	}
	if line.Refusal != string(reasonKeysOverPlaintext) {
		t.Errorf("the line names the refusal %q, want %q", line.Refusal, reasonKeysOverPlaintext)
	}
}

// A key address an authorization server's metadata names that is plain http on
// another machine is never asked, and the caller is answered as for keys that
// could not be read.
//
// Three things are read per row, and each is the others' control. The answer is
// 503 with no challenge. The one line for the request names this refusal. And
// the recording gate shows no request made to the host the metadata named, while
// it shows the metadata itself was read — so the refusal was made where the
// address was learned, not before anything was asked. A listener that asked
// and then failed to reach the address would give the same answer and a
// different line, and the recording would show the request.
//
// The last row is the re-read a token naming an unknown key makes: the read
// that establishes the set goes to the double's own loopback address and is
// admitted, which is the control that plaintext on this machine stays admitted,
// and only the re-read meets the moved address.
func TestAKeyAddressInPlaintextOffThisMachineIsNeverAsked(t *testing.T) {
	for _, keysAt := range []string{
		"http://keys.example.test/realms/probe/certs",
		"http://192.0.2.10/realms/probe/certs",
	} {
		t.Run("the first read: "+keysAt, func(t *testing.T) {
			issuer := newAuthorizationServerDouble(t)
			issuer.publishKeysAt(keysAt)
			rec := newRecordingEgress(issuer.ts.Client())
			f := newTokenFixture(t, transportFor(issuer.authorizationConfig(), rec.gate()))

			from := f.log.mark()
			token := issuer.mint(t, tokenClaimSet{scope: scopeRead})
			wantKeysRefusedOverPlaintext(t, f.call(t, bearer(token)), f.log.since(from), token)
			if asked := rec.requestsTo(hostOf(t, keysAt)); asked != 0 {
				t.Errorf("%d requests were made to %s, want 0", asked, hostOf(t, keysAt))
			}
			if reads := issuer.metadataReads.Load(); reads != 1 {
				t.Errorf("the metadata was read %d times, want 1: the refusal is made once the address is "+
					"learned, not before anything is asked", reads)
			}
			if reads := issuer.keySetReads.Load(); reads != 0 {
				t.Errorf("the double's own key set was read %d times, want 0", reads)
			}
		})
	}

	t.Run("the re-read a token naming an unknown key makes", func(t *testing.T) {
		issuer := newAuthorizationServerDouble(t)
		rec := newRecordingEgress(issuer.ts.Client())
		f := newTokenFixture(t, transportFor(issuer.authorizationConfig(), rec.gate()))

		wantReachedTheToolSurface(t, f.call(t, bearer(issuer.mint(t, tokenClaimSet{scope: scopeRead}))))

		moved := "http://keys.example.test/realms/probe/certs"
		issuer.publishKeysAt(moved)
		from := f.log.mark()
		unknown := issuer.mintSignedBy(t, tokenClaimSet{scope: scopeRead}, newSigningKey(t), "a-key-published-elsewhere")
		wantKeysRefusedOverPlaintext(t, f.call(t, bearer(unknown)), f.log.since(from), unknown)
		if asked := rec.requestsTo(hostOf(t, moved)); asked != 0 {
			t.Errorf("%d requests were made to %s, want 0", asked, hostOf(t, moved))
		}
		if reads := issuer.metadataReads.Load(); reads != 2 {
			t.Errorf("the metadata was read %d times, want 2: once to establish the set and once for the "+
				"re-read", reads)
		}
	})

	// The caller is told exactly what it is told when the keys are out of reach,
	// and only the operator's line tells the two apart. The unreachable case is a
	// listener whose double points its key read at a path it does not serve.
	t.Run("the caller cannot tell this refusal from keys out of reach, and the operator can", func(t *testing.T) {
		refusing := newAuthorizationServerDouble(t)
		refusing.publishKeysAt("http://keys.example.test/realms/probe/certs")
		refusingFixture := newTokenFixture(t, transportFor(refusing.authorizationConfig(),
			newRecordingEgress(refusing.ts.Client()).gate()))

		unreachable := newAuthorizationServerDouble(t)
		unreachable.publishKeysAt(unreachable.ts.URL + "/nothing-is-published-here")
		unreachableFixture := newTokenFixture(t, transportFor(unreachable.authorizationConfig(),
			newRecordingEgress(unreachable.ts.Client()).gate()))

		token := refusing.mint(t, tokenClaimSet{scope: scopeRead})
		refused := refusingFixture.call(t, bearer(token))
		wantKeysRefusedOverPlaintext(t, refused, refusingFixture.log.String(), token)

		out := unreachableFixture.call(t, bearer(unreachable.mint(t, tokenClaimSet{scope: scopeRead})))
		wantSameAnswerAs(t, refused, out)
		wantOperatorToldWhichCheck(t, unreachableFixture.log.String(), reasonKeysOutOfReach)
		if strings.Contains(unreachableFixture.log.String(), string(reasonKeysOverPlaintext)) {
			t.Errorf("an unreachable authorization server was recorded as one refused for plaintext: %q",
				unreachableFixture.log.String())
		}
	})
}

// redirectRow is one way a TLS authorization server on this machine answers the
// two reads a listener makes of it. Each of metadata and keys returns where
// that read is redirected, or "" to answer it directly.
type redirectRow struct {
	name     string
	metadata func(e redirectEnv) string
	keys     func(e redirectEnv) string
	// refusedHost is the host the refused hop names; empty for an admitted row.
	refusedHost string
	// followedTo is the base address an admitted row's redirect reaches, read
	// back off the recording so an admitted row cannot pass without the redirect
	// having been followed.
	followedTo func(e redirectEnv) string
}

// redirectEnv is the base addresses a row's redirects can name: the TLS
// authorization server itself, a second TLS address, and a plaintext address on
// this machine.
type redirectEnv struct {
	front, elsewhere, plain string
}

// movedKeysPath is where the TLS authorization server's metadata says its keys
// are, and it is the path a row redirects the key read from.
const movedKeysPath = "/keys/moved"

// A redirect that would hand a metadata or key read to plain http on another
// machine is never followed, and the hop it names is never asked.
//
// The authorization server is served over TLS on this machine and trusted by the
// listener's client, so the only plaintext in any row is the hop the row's
// redirect names. The refused rows are answered exactly as a plaintext key
// address is — 503, the same line — and the recording shows no request to the
// redirect's target. The admitted rows are the controls:
// a redirect to another https address, and one to plain http on this machine,
// are each followed, the token is admitted, and the recording shows the
// redirect's target was reached.
//
// The client the gate hands out is read afterwards for a redirect policy of its
// own, and must carry none: the policy belongs to the key reads, and a gate
// sharing its client with other requests must not find their policy changed.
//
// Planted defect, demonstrated: remove the redirect policy from keyReadClient and
// every refused row reads a request made to the redirect's target.
func TestARedirectToPlaintextOffThisMachineIsNeverFollowed(t *testing.T) {
	for _, row := range []redirectRow{
		{
			name:        "refused: the metadata read, redirected to plain http on another machine",
			metadata:    func(redirectEnv) string { return "http://auth.example.test" + wellKnownAuthorizationServer },
			keys:        func(redirectEnv) string { return "" },
			refusedHost: "auth.example.test",
		},
		{
			name:        "refused: the key read, redirected to plain http on another machine",
			metadata:    func(redirectEnv) string { return "" },
			keys:        func(redirectEnv) string { return "http://keys.example.test" + keySetPath },
			refusedHost: "keys.example.test",
		},
		{
			name:        "refused: the key read, redirected to a plain http address literal on another machine",
			metadata:    func(redirectEnv) string { return "" },
			keys:        func(redirectEnv) string { return "http://192.0.2.10" + keySetPath },
			refusedHost: "192.0.2.10",
		},
		{
			name:       "admitted: the metadata read, redirected to another https address",
			metadata:   func(e redirectEnv) string { return e.elsewhere + wellKnownAuthorizationServer },
			keys:       func(redirectEnv) string { return "" },
			followedTo: func(e redirectEnv) string { return e.elsewhere },
		},
		{
			name:       "admitted: the key read, redirected to another https address",
			metadata:   func(redirectEnv) string { return "" },
			keys:       func(e redirectEnv) string { return e.elsewhere + keySetPath },
			followedTo: func(e redirectEnv) string { return e.elsewhere },
		},
		{
			name:       "admitted: the metadata read, redirected to plain http on this machine",
			metadata:   func(e redirectEnv) string { return e.plain + wellKnownAuthorizationServer },
			keys:       func(redirectEnv) string { return "" },
			followedTo: func(e redirectEnv) string { return e.plain },
		},
		{
			name:       "admitted: the key read, redirected to plain http on this machine",
			metadata:   func(redirectEnv) string { return "" },
			keys:       func(e redirectEnv) string { return e.plain + keySetPath },
			followedTo: func(e redirectEnv) string { return e.plain },
		},
	} {
		t.Run(row.name, func(t *testing.T) {
			// The double is used for its signing key and the key set it publishes,
			// which every address in this row serves; its own server is not asked.
			keys := newAuthorizationServerDouble(t)
			var env redirectEnv

			// Every address other than the authorization server itself serves the
			// metadata that server would state, naming its own key set.
			servesTheDocuments := func() *http.ServeMux {
				mux := http.NewServeMux()
				mux.HandleFunc(wellKnownAuthorizationServer, func(w http.ResponseWriter, r *http.Request) {
					self := "https://" + r.Host
					if r.TLS == nil {
						self = "http://" + r.Host
					}
					writeJSON(w, map[string]any{"issuer": env.front, "jwks_uri": self + keySetPath})
				})
				mux.HandleFunc(keySetPath, func(w http.ResponseWriter, _ *http.Request) {
					writeJSON(w, keys.published)
				})
				return mux
			}
			elsewhere := httptest.NewTLSServer(servesTheDocuments())
			t.Cleanup(elsewhere.Close)
			plain := httptest.NewServer(servesTheDocuments())
			t.Cleanup(plain.Close)

			frontMux := http.NewServeMux()
			frontMux.HandleFunc(wellKnownAuthorizationServer, func(w http.ResponseWriter, r *http.Request) {
				if to := row.metadata(env); to != "" {
					http.Redirect(w, r, to, http.StatusFound)
					return
				}
				writeJSON(w, map[string]any{"issuer": env.front, "jwks_uri": env.front + movedKeysPath})
			})
			frontMux.HandleFunc(movedKeysPath, func(w http.ResponseWriter, r *http.Request) {
				if to := row.keys(env); to != "" {
					http.Redirect(w, r, to, http.StatusFound)
					return
				}
				writeJSON(w, keys.published)
			})
			front := httptest.NewTLSServer(frontMux)
			t.Cleanup(front.Close)
			env = redirectEnv{front: front.URL, elsewhere: elsewhere.URL, plain: plain.URL}

			// Every TLS address in a row presents the same test certificate, so the
			// front server's client trusts the second one too.
			rec := newRecordingEgress(front.Client())
			f := newTokenFixture(t, transportFor(AuthorizationConfig{Issuer: front.URL, Resource: testResource},
				rec.gate()))

			from := f.log.mark()
			token := keys.mint(t, tokenClaimSet{issuer: front.URL, scope: scopeRead})
			got := f.call(t, bearer(token))

			if row.refusedHost != "" {
				wantKeysRefusedOverPlaintext(t, got, f.log.since(from), token)
				if asked := rec.requestsTo(row.refusedHost); asked != 0 {
					t.Errorf("%d requests were made to %s, the redirect's target, want 0", asked, row.refusedHost)
				}
			} else {
				wantReachedTheToolSurface(t, got)
				if asked := rec.requestsTo(hostOf(t, row.followedTo(env))); asked == 0 {
					t.Errorf("nothing was asked of %s, so the redirect this row is about was never followed",
						row.followedTo(env))
				}
			}
			if rec.client.CheckRedirect != nil {
				t.Error("the client the gate handed out now carries a redirect policy, so every other request " +
					"made through it follows redirects differently")
			}
		})
	}
}
