// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package mcp

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/aggrega-ai/aggrega-cli/internal/publishops"
	"github.com/aggrega-ai/aggrega-cli/pkg/signing"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// A key the served workspace's own configuration names is the other half of the
// key argument a listener refuses, and it is the half no caller had to send.
//
// The argument is refused because a key on this disk is the operator's rather than
// the caller's. That reason does not become less true when the same key arrives
// from the configuration instead — the caller still supplied nothing, anyone who
// reached this address could have caused the same signature, and the signature is
// still attributable to whoever started the process. So the configured key is not
// reached either, and a deployment that names one is told so where its listener
// starts.

// signingKeyEnvVarName is the variable that supplies signing.key_path from the
// environment. It is spelled here because the configuration package keeps its
// table of variables unexported; the needle below asserts the spelling still
// reaches a resolved configuration, so a rename fails loudly rather than leaving
// the environment row configuring nothing.
const signingKeyEnvVarName = "AGGREGA_SIGNING_KEY_PATH"

// heldKeyForAWorkspace writes a real ECDSA P-256 private key and returns its path.
//
// A real key rather than a path to nothing, because the keyboard rows assert that
// a KEYED SIGNER was built. A path to nothing would fail to load, and those rows
// would then pass on the wrong failure — proving only that something went wrong,
// which is what the listener rows already say.
func heldKeyForAWorkspace(t *testing.T) string {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate the held key: %v", err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatalf("marshal the held key: %v", err)
	}
	path := filepath.Join(t.TempDir(), "held-key.pem")
	if err := os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}), 0o600); err != nil {
		t.Fatalf("write the held key: %v", err)
	}
	return path
}

// configuredKeyRoute is one of the ways a served workspace's configuration comes
// to name a signing key.
type configuredKeyRoute struct {
	name string
	// session stands up a session on one transport over a workspace that names
	// keyPath by this route.
	session func(t *testing.T, transport transportMode, keyPath string) *server
}

// keySessionOn stands up a publication session on one transport over a workspace
// whose configuration document is workspaceYAML.
//
// The variable that supplies the same setting is cleared first, because the
// environment overlay sits ABOVE the configuration file: a machine whose owner
// exports it would have every file-based row here resolving their key path rather
// than the one the row wrote, and the row that asserts a keyed signer was built
// would then fail for a reason nothing in it names. An empty value is what the
// overlay reads as unset. The route that wants the variable sets it afterwards.
func keySessionOn(t *testing.T, transport transportMode, workspaceYAML string) *server {
	t.Helper()
	s, _ := publishFixture(t, workspaceYAML)
	t.Setenv(signingKeyEnvVarName, "")
	s.transport = transport
	return s
}

// theThreeWaysAWorkspaceNamesASigningKey is every route by which the served
// workspace's configuration can carry a key path.
//
// All three are walked because a rule closed at one of them is a rule a reader
// cannot derive from the others: an operator who moved the setting from the file
// into the environment, or from the global block into the one registry it applies
// to, has not changed what the key is or who holds it.
func theThreeWaysAWorkspaceNamesASigningKey() []configuredKeyRoute {
	return []configuredKeyRoute{
		{
			name: "named globally",
			session: func(t *testing.T, transport transportMode, keyPath string) *server {
				t.Helper()
				return keySessionOn(t, transport, defaultRegistryWorkspace+"signing:\n  key_path: "+keyPath+"\n")
			},
		},
		{
			name: "named for this registry",
			session: func(t *testing.T, transport transportMode, keyPath string) *server {
				t.Helper()
				return keySessionOn(t, transport, defaultRegistryWorkspace+"registries:\n  - host: "+
					publishTestRegistry+"\n    signing:\n      key_path: "+keyPath+"\n")
			},
		},
		{
			name: "supplied from the environment",
			session: func(t *testing.T, transport transportMode, keyPath string) *server {
				t.Helper()
				s := keySessionOn(t, transport, defaultRegistryWorkspace)
				t.Setenv(signingKeyEnvVarName, keyPath)
				return s
			},
		},
	}
}

// TestEverySigningToolWithholdsTheConfiguredKeyOnlyOnAListener reads what each of
// the three signing tools projects onto its own input contract.
//
// The axis here is the TOOL, and the walk is deliberately not crossed with the
// configuration routes below: what a tool projects is decided by the transport
// alone, so crossing the two would run one assertion nine times and read as
// covering something it does not. What the routes change is what there is to
// withhold, and that is the next test's axis.
//
// Each tool projects onto a different config type, which is exactly why all three
// are walked: a tool whose projection forgot the field would compile, would carry
// every other credential input correctly, and would sign every connected caller's
// publication with the operator's key.
func TestEverySigningToolWithholdsTheConfiguredKeyOnlyOnAListener(t *testing.T) {
	keyPath := heldKeyForAWorkspace(t)

	for _, tool := range theThreeSigningTools() {
		t.Run(tool.tool, func(t *testing.T) {
			t.Run("reached over a listener", func(t *testing.T) {
				s := keySessionOn(t, transportHTTP, defaultRegistryWorkspace+"signing:\n  key_path: "+keyPath+"\n")
				if !tool.project(t, s, "").configuredKeyWithheld {
					t.Fatal("this tool hands the publication layer a call that may spend the key the served " +
						"workspace's configuration names — the caller sent no credential of its own, so that " +
						"signature would be the operator's and could not be recalled from anyone who fetched it")
				}
			})

			t.Run("reached over the process's own streams", func(t *testing.T) {
				s := keySessionOn(t, transportStdio, defaultRegistryWorkspace+"signing:\n  key_path: "+keyPath+"\n")
				if tool.project(t, s, "").configuredKeyWithheld {
					t.Fatal("a caller at this machine's keyboard was refused its own workspace's configured " +
						"key, which is a file that caller owns; without this row the listener row above would " +
						"pass against a build that reaches no configured key on either transport")
				}
			})
		})
	}
}

// TestAConfiguredKeyIsNotSpentForAListenersCaller drives the credential the whole
// way — the tool's own argument projection, the publication layer, and the signer
// it builds — against a workspace that names a key by each route in turn.
//
// The deprecation tool is the one driven here, and the reason is a measured
// property rather than a preference: its signer is constructed before anything is
// attached and the layer exposes that construction, so both rows reach a real
// signer without a byte leaving this machine. A publication and a signing batch
// resolve a published digest first, so the keyboard row would have to reach a
// registry to get as far as the credential. What those two verbs do with a
// withheld key is held at each of their own resolution sites, in the publication
// package, where every site can be driven directly.
func TestAConfiguredKeyIsNotSpentForAListenersCaller(t *testing.T) {
	keyPath := heldKeyForAWorkspace(t)
	args := deprecateArtifactArgs{
		Reference: publishTestRegistry + "/component/core/api-server:1.0.0",
		Severity:  "warning",
		Reason:    "superseded",
	}

	configFor := func(t *testing.T, s *server) publishops.DeprecateConfig {
		t.Helper()
		cfg, refusal := s.deprecateArtifactConfig(args)
		if refusal != "" {
			t.Fatalf("deprecateArtifactConfig refused legal arguments: %s", refusal)
		}
		return cfg
	}

	for _, route := range theThreeWaysAWorkspaceNamesASigningKey() {
		t.Run(route.name, func(t *testing.T) {
			t.Run("reached over a listener, carrying no credential of its own", func(t *testing.T) {
				s := route.session(t, transportHTTP, keyPath)
				signer, err := publishops.DeprecateSigner(configFor(t, s))
				if err == nil {
					t.Fatalf("a caller that sent no credential was given a %T built from the key this "+
						"workspace's configuration names", signer)
				}
				if strings.Contains(err.Error(), keyPath) {
					t.Errorf("the refusal names the configured key %q, so it was read: %v", keyPath, err)
				}
				assertRefusalNamesOnlyWhatThisCallerHas(t, err.Error())
			})

			t.Run("reached over the process's own streams", func(t *testing.T) {
				s := route.session(t, transportStdio, keyPath)
				signer, err := publishops.DeprecateSigner(configFor(t, s))
				if err != nil {
					t.Fatalf("a caller at this machine's keyboard must still sign with its own workspace's "+
						"configured key: %v", err)
				}
				if _, keyed := signer.(*signing.KeyedSigner); !keyed {
					t.Fatalf("the configured key built a %T at this machine's keyboard, want the keyed signer — "+
						"without this row the listener row above would pass against a build that reaches no "+
						"configured key at all, or against a route that configures nothing", signer)
				}
			})
		})
	}
}

// TestTheEnvironmentRouteReachesTheServedWorkspace is the needle under the
// environment row above.
//
// That row supplies the key through a variable name spelled in this file, because
// the configuration package keeps its own table of them unexported. A rename there
// would leave the row configuring nothing — and a row that configures nothing
// passes its listener case for the wrong reason, silently, while reporting that
// the withholding works.
func TestTheEnvironmentRouteReachesTheServedWorkspace(t *testing.T) {
	s, _ := publishFixture(t, defaultRegistryWorkspace)
	t.Setenv(signingKeyEnvVarName, "/key/from/the/environment.pem")

	tw, err := s.resolveToolWorkspace("", nil)
	if err != nil {
		t.Fatalf("resolve the served workspace: %v", err)
	}
	if got := tw.cfg.ResolveSigning(publishTestRegistry).KeyPath; got != "/key/from/the/environment.pem" {
		t.Fatalf("%s did not reach the served workspace's resolved signing configuration (key_path = %q) — "+
			"the environment row above is configuring nothing and proving nothing", signingKeyEnvVarName, got)
	}
}

// listenerRootNaming writes a workspace a listener can be started over, carrying
// whatever extra configuration a row needs.
//
// It declares itself offline so that starting a server over it reaches no
// registry: what these rows are about is what the process says about its own
// configuration, and a network round trip on the way there would be measuring
// something else.
//
// The global configuration layer is a file of this test's own, reached through the
// environment override. That is not tidiness: the launch reads the whole cascade to
// decide what it has to say about a configured key, so a row that did not redirect
// the global layer would read the configuration of whoever is running these tests —
// and the row that asserts NOTHING is said would then fail on a machine whose owner
// happens to hold a signing key.
func listenerRootNaming(t *testing.T, extraYAML string) string {
	t.Helper()
	globalPath := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(globalPath, []byte("registries: []\n"), 0o644); err != nil {
		t.Fatalf("write the global layer: %v", err)
	}
	t.Setenv(workspaceconfig.EnvConfigOverride, globalPath)
	// The environment overlay sits above the file, so a machine whose owner
	// exports this variable would configure a key into the row that asserts none
	// is configured. An empty value is what the overlay reads as unset.
	t.Setenv(signingKeyEnvVarName, "")

	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, workspaceconfig.ConfigFilename),
		[]byte("workspace: {}\noffline: true\n"+extraYAML), 0o644); err != nil {
		t.Fatalf("write the workspace configuration: %v", err)
	}
	return root
}

// theListenerKeyPath is the path the rows below configure. It is distinctive
// enough that a search for it in the captured stream cannot match anything else,
// and it points at nothing: no row here gets as far as reading it, which is the
// property under test.
const theListenerKeyPath = "/held/listener-signing-key-4c7e.pem"

// TestAListenerSaysOnceThatItWillNotUseTheConfiguredKey drives a real launch and
// reads what the process wrote about itself.
//
// The line exists to pay down the one cost of not reaching the key: a workspace
// that signs with a held key at a terminal signs with a resolved identity when the
// same workspace is served over a listener, and that is a change of behaviour
// nothing else announces. An operator must learn it from the process they started
// rather than from a signature they did not expect.
//
// The row that configures no key is as load-bearing as the row that does. A line
// written unconditionally would be noise on every deployment's stream, and an
// operator who learned to skip it would skip the one that mattered.
func TestAListenerSaysOnceThatItWillNotUseTheConfiguredKey(t *testing.T) {
	launch := func(t *testing.T, root string) string {
		t.Helper()
		log := &lockedBuffer{}
		runCtx, endRun := context.WithCancel(context.Background())
		served := make(chan error, 1)
		go func() {
			served <- listenAndServe(runCtx,
				Config{Stderr: log, WorkspaceRoot: root},
				defaultTransportConfig(),
				ListenConfig{Address: "127.0.0.1:0"}, nil)
		}()
		// Waiting for the endpoint to be up is what makes the read below happen to a
		// launch that got all the way through startup, rather than to one caught
		// part-way.
		waitForEvent(t, log, eventListening)
		endRun()
		select {
		case err := <-served:
			if err != nil {
				t.Fatalf("the run ended with %v", err)
			}
		case <-time.After(runReturnCeiling):
			t.Fatal("the run never ended within the liveness ceiling")
		}
		return log.String()
	}

	t.Run("a workspace that names a signing key", func(t *testing.T) {
		written := launch(t, listenerRootNaming(t, "signing:\n  key_path: "+theListenerKeyPath+"\n"))

		events := eventsWritten(t, written)
		if got := strings.Count(strings.Join(events, "\n"), eventSigningKeyIgnored); got != 1 {
			t.Fatalf("the stream carries %d %q lines, want exactly one: %v", got, eventSigningKeyIgnored, events)
		}
		// Before the endpoint is up, because an operator reads this stream in order
		// and the point of the line is to be read before a caller can arrive.
		if slices.Index(events, eventSigningKeyIgnored) > slices.Index(events, eventListening) {
			t.Errorf("the line arrives after the endpoint was already accepting: %v", events)
		}

		for _, line := range auditLines(t, written) {
			if auditString(t, line, "event") != eventSigningKeyIgnored {
				continue
			}
			detail := auditString(t, line, "detail")
			// The setting, because that is what the operator wrote and can go and
			// change; not the path, because where a key sits is information about
			// this machine.
			if !strings.Contains(detail, "signing.key_path") {
				t.Errorf("the line does not name the setting it is about: %q", detail)
			}
			if strings.Contains(detail, theListenerKeyPath) {
				t.Errorf("the line carries the configured path, which says where a key sits on this "+
					"machine: %q", detail)
			}
		}
		if strings.Contains(written, theListenerKeyPath) {
			t.Errorf("the configured path reached the stream somewhere: %q", written)
		}
	})

	t.Run("a workspace that names none", func(t *testing.T) {
		events := eventsWritten(t, launch(t, listenerRootNaming(t, "")))
		if slices.Contains(events, eventSigningKeyIgnored) {
			t.Errorf("a deployment that configured no signing key was told one is not used: %v", events)
		}
		// The control that keeps the row above from passing on a launch that wrote
		// nothing at all.
		if !slices.Contains(events, eventListening) {
			t.Fatalf("this launch never reported an endpoint, so its silence proves nothing: %v", events)
		}
	})
}

// TestNamesASigningKeyReadsEveryScopeAKeyCanBeNamedAt pins the predicate the
// listener's startup line is decided by.
//
// It is asked before any call names a registry, so it cannot use the per-host
// resolution a publication uses: it reports whether ANY scope names a key. The row
// that names one only under a registry entry is the one that matters — a predicate
// reading the global block alone would pass every other row here and tell a
// deployment nothing about the key it actually configured.
func TestNamesASigningKeyReadsEveryScopeAKeyCanBeNamedAt(t *testing.T) {
	for _, row := range []struct {
		name          string
		workspaceYAML string
		want          bool
	}{
		{"none configured", defaultRegistryWorkspace, false},
		{"named globally", defaultRegistryWorkspace + "signing:\n  key_path: /held/key.pem\n", true},
		{
			"named for one registry",
			defaultRegistryWorkspace + "registries:\n  - host: " + publishTestRegistry +
				"\n    signing:\n      key_path: /held/key.pem\n",
			true,
		},
		{
			"a registry entry naming other signing settings but no key",
			defaultRegistryWorkspace + "registries:\n  - host: " + publishTestRegistry +
				"\n    signing:\n      fulcio_url: https://fulcio.private\n",
			false,
		},
	} {
		t.Run(row.name, func(t *testing.T) {
			root := t.TempDir()
			globalPath := filepath.Join(t.TempDir(), "config.yaml")
			if err := os.WriteFile(globalPath, []byte("registries: []\n"), 0o644); err != nil {
				t.Fatalf("write the global layer: %v", err)
			}
			t.Setenv(workspaceconfig.EnvConfigOverride, globalPath)
			if err := os.WriteFile(filepath.Join(root, workspaceconfig.ConfigFilename),
				[]byte(row.workspaceYAML), 0o644); err != nil {
				t.Fatalf("write the workspace layer: %v", err)
			}
			rw, err := workspaceconfig.Resolve(root, "")
			if err != nil {
				t.Fatalf("resolve the workspace: %v", err)
			}
			if got := namesASigningKey(rw.Config()); got != row.want {
				t.Errorf("namesASigningKey = %v, want %v", got, row.want)
			}
		})
	}
}
