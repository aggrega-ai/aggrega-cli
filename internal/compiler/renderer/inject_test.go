// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"testing"

	"github.com/cognitio/aggrega-cli/pkg/types"
)

func TestBuildInjectPipe_NilRaw(t *testing.T) {
	t.Parallel()
	inject, raw := BuildInjectPipe(nil)
	if raw != nil {
		t.Error("expected nil injectRaw")
	}
	if inject.Env == nil || inject.File == nil || inject.Arg == nil || inject.All == nil {
		t.Error("expected guaranteed empty maps, got nil")
	}
}

func TestBuildInjectPipe_WithValues(t *testing.T) {
	t.Parallel()
	rawBlock := &types.InjectBlock{
		Env:  map[string]interface{}{"DB_URL": "host"},
		File: map[string]interface{}{"config": "/etc/app.yaml"},
	}
	inject, raw := BuildInjectPipe(rawBlock)
	if raw == nil {
		t.Error("expected non-nil injectRaw")
	}
	if inject.Env["DB_URL"] != "host" {
		t.Errorf("env: %v", inject.Env)
	}
	if inject.File["config"] != "/etc/app.yaml" {
		t.Errorf("file: %v", inject.File)
	}
	// .all should contain both
	if inject.All["DB_URL"] != "host" || inject.All["config"] != "/etc/app.yaml" {
		t.Errorf("all: %v", inject.All)
	}
}

func TestBuildInjectPipe_GuaranteedKeys(t *testing.T) {
	t.Parallel()
	// Even with only env set, file and arg must be non-nil empty maps
	rawBlock := &types.InjectBlock{
		Env: map[string]interface{}{"X": "1"},
	}
	inject, _ := BuildInjectPipe(rawBlock)
	if inject.File == nil {
		t.Error("file should be guaranteed non-nil")
	}
	if inject.Arg == nil {
		t.Error("arg should be guaranteed non-nil")
	}
	if len(inject.File) != 0 {
		t.Error("file should be empty")
	}
}

func TestRehydrateInjectFromProvides(t *testing.T) {
	t.Parallel()

	inject := types.InjectPipe{
		Env:  map[string]string{},
		File: map[string]string{},
		Arg:  map[string]string{},
		All:  map[string]string{},
	}

	edges := []types.IREdge{
		{
			Source: "api",
			Target: "db",
			Inject: map[string]map[string]string{
				"env": {"DB_URL": "host", "DB_PORT": "service_port"},
			},
		},
	}

	allProvides := NodeProvides{
		"db": {"host": "db.svc.cluster.local", "service_port": "5432"},
	}

	RehydrateInjectFromProvides(&inject, edges, "api", allProvides)

	if inject.Env["DB_URL"] != "db.svc.cluster.local" {
		t.Errorf("DB_URL: %q", inject.Env["DB_URL"])
	}
	if inject.Env["DB_PORT"] != "5432" {
		t.Errorf("DB_PORT: %q", inject.Env["DB_PORT"])
	}
	if inject.All["DB_URL"] != "db.svc.cluster.local" {
		t.Errorf("all.DB_URL: %q", inject.All["DB_URL"])
	}
}

func TestBuildInjectAll_Deterministic(t *testing.T) {
	t.Parallel()

	inject := types.InjectPipe{
		Env:  map[string]string{"Z_VAR": "z", "A_VAR": "a"},
		File: map[string]string{"B_FILE": "b"},
		Arg:  map[string]string{"C_ARG": "c"},
	}

	all := buildInjectAll(inject)
	if len(all) != 4 {
		t.Fatalf("expected 4 keys, got %d", len(all))
	}
	// All keys present
	if all["Z_VAR"] != "z" || all["A_VAR"] != "a" || all["B_FILE"] != "b" || all["C_ARG"] != "c" {
		t.Errorf("all: %v", all)
	}
}

func TestBuildInjectAll_CustomMechanisms(t *testing.T) {
	t.Parallel()

	inject := types.InjectPipe{
		Env:  map[string]string{"ENV_KEY": "e"},
		File: map[string]string{"FILE_KEY": "f"},
		Arg:  map[string]string{"ARG_KEY": "a"},
		Additional: map[string]map[string]string{
			"vault":   {"VAULT_KEY": "v"},
			"ssm":     {"SSM_KEY": "s"},
			"azurekv": {"AZ_KEY": "z"},
		},
	}

	all := buildInjectAll(inject)
	if len(all) != 6 {
		t.Fatalf("expected 6 keys (3 std + 3 custom), got %d: %v", len(all), all)
	}
	for k, want := range map[string]string{
		"ENV_KEY": "e", "FILE_KEY": "f", "ARG_KEY": "a",
		"VAULT_KEY": "v", "SSM_KEY": "s", "AZ_KEY": "z",
	} {
		if all[k] != want {
			t.Errorf("all[%q] = %q, want %q", k, all[k], want)
		}
	}
}
