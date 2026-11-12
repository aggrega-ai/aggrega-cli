// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package renderer

import (
	"errors"
	"reflect"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/redact"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

func emptyPipe() types.InjectPipe {
	return types.InjectPipe{
		Env:  types.OrderedMap[string]{},
		File: types.OrderedMap[string]{},
		Arg:  types.OrderedMap[string]{},
		All:  types.OrderedMap[string]{},
	}
}

func TestExecutePass2a_LiteralValue(t *testing.T) {
	t.Parallel()

	inject := emptyPipe()
	selfInject := &types.InjectBlock{
		Env: []any{map[string]any{"in": "PORT", "value": "8080"}},
	}

	err := ExecutePass2a(&inject, selfInject, "api", types.OrderedMap[string]{}, SelfInjectContext{}, redact.Keys{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	port, _ := inject.Env.Get("PORT")
	if port != "8080" {
		t.Errorf("PORT = %q, want '8080'", port)
	}
	portAll, _ := inject.All.Get("PORT")
	if portAll != "8080" {
		t.Errorf("all.PORT = %q", portAll)
	}
}

func TestExecutePass2a_FromOwnProvides(t *testing.T) {
	t.Parallel()

	inject := emptyPipe()
	selfInject := &types.InjectBlock{
		Env: []any{
			map[string]any{"in": "MY_HOST", "from": "host"},
		},
	}
	ownProvides := types.OrderedMap[string]{}
	ownProvides.Set("host", "api.svc.cluster.local")

	err := ExecutePass2a(&inject, selfInject, "api", ownProvides, SelfInjectContext{}, redact.Keys{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	host, _ := inject.Env.Get("MY_HOST")
	if host != "api.svc.cluster.local" {
		t.Errorf("MY_HOST = %q", host)
	}
}

func TestExecutePass2a_ValueTemplate(t *testing.T) {
	t.Parallel()

	inject := emptyPipe()
	selfInject := &types.InjectBlock{
		Env: []any{
			map[string]any{"in": "APP_ID", "value": "{{ .context.instance_id }}-app"},
		},
	}
	siCtx := SelfInjectContext{
		Context: SelfInjectContextBlock{InstanceID: "my-api"},
	}

	err := ExecutePass2a(&inject, selfInject, "my-api", types.OrderedMap[string]{}, siCtx, redact.Keys{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	appID, _ := inject.Env.Get("APP_ID")
	if appID != "my-api-app" {
		t.Errorf("APP_ID = %q, want 'my-api-app'", appID)
	}
}

func TestExecutePass2a_ForbiddenReference(t *testing.T) {
	t.Parallel()

	inject := emptyPipe()
	selfInject := &types.InjectBlock{
		Env: []any{
			map[string]any{"in": "BAD", "value": "{{ .context.inject.env.SECRET }}"},
		},
	}

	err := ExecutePass2a(&inject, selfInject, "api", types.OrderedMap[string]{}, SelfInjectContext{}, redact.Keys{})
	if err == nil {
		t.Fatal("expected error for forbidden reference")
	}
	// The inject pipe is not a pillar of this surface's context, so the reference
	// halts as the undefined key it is rather than through a condition naming
	// cross-node data — one condition for every reference the context does not
	// define, on every surface.
	if !errors.Is(err, ErrTemplateUnknownRoot()) {
		t.Errorf("expected ErrTemplateUnknownRoot, got: %v", err)
	}
	// Every frame the diagnostic carries is rendered to the author, so every frame
	// has to name the artifact actually at fault. A component-level inject: block is
	// authored on the Aggregate, so a catalog-tagged frame anywhere in this chain
	// would point the author at a Recipe that is not at fault.
	for _, category := range chainCategories(err) {
		if category != cerrors.DeveloperError {
			t.Errorf("self-inject diagnostic carries a %s frame; every frame on this surface must be %s: %v",
				category, cerrors.DeveloperError, err)
		}
	}
}

func TestExecutePass2a_NilSelfInject(t *testing.T) {
	t.Parallel()

	inject := types.InjectPipe{
		Env:  func() types.OrderedMap[string] { m := types.OrderedMap[string]{}; m.Set("EXISTING", "val"); return m }(),
		File: types.OrderedMap[string]{},
		Arg:  types.OrderedMap[string]{},
		All:  types.OrderedMap[string]{},
	}

	if err := ExecutePass2a(&inject, nil, "api", types.OrderedMap[string]{}, SelfInjectContext{}, redact.Keys{}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	existing, _ := inject.Env.Get("EXISTING")
	if existing != "val" {
		t.Error("existing inject should be preserved")
	}
}

func TestExecutePass2a_FromMissingProvides(t *testing.T) {
	t.Parallel()

	inject := emptyPipe()
	selfInject := &types.InjectBlock{
		Env: []any{
			map[string]any{"in": "X", "from": "nonexistent"},
		},
	}

	err := ExecutePass2a(&inject, selfInject, "api", types.OrderedMap[string]{}, SelfInjectContext{}, redact.Keys{})
	if err == nil {
		t.Fatal("expected error for missing provides key")
	}
}

// TestNormalizeSelfInjectEntries_OrderedMapEntries_Decode pins that a list
// whose entries arrive as *types.OrderedMap[any] — the shape an order-preserving
// decode gives each entry — reads exactly as one whose entries are plain maps,
// and that a mechanism arriving as an ordered map rather than a list yields no
// entry.
func TestNormalizeSelfInjectEntries_OrderedMapEntries_Decode(t *testing.T) {
	t.Parallel()
	entry := func(pairs ...string) *types.OrderedMap[any] {
		m := types.NewOrderedMap[any]()
		for i := 0; i+1 < len(pairs); i += 2 {
			m.Set(pairs[i], pairs[i+1])
		}
		return m
	}
	raw := []any{
		entry("in", "zulu", "value", "literal-1"),
		entry("in", "alpha", "from", "alpha-key"),
		entry("in", "middle", "value", "literal-2"),
	}

	entries := normalizeSelfInjectEntries(raw)
	if entries.Len() != 3 {
		t.Fatalf("expected 3 entries, got %d", entries.Len())
	}
	if got, want := entries.Keys(), []string{"zulu", "alpha", "middle"}; !reflect.DeepEqual(got, want) {
		t.Errorf("entries = %v; want %v, in the order written", got, want)
	}
	if e, ok := entries.Get("zulu"); !ok || e.value != "literal-1" || e.from != "" {
		t.Errorf("zulu: %+v want value=literal-1", e)
	}
	if e, ok := entries.Get("alpha"); !ok || e.from != "alpha-key" || e.value != "" {
		t.Errorf("alpha: %+v want from=alpha-key", e)
	}
	if e, ok := entries.Get("middle"); !ok || e.value != "literal-2" || e.from != "" {
		t.Errorf("middle: %+v want value=literal-2", e)
	}

	notAList := entry("zulu", "literal-1")
	if got := normalizeSelfInjectEntries(notAList); got.Len() != 0 {
		t.Errorf("an ordered map in place of a list: got entries=%v, want no entries", got.Keys())
	}
}

// TestNormalizeSelfInjectEntries_VerboseArrayForm pins the list decode. The
// schema's inject_delivery is a list of {in: K, from?: P, value?: V} entries.
// The `in:` field maps to the entry key; `value:` and `from:` propagate to the
// selfInjectEntry's value / from fields.
func TestNormalizeSelfInjectEntries_VerboseArrayForm(t *testing.T) {
	t.Parallel()

	raw := []any{
		map[string]any{"in": "DB_URL", "from": "connection.url"},
		map[string]any{"in": "PORT", "value": "8080"},
	}

	entries := normalizeSelfInjectEntries(raw)
	if entries.Len() != 2 {
		t.Fatalf("expected 2 entries, got %d", entries.Len())
	}
	if e, ok := entries.Get("DB_URL"); !ok || e.from != "connection.url" {
		t.Errorf("DB_URL: %+v want from=connection.url", e)
	}
	if e, ok := entries.Get("PORT"); !ok || e.value != "8080" {
		t.Errorf("PORT: %+v want value=8080", e)
	}

	emptyEntries := normalizeSelfInjectEntries([]any{})
	if emptyEntries.Len() != 0 {
		t.Errorf("empty []any should produce empty map; got %d entries", emptyEntries.Len())
	}
}

// TestNormalizeSelfInjectEntries_BothFieldsPresent_BlocksTheCaseBeforeResolveRuns
// pins the architectural-intent record: a verbose-form entry that
// simultaneously carries `value:` AND `from:` is schema-illegal (the
// inject_delivery schema's `oneOf: [required:[from], required:[value]]`
// rejects it at envelope load); the rules asked while the documents are
// resolved halt the build with [resolver.ErrInjectEntryAmbiguous] for code
// paths that bypass envelope-load validation (in-memory fixtures, programmatic
// catalog builders). The renderer-layer decode arm still populates both
// selfInjectEntry fields verbatim if the case somehow reaches Pass 2a (e.g., a
// unit test that constructs entries directly without resolving the documents);
// this test documents that direct-construction path and pins
// resolveSelfInjectEntry's downstream behavior in that scenario — `from:`
// resolves first per the function body's if/else cascade. The pin is
// architectural-intent documentation; the case is unreachable in any
// production path through a build because resolution refuses it first.
func TestNormalizeSelfInjectEntries_BothFieldsPresent_BlocksTheCaseBeforeResolveRuns(t *testing.T) {
	t.Parallel()

	verboseRaw := []any{
		map[string]any{"in": "BOTH", "from": "ref.path", "value": "literal-text"},
	}
	entries := normalizeSelfInjectEntries(verboseRaw)
	entry, ok := entries.Get("BOTH")
	if !ok {
		t.Fatalf("entry BOTH missing from decoded entries")
	}
	if entry.value != "literal-text" || entry.from != "ref.path" {
		t.Fatalf("decode should populate both fields verbatim; got value=%q from=%q",
			entry.value, entry.from)
	}

	// Direct-construction scenario: the only path to reach resolveSelfInjectEntry
	// with both fields populated is to skip IR build entirely (this test does so).
	// Under that scenario, the function's if/else cascade picks from: first.
	ownProvides := types.OrderedMap[string]{}
	ownProvides.Set("ref.path", "resolved-from-provides")

	resolved, err := resolveSelfInjectEntry("BOTH", entry, "env", ownProvides, SelfInjectContext{}, redact.Keys{})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if resolved != "resolved-from-provides" {
		t.Errorf("direct-construction scenario: from: should win at resolveSelfInjectEntry; got %q want %q",
			resolved, "resolved-from-provides")
	}
}

// TestAFeatureRecipeOverlayWinsOverAKeyTheComponentInjectedForItself pins the
// overlay's precedence over a delivery key the component's own injection block
// already set, which is the last-wins order the attachment overlay declares.
//
// It is the surviving half of a wider pin. The other half asserted that the
// reading which refused a key delivered from two authoring surfaces could not
// observe this overlay, because the overlay runs after it — and that reading is
// gone: the question is now a rule over the finalised graph, asked of the
// authored blocks before anything renders. The separation is therefore
// structural rather than a matter of ordering inside the render, and stronger
// than the pin it replaces: the overlay is applied while rendering one node and
// the rule reads the graph, so there is no arrangement of this file's code that
// could bring one within reach of the other.
func TestAFeatureRecipeOverlayWinsOverAKeyTheComponentInjectedForItself(t *testing.T) {
	t.Parallel()

	envPipe := types.OrderedMap[string]{}
	envPipe.Set("HOST", "literal")
	allPipe := types.OrderedMap[string]{}
	allPipe.Set("HOST", "literal")
	ctx := &types.TemplateContext{
		Context: types.ContextBlock{
			InstanceID: "api",
			Inject: types.InjectPipe{
				Env:  envPipe,
				File: types.OrderedMap[string]{},
				Arg:  types.OrderedMap[string]{},
				All:  allPipe,
			},
		},
	}
	refs := []FeatureRecipeRef{
		{
			InstanceName: "fr-overlay",
			FeatureRecipe: &types.FeatureRecipe{
				Metadata: types.FeatureRecipeMetadata{Name: "fr-overlay"},
				Spec: types.FeatureRecipeSpec{
					InjectMapping: `{"env": {"HOST": "fr-value"}}`,
				},
			},
		},
	}
	if _, err := ExecutePhaseA(ctx, refs, hostWithheld{}); err != nil {
		t.Fatalf("attachment overlay: %v", err)
	}

	host, _ := ctx.Context.Inject.Env.Get("HOST")
	if host != "fr-value" {
		t.Errorf("HOST after the overlay = %q, want %q — the attachment overlay is applied last and wins",
			host, "fr-value")
	}
}

// TestExecutePass2a_CustomMechanism pins that a mechanism a component names on
// its own inject block outside the three typed ones resolves through the same
// per-key resolution the typed ones use and lands under its own name, reachable
// both per-mechanism and through the union view. Each row fixes one authored
// entry shape: a literal, a provides reference, and a template.
func TestExecutePass2a_CustomMechanism(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		entry   any
		want    string
		wantErr bool
	}{
		{
			name:  "verbose value literal",
			entry: []any{map[string]any{"in": "DB_ARN", "value": "arn:aws:ssm:eu-west-1:param/db"}},
			want:  "arn:aws:ssm:eu-west-1:param/db",
		},
		{
			name:  "verbose from own provides",
			entry: []any{map[string]any{"in": "DB_ARN", "from": "arn"}},
			want:  "arn:aws:rds:eu-west-1:db-main",
		},
		{
			name:  "verbose value template over the restricted context",
			entry: []any{map[string]any{"in": "DB_ARN", "value": "param/{{ .context.instance_id }}"}},
			want:  "param/api",
		},
		{
			// A custom mechanism's provides reference is resolved by the same
			// resolver the typed mechanisms use, so it fails the same way — and
			// every frame names the Aggregate, which is where the block is authored.
			name:    "verbose from an absent provides key halts",
			entry:   []any{map[string]any{"in": "DB_ARN", "from": "nope"}},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			inject := emptyPipe()
			selfInject := &types.InjectBlock{}
			selfInject.Additional.Set("ssm_param", tt.entry)

			ownProvides := types.OrderedMap[string]{}
			ownProvides.Set("arn", "arn:aws:rds:eu-west-1:db-main")
			siCtx := SelfInjectContext{Context: SelfInjectContextBlock{InstanceID: "api"}}

			err := ExecutePass2a(&inject, selfInject, "api", ownProvides, siCtx, redact.Keys{})
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected an error, got nil")
				}
				for _, category := range chainCategories(err) {
					if category != cerrors.DeveloperError {
						t.Errorf("diagnostic carries a %s frame; every frame on this surface must be %s: %v",
							category, cerrors.DeveloperError, err)
					}
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			mechMap, ok := inject.Additional.Get("ssm_param")
			if !ok {
				t.Fatalf("Additional has no %q mechanism; keys = %v", "ssm_param", inject.Additional.Keys())
			}
			if got, _ := mechMap.Get("DB_ARN"); got != tt.want {
				t.Errorf("Additional[%q][%q] = %q, want %q", "ssm_param", "DB_ARN", got, tt.want)
			}
			if got, _ := inject.All.Get("DB_ARN"); got != tt.want {
				t.Errorf("all[%q] = %q, want %q", "DB_ARN", got, tt.want)
			}
			// The three typed views stay clear of a custom mechanism's keys.
			for name, view := range map[string]types.OrderedMap[string]{"env": inject.Env, "file": inject.File, "arg": inject.Arg} {
				if _, present := view.Get("DB_ARN"); present {
					t.Errorf("%s view carries %q, want it only under the custom mechanism", name, "DB_ARN")
				}
			}
			// The mechanism is addressable from a template under its own name.
			untagged := inject.TemplateUntaggedKeys()
			if untagged == nil {
				t.Fatal("TemplateUntaggedKeys() = nil, want the custom mechanism projected")
			}
			if _, ok := untagged["ssm_param"]; !ok {
				t.Errorf("TemplateUntaggedKeys() keys = %v, want one named %q", untagged, "ssm_param")
			}
		})
	}
}

// TestExecutePass2a_CustomMechanismOrderIsSorted pins that custom mechanisms
// resolve in sorted name order whatever order the component names them.
func TestExecutePass2a_CustomMechanismOrderIsSorted(t *testing.T) {
	t.Parallel()

	inject := emptyPipe()
	selfInject := &types.InjectBlock{}
	selfInject.Additional.Set("vault", []any{map[string]any{"in": "V", "value": "v"}})
	selfInject.Additional.Set("azurekv", []any{map[string]any{"in": "A", "value": "a"}})
	selfInject.Additional.Set("ssm_param", []any{map[string]any{"in": "S", "value": "s"}})

	if err := ExecutePass2a(&inject, selfInject, "api", types.OrderedMap[string]{}, SelfInjectContext{}, redact.Keys{}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

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

// TestExecutePass2a_NoCustomMechanismLeavesAdditionalEmpty pins that a component
// naming only the three typed mechanisms adds nothing to the per-mechanism view,
// which is what keeps the rendered output of every input that names no custom
// mechanism unchanged.
func TestExecutePass2a_NoCustomMechanismLeavesAdditionalEmpty(t *testing.T) {
	t.Parallel()

	inject := emptyPipe()
	selfInject := &types.InjectBlock{
		Env:  []any{map[string]any{"in": "PORT", "value": "8080"}},
		File: []any{map[string]any{"in": "CERT", "value": "/etc/cert.pem"}},
		Arg:  []any{map[string]any{"in": "--port", "value": "8080"}},
	}

	if err := ExecutePass2a(&inject, selfInject, "api", types.OrderedMap[string]{}, SelfInjectContext{}, redact.Keys{}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if inject.Additional.Len() != 0 {
		t.Errorf("Additional = %v, want empty", inject.Additional.Keys())
	}
	if inject.TemplateUntaggedKeys() != nil {
		t.Error("TemplateUntaggedKeys() non-nil, want nil so no template key is added")
	}
}
