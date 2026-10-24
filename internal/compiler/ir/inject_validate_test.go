// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package ir

import (
	"errors"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// verboseEntry builds a single schema-verbose-form inject entry as
// map[string]any (the default unmarshal shape).
func verboseEntry(in, value, from string) map[string]any {
	e := map[string]any{"in": in}
	if value != "" {
		e["value"] = value
	}
	if from != "" {
		e["from"] = from
	}
	return e
}

// TestValidateInjectBlock_BothPresent_Error pins the primary detection:
// a verbose-form entry carrying both `value:` AND `from:` returns
// ErrInjectEntryAmbiguous wrapped with the violating field path.
func TestValidateInjectBlock_BothPresent_Error(t *testing.T) {
	t.Parallel()

	b := &types.InjectBlock{
		Env: []any{
			verboseEntry("DB_URL", "literal-text", "connection.url"),
		},
	}

	err := ValidateInjectBlock(b, ".inject")
	if err == nil {
		t.Fatalf("expected error, got nil")
	}
	if !errors.Is(err, ErrInjectEntryAmbiguous()) {
		t.Errorf("error is not ErrInjectEntryAmbiguous: %v", err)
	}
	if !strings.Contains(err.Error(), ".inject.env[0]") {
		t.Errorf("error path missing .inject.env[0]: %v", err)
	}
}

// TestValidateInjectBlock_BothPresent_MultipleMechanisms pins the
// aggregation behavior: both-present at multiple mechanisms produces a
// single error naming every violating path.
func TestValidateInjectBlock_BothPresent_MultipleMechanisms(t *testing.T) {
	t.Parallel()

	b := &types.InjectBlock{
		Env: []any{verboseEntry("E", "v", "ref-e")},
		Arg: []any{verboseEntry("A", "v", "ref-a")},
	}

	err := ValidateInjectBlock(b, ".inject")
	if err == nil {
		t.Fatalf("expected aggregated error, got nil")
	}
	if !errors.Is(err, ErrInjectEntryAmbiguous()) {
		t.Errorf("error is not ErrInjectEntryAmbiguous: %v", err)
	}
	msg := err.Error()
	if !strings.Contains(msg, ".inject.env[0]") || !strings.Contains(msg, ".inject.arg[0]") {
		t.Errorf("aggregated error missing one of the paths: %v", err)
	}
}

// TestValidateInjectBlock_MapShapedEnvIsRefused pins the refusal of a mechanism
// that is not a list: a map-shaped env halts with the delivery-shape condition,
// naming the mechanism's path, and the whole message is the registered text
// followed by that path.
func TestValidateInjectBlock_MapShapedEnvIsRefused(t *testing.T) {
	t.Parallel()

	b := &types.InjectBlock{
		Env: map[string]any{"DB_URL": "connection.url"},
	}

	err := ValidateInjectBlock(b, ".inject")
	if err == nil {
		t.Fatal("a map-shaped env must be refused; got nil")
	}
	if !errors.Is(err, ErrInjectDeliveryShape()) {
		t.Errorf("error is not ErrInjectDeliveryShape: %v", err)
	}
	if errors.Is(err, ErrInjectEntryAmbiguous()) {
		t.Errorf("a shape refusal must not also report an ambiguous entry: %v", err)
	}
	const want = "inject delivery: shape must be a list of entries, each `in:` with exactly one of `from:` or `value:`: .inject.env"
	if got := err.Error(); got != want {
		t.Errorf("message\n got: %s\nwant: %s", got, want)
	}
}

// TestValidateInjectBlock_EveryNonListShapeIsRefusedOnEveryMechanism pins that the
// refusal reads the shape alone — a mapping in any of its decoded forms, a
// scalar — and reaches every mechanism, the three typed ones and one an author
// names beside them, each named at its own path in the order the block holds
// them.
func TestValidateInjectBlock_EveryNonListShapeIsRefusedOnEveryMechanism(t *testing.T) {
	t.Parallel()

	ordered := types.NewOrderedMap[any]()
	ordered.Set("DB_URL", "connection.url")

	for _, tc := range []struct {
		name  string
		value any
	}{
		{"plain map", map[string]any{"DB_URL": "connection.url"}},
		{"string map", map[string]string{"DB_URL": "connection.url"}},
		{"ordered map", ordered},
		{"scalar", "connection.url"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			b := &types.InjectBlock{Env: tc.value, File: tc.value, Arg: tc.value}
			b.Additional.Set("ssm_param", tc.value)

			err := ValidateInjectBlock(b, ".inject")
			if err == nil {
				t.Fatalf("%s on every mechanism must be refused; got nil", tc.name)
			}
			if !errors.Is(err, ErrInjectDeliveryShape()) {
				t.Errorf("error is not ErrInjectDeliveryShape: %v", err)
			}
			const wantPaths = ": .inject.env; .inject.file; .inject.arg; .inject.ssm_param"
			if !strings.HasSuffix(err.Error(), wantPaths) {
				t.Errorf("error must name every mechanism in block order, ending %q; got %v", wantPaths, err)
			}
		})
	}
}

// TestValidateInjectBlock_ListFormValidates pins the other side of the refusal:
// a list on every mechanism — entries as plain or ordered maps, an empty list —
// and a mechanism carrying no value at all pass.
func TestValidateInjectBlock_ListFormValidates(t *testing.T) {
	t.Parallel()

	ordered := types.NewOrderedMap[any]()
	ordered.Set("in", "CERT")
	ordered.Set("value", "/etc/cert.pem")

	b := &types.InjectBlock{
		Env:  []any{verboseEntry("DB_URL", "", "connection.url"), verboseEntry("PORT", "8080", "")},
		File: []any{ordered},
		Arg:  []any{},
	}
	b.Additional.Set("ssm_param", []any{verboseEntry("API_KEY", "", "secret.path")})
	b.Additional.Set("unset", nil)

	if err := ValidateInjectBlock(b, ".inject"); err != nil {
		t.Errorf("the list form must validate; got %v", err)
	}
}

// TestValidateInjectBlock_ShapeAndAmbiguityAreBothReported pins that a block
// carrying both faults reports both, the shape refusal first, so an author sees
// every fault at once.
func TestValidateInjectBlock_ShapeAndAmbiguityAreBothReported(t *testing.T) {
	t.Parallel()

	b := &types.InjectBlock{
		Env: map[string]any{"DB_URL": "connection.url"},
		Arg: []any{verboseEntry("FLAG", "v", "ref")},
	}

	err := ValidateInjectBlock(b, ".inject")
	if !errors.Is(err, ErrInjectDeliveryShape()) || !errors.Is(err, ErrInjectEntryAmbiguous()) {
		t.Fatalf("want both ErrInjectDeliveryShape and ErrInjectEntryAmbiguous; got %v", err)
	}
	msg := err.Error()
	shapeAt, ambiguousAt := strings.Index(msg, ".inject.env"), strings.Index(msg, ".inject.arg[0]")
	if shapeAt < 0 || ambiguousAt < 0 || shapeAt > ambiguousAt {
		t.Errorf("want the shape refusal at .inject.env before the ambiguous entry at .inject.arg[0]; got %v", err)
	}
}

// TestValidateInjectBlocks_MapShapedSelfInjectIsRefusedAtItsPath pins the refusal
// through the walk a build runs: a component's own block carrying a map-shaped
// env halts at the root-shaped locator of that mechanism.
func TestValidateInjectBlocks_MapShapedSelfInjectIsRefusedAtItsPath(t *testing.T) {
	t.Parallel()
	components := types.OrderedMap[types.ComponentInstance]{}
	components.Set("api", types.ComponentInstance{
		Inject: &types.InjectBlock{Env: map[string]any{"DB_URL": "connection.url"}},
	})
	agg := &types.Aggregate{Spec: types.AggregateSpec{Components: components}}

	err := ValidateInjectBlocks(normalized(t, agg), nil)
	if err == nil || !errors.Is(err, ErrInjectDeliveryShape()) {
		t.Fatalf("want ErrInjectDeliveryShape; got %v", err)
	}
	if !strings.Contains(err.Error(), `: .spec.components["api"].inject.env`) {
		t.Errorf("refusal must name the mechanism's path; got %v", err)
	}
}

// TestValidateInjectBlock_VerboseOnlyFrom_OK pins the verbose-from-only path.
func TestValidateInjectBlock_VerboseOnlyFrom_OK(t *testing.T) {
	t.Parallel()

	b := &types.InjectBlock{
		Env: []any{verboseEntry("DB_URL", "", "connection.url")},
	}

	if err := ValidateInjectBlock(b, ".inject"); err != nil {
		t.Errorf("verbose-from-only should not error; got %v", err)
	}
}

// TestValidateInjectBlock_VerboseOnlyValue_OK pins the verbose-value-only path.
func TestValidateInjectBlock_VerboseOnlyValue_OK(t *testing.T) {
	t.Parallel()

	b := &types.InjectBlock{
		Env: []any{verboseEntry("PORT", "8080", "")},
	}

	if err := ValidateInjectBlock(b, ".inject"); err != nil {
		t.Errorf("verbose-value-only should not error; got %v", err)
	}
}

// TestValidateInjectBlock_MalformedEntries_OK pins the out-of-scope contract:
// entries of a list lacking `in:` or carrying a non-map shape are NOT flagged
// by this validator; every reader of a mechanism skips them.
func TestValidateInjectBlock_MalformedEntries_OK(t *testing.T) {
	t.Parallel()

	b := &types.InjectBlock{
		Env: []any{
			map[string]any{"value": "missing-in"},
			"not-a-map",
			map[string]any{"in": "", "value": "empty-in"},
		},
	}

	if err := ValidateInjectBlock(b, ".inject"); err != nil {
		t.Errorf("malformed entries should be silently skipped; got %v", err)
	}
}

// TestValidateInjectBlock_AdditionalMechanisms pins that both-present on a
// custom (Additional) mechanism is caught — the validator walks Additional
// alongside env/file/arg.
func TestValidateInjectBlock_AdditionalMechanisms(t *testing.T) {
	t.Parallel()

	b := &types.InjectBlock{}
	b.Additional.Set("ssm_param", []any{verboseEntry("API_KEY", "literal", "secret.path")})

	err := ValidateInjectBlock(b, ".inject")
	if err == nil {
		t.Fatalf("expected error on custom-mechanism both-present, got nil")
	}
	if !errors.Is(err, ErrInjectEntryAmbiguous()) {
		t.Errorf("error is not ErrInjectEntryAmbiguous: %v", err)
	}
	if !strings.Contains(err.Error(), ".inject.ssm_param[0]") {
		t.Errorf("error path missing .inject.ssm_param[0]: %v", err)
	}
}

// TestValidateInjectBlocks_NilAggregate pins the silent-short-circuit
// contract for nil inputs — IR build's existing nil-handling already
// surfaces those upstream.
func TestValidateInjectBlocks_NilAggregate(t *testing.T) {
	t.Parallel()

	if err := ValidateInjectBlocks(normalized(t, nil), nil); err != nil {
		t.Errorf("nil Aggregate should short-circuit silently; got %v", err)
	}
}

// TestValidateAggregate_TierThreeFeatureInject_BothPresent pins the
// aggregate-level walker's coverage of Tier 3+ surfaces: a both-present
// InjectBlock inside a subsystem feature (Tier 3 in the documentation
// tiering) is caught. The walker's source-walking approach (over
// Aggregate.Spec.Subsystems recursively) covers Tier 3+ features that the
// topology.FeatureAttachments structure misses (it carries Tier 1+2 only).
func TestValidateAggregate_TierThreeFeatureInject_BothPresent(t *testing.T) {
	t.Parallel()

	// Build an Aggregate carrying a subsystem with a feature whose Inject
	// has the both-present violation. Source-walk path:
	//   Aggregate.Spec.Subsystems["data"].Features["audit"].Inject
	subFeatures := types.OrderedMap[types.FeatureInstance]{}
	subFeatures.Set("audit", types.FeatureInstance{
		Type: "core/audit-logging",
		Inject: &types.InjectBlock{
			Env: []any{verboseEntry("AUDIT_KEY", "literal-text", "ref.path")},
		},
	})
	subsystem := types.Subsystem{
		Features: subFeatures,
	}
	subsystems := types.OrderedMap[types.Subsystem]{}
	subsystems.Set("data", subsystem)

	agg := &types.Aggregate{
		Spec: types.AggregateSpec{
			Subsystems: subsystems,
		},
	}

	err := ValidateInjectBlocks(normalized(t, agg), nil)
	if err == nil {
		t.Fatalf("expected error on Tier 3 feature inject both-present, got nil")
	}
	if !errors.Is(err, ErrInjectEntryAmbiguous()) {
		t.Errorf("error is not ErrInjectEntryAmbiguous: %v", err)
	}
	msg := err.Error()
	if !strings.Contains(msg, `subsystems["data"]`) ||
		!strings.Contains(msg, `features["audit"]`) ||
		!strings.Contains(msg, ".env[0]") {
		t.Errorf("error path missing Tier 3 feature signature: %v", err)
	}
}

// TestValidateInjectBlocks_SynthesizedDefaultElidesPath is the A3 elision
// red/green: a both-present inject violation on a top-level component (folded
// into the synthesized default) renders the root-shaped locator, never under the
// generated `default` scope. The IR keeps the literal default scope; this elides
// only the diagnostic locator string.
func TestValidateInjectBlocks_SynthesizedDefaultElidesPath(t *testing.T) {
	t.Parallel()
	components := types.OrderedMap[types.ComponentInstance]{}
	components.Set("api", types.ComponentInstance{
		Inject: &types.InjectBlock{
			Env: []any{verboseEntry("DB_URL", "literal", "ref.path")},
		},
	})
	agg := &types.Aggregate{Spec: types.AggregateSpec{Components: components}}

	err := ValidateInjectBlocks(normalized(t, agg), nil)
	if err == nil || !errors.Is(err, ErrInjectEntryAmbiguous()) {
		t.Fatalf("want ErrInjectEntryAmbiguous; got %v", err)
	}
	msg := err.Error()
	if !strings.Contains(msg, `.spec.components["api"].inject`) {
		t.Errorf("synthesized-default inject diagnostic must render root-shaped; got %v", err)
	}
	if strings.Contains(msg, "subsystems") {
		t.Errorf("synthesized default must not leak into the inject locator: %v", err)
	}
}

// TestValidateAggregate_NestedSubsystems pins recursive coverage: a
// both-present InjectBlock inside a nested subsystem (subsystems within
// subsystems) is caught.
func TestValidateAggregate_NestedSubsystems(t *testing.T) {
	t.Parallel()

	innerComponents := types.OrderedMap[types.ComponentInstance]{}
	innerComponents.Set("worker", types.ComponentInstance{
		Inject: &types.InjectBlock{
			Env: []any{verboseEntry("X", "v", "ref")},
		},
	})
	innerSub := types.Subsystem{Components: innerComponents}

	outerSubs := types.OrderedMap[types.Subsystem]{}
	outerSubs.Set("inner", innerSub)
	outerSub := types.Subsystem{Subsystems: outerSubs}

	subsystems := types.OrderedMap[types.Subsystem]{}
	subsystems.Set("outer", outerSub)

	agg := &types.Aggregate{Spec: types.AggregateSpec{Subsystems: subsystems}}

	err := ValidateInjectBlocks(normalized(t, agg), nil)
	if err == nil {
		t.Fatalf("expected error on nested-subsystem component-inject both-present, got nil")
	}
	if !strings.Contains(err.Error(), `subsystems["outer"].subsystems["inner"].components["worker"].inject.env[0]`) {
		t.Errorf("error path missing nested-subsystem signature: %v", err)
	}
}

// TestValidateAggregate_FeatureInstance_NestedUses pins that the walker
// reaches both-present InjectBlock instances nested inside a feature's
// own Uses substructure — FeatureUsesEntry.Inject, reached via
// FeatureInstance.Uses rather than directly via ComponentInstance.Uses.
func TestValidateAggregate_FeatureInstance_NestedUses(t *testing.T) {
	t.Parallel()

	verbose := types.OrderedMap[types.FeatureUsesEntry]{}
	verbose.Set("alias", types.FeatureUsesEntry{
		Target: "api",
		Inject: &types.InjectBlock{
			Env: []any{verboseEntry("K", "v", "ref")},
		},
	})
	uses := types.OrderedMap[types.FeatureUsesSocketValue]{}
	uses.Set("scrape_target", types.FeatureUsesSocketValue{Verbose: verbose})

	features := types.OrderedMap[types.FeatureInstance]{}
	features.Set("metrics", types.FeatureInstance{
		Type: "core/prometheus-exporter",
		Uses: uses,
	})

	components := types.OrderedMap[types.ComponentInstance]{}
	components.Set("api", types.ComponentInstance{Features: features})

	agg := &types.Aggregate{Spec: types.AggregateSpec{Components: components}}

	err := ValidateInjectBlocks(normalized(t, agg), nil)
	if err == nil {
		t.Fatalf("expected error on feature-instance nested-uses both-present, got nil")
	}
	if !errors.Is(err, ErrInjectEntryAmbiguous()) {
		t.Errorf("error is not ErrInjectEntryAmbiguous: %v", err)
	}
	if !strings.Contains(err.Error(), `components["api"].features["metrics"].uses["scrape_target"]["alias"].inject.env[0]`) {
		t.Errorf("error path missing feature-uses signature: %v", err)
	}
}

// TestValidateAggregate_FeatureInstance_NestedSecrets pins that the walker
// reaches both-present InjectBlock instances nested inside a feature's
// Secrets substructure.
// TestValidateAggregate_CredentialAttachment_NestedUses pins that the walker
// reaches both-present InjectBlock instances nested inside a credential
// attachment's own Uses substructure. A credential is declared on the one
// attachment surface, so it is reached through the same walk as any other
// attachment — which is exactly why there is no second walk to forget.
func TestValidateAggregate_CredentialAttachment_NestedUses(t *testing.T) {
	t.Parallel()

	verbose := types.OrderedMap[types.FeatureUsesEntry]{}
	verbose.Set("alias", types.FeatureUsesEntry{
		Target: "vault",
		Inject: &types.InjectBlock{
			Env: []any{verboseEntry("VAULT_TOKEN", "v", "ref")},
		},
	})
	uses := types.OrderedMap[types.FeatureUsesSocketValue]{}
	uses.Set("backend", types.FeatureUsesSocketValue{Verbose: verbose})

	secrets := types.OrderedMap[types.FeatureInstance]{}
	secrets.Set("db-creds", types.FeatureInstance{
		Type: "core/secret-value",
		Uses: uses,
	})

	components := types.OrderedMap[types.ComponentInstance]{}
	components.Set("api", types.ComponentInstance{Features: secrets})

	agg := &types.Aggregate{Spec: types.AggregateSpec{Components: components}}

	err := ValidateInjectBlocks(normalized(t, agg), nil)
	if err == nil {
		t.Fatalf("expected error on secret-instance nested-uses both-present, got nil")
	}
	if !errors.Is(err, ErrInjectEntryAmbiguous()) {
		t.Errorf("error is not ErrInjectEntryAmbiguous: %v", err)
	}
	if !strings.Contains(err.Error(), `components["api"].features["db-creds"].uses["backend"]["alias"].inject.env[0]`) {
		t.Errorf("error path missing attachment-uses signature: %v", err)
	}
}
