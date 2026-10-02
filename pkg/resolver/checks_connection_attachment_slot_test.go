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
	const gwYAML = `apiVersion: aggrega.ai/v1alpha1
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
	const modelYAML = `apiVersion: aggrega.ai/v1alpha1
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

// TestConnectionAttachmentFillsADeclaredSlot proves both earlier halves of the
// declared-slots rule — the developer's connection entries and the Release's —
// over every answer the socket declaration gives, in both directions: a name the
// socket declares passes, any other name is refused naming the attachment, the
// connection, the socket, the component and the declared slots.
//
// Each row carries one fault or none, and the passing rows are the controls that
// keep each refusal attributable to its own fault.
func TestConnectionAttachmentFillsADeclaredSlot(t *testing.T) {
	t.Parallel()

	aggregate := func(uses string) *types.Aggregate {
		return parseAggregate(t, `apiVersion: aggrega.ai/v1alpha1
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
		name    string
		uses    string
		release string
		rule    constraint.Check[constraint.NormalizedView, constraint.EdgeSubject]
		want    []connectionSlotFinding
	}{
		{
			name: "the developer fills the slot the socket declares",
			uses: declaredSlot,
			rule: connectionAttachmentFillsADeclaredSlot,
		},
		{
			name: "the developer writes a name the socket declares no slot for",
			uses: "models:\n  gpt:\n    target: gpt\n    features:\n      token:\n        type: core/referenced-secret\n",
			rule: connectionAttachmentFillsADeclaredSlot,
			want: []connectionSlotFinding{{
				condition: true,
				detail:    []string{"token", "gpt", "models", "gw", "credential"},
				advice:    "Write the attachment under one of the slots the socket declares",
			}},
		},
		{
			name: "only the undeclared one of two names is refused",
			uses: "models:\n  gpt:\n    target: gpt\n    features:\n      credential:\n        type: core/referenced-secret\n      token:\n        type: core/referenced-secret\n",
			rule: connectionAttachmentFillsADeclaredSlot,
			want: []connectionSlotFinding{{
				condition: true,
				detail:    []string{"token", "gpt", "models", "gw", "credential"},
				advice:    "Write the attachment under one of the slots the socket declares",
			}},
		},
		{
			name: "a declared socket declaring no slots admits no attachment",
			uses: "logs:\n  sink:\n    target: gpt\n    features:\n      credential:\n        type: core/referenced-secret\n",
			rule: connectionAttachmentFillsADeclaredSlot,
			want: []connectionSlotFinding{{
				condition: true,
				detail:    []string{"credential", "sink", "logs", "gw"},
				advice:    "The socket declares no slots",
			}},
		},
		{
			name: "a socket the type does not declare admits no attachment",
			uses: "backups:\n  spare:\n    target: gpt\n    features:\n      credential:\n        type: core/referenced-secret\n",
			rule: connectionAttachmentFillsADeclaredSlot,
			want: []connectionSlotFinding{{
				condition: true,
				detail:    []string{"credential", "spare", "backups", "gw"},
				advice:    "The component's type does not declare this socket",
			}},
		},
		{
			name:    "the operator sizes the slot the developer filled",
			uses:    declaredSlot,
			release: "models:\n  gpt:\n    features:\n      credential:\n        infra:\n          rotation: hourly\n",
			rule:    releaseConnectionAttachmentFillsADeclaredSlot,
		},
		{
			name:    "the operator writes a name the socket declares no slot for",
			uses:    declaredSlot,
			release: "models:\n  gpt:\n    features:\n      token:\n        type: core/referenced-secret\n",
			rule:    releaseConnectionAttachmentFillsADeclaredSlot,
			want: []connectionSlotFinding{{
				condition: true,
				detail:    []string{"token", "gpt", "models", "gw", "credential"},
				advice:    "Write the attachment under one of the slots the socket declares",
			}},
		},
		{
			// The Release may address only declared sockets, and the socket rule
			// refuses this entry for that by name; this rule leaves it alone so one
			// fault is reported once.
			name:    "the operator writes on a socket the type does not declare",
			uses:    declaredSlot,
			release: "backups:\n  spare:\n    target: gpt\n    features:\n      credential:\n        type: core/referenced-secret\n",
			rule:    releaseConnectionAttachmentFillsADeclaredSlot,
		},
		{
			// The developer's names are this rule's twin's to judge.
			name: "the operator's rule is not asked about the developer's names",
			uses: "models:\n  gpt:\n    target: gpt\n    features:\n      token:\n        type: core/referenced-secret\n",
			rule: releaseConnectionAttachmentFillsADeclaredSlot,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var release *types.Release
			if tc.release != "" {
				release = parseRelease(t, `apiVersion: aggrega.ai/v1alpha1
kind: Release
metadata:
  name: demo-prod
spec:
  boundaries:
    prod:
      components:
        gw:
          uses:
`+indent(tc.release, 12)+`        gpt: {}
`)
			}
			view := newNormalizedView(normalized(t, aggregate(tc.uses)), release, connectionSlotCatalog(t), nil)
			var objections []constraint.Objection
			func() {
				defer func() {
					if r := recover(); r != nil {
						t.Fatalf("the rule panicked: %v", r)
					}
				}()
				var err error
				objections, err = constraint.RunOnEdges[constraint.NormalizedView](
					nil, newCoordinateSet(view), view, tc.rule)
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
		return parseAggregate(t, `apiVersion: aggrega.ai/v1alpha1
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
		errs, err := runDeclaredRules(nil, view, "Aggregate demo", "Release")
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
