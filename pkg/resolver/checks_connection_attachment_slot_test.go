// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package resolver

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/constraint"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// connectionSlotCatalog declares one consuming Component with three sockets, one
// per answer the socket declaration can give: "models" declares one slot, "logs"
// is declared and declares none, and every other socket name is one the type does
// not declare at all.
func connectionSlotCatalog(t *testing.T) CatalogLookup {
	t.Helper()
	const gwYAML = `apiVersion: aggrega.ai/v1beta1
kind: Component
metadata:
  name: app/gateway
  version: "1.0.0"
spec:
  usesSchema:
    models:
      selector: "component.aggrega.ai/category=ai-model"
      featuresSchema:
        credential:
          selector: "feature.aggrega.ai/kind=secret"
          sensitive: true
          inject: [env]
    logs:
      selector: "component.aggrega.ai/category=sink"
`
	const modelYAML = `apiVersion: aggrega.ai/v1beta1
kind: Component
metadata:
  name: app/model
  version: "1.0.0"
spec: {}
`
	var gw, model types.Component
	if err := yaml.Unmarshal([]byte(gwYAML), &gw); err != nil {
		t.Fatalf("gateway fixture parse: %v", err)
	}
	if err := yaml.Unmarshal([]byte(modelYAML), &model); err != nil {
		t.Fatalf("model fixture parse: %v", err)
	}
	return CatalogLookup{
		Component: func(u string) (types.Component, bool) {
			switch u {
			case "app/gateway:1.0.0":
				return gw, true
			case "app/model:1.0.0":
				return model, true
			}
			return types.Component{}, false
		},
	}
}

// connectionSlotFinding is what one raised objection says, read through the
// accessors an objection exports: whether it is this rule's condition, and the
// values it names.
type connectionSlotFinding struct {
	condition bool
	detail    []string
	advice    string
}

func connectionSlotFindings(objections []constraint.Objection) []connectionSlotFinding {
	var out []connectionSlotFinding
	for _, o := range objections {
		out = append(out, connectionSlotFinding{
			condition: errors.Is(o.Sentinel(), ErrConnectionAttachmentSlotUndeclared()),
			detail:    o.Detail(),
			advice:    o.Advice(),
		})
	}
	return out
}

// TestConnectionAttachmentFillsADeclaredSlot proves the earlier half of the
// declared-slots rule — the developer's connection entries of a component the
// Aggregate declares — over every answer the socket declaration gives, in both
// directions: a name the socket declares passes, any other name is refused naming
// the attachment, each declared slot within one edit of it, the connection, the
// socket, the component and the declared slots. The Release's half is asked once
// the composition is flattened and is proved there.
//
// Each row carries one fault or none, and the passing rows are the controls that
// keep each refusal attributable to its own fault.
func TestConnectionAttachmentFillsADeclaredSlot(t *testing.T) {
	t.Parallel()

	aggregate := func(uses string) *types.Aggregate {
		return parseAggregate(t, `apiVersion: aggrega.ai/v1beta1
kind: Aggregate
metadata:
  name: demo
spec:
  components:
    gw:
      type: app/gateway:1.0.0
      uses:
`+indent(uses, 8)+`    gpt:
      type: app/model:1.0.0
`)
	}
	const declaredSlot = "models:\n  gpt:\n    target: gpt\n    features:\n      credential:\n        type: core/referenced-secret\n"

	cases := []struct {
		name string
		uses string
		want []connectionSlotFinding
	}{
		{
			name: "the developer fills the slot the socket declares",
			uses: declaredSlot,
		},
		{
			name: "the developer writes a name the socket declares no slot for",
			uses: "models:\n  gpt:\n    target: gpt\n    features:\n      token:\n        type: core/referenced-secret\n",
			want: []connectionSlotFinding{{
				condition: true,
				detail:    []string{"token", "gpt", "models", "gw", "credential"},
				advice:    "Write the attachment under one of the slots the socket declares",
			}},
		},
		{
			// One transposition from the declared slot: the refusal names the slot
			// it sits near in the same sentence.
			name: "a name one edit from a declared slot is refused naming that slot",
			uses: "models:\n  gpt:\n    target: gpt\n    features:\n      credentail:\n        type: core/referenced-secret\n",
			want: []connectionSlotFinding{{
				condition: true,
				detail:    []string{"credentail", "credential", "gpt", "models", "gw", "credential"},
				advice:    "Write the attachment under one of the slots the socket declares",
			}},
		},
		{
			name: "only the undeclared one of two names is refused",
			uses: "models:\n  gpt:\n    target: gpt\n    features:\n      credential:\n        type: core/referenced-secret\n      token:\n        type: core/referenced-secret\n",
			want: []connectionSlotFinding{{
				condition: true,
				detail:    []string{"token", "gpt", "models", "gw", "credential"},
				advice:    "Write the attachment under one of the slots the socket declares",
			}},
		},
		{
			name: "a declared socket declaring no slots admits no attachment",
			uses: "logs:\n  sink:\n    target: gpt\n    features:\n      credential:\n        type: core/referenced-secret\n",
			want: []connectionSlotFinding{{
				condition: true,
				detail:    []string{"credential", "sink", "logs", "gw"},
				advice:    "The socket declares no slots",
			}},
		},
		{
			name: "a socket the type does not declare admits no attachment",
			uses: "backups:\n  spare:\n    target: gpt\n    features:\n      credential:\n        type: core/referenced-secret\n",
			want: []connectionSlotFinding{{
				condition: true,
				detail:    []string{"credential", "spare", "backups", "gw"},
				advice:    "The component's type does not declare this socket",
			}},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			view := newNormalizedView(normalized(t, aggregate(tc.uses)), nil, connectionSlotCatalog(t), nil)
			var objections []constraint.Objection
			func() {
				defer func() {
					if r := recover(); r != nil {
						t.Fatalf("the rule panicked: %v", r)
					}
				}()
				var err error
				objections, err = constraint.RunOnEdges[constraint.NormalizedView](
					nil, newCoordinateSet(view), view, connectionAttachmentFillsADeclaredSlot)
				if err != nil {
					t.Fatalf("the connection walk returned an error: %v", err)
				}
			}()
			got := connectionSlotFindings(objections)
			if len(got) != len(tc.want) {
				t.Fatalf("raised %d finding(s) %+v; want %d %+v", len(got), got, len(tc.want), tc.want)
			}
			for i := range got {
				if !got[i].condition {
					t.Errorf("finding %d does not carry the declared-slots condition", i)
				}
				if !reflect.DeepEqual(got[i].detail, tc.want[i].detail) {
					t.Errorf("finding %d detail = %v; want %v", i, got[i].detail, tc.want[i].detail)
				}
				if !strings.HasPrefix(got[i].advice, tc.want[i].advice) {
					t.Errorf("finding %d advice = %q; want it to open with %q", i, got[i].advice, tc.want[i].advice)
				}
			}
		})
	}
}

// TestConnectionAttachmentSlotRuleIsInvokedByTheResolve proves the developer's
// half is reached through the production entry rather than only through a direct
// traversal: resolving a document carrying an undeclared name halts with the
// condition, and the same document carrying the declared name does not halt for
// it.
func TestConnectionAttachmentSlotRuleIsInvokedByTheResolve(t *testing.T) {
	t.Parallel()

	doc := func(name string) *types.Aggregate {
		return parseAggregate(t, `apiVersion: aggrega.ai/v1beta1
kind: Aggregate
metadata:
  name: demo
spec:
  components:
    gw:
      type: app/gateway:1.0.0
      uses:
        models:
          gpt:
            target: gpt
            features:
              `+name+`:
                type: core/referenced-secret
    gpt:
      type: app/model:1.0.0
`)
	}
	ask := func(agg *types.Aggregate) []*ResolverError {
		view := newNormalizedView(normalized(t, agg), nil, connectionSlotCatalog(t), nil)
		errs, err := runDeclaredRules(nil, view, "", "Aggregate demo", "Release")
		if err != nil {
			t.Fatalf("the declared rules returned an error: %v", err)
		}
		var out []*ResolverError
		for _, e := range errs {
			if errors.Is(e, ErrConnectionAttachmentSlotUndeclared()) {
				out = append(out, e)
			}
		}
		return out
	}
	if got := ask(doc("token")); len(got) != 1 {
		t.Fatalf("an undeclared name must halt once through the resolve's own rules; got %v", got)
	} else if got[0].Entity != "Aggregate demo" || got[0].Field != ".spec.components.gw.uses.models.gpt" {
		t.Errorf("the finding names %q at %q; want the Aggregate at the connection entry", got[0].Entity, got[0].Field)
	}
	if got := ask(doc("credential")); len(got) != 0 {
		t.Fatalf("the declared name must not halt; got %v", got)
	}
}

// TestNearMissOnAConnectionIsRefusedAndNeverAdvised proves the two readings of a
// name one edit from a declared slot agree about where each applies: on a
// component's own attachment block — which is open — the name draws the advisory
// pairing the two spellings and nothing halts, while on a connection — whose set
// is closed — the refusal names the near slot and no advisory is raised beside it.
//
// The first row is the control: the advisory fires on the component's own block,
// so its silence on the connection row is the rule not applying there rather than
// the reading not running.
func TestNearMissOnAConnectionIsRefusedAndNeverAdvised(t *testing.T) {
	t.Parallel()

	const gwYAML = `apiVersion: aggrega.ai/v1beta1
kind: Component
metadata:
  name: app/gateway
  version: "1.0.0"
spec:
  featuresSchema:
    credential: {}
  usesSchema:
    models:
      featuresSchema:
        credential: {}
`
	var gw types.Component
	if err := yaml.Unmarshal([]byte(gwYAML), &gw); err != nil {
		t.Fatalf("gateway fixture parse: %v", err)
	}
	catalog := CatalogLookup{
		Component: func(u string) (types.Component, bool) {
			if u == "app/gateway:1.0.0" {
				return gw, true
			}
			return types.Component{}, false
		},
	}
	doc := func(block string) *types.Aggregate {
		return parseAggregate(t, `apiVersion: aggrega.ai/v1beta1
kind: Aggregate
metadata:
  name: demo
spec:
  components:
    gw:
      type: app/gateway:1.0.0
`+indent(block, 6)+`    gpt:
      type: app/gateway:1.0.0
`)
	}

	cases := []struct {
		name         string
		block        string
		wantAdvisory bool
		wantRefusal  bool
	}{
		{
			name:         "on the component's own block the near name is advised and not refused",
			block:        "features:\n  credentail:\n    type: core/referenced-secret\n",
			wantAdvisory: true,
		},
		{
			name: "on a connection the near name is refused and not advised",
			block: "uses:\n  models:\n    gpt:\n      target: gpt\n      features:\n" +
				"        credentail:\n          type: core/referenced-secret\n",
			wantRefusal: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			view := newNormalizedView(normalized(t, doc(tc.block)), nil, catalog, nil)
			advisories, err := runDeclaredAdvisories(nil, view, nil, "Aggregate demo")
			if err != nil {
				t.Fatalf("the advisories returned an error: %v", err)
			}
			advised := false
			for _, a := range advisories {
				if errors.Is(a, ErrAttachmentSlotNearMiss()) {
					advised = true
				}
			}
			if advised != tc.wantAdvisory {
				t.Errorf("near-miss advisory raised = %v; want %v (advisories: %v)", advised, tc.wantAdvisory, advisories)
			}
			halts, err := runDeclaredRules(nil, view, "", "Aggregate demo", "Release")
			if err != nil {
				t.Fatalf("the declared rules returned an error: %v", err)
			}
			var refusals []*ResolverError
			for _, h := range halts {
				if errors.Is(h, ErrConnectionAttachmentSlotUndeclared()) {
					refusals = append(refusals, h)
				}
			}
			if got := len(refusals) > 0; got != tc.wantRefusal {
				t.Fatalf("declared-slot refusal raised = %v; want %v (findings: %v)", got, tc.wantRefusal, halts)
			}
			if tc.wantRefusal && !strings.Contains(refusals[0].Error(), `near the declared slot "credential"`) {
				t.Errorf("the refusal must name the slot the name sits near; got %v", refusals[0])
			}
		})
	}
}
