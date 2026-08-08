// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

func TestCheckProvidesSelfContainment_Clean(t *testing.T) {
	t.Parallel()
	err := CheckProvidesSelfContainment(`{{ .context.instance_id }}-db`, "test-recipe")
	if err != nil {
		t.Errorf("expected no error, got: %v", err)
	}
}

func TestCheckProvidesSelfContainment_ForbiddenInject(t *testing.T) {
	t.Parallel()
	err := CheckProvidesSelfContainment(`{{ .context.inject.env.DB_URL }}`, "bad-recipe")
	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.Is(err, ErrProvidesSelfContainment()) {
		t.Errorf("expected ErrProvidesSelfContainment, got: %v", err)
	}
}

func TestCheckProvidesSelfContainment_ForbiddenUses(t *testing.T) {
	t.Parallel()
	err := CheckProvidesSelfContainment(`{{ .context.uses.my_db.provides.host }}`, "bad-recipe")
	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.Is(err, ErrProvidesSelfContainment()) {
		t.Errorf("expected ErrProvidesSelfContainment, got: %v", err)
	}
}

func TestCheckProvidesSelfContainment_StringLiteralOK(t *testing.T) {
	t.Parallel()
	// The word "inject" in a static string (not inside {{ }}) should not trigger
	err := CheckProvidesSelfContainment(`static string with inject word`, "test-recipe")
	if err != nil {
		t.Errorf("expected no error for static string, got: %v", err)
	}
}

func TestEvaluateAllProvides_Simple(t *testing.T) {
	t.Parallel()
	nodes := types.OrderedMap[types.IRNode]{}
	nodes.Set("db", types.IRNode{Component: "core/datastore/postgres"})

	graph := &types.IRGraph{
		Spec: types.IRSpec{
			Nodes: nodes,
		},
	}
	provides := types.OrderedMap[string]{}
	provides.Set("host", `{{ .context.instance_id }}.svc.cluster.local`)
	provides.Set("service_port", `5432`)
	locals := types.OrderedMap[string]{}
	locals.Set("primary_compute", "postgres")

	recipes := map[string]*types.Recipe{
		"db": {
			Metadata: types.RecipeMetadata{Name: "postgres-recipe"},
			Spec: types.RecipeSpec{
				Provides: provides,
				Locals:   locals,
			},
		},
	}

	result, resultLocals, err := EvaluateAllProvides(graph, recipes)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	dbProv := result["db"]
	if host, _ := dbProv.Get("host"); host != "db.svc.cluster.local" {
		t.Errorf("host: %q", host)
	}
	if sp, _ := dbProv.Get("service_port"); sp != "5432" {
		t.Errorf("service_port: %q", sp)
	}
	dbLocals := resultLocals["db"]
	if pc, _ := dbLocals.Get("primary_compute"); pc != "postgres" {
		t.Errorf("locals travel out of the pass alongside provides; primary_compute = %q", pc)
	}
}

// The ordering the pass fixes, proved through the pass itself: a provides template
// reading .locals receives the RESOLVED value, not the authored template text.
func TestEvaluateAllProvides_ProvidesReadsResolvedLocals(t *testing.T) {
	t.Parallel()
	nodes := types.OrderedMap[types.IRNode]{}
	nodes.Set("db", types.IRNode{Component: "core/datastore/postgres"})
	vm := types.OrderedMap[json.RawMessage]{}
	vm.Set("db", json.RawMessage(`{"zone":"eu-west-1"}`))
	graph := &types.IRGraph{Spec: types.IRSpec{Nodes: nodes, ValuesMapping: vm}}

	locals := types.OrderedMap[string]{}
	locals.Set("db_identifier", `{{ .context.instance_id }}-{{ .values.zone }}`)
	provides := types.OrderedMap[string]{}
	provides.Set("host", `{{ .locals.db_identifier }}.svc.cluster.local`)

	recipes := map[string]*types.Recipe{
		"db": {
			Metadata: types.RecipeMetadata{Name: "postgres-recipe"},
			Spec:     types.RecipeSpec{Provides: provides, Locals: locals},
		},
	}

	result, resultLocals, err := EvaluateAllProvides(graph, recipes)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	dbLocals := resultLocals["db"]
	if got, _ := dbLocals.Get("db_identifier"); got != "db-eu-west-1" {
		t.Fatalf("locals: got %q, want %q", got, "db-eu-west-1")
	}
	dbProv := result["db"]
	got, _ := dbProv.Get("host")
	if got != "db-eu-west-1.svc.cluster.local" {
		t.Errorf("provides read of .locals: got %q, want %q", got, "db-eu-west-1.svc.cluster.local")
	}
	if strings.Contains(got, "{{") {
		t.Errorf("provides received authored template text rather than a resolved value: %q", got)
	}
}

// A Recipe declaring locals and no provides still has its locals resolved: the
// preamble runs for every node, so a downstream reader of the returned locals is
// not silently handed an empty map.
func TestEvaluateAllProvides_LocalsResolvedWithoutProvides(t *testing.T) {
	t.Parallel()
	nodes := types.OrderedMap[types.IRNode]{}
	nodes.Set("api", types.IRNode{})
	vm := types.OrderedMap[json.RawMessage]{}
	vm.Set("api", json.RawMessage(`{"replicas":3}`))
	graph := &types.IRGraph{Spec: types.IRSpec{Nodes: nodes, ValuesMapping: vm}}

	locals := types.OrderedMap[string]{}
	locals.Set("scaled", `{{ .values.replicas }}x`)
	recipes := map[string]*types.Recipe{
		"api": {Metadata: types.RecipeMetadata{Name: "api-recipe"}, Spec: types.RecipeSpec{Locals: locals}},
	}

	result, resultLocals, err := EvaluateAllProvides(graph, recipes)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if apiProv := result["api"]; apiProv.Len() != 0 {
		t.Errorf("expected empty provides, got %v", apiProv.Keys())
	}
	apiLocals := resultLocals["api"]
	if got, _ := apiLocals.Get("scaled"); got != "3x" {
		t.Errorf("locals for a provides-less Recipe: got %q, want %q", got, "3x")
	}
}

// A local reaching for its own node's provides is the reverse of the order the
// pass fixes, and it halts inside the pass rather than resolving to empty.
func TestEvaluateAllProvides_LocalReadingProvidesHalts(t *testing.T) {
	t.Parallel()
	nodes := types.OrderedMap[types.IRNode]{}
	nodes.Set("db", types.IRNode{})
	graph := &types.IRGraph{Spec: types.IRSpec{Nodes: nodes}}

	locals := types.OrderedMap[string]{}
	locals.Set("fqdn", `{{ .provides.host }}.svc`)
	provides := types.OrderedMap[string]{}
	provides.Set("host", `{{ .context.instance_id }}`)
	recipes := map[string]*types.Recipe{
		"db": {Metadata: types.RecipeMetadata{Name: "reverse-recipe"}, Spec: types.RecipeSpec{Locals: locals, Provides: provides}},
	}

	_, _, err := EvaluateAllProvides(graph, recipes)
	if err == nil {
		t.Fatal("expected a halt on the reverse reference, got nil")
	}
	if !errors.Is(err, ErrTemplateUnknownRoot()) {
		t.Errorf("expected the unknown-reference halt, got: %v", err)
	}
	for _, want := range []string{"db", "reverse-recipe", ".provides.host"} {
		if !strings.Contains(diagnosticChainText(err), want) {
			t.Errorf("diagnostic omits %q: %s", want, diagnosticChainText(err))
		}
	}
}

func TestEvaluateAllProvides_SelfContainmentViolation(t *testing.T) {
	t.Parallel()
	nodes := types.OrderedMap[types.IRNode]{}
	nodes.Set("api", types.IRNode{})

	graph := &types.IRGraph{
		Spec: types.IRSpec{
			Nodes: nodes,
		},
	}
	provides := types.OrderedMap[string]{}
	provides.Set("url", `{{ .context.inject.env.TARGET }}`)

	recipes := map[string]*types.Recipe{
		"api": {
			Metadata: types.RecipeMetadata{Name: "bad-recipe"},
			Spec: types.RecipeSpec{
				Provides: provides,
			},
		},
	}

	_, _, err := EvaluateAllProvides(graph, recipes)
	if err == nil {
		t.Fatal("expected error for self-containment violation")
	}
	if !errors.Is(err, ErrProvidesSelfContainment()) {
		t.Errorf("expected ErrProvidesSelfContainment, got: %v", err)
	}
}

// Admitting .locals on the provides surface admits an open keyed map, so a mistyped
// local key would otherwise resolve away to an empty string inside an emitted
// pointer. The halt names BOTH sides of the mistake — the provides key that reads and
// the local key that does not exist — because either alone leaves the author hunting.
func TestEvaluateAllProvides_ProvidesReadingUnknownLocalHalts(t *testing.T) {
	t.Parallel()
	nodes := types.OrderedMap[types.IRNode]{}
	nodes.Set("db", types.IRNode{})
	graph := &types.IRGraph{Spec: types.IRSpec{Nodes: nodes}}

	locals := types.OrderedMap[string]{}
	locals.Set("db_identifier", `{{ .context.instance_id }}-main`)
	provides := types.OrderedMap[string]{}
	provides.Set("host", `{{ .locals.db_identifer }}.svc.cluster.local`)
	recipes := map[string]*types.Recipe{
		"db": {Metadata: types.RecipeMetadata{Name: "postgres-recipe"}, Spec: types.RecipeSpec{Locals: locals, Provides: provides}},
	}

	_, _, err := EvaluateAllProvides(graph, recipes)
	if err == nil {
		t.Fatal("expected a halt on the unknown local, got nil")
	}
	if !errors.Is(err, ErrLocalsUnknownReference()) {
		t.Fatalf("expected ErrLocalsUnknownReference, got: %v", err)
	}
	text := diagnosticChainText(err)
	// Both sides of the mistake, plus the keys the block does declare.
	for _, want := range []string{`provides["host"]`, "db_identifer", ".locals.db_identifier", "postgres-recipe"} {
		if !strings.Contains(text, want) {
			t.Errorf("diagnostic omits %q: %s", want, text)
		}
	}
}

// The same halt on a Recipe that declares no locals at all: the diagnostic must still
// read as a sentence rather than trailing an empty list of declared keys.
func TestEvaluateAllProvides_ProvidesReadingLocalWithoutLocalsBlockHalts(t *testing.T) {
	t.Parallel()
	nodes := types.OrderedMap[types.IRNode]{}
	nodes.Set("db", types.IRNode{})
	graph := &types.IRGraph{Spec: types.IRSpec{Nodes: nodes}}

	provides := types.OrderedMap[string]{}
	provides.Set("host", `{{ .locals.db_identifier }}.svc`)
	recipes := map[string]*types.Recipe{
		"db": {Metadata: types.RecipeMetadata{Name: "no-locals-recipe"}, Spec: types.RecipeSpec{Provides: provides}},
	}

	_, _, err := EvaluateAllProvides(graph, recipes)
	if err == nil {
		t.Fatal("expected a halt on the unknown local, got nil")
	}
	if !errors.Is(err, ErrLocalsUnknownReference()) {
		t.Fatalf("expected ErrLocalsUnknownReference, got: %v", err)
	}
	text := diagnosticChainText(err)
	for _, want := range []string{`provides["host"]`, "db_identifier", "no locals are declared"} {
		if !strings.Contains(text, want) {
			t.Errorf("diagnostic omits %q: %s", want, text)
		}
	}
}

// The check reads the parsed tree for a DIRECT .locals.<key> reference, so an
// indirect read through `index` is not analysed — and must keep rendering. This is
// not an oversight to close: recognising the key inside `index .locals (printf …)`
// would require reading template TEXT and inferring what a computed key means, which
// the compiler does not do. The guard exists so a later tightening of the direct
// check cannot silently break the indirect idiom catalog content already uses.
func TestEvaluateAllProvides_ProvidesIndirectLocalsReadStillRenders(t *testing.T) {
	t.Parallel()
	nodes := types.OrderedMap[types.IRNode]{}
	nodes.Set("db", types.IRNode{})
	graph := &types.IRGraph{Spec: types.IRSpec{Nodes: nodes}}

	locals := types.OrderedMap[string]{}
	locals.Set("db_identifier", `{{ .context.instance_id }}-main`)
	provides := types.OrderedMap[string]{}
	provides.Set("host", `{{ index .locals "db_identifier" }}.svc`)
	recipes := map[string]*types.Recipe{
		"db": {Metadata: types.RecipeMetadata{Name: "indirect-recipe"}, Spec: types.RecipeSpec{Locals: locals, Provides: provides}},
	}

	result, _, err := EvaluateAllProvides(graph, recipes)
	if err != nil {
		t.Fatalf("unexpected halt on an indirect .locals read: %v", err)
	}
	dbProv := result["db"]
	if got, _ := dbProv.Get("host"); got != "db-main.svc" {
		t.Errorf("indirect read: got %q, want %q", got, "db-main.svc")
	}
}

func TestEvaluateAllProvides_NoRecipe(t *testing.T) {
	t.Parallel()
	nodes := types.OrderedMap[types.IRNode]{}
	nodes.Set("api", types.IRNode{})

	graph := &types.IRGraph{
		Spec: types.IRSpec{
			Nodes: nodes,
		},
	}

	result, _, err := EvaluateAllProvides(graph, map[string]*types.Recipe{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	apiProv := result["api"]
	if apiProv.Len() != 0 {
		t.Errorf("expected empty provides for api, got %v", apiProv.Keys())
	}
}

func TestCheckProvidesSelfContainment_ForbiddenRoots(t *testing.T) {
	t.Parallel()
	// Each of these references a root the self-contained Pass-1 surface does not
	// expose; the allowlist must HALT (not render empty — the silent footgun the
	// blocklist permitted). Includes the placement-coupled roots a naive
	// "namespace in provides" fix would reach for.
	forbidden := map[string]string{
		"context.annotations": `{{ index .context.annotations "aggrega.ai/namespace" }}`,
		"context.labels":      `{{ .context.labels.tier }}`,
		"context.metadata":    `{{ .context.metadata.name }}`,
		"top-level metadata":  `{{ .metadata.release_name }}`,
		"context.inject":      `{{ .context.inject.env.X }}`,
		"context.uses":        `{{ .context.uses.db.provides.host }}`,
		"unknown top-level":   `{{ .provides.host }}`,
		// No attachment is being resolved during a base Recipe's provides pass, so
		// the name would render empty. Permitting the root would reopen exactly the
		// silent-empty footgun this allowlist closed.
		"attachment_name": `{{ .attachment_name }}`,
	}
	for name, tmpl := range forbidden {
		t.Run(name, func(t *testing.T) {
			err := CheckProvidesSelfContainment(tmpl, "bad-recipe")
			if err == nil {
				t.Fatalf("%s: expected halt, got nil (silent-empty footgun)", name)
			}
			if !errors.Is(err, ErrProvidesSelfContainment()) {
				t.Errorf("%s: expected ErrProvidesSelfContainment, got: %v", name, err)
			}
		})
	}
}

func TestCheckProvidesSelfContainment_AllowedRoots(t *testing.T) {
	t.Parallel()
	// The wired self-contained set: every one of these must pass the allowlist.
	// .locals is in the set because a node's locals resolve in the pass preamble,
	// ahead of its provides, so the reference reaches a resolved value.
	allowed := []string{
		`{{ .context.instance_id }}-default`,
		`{{ .values.port }}`,
		`{{ .infra.tier }}`,
		`{{ .context.instance_id }}:{{ .values.port }}`,
		`{{ .locals.db_identifier }}`,
		`{{ .locals.db_identifier }}.{{ .infra.zone }}`,
		`static-only`,
	}
	for _, tmpl := range allowed {
		if err := CheckProvidesSelfContainment(tmpl, "ok-recipe"); err != nil {
			t.Errorf("expected no error for %q, got: %v", tmpl, err)
		}
	}
}

func TestProvidesContainmentErr_ListsExactlyTheAdmittedRoots(t *testing.T) {
	t.Parallel()
	// The diagnostic's root list is derived from the allowlists, so it cannot
	// name a root the check refuses. This pins the derivation: every admitted
	// root appears, and the surface's one narrowing does not.
	msg := providesContainmentErr("bad-recipe", ".attachment_name").Error()
	for _, want := range []string{".context.instance_id", ".values", ".infra", ".locals"} {
		if !strings.Contains(msg, want) {
			t.Errorf("diagnostic omits admitted root %s: %s", want, msg)
		}
	}
	if strings.Contains(msg, ".attachment_name,") {
		t.Errorf("diagnostic still offers .attachment_name as permitted: %s", msg)
	}
	// The offending expression and the recipe both have to be nameable from the
	// message alone, since that is all the author sees.
	if !strings.Contains(msg, ".attachment_name") || !strings.Contains(msg, "bad-recipe") {
		t.Errorf("diagnostic does not name both the recipe and the expression: %s", msg)
	}
}

func TestEvaluateAllProvides_ForbiddenAnnotationHaltsNotEmpty(t *testing.T) {
	t.Parallel()
	// Footgun-closure proof through the real provides-evaluation path: a provides
	// reading a placement annotation HALTS loudly rather than rendering empty.
	nodes := types.OrderedMap[types.IRNode]{}
	nodes.Set("db", types.IRNode{})
	graph := &types.IRGraph{Spec: types.IRSpec{Nodes: nodes}}
	provides := types.OrderedMap[string]{}
	provides.Set("host", `{{ .context.instance_id }}-default.{{ index .context.annotations "aggrega.ai/namespace" }}`)
	recipes := map[string]*types.Recipe{
		"db": {Metadata: types.RecipeMetadata{Name: "footgun-recipe"}, Spec: types.RecipeSpec{Provides: provides}},
	}
	_, _, err := EvaluateAllProvides(graph, recipes)
	if err == nil {
		t.Fatal("expected halt for placement annotation in provides, got nil")
	}
	if !errors.Is(err, ErrProvidesSelfContainment()) {
		t.Errorf("expected ErrProvidesSelfContainment, got: %v", err)
	}
}

func TestEvaluateAllProvides_SprigFunction(t *testing.T) {
	t.Parallel()
	nodes := types.OrderedMap[types.IRNode]{}
	nodes.Set("api", types.IRNode{})

	graph := &types.IRGraph{
		Spec: types.IRSpec{
			Nodes: nodes,
		},
	}
	provides := types.OrderedMap[string]{}
	provides.Set("host", `{{ .context.instance_id | upper }}`)

	recipes := map[string]*types.Recipe{
		"api": {
			Metadata: types.RecipeMetadata{Name: "sprig-recipe"},
			Spec: types.RecipeSpec{
				Provides: provides,
			},
		},
	}

	result, _, err := EvaluateAllProvides(graph, recipes)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	apiProv := result["api"]
	host, _ := apiProv.Get("host")
	if host != "API" {
		t.Errorf("expected 'API', got %q", host)
	}
}
