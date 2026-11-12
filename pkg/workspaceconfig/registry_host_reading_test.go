// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

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

// registryLocationRows are default-registry spellings carrying a repository path,
// each with whether the load accepts it and, for a refusal, the location the
// refusal must name — empty where the value names no location to write. A
// registries[].host carries no path, so every one of them is refused there,
// naming the host alone.
var registryLocationRows = []struct {
	value    string
	accepted bool
	write    string
}{
	{value: "ghcr.io/acme", accepted: true},
	{value: "harbor.example.com/platform", accepted: true},
	{value: "127.0.0.1:5000/team", accepted: true},
	{value: "localhost/acme/catalog", accepted: true},
	{value: "[::1]:5000/acme", accepted: true},
	{value: "registry.example.com/my-team_1/sub.name__x", accepted: true},
	{value: "GHCR.io/acme", write: "ghcr.io/acme"},
	{value: "https://ghcr.io/acme", write: "ghcr.io/acme"},
	{value: "user:secret@ghcr.io/acme", write: "ghcr.io/acme"},
	{value: "ghcr.io:443/acme", write: "ghcr.io/acme"},
	{value: "ghcr.io/Acme"},
	{value: "ghcr.io/acme:1.0.0"},
	{value: "ghcr.io/acme/"},
	{value: "ghcr.io//acme"},
	{value: "myregistry/acme"},
	{value: "core/web-api"},
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
// normaliser leaves unchanged, or such a host followed by a repository path, and
// to refuse the rest as a configuration error that names the key, the layer,
// and, where the value names a host at all, the spelling to write. The location
// rows drive the path-bearing shapes the same way, and hold registries[].host to
// refusing every one of them, since an entry's host carries no path. The alias
// rows then drive a default that names an entry: accepted as that entry's host
// where one entry carries the name, and refused where none or two do.
func TestTheRegistryHostPatternAgreesWithThePredicate(t *testing.T) {
	t.Parallel()
	for _, s := range registryHostCorpus {
		isHost := registryhost.IsHost(s)
		canonical := isHost && registryhost.Normalize(s) == s
		normalised := registryhost.Normalize(s)
		spelling := ""
		if registryhost.IsHost(normalised) {
			spelling = normalised
		}
		defaultAccepted := s == "" || canonical || canonicalLocation(s)

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
		checkDefaultLayers(t, s, defaultAccepted, spelling, defaultPath)
		hostPath := writeFixture(t, string(hostDoc))
		checkLayer(t, "registries[].host in a file", s, canonical, spelling, hostPath,
			func() error { _, err := load(hostPath); return err })
	}

	for _, row := range registryLocationRows {
		defaultDoc := marshalConfig(t, map[string]any{"workspace": map[string]any{}, "default_registry": row.value})
		if _, err := envelope.ValidateBytes(envelope.KindAggregaConfig, defaultDoc, "location"); err != nil {
			t.Errorf("default_registry %q: the schema alone refuses it, but a default may carry a repository path: %v", row.value, err)
		}
		if canonicalLocation(row.value) != row.accepted {
			t.Errorf("default_registry %q: the row says accepted=%v, but the host and path readings say %v", row.value, row.accepted, !row.accepted)
		}
		checkDefaultLayers(t, row.value, row.accepted, row.write, writeFixture(t, string(defaultDoc)))

		hostDoc := marshalConfig(t, map[string]any{"workspace": map[string]any{},
			"registries": []any{map[string]any{"host": row.value}}})
		if _, err := envelope.ValidateBytes(envelope.KindAggregaConfig, hostDoc, "location"); err == nil {
			t.Errorf("registries[].host %q: the schema's pattern admits a repository path, which an entry's host never carries", row.value)
		}
		hostSpelling := ""
		if h := registryhost.Normalize(row.value); registryhost.IsHost(h) {
			hostSpelling = h
		}
		hostPath := writeFixture(t, string(hostDoc))
		checkLayer(t, "registries[].host in a file", row.value, false, hostSpelling, hostPath,
			func() error { _, err := load(hostPath); return err })
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
			{label: "through AGGREGA_DEFAULT_REGISTRY", path: writeFixture(t, string(entriesOnly)),
				names: "the AGGREGA_DEFAULT_REGISTRY environment variable: ", env: map[string]string{"AGGREGA_DEFAULT_REGISTRY": row.value}},
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

// checkDefaultLayers drives one default-registry spelling through both layers
// that carry it — the configuration file at path, resolved whole, and the
// environment variable over a file that names no default — requiring each to
// accept it exactly when accepted says, as the value written, and otherwise to
// refuse it as checkLayer requires, naming spelling where it is not empty.
func checkDefaultLayers(t *testing.T, s string, accepted bool, spelling, path string) {
	t.Helper()
	checkLayer(t, "default_registry in a file", s, accepted, spelling, path, func() error {
		cfg, err := resolveFixture(path, nil)
		if err == nil && cfg.DefaultRegistry != s {
			t.Errorf("default_registry %q was accepted but the resolved default is %q", s, cfg.DefaultRegistry)
		}
		return err
	})
	// An unset or empty variable falls through by design, so only a set value is a
	// value this layer carries.
	if s == "" {
		return
	}
	envPath := writeFixture(t, "workspace: {}\n")
	checkLayer(t, "AGGREGA_DEFAULT_REGISTRY", s, accepted, spelling, "the AGGREGA_DEFAULT_REGISTRY environment variable: ", func() error {
		cfg, err := resolveFixture(envPath, map[string]string{"AGGREGA_DEFAULT_REGISTRY": s})
		if err == nil && cfg.DefaultRegistry != s {
			t.Errorf("AGGREGA_DEFAULT_REGISTRY %q was accepted but the resolved default is %q", s, cfg.DefaultRegistry)
		}
		return err
	})
}

// canonicalLocation reports whether s is a registry host in the spelling its
// trust policy is kept under followed by "/" and a repository path — the shape a
// default registry may carry beyond a host alone. It is stated here from the
// host predicate, the normaliser and the path grammar rather than by asking the
// load, so the load is compared against it rather than with itself.
func canonicalLocation(s string) bool {
	host, path, ok := strings.Cut(s, "/")
	return ok && registryhost.IsHost(host) && registryhost.Normalize(host) == host && registryhost.IsRepositoryPath(path)
}

// checkLayer asserts one layer's verdict on one spelling: accepted exactly when
// want says, and otherwise refused as a configuration error naming the key, the
// layer that carried the value — names, a file's path or the variable — and,
// where spelling is not empty, the spelling to write.
func checkLayer(t *testing.T, layer, s string, want bool, spelling, names string, run func() error) {
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
	if spelling != "" && !strings.Contains(err.Error(), `write "`+spelling+`"`) {
		t.Errorf("%s %q: the refusal does not name the spelling to write, %q: %v", layer, s, spelling, err)
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
