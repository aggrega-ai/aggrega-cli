// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"testing"

	"github.com/cognitio/aggrega-cli/pkg/types"
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

	RehydrateInjectFromProvides(&inject, edges, "api", allProvides)

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
		Additional: map[string]map[string]string{
			"vault":   {"VAULT_KEY": "v"},
			"ssm":     {"SSM_KEY": "s"},
			"azurekv": {"AZ_KEY": "z"},
		},
	}

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
