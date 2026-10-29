// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Tests for the reading of a node's OWN component-level self-inject as a delivery
// route.
//
// Every case is a PAIR: an authored entry that must be withheld sits beside a
// structurally identical entry that must not be, differing only in whether the
// datum it names or reads is declared. A marking that fired on every self-inject
// entry passes each positive case on its own, so the negative half is what makes
// the positive half mean anything.
//
// The two authored halves are tested separately and against DIFFERENT declaration
// sets, because that separation is the reading's whole content: `from:` answers to
// the Component's `provides` schema, `value:` to its `values` and `infra`. A
// reading that compared both halves against one set would pass a test that only
// ever declared the same path in both.

package renderer

import (
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// selfInjectVerbose builds one mechanism's authored entries as the list the
// inject_delivery definition describes, from (deliveryKey, field, text) triples
// where field is "from" or "value". Each entry is an ordered map, the shape an
// order-preserving decode gives it.
//
// It panics on a triple count that does not divide, rather than silently building
// fewer entries than the caller wrote: every negative assertion below is "this key
// is absent from the marking", and a helper that quietly dropped an entry would
// satisfy all of them while testing nothing.
func selfInjectVerbose(triples ...string) any {
	if len(triples) == 0 || len(triples)%3 != 0 {
		panic("selfInjectVerbose: want (deliveryKey, field, text) triples")
	}
	out := []any{}
	for i := 0; i+2 < len(triples); i += 3 {
		entry := types.NewOrderedMap[any]()
		entry.Set("in", triples[i])
		entry.Set(triples[i+1], triples[i+2])
		out = append(out, entry)
	}
	if len(out) != len(triples)/3 {
		panic("selfInjectVerbose: entries lost while building the fixture")
	}
	return out
}

func TestSelfInjectWithheld_FromResolvesAgainstTheNodesOwnProvides(t *testing.T) {
	t.Parallel()
	declaredProvides := map[string]bool{"rootPassword": true}

	got := selfInjectWithheld(
		&types.InjectBlock{Env: selfInjectVerbose(
			"ZZ_DECLARED", "from", "rootPassword",
			"ZZ_PLAIN", "from", "port",
		)},
		declaredProvides, nil, nil)

	if !got["env"]["ZZ_DECLARED"] {
		t.Errorf("a self-inject naming a declared provides key must be withheld; got %v", got)
	}
	if got["env"]["ZZ_PLAIN"] {
		t.Errorf("a self-inject naming an undeclared provides key must stay in the clear; got %v", got)
	}
}

func TestSelfInjectWithheld_ValueTemplateResolvesAgainstValuesAndInfra(t *testing.T) {
	t.Parallel()
	declaredValues := map[string]bool{"dbPassword": true}
	declaredInfra := map[string]bool{"apiKey": true}

	got := selfInjectWithheld(
		&types.InjectBlock{Env: selfInjectVerbose(
			"ZZ_FROM_VALUES", "value", "{{ .values.dbPassword }}",
			"ZZ_FROM_INFRA", "value", "prefix-{{ .infra.apiKey }}",
			"ZZ_PLAIN_VALUES", "value", "{{ .values.host }}",
			"ZZ_PLAIN_CONTEXT", "value", "{{ .context.instance_id }}",
		)},
		nil, declaredValues, declaredInfra)

	for _, key := range []string{"ZZ_FROM_VALUES", "ZZ_FROM_INFRA"} {
		if !got["env"][key] {
			t.Errorf("a value: template reading a declared leaf must be withheld at %q; got %v", key, got)
		}
	}
	for _, key := range []string{"ZZ_PLAIN_VALUES", "ZZ_PLAIN_CONTEXT"} {
		if got["env"][key] {
			t.Errorf("a value: template reading nothing declared must stay in the clear at %q; got %v", key, got)
		}
	}
}

// TestSelfInjectWithheld_TheTwoHalvesDoNotShareADeclarationSet is the assertion
// that separates the halves. A reading that compared `from:` against the values
// pillar, or a `value:` template against the provides pillar, would withhold the
// wrong entry here while every same-set test above still passed.
func TestSelfInjectWithheld_TheTwoHalvesDoNotShareADeclarationSet(t *testing.T) {
	t.Parallel()
	got := selfInjectWithheld(
		&types.InjectBlock{Env: selfInjectVerbose(
			"ZZ_FROM_NAMES_A_VALUES_PATH", "from", "dbPassword",
			"ZZ_VALUE_READS_A_PROVIDES_PATH", "value", "{{ .values.rootPassword }}",
		)},
		map[string]bool{"rootPassword": true},
		map[string]bool{"dbPassword": true},
		nil)

	if got["env"]["ZZ_FROM_NAMES_A_VALUES_PATH"] {
		t.Errorf("a from: key declared in the values pillar is not a declared provides key; got %v", got)
	}
	if got["env"]["ZZ_VALUE_READS_A_PROVIDES_PATH"] {
		t.Errorf("a value: template reading a path declared in the provides pillar reads nothing declared; got %v", got)
	}
}

func TestSelfInjectWithheld_EveryDeliveredMechanismIsRead(t *testing.T) {
	t.Parallel()
	declaredProvides := map[string]bool{"rootPassword": true}
	block := &types.InjectBlock{
		Env:  selfInjectVerbose("ZZ_ENV", "from", "rootPassword"),
		File: selfInjectVerbose("zz-file", "from", "rootPassword"),
		Arg:  selfInjectVerbose("--zz-arg", "from", "rootPassword"),
	}

	got := selfInjectWithheld(block, declaredProvides, nil, nil)

	for mechanism, key := range map[string]string{"env": "ZZ_ENV", "file": "zz-file", "arg": "--zz-arg"} {
		if !got[mechanism][key] {
			t.Errorf("the %s mechanism must be read; got %v", mechanism, got)
		}
	}
	if got["env"]["zz-file"] {
		t.Errorf("a marking must not cross into another mechanism; got %v", got)
	}
}

// TestSelfInjectWithheld_AnAuthoredLiteralIsNotARoute pins the one entry shape the
// reading deliberately ignores. A value: carrying no template reference is the
// string the author typed; no declaration stands between them and it, so
// withholding it would put a placeholder over a value its own author wrote in the
// clear.
func TestSelfInjectWithheld_AnAuthoredLiteralIsNotARoute(t *testing.T) {
	t.Parallel()
	got := selfInjectWithheld(
		&types.InjectBlock{Env: selfInjectVerbose("ZZ_LITERAL", "value", "rootPassword")},
		map[string]bool{"rootPassword": true}, nil, nil)

	if len(got) != 0 {
		t.Errorf("an authored literal must contribute no route even when its text matches a declared key name; got %v", got)
	}
}

func TestSelfInjectWithheld_NothingDeclaredAndNoBlockReadNothing(t *testing.T) {
	t.Parallel()
	if got := selfInjectWithheld(nil, map[string]bool{"rootPassword": true}, nil, nil); got != nil {
		t.Errorf("a node with no self-inject must produce no marking; got %v", got)
	}
	got := selfInjectWithheld(
		&types.InjectBlock{Env: selfInjectVerbose("ZZ_ANY", "from", "rootPassword")},
		nil, nil, nil)
	if got != nil {
		t.Errorf("a node declaring nothing must produce no marking; got %v", got)
	}
}
