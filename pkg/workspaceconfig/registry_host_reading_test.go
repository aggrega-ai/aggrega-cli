// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package workspaceconfig

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/aggrega-ai/aggrega-cli/pkg/registryhost"
	"github.com/aggrega-ai/aggrega-cli/pkg/validator/envelope"
)

// registryHostCorpus is the one corpus of spellings the registry-host reading is
// held to. It carries every shape a registry host takes, the spellings that used
// to load and then reach the trust lookups and the verification filter in forms
// they read differently — capitals, a default port, a scheme, a path, credentials,
// a single label, whitespace and braces — and the malformed literals that sit one
// character away from a valid shape, where a pattern and a predicate are most
// likely to part company.
var registryHostCorpus = []string{
	"hub.aggrega.ai",
	"registry.example.com",
	"registry.example.com:8443",
	"hub.aggrega.ai:443",
	"localhost",
	"localhost:5000",
	"localhost:443",
	"registry:5000",
	"127.0.0.1",
	"127.0.0.1:5000",
	"[::1]",
	"[::1]:5000",
	"[0:0:0:0:0:0:0:1]:5000",
	"foo.localhost",
	"xn--bcher-kva.example",
	"10.example.com",
	"a.b:65535",
	"",
	"HUB.AGGREGA.AI",
	"Registry.Example.com",
	"LOCALHOST",
	"https://hub.aggrega.ai",
	"https://registry.example.com",
	"registry.example.com/charts",
	"user:secret@hub.aggrega.ai",
	"myregistry",
	"hub aggrega.ai",
	"hub.aggrega.ai\n",
	"{\n  not a host\n}",
	"{\n}",
	"hub.aggrega.ai.",
	"hub..aggrega.ai",
	"-hub.aggrega.ai",
	"999.1.1.1",
	"256.1.1.1",
	"01.2.3.4",
	"1.2.3",
	"a.b:0",
	"a.b:65536",
	"a.b:0443",
	"a.b:",
	":5000",
	"web-api:1.0.0",
	"123:5000",
	"::1",
	"[::1",
	"[::FFFF]",
	"[::ffff:1.2.3.4]",
	"[fe80::1%eth0]",
	"[1::2::3]",
	"my_registry.example.com",
}

// registryAliasRows are the default-registry spellings that name a registries
// entry rather than a host, each beside the entries it is read against and what
// it must resolve to — an empty host where the load refuses it. They are
// exactly the rows the corpus above cannot carry, because whether a default is
// accepted there turns on the entries rather than on the spelling.
var registryAliasRows = []struct {
	name      string
	value     string
	entries   []any
	host      string
	ambiguous bool
}{
	{name: "an alias naming one entry", value: "local",
		entries: []any{map[string]any{"name": "local", "host": "127.0.0.1:5000"}},
		host:    "127.0.0.1:5000"},
	{name: "an alias spelled like a host, naming one entry", value: "hub.aggrega.ai",
		entries: []any{map[string]any{"name": "hub.aggrega.ai", "host": "mirror.example.com:8443"}},
		host:    "mirror.example.com:8443"},
	{name: "an alias spelled in capitals, naming one entry", value: "Private Registry",
		entries: []any{map[string]any{"name": "Private Registry", "host": "private.example.com"}},
		host:    "private.example.com"},
	{name: "a name no entry carries", value: "nowhere",
		entries: []any{map[string]any{"name": "local", "host": "127.0.0.1:5000"}}},
	{name: "an alias naming two entries", value: "shared", ambiguous: true,
		entries: []any{
			map[string]any{"name": "shared", "host": "a.example.com"},
			map[string]any{"name": "shared", "host": "b.example.com"},
		}},
}

// TestTheRegistryHostPatternAgreesWithThePredicate holds the configuration
// schema's host pattern to the one predicate, registryhost.IsHost, and holds the
// configuration load to the one reading built on it.
//
// The pattern and the predicate are two independent statements of one shape: the
// pattern is what an editor or any validator reading the schema alone refuses, and
// the predicate is what the verification filter and every load layer consult. The
// comparison is written across the two rather than generating one from the other,
// because a pattern emitted from the predicate could not disagree with it, and
// disagreement is the entire signal. So each row is driven through the schema gate
// on its own — the document-envelope validation the loader runs — and compared
// with the predicate: loosening the pattern by one character class makes this test
// fail on the row that class admits.
//
// default_registry carries no pattern at all, because a default may also be the
// name of a registries entry and a name may be any string; so the schema alone
// admits every spelling there, and this test requires that it does — a pattern
// added back to the key would refuse a name the load accepts. What judges the
// default is the resolution, and the test drives each row through it on both of
// its layers — the file and the environment overlay, which the schema never sees
// — requiring each to accept exactly the spellings the predicate accepts and the
// normaliser leaves unchanged, and to refuse the rest as a configuration error
// that names the key, the layer, and, where the value names a host at all, the
// spelling to write. The alias rows then drive a default that names an entry:
// accepted as that entry's host where one entry carries the name, and refused
// where none or two do.
func TestTheRegistryHostPatternAgreesWithThePredicate(t *testing.T) {
	t.Parallel()
	for _, s := range registryHostCorpus {
		isHost := registryhost.IsHost(s)
		canonical := isHost && registryhost.Normalize(s) == s
		normalised := registryhost.Normalize(s)

		// The schema alone, on both keys.
		defaultDoc := marshalConfig(t, map[string]any{"workspace": map[string]any{}, "default_registry": s})
		if _, err := envelope.ValidateBytes(envelope.KindAggregaConfig, defaultDoc, "corpus"); err != nil {
			t.Errorf("default_registry %q: the schema alone refuses it, but a default may be any entry's name: %v", s, err)
		}
		hostDoc := marshalConfig(t, map[string]any{"workspace": map[string]any{},
			"registries": []any{map[string]any{"host": s}}})
		_, herr := envelope.ValidateBytes(envelope.KindAggregaConfig, hostDoc, "corpus")
		if (herr == nil) != isHost {
			t.Errorf("registries[].host %q: the schema's pattern accepts=%v but the predicate accepts=%v: %v",
				s, herr == nil, isHost, herr)
		}

		// The file layer, through the whole resolution for the default and through
		// the file load for an entry's host.
		defaultPath := writeFixture(t, string(defaultDoc))
		checkLayer(t, "default_registry in a file", s, s == "" || canonical, normalised, defaultPath, func() error {
			cfg, err := resolveFixture(defaultPath, nil)
			if err == nil && cfg.DefaultRegistry != s {
				t.Errorf("default_registry %q was accepted but the resolved default is %q", s, cfg.DefaultRegistry)
			}
			return err
		})
		hostPath := writeFixture(t, string(hostDoc))
		checkLayer(t, "registries[].host in a file", s, canonical, normalised, hostPath,
			func() error { _, err := load(hostPath); return err })

		// The environment layer. An unset or empty variable falls through by design,
		// so only a set value is a value this layer carries.
		if s != "" {
			envPath := writeFixture(t, "workspace: {}\n")
			checkLayer(t, "$AGGREGA_DEFAULT_REGISTRY", s, canonical, normalised, "$AGGREGA_DEFAULT_REGISTRY", func() error {
				cfg, err := resolveFixture(envPath, map[string]string{"AGGREGA_DEFAULT_REGISTRY": s})
				if err == nil && cfg.DefaultRegistry != s {
					t.Errorf("$AGGREGA_DEFAULT_REGISTRY %q was accepted but the resolved default is %q", s, cfg.DefaultRegistry)
				}
				return err
			})
		}
	}

	for _, row := range registryAliasRows {
		doc := marshalConfig(t, map[string]any{"workspace": map[string]any{},
			"default_registry": row.value, "registries": row.entries})
		if _, err := envelope.ValidateBytes(envelope.KindAggregaConfig, doc, "alias"); err != nil {
			t.Errorf("%s: the schema alone refuses the configuration: %v", row.name, err)
		}
		entriesOnly := marshalConfig(t, map[string]any{"workspace": map[string]any{}, "registries": row.entries})
		layers := []struct {
			label, path, names string
			env                map[string]string
		}{
			{label: "in a file", path: writeFixture(t, string(doc))},
			{label: "through $AGGREGA_DEFAULT_REGISTRY", path: writeFixture(t, string(entriesOnly)),
				names: "$AGGREGA_DEFAULT_REGISTRY", env: map[string]string{"AGGREGA_DEFAULT_REGISTRY": row.value}},
		}
		for _, layer := range layers {
			names := layer.names
			if names == "" {
				names = layer.path
			}
			cfg, err := resolveFixture(layer.path, layer.env)
			switch {
			case row.host != "":
				if err != nil {
					t.Errorf("%s %s: refused, want it resolved to %q: %v", row.name, layer.label, row.host, err)
				} else if cfg.DefaultRegistry != row.host {
					t.Errorf("%s %s: the resolved default is %q, want the entry's host %q", row.name, layer.label, cfg.DefaultRegistry, row.host)
				}
			case row.ambiguous:
				if !errors.Is(err, ErrAmbiguousRegistryAlias()) {
					t.Errorf("%s %s: got %v, want the default refused as an ambiguous alias", row.name, layer.label, err)
				} else if !strings.Contains(err.Error(), names) || !strings.Contains(err.Error(), "default_registry") {
					t.Errorf("%s %s: the refusal does not name the key and the layer %q: %v", row.name, layer.label, names, err)
				}
			default:
				if !errors.Is(err, ErrConfigInvalid()) {
					t.Errorf("%s %s: got %v, want the default refused as a configuration error", row.name, layer.label, err)
				} else if !strings.Contains(err.Error(), names) || !strings.Contains(err.Error(), "the name of an entry under registries") {
					t.Errorf("%s %s: the refusal does not name the layer %q and say a default may name an entry: %v",
						row.name, layer.label, names, err)
				}
			}
		}
	}
}

// checkLayer asserts one layer's verdict on one spelling: accepted exactly when
// want says, and otherwise refused as a configuration error naming the key, the
// layer that carried the value — names, a file's path or the variable — and,
// where the value names a host, the spelling to write.
func checkLayer(t *testing.T, layer, s string, want bool, normalised, names string, run func() error) {
	t.Helper()
	err := run()
	if (err == nil) != want {
		t.Errorf("%s %q: accepted=%v, want accepted=%v: %v", layer, s, err == nil, want, err)
		return
	}
	if err == nil {
		return
	}
	if !errors.Is(err, ErrConfigInvalid()) {
		t.Errorf("%s %q: the refusal is not a configuration error: %v", layer, s, err)
	}
	if !strings.Contains(err.Error(), "is not") {
		t.Errorf("%s %q: the refusal does not say what is wrong: %v", layer, s, err)
	}
	if !strings.Contains(err.Error(), names) {
		t.Errorf("%s %q: the refusal does not name the layer that carried it, %q: %v", layer, s, names, err)
	}
	if registryhost.IsHost(normalised) && !strings.Contains(err.Error(), `write "`+normalised+`"`) {
		t.Errorf("%s %q: the refusal does not name the spelling to write, %q: %v", layer, s, normalised, err)
	}
	if strings.Contains(s, "secret") && strings.Contains(err.Error(), "secret") {
		t.Errorf("%s %q: the refusal repeats the credentials the value carries: %v", layer, s, err)
	}
}

// resolveFixture resolves the workspace whose configuration file is path, with
// an environment holding only env: no ambient variable and no global
// configuration file reaches the resolution.
func resolveFixture(path string, env map[string]string) (Config, error) {
	rw, err := Resolve(filepath.Dir(path), "", fakeEnv(env))
	if err != nil {
		return Config{}, err
	}
	return rw.Config(), nil
}

// marshalConfig renders a configuration document, quoting each value the way
// YAML needs so an arbitrary spelling reaches the loader as the string it is.
func marshalConfig(t *testing.T, doc map[string]any) []byte {
	t.Helper()
	out, err := yaml.Marshal(doc)
	if err != nil {
		t.Fatalf("marshal config: %v", err)
	}
	return out
}
