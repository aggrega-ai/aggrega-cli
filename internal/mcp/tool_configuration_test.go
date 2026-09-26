// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/opsurface"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// What the configuration reading tells a caller, read on both transports over
// one workspace.
//
// The two answers are compared rather than asserted apart. A rule that keeps
// something back is only testable against what would have been there without
// it: an assertion that read the listener's answer alone would pass against a
// reading that kept back everything, and one that read the terminal's alone
// would pass against a reading that kept back nothing.

// theConfiguredKeyPath is the signing key path the cases below configure. It is
// distinctive enough that finding it anywhere in a listener's answer cannot be a
// coincidence, and it points at nothing: no case here loads a key.
const theConfiguredKeyPath = "/held/configuration-reading-key-9f31.pem"

// theConfiguredReleasePath is a second location, reached through a different
// setting, so no case here passes on a rule written for one field. It is a
// Release document named absolutely, which the loader accepts and the cascade
// then takes as written rather than resolving against the workspace root.
const theConfiguredReleasePath = "/held/configuration-reading-release-9f31.yaml"

// theSecondLocationYAML writes that second location into a workspace's own
// configuration document.
const theSecondLocationYAML = "build:\n  authoring_release: " + theConfiguredReleasePath + "\n"

// listenerOverWorkspace starts one real listener over the given workspace root,
// with a real authorization server behind it.
//
// The authenticator is the one the handler ships with rather than a stub, so a
// token minted here is admitted on the terms a served listener admits one. It
// composes the same fixture the credential grid drives, which is what makes a
// grant minted here and a grant minted there the same thing.
func listenerOverWorkspace(t *testing.T, root string) *scopeGridFixture {
	t.Helper()
	issuer := newAuthorizationServerDouble(t)
	s := newCommandToolServer(t, nil)
	s.startDir = root
	h, err := newHTTPHandler(context.Background(), s, transportFor(issuer.authorizationConfig(), issuer.egress()))
	if err != nil {
		t.Fatalf("newHTTPHandler: %v", err)
	}
	ts := httptest.NewServer(h)
	t.Cleanup(ts.Close)
	return &scopeGridFixture{httpFixture: &httpFixture{ts: ts, h: h}, issuer: issuer, root: root, srv: s}
}

// readOverTheListener calls the configuration reading over a real listener with
// a credential carrying the read scope, and returns the structured answer.
func readOverTheListener(t *testing.T, f *scopeGridFixture) configShowResult {
	t.Helper()
	got := f.callTool(t, f.grantOf(t, scopeRead), "config-show", map[string]any{})
	if got.status != http.StatusOK {
		t.Fatalf("the configuration reading was answered %d over the listener (body %q)", got.status, got.raw)
	}
	if got.frame.Error != nil {
		t.Fatalf("the configuration reading was answered with an error envelope: %+v", got.frame.Error)
	}
	var result struct {
		Content           []toolTextContent `json:"content"`
		StructuredContent json.RawMessage   `json:"structuredContent"`
		IsError           bool              `json:"isError"`
	}
	if err := json.Unmarshal(got.frame.Result, &result); err != nil {
		t.Fatalf("the answer is not a tool result: %v (body %q)", err, got.raw)
	}
	if result.IsError {
		t.Fatalf("the configuration reading refused over the listener: %q", result.Content)
	}
	if len(result.StructuredContent) == 0 {
		t.Fatalf("the configuration reading answered over the listener with no structured payload (body %q)", got.raw)
	}
	var payload configShowResult
	if err := json.Unmarshal(result.StructuredContent, &payload); err != nil {
		t.Fatalf("the structured payload does not decode: %v (bytes %s)", err, result.StructuredContent)
	}
	return payload
}

// readOverTheProcessStreams calls the configuration reading the way a client
// that launched this process reaches it, over the same workspace root.
func readOverTheProcessStreams(t *testing.T, root string) configShowResult {
	t.Helper()
	s := newTestServer(t)
	s.startDir = root
	s.transport = transportStdio

	res, rpcErr := configShowInvoke(s, context.Background(), nil)
	if rpcErr != nil {
		t.Fatalf("the configuration reading returned an error envelope: %+v", rpcErr)
	}
	if res.IsError {
		t.Fatalf("the configuration reading refused at the keyboard: %q", res.Content)
	}
	if len(res.StructuredContent) == 0 {
		t.Fatal("the configuration reading answered with no structured payload")
	}
	var payload configShowResult
	if err := json.Unmarshal(res.StructuredContent, &payload); err != nil {
		t.Fatalf("the structured payload does not decode: %v", err)
	}
	return payload
}

// theAbsoluteValueEachDeclaredSettingTakes is one workspace document per setting
// this reading declares as naming a place on the host, each writing an absolute
// location into that setting and nothing else.
//
// It is what makes the declared set a measurement rather than an assertion. An
// entry claiming a setting can carry a host location, for a setting the
// configuration refuses an absolute value for, keeps back something that could
// never have been one — which costs a caller a fact for nothing and is invisible
// from the set's own text.
//
// Each document is written INSTEAD of the shared fixture's rather than after it,
// because several of these settings live under a key that fixture already
// writes and two of the same key is not a document the loader will read.
func theAbsoluteValueEachDeclaredSettingTakes() map[string]string {
	return map[string]string{
		"build.authoring_release":     "build:\n  authoring_release: /abs/held/release.yaml\n",
		"mcp.listen":                  "mcp:\n  listen: unix:/abs/held/aggrega.sock\n",
		"mcp.tls.cert_file":           "mcp:\n  tls:\n    cert_file: /abs/held/cert.pem\n    key_file: /abs/held/key.pem\n",
		"mcp.tls.key_file":            "mcp:\n  tls:\n    cert_file: /abs/held/cert.pem\n    key_file: /abs/held/key.pem\n",
		"mcp.authorization.jwks_file": "mcp:\n  authorization:\n    jwks_file: /abs/held/jwks.json\n",
		"mcp.health.listen":           "mcp:\n  health:\n    listen: unix:/abs/held/health.sock\n",
		"signing.key_path":            "signing:\n  key_path: /abs/held/key.pem\n",
		"workspace.catalog_dirs":      "workspace:\n  catalog_dirs: [/abs/held/catalog]\n",
		"plugins.governance":          "plugins:\n  governance:\n    - uri: /abs/held/redact.wasm\n      role: ir-verdict\n",
		"plugins.linter":              "plugins:\n  linter:\n    - uri: /abs/held/lint.wasm\n",
		"plugins.importer":            "plugins:\n  importer:\n    - uri: /abs/held/import.wasm\n",
		"plugins.providers":           "plugins:\n  providers:\n    - capability: helm\n      uri: /abs/held/helm.wasm\n",
	}
}

// resolvedOver resolves one workspace whose own document is the given body, with
// the machine-wide layer redirected at a file of this test's own.
//
// The redirection is not tidiness. The cascade reads the machine-wide layer
// whatever this test wrote, so a case that did not redirect it would be reading
// the configuration of whoever is running these tests — and a row asserting that
// a setting is NOT configured would then fail on a machine whose owner happens to
// configure it.
func resolvedOver(t *testing.T, body string) *workspaceconfig.ResolvedWorkspace {
	t.Helper()
	globalPath := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(globalPath, []byte("registries: []\n"), 0o644); err != nil {
		t.Fatalf("write the machine-wide layer: %v", err)
	}
	t.Setenv(workspaceconfig.EnvConfigOverride, globalPath)
	t.Setenv(signingKeyEnvVarName, "")

	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, workspaceconfig.ConfigFilename), []byte(body), 0o644); err != nil {
		t.Fatalf("write the workspace's own configuration: %v", err)
	}
	rw, err := workspaceconfig.Resolve(root, root)
	if err != nil {
		t.Fatalf("resolve a workspace whose configuration reads %q: %v", body, err)
	}
	return rw
}

// TestEveryDeclaredHostLocationSettingCanActuallyCarryOne drives an absolute
// location into each declared setting and reads it back out of the configuration.
//
// 🔴 The control row is the whole point of the assertion, and it is the one that
// was measured to fail. The on-disk artifact cache directory reads like the
// plainest host location in the configuration, and the loader REFUSES an absolute
// value for it: every reader resolves that setting against the workspace root, so
// it can only ever be relative to a root this transport already keeps back.
// Declaring it would keep back something that is not a location at all, and
// nothing about the declared set's own text would show that.
func TestEveryDeclaredHostLocationSettingCanActuallyCarryOne(t *testing.T) {
	documents := theAbsoluteValueEachDeclaredSettingTakes()

	for name := range settingsNamingAHostLocation {
		if _, covered := documents[name]; !covered {
			t.Errorf("%q is declared as a setting whose value names a place on the host and no document "+
				"here drives one into it, so nothing shows it can carry one", name)
		}
	}
	for name := range documents {
		if _, declared := settingsNamingAHostLocation[name]; !declared {
			t.Errorf("a document here drives an absolute location into %q, which this reading does not "+
				"declare as naming one; either declare it or drop the document", name)
		}
	}

	names := make([]string, 0, len(documents))
	for name := range documents {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		t.Run(name, func(t *testing.T) {
			rw := resolvedOver(t, "offline: true\n"+documents[name])
			origins, err := rw.ValueOrigins()
			if err != nil {
				t.Fatalf("read the provenance: %v", err)
			}
			for _, o := range origins {
				if o.Field != name {
					continue
				}
				if !strings.Contains(o.Value, "/abs/held/") {
					t.Fatalf("%s resolved to %q, and the document drove an absolute location into it; the "+
						"row is configuring nothing and proves nothing", name, o.Value)
				}
				if !valueNamesAHostLocation(o) {
					t.Fatalf("%s holds %q, which is a location on this host, and this reading would not "+
						"keep it back", name, o.Value)
				}
				return
			}
			t.Fatalf("the configuration reports no setting called %q at all", name)
		})
	}

	t.Run("the control: a setting the loader refuses an absolute value for", func(t *testing.T) {
		if _, declared := settingsNamingAHostLocation["artifact_cache_dir"]; declared {
			t.Fatal("artifact_cache_dir is declared as naming a place on the host, and the loader refuses " +
				"an absolute value for it, so it can only ever be relative to a root this transport " +
				"already keeps back")
		}
		globalPath := filepath.Join(t.TempDir(), "config.yaml")
		if err := os.WriteFile(globalPath, []byte("registries: []\n"), 0o644); err != nil {
			t.Fatalf("write the machine-wide layer: %v", err)
		}
		t.Setenv(workspaceconfig.EnvConfigOverride, globalPath)
		root := t.TempDir()
		if err := os.WriteFile(filepath.Join(root, workspaceconfig.ConfigFilename),
			[]byte("workspace: {}\noffline: true\nartifact_cache_dir: /abs/held/cache\n"), 0o644); err != nil {
			t.Fatalf("write the workspace's own configuration: %v", err)
		}
		if _, err := workspaceconfig.Resolve(root, root); err == nil {
			t.Fatal("the loader accepted an absolute artifact cache directory; the reason this setting is " +
				"left out of the declared set no longer holds, so the decision needs taking again")
		} else {
			t.Logf("the loader refuses an absolute artifact cache directory: %v", err)
		}
	})
}

// TestTheConfigurationReadingAnswersAListenersReadCredential is clause one: a
// credential carrying the read scope is offered the tool and gets a structured
// answer from it, both read off a real listener rather than out of a registry.
//
// The listing assertion is what the planted class defect reddens. The tool
// declares no class of its own — the row below asserts that too — so its class
// is the one the operation set declares, and declaring that operation to reach
// the machine takes the tool out of this listing with no edit to the tool at all.
func TestTheConfigurationReadingAnswersAListenersReadCredential(t *testing.T) {
	if configShowToolSpec.reach != opsurface.Reach("") {
		t.Errorf("the configuration reading's definition declares the class %q of its own; the class is a "+
			"property of the operation and is read where the tool enters a registry, so a class authored "+
			"here would be a second declaration of it", configShowToolSpec.reach)
	}

	f := listenerOverWorkspace(t, listenerRootNaming(t, ""))
	credential := f.grantOf(t, scopeRead)

	offered := append(f.listed(t, credential), f.surfaced(t, credential)...)
	if !slices.Contains(offered, "config-show") {
		t.Fatalf("a credential carrying %s was not offered config-show; it was offered %v",
			scopeRead, sortedNames(offered))
	}

	payload := readOverTheListener(t, f)
	if len(payload.Settings) == 0 {
		t.Fatal("the answer carries no settings at all, so nothing about it is a reading of a configuration")
	}
	if len(payload.Layers) == 0 {
		t.Fatal("the answer names no contributing layer, and this workspace has a configuration document " +
			"of its own, so the cascade this fixture wrote did not reach the answer")
	}
	for _, setting := range payload.Settings {
		if setting.Field == "" {
			t.Errorf("a setting is reported under no name at all: %+v", setting)
		}
	}
}

// TestTheTwoTransportsDifferOnlyByTheHostLocationsTheListenerWithholds is
// clause three, driven over one workspace and compared field by field.
//
// The workspace names two locations through two different settings, so a rule
// written for one field would fail here rather than pass on the other. Every
// difference between the two answers has to be a value that names a place on
// this host, and every such value has to actually differ — a reading that kept
// back nothing and a reading that kept back everything would each satisfy only
// half of that, which is why both halves are asserted.
func TestTheTwoTransportsDifferOnlyByTheHostLocationsTheListenerWithholds(t *testing.T) {
	root := listenerRootNaming(t, theSecondLocationYAML+
		"signing:\n  key_path: "+theConfiguredKeyPath+"\n")

	atTheKeyboard := readOverTheProcessStreams(t, root)
	overTheListener := readOverTheListener(t, listenerOverWorkspace(t, root))

	for _, complaint := range transportDifferenceComplaints(atTheKeyboard, overTheListener) {
		t.Errorf("%s", complaint)
	}

	// The whole answer is searched for each configured location as well, because
	// the field-by-field reading above can only look at the fields it knows to
	// look at, and a location reaching some other field would pass it.
	encoded, err := json.Marshal(overTheListener)
	if err != nil {
		t.Fatalf("marshal the listener's answer: %v", err)
	}
	for _, location := range []string{theConfiguredKeyPath, theConfiguredReleasePath, root} {
		if strings.Contains(string(encoded), location) {
			t.Errorf("the listener's answer carries %q somewhere in it: %s", location, encoded)
		}
	}
}

// transportDifferenceComplaints reads the listener's answer against the answer
// the same workspace gives at the keyboard, and returns one complaint per
// difference that is not a value naming a place on this host.
//
// It returns complaints rather than failing, so the same reading can be run over
// a deliberately damaged answer. An assertion nobody has watched reject anything
// says nothing about what it would catch.
//
// It requires the keeping-back to have actually happened as well as to have been
// confined: a listener answer identical to the keyboard's would draw no
// difference complaints at all, and would be the exact failure this rule exists
// to prevent.
func transportDifferenceComplaints(atTheKeyboard, overTheListener configShowResult) []string {
	var complaints []string
	say := func(format string, args ...any) {
		complaints = append(complaints, fmt.Sprintf(format, args...))
	}

	if !overTheListener.HostLocationsWithheld {
		say("the listener's answer does not say it keeps host locations back, so a caller reading it cannot " +
			"tell an answer with nothing kept back from one with nothing configured")
	}
	if atTheKeyboard.HostLocationsWithheld {
		say("the keyboard's answer says it keeps host locations back, and a client that launched this " +
			"process owns every location it could name")
	}
	if atTheKeyboard.WorkspaceRoot == nil {
		say("the keyboard's answer names no workspace root, so the comparison below has nothing to be about")
	}
	if overTheListener.WorkspaceRoot != nil {
		say("the listener's answer names the workspace root %q, which is a directory on this host",
			*overTheListener.WorkspaceRoot)
	}
	if !overTheListener.WorkspaceRootWithheld {
		say("the listener's answer carries no workspace root and does not mark it as kept back, so the " +
			"absence reads as a server that could not resolve one")
	}

	if len(atTheKeyboard.Layers) != len(overTheListener.Layers) {
		say("the two answers name %d and %d contributing layer(s); a layer whose name is kept back is still "+
			"a layer that contributed, and dropping it changes what the cascade looks like",
			len(atTheKeyboard.Layers), len(overTheListener.Layers))
	} else {
		withheldLayers := 0
		for i := range atTheKeyboard.Layers {
			mine, theirs := atTheKeyboard.Layers[i], overTheListener.Layers[i]
			switch {
			case theirs.Name != nil && mine.Name != nil && *theirs.Name == *mine.Name:
				if sourceNamesAHostLocation(*theirs.Name) {
					say("layer %d is reported to the listener as %q, which names a config file on this host",
						i, *theirs.Name)
				}
			case theirs.Name == nil && theirs.Withheld:
				withheldLayers++
				if mine.Name == nil {
					say("layer %d is kept back from the listener and the keyboard names none either, so "+
						"nothing here shows a name was kept back rather than never produced", i)
				} else if !sourceNamesAHostLocation(*mine.Name) {
					say("layer %d is kept back from the listener and its name %q is not a config file on "+
						"this host", i, *mine.Name)
				}
			case theirs.Name == nil:
				say("layer %d reaches the listener with no name and no mark, so its absence reads as a "+
					"layer that has none", i)
			default:
				say("layer %d differs between the two answers: the keyboard reads %v and the listener %q",
					i, mine.Name, *theirs.Name)
			}
		}
		if withheldLayers == 0 {
			say("no contributing layer was kept back from the listener, so this comparison would hold " +
				"against a reading that publishes every config file's path")
		}
	}

	if len(atTheKeyboard.Settings) != len(overTheListener.Settings) {
		say("the two answers carry %d and %d setting(s); a setting whose value is kept back is still a "+
			"setting in force, and dropping it hides that this server is configured for it at all",
			len(atTheKeyboard.Settings), len(overTheListener.Settings))
		return complaints
	}

	withheldValues, withheldSources := 0, 0
	for i := range atTheKeyboard.Settings {
		mine, theirs := atTheKeyboard.Settings[i], overTheListener.Settings[i]
		if mine.Field != theirs.Field {
			say("setting %d is %q at the keyboard and %q over the listener, so the two answers are not in "+
				"the same order and nothing below compares like with like", i, mine.Field, theirs.Field)
			continue
		}
		switch {
		case theirs.Value != nil && mine.Value != nil && *theirs.Value == *mine.Value:
			// Agreement is the case a comparison written only around differences
			// misses, and it is the one a leak arrives in: a listener answer
			// carrying the value the keyboard carries reads as two answers that
			// agree, which is exactly what a rule that did nothing would produce.
			if keyboardValueNamesAHostLocation(mine) {
				say("%s is reported to the listener as %q, which names a place on this host",
					mine.Field, *theirs.Value)
			}
		case theirs.Value == nil && theirs.ValueWithheld:
			withheldValues++
			if mine.Value == nil {
				say("%s has its value kept back from the listener and the keyboard carries none either, "+
					"so nothing shows a value was kept back rather than never produced", mine.Field)
			} else if _, declared := settingsNamingAHostLocation[mine.Field]; !declared {
				say("%s has its value kept back from the listener and its value %q is not one this "+
					"reading declares as naming a place on the host", mine.Field, *mine.Value)
			}
		case theirs.Value == nil:
			say("%s reaches the listener with no value and no mark, so a caller cannot tell it from a "+
				"setting nobody configured", mine.Field)
		default:
			say("%s is %v at the keyboard and %q over the listener, and that difference is not a value "+
				"being kept back", mine.Field, mine.Value, *theirs.Value)
		}

		switch {
		case theirs.Source != nil && mine.Source != nil && *theirs.Source == *mine.Source:
			if sourceNamesAHostLocation(*theirs.Source) {
				say("%s is reported to the listener as coming from %q, which names a config file on this "+
					"host", mine.Field, *theirs.Source)
			}
		case theirs.Source == nil && theirs.SourceWithheld:
			withheldSources++
			if mine.Source != nil && !sourceNamesAHostLocation(*mine.Source) {
				say("%s has its originating layer kept back from the listener and that layer %q is not a "+
					"config file on this host", mine.Field, *mine.Source)
			}
		case theirs.Source == nil:
			say("%s reaches the listener with no originating layer and no mark", mine.Field)
		default:
			say("%s came from %v at the keyboard and %q over the listener", mine.Field, mine.Source, *theirs.Source)
		}
	}

	if withheldValues == 0 {
		say("no setting's value was kept back from the listener, so this comparison would hold against a " +
			"reading that publishes every path this server is configured with")
	}
	if withheldSources == 0 {
		say("no setting's originating layer was kept back from the listener, so this comparison would " +
			"hold against a reading that publishes the path of every config file it read")
	}

	return complaints
}

// keyboardValueNamesAHostLocation reports whether a setting as the keyboard's
// answer reports it holds a value naming a place on this host — which is what
// the listener's answer for that same setting must therefore not carry.
//
// It reads the declared set rather than restating one, and that is deliberate:
// the set IS the decision about which settings hold a location, so a second copy
// here would be a copy of the thing under test rather than an independent
// reading of it. What this comparison establishes independently is the
// MECHANISM — that the two answers differ at exactly those settings and nowhere
// else. That the set names the right settings is established separately, by
// driving an absolute location into each of them; and that no location reaches
// the listener whatever the set says is established separately again, by
// searching the whole answer for each configured location.
//
// The second condition is read the same way the reading itself reads it, off the
// layer the keyboard attributed the setting to: a setting no layer sets holds
// what this binary ships.
func keyboardValueNamesAHostLocation(setting configSettingResult) bool {
	if _, declared := settingsNamingAHostLocation[setting.Field]; !declared {
		return false
	}
	if setting.Source == nil {
		return false
	}
	kind, _ := workspaceconfig.ValueOrigin{Source: *setting.Source}.Layer()
	return kind != workspaceconfig.OriginBuiltInDefault
}

// TestTheTransportComparisonRejectsALocationCarriedThrough is the negative
// control under the comparison above, and it is what makes that comparison worth
// running.
//
// Each row damages an otherwise-correct listener answer in one way a wrong
// implementation would produce, and requires the comparison to name it. A
// reading that stayed silent here would be silent over the leak it exists to
// catch.
func TestTheTransportComparisonRejectsALocationCarriedThrough(t *testing.T) {
	root := listenerRootNaming(t, theSecondLocationYAML+
		"signing:\n  key_path: "+theConfiguredKeyPath+"\n")

	atTheKeyboard := readOverTheProcessStreams(t, root)
	overTheListener := readOverTheListener(t, listenerOverWorkspace(t, root))

	if got := transportDifferenceComplaints(atTheKeyboard, overTheListener); len(got) != 0 {
		t.Fatalf("the undamaged pair already draws complaints, so nothing below is attributable: %v", got)
	}

	for _, tc := range []struct {
		// scenario says what a wrong implementation would have done.
		scenario string
		damage   func(p *configShowResult)
		// names is what the complaint has to mention.
		names string
	}{
		{
			scenario: "a configured signing key carried through to the listener",
			damage: func(p *configShowResult) {
				for i := range p.Settings {
					if p.Settings[i].Field == "signing.key_path" {
						p.Settings[i].Value = stringPointer(theConfiguredKeyPath)
						p.Settings[i].ValueWithheld = false
					}
				}
			},
			names: "signing.key_path",
		},
		{
			scenario: "a configured Release document carried through to the listener",
			damage: func(p *configShowResult) {
				for i := range p.Settings {
					if p.Settings[i].Field == "build.authoring_release" {
						p.Settings[i].Value = stringPointer(theConfiguredReleasePath)
						p.Settings[i].ValueWithheld = false
					}
				}
			},
			names: "build.authoring_release",
		},
		{
			scenario: "a value dropped without the mark that says it was kept back",
			damage: func(p *configShowResult) {
				for i := range p.Settings {
					if p.Settings[i].Field == "signing.key_path" {
						p.Settings[i].ValueWithheld = false
					}
				}
			},
			names: "cannot tell it from a setting nobody configured",
		},
		{
			scenario: "a config file's path carried through as a setting's originating layer",
			damage: func(p *configShowResult) {
				for i := range p.Settings {
					if p.Settings[i].Field == "signing.key_path" {
						p.Settings[i].Source = stringPointer(filepath.Join(root, workspaceconfig.ConfigFilename))
						p.Settings[i].SourceWithheld = false
					}
				}
			},
			names: "names a config file on this host",
		},
		{
			scenario: "a config file's path carried through in the contributing-layer list",
			damage: func(p *configShowResult) {
				for i := range p.Layers {
					if p.Layers[i].Withheld {
						p.Layers[i].Name = stringPointer(filepath.Join(root, workspaceconfig.ConfigFilename))
						p.Layers[i].Withheld = false
					}
				}
			},
			names: "names a config file on this host",
		},
		{
			scenario: "a kept-back layer dropped from the list rather than marked",
			damage: func(p *configShowResult) {
				kept := p.Layers[:0]
				for _, layer := range p.Layers {
					if layer.Withheld {
						continue
					}
					kept = append(kept, layer)
				}
				p.Layers = kept
			},
			names: "contributing layer",
		},
		{
			scenario: "the workspace root carried through",
			damage: func(p *configShowResult) {
				p.WorkspaceRoot = stringPointer(root)
				p.WorkspaceRootWithheld = false
			},
			names: "workspace root",
		},
		{
			scenario: "an answer that says nothing about keeping anything back",
			damage:   func(p *configShowResult) { p.HostLocationsWithheld = false },
			names:    "does not say it keeps host locations back",
		},
	} {
		t.Run(tc.scenario, func(t *testing.T) {
			damaged := cloneConfigShowResult(overTheListener)
			tc.damage(&damaged)
			complaints := transportDifferenceComplaints(atTheKeyboard, damaged)
			if len(complaints) == 0 {
				t.Fatalf("%s drew no complaint, so the comparison would not notice it", tc.scenario)
			}
			named := false
			for _, complaint := range complaints {
				if strings.Contains(complaint, tc.names) {
					named = true
					break
				}
			}
			if !named {
				t.Errorf("%s complained without naming %q: %v", tc.scenario, tc.names, complaints)
			}
			t.Logf("%s produced: %v", tc.scenario, complaints)
		})
	}
}

// cloneConfigShowResult copies an answer deeply enough that a damaged copy
// cannot reach back into the one it was taken from.
func cloneConfigShowResult(p configShowResult) configShowResult {
	out := p
	out.Layers = append([]configLayerResult(nil), p.Layers...)
	out.Settings = append([]configSettingResult(nil), p.Settings...)
	return out
}

// TestAKeptBackValueReadsAsKeptBackRatherThanAsNothingConfigured is clause four:
// two workspaces, one naming a signing key and one naming none, read over a
// listener, with the two answers required to differ.
//
// This is the half of the rule that decides whether the answer is usable at all.
// A reading that kept every location-bearing setting back whatever it held would
// pass every leak assertion above and would tell a caller exactly the same thing
// about a server that has been given a key and one that has not.
func TestAKeptBackValueReadsAsKeptBackRatherThanAsNothingConfigured(t *testing.T) {
	read := func(t *testing.T, extraYAML string) configSettingResult {
		t.Helper()
		root := listenerRootNaming(t, extraYAML)
		payload := readOverTheListener(t, listenerOverWorkspace(t, root))
		for _, setting := range payload.Settings {
			if setting.Field == "signing.key_path" {
				return setting
			}
		}
		t.Fatalf("the answer reports no signing.key_path at all: %+v", payload.Settings)
		return configSettingResult{}
	}

	t.Run("a workspace that names a signing key", func(t *testing.T) {
		got := read(t, "signing:\n  key_path: "+theConfiguredKeyPath+"\n")
		if !got.ValueWithheld {
			t.Errorf("the configured key path is not marked as kept back: %+v", got)
		}
		if got.Value != nil {
			t.Errorf("the configured key path reached the listener as %q", *got.Value)
		}
	})

	t.Run("a workspace that names none", func(t *testing.T) {
		got := read(t, "")
		if got.ValueWithheld {
			t.Errorf("a workspace that configures no signing key is told one is being kept back, which is " +
				"a claim about this host that is not true of it")
		}
		if got.Value == nil {
			t.Errorf("a workspace that configures no signing key reports no value at all, so a caller " +
				"cannot tell it from one whose key path is being kept back")
		} else if *got.Value != "" {
			t.Errorf("a workspace that configures no signing key reports %q", *got.Value)
		}
	})

	t.Run("the two answers differ", func(t *testing.T) {
		named := read(t, "signing:\n  key_path: "+theConfiguredKeyPath+"\n")
		unnamed := read(t, "")
		if named == unnamed {
			t.Fatalf("both workspaces are reported identically as %+v, so this reading tells a caller "+
				"nothing it can act on", named)
		}
	})
}

// TestTheConfigurationReadingCarriesEveryFactTheProvenanceCallProduced is clause
// five: the values the tool carries are the values its command renders.
//
// It is driven over the process's own streams, where nothing is kept back, and
// it is driven the way the cache inventory's parity assertion is driven — the
// projection is handed a cascade and every fact that cascade holds is required
// on the wire, rather than an answer being read off whatever the machine
// happens to hold.
//
// 🔴 The bound on what it proves, stated because the clause it discharges is
// about the command: the expectation is the per-value provenance call, which is
// the ONE call both surfaces read. The command's provenance view prints that
// call's three fields — the setting, its value and its originating layer —
// unchanged and adds nothing, so a tool carrying all three carries what that
// view renders. What this does NOT do is compare bytes against the renderer
// itself, which lives in the command package and cannot be imported here; the
// one-fact property rests on both surfaces reading one call, which is what the
// row below asserts the tool does.
func TestTheConfigurationReadingCarriesEveryFactTheProvenanceCallProduced(t *testing.T) {
	root := listenerRootNaming(t, theSecondLocationYAML+
		"signing:\n  key_path: "+theConfiguredKeyPath+"\n")

	rw, err := workspaceconfig.Resolve(root, root)
	if err != nil {
		t.Fatalf("resolve the fixture workspace: %v", err)
	}
	origins, err := rw.ValueOrigins()
	if err != nil {
		t.Fatalf("read the fixture workspace's provenance: %v", err)
	}
	layers := rw.ContributingLayers()
	if len(origins) == 0 || len(layers) == 0 {
		t.Fatalf("the fixture produced %d setting(s) and %d layer(s); the comparison would hold vacuously",
			len(origins), len(layers))
	}

	payload := readOverTheProcessStreams(t, root)
	encoded, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal the answer: %v", err)
	}
	t.Logf("config-show payload over the process's own streams:\n%s", encoded)

	for _, complaint := range provenanceParityComplaints(root, layers, origins, encoded) {
		t.Errorf("%s", complaint)
	}
}

// provenanceParityComplaints reads an encoded answer against the provenance call
// it should have been projected from, and returns one complaint per fact the
// answer does not carry.
//
// Assertions are made against the ENCODED payload rather than the struct, so a
// field whose tag is wrong fails here instead of passing a struct comparison and
// reaching a caller under a name it cannot find.
func provenanceParityComplaints(root string, layers []string,
	origins []workspaceconfig.ValueOrigin, encoded []byte) []string {

	var complaints []string
	say := func(format string, args ...any) {
		complaints = append(complaints, fmt.Sprintf(format, args...))
	}

	var wire map[string]any
	if err := json.Unmarshal(encoded, &wire); err != nil {
		return []string{fmt.Sprintf("the payload does not decode: %v (bytes %s)", err, encoded)}
	}

	if got, _ := wire["workspace_root"].(string); got != root {
		say("workspace_root is %q, and the answer was computed over %q", got, root)
	}
	if withheld, present := wire["host_locations_withheld"].(bool); !present {
		say("the payload carries no host_locations_withheld key, so a caller cannot tell whether it is " +
			"reading a whole answer")
	} else if withheld {
		say("the answer at the keyboard says it keeps host locations back")
	}

	wireLayers, present := wire["layers"].([]any)
	switch {
	case !present:
		say("the payload names no contributing layer at all, and the provenance call reports %d", len(layers))
	case len(wireLayers) != len(layers):
		say("the payload names %d contributing layer(s) and the provenance call reports %d: %v",
			len(wireLayers), len(layers), layers)
	default:
		for i, want := range layers {
			entry, _ := wireLayers[i].(map[string]any)
			if got, _ := entry["name"].(string); got != want {
				say("layers[%d] is %q, and the provenance call reports %q", i, got, want)
			}
			if _, marked := entry["withheld"]; marked {
				say("layers[%d] is marked as kept back at the keyboard", i)
			}
		}
	}

	wireSettings, present := wire["settings"].([]any)
	if !present || len(wireSettings) != len(origins) {
		say("the payload carries %d setting(s) and the provenance call attributed %d",
			len(wireSettings), len(origins))
		return complaints
	}
	for i, want := range origins {
		entry, _ := wireSettings[i].(map[string]any)
		if got, _ := entry["field"].(string); got != want.Field {
			say("settings[%d].field is %q, want %q", i, got, want.Field)
		}
		got, carried := entry["value"].(string)
		if !carried {
			say("settings[%d] (%s) carries no value; the provenance call rendered %q", i, want.Field, want.Value)
		} else if got != want.Value {
			say("settings[%d] (%s) is %q, and the provenance call rendered %q", i, want.Field, got, want.Value)
		}
		source, carried := entry["source"].(string)
		if !carried {
			say("settings[%d] (%s) names no originating layer; the provenance call attributed it to %q",
				i, want.Field, want.Source)
		} else if source != want.Source {
			say("settings[%d] (%s) came from %q, and the provenance call attributed it to %q",
				i, want.Field, source, want.Source)
		}
		if _, marked := entry["value_withheld"]; marked {
			say("settings[%d] (%s) is marked as having its value kept back at the keyboard", i, want.Field)
		}
		if _, marked := entry["source_withheld"]; marked {
			say("settings[%d] (%s) is marked as having its layer kept back at the keyboard", i, want.Field)
		}
	}

	return complaints
}

// TestTheParityReadingRejectsADroppedFact is the negative control under the
// parity reading above.
func TestTheParityReadingRejectsADroppedFact(t *testing.T) {
	root := listenerRootNaming(t, "signing:\n  key_path: "+theConfiguredKeyPath+"\n")
	rw, err := workspaceconfig.Resolve(root, root)
	if err != nil {
		t.Fatalf("resolve the fixture workspace: %v", err)
	}
	origins, err := rw.ValueOrigins()
	if err != nil {
		t.Fatalf("read the fixture workspace's provenance: %v", err)
	}
	layers := rw.ContributingLayers()

	undamaged := configShowPayload(root, layers, origins, false)
	encoded, err := json.Marshal(undamaged)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if got := provenanceParityComplaints(root, layers, origins, encoded); len(got) != 0 {
		t.Fatalf("the undamaged payload already draws complaints, so nothing below is attributable: %v", got)
	}

	for _, tc := range []struct {
		name  string
		drop  func(p *configShowResult)
		names string
	}{
		{
			name:  "a setting's originating layer",
			drop:  func(p *configShowResult) { p.Settings[0].Source = nil },
			names: "names no originating layer",
		},
		{
			name:  "a setting's value",
			drop:  func(p *configShowResult) { p.Settings[0].Value = nil },
			names: "carries no value",
		},
		{
			name:  "the contributing layers",
			drop:  func(p *configShowResult) { p.Layers = nil },
			names: "contributing layer",
		},
		{
			name:  "the workspace the answer was computed over",
			drop:  func(p *configShowResult) { p.WorkspaceRoot = nil },
			names: "workspace_root",
		},
		{
			name:  "the statement of whether anything was kept back",
			drop:  func(p *configShowResult) { p.HostLocationsWithheld = true },
			names: "keeps host locations back",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			damaged := cloneConfigShowResult(undamaged)
			tc.drop(&damaged)
			bytes, err := json.Marshal(damaged)
			if err != nil {
				t.Fatalf("marshal the damaged payload: %v", err)
			}
			complaints := provenanceParityComplaints(root, layers, origins, bytes)
			if len(complaints) == 0 {
				t.Fatalf("dropping %s drew no complaint, so this reading would not notice it: %s", tc.name, bytes)
			}
			named := false
			for _, complaint := range complaints {
				if strings.Contains(complaint, tc.names) {
					named = true
					break
				}
			}
			if !named {
				t.Errorf("dropping %s complained without naming %q: %v", tc.name, tc.names, complaints)
			}
		})
	}
}

// TestEverySettingDeclaredAsNamingAHostLocationIsASettingThatExists is the
// needle under the declared set.
//
// That set is keyed by a setting's dotted name, and nothing makes the compiler
// check one. A setting renamed in the configuration package would leave a stale
// entry here matching nothing — and the symptom would be a location quietly
// reaching a remote caller, with every other entry still working, which is a
// wrong answer that looks exactly like a right one.
func TestEverySettingDeclaredAsNamingAHostLocationIsASettingThatExists(t *testing.T) {
	root := listenerRootNaming(t, "")
	rw, err := workspaceconfig.Resolve(root, root)
	if err != nil {
		t.Fatalf("resolve the fixture workspace: %v", err)
	}
	origins, err := rw.ValueOrigins()
	if err != nil {
		t.Fatalf("read the fixture workspace's provenance: %v", err)
	}
	if len(origins) == 0 {
		t.Fatal("the provenance call attributed no setting, so this census measured nothing")
	}

	reported := map[string]bool{}
	for _, o := range origins {
		reported[o.Field] = true
	}

	declared := make([]string, 0, len(settingsNamingAHostLocation))
	for name := range settingsNamingAHostLocation {
		declared = append(declared, name)
	}
	sort.Strings(declared)

	for _, name := range declared {
		if !reported[name] {
			t.Errorf("this reading declares %q as a setting whose value names a place on the host, and the "+
				"configuration reports no such setting. Either it was renamed — rename it here too — or the "+
				"entry is a leftover keeping nothing back", name)
		}
		if strings.TrimSpace(settingsNamingAHostLocation[name]) == "" {
			t.Errorf("%q is declared as naming a place on the host with no reason written beside it", name)
		}
	}
	t.Logf("%d setting(s) declared as naming a place on the host, of %d the configuration reports: %v",
		len(declared), len(origins), declared)
}

// TestTheConfigurationReadingIsTheSameSizeWhateverADeploymentConfigures is the
// measurement behind this tool taking no window.
//
// A bounded listing exists for an answer whose size a caller cannot know before
// it arrives. This answer's size is the set of settings this binary accounts
// for: every resolution reports every one of them, so two workspaces configured
// quite differently produce the same number of rows. That is why no page, limit
// or offset is declared here, and it is asserted rather than claimed — a change
// that made the reading report only what an operator set would make it grow
// with the configuration, and would need this decision taken again.
func TestTheConfigurationReadingIsTheSameSizeWhateverADeploymentConfigures(t *testing.T) {
	bare := readOverTheProcessStreams(t, listenerRootNaming(t, ""))
	loaded := readOverTheProcessStreams(t, listenerRootNaming(t,
		theSecondLocationYAML+"auto_pull: true\nsigning:\n  key_path: "+theConfiguredKeyPath+
			"\n  required: true\nplugins:\n  linter:\n    - uri: /held/lint.wasm\n"))

	if len(bare.Settings) != len(loaded.Settings) {
		t.Fatalf("a workspace configuring almost nothing reports %d setting(s) and one configuring a good "+
			"deal reports %d; the answer grows with the configuration, so the decision to declare no page "+
			"needs taking again", len(bare.Settings), len(loaded.Settings))
	}
	if len(bare.Settings) == 0 {
		t.Fatal("both answers carry no settings, so this comparison measured nothing")
	}
	for i := range bare.Settings {
		if bare.Settings[i].Field != loaded.Settings[i].Field {
			t.Errorf("setting %d is %q in one workspace and %q in the other; the rows are not the fixed set "+
				"this decision rests on", i, bare.Settings[i].Field, loaded.Settings[i].Field)
		}
	}
	t.Logf("both workspaces report the same %d setting(s)", len(bare.Settings))
}
