// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package mcp

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

// admissionRefusalText is the phrase the listener's refusal of an unconfigured
// registry carries, held once so every row asserting its presence or absence
// reads the same words.
const admissionRefusalText = "is not a registry this workspace is configured with"

// describePublishedFailureText returns the text of the tool failure describe
// answered with, failing the test when describe answered anything else.
func describePublishedFailureText(t *testing.T, res *callToolResult, rpcErr *rpcError) string {
	t.Helper()
	if rpcErr != nil {
		t.Fatalf("describe answered with a protocol error rather than a tool result: %+v", rpcErr)
	}
	if res == nil || !res.IsError || len(res.Content) == 0 {
		t.Fatalf("describe did not answer with a tool failure: %+v", res)
	}
	return res.Content[0].Text
}

// pinDescribeEnvironment empties the two environment settings these tests'
// workspaces depend on, for the test's duration. The environment is a layer
// above every configuration file, so a value exported on the machine running
// the suite would otherwise decide which registry a fixture's default is, or
// turn its offline posture off — and an offline workspace is what keeps every
// row here from reaching a network. An empty value is a fall-through, so the
// fixture files decide.
//
// It makes the calling test serial: the environment is the whole process's.
func pinDescribeEnvironment(t *testing.T) {
	t.Helper()
	t.Setenv("AGGREGA_DEFAULT_REGISTRY", "")
	t.Setenv("AGGREGA_OFFLINE", "")
}

// writeDescribeWorkspace writes a workspace whose configuration is config, and
// returns its root.
func writeDescribeWorkspace(t *testing.T, config string) string {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, ".aggrega-config.yaml"), []byte(config), 0o644); err != nil {
		t.Fatalf("write workspace config: %v", err)
	}
	return root
}

// TestDescribeOverAListenerReadsOnlyAConfiguredRegistry pins which registry
// references the describe tool reads for a caller reaching a listener: a
// reference to a registry the served workspace is configured with goes on to the
// read, and one to any other host is refused, naming that host, before anything
// is contacted.
//
// No row can reach a network. Every workspace is offline, so a reference the
// listener admits is refused at the egress gate the reading passes through — and
// that refusal, reachable only through the reading, is the evidence the reference
// was admitted. A reference the listener refuses must carry the admission refusal
// and must NOT carry the egress one: a refusal placed after the read, or a
// predicate that admitted everything, would show the egress refusal instead.
//
// The workspace is the one the server was started over, named by the root the
// server was launched with rather than by the call, which is how a listener serves it —
// a call on a listener cannot name another.
func TestDescribeOverAListenerReadsOnlyAConfiguredRegistry(t *testing.T) {
	pinDescribeEnvironment(t)
	const (
		configured = "workspace: {}\noffline: true\n" +
			"default_registry: registry.example.com\n" +
			"registries:\n" +
			"  - host: listed.example.com\n" +
			"  - host: \"127.0.0.1:5000\"\n"
		// A workspace naming no registry at all resolves to the documented default
		// — the hub as default registry and an empty list — which is what a machine
		// that was never initialised serves.
		neverInitialised = "workspace: {}\noffline: true\n"
	)
	cases := []struct {
		name      string
		config    string
		reference string
		admitted  bool
		host      string
	}{
		{"the default registry's host is admitted", configured, "oci://registry.example.com/component/core/api:1.0.0", true, ""},
		{"a host listed only under registries is admitted", configured, "oci://listed.example.com/component/core/api:1.0.0", true, ""},
		{"a listed loopback host with its port is admitted", configured, "oci://127.0.0.1:5000/component/core/api:1.0.0", true, ""},
		{"an unlisted host is refused", configured, "oci://unlisted.example.com/component/core/api:1.0.0", false, "unlisted.example.com"},
		{"the listed loopback host on another port is refused", configured, "oci://127.0.0.1:5001/component/core/api:1.0.0", false, "127.0.0.1:5001"},
		{"the hub is refused where it is configured nowhere", configured, "oci://hub.aggrega.ai/component/core/api:1.0.0", false, "hub.aggrega.ai"},
		{"a never-initialised workspace admits the hub", neverInitialised, "oci://hub.aggrega.ai/component/core/api:1.0.0", true, ""},
		{"a never-initialised workspace refuses any other host", neverInitialised, "oci://registry.example.com/component/core/api:1.0.0", false, "registry.example.com"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := &server{transport: transportHTTP, launchRoot: writeDescribeWorkspace(t, tc.config)}
			args, _ := json.Marshal(map[string]any{"name": tc.reference})
			res, rpcErr := describeInvoke(s, context.Background(), args)
			text := describePublishedFailureText(t, res, rpcErr)
			reachedTheRead := strings.Contains(text, "E0125") && strings.Contains(text, "offline-egress")
			refusedByAdmission := strings.Contains(text, admissionRefusalText)
			if tc.admitted {
				if !reachedTheRead || refusedByAdmission {
					t.Errorf("%s was not admitted to the read: %q", tc.reference, text)
				}
				return
			}
			if !refusedByAdmission || reachedTheRead {
				t.Errorf("%s was not refused before the read: %q", tc.reference, text)
			}
			if !strings.Contains(text, tc.host+" "+admissionRefusalText) {
				t.Errorf("the refusal does not name the host %s: %q", tc.host, text)
			}
			if !strings.Contains(text, "aggrega registry add "+tc.host+" --workspace") ||
				!strings.Contains(text, "aggrega inspect "+tc.reference) {
				t.Errorf("the refusal does not name what to do instead: %q", text)
			}
		})
	}
}

// TestDescribeOverAListenerContactsNothingItRefuses pins the order the table
// above cannot: that a refused reference is refused before anything is
// contacted, not merely answered with a refusal. In an offline workspace a read
// that ran first and a refusal that came second would read exactly like a
// refusal that came first, so this workspace is online and each reference names
// a listener on this machine that counts the connections it accepts — counted at
// accept, before a byte is read, so a dial of either scheme is seen.
//
// The admitted reference is the positive control: its listener must see a
// connection, which proves the counter fires. The refused one must see none.
// Both listeners are on loopback, which the reading contacts over plain HTTP and
// no proxy setting redirects, so nothing leaves the machine.
func TestDescribeOverAListenerContactsNothingItRefuses(t *testing.T) {
	pinDescribeEnvironment(t)
	counting := func(t *testing.T) (string, *atomic.Int64) {
		t.Helper()
		var accepted atomic.Int64
		srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusNotFound)
		}))
		srv.Config.ConnState = func(_ net.Conn, state http.ConnState) {
			if state == http.StateNew {
				accepted.Add(1)
			}
		}
		srv.Start()
		t.Cleanup(srv.Close)
		return strings.TrimPrefix(srv.URL, "http://"), &accepted
	}
	listedHost, listedAccepted := counting(t)
	unlistedHost, unlistedAccepted := counting(t)

	root := writeDescribeWorkspace(t, "workspace: {}\noffline: false\n"+
		"default_registry: registry.example.com\n"+
		"registries:\n  - host: \""+listedHost+"\"\n")
	s := &server{transport: transportHTTP, launchRoot: root}
	describe := func(reference string) string {
		args, _ := json.Marshal(map[string]any{"name": reference})
		res, rpcErr := describeInvoke(s, context.Background(), args)
		return describePublishedFailureText(t, res, rpcErr)
	}

	admitted := describe("oci://" + listedHost + "/component/core/api:1.0.0")
	if strings.Contains(admitted, admissionRefusalText) || listedAccepted.Load() == 0 {
		t.Fatalf("control: the listed loopback host was not read (%d connection(s) accepted): %q",
			listedAccepted.Load(), admitted)
	}

	refused := describe("oci://" + unlistedHost + "/component/core/api:1.0.0")
	if !strings.Contains(refused, unlistedHost+" "+admissionRefusalText) {
		t.Errorf("the unlisted loopback host was not refused by the admission: %q", refused)
	}
	if n := unlistedAccepted.Load(); n != 0 {
		t.Errorf("the unlisted loopback host accepted %d connection(s); a refused reference must contact nothing", n)
	}
}

// TestDescribeOverAListenerRefusesAReferenceWithNoHostToAdmit pins the listener's
// answer to a reference whose host cannot be read: it is refused with the
// reading's own sentence, naming the form to write, before the read and before
// the admission — there is no host to admit, so nothing is either admitted or
// contacted.
func TestDescribeOverAListenerRefusesAReferenceWithNoHostToAdmit(t *testing.T) {
	pinDescribeEnvironment(t)
	s := &server{transport: transportHTTP, launchRoot: writeDescribeWorkspace(t, "workspace: {}\noffline: true\n")}
	args, _ := json.Marshal(map[string]any{"name": "oci://hub.aggrega.ai/component/core/api"})
	res, rpcErr := describeInvoke(s, context.Background(), args)
	text := describePublishedFailureText(t, res, rpcErr)
	if !strings.Contains(text, "is not a registry reference this can read") {
		t.Errorf("a reference naming no version was not refused for its shape: %q", text)
	}
	if strings.Contains(text, "E0125") || strings.Contains(text, admissionRefusalText) {
		t.Errorf("a reference with no host to admit reached the admission or the read: %q", text)
	}
}

// TestDescribeOnStandardInputAndOutputAsksNoAdmission pins that the admission is
// a listener's alone. A client that launched this server over its own streams
// reads from whatever registry it names, as the command line does, so a
// reference to a host its workspace does not configure goes on to the read —
// which, in this offline workspace, is refused at the egress gate rather than by
// the admission.
func TestDescribeOnStandardInputAndOutputAsksNoAdmission(t *testing.T) {
	pinDescribeEnvironment(t)
	root := writeDescribeWorkspace(t, "workspace: {}\noffline: true\ndefault_registry: registry.example.com\n")
	s := &server{transport: transportStdio}
	args, _ := json.Marshal(map[string]any{"name": "oci://unlisted.example.com/component/core/api:1.0.0", "workspace_root": root})
	res, rpcErr := describeInvoke(s, context.Background(), args)
	text := describePublishedFailureText(t, res, rpcErr)
	if !strings.Contains(text, "E0125") || !strings.Contains(text, "offline-egress") {
		t.Errorf("the read did not reach the egress gate the published-entity reading passes through: %q", text)
	}
	if strings.Contains(text, admissionRefusalText) || strings.Contains(text, "No catalog entity named") {
		t.Errorf("a client on standard input and output was held to the listener's admission, or sent to the catalog: %q", text)
	}
}

// TestDescribeAdvertisesNoCredentialArgument pins that the describe tool takes
// no credential from a call, on either transport: the read spends the login the
// host already holds for the registry, and a secret sent as an argument would
// travel through the conversation the call came from. The properties are read
// from the schema each transport advertises, so a credential argument added to
// the declaration fails here however it is spelled among the ones listed.
func TestDescribeAdvertisesNoCredentialArgument(t *testing.T) {
	t.Parallel()
	for _, transport := range []transportMode{transportStdio, transportHTTP} {
		s := &server{transport: transport}
		var schema struct {
			Properties map[string]json.RawMessage `json:"properties"`
		}
		if err := json.Unmarshal(s.advertisedSchema(describeToolSpec), &schema); err != nil {
			t.Fatalf("transport %d: the advertised schema does not parse: %v", transport, err)
		}
		if _, ok := schema.Properties["name"]; !ok {
			t.Fatalf("transport %d: the advertised schema lists no `name`, so this reads nothing: %v", transport, schema.Properties)
		}
		for property := range schema.Properties {
			lower := strings.ToLower(property)
			for _, word := range []string{"token", "password", "credential", "secret", "auth", "login", "username"} {
				if strings.Contains(lower, word) {
					t.Errorf("transport %d: describe advertises %q, an argument a credential would be sent in", transport, property)
				}
			}
		}
	}
}
