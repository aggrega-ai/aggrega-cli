// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Tests for the static reading of which template outputs were computed from a
// declared-sensitive datum.
//
// Every case is a PAIR: a template shape that must be withheld sits beside a
// structurally identical template shape that must not be, differing only in
// whether the path it reads is declared. A withholding rule that fires on
// everything passes each positive case on its own, so the negative half is what
// makes the positive half mean anything.
//
// The matrix is per template SHAPE rather than per representative, because a
// clause dropped from one shape's branch leaves every other shape passing.

package renderer

import (
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/redact"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// taintFixtureValues / taintFixtureInfra are the declared leaf paths every case
// below is judged against: a top-level leaf, a leaf under an object, and a
// declaration ON an object (whose whole subtree is therefore withheld).
func taintFixtureValues() map[string]bool {
	return map[string]bool{
		"dbPassword":       true,
		"creds.adminToken": true,
		"tuning":           true,
	}
}

func taintFixtureInfra() map[string]bool {
	return map[string]bool{"apiKey": true}
}

func recipeWithLocals(pairs ...string) *types.Recipe {
	r := &types.Recipe{Metadata: types.RecipeMetadata{Name: "taint-recipe", Version: "1.0.0"}}
	for i := 0; i+1 < len(pairs); i += 2 {
		r.Spec.Locals.Set(pairs[i], pairs[i+1])
	}
	return r
}

// withheldLocal is the single-local convenience: build a Recipe with one local
// carrying tmplText and report whether the reading withholds it.
func withheldLocal(tmplText string) bool {
	derived := derivedSensitiveKeys(recipeWithLocals("subject", tmplText),
		taintFixtureValues(), taintFixtureInfra())
	return derived.Locals["subject"]
}

func TestDerivedSensitiveKeys_PerTemplateShape(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name     string
		withheld string // the shape that must be withheld
		control  string // the same shape over an undeclared path
	}{
		{
			name:     "direct values leaf",
			withheld: `postgres://app:{{ .values.dbPassword }}@db:5432/appdb`,
			control:  `postgres://app@{{ .values.appName }}:5432/appdb`,
		},
		{
			name:     "direct infra leaf",
			withheld: `key={{ .infra.apiKey }}`,
			control:  `region={{ .infra.region }}`,
		},
		{
			name:     "leaf under an object",
			withheld: `{{ .values.creds.adminToken }}`,
			control:  `{{ .values.creds.adminUser }}`,
		},
		{
			name: "descendant of a declaration on the object itself",
			// `tuning` is declared, so a read of any leaf beneath it reads
			// something the pillar display already withholds wholesale.
			withheld: `{{ .values.tuning.poolSize }}`,
			control:  `{{ .values.sizing.poolSize }}`,
		},
		{
			name: "whole-pillar read, which is what a dynamic key resolves to",
			// `index .values $k` cannot be resolved to a key statically; the
			// reference is the pillar, which reaches every declared leaf under it.
			withheld: `{{ index .values "dbPassword" }}`,
			control:  `{{ index .provides "host" }}`,
		},
		{
			name:     "range over an object holding a declared leaf",
			withheld: `{{ range $k, $v := .values.creds }}{{ $k }}={{ $v }};{{ end }}`,
			control:  `{{ range $k, $v := .values.ports }}{{ $k }}={{ $v }};{{ end }}`,
		},
		{
			name: "read of a rebound dot inside a with body",
			// The withheld half is caught by the pipeline reference; the control
			// proves a rebound read alone does not withhold, so the rule is not
			// simply "anything inside a body".
			withheld: `{{ with .values.creds }}{{ .adminToken }}{{ end }}`,
			control:  `{{ with .values.ports }}{{ .adminToken }}{{ end }}`,
		},
		{
			name:     "root-anchored variable inside a rebound body",
			withheld: `{{ range .values.ports }}{{ $.values.dbPassword }}{{ end }}`,
			control:  `{{ range .values.ports }}{{ $.values.appName }}{{ end }}`,
		},
		{
			name:     "chained expression whose receiver reaches the declaration",
			withheld: `{{ (default dict .values.creds).adminToken }}`,
			control:  `{{ (default dict .values.ports).adminToken }}`,
		},
		{
			name:     "pipeline argument",
			withheld: `{{ .values.dbPassword | upper }}`,
			control:  `{{ .values.appName | upper }}`,
		},
		{
			name:     "conditional predicate",
			withheld: `{{ if .values.dbPassword }}set{{ else }}unset{{ end }}`,
			control:  `{{ if .values.appName }}set{{ else }}unset{{ end }}`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if !withheldLocal(tc.withheld) {
				t.Errorf("must be withheld, was not: %s", tc.withheld)
			}
			if withheldLocal(tc.control) {
				t.Errorf("must NOT be withheld, was: %s", tc.control)
			}
		})
	}
}

func TestDerivedSensitiveKeys_TemplateVariableIsNotTheRoot(t *testing.T) {
	t.Parallel()
	// A variable the template declared names a value of its own, so it reaches no
	// authored key however it is spelled — only `$` names the context root.
	if withheldLocal(`{{ $own := .values.appName }}{{ $own.dbPassword }}`) {
		t.Error("a template-declared variable must not read as a root reference")
	}
}

func TestDerivedSensitiveKeys_WithholdingIsTransitiveAcrossLocals(t *testing.T) {
	t.Parallel()
	// Author order matters: `first` is withheld, `second` reads it and inherits the
	// withholding, `third` reads only the untouched one and does not.
	derived := derivedSensitiveKeys(recipeWithLocals(
		"first", `{{ .values.dbPassword }}`,
		"plain", `{{ .values.appName }}`,
		"second", `wrapped:{{ .locals.first }}`,
		"third", `wrapped:{{ .locals.plain }}`,
	), taintFixtureValues(), taintFixtureInfra())

	for _, want := range []string{"first", "second"} {
		if !derived.Locals[want] {
			t.Errorf("%q must be withheld; got %v", want, derived.Locals)
		}
	}
	for _, notWant := range []string{"plain", "third"} {
		if derived.Locals[notWant] {
			t.Errorf("%q must not be withheld; got %v", notWant, derived.Locals)
		}
	}
}

func TestDerivedSensitiveKeys_WholeLocalsPillarRead(t *testing.T) {
	t.Parallel()
	// A read of the whole pillar reaches every entry in it, so it inherits from any
	// withheld sibling above it.
	derived := derivedSensitiveKeys(recipeWithLocals(
		"first", `{{ .values.dbPassword }}`,
		"census", `{{ range $k, $v := .locals }}{{ $k }};{{ end }}`,
	), taintFixtureValues(), taintFixtureInfra())
	if !derived.Locals["census"] {
		t.Errorf("a whole-pillar locals read must inherit from a withheld sibling; got %v", derived.Locals)
	}

	// The control: with no withheld sibling above it, the same read withholds nothing.
	control := derivedSensitiveKeys(recipeWithLocals(
		"plain", `{{ .values.appName }}`,
		"census", `{{ range $k, $v := .locals }}{{ $k }};{{ end }}`,
	), taintFixtureValues(), taintFixtureInfra())
	if control.Locals["census"] {
		t.Errorf("a whole-pillar locals read over untouched siblings must not be withheld; got %v", control.Locals)
	}
}

func TestDerivedSensitiveKeys_UnresolvableTextIsWithheld(t *testing.T) {
	t.Parallel()
	// A template that does not parse yields no reference set. Over-withholding is
	// the chosen direction, so the unresolvable case is withheld rather than
	// assumed clean.
	if !withheldLocal(`{{ unclosed`) {
		t.Error("a template that does not parse must be withheld")
	}
	// Text with no actions reads nothing at all and is a different case.
	if withheldLocal(`a plain literal`) {
		t.Error("text carrying no template action reads nothing and must not be withheld")
	}
}

func TestDerivedSensitiveKeys_NonPillarRootsReachNothing(t *testing.T) {
	t.Parallel()
	for _, tmpl := range []string{
		`{{ .context.instance_id }}`,
		`{{ .attachment_name }}`,
	} {
		if withheldLocal(tmpl) {
			t.Errorf("a root no declaration covers must not be withheld: %s", tmpl)
		}
	}
}

func TestDerivedSensitiveKeys_NothingDeclaredReadsNothing(t *testing.T) {
	t.Parallel()
	// With no declaration there is nothing a template could have computed FROM, so
	// the reading short-circuits and no template is parsed.
	derived := derivedSensitiveKeys(recipeWithLocals("dsn", `{{ .values.dbPassword }}`), nil, nil)
	if len(derived.Locals) != 0 || len(derived.Source) != 0 {
		t.Errorf("nothing declared must read nothing; got %+v", derived)
	}
	if derived := derivedSensitiveKeys(nil, taintFixtureValues(), taintFixtureInfra()); len(derived.Locals) != 0 {
		t.Errorf("a node with no bound Recipe must read nothing; got %+v", derived)
	}
}

func TestDerivedSensitiveKeys_SourceStringShape(t *testing.T) {
	t.Parallel()
	r := recipeWithLocals()
	r.Spec.Source = `oci://ghcr.io/app:{{ .values.dbPassword }}`
	derived := derivedSensitiveKeys(r, taintFixtureValues(), taintFixtureInfra())
	if !derived.Source[redact.WholeSourceKey] {
		t.Errorf("a withheld string source must be keyed by the whole-source key; got %v", derived.Source)
	}

	control := recipeWithLocals()
	control.Spec.Source = `oci://ghcr.io/app:{{ .values.appName }}`
	if got := derivedSensitiveKeys(control, taintFixtureValues(), taintFixtureInfra()); len(got.Source) != 0 {
		t.Errorf("a string source reading nothing declared must not be withheld; got %v", got.Source)
	}
}

// TestWorkloadReadsWithheld_PerRootAndDepth is the matrix for the whole-projection
// reading. It runs per ROOT and per DEPTH rather than on a representative, because
// the failure this file exists to catch is a clause dropped from one branch while
// every other branch keeps passing.
//
// Every withheld case sits beside a control of the same shape over a path nothing
// withholds, so a rule that fired on everything would fail the control half.
func TestWorkloadReadsWithheld_PerRootAndDepth(t *testing.T) {
	t.Parallel()
	keys := redact.Keys{
		Values:       taintFixtureValues(),
		Infra:        taintFixtureInfra(),
		Locals:       map[string]bool{"zzDsn": true},
		Inject:       map[string]map[string]bool{"env": {"DATABASE_PASSWORD": true}},
		UsesProvides: map[string]map[string]bool{"storeEdge": {"zzTargetSecret": true}},
	}

	cases := []struct {
		name     string
		withheld string
		control  string
	}{
		{
			name:     "own values leaf",
			withheld: `{{ .values.dbPassword }}`,
			control:  `{{ .values.appName }}`,
		},
		{
			name:     "own withheld local",
			withheld: `{{ .locals.zzDsn }}`,
			control:  `{{ .locals.appLabel }}`,
		},
		{
			name:     "delivery key at full depth",
			withheld: `{{ .context.inject.env.DATABASE_PASSWORD }}`,
			control:  `{{ .context.inject.env.DATABASE_NAME }}`,
		},
		{
			name:     "delivery mechanism, one level short of the key",
			withheld: `{{ range $k, $v := .context.inject.env }}{{ $v }}{{ end }}`,
			control:  `{{ range $k, $v := .context.inject.file }}{{ $v }}{{ end }}`,
		},
		{
			name:     "consumed target's provides key at full depth",
			withheld: `{{ .context.uses.storeEdge.provides.zzTargetSecret }}`,
			control:  `{{ .context.uses.plainEdge.provides.zzTargetSecret }}`,
		},
		{
			name:     "consumed target's provides bag, one level short of the key",
			withheld: `{{ range $k, $v := .context.uses.storeEdge.provides }}{{ $v }}{{ end }}`,
			control:  `{{ range $k, $v := .context.uses.plainEdge.provides }}{{ $v }}{{ end }}`,
		},
		{
			name:     "the whole edge, two levels short of the key",
			withheld: `{{ .context.uses.storeEdge }}`,
			control:  `{{ .context.uses.plainEdge }}`,
		},
		{
			name: "an edge field a provides declaration does not govern",
			// Per-connection config is authored on the edge, not published by the
			// target, so the target's provides declaration does not reach it.
			withheld: `{{ .context.uses.storeEdge.provides.zzTargetSecret }}`,
			control:  `{{ .context.uses.storeEdge.values.protocol }}`,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			if !workloadReadsWithheld(c.withheld, keys) {
				t.Errorf("must be withheld: %s", c.withheld)
			}
			if workloadReadsWithheld(c.control, keys) {
				t.Errorf("must NOT be withheld: %s", c.control)
			}
		})
	}
}

// TestWorkloadReadsWithheld_DynamicAliasLookupIsConservative pins the deliberate
// over-withholding, which has no control half because there cannot be one.
//
// A lookup through `index` does not attribute the alias: the reference census
// reports the whole uses pillar, so the read is indistinguishable from a read of
// every edge — including the marked one. Withholding is the direction that costs a
// reader one value they must find another way, where the alternative prints a
// secret. The dotted form above IS attributed, so an author who wants the precise
// answer has a shape that gives it.
func TestWorkloadReadsWithheld_DynamicAliasLookupIsConservative(t *testing.T) {
	t.Parallel()
	keys := redact.Keys{UsesProvides: map[string]map[string]bool{"storeEdge": {"zzTargetSecret": true}}}

	// Both are withheld, including the one naming only the UNMARKED alias.
	for _, tmpl := range []string{
		`{{ (index .context.uses "storeEdge").provides.zzTargetSecret }}`,
		`{{ (index .context.uses "plainEdge").provides.zzTargetSecret }}`,
	} {
		if !workloadReadsWithheld(tmpl, keys) {
			t.Errorf("a dynamic alias lookup must take the conservative answer: %s", tmpl)
		}
	}
	// The same conservatism does not leak across pillars: a dynamic lookup into a
	// pillar nothing withholds is still not withheld.
	if workloadReadsWithheld(`{{ index .context.inject.env "ANY" }}`, keys) {
		t.Error("a pillar the set names nothing in must not be withheld")
	}
}

// TestWorkloadReadsWithheld_EmptySetShortCircuits is the escape hatch's structural
// half. The capture mode turns withholding off by emptying the set, so a reading
// that consulted the template rather than the set would keep firing under a mode
// meant to show everything. It short-circuits before the parse, which is why an
// unparseable template — withheld under any non-empty set — is not withheld here.
func TestWorkloadReadsWithheld_EmptySetShortCircuits(t *testing.T) {
	t.Parallel()
	for _, tmpl := range []string{
		`{{ .values.dbPassword }}`,
		`{{ .context.inject.env.DATABASE_PASSWORD }}`,
		`{{ .context.uses.storeEdge.provides.zzTargetSecret }}`,
		`{{ .values.dbPassword`, // unparseable
	} {
		if workloadReadsWithheld(tmpl, redact.Keys{}) {
			t.Errorf("an empty set withholds nothing, so nothing can be read from it: %s", tmpl)
		}
	}
	// The control: the same unparseable text IS withheld once anything is withheld,
	// because the reference set cannot be recovered and the conservative direction
	// is to withhold.
	if !workloadReadsWithheld(`{{ .values.dbPassword`, redact.Keys{Values: taintFixtureValues()}) {
		t.Error("an unparseable template must be withheld whenever the set names anything")
	}
	// A set that carries ONLY the display decision must not feed the reading its
	// own result.
	if workloadReadsWithheld(`{{ .values.dbPassword }}`, redact.Keys{Workload: redact.WorkloadSubstituted}) {
		t.Error("the display decision is the reading's output; a reading that consulted it would be self-referential")
	}
}

// TestUsesWithheld_KeysPublishedByAliasAndDeliveredByMechanism covers the one walk
// that turns a consumed target's declarations into the two sets its two display
// surfaces need. The control throughout is two edges to two DIFFERENT targets
// publishing the same key name: only the one whose target declared it may appear.
func TestUsesWithheld_KeysPublishedByAliasAndDeliveredByMechanism(t *testing.T) {
	t.Parallel()
	markedInject := types.OrderedMap[types.OrderedMap[string]]{}
	markedInject.Set("env", pairs("STORE_SECRET", "zzTargetSecret", "STORE_HOST", "aaTargetHost"))
	plainInject := types.OrderedMap[types.OrderedMap[string]]{}
	// The SAME delivery-key-to-provides-key mapping on the edge whose target
	// declares nothing. A rule keyed on the provides key's name rather than on the
	// declaring target would mark this one too.
	plainInject.Set("env", pairs("PLAIN_SECRET", "zzTargetSecret"))

	uses := types.OrderedMap[types.EnrichedEdge]{}
	uses.Set("storeEdge", types.EnrichedEdge{Target: "store", Component: "core/uses-target", Inject: markedInject})
	uses.Set("plainEdge", types.EnrichedEdge{Target: "plain", Component: "core/uses-plain", Inject: plainInject})

	declared := map[string]map[string]bool{
		"core/uses-target": {"zzTargetSecret": true},
	}
	got := usesWithheld(uses, func(componentType string) map[string]bool {
		return declared[componentType]
	})

	if !got.Provides["storeEdge"]["zzTargetSecret"] {
		t.Errorf("the declaring target's key must reach the published set under its edge's alias; got %v", got.Provides)
	}
	if _, present := got.Provides["plainEdge"]; present {
		t.Errorf("an alias whose target declares nothing must be omitted, not mapped empty; got %v", got.Provides)
	}
	// The delivered half: the delivery key filled FROM the declared provides key is
	// marked, its sibling filled from an undeclared key is not, and the identically
	// mapped key on the undeclared target's edge is not.
	if !got.Inject["env"]["STORE_SECRET"] {
		t.Errorf("a delivery key filled from the target's declared provides key must be marked; got %v", got.Inject)
	}
	if got.Inject["env"]["STORE_HOST"] {
		t.Errorf("a delivery key filled from an undeclared provides key must not be marked; got %v", got.Inject)
	}
	if got.Inject["env"]["PLAIN_SECRET"] {
		t.Errorf("the same mapping on an undeclared target's edge must not be marked; got %v", got.Inject)
	}
	// A node consuming only undeclared targets produces two nil maps, so every
	// applier is a no-op and its display is byte-identical to one taken before the
	// axis existed.
	onlyPlain := types.OrderedMap[types.EnrichedEdge]{}
	onlyPlain.Set("plainEdge", types.EnrichedEdge{Target: "plain", Component: "core/uses-plain", Inject: plainInject})
	if got := usesWithheld(onlyPlain, func(string) map[string]bool { return declared["nope"] }); got.Provides != nil || got.Inject != nil {
		t.Errorf("no declaring target must produce a nil set on both halves; got %+v", got)
	}
	if got := usesWithheld(types.OrderedMap[types.EnrichedEdge]{}, nil); got.Provides != nil || got.Inject != nil {
		t.Errorf("no edges must produce a nil set on both halves; got %+v", got)
	}
	// An edge that declares no inject mapping contributes to the published half
	// only, which is what keeps the delivered half attributable to an authored
	// mapping rather than to the declaration alone.
	noInject := types.OrderedMap[types.EnrichedEdge]{}
	noInject.Set("storeEdge", types.EnrichedEdge{Target: "store", Component: "core/uses-target"})
	bare := usesWithheld(noInject, func(componentType string) map[string]bool { return declared[componentType] })
	if !bare.Provides["storeEdge"]["zzTargetSecret"] {
		t.Errorf("an edge with no inject mapping still publishes; got %v", bare.Provides)
	}
	if bare.Inject != nil {
		t.Errorf("an edge with no inject mapping delivers nothing to mark; got %v", bare.Inject)
	}
}

func pairs(kv ...string) types.OrderedMap[string] {
	m := types.OrderedMap[string]{}
	for i := 0; i+1 < len(kv); i += 2 {
		m.Set(kv[i], kv[i+1])
	}
	return m
}

func TestDerivedSensitiveKeys_SourceStructuredShape(t *testing.T) {
	t.Parallel()
	r := recipeWithLocals("dsn", `{{ .values.dbPassword }}`)
	r.Spec.Source = map[string]any{
		"ref":      `ghcr.io/app:{{ .locals.dsn }}`, // reads a withheld local
		"registry": `{{ .values.appName }}.example`, // reads nothing declared
		"port":     443,                             // never evaluated as a template
	}
	derived := derivedSensitiveKeys(r, taintFixtureValues(), taintFixtureInfra())

	if !derived.Source["ref"] {
		t.Errorf("a structured source field reading a withheld local must be withheld; got %v", derived.Source)
	}
	if derived.Source["registry"] {
		t.Errorf("a sibling field reading nothing declared must not be withheld; got %v", derived.Source)
	}
	if derived.Source["port"] {
		t.Errorf("a non-string field carries no template and must not be withheld; got %v", derived.Source)
	}
	if derived.Source[redact.WholeSourceKey] {
		t.Errorf("a structured source must not be withheld wholesale; got %v", derived.Source)
	}
}
