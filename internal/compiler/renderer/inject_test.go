// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// omStrInject builds an OrderedMap[string] from key-value pairs.
func omStrInject(pairs ...string) types.OrderedMap[string] {
	m := types.OrderedMap[string]{}
	for i := 0; i+1 < len(pairs); i += 2 {
		m.Set(pairs[i], pairs[i+1])
	}
	return m
}

func TestBuildInjectPipe_NilRaw(t *testing.T) {
	t.Parallel()
	inject, raw := BuildInjectPipe(nil)
	if raw != nil {
		t.Error("expected nil injectRaw")
	}
	// OrderedMap zero value is safe; just ensure Len() == 0 for empty pipes.
	if inject.Env.Len() != 0 || inject.File.Len() != 0 || inject.Arg.Len() != 0 || inject.All.Len() != 0 {
		t.Error("expected empty inject pipe")
	}
}

func TestBuildInjectPipe_WithValues(t *testing.T) {
	t.Parallel()
	rawBlock := &types.InjectBlock{
		Env:  map[string]any{"DB_URL": "host"},
		File: map[string]any{"config": "/etc/app.yaml"},
	}
	inject, raw := BuildInjectPipe(rawBlock)
	if raw == nil {
		t.Error("expected non-nil injectRaw")
	}
	if v, _ := inject.Env.Get("DB_URL"); v != "host" {
		t.Errorf("env: %v", inject.Env.Keys())
	}
	if v, _ := inject.File.Get("config"); v != "/etc/app.yaml" {
		t.Errorf("file: %v", inject.File.Keys())
	}
	// .all should contain both
	if v, _ := inject.All.Get("DB_URL"); v != "host" {
		t.Errorf("all.DB_URL: %v", inject.All.Keys())
	}
	if v, _ := inject.All.Get("config"); v != "/etc/app.yaml" {
		t.Errorf("all.config: %v", inject.All.Keys())
	}
}

func TestBuildInjectPipe_GuaranteedKeys(t *testing.T) {
	t.Parallel()
	// Even with only env set, file and arg must be empty but usable
	rawBlock := &types.InjectBlock{
		Env: map[string]any{"X": "1"},
	}
	inject, _ := BuildInjectPipe(rawBlock)
	if inject.File.Len() != 0 {
		t.Error("file should be empty")
	}
	if inject.Arg.Len() != 0 {
		t.Error("arg should be empty")
	}
}

func TestRehydrateInjectFromProvides(t *testing.T) {
	t.Parallel()

	inject := types.InjectPipe{
		Env:  types.OrderedMap[string]{},
		File: types.OrderedMap[string]{},
		Arg:  types.OrderedMap[string]{},
		All:  types.OrderedMap[string]{},
	}

	envInject := types.OrderedMap[string]{}
	envInject.Set("DB_URL", "host")
	envInject.Set("DB_PORT", "service_port")
	edgeInject := types.OrderedMap[types.OrderedMap[string]]{}
	edgeInject.Set("env", envInject)
	edges := []types.IREdge{
		{Source: "api", Target: "db", Inject: edgeInject},
	}

	dbProv := types.OrderedMap[string]{}
	dbProv.Set("host", "db.svc.cluster.local")
	dbProv.Set("service_port", "5432")
	allProvides := NodeProvides{"db": dbProv}

	RehydrateInjectFromProvides(&inject, edges, "api", allProvides, nil, RecipeLookup{}, nil)

	if v, _ := inject.Env.Get("DB_URL"); v != "db.svc.cluster.local" {
		t.Errorf("DB_URL: %q", v)
	}
	if v, _ := inject.Env.Get("DB_PORT"); v != "5432" {
		t.Errorf("DB_PORT: %q", v)
	}
	if v, _ := inject.All.Get("DB_URL"); v != "db.svc.cluster.local" {
		t.Errorf("all.DB_URL: %q", v)
	}
}

func TestBuildInjectAll_Deterministic(t *testing.T) {
	t.Parallel()

	inject := types.InjectPipe{
		Env:  omStrInject("Z_VAR", "z", "A_VAR", "a"),
		File: omStrInject("B_FILE", "b"),
		Arg:  omStrInject("C_ARG", "c"),
	}

	all := buildInjectAll(inject)
	if all.Len() != 4 {
		t.Fatalf("expected 4 keys, got %d", all.Len())
	}
	z, _ := all.Get("Z_VAR")
	a, _ := all.Get("A_VAR")
	b, _ := all.Get("B_FILE")
	c, _ := all.Get("C_ARG")
	if z != "z" || a != "a" || b != "b" || c != "c" {
		t.Errorf("all: %v", all.Keys())
	}
}

func TestBuildInjectAll_CustomMechanisms(t *testing.T) {
	t.Parallel()

	inject := types.InjectPipe{
		Env:  omStrInject("ENV_KEY", "e"),
		File: omStrInject("FILE_KEY", "f"),
		Arg:  omStrInject("ARG_KEY", "a"),
	}
	inject.Additional.Set("vault", omStrInject("VAULT_KEY", "v"))
	inject.Additional.Set("ssm", omStrInject("SSM_KEY", "s"))
	inject.Additional.Set("azurekv", omStrInject("AZ_KEY", "z"))

	all := buildInjectAll(inject)
	if all.Len() != 6 {
		t.Fatalf("expected 6 keys (3 std + 3 custom), got %d: %v", all.Len(), all.Keys())
	}
	for k, want := range map[string]string{
		"ENV_KEY": "e", "FILE_KEY": "f", "ARG_KEY": "a",
		"VAULT_KEY": "v", "SSM_KEY": "s", "AZ_KEY": "z",
	} {
		got, _ := all.Get(k)
		if got != want {
			t.Errorf("all[%q] = %q, want %q", k, got, want)
		}
	}
}

// TestNormalizeInjectMechanism_OrderedMapShape_PreservesAuthorOrder pins
// the typed-arm fix at normalizeInjectMechanism. When the InjectBlock
// field arrives as *types.OrderedMap[any] (the round-trip-fidelity decode
// shape), the function walks via .All() and the resulting OrderedMap[string]
// preserves the input's authoring sequence rather than the alphabetical-
// sort fallback applied to the plain map[string]X arms (where author
// order is already lost at decode).
func TestNormalizeInjectMechanism_OrderedMapShape_PreservesAuthorOrder(t *testing.T) {
	t.Parallel()
	raw := types.NewOrderedMap[any]()
	raw.Set("zulu", "z-value")
	raw.Set("alpha", "a-value")
	raw.Set("middle", "m-value")

	out := normalizeInjectMechanism(raw)
	if out.Len() != 3 {
		t.Fatalf("expected 3 entries, got %d", out.Len())
	}
	keys := out.Keys()
	if keys[0] != "zulu" || keys[1] != "alpha" || keys[2] != "middle" {
		t.Errorf("output key order = %v, want [zulu alpha middle] (author order)", keys)
	}
	for k, want := range map[string]string{"zulu": "z-value", "alpha": "a-value", "middle": "m-value"} {
		got, _ := out.Get(k)
		if got != want {
			t.Errorf("out[%q] = %q, want %q", k, got, want)
		}
	}
}

// TestNormalizeInjectMechanism_VerboseArrayForm pins the schema-verbose-form
// decode arm. Entries decode in source order; value: wins over from: when
// both are present. Entries without `in:` skip.
func TestNormalizeInjectMechanism_VerboseArrayForm(t *testing.T) {
	t.Parallel()

	raw := []any{
		map[string]any{"in": "DB_URL", "from": "connection.url"},
		map[string]any{"in": "PORT", "value": "8080"},
		map[string]any{"in": "BOTH", "from": "ref.path", "value": "literal"},
	}

	out := normalizeInjectMechanism(raw)
	if out.Len() != 3 {
		t.Fatalf("expected 3 entries, got %d", out.Len())
	}
	keys := out.Keys()
	if keys[0] != "DB_URL" || keys[1] != "PORT" || keys[2] != "BOTH" {
		t.Errorf("output key order = %v, want [DB_URL PORT BOTH] (source order)", keys)
	}
	cases := map[string]string{
		"DB_URL": "connection.url",
		"PORT":   "8080",
		"BOTH":   "literal",
	}
	for k, want := range cases {
		got, _ := out.Get(k)
		if got != want {
			t.Errorf("out[%q] = %q, want %q", k, got, want)
		}
	}

	emptyOut := normalizeInjectMechanism([]any{})
	if emptyOut.Len() != 0 {
		t.Errorf("empty []any should produce empty result; got Len=%d", emptyOut.Len())
	}
}

// TestRehydrateInjectFromProvides_CustomMechanism pins that a mechanism an edge
// names outside the three typed ones resolves against the target's provides and
// lands under its own name, reachable both per-mechanism and through the union
// view. Each row fixes one authored shape; the absent-provides row pins that an
// unresolvable reference delivers nothing rather than delivering an empty value.
func TestRehydrateInjectFromProvides_CustomMechanism(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		mechanism     string
		deliveryKey   string
		providesKey   string
		wantDelivered bool
		wantValue     string
	}{
		{
			name:          "custom mechanism resolves from target provides",
			mechanism:     "ssm_param",
			deliveryKey:   "DB_ARN",
			providesKey:   "arn",
			wantDelivered: true,
			wantValue:     "arn:aws:rds:eu-west-1:db-main",
		},
		{
			name:          "custom mechanism referencing an absent provides key delivers nothing",
			mechanism:     "ssm_param",
			deliveryKey:   "DB_ARN",
			providesKey:   "not_provided",
			wantDelivered: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			inject := emptyPipe()
			edgeInject := types.OrderedMap[types.OrderedMap[string]]{}
			edgeInject.Set(tt.mechanism, omStrInject(tt.deliveryKey, tt.providesKey))
			edges := []types.IREdge{{Source: "api", Target: "db", Inject: edgeInject}}

			allProvides := NodeProvides{"db": omStrInject("arn", "arn:aws:rds:eu-west-1:db-main")}

			RehydrateInjectFromProvides(&inject, edges, "api", allProvides, nil, RecipeLookup{}, nil)

			mechMap, ok := inject.Additional.Get(tt.mechanism)
			if !tt.wantDelivered {
				if ok && mechMap.Len() > 0 {
					t.Fatalf("Additional[%q] = %v, want no delivered key", tt.mechanism, mechMap.Keys())
				}
				if _, inAll := inject.All.Get(tt.deliveryKey); inAll {
					t.Errorf("all[%q] present, want absent", tt.deliveryKey)
				}
				return
			}
			if !ok {
				t.Fatalf("Additional has no %q mechanism; keys = %v", tt.mechanism, inject.Additional.Keys())
			}
			if got, _ := mechMap.Get(tt.deliveryKey); got != tt.wantValue {
				t.Errorf("Additional[%q][%q] = %q, want %q", tt.mechanism, tt.deliveryKey, got, tt.wantValue)
			}
			if got, _ := inject.All.Get(tt.deliveryKey); got != tt.wantValue {
				t.Errorf("all[%q] = %q, want %q", tt.deliveryKey, got, tt.wantValue)
			}
			// The three typed views stay clear of a custom mechanism's keys.
			for name, view := range map[string]types.OrderedMap[string]{"env": inject.Env, "file": inject.File, "arg": inject.Arg} {
				if _, present := view.Get(tt.deliveryKey); present {
					t.Errorf("%s view carries %q, want it only under the custom mechanism", name, tt.deliveryKey)
				}
			}
		})
	}
}

// TestRehydrateInjectFromProvides_CustomMechanismOrderIsSorted pins that custom
// mechanisms are delivered in sorted name order whatever order the edge names
// them, so the emission is comparable across runs of the same input.
func TestRehydrateInjectFromProvides_CustomMechanismOrderIsSorted(t *testing.T) {
	t.Parallel()

	inject := emptyPipe()
	edgeInject := types.OrderedMap[types.OrderedMap[string]]{}
	edgeInject.Set("vault", omStrInject("V", "arn"))
	edgeInject.Set("azurekv", omStrInject("A", "arn"))
	edgeInject.Set("ssm_param", omStrInject("S", "arn"))
	edges := []types.IREdge{{Source: "api", Target: "db", Inject: edgeInject}}

	RehydrateInjectFromProvides(&inject, edges, "api",
		NodeProvides{"db": omStrInject("arn", "v")}, nil, RecipeLookup{}, nil)

	got := inject.Additional.Keys()
	want := []string{"azurekv", "ssm_param", "vault"}
	if len(got) != len(want) {
		t.Fatalf("Additional keys = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("Additional keys = %v, want %v", got, want)
		}
	}
}

// TestRehydrateInjectFromProvides_NoCustomMechanismLeavesAdditionalEmpty pins
// that an edge naming only the three typed mechanisms adds nothing to the
// per-mechanism view, which is what keeps the rendered output of every input
// that names no custom mechanism unchanged.
func TestRehydrateInjectFromProvides_NoCustomMechanismLeavesAdditionalEmpty(t *testing.T) {
	t.Parallel()

	inject := emptyPipe()
	edgeInject := types.OrderedMap[types.OrderedMap[string]]{}
	edgeInject.Set("env", omStrInject("DB_URL", "host"))
	edgeInject.Set("file", omStrInject("DB_CERT", "cert"))
	edgeInject.Set("arg", omStrInject("--db", "host"))
	edges := []types.IREdge{{Source: "api", Target: "db", Inject: edgeInject}}

	RehydrateInjectFromProvides(&inject, edges, "api",
		NodeProvides{"db": omStrInject("host", "db.svc", "cert", "/etc/db.pem")}, nil, RecipeLookup{}, nil)

	if inject.Additional.Len() != 0 {
		t.Errorf("Additional = %v, want empty", inject.Additional.Keys())
	}
	if inject.TemplateUntaggedKeys() != nil {
		t.Error("TemplateUntaggedKeys() non-nil, want nil so no template key is added")
	}
}
