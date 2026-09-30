// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/signing"
)

// What signs, and with whose credential, is a property of how the caller reached
// this server.
//
// A caller reaching it over the process's own streams launched it and is at this
// machine's keyboard: the identity this process carries is that caller's own, and
// a key on this disk is their key. A caller reaching a listener is somewhere else,
// and both of those belong to whoever started the server — so the identity ladder
// is handed an environment that answers nothing and the argument that selects a
// held key is refused and withheld.
//
// Every assertion here is written as a pair, and the keyboard row is never
// decoration: it is what stops the listener row passing against a build that
// resolves no identity anywhere.

// The credential values these assertions export and send. They are deliberately
// not well-formed tokens: nothing here reaches a certificate authority, and a
// value that cannot pass the shared subject check is what makes "the signer
// received THIS one" visible as a refusal naming it rather than as a network call.
//
// They are distinct from one another so a text carrying one can be told from a
// text carrying another, and distinctive enough that a search for one cannot match
// something else in a message.
const (
	exportedIdentityToken = "exported-identity-token-value-7f3a"
	exportedSigstoreToken = "exported-sigstore-alias-value-91cd"
	sentIdentityToken     = "sent-in-the-call-token-value-04be"
	ambientRequestToken   = "ambient-request-token-value-b52e"
)

// everyTokenValue is every credential value these assertions put into play, which
// is what the leak check below holds every answer against.
func everyTokenValue() []string {
	return []string{exportedIdentityToken, exportedSigstoreToken, sentIdentityToken, ambientRequestToken}
}

// The ambient endpoint's environment names, spelled here because the ladder
// declares them unexported.
//
// A second spelling is exactly the kind that rots silently — a renamed variable
// would leave these assertions exporting nothing and reporting the endpoint
// unreached for the wrong reason — so every assertion that uses them first asks
// the ladder's own predicate whether it can see what was exported. That check is
// the needle: it fails when the names drift, rather than passing vacuously.
const (
	ambientRequestURLName   = "ACTIONS_ID_TOKEN_REQUEST_URL"
	ambientRequestTokenName = "ACTIONS_ID_TOKEN_REQUEST_TOKEN"
)

// exportEveryEnvironmentCredential puts an identity in every environment source
// the ladder reads, so what a row measures is which of them the session consults
// rather than which of them exists.
func exportEveryEnvironmentCredential(t *testing.T) {
	t.Helper()
	t.Setenv(signing.EnvIdentityToken, exportedIdentityToken)
	t.Setenv(signing.EnvSigstoreIDToken, exportedSigstoreToken)
}

// signingSession stands up a publication session on one transport, over a real
// workspace, and captures everything the server writes.
//
// The capture is not incidental: a credential that reached a log line is a
// credential at rest on the machine running the server, so the sink is a buffer
// this test can read rather than a discard nobody can.
func signingSession(t *testing.T, transport transportMode) (*server, string, *bytes.Buffer) {
	t.Helper()
	s, root := publishFixture(t, defaultRegistryWorkspace)
	captured := &bytes.Buffer{}
	s.transport = transport
	s.out = captured
	s.logw = captured
	return s, root, captured
}

// listenerRequest is a request carrying a credential that admits everything this
// server publishes.
//
// A request reaching a listener with no credential at all is refused before any
// tool is entered, so a row driving one would measure the scope check rather than
// what it came to measure.
func listenerRequest() context.Context {
	return context.WithValue(context.Background(), principalKey{}, &principal{
		Scopes: []string{scopeRead, scopeWorkspaceWrite, scopeRegistryWrite},
	})
}

// callToolOn drives one tools/call through the real dispatch on behalf of a
// request that may run what it names, and returns the tool's own result.
func callToolOn(t *testing.T, s *server, ctx context.Context, name string, args any) *callToolResult {
	t.Helper()
	raw, err := json.Marshal(args)
	if err != nil {
		t.Fatalf("marshal arguments: %v", err)
	}
	params, err := json.Marshal(callToolParams{Name: name, Arguments: raw})
	if err != nil {
		t.Fatalf("marshal params: %v", err)
	}
	res, rpcErr := s.handleToolsCall(ctx, params)
	if rpcErr != nil {
		t.Fatalf("tools/call %s was answered with an error envelope (code %d): %s", name, rpcErr.Code, rpcErr.Message)
	}
	cr, ok := res.(*callToolResult)
	if !ok {
		t.Fatalf("tools/call %s returned %T, want a tool result", name, res)
	}
	if len(cr.Content) == 0 || cr.Content[0].Type != "text" {
		t.Fatalf("tools/call %s returned no text content block: %+v", name, cr.Content)
	}
	return cr
}

// credentialInputs is the slice of a publication's input contract that decides
// what may sign: the token the call carried, the environment the identity ladder
// reads, the vocabulary the refusal at the end of that ladder is written in, and
// whether the key the served workspace's own configuration names is reachable.
type credentialInputs struct {
	idToken string
	look    func(string) string
	supply  signing.IdentityTokenSupply

	// configuredKeyWithheld is the tool's own projection of whether the key the
	// workspace configuration names may be spent for this caller. It is lifted
	// alongside the three above because it is the same question about a different
	// credential, and a surface that answered the two differently would refuse the
	// argument naming a key while the configuration supplied the same key.
	configuredKeyWithheld bool
}

// resolveAsTheLayerWould drives the shared identity ladder with the inputs a tool
// produced, through the entry point the publication layer uses.
//
// A nil reader is the layer's documented default for the live process
// environment, and it is applied here for the same reason the layer applies it:
// the row that produces one is the transport whose caller owns this machine, and
// resolving it any other way would measure something no caller reaches.
func resolveAsTheLayerWould(inputs credentialInputs) (string, signing.IdentityTokenSource, error) {
	look := inputs.look
	if look == nil {
		look = os.Getenv
	}
	return signing.ResolveIdentityTokenNonInteractive(inputs.idToken, look, nil, &http.Client{}, inputs.supply)
}

// signingToolInputs is one signing tool and the projection of its own arguments
// onto its own input contract.
//
// Each tool projects onto a different config type, so each row lifts its own
// credential inputs out rather than sharing a helper: the projection IS what is
// under test, and a helper that built one would be measuring itself.
type signingToolInputs struct {
	// tool names the tool, so a failure says which one.
	tool string
	// project runs the tool's own argument projection and lifts out what decides
	// the identity. sentToken is what the call carried, empty for a call carrying
	// none.
	project func(t *testing.T, s *server, sentToken string) credentialInputs
}

// theThreeSigningTools is every tool on this surface that resolves a keyless
// identity. The data-artifact publication is absent because it signs nothing —
// a published artifact is signed afterwards, by reference — so it has no identity
// to resolve and claiming it as a fourth passing row would prove nothing.
func theThreeSigningTools() []signingToolInputs {
	return []signingToolInputs{
		{
			tool: publishEntityToolName,
			project: func(t *testing.T, s *server, sentToken string) credentialInputs {
				t.Helper()
				cfg, refusal := s.publishEntityConfig(publishEntityArgs{
					Entity: "1.0.0/entity.yaml", IdentityToken: sentToken,
				})
				if refusal != "" {
					t.Fatalf("publishEntityConfig refused legal arguments: %s", refusal)
				}
				return credentialInputs{
					idToken:               cfg.IDToken,
					look:                  cfg.EnvLookup,
					supply:                cfg.IdentitySupply,
					configuredKeyWithheld: cfg.ConfiguredKeyNotConsulted,
				}
			},
		},
		{
			tool: signArtifactsToolName,
			project: func(t *testing.T, s *server, sentToken string) credentialInputs {
				t.Helper()
				cfg, refusal := s.signArtifactsConfig(signArtifactsArgs{
					References: []string{publishTestRegistry + "/component/core/api-server:1.0.0"}, IdentityToken: sentToken,
				})
				if refusal != "" {
					t.Fatalf("signArtifactsConfig refused legal arguments: %s", refusal)
				}
				return credentialInputs{
					idToken:               cfg.IDToken,
					look:                  cfg.EnvLookup,
					supply:                cfg.IdentitySupply,
					configuredKeyWithheld: cfg.ConfiguredKeyNotConsulted,
				}
			},
		},
		{
			tool: deprecateArtifactToolName,
			project: func(t *testing.T, s *server, sentToken string) credentialInputs {
				t.Helper()
				cfg, refusal := s.deprecateArtifactConfig(deprecateArtifactArgs{
					Reference: publishTestRegistry + "/component/core/api-server:1.0.0",
					Severity:  "warning", Reason: "superseded", IdentityToken: sentToken,
				})
				if refusal != "" {
					t.Fatalf("deprecateArtifactConfig refused legal arguments: %s", refusal)
				}
				return credentialInputs{
					idToken:               cfg.IDToken,
					look:                  cfg.EnvLookup,
					supply:                cfg.IdentitySupply,
					configuredKeyWithheld: cfg.ConfiguredKeyNotConsulted,
				}
			},
		},
	}
}

// assertRefusalNamesOnlyWhatThisCallerHas holds a listener's credential refusal to
// naming the one input such a caller can supply, and to naming none of the ones it
// cannot.
//
// Both halves are asserted because either alone passes on a broken message. A
// refusal that named the argument AND told the caller to export a variable would
// satisfy the first; a refusal that named nothing at all would satisfy the second.
func assertRefusalNamesOnlyWhatThisCallerHas(t *testing.T, message string) {
	t.Helper()
	// A substring check against an empty string succeeds against anything, so an
	// unset vocabulary would turn the loop below into a check that cannot fail.
	if toolIdentitySupply.AbsentInput == "" || toolIdentitySupply.Remedy == "" {
		t.Fatalf("this surface declares no identity vocabulary (%+v), so the assertions below would "+
			"pass against any message at all", toolIdentitySupply)
	}
	for _, want := range []string{"identity token", toolIdentitySupply.AbsentInput, toolIdentitySupply.Remedy} {
		if !strings.Contains(message, want) {
			t.Errorf("the refusal must name %q, which is what a caller here can send; got %q", want, message)
		}
	}
	for _, unwanted := range []string{
		signing.EnvIdentityToken,
		signing.EnvSigstoreIDToken,
		"ambient",
		"GitHub Actions",
		"--",
		"terminal",
		"stdin",
	} {
		if strings.Contains(message, unwanted) {
			t.Errorf("the refusal names %q, which this caller neither has nor can arrange — nothing read it, "+
				"so acting on it would change nothing: %q", unwanted, message)
		}
	}
}

// TestOverAListenerOnlyATokenSentInTheCallResolves walks all three signing tools
// on both transports, through each tool's own argument projection and the shared
// ladder the publication layer resolves through.
//
// The three rows are one property read three ways. Over a listener with nothing
// sent, the ladder has nothing to find although the environment carries an
// identity in every source it reads — and refuses naming only the argument. Over a
// listener with a token sent, that exact token resolves, which is what keeps the
// first row from passing against a session that can resolve nothing at all. At
// this machine's keyboard, with the same environment, the exported token resolves
// — the control that keeps both listener rows from passing against a ladder that
// stopped reading the environment anywhere.
func TestOverAListenerOnlyATokenSentInTheCallResolves(t *testing.T) {
	exportEveryEnvironmentCredential(t)

	for _, row := range theThreeSigningTools() {
		t.Run(row.tool, func(t *testing.T) {
			t.Run("reached over a listener, carrying no token", func(t *testing.T) {
				s, _, _ := signingSession(t, transportHTTP)
				inputs := row.project(t, s, "")
				if inputs.look == nil {
					t.Fatal("the identity ladder was left reading the live process environment, which is " +
						"this machine's rather than the caller's")
				}
				idToken, source, err := resolveAsTheLayerWould(inputs)
				if err == nil {
					t.Fatalf("an identity resolved for a caller that sent none: idToken=%q source!=nil=%v",
						idToken, source != nil)
				}
				if idToken != "" || source != nil {
					t.Errorf("the refusal came back alongside a resolution: idToken=%q source!=nil=%v",
						idToken, source != nil)
				}
				assertRefusalNamesOnlyWhatThisCallerHas(t, err.Error())
				assertCarriesNoTokenValue(t, "the refusal", err.Error())
				t.Logf("refusal: %s", err.Error())
			})

			t.Run("reached over a listener, carrying a token", func(t *testing.T) {
				s, _, _ := signingSession(t, transportHTTP)
				idToken, source, err := resolveAsTheLayerWould(row.project(t, s, sentIdentityToken))
				if err != nil {
					t.Fatalf("a token sent in the call must resolve: %v", err)
				}
				if source != nil {
					t.Fatal("a token sent in the call resolves to itself, not to a live source")
				}
				if idToken != sentIdentityToken {
					t.Errorf("the signer would receive %q; the call sent %q", idToken, sentIdentityToken)
				}
			})

			t.Run("reached over the process's own streams, carrying no token", func(t *testing.T) {
				s, _, _ := signingSession(t, transportStdio)
				inputs := row.project(t, s, "")
				if inputs.look != nil {
					t.Fatal("a caller at this machine's keyboard was handed an environment that is not " +
						"this machine's, so the control below would measure the wrong thing")
				}
				idToken, source, err := resolveAsTheLayerWould(inputs)
				if err != nil {
					t.Fatalf("the exported identity must resolve for a caller at this machine's keyboard: %v", err)
				}
				if source != nil {
					t.Fatal("an exported token resolves to itself, not to a live source")
				}
				if idToken != exportedIdentityToken {
					t.Errorf("the signer would receive %q; the environment exports %q", idToken, exportedIdentityToken)
				}
			})
		})
	}
}

// TestTheDeprecationToolAnswersAListenerWithACredentialRefusal drives the whole
// path a caller drives — dispatch, argument projection, the publication layer, the
// shared ladder — rather than the projection alone.
//
// The deprecation tool is the one driven here, and the reason is a measured
// property rather than a preference: it builds its signer before the marker is
// attached and the shared layer checks a token's shape locally, so both rows reach
// the credential without a byte leaving this machine. A signing batch resolves the
// published digest of each reference first, so the keyboard row would have to
// reach a registry to get as far as the credential.
func TestTheDeprecationToolAnswersAListenerWithACredentialRefusal(t *testing.T) {
	exportEveryEnvironmentCredential(t)

	args := deprecateArtifactArgs{
		Reference: publishTestRegistry + "/component/core/api-server:1.0.0",
		Severity:  "warning",
		Reason:    "superseded",
	}

	t.Run("reached over a listener", func(t *testing.T) {
		s, _, captured := signingSession(t, transportHTTP)
		res := callToolOn(t, s, listenerRequest(), deprecateArtifactToolName, args)
		if !res.IsError {
			t.Fatalf("a call with no token of its own must be refused; got %s", res.Content[0].Text)
		}
		assertRefusalNamesOnlyWhatThisCallerHas(t, res.Content[0].Text)
		assertCarriesNoTokenValue(t, "the refusal", res.Content[0].Text)
		assertCarriesNoTokenValue(t, "what the server wrote", captured.String())
	})

	t.Run("reached over a listener, carrying a token of its own", func(t *testing.T) {
		s, _, captured := signingSession(t, transportHTTP)
		sent := args
		sent.IdentityToken = sentIdentityToken
		res := callToolOn(t, s, listenerRequest(), deprecateArtifactToolName, sent)
		if !res.IsError {
			t.Fatalf("the sent value is not a usable token, so the marking must fail: %s", res.Content[0].Text)
		}
		// The shared layer's own check on the token it was handed, which it reaches
		// only by having been handed one. That is the whole of the property: a caller
		// reaching a listener signs with what it sent, so a call that sent something
		// gets as far as what it sent being judged.
		if !strings.Contains(res.Content[0].Text, "not a well-formed JWT") {
			t.Errorf("the failure is not the layer's check on the sent token, so this proves nothing "+
				"about what reached the signer: %q", res.Content[0].Text)
		}
		assertCarriesNoTokenValue(t, "the failure", res.Content[0].Text)
		assertCarriesNoTokenValue(t, "what the server wrote", captured.String())
	})

	t.Run("reached over the process's own streams", func(t *testing.T) {
		s, _, captured := signingSession(t, transportStdio)
		res := callToolOn(t, s, context.Background(), deprecateArtifactToolName, args)
		if !res.IsError {
			t.Fatalf("the exported value is not a usable token, so the marking must fail: %s", res.Content[0].Text)
		}
		// Reaching that same check with nothing sent is what proves the exported
		// identity travelled the whole path to the signer: a session that had not
		// read the environment would have refused for want of a credential instead,
		// which is the first row's answer.
		if !strings.Contains(res.Content[0].Text, "not a well-formed JWT") {
			t.Errorf("the failure is not the layer's check on the exported token, so this proves nothing "+
				"about what reached the signer: %q", res.Content[0].Text)
		}
		assertCarriesNoTokenValue(t, "the failure", res.Content[0].Text)
		assertCarriesNoTokenValue(t, "what the server wrote", captured.String())
	})
}

// TestTheSigningRefusalPointsAtThePageTeachingItsCredentials drives the signing
// tool's credential refusal through the real dispatch, and reads the page its
// guidance closes with.
//
// The refusal is about which credential a call spends and what to send, which no
// entity kind's page teaches, so it names the page that does. The deprecation
// tool's refusal is the control: it reaches the same missing credential through a
// different tool whose pointer did not change, so a composition that re-pointed
// every refusal would fail there.
func TestTheSigningRefusalPointsAtThePageTeachingItsCredentials(t *testing.T) {
	exportEveryEnvironmentCredential(t)

	for _, tc := range []struct {
		scenario string
		tool     string
		args     any
		page     string
	}{
		{
			scenario: "the signing tool names the page teaching its credentials",
			tool:     signArtifactsToolName,
			args:     signArtifactsArgs{References: []string{publishTestRegistry + "/component/core/api-server:1.0.0"}},
			page:     docServerReference,
		},
		{
			scenario: "the deprecation tool keeps the pointer it had",
			tool:     deprecateArtifactToolName,
			args: deprecateArtifactArgs{
				Reference: publishTestRegistry + "/component/core/api-server:1.0.0",
				Severity:  "warning",
				Reason:    "superseded",
			},
			page: docIndex,
		},
	} {
		t.Run(tc.scenario, func(t *testing.T) {
			s, _, _ := signingSession(t, transportHTTP)
			res := callToolOn(t, s, listenerRequest(), tc.tool, tc.args)
			if !res.IsError {
				t.Fatalf("a call with no token of its own must be refused; got %s", res.Content[0].Text)
			}
			assertRefusalNamesOnlyWhatThisCallerHas(t, res.Content[0].Text)
			if want := "\n\n" + docPointerLine(tc.page); !strings.HasSuffix(res.Content[0].Text, want) {
				t.Errorf("the refusal does not close with %q:\n%s", want, res.Content[0].Text)
			}
		})
	}
}

// TestTheAmbientIdentityEndpointIsNeverReachedOverAListener pins the third rung,
// which is the one no argument and no variable name appears in: the ladder
// discovers it by reading two environment variables and then fetches from
// whatever they point at.
//
// A listening endpoint is what makes the assertion mean anything. Without one, "no
// request arrived" would be true of a session that tried and failed to reach it.
func TestTheAmbientIdentityEndpointIsNeverReachedOverAListener(t *testing.T) {
	// The counter is written by the endpoint's own goroutine and read by this one,
	// so it is atomic rather than a plain integer: a race here would be reported as
	// a failure of whatever test happened to be running beside it.
	var requests atomic.Int64
	endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"value":%q}`, exportedIdentityToken)
	}))
	defer endpoint.Close()

	t.Setenv(ambientRequestURLName, endpoint.URL)
	t.Setenv(ambientRequestTokenName, ambientRequestToken)
	t.Setenv(signing.EnvIdentityToken, "")
	t.Setenv(signing.EnvSigstoreIDToken, "")

	// The needle, before anything is measured. These variable names are spelled in
	// this file and declared unexported in the ladder, so a rename would leave the
	// rows below exporting nothing and reporting the endpoint unreached because it
	// was never discoverable — which is a property of this test rather than of the
	// session under test.
	if !signing.GitHubActionsAmbientAvailable(os.Getenv) {
		t.Fatal("the ladder's own predicate cannot see the ambient endpoint this test exported, so the " +
			"names here no longer match the ones it reads; the rows below would measure nothing")
	}

	t.Run("reached over a listener", func(t *testing.T) {
		s, _, _ := signingSession(t, transportHTTP)
		for _, row := range theThreeSigningTools() {
			t.Run(row.tool, func(t *testing.T) {
				inputs := row.project(t, s, "")
				if inputs.look == nil {
					t.Fatal("the identity ladder was left reading the live process environment, which is " +
						"this machine's rather than the caller's")
				}
				if signing.GitHubActionsAmbientAvailable(inputs.look) {
					t.Fatal("the environment this session hands the ladder still reports an ambient endpoint")
				}
				if _, _, err := resolveAsTheLayerWould(inputs); err == nil {
					t.Fatal("an identity resolved from an endpoint this caller does not have")
				}
			})
		}
		// And the same thing driven rather than projected: a real call, refused, with
		// the endpoint standing by throughout.
		driven := callToolOn(t, s, listenerRequest(), deprecateArtifactToolName, deprecateArtifactArgs{
			Reference: publishTestRegistry + "/component/core/api-server:1.0.0",
			Severity:  "warning", Reason: "superseded",
		})
		if !driven.IsError {
			t.Fatalf("a call with no token of its own must be refused; got %s", driven.Content[0].Text)
		}
		assertRefusalNamesOnlyWhatThisCallerHas(t, driven.Content[0].Text)

		if got := requests.Load(); got != 0 {
			t.Errorf("the ambient endpoint was contacted %d time(s) on behalf of a caller reaching a "+
				"listener; the identity it mints is this machine's", got)
		}
	})

	// The positive control, and it is what makes the count above evidence: the same
	// endpoint, reached through the same ladder, for the caller whose machine this
	// is. Without it a count of zero would be satisfied by an endpoint nothing could
	// have reached.
	t.Run("reached over the process's own streams", func(t *testing.T) {
		before := requests.Load()
		s, _, _ := signingSession(t, transportStdio)
		inputs := theThreeSigningTools()[0].project(t, s, "")
		idToken, source, err := resolveAsTheLayerWould(inputs)
		if err != nil {
			t.Fatalf("the ambient endpoint must resolve for a caller at this machine's keyboard: %v", err)
		}
		if source == nil {
			t.Fatalf("the ambient rung resolves to a live source; got a static token %q", idToken)
		}
		minted, ferr := source(context.Background())
		if ferr != nil {
			t.Fatalf("fetch from the ambient endpoint: %v", ferr)
		}
		if minted != exportedIdentityToken {
			t.Errorf("the endpoint minted %q, want %q", minted, exportedIdentityToken)
		}
		if got := requests.Load(); got != before+1 {
			t.Errorf("the endpoint recorded %d request(s) and then %d; the counter does not move, so a "+
				"count of zero above says nothing", before, got)
		}
	})
}

// TestOverAListenerTheSigningToolsRefuseAKeyHeldOnThisMachine drives the refusal
// through the real dispatch, which is where it is composed for every tool that
// declares one.
//
// Two negative controls sit beside it, and both are load-bearing. A tool that
// signs nothing declares no such argument and must not be refused for carrying
// one, or the refusal would be keyed on the spelling rather than on what the tool
// does. And the same call at this machine's keyboard must be accepted, or the row
// above would pass against a build that refused the argument everywhere.
func TestOverAListenerTheSigningToolsRefuseAKeyHeldOnThisMachine(t *testing.T) {
	const heldKey = "/etc/aggrega/signing-key.pem"

	refused := []struct {
		tool string
		args string
	}{
		{publishEntityToolName, `{"entity":"1.0.0/entity.yaml","dry_run":true,"key_path":"` + heldKey + `"}`},
		{signArtifactsToolName, `{"references":["` + publishTestRegistry + `/component/core/api-server:1.0.0"],"key_path":"` + heldKey + `"}`},
		{deprecateArtifactToolName, `{"reference":"` + publishTestRegistry + `/component/core/api-server:1.0.0","severity":"warning","reason":"superseded","key_path":"` + heldKey + `"}`},
	}

	for _, tc := range refused {
		t.Run("refused over a listener: "+tc.tool, func(t *testing.T) {
			s, _, _ := signingSession(t, transportHTTP)
			res := rawCallOn(t, s, listenerRequest(), tc.tool, tc.args)
			if !res.IsError {
				t.Fatalf("a call selecting a key on this machine must be refused; got %s", res.Content[0].Text)
			}
			text := res.Content[0].Text
			if !strings.Contains(text, "'"+argumentKeyPath+"'") {
				t.Errorf("the refusal must name the argument to remove: %q", text)
			}
			if !strings.Contains(text, tc.tool) {
				t.Errorf("the refusal must name the tool it applies to: %q", text)
			}
			if !strings.Contains(text, "'identity_token'") {
				t.Errorf("the refusal must name what to send instead: %q", text)
			}
			// The key's own location is the caller's to correct, and naming it back
			// would put a path on this machine into an answer a stranger reads.
			if strings.Contains(text, heldKey) {
				t.Errorf("the refusal echoes the path it refused, which names a location on this machine "+
					"to a caller that is not on it: %q", text)
			}
		})
	}

	// A tool that signs nothing declares no such argument, so a call carrying one is
	// answered by the tool rather than refused by the transport.
	t.Run("not refused for a tool that signs nothing", func(t *testing.T) {
		s, _, _ := signingSession(t, transportHTTP)
		res := rawCallOn(t, s, listenerRequest(), publishArtifactToolName,
			`{"file":"bundle.txt","name":"handbook","tag":"2.0.0","dry_run":true,"key_path":"`+heldKey+`"}`)
		if res.IsError {
			t.Fatalf("a tool that declares no signing key must not be refused for one: %s", res.Content[0].Text)
		}
	})

	// The control for the whole assertion: at this machine's keyboard the same
	// argument is accepted, because the machine holding the key is the caller's.
	t.Run("accepted at this machine's keyboard", func(t *testing.T) {
		s, _, _ := signingSession(t, transportStdio)
		res := rawCallOn(t, s, context.Background(), publishEntityToolName,
			`{"entity":"1.0.0/entity.yaml","dry_run":true,"key_path":"`+heldKey+`"}`)
		if res.IsError {
			t.Fatalf("a caller at this machine's keyboard may name a key on it: %s", res.Content[0].Text)
		}

		// And the argument reaches the operation rather than merely being tolerated:
		// the projection every keyed signature is built from carries it.
		for _, row := range []struct {
			tool    string
			keyPath func(t *testing.T) string
		}{
			{publishEntityToolName, func(t *testing.T) string {
				cfg, refusal := s.publishEntityConfig(publishEntityArgs{Entity: "1.0.0/entity.yaml", KeyPath: heldKey})
				if refusal != "" {
					t.Fatalf("publishEntityConfig: %s", refusal)
				}
				return cfg.KeyPath
			}},
			{signArtifactsToolName, func(t *testing.T) string {
				cfg, refusal := s.signArtifactsConfig(signArtifactsArgs{
					References: []string{publishTestRegistry + "/component/core/api-server:1.0.0"}, KeyPath: heldKey,
				})
				if refusal != "" {
					t.Fatalf("signArtifactsConfig: %s", refusal)
				}
				return cfg.KeyPath
			}},
			{deprecateArtifactToolName, func(t *testing.T) string {
				cfg, refusal := s.deprecateArtifactConfig(deprecateArtifactArgs{
					Reference: publishTestRegistry + "/component/core/api-server:1.0.0",
					Severity:  "warning", Reason: "superseded", KeyPath: heldKey,
				})
				if refusal != "" {
					t.Fatalf("deprecateArtifactConfig: %s", refusal)
				}
				return cfg.KeyPath
			}},
		} {
			if got := row.keyPath(t); got != heldKey {
				t.Errorf("%s carries %q into the operation; the call named %q", row.tool, got, heldKey)
			}
		}
	})
}

// rawCallOn drives one tools/call with a verbatim arguments payload, so a row can
// send an argument the tool's own argument type does not declare — which is what a
// caller composing a call by hand can do and is exactly the case under test.
func rawCallOn(t *testing.T, s *server, ctx context.Context, name, rawArgs string) *callToolResult {
	t.Helper()
	params := json.RawMessage(fmt.Sprintf(`{"name":%q,"arguments":%s}`, name, rawArgs))
	res, rpcErr := s.handleToolsCall(ctx, params)
	if rpcErr != nil {
		t.Fatalf("tools/call %s was answered with an error envelope (code %d): %s", name, rpcErr.Code, rpcErr.Message)
	}
	cr, ok := res.(*callToolResult)
	if !ok {
		t.Fatalf("tools/call %s returned %T, want a tool result", name, res)
	}
	if len(cr.Content) == 0 || cr.Content[0].Type != "text" {
		t.Fatalf("tools/call %s returned no text content block: %+v", name, cr.Content)
	}
	return cr
}

// TestAListenerIsNotOfferedAKeyItWouldBeRefusedForSending walks every schema both
// advertising surfaces carry, on both transports.
//
// The schema is what a caller reads before it composes anything, so a schema
// offering an argument its own call surface refuses is the same defect the refusal
// exists to prevent, arriving one step earlier. The keyboard row is the control:
// the same walk over the transport that accepts the argument must still find it,
// so a projection that had started removing it everywhere fails here rather than
// reporting the listener clean.
func TestAListenerIsNotOfferedAKeyItWouldBeRefusedForSending(t *testing.T) {
	overHTTP := advertisedOverHTTP(t, newHTTPFixture(t, defaultTransportConfig(), nil))
	overStdio := advertisedOverStdio(t, newCommandToolServer(t, nil))

	keyArgument := []string{argumentKeyPath}

	t.Run("reached over a listener", func(t *testing.T) {
		if len(overHTTP) == 0 {
			t.Fatal("no schema was advertised at all; the walk measured nothing")
		}
		// The three tools have to be offered, or "none of them advertises the
		// argument" would be satisfied by a listener that advertises none of them.
		offered := advertisedToolNames(overHTTP)
		for _, name := range []string{publishEntityToolName, signArtifactsToolName, deprecateArtifactToolName} {
			if !containsStr(offered, name) {
				t.Fatalf("a caller reaching the listener is not offered %s at all, so this walk cannot "+
					"measure what its schema withholds", name)
			}
		}
		for _, a := range overHTTP {
			for _, site := range workspaceArgumentSites(t, a.schema, keyArgument) {
				t.Errorf("%s advertises %s with %s, which a call on this transport is refused for sending",
					a.surface, a.tool, site)
			}
		}
	})

	t.Run("reached over the process's own streams", func(t *testing.T) {
		carrying := map[string]bool{}
		for _, a := range overStdio {
			if len(workspaceArgumentSites(t, a.schema, keyArgument)) > 0 {
				carrying[a.tool] = true
			}
		}
		for _, name := range []string{publishEntityToolName, signArtifactsToolName, deprecateArtifactToolName} {
			if !carrying[name] {
				t.Errorf("%s no longer advertises %s on the transport that accepts it, so a caller that may "+
					"send it is not told so", name, argumentKeyPath)
			}
		}
		if carrying[publishArtifactToolName] {
			t.Errorf("%s advertises %s though it signs nothing", publishArtifactToolName, argumentKeyPath)
		}
	})
}

// tokenValueIn reports the credential value a text carries, and whether it
// carries one at all. It is the one predicate every absence claim in this file
// rests on, so the needle below has something to point at: an assertion helper
// that both looked and reported would be its own instrument.
func tokenValueIn(text string) (string, bool) {
	for _, value := range everyTokenValue() {
		if strings.Contains(text, value) {
			return value, true
		}
	}
	return "", false
}

// assertCarriesNoTokenValue holds one answer to the rule that a refusal never
// echoes a credential.
//
// A token is a bearer secret: whatever holds it can sign as its owner until it
// expires, so a value that reaches a message reaches every hop that message takes
// and a value that reaches a log line is at rest on this machine.
func assertCarriesNoTokenValue(t *testing.T, what, text string) {
	t.Helper()
	if value, found := tokenValueIn(text); found {
		t.Errorf("%s carries a credential value (%s): %q", what, value, text)
	}
}

// TestTheLeakCheckFindsACredentialWhenOneIsThere is the needle under every
// absence claim above.
//
// Each of those reports nothing found, and a predicate that had stopped looking —
// because a value was renamed on one side and not the other, or because the set it
// walks came back empty — would report exactly the same thing. So it is pointed at
// a text that does carry one, once per value in play, and must name it.
func TestTheLeakCheckFindsACredentialWhenOneIsThere(t *testing.T) {
	if len(everyTokenValue()) == 0 {
		t.Fatal("no credential value is in play, so the predicate walks an empty set and reports " +
			"nothing found whatever it is handed")
	}
	for _, value := range everyTokenValue() {
		planted := "the operation refused after reading " + value + " from somewhere it should not have"
		found, carries := tokenValueIn(planted)
		if !carries {
			t.Errorf("a text carrying %q is not reported as carrying a credential, so the absence "+
				"assertions elsewhere in this file measure nothing", value)
			continue
		}
		if found != value {
			t.Errorf("a text carrying %q is reported as carrying %q", value, found)
		}
	}
	if _, carries := tokenValueIn("the operation refused: no 'identity_token' was sent"); carries {
		t.Error("a text carrying no credential is reported as carrying one, so the predicate would " +
			"fail every answer whatever it said")
	}
}
