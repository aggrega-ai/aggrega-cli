// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"testing"

	"github.com/cognitio/aggrega-cli/pkg/types"
)

// mkMech builds a nested OrderedMap[OrderedMap[string]] from mechanism→key→value
// alternating args for terseness in test fixtures.
func mkMech(mech string, pairs ...string) types.OrderedMap[types.OrderedMap[string]] {
	out := types.OrderedMap[types.OrderedMap[string]]{}
	inner := types.OrderedMap[string]{}
	for i := 0; i+1 < len(pairs); i += 2 {
		inner.Set(pairs[i], pairs[i+1])
	}
	out.Set(mech, inner)
	return out
}

// mkMultiMech builds from a list of (mechanism, []kv) groups.
func mkMultiMech(groups ...interface{}) types.OrderedMap[types.OrderedMap[string]] {
	out := types.OrderedMap[types.OrderedMap[string]]{}
	for i := 0; i+1 < len(groups); i += 2 {
		mech := groups[i].(string)
		kvs := groups[i+1].([]string)
		inner := types.OrderedMap[string]{}
		for j := 0; j+1 < len(kvs); j += 2 {
			inner.Set(kvs[j], kvs[j+1])
		}
		out.Set(mech, inner)
	}
	return out
}

func TestBuildSecretInject_BasicMechanisms(t *testing.T) {
	t.Parallel()

	entry := types.IRSecretEntry{
		Inject: mkMultiMech(
			"env", []string{"DB_PASSWORD", "ref:root_password"},
			"file", []string{"/etc/db/pw", "ref:root_password"},
			"arg", []string{"--password", "ref:root_password"},
		),
	}

	pipe := buildSecretInject(entry)
	if pipe == nil {
		t.Fatal("expected non-nil pipe")
	}
	if v, _ := pipe.Env.Get("DB_PASSWORD"); v != "ref:root_password" {
		t.Errorf("env: %v", pipe.Env.Keys())
	}
	if v, _ := pipe.File.Get("/etc/db/pw"); v != "ref:root_password" {
		t.Errorf("file: %v", pipe.File.Keys())
	}
	if v, _ := pipe.Arg.Get("--password"); v != "ref:root_password" {
		t.Errorf("arg: %v", pipe.Arg.Keys())
	}
	// .All merges all three channels
	dbAll, _ := pipe.All.Get("DB_PASSWORD")
	fileAll, _ := pipe.All.Get("/etc/db/pw")
	argAll, _ := pipe.All.Get("--password")
	if dbAll != "ref:root_password" || fileAll != "ref:root_password" || argAll != "ref:root_password" {
		t.Errorf("all: %v", pipe.All.Keys())
	}
}

func TestBuildSecretInject_GuaranteedEmptyMaps(t *testing.T) {
	t.Parallel()

	pipe := buildSecretInject(types.IRSecretEntry{}) // empty inject
	if pipe == nil {
		t.Fatal("expected non-nil pipe")
	}
	// OrderedMap zero value is safe to use; length checks suffice.
	if pipe.Env.Len() != 0 || pipe.File.Len() != 0 || pipe.Arg.Len() != 0 {
		t.Errorf("expected empty maps, got env=%v file=%v arg=%v", pipe.Env.Keys(), pipe.File.Keys(), pipe.Arg.Keys())
	}
}

func TestBuildSecretInject_PartialChannels(t *testing.T) {
	t.Parallel()

	entry := types.IRSecretEntry{
		Inject: mkMech("env", "PG_PASSWORD", "ref:primary"),
	}

	pipe := buildSecretInject(entry)
	if v, _ := pipe.Env.Get("PG_PASSWORD"); v != "ref:primary" {
		t.Errorf("env: %v", pipe.Env.Keys())
	}
	if pipe.File.Len() != 0 || pipe.Arg.Len() != 0 {
		t.Errorf("other channels should be empty, got file=%v arg=%v", pipe.File.Keys(), pipe.Arg.Keys())
	}
}

func TestBuildSecretInject_DoesNotMutateSource(t *testing.T) {
	t.Parallel()

	src := types.OrderedMap[string]{}
	src.Set("K", "v")
	inject := types.OrderedMap[types.OrderedMap[string]]{}
	inject.Set("env", src)
	entry := types.IRSecretEntry{Inject: inject}

	pipe := buildSecretInject(entry)
	pipe.Env.Set("K", "mutated")
	if got, _ := src.Get("K"); got != "v" {
		t.Error("buildSecretInject should not alias the source map")
	}
}
