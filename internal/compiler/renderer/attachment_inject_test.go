// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/constraint"
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
	if _, err := ResolveAttachmentInject(&inject, graph, "api", "", lookup, nil, nil, nil, nil); err != nil {
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
	if _, err := ResolveAttachmentInject(&inject, graph, "api", "", lookup, nil, nil, nil, nil); err != nil {
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
	deliveries, err := ResolveAttachmentInject(&inject, graph, "api", "", lookup, nil, nil, nil, nil)
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

// TestDeliverAttachmentInjectKeys_ClaimsComeBackEnvFileArgThenCustomSorted pins
// the order an attachment's claims and its sensitive reports come back in: env,
// file and arg, then every other mechanism in sorted name order, whatever order
// the attachment's mapping names them in, each mechanism's keys in the order the
// mapping lists them. It also pins where each lands: a typed mechanism in the
// pipe's own field, a custom one under its own name — kept even when none of its
// claims delivered — beside a custom mechanism the pipe already carried, whose
// standing key is left in place.
func TestDeliverAttachmentInjectKeys_ClaimsComeBackEnvFileArgThenCustomSorted(t *testing.T) {
	t.Parallel()

	mapping := func(pairs ...[3]string) types.OrderedMap[types.OrderedMap[string]] {
		var out types.OrderedMap[types.OrderedMap[string]]
		for _, p := range pairs {
			keys, _ := out.Get(p[0])
			keys.Set(p[1], p[2])
			out.Set(p[0], keys)
		}
		return out
	}
	// Named in an order that is neither the typed order nor sorted.
	entryInject := mapping(
		[3]string{"vault", "V", "v"},
		[3]string{"arg", "A", "a"},
		[3]string{"ssm", "S", "s"},
		[3]string{"ssm", "S_TAKEN", "s"},
		[3]string{"env", "E", "e"},
		[3]string{"env", "E_MISSING", "nope"},
		[3]string{"kv", "K", "nope"},
		[3]string{"file", "F", "f"},
	)
	sensitive := mapping(
		[3]string{"env", "E", "e"},
		[3]string{"vault", "V", "v"},
		[3]string{"ssm", "S_TAKEN", "s"},
	)
	provided := types.OrderedMap[string]{}
	for _, k := range []string{"e", "f", "a", "s", "v"} {
		provided.Set(k, "value-"+k)
	}
	inject := types.InjectPipe{}
	var standing types.OrderedMap[string]
	standing.Set("S_TAKEN", "standing")
	inject.Additional.Set("ssm", standing)

	var reported []string
	source := &attachmentProvidesSource{featureRecipe: "creds-fr", provided: provided}
	claims := deliverAttachmentInjectKeys(&inject, "creds", entryInject, source, sensitive, func(attachment, mechanism, deliveryKey string) {
		reported = append(reported, attachment+"/"+mechanism+"/"+deliveryKey)
	})

	published := []string{"a", "e", "f", "s", "v"}
	claim := func(mech, key, from string, delivered, unresolved bool) constraint.InjectDelivery {
		return constraint.InjectDelivery{
			Mechanism: mech, DeliveryKey: key, Attachment: "creds", Delivered: delivered,
			From: from, Unresolved: unresolved, FeatureRecipe: "creds-fr", Published: published,
		}
	}
	wantClaims := []constraint.InjectDelivery{
		claim("env", "E", "e", true, false),
		claim("env", "E_MISSING", "nope", false, true),
		claim("file", "F", "f", true, false),
		claim("arg", "A", "a", true, false),
		claim("kv", "K", "nope", false, true),
		claim("ssm", "S", "s", true, false),
		claim("ssm", "S_TAKEN", "s", false, false),
		claim("vault", "V", "v", true, false),
	}
	if !reflect.DeepEqual(claims, wantClaims) {
		t.Errorf("claims came back as\n  %+v\nwant\n  %+v", claims, wantClaims)
	}
	if want := []string{"creds/env/E", "creds/vault/V"}; !reflect.DeepEqual(reported, want) {
		t.Errorf("sensitive reports %v, want %v", reported, want)
	}

	for _, tc := range []struct {
		view types.OrderedMap[string]
		key  string
		want string
	}{
		{inject.Env, "E", "value-e"},
		{inject.File, "F", "value-f"},
		{inject.Arg, "A", "value-a"},
	} {
		if got, _ := tc.view.Get(tc.key); got != tc.want {
			t.Errorf("typed field %s = %q, want %q", tc.key, got, tc.want)
		}
	}
	if got, want := inject.Additional.Keys(), []string{"ssm", "kv", "vault"}; !reflect.DeepEqual(got, want) {
		t.Errorf("custom mechanisms in the pipe %v, want %v", got, want)
	}
	ssm, _ := inject.Additional.Get("ssm")
	if got, _ := ssm.Get("S_TAKEN"); got != "standing" {
		t.Errorf("ssm S_TAKEN = %q, want the standing value kept", got)
	}
	if got, _ := ssm.Get("S"); got != "value-s" {
		t.Errorf("ssm S = %q, want %q", got, "value-s")
	}
	vault, _ := inject.Additional.Get("vault")
	if got, _ := vault.Get("V"); got != "value-v" {
		t.Errorf("vault V = %q, want %q", got, "value-v")
	}
	if kv, _ := inject.Additional.Get("kv"); kv.Len() != 0 {
		t.Errorf("kv carries %v, want an empty entry: none of its claims delivered", kv.Keys())
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
