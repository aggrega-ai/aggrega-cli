// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/gotmpl"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

func TestProvidesSelfContainment_Clean(t *testing.T) {
	t.Parallel()
	err := providesSelfContainmentHalt(t, `{{ .context.instance_id }}-db`, "test-recipe")
	if err != nil {
		t.Errorf("expected no error, got: %v", err)
	}
}

func TestProvidesSelfContainment_ForbiddenInject(t *testing.T) {
	t.Parallel()
	err := providesSelfContainmentHalt(t, `{{ .context.inject.env.DB_URL }}`, "bad-recipe")
	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.Is(err, ErrProvidesSelfContainment()) {
		t.Errorf("expected ErrProvidesSelfContainment, got: %v", err)
	}
}

func TestProvidesSelfContainment_ForbiddenUses(t *testing.T) {
	t.Parallel()
	err := providesSelfContainmentHalt(t, `{{ .context.uses.my_db.provides.host }}`, "bad-recipe")
	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.Is(err, ErrProvidesSelfContainment()) {
		t.Errorf("expected ErrProvidesSelfContainment, got: %v", err)
	}
}

func TestProvidesSelfContainment_StringLiteralOK(t *testing.T) {
	t.Parallel()
	// The word "inject" in a static string (not inside {{ }}) should not trigger
	err := providesSelfContainmentHalt(t, `static string with inject word`, "test-recipe")
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

	result, resultLocals, err := EvaluateAllProvides(graph, recipes, nil, refusalDeclarations{})
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

	result, resultLocals, err := EvaluateAllProvides(graph, recipes, nil, refusalDeclarations{})
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

	result, resultLocals, err := EvaluateAllProvides(graph, recipes, nil, refusalDeclarations{})
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

	_, _, err := EvaluateAllProvides(graph, recipes, nil, refusalDeclarations{})
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

// The self-containment refusal is reported before anything is evaluated, so the
// pass below is not where it is asked. What the pass owes is that the entry still
// reaches the evaluator unchanged when nothing refuses it — the refusal itself is
// proven at the site that asks for it.
func TestProvidesSelfContainment_RefusedEntryIsRefusedAheadOfEvaluation(t *testing.T) {
	t.Parallel()
	err := providesSelfContainmentHalt(t, `{{ .context.inject.env.TARGET }}`, "bad-recipe")
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

	_, _, err := EvaluateAllProvides(graph, recipes, nil, refusalDeclarations{})
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

	_, _, err := EvaluateAllProvides(graph, recipes, nil, refusalDeclarations{})
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

	result, _, err := EvaluateAllProvides(graph, recipes, nil, refusalDeclarations{})
	if err != nil {
		t.Fatalf("unexpected halt on an indirect .locals read: %v", err)
	}
	dbProv := result["db"]
	if got, _ := dbProv.Get("host"); got != "db-main.svc" {
		t.Errorf("indirect read: got %q, want %q", got, "db-main.svc")
	}
}

// A provides template that fails to evaluate halts under the provides-evaluation
// condition, and the halt's own line carries what the evaluation said: the key a
// read did not find and the action that read it. The halt wraps its condition
// rather than the evaluation's error, so that line is the only place the reason
// survives — a halt carrying only its own frame would reach a reader as the node
// and the entry with nothing after them. The line is asserted rather than the
// whole chain because the outermost frame is the one every surface prints,
// however few of the frames beneath it a surface goes on to render.
//
// Each fault row reads an absent key through a different pillar, so the text
// asserted is that row's own answer rather than a constant every failure carries.
// The control is the same read of a key the block holds, and it renders.
func TestEvaluateAllProvides_FailingTemplateHaltCarriesTheReason(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name     string
		template string
		// want is nil for the control, whose output is rendered; otherwise it is
		// the text the halt's own line carries beside the entry it names.
		want     []string
		rendered string
	}{
		{name: "an absent local read through index", template: `{{ index .locals "replica_host" }}.svc`,
			want: []string{`key "replica_host"`, `<index .locals "replica_host">`}},
		{name: "an absent value read through index", template: `{{ index .values "port" }}`,
			want: []string{`key "port"`, `<index .values "port">`}},
		{name: "the control: a local the block holds, read through index",
			template: `{{ index .locals "db_identifier" }}.svc`, rendered: "db-main.svc"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			nodes := types.OrderedMap[types.IRNode]{}
			nodes.Set("db", types.IRNode{})
			vm := types.OrderedMap[json.RawMessage]{}
			vm.Set("db", json.RawMessage(`{"zone":"eu-west-1"}`))
			graph := &types.IRGraph{Spec: types.IRSpec{Nodes: nodes, ValuesMapping: vm}}

			locals := types.OrderedMap[string]{}
			locals.Set("db_identifier", `{{ .context.instance_id }}-main`)
			provides := types.OrderedMap[string]{}
			provides.Set("host", tc.template)
			recipes := map[string]*types.Recipe{
				"db": {Metadata: types.RecipeMetadata{Name: "postgres-recipe"}, Spec: types.RecipeSpec{Locals: locals, Provides: provides}},
			}

			result, _, err := EvaluateAllProvides(graph, recipes, nil, refusalDeclarations{})
			if tc.want == nil {
				if err != nil {
					t.Fatalf("the control halted: %v", err)
				}
				dbProv := result["db"]
				if got, _ := dbProv.Get("host"); got != tc.rendered {
					t.Errorf("the control rendered %q, want %q", got, tc.rendered)
				}
				return
			}
			if err == nil {
				t.Fatalf("%s rendered; want a halt naming what the evaluation could not read", tc.template)
			}
			if !errors.Is(err, ErrProvidesEvaluation()) {
				t.Fatalf("the halt is not the provides-evaluation condition: %v", err)
			}
			line := err.Error()
			for _, want := range append([]string{`provides["host"]`}, tc.want...) {
				if !strings.Contains(line, want) {
					t.Errorf("the halt's line omits %q: %s", want, line)
				}
			}
		})
	}
}

// TestEvaluateAllProvides_FailingTemplateHaltChainsItsInnerCondition pins that the
// provides-evaluation halt carries the condition the evaluation itself named as a
// matchable cause, while the code the halt heads with stays its own.
//
// Both halves are read off the chain a build receives — the halt as the pass
// returns it, wrapped once more as the stage above it wraps it — so the head is
// the one the terminal composes, read through the same composition, and the inner
// condition is matched through its accessor rather than through its code.
func TestEvaluateAllProvides_FailingTemplateHaltChainsItsInnerCondition(t *testing.T) {
	t.Parallel()
	head, _ := cerrors.ShortCodeOf(ErrProvidesEvaluation())
	for _, tc := range []struct {
		name     string
		template string
		inner    error
	}{
		{name: "an absent value read through index", template: `{{ index .values "noport" }}`, inner: gotmpl.ErrIndexOfAbsentMapKey()},
		{name: "an absent local read through index", template: `{{ index .locals "replica_host" }}`, inner: gotmpl.ErrIndexOfAbsentMapKey()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			nodes := types.OrderedMap[types.IRNode]{}
			nodes.Set("db", types.IRNode{})
			vm := types.OrderedMap[json.RawMessage]{}
			vm.Set("db", json.RawMessage(`{"zone":"eu-west-1"}`))
			graph := &types.IRGraph{Spec: types.IRSpec{Nodes: nodes, ValuesMapping: vm}}

			locals := types.OrderedMap[string]{}
			locals.Set("db_identifier", `{{ .context.instance_id }}-main`)
			provides := types.OrderedMap[string]{}
			provides.Set("port", tc.template)
			recipes := map[string]*types.Recipe{
				"db": {Metadata: types.RecipeMetadata{Name: "mariadb-recipe"}, Spec: types.RecipeSpec{Locals: locals, Provides: provides}},
			}

			_, _, err := EvaluateAllProvides(graph, recipes, nil, refusalDeclarations{})
			if err == nil {
				t.Fatalf("%s rendered; want the provides-evaluation halt", tc.template)
			}
			build := cerrors.Wrap(err, cerrors.CatalogError, cerrors.SourceLocation{}, "locals and provides evaluation")

			if !errors.Is(build, ErrProvidesEvaluation()) {
				t.Fatalf("the halt is not the provides-evaluation condition: %v", err)
			}
			if !errors.Is(build, tc.inner) {
				t.Errorf("the condition the evaluation named is not matchable beneath the halt: %s", cerrors.CauseChain(build))
			}
			if got := cerrors.QualifiedShortCodeOf(build); got != head {
				t.Errorf("the halt heads with %q; want its own code %q", got, head)
			}
			wantHead := "[" + string(cerrors.CatalogError) + " " + head + "]"
			if got := cerrors.HeadedCauseChain(build); !strings.HasPrefix(got, wantHead) {
				t.Errorf("the one-line form does not open with %q: %s", wantHead, got)
			}
		})
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

	result, _, err := EvaluateAllProvides(graph, map[string]*types.Recipe{}, nil, refusalDeclarations{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	apiProv := result["api"]
	if apiProv.Len() != 0 {
		t.Errorf("expected empty provides for api, got %v", apiProv.Keys())
	}
}

func TestProvidesSelfContainment_ForbiddenRoots(t *testing.T) {
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
			err := providesSelfContainmentHalt(t, tmpl, "bad-recipe")
			if err == nil {
				t.Fatalf("%s: expected halt, got nil (silent-empty footgun)", name)
			}
			if !errors.Is(err, ErrProvidesSelfContainment()) {
				t.Errorf("%s: expected ErrProvidesSelfContainment, got: %v", name, err)
			}
		})
	}
}

func TestProvidesSelfContainment_AllowedRoots(t *testing.T) {
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
		if err := providesSelfContainmentHalt(t, tmpl, "ok-recipe"); err != nil {
			t.Errorf("expected no error for %q, got: %v", tmpl, err)
		}
	}
}

func TestProvidesSelfContainment_ForbiddenAnnotationHaltsNotEmpty(t *testing.T) {
	t.Parallel()
	// Footgun closure: a provides entry reading a placement annotation HALTS
	// loudly rather than rendering empty. Asked at the site that refuses the
	// entry, which is ahead of evaluation — the value a refused entry would
	// produce is never produced, so the refusal cannot be asked of the evaluator.
	err := providesSelfContainmentHalt(t,
		`{{ .context.instance_id }}-default.{{ index .context.annotations "aggrega.ai/namespace" }}`,
		"footgun-recipe")
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

	result, _, err := EvaluateAllProvides(graph, recipes, nil, refusalDeclarations{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	apiProv := result["api"]
	host, _ := apiProv.Get("host")
	if host != "API" {
		t.Errorf("expected 'API', got %q", host)
	}
}
