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
	if !errors.Is(err, ErrInjectEntryAmbiguous) {
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
	if !errors.Is(err, ErrInjectEntryAmbiguous) {
		t.Errorf("error is not ErrInjectEntryAmbiguous: %v", err)
	}
	msg := err.Error()
	if !strings.Contains(msg, ".inject.env[0]") || !strings.Contains(msg, ".inject.arg[0]") {
		t.Errorf("aggregated error missing one of the paths: %v", err)
	}
}

// TestValidateInjectBlock_CompactForm_OK pins that map-shaped (compact-form)
// mechanism payloads are silently accepted — compact form cannot
// structurally express the both-present case. (Schema validation rejects
// the compact form itself at envelope load post-flip; this validator stays
// silent on map shapes to avoid duplicating that check.)
func TestValidateInjectBlock_CompactForm_OK(t *testing.T) {
	t.Parallel()

	b := &types.InjectBlock{
		Env: map[string]any{"DB_URL": "connection.url"},
	}

	if err := ValidateInjectBlock(b, ".inject"); err != nil {
		t.Errorf("compact-form should not error; got %v", err)
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
// entries lacking `in:` or carrying a non-map shape are NOT flagged by this
// validator (decode arms skip them silently; surfacing those as errors here
// is a separate concern for a future hardening pass).
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
	if !errors.Is(err, ErrInjectEntryAmbiguous) {
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

	if err := ValidateInjectBlocks(nil, nil); err != nil {
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

	err := ValidateInjectBlocks(agg, nil)
	if err == nil {
		t.Fatalf("expected error on Tier 3 feature inject both-present, got nil")
	}
	if !errors.Is(err, ErrInjectEntryAmbiguous) {
		t.Errorf("error is not ErrInjectEntryAmbiguous: %v", err)
	}
	msg := err.Error()
	if !strings.Contains(msg, `subsystems["data"]`) ||
		!strings.Contains(msg, `features["audit"]`) ||
		!strings.Contains(msg, ".env[0]") {
		t.Errorf("error path missing Tier 3 feature signature: %v", err)
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

	err := ValidateInjectBlocks(agg, nil)
	if err == nil {
		t.Fatalf("expected error on nested-subsystem component-inject both-present, got nil")
	}
	if !strings.Contains(err.Error(), `subsystems["outer"].subsystems["inner"].components["worker"].inject.env[0]`) {
		t.Errorf("error path missing nested-subsystem signature: %v", err)
	}
}

// TestValidateAggregate_FeatureInstance_NestedUses pins that the walker
// reaches both-present InjectBlock instances nested inside a feature's
// own Uses substructure — same surface type (VerboseUsesEntry.Inject)
// reached via FeatureInstance.Uses rather than directly via
// ComponentInstance.Uses.
func TestValidateAggregate_FeatureInstance_NestedUses(t *testing.T) {
	t.Parallel()

	verbose := types.OrderedMap[types.VerboseUsesEntry]{}
	verbose.Set("alias", types.VerboseUsesEntry{
		Target: "api",
		Inject: &types.InjectBlock{
			Env: []any{verboseEntry("K", "v", "ref")},
		},
	})
	uses := types.OrderedMap[types.UsesSocketValue]{}
	uses.Set("scrape_target", types.UsesSocketValue{Verbose: verbose})

	features := types.OrderedMap[types.FeatureInstance]{}
	features.Set("metrics", types.FeatureInstance{
		Type: "core/prometheus-exporter",
		Uses: uses,
	})

	components := types.OrderedMap[types.ComponentInstance]{}
	components.Set("api", types.ComponentInstance{Features: features})

	agg := &types.Aggregate{Spec: types.AggregateSpec{Components: components}}

	err := ValidateInjectBlocks(agg, nil)
	if err == nil {
		t.Fatalf("expected error on feature-instance nested-uses both-present, got nil")
	}
	if !errors.Is(err, ErrInjectEntryAmbiguous) {
		t.Errorf("error is not ErrInjectEntryAmbiguous: %v", err)
	}
	if !strings.Contains(err.Error(), `components["api"].features["metrics"].uses["scrape_target"]["alias"].inject.env[0]`) {
		t.Errorf("error path missing feature-uses signature: %v", err)
	}
}

// TestValidateAggregate_FeatureInstance_NestedSecrets pins that the walker
// reaches both-present InjectBlock instances nested inside a feature's
// Secrets substructure.
func TestValidateAggregate_FeatureInstance_NestedSecrets(t *testing.T) {
	t.Parallel()

	featureSecrets := types.OrderedMap[types.SecretInstance]{}
	featureSecrets.Set("password", types.SecretInstance{
		Type: "core/secret-value",
		Inject: &types.InjectBlock{
			Env: []any{verboseEntry("PASSWORD", "v", "ref")},
		},
	})

	features := types.OrderedMap[types.FeatureInstance]{}
	features.Set("metrics", types.FeatureInstance{
		Type:    "core/prometheus-exporter",
		Secrets: featureSecrets,
	})

	components := types.OrderedMap[types.ComponentInstance]{}
	components.Set("api", types.ComponentInstance{Features: features})

	agg := &types.Aggregate{Spec: types.AggregateSpec{Components: components}}

	err := ValidateInjectBlocks(agg, nil)
	if err == nil {
		t.Fatalf("expected error on feature-instance nested-secrets both-present, got nil")
	}
	if !errors.Is(err, ErrInjectEntryAmbiguous) {
		t.Errorf("error is not ErrInjectEntryAmbiguous: %v", err)
	}
	if !strings.Contains(err.Error(), `components["api"].features["metrics"].secrets["password"].inject.env[0]`) {
		t.Errorf("error path missing feature-secrets signature: %v", err)
	}
}

// TestValidateAggregate_SecretInstance_NestedUses pins that the walker
// reaches both-present InjectBlock instances nested inside a secret's
// own Uses substructure — same surface type as the
// FeatureInstance-nested-uses path but reached via SecretInstance.Uses.
func TestValidateAggregate_SecretInstance_NestedUses(t *testing.T) {
	t.Parallel()

	verbose := types.OrderedMap[types.VerboseUsesEntry]{}
	verbose.Set("alias", types.VerboseUsesEntry{
		Target: "vault",
		Inject: &types.InjectBlock{
			Env: []any{verboseEntry("VAULT_TOKEN", "v", "ref")},
		},
	})
	uses := types.OrderedMap[types.UsesSocketValue]{}
	uses.Set("backend", types.UsesSocketValue{Verbose: verbose})

	secrets := types.OrderedMap[types.SecretInstance]{}
	secrets.Set("db-creds", types.SecretInstance{
		Type: "core/secret-value",
		Uses: uses,
	})

	components := types.OrderedMap[types.ComponentInstance]{}
	components.Set("api", types.ComponentInstance{Secrets: secrets})

	agg := &types.Aggregate{Spec: types.AggregateSpec{Components: components}}

	err := ValidateInjectBlocks(agg, nil)
	if err == nil {
		t.Fatalf("expected error on secret-instance nested-uses both-present, got nil")
	}
	if !errors.Is(err, ErrInjectEntryAmbiguous) {
		t.Errorf("error is not ErrInjectEntryAmbiguous: %v", err)
	}
	if !strings.Contains(err.Error(), `components["api"].secrets["db-creds"].uses["backend"]["alias"].inject.env[0]`) {
		t.Errorf("error path missing secret-uses signature: %v", err)
	}
}
