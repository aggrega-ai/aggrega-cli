// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package workspaceconfig

import (
	"errors"
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

// TestTheRegistryHostPatternAgreesWithThePredicate holds the configuration
// schema's two host patterns to the one predicate, registryhost.IsHost, and holds
// the configuration load to the one reading built on it.
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
// It then drives the same row through the whole file load and through the
// environment overlay, which the schema never sees, and requires each to accept
// exactly the spellings the predicate accepts and the normaliser leaves unchanged
// — refusing the rest as a configuration error that names the key and, where the
// value names a host at all, the spelling to write.
func TestTheRegistryHostPatternAgreesWithThePredicate(t *testing.T) {
	t.Parallel()
	for _, s := range registryHostCorpus {
		isHost := registryhost.IsHost(s)
		canonical := isHost && registryhost.Normalize(s) == s
		normalised := registryhost.Normalize(s)

		// The schema alone, on both keys. An empty default names no default
		// registry, which the default's pattern admits and the host's does not.
		defaultDoc := marshalConfig(t, map[string]any{"workspace": map[string]any{}, "default_registry": s})
		_, derr := envelope.ValidateBytes(envelope.KindAggregaConfig, defaultDoc, "corpus")
		if wantDefault := s == "" || isHost; (derr == nil) != wantDefault {
			t.Errorf("default_registry %q: the schema's pattern accepts=%v but the predicate accepts=%v: %v",
				s, derr == nil, wantDefault, derr)
		}
		hostDoc := marshalConfig(t, map[string]any{"workspace": map[string]any{},
			"registries": []any{map[string]any{"host": s}}})
		_, herr := envelope.ValidateBytes(envelope.KindAggregaConfig, hostDoc, "corpus")
		if (herr == nil) != isHost {
			t.Errorf("registries[].host %q: the schema's pattern accepts=%v but the predicate accepts=%v: %v",
				s, herr == nil, isHost, herr)
		}

		// The file layer, through the whole load.
		checkLayer(t, "default_registry in a file", s, s == "" || canonical, normalised,
			func() error { _, err := load(writeFixture(t, string(defaultDoc))); return err })
		checkLayer(t, "registries[].host in a file", s, canonical, normalised,
			func() error { _, err := load(writeFixture(t, string(hostDoc))); return err })

		// The environment layer. An unset or empty variable falls through by design,
		// so only a set value is a value this layer carries.
		if s != "" {
			checkLayer(t, "$AGGREGA_DEFAULT_REGISTRY", s, canonical, normalised, func() error {
				cfg := Default()
				_, err := applyEnvOverlay(&cfg, func(k string) string {
					if k == "AGGREGA_DEFAULT_REGISTRY" {
						return s
					}
					return ""
				})
				if err == nil && cfg.DefaultRegistry != s {
					t.Errorf("$AGGREGA_DEFAULT_REGISTRY %q was accepted but the resolved default is %q", s, cfg.DefaultRegistry)
				}
				return err
			})
		}
	}
}

// checkLayer asserts one layer's verdict on one spelling: accepted exactly when
// want says, and otherwise refused as a configuration error naming the key and,
// where the value names a host, the spelling to write.
func checkLayer(t *testing.T, layer, s string, want bool, normalised string, run func() error) {
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
	if registryhost.IsHost(normalised) && !strings.Contains(err.Error(), `write "`+normalised+`"`) {
		t.Errorf("%s %q: the refusal does not name the spelling to write, %q: %v", layer, s, normalised, err)
	}
	if strings.Contains(s, "secret") && strings.Contains(err.Error(), "secret") {
		t.Errorf("%s %q: the refusal repeats the credentials the value carries: %v", layer, s, err)
	}
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
