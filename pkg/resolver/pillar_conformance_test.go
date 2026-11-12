// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package resolver

import (
	"errors"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/redact"
	"github.com/aggrega-ai/aggrega-cli/pkg/compactform"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"go.yaml.in/yaml/v3"
)

// compileTestSchema compiles a compact pillar schema to a compactform.Schema.
func compileTestSchema(t *testing.T, schemaYAML string) compactform.Schema {
	t.Helper()
	var node yaml.Node
	if err := yaml.Unmarshal([]byte(schemaYAML), &node); err != nil {
		t.Fatalf("schema fixture parse: %v", err)
	}
	compiled, err := compactform.Compile(&node)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	return compiled
}

// --- Declared-sensitive leaves: the value never reaches the diagnostic -----
//
// The validator interpolates the offending instance value into its failure
// text, so a leaf an author declared sensitive would otherwise have its secret
// printed back in a build diagnostic. Every assertion below is paired: a
// withheld message sits beside a NON-sensitive sibling in the SAME call whose
// message must survive byte for byte, because a redaction that never fires is
// indistinguishable from correct behaviour in a test that only looks for the
// placeholder.

const (
	// The plaintext each declared-sensitive leaf carries. No message may
	// contain these.
	withheldValuesSecret = "hunter2-PLAINTEXT"
	withheldNestedSecret = "adm1n-PLAINTEXT"
	withheldInfraSecret  = "ADMIN-PLAINTEXT"

	// The plaintext each non-sensitive sibling carries. Every one must survive
	// in the clear.
	shownValuesPlain = "DEMO_123"
	shownInfraPlain  = "INFRA-IN-THE-CLEAR"
)

// sensitiveLeafSchema declares, in one schema, the three shapes the withholding
// must tell apart: a sensitive leaf beside a plain sibling in values; a
// sensitive OBJECT whose failing child is two levels down; and one leaf name
// declared in BOTH pillars but sensitive in only one.
const sensitiveLeafSchema = `values:
  dbPassword: { type: string, sensitive: true, pattern: "^pg://" }
  appName:    { type: string, pattern: "^[a-z-]+$" }
  creds:
    type: object
    sensitive: true
    properties:
      adminToken: { type: string, minLength: 40 }
  password: { type: string, sensitive: true, minLength: 40 }
infra:
  password: { type: string, minLength: 40 }
`

// plainLeafSchema is sensitiveLeafSchema with every sensitivity declaration
// removed and nothing else changed. It is the control: the messages it produces
// are what an unwithheld diagnostic looks like, so a byte comparison against it
// proves the withholding touches only the declared leaves.
const plainLeafSchema = `values:
  dbPassword: { type: string, pattern: "^pg://" }
  appName:    { type: string, pattern: "^[a-z-]+$" }
  creds:
    type: object
    properties:
      adminToken: { type: string, minLength: 40 }
  password: { type: string, minLength: 40 }
infra:
  password: { type: string, minLength: 40 }
`

// violatingValues is the values instance every leaf of which violates its
// declared constraint, so one call produces a finding on each.
func violatingValues() types.OrderedMap[any] {
	creds := types.OrderedMap[any]{}
	creds.Set("adminToken", withheldNestedSecret)
	vals := types.OrderedMap[any]{}
	vals.Set("dbPassword", withheldValuesSecret)
	vals.Set("appName", shownValuesPlain)
	vals.Set("creds", creds)
	vals.Set("password", "")
	return vals
}

// messagesByField validates one pillar and returns the findings keyed by field
// path, failing when the instance conforms — an empty result would make every
// assertion downstream vacuous.
func messagesByField(t *testing.T, schemaYAML, pillar string, instance types.OrderedMap[any]) map[string]string {
	t.Helper()
	errs := ValidatePillarConformance(compileTestSchema(t, schemaYAML), pillar, instance, nil,
		"Aggregate demo", `.spec.components["svc"]`, "the Component schema")
	if len(errs) == 0 {
		t.Fatalf("the %s fixture must violate its schema; it produced no finding", pillar)
	}
	out := make(map[string]string, len(errs))
	for _, e := range errs {
		out[e.Field] = e.Message
	}
	return out
}

// TestPillarConformance_SensitiveLeafWithheldPlainSiblingUnchanged is the
// discrimination proof. Four findings arrive from one call: three on declared
// leaves, one on a plain sibling. The three carry the placeholder; the sibling's
// message is byte-identical to the same finding produced against a schema that
// declares nothing sensitive.
func TestPillarConformance_SensitiveLeafWithheldPlainSiblingUnchanged(t *testing.T) {
	t.Parallel()
	const prefix = `.spec.components["svc"].values`

	got := messagesByField(t, sensitiveLeafSchema, "values", violatingValues())
	control := messagesByField(t, plainLeafSchema, "values", violatingValues())

	if len(got) != len(control) {
		t.Fatalf("the two schemas must produce the same finding set; got %d vs %d\n got: %v\n control: %v",
			len(got), len(control), got, control)
	}

	// The plain sibling: byte-identical to the control, value included.
	plainField := prefix + ".appName"
	if got[plainField] != control[plainField] {
		t.Errorf("a non-sensitive leaf's message must be unchanged.\n got:     %q\n control: %q", got[plainField], control[plainField])
	}
	if !strings.Contains(got[plainField], shownValuesPlain) {
		t.Errorf("a non-sensitive leaf must still show its offending value; got %q", got[plainField])
	}

	// The declared leaves: withheld, including the child of a sensitive object.
	for _, field := range []string{prefix + ".dbPassword", prefix + ".creds.adminToken", prefix + ".password"} {
		msg, ok := got[field]
		if !ok {
			t.Fatalf("no finding at %q; the fixture drifted. got: %v", field, got)
		}
		if !strings.Contains(msg, redact.Placeholder()) {
			t.Errorf("%s must be withheld; got %q", field, msg)
		}
		if msg == control[field] {
			t.Errorf("%s produced the control's message, so the withholding never fired: %q", field, msg)
		}
	}

	// No message anywhere may reproduce a declared-sensitive value.
	for field, msg := range got {
		for _, secret := range []string{withheldValuesSecret, withheldNestedSecret} {
			if strings.Contains(msg, secret) {
				t.Errorf("%s leaks %q: %s", field, secret, msg)
			}
		}
	}
}

// TestPillarConformance_SensitiveLeafKeepsLocatorAndKeyword pins the
// actionability bound: withholding the constraint text costs the regex, never
// the locator. The reader must still learn which entity, which field, which
// pillar and which contract, plus the keyword the finding carries.
func TestPillarConformance_SensitiveLeafKeepsLocatorAndKeyword(t *testing.T) {
	t.Parallel()
	errs := ValidatePillarConformance(compileTestSchema(t, sensitiveLeafSchema), "values",
		violatingValues(), nil, "Aggregate demo", `.spec.components["svc"]`, "the Component schema")

	var found *ResolverError
	for _, e := range errs {
		if e.Field == `.spec.components["svc"].values.dbPassword` {
			found = e
		}
	}
	if found == nil {
		t.Fatalf("no finding at the sensitive leaf; got %v", errs)
	}
	if !errors.Is(found, ErrSchemaConformance()) {
		t.Errorf("a withheld finding must still wrap ErrSchemaConformance; got %v", found.Err)
	}
	if found.Entity != "Aggregate demo" {
		t.Errorf("entity lost: %q", found.Entity)
	}
	for _, want := range []string{"values pillar", "the Component schema", redact.Placeholder(), "(validation)"} {
		if !strings.Contains(found.Message, want) {
			t.Errorf("a withheld message must retain %q; got %q", want, found.Message)
		}
	}
}

// TestPillarConformance_SensitivityIsPerPillar pins the pillar prefix: `password`
// is one leaf name declared in both pillars and sensitive in only one, so a
// reader of the union of the two pillars' declarations would withhold the
// operator's infra sizing value for no reason. The two pillars are read apart.
func TestPillarConformance_SensitivityIsPerPillar(t *testing.T) {
	t.Parallel()
	infra := types.OrderedMap[any]{}
	infra.Set("password", shownInfraPlain)

	values := messagesByField(t, sensitiveLeafSchema, "values", violatingValues())
	got := messagesByField(t, sensitiveLeafSchema, "infra", infra)

	if msg := values[`.spec.components["svc"].values.password`]; !strings.Contains(msg, redact.Placeholder()) {
		t.Errorf("values.password is declared sensitive and must be withheld; got %q", msg)
	}
	msg := got[`.spec.components["svc"].infra.password`]
	if strings.Contains(msg, redact.Placeholder()) {
		t.Errorf("infra.password is NOT declared sensitive; withholding it means the pillars were unioned: %q", msg)
	}
	if !strings.Contains(msg, shownInfraPlain) {
		t.Errorf("infra.password must still show its offending value; got %q", msg)
	}
}

// TestPillarConformance_NoDeclarationsLeavesEveryMessageUntouched is the
// non-regression floor: a schema declaring nothing sensitive — every schema
// predating the axis — produces exactly the messages it always did.
func TestPillarConformance_NoDeclarationsLeavesEveryMessageUntouched(t *testing.T) {
	t.Parallel()
	for field, msg := range messagesByField(t, plainLeafSchema, "values", violatingValues()) {
		if strings.Contains(msg, redact.Placeholder()) {
			t.Errorf("%s was withheld against a schema that declares nothing sensitive: %q", field, msg)
		}
	}
	infra := types.OrderedMap[any]{}
	infra.Set("password", withheldInfraSecret)
	for field, msg := range messagesByField(t, plainLeafSchema, "infra", infra) {
		if !strings.Contains(msg, withheldInfraSecret) {
			t.Errorf("%s lost its offending value against an undeclared schema: %q", field, msg)
		}
	}
}

// TestFindingOnSensitiveLeaf_PathShapes covers the path arithmetic directly,
// including the shapes an end-to-end fixture cannot easily reach: a finding at
// the pillar root, at the instance root, and under a pillar this entry does not
// validate. Those messages name property keys rather than quoting values, so
// reporting false for them is what keeps a missing-required diagnostic readable.
func TestFindingOnSensitiveLeaf_PathShapes(t *testing.T) {
	t.Parallel()
	declared := SensitiveLeaves{
		Values:   map[string]bool{"dbPassword": true, "creds": true},
		Infra:    map[string]bool{"adminPassword": true},
		Provides: map[string]bool{"host": true},
	}
	for _, tc := range []struct {
		pillar, path string
		want         bool
	}{
		{"values", "values.dbPassword", true},
		{"values", "values.creds.adminToken", true}, // inside a sensitive object
		{"values", "values.creds.a.b.c", true},      // arbitrarily deep inside it
		{"values", "values.appName", false},         // plain sibling
		{"values", "values.dbPasswordExtra", false}, // shares a prefix, is not the leaf
		{"values", "values", false},                 // pillar root
		{"values", "", false},                       // instance root
		{"values", "infra.adminPassword", false},    // other pillar's path
		{"infra", "infra.adminPassword", true},      // same leaf, correct pillar
		{"infra", "infra.dbPassword", false},        // sensitive in values, not here
		{"provides", "provides.host", false},        // not validated through this entry
	} {
		if got := findingOnSensitiveLeaf(declared, tc.pillar, tc.path); got != tc.want {
			t.Errorf("findingOnSensitiveLeaf(%q, %q) = %v, want %v", tc.pillar, tc.path, got, tc.want)
		}
	}
}

// TestPillarConformance_WithholdingFollowsTheDeclarationReader pins that this
// diagnostic surface withholds exactly what the declaration reader collects, and
// does so for free — there is no second walk here, so a declaration the reader
// learns to see is withheld here the same day.
//
// A declaration inside a reference TARGET is one such case: the reader resolves
// the reference, so the value that once reached this diagnostic in plaintext is
// now replaced. The test asserts the replacement rather than the leak, and it
// asserts the key survives, because a diagnostic that dropped the failing locus
// would be worse than one that printed it.
func TestPillarConformance_WithholdingFollowsTheDeclarationReader(t *testing.T) {
	t.Parallel()
	compiled := compileTestSchema(t, `$defs:
  MarkedCred: { type: string, minLength: 40, sensitive: true }
values:
  cred:
    $ref: "#/$defs/MarkedCred"
`)
	if !DeclaredSensitiveLeaves(compiled).Values["cred"] {
		t.Fatalf("a declaration inside the reference target must be collected at the referring path; got %v",
			DeclaredSensitiveLeaves(compiled).Values)
	}
	instance := types.OrderedMap[any]{}
	instance.Set("cred", "INSIDE-REF-PLAINTEXT")
	errs := ValidatePillarConformance(compiled, "values", instance, nil,
		"Aggregate demo", `.spec.components["svc"]`, "the Component schema")
	if len(errs) == 0 {
		t.Fatalf("the fixture must violate its schema; it produced no finding")
	}
	var withheld bool
	for _, e := range errs {
		if strings.Contains(e.Message, "INSIDE-REF-PLAINTEXT") {
			t.Errorf("the declared value still reaches the diagnostic in plaintext: %v", e)
		}
		if strings.Contains(e.Message, redact.Placeholder()) {
			withheld = true
		}
	}
	if !withheld {
		t.Errorf("no finding carries the replacement text; got %v", errs)
	}
}

// TestPillarConformance_WholePillarBehindAReferenceIsWithheld pins that a
// declaration reached through a reference is withheld wherever the refusal lands.
// The declaration reader resolves a reference on the pillar node, and this
// diagnostic surface withholds a finding on a declared leaf and a finding on a
// position holding one beneath it. There is no second walk here: both readings
// ask the declarations the reader collected.
//
// Where the reference is the whole pillar, the validator stands the definition in
// for the pillar and refuses each failing value at its own path. Where the
// reference is written beside other keywords — the pillar's own properties, or a
// property's — it checks the definition whole at the position holding the
// reference, so a failure inside the definition is reported at that position,
// quoting the value from beneath it. Every row asserts the declared value appears
// nowhere in clear text; rows whose undeclared value fails assert that value is
// still shown, because a withholding that replaced every message would pass a
// placeholder-only check.
//
// The row where both fail beside the pillar's own properties pins a limit rather
// than a guarantee: the finding at the position holding the reference is an
// ancestor of the sibling's, and the leaf-most reduction keeps the sibling's alone.
// The build still halts on it, and the declared leaf's refusal is reported — and
// withheld — once the sibling conforms, which the row before it shows.
func TestPillarConformance_WholePillarBehindAReferenceIsWithheld(t *testing.T) {
	t.Parallel()
	const besidePillarProperties = `values:
  $ref: "#/$defs/Creds"
  $defs:
    Creds:
      properties:
        marked: { type: string, minLength: 40, sensitive: true }
  properties:
    plain: { type: string, minLength: 40 }
`
	for _, tc := range []struct {
		name     string
		schema   string
		instance func() types.OrderedMap[any]
		// declaredAt is the path below the pillar the declaration is collected
		// at; withheldAt is the field suffix of the finding that must carry the
		// replacement text, and shownAt the one that must still show the
		// undeclared value, empty where no such finding is expected.
		declaredAt, withheldAt, shownAt string
	}{
		{
			name: "definitions on the pillar node",
			schema: `values:
  $ref: "#/$defs/Creds"
  $defs:
    Creds:
      marked: { type: string, minLength: 40, sensitive: true }
      plain:  { type: string, minLength: 40 }
`,
			instance:   pillarInstance("marked", "WHOLE-PILLAR-PLAINTEXT", "plain", "SIBLING-IN-THE-CLEAR"),
			declaredAt: "marked", withheldAt: ".values.marked", shownAt: ".values.plain",
		},
		{
			name: "definitions at the document root",
			schema: `$defs:
  Creds:
    marked: { type: string, minLength: 40, sensitive: true }
    plain:  { type: string, minLength: 40 }
values:
  $ref: "#/$defs/Creds"
`,
			instance:   pillarInstance("marked", "WHOLE-PILLAR-PLAINTEXT", "plain", "SIBLING-IN-THE-CLEAR"),
			declaredAt: "marked", withheldAt: ".values.marked", shownAt: ".values.plain",
		},
		{
			name:   "beside the pillar's own properties, the declared leaf failing alone",
			schema: besidePillarProperties,
			instance: pillarInstance("marked", "WHOLE-PILLAR-PLAINTEXT",
				"plain", "A-SIBLING-VALUE-LONG-ENOUGH-TO-SATISFY-ITS-BOUND"),
			declaredAt: "marked", withheldAt: ".values",
		},
		{
			name:       "beside the pillar's own properties, both failing",
			schema:     besidePillarProperties,
			instance:   pillarInstance("marked", "WHOLE-PILLAR-PLAINTEXT", "plain", "SIBLING-IN-THE-CLEAR"),
			declaredAt: "marked", shownAt: ".values.plain",
		},
		{
			name: "beside a property's own properties, the declared leaf failing alone",
			schema: `values:
  $defs:
    Creds:
      properties:
        marked: { type: string, minLength: 40, sensitive: true }
  creds:
    $ref: "#/$defs/Creds"
    properties:
      plain: { type: string }
`,
			instance: func() types.OrderedMap[any] {
				creds := pillarInstance("marked", "WHOLE-PILLAR-PLAINTEXT")()
				out := types.OrderedMap[any]{}
				out.Set("creds", creds)
				return out
			},
			declaredAt: "creds.marked", withheldAt: ".values.creds",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			compiled := compileTestSchema(t, tc.schema)
			declared := DeclaredSensitiveLeaves(compiled)
			if len(declared.Values) != 1 || !declared.Values[tc.declaredAt] {
				t.Fatalf("the declaration behind the reference must be collected at %q, and only there; got %v",
					tc.declaredAt, declared.Values)
			}
			errs := ValidatePillarConformance(compiled, "values", tc.instance(), nil,
				"Aggregate demo", `.spec.components["svc"]`, "the Component schema")
			if len(errs) == 0 {
				t.Fatalf("the fixture must violate its schema; it produced no finding")
			}
			var withheld, shown bool
			for _, e := range errs {
				if strings.Contains(e.Message, "WHOLE-PILLAR-PLAINTEXT") {
					t.Errorf("the declared value reaches the diagnostic in plaintext: %v", e)
				}
				if tc.withheldAt != "" && strings.HasSuffix(e.Field, tc.withheldAt) &&
					strings.Contains(e.Message, redact.Placeholder()) {
					withheld = true
				}
				if tc.shownAt != "" && strings.HasSuffix(e.Field, tc.shownAt) &&
					strings.Contains(e.Message, "SIBLING-IN-THE-CLEAR") {
					shown = true
				}
			}
			if tc.withheldAt != "" && !withheld {
				t.Errorf("no finding at %q carries the replacement text, so the declared leaf's refusal went unreported or unwithheld; got %v",
					tc.withheldAt, errs)
			}
			if tc.shownAt != "" && !shown {
				t.Errorf("the undeclared value must still be shown at %q; got %v", tc.shownAt, errs)
			}
		})
	}
}

// pillarInstance builds an authored pillar instance from key-value pairs, in the
// order given.
func pillarInstance(pairs ...string) func() types.OrderedMap[any] {
	return func() types.OrderedMap[any] {
		out := types.OrderedMap[any]{}
		for i := 0; i+1 < len(pairs); i += 2 {
			out.Set(pairs[i], pairs[i+1])
		}
		return out
	}
}

// TestFindingAboveSensitiveLeaf_PathShapes covers the downward reading's path
// arithmetic directly: a position is reached when a declaration lies strictly
// beneath it, the pillar root holds every declaration of its pillar, and a path
// sharing a prefix without sharing a segment is not above anything.
func TestFindingAboveSensitiveLeaf_PathShapes(t *testing.T) {
	t.Parallel()
	declared := SensitiveLeaves{
		Values: map[string]bool{"creds.adminToken": true},
		Infra:  map[string]bool{},
	}
	for _, tc := range []struct {
		pillar, path string
		want         bool
	}{
		{"values", "values", true},                   // pillar root holding a declaration
		{"values", "values.creds", true},             // object above the declared leaf
		{"values", "values.creds.adminToken", false}, // the leaf itself is the upward reading's
		{"values", "values.cred", false},             // shares a prefix, not a segment
		{"values", "values.appName", false},          // unrelated sibling
		{"values", "infra.creds", false},             // another pillar's path
		{"infra", "infra", false},                    // a pillar declaring nothing
		{"provides", "provides", false},              // not validated through this entry
	} {
		if got := findingAboveSensitiveLeaf(declared, tc.pillar, tc.path); got != tc.want {
			t.Errorf("findingAboveSensitiveLeaf(%q, %q) = %v, want %v", tc.pillar, tc.path, got, tc.want)
		}
	}
}
