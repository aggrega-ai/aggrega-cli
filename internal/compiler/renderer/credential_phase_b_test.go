// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"testing"

	"github.com/cognitio/aggrega-cli/pkg/types"
)

func TestBuildSecretInject_BasicMechanisms(t *testing.T) {
	t.Parallel()

	entry := types.IRSecretEntry{
		Inject: map[string]map[string]string{
			"env":  {"DB_PASSWORD": "ref:root_password"},
			"file": {"/etc/db/pw": "ref:root_password"},
			"arg":  {"--password": "ref:root_password"},
		},
	}

	pipe := buildSecretInject(entry)
	if pipe == nil {
		t.Fatal("expected non-nil pipe")
	}
	if pipe.Env["DB_PASSWORD"] != "ref:root_password" {
		t.Errorf("env: %v", pipe.Env)
	}
	if pipe.File["/etc/db/pw"] != "ref:root_password" {
		t.Errorf("file: %v", pipe.File)
	}
	if pipe.Arg["--password"] != "ref:root_password" {
		t.Errorf("arg: %v", pipe.Arg)
	}
	// .All merges all three channels
	if pipe.All["DB_PASSWORD"] != "ref:root_password" || pipe.All["/etc/db/pw"] != "ref:root_password" || pipe.All["--password"] != "ref:root_password" {
		t.Errorf("all: %v", pipe.All)
	}
}

func TestBuildSecretInject_GuaranteedEmptyMaps(t *testing.T) {
	t.Parallel()

	pipe := buildSecretInject(types.IRSecretEntry{}) // empty inject
	if pipe == nil {
		t.Fatal("expected non-nil pipe")
	}
	if pipe.Env == nil || pipe.File == nil || pipe.Arg == nil {
		t.Error("expected guaranteed non-nil maps for env/file/arg")
	}
	if len(pipe.Env) != 0 || len(pipe.File) != 0 || len(pipe.Arg) != 0 {
		t.Errorf("expected empty maps, got env=%v file=%v arg=%v", pipe.Env, pipe.File, pipe.Arg)
	}
	if pipe.All == nil {
		t.Error("expected non-nil .All")
	}
}

func TestBuildSecretInject_PartialChannels(t *testing.T) {
	t.Parallel()

	entry := types.IRSecretEntry{
		Inject: map[string]map[string]string{
			"env": {"PG_PASSWORD": "ref:primary"},
		},
	}

	pipe := buildSecretInject(entry)
	if pipe.Env["PG_PASSWORD"] != "ref:primary" {
		t.Errorf("env: %v", pipe.Env)
	}
	if len(pipe.File) != 0 || len(pipe.Arg) != 0 {
		t.Errorf("other channels should be empty, got file=%v arg=%v", pipe.File, pipe.Arg)
	}
}

func TestBuildSecretInject_DoesNotMutateSource(t *testing.T) {
	t.Parallel()

	src := map[string]string{"K": "v"}
	entry := types.IRSecretEntry{
		Inject: map[string]map[string]string{"env": src},
	}

	pipe := buildSecretInject(entry)
	pipe.Env["K"] = "mutated"
	if src["K"] != "v" {
		t.Error("buildSecretInject should not alias the source map")
	}
}
