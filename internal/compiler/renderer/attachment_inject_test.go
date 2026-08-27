// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"errors"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// injectEnvBlock builds an IR attachment inject of shape {env: {localKey: providesKey}}.
func injectEnvBlock(localKey, providesKey string) types.OrderedMap[types.OrderedMap[string]] {
	m := types.OrderedMap[string]{}
	m.Set(localKey, providesKey)
	out := types.OrderedMap[types.OrderedMap[string]]{}
	out.Set("env", m)
	return out
}

func attachValues(key, val string) types.OrderedMap[any] {
	v := types.OrderedMap[any]{}
	v.Set(key, val)
	return v
}

// providesFR is a FeatureRecipe that declares a provides: block — the source an
// authored attachment inject's provides-key reference resolves against.
func providesFR(implements, provideKey, provideTmpl string) *types.FeatureRecipe {
	pv := types.OrderedMap[string]{}
	pv.Set(provideKey, provideTmpl)
	return &types.FeatureRecipe{
		Metadata: types.FeatureRecipeMetadata{Name: "provides-fr"},
		Spec:     types.FeatureRecipeSpec{Capability: "compose", Implements: implements, Provides: pv},
	}
}

// TestResolveAttachmentInject_SecretDeliversResolvedProvidesToHostPipe is the
// load-bearing secret proof: an authored secret.inject provides-key reference
// (DB_PASSWORD: root_password) resolves against the credential's OWN resolved
// provides (root_password evaluated from the credential FR's provides block)
// and lands in the host component's delivery pipe .context.inject.env.DB_PASSWORD
// — the exact value a base Recipe reads. This inspects the delivered value, not
// merely that entry.Inject is populated.
func TestResolveAttachmentInject_SecretDeliversResolvedProvidesToHostPipe(t *testing.T) {
	t.Parallel()
	entry := types.IRFeatureEntry{
		Type:   "core/db-creds",
		Values: attachValues("generated", "s3cr3t"),
		Inject: injectEnvBlock("DB_PASSWORD", "root_password"),
	}
	inner := types.OrderedMap[types.IRFeatureEntry]{}
	inner.Set("db-creds", entry)
	secrets := types.OrderedMap[types.OrderedMap[types.IRFeatureEntry]]{}
	secrets.Set("api", inner)
	graph := &types.IRGraph{Spec: types.IRSpec{FeatureValuesMapping: secrets}}

	lookup := RecipeLookup{FeatureRecipe: func(_, ft string) (*types.FeatureRecipe, bool) {
		if ft == "core/db-creds" {
			return providesFR("core/db-creds:1.0.0", "root_password", "{{ .values.generated }}"), true
		}
		return nil, false
	}}

	inject, _ := BuildInjectPipe(nil)
	if _, err := ResolveAttachmentInject(&inject, graph, "api", lookup, nil); err != nil {
		t.Fatalf("ResolveAttachmentInject: %v", err)
	}
	got, ok := inject.Env.Get("DB_PASSWORD")
	if !ok {
		t.Fatalf("DB_PASSWORD absent from host inject pipe; env keys=%v", inject.Env.Keys())
	}
	if got != "s3cr3t" {
		t.Errorf("DB_PASSWORD = %q, want %q (resolved from credential provides root_password)", got, "s3cr3t")
	}
	if v, _ := inject.All.Get("DB_PASSWORD"); v != "s3cr3t" {
		t.Errorf("all.DB_PASSWORD = %q (pipe .all must be rebuilt)", v)
	}
}

// TestResolveAttachmentInject_FeatureDeliversResolvedProvidesToHostPipe is the
// symmetric non-credential proof: a feature.inject provides-key reference
// (CACHE_URL: url) resolves against the feature FR's own provides and lands in
// the host pipe — the same surface as the secret path (one feature, per the docs).
func TestResolveAttachmentInject_FeatureDeliversResolvedProvidesToHostPipe(t *testing.T) {
	t.Parallel()
	entry := types.IRFeatureEntry{
		Type:   "core/cache",
		Values: attachValues("host", "redis.svc.cluster.local"),
		Inject: injectEnvBlock("CACHE_URL", "url"),
	}
	inner := types.OrderedMap[types.IRFeatureEntry]{}
	inner.Set("cache", entry)
	fvm := types.OrderedMap[types.OrderedMap[types.IRFeatureEntry]]{}
	fvm.Set("api", inner)
	graph := &types.IRGraph{Spec: types.IRSpec{FeatureValuesMapping: fvm}}

	lookup := RecipeLookup{FeatureRecipe: func(_, ft string) (*types.FeatureRecipe, bool) {
		if ft == "core/cache" {
			return providesFR("core/cache:1.0.0", "url", "{{ .values.host }}"), true
		}
		return nil, false
	}}

	inject, _ := BuildInjectPipe(nil)
	if _, err := ResolveAttachmentInject(&inject, graph, "api", lookup, nil); err != nil {
		t.Fatalf("ResolveAttachmentInject: %v", err)
	}
	if got, _ := inject.Env.Get("CACHE_URL"); got != "redis.svc.cluster.local" {
		t.Errorf("CACHE_URL = %q, want %q (resolved from feature provides url)", got, "redis.svc.cluster.local")
	}
}

// TestResolveAttachmentInject_CollisionWithExistingKeyIsRefused proves the fatal
// halt: an attachment delivery key already present in the pipe (here from
// self-inject / uses-inject, seeded directly) is a duplicate across developer
// sources and is refused rather than silently overwriting.
//
// Delivery records the claim and the declared rule refuses it, so this drives the
// two halves the build drives — the delivery, then the rule over what it recorded,
// through the framework's own traversal — rather than a call production never
// makes. The pipe is also inspected: the standing value has to survive, because
// the record's own contract is that the first claim under a key is the one that
// holds it.
func TestResolveAttachmentInject_CollisionWithExistingKeyIsRefused(t *testing.T) {
	t.Parallel()
	entry := types.IRFeatureEntry{
		Type:   "core/db-creds",
		Values: attachValues("generated", "s3cr3t"),
		Inject: injectEnvBlock("DB_PASSWORD", "root_password"),
	}
	inner := types.OrderedMap[types.IRFeatureEntry]{}
	inner.Set("db-creds", entry)
	secrets := types.OrderedMap[types.OrderedMap[types.IRFeatureEntry]]{}
	secrets.Set("api", inner)
	graph := &types.IRGraph{Spec: types.IRSpec{FeatureValuesMapping: secrets}}

	lookup := RecipeLookup{FeatureRecipe: func(_, ft string) (*types.FeatureRecipe, bool) {
		return providesFR("core/db-creds:1.0.0", "root_password", "{{ .values.generated }}"), true
	}}

	inject, _ := BuildInjectPipe(nil)
	inject.Env.Set("DB_PASSWORD", "from-self-inject") // a prior developer source claimed the key
	deliveries, err := ResolveAttachmentInject(&inject, graph, "api", lookup, nil)
	if err != nil {
		t.Fatalf("delivery itself must not fail: %v", err)
	}
	if got, _ := inject.Env.Get("DB_PASSWORD"); got != "from-self-inject" {
		t.Errorf("the standing value must survive the duplicate claim; env.DB_PASSWORD = %q", got)
	}

	halt := injectDeliveryHalt(t, "api", []string{"db-creds"}, deliveries)
	if !errors.Is(halt, ErrAttachmentInjectKeyCollision()) {
		t.Fatalf("expected ErrAttachmentInjectKeyCollision, got %v", halt)
	}
	if got := injectDeliveryFindings(t, halt); len(got) != 1 ||
		!strings.Contains(got[0], `"DB_PASSWORD"`) ||
		!strings.Contains(got[0], `mechanism "env"`) {
		t.Errorf("finding must name the key and the mechanism; got %q", got)
	}
}

// providedBag builds a produced provides bag from the given keys.
func providedBag(keys ...string) types.OrderedMap[string] {
	m := types.OrderedMap[string]{}
	for _, k := range keys {
		m.Set(k, "v-"+k)
	}
	return m
}

func TestEnforceProvidesContract_ExactMatchPasses(t *testing.T) {
	t.Parallel()
	declared := map[string]bool{"host": true, "port": true}
	produced := providedBag("host", "port")
	if err := enforceProvidesContract(declared, produced, "web",
		`node "web"`, `Recipe "svc"`, `Component "core/service"`); err != nil {
		t.Fatalf("produced covering every declared key must pass; got: %v", err)
	}
}

func TestEnforceProvidesContract_SupersetPasses(t *testing.T) {
	t.Parallel()
	declared := map[string]bool{"host": true}
	produced := providedBag("host", "port", "extra")
	if err := enforceProvidesContract(declared, produced, "web",
		`node "web"`, `Recipe "svc"`, `Component "core/service"`); err != nil {
		t.Fatalf("extra produced keys beyond the declared set must be tolerated; got: %v", err)
	}
}

func TestEnforceProvidesContract_ShortfallHalts(t *testing.T) {
	t.Parallel()
	declared := map[string]bool{"host": true, "port": true}
	produced := providedBag("host")
	err := enforceProvidesContract(declared, produced, "web",
		`node "web"`, `Recipe "svc"`, `Component "core/service"`)
	if !errors.Is(err, ErrProvidesContractUnfulfilled()) {
		t.Fatalf("a missing declared key must halt with ErrProvidesContractUnfulfilled; got: %v", err)
	}
	if !strings.Contains(err.Error(), `"port"`) {
		t.Fatalf("the halt must name the missing key %q; got: %v", "port", err)
	}
}

func TestEnforceProvidesContract_EmptyDeclaredPasses(t *testing.T) {
	t.Parallel()
	if err := enforceProvidesContract(map[string]bool{}, providedBag(), "web",
		`node "web"`, `Recipe "svc"`, `Component "core/service"`); err != nil {
		t.Fatalf("a contract with no declared keys has nothing to enforce; got: %v", err)
	}
}
