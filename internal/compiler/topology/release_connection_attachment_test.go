// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package topology

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/constraint"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
)

// releaseFor writes a Release placing the gateway and both models in one
// boundary, with gwBlock as the keys of the gateway's own entry (already
// indented under it) and extra appended as further component entries.
//
// throughModule routes the three components in through the module's routing
// entry instead, under their local names, which is how a Release addresses the
// components a sub-module contributes.
func releaseFor(throughModule bool, gwBlock, extra string) string {
	container := "      components:\n"
	pad := 8
	if throughModule {
		container = "      aggregates:\n        mod:\n          components:\n"
		pad = 12
	}
	at := strings.Repeat(" ", pad)
	gw := at + "gw: {}\n"
	if gwBlock != "" {
		gw = at + "gw:\n" + indentBlock(gwBlock, pad+2)
	}
	return `apiVersion: aggrega.ai/v1beta1
kind: Release
metadata:
  name: demo-prod
spec:
  boundaries:
    prod:
` + container + gw + at + "gpt: {}\n" + at + "nim: {}\n" + indentBlock(extra, pad)
}

// releaseFinding is where one finding sits: the document it names and the
// position inside that document.
type releaseFinding struct {
	entity, field string
}

// releaseFindings reads the per-entry findings of a halting build, keeping those
// carrying the given condition, and fails the test on a build that did not halt
// with a group of findings.
func releaseFindings(t *testing.T, err error, condition error) []*resolver.ResolverError {
	t.Helper()
	var multi *resolver.MultiResolverError
	if !errors.As(err, &multi) {
		t.Fatalf("the build must halt with a group of findings; got %v", err)
	}
	var out []*resolver.ResolverError
	for _, v := range multi.Violations {
		if !errors.Is(v, condition) {
			t.Errorf("finding %v does not carry the expected condition", v)
			continue
		}
		out = append(out, v)
	}
	return out
}

// declaredNoNim is the gateway's block with one developer credential on the gpt
// connection and a nim connection carrying none.
const declaredNoNim = "models:\n  gpt:\n    target: gpt\n    features:\n      credential:\n        type: core/referenced-secret\n" +
	"  nim:\n    target: nim\n"

// TestReleaseConnectionEntryCarryingAttachmentsMustReachAConnection proves a
// Release per-edge entry carrying only attachments is held to the rule an entry
// carrying infra is: once the connection set is final, it must have reached a
// connection. Both arms are asked — the rule over the finished composition for a
// component it holds, and the guard inside the flattening for one it does not —
// and both keep their exceptions: an entry on a connection some document drew
// passes, an entry configuring nothing passes, and a component switched off takes
// its entries with it.
//
// Each row carries one fault or none, and the controls are rows of the same
// table so a predicate that refused every entry fails here too.
func TestReleaseConnectionEntryCarryingAttachmentsMustReachAConnection(t *testing.T) {
	t.Parallel()

	const ghostAttachment = "uses:\n  models:\n    ghost:\n      features:\n        credential:\n" +
		"          type: core/referenced-secret\n"
	cases := []struct {
		name string
		agg  string
		rel  string
		// want is the finding the declared rule raises; empty with wantGuard empty
		// means the build is accepted.
		want []releaseFinding
		// wantGuard is the sentence the guard inside the flattening reports.
		wantGuard string
	}{
		{
			name: "attachments on a connection nothing drew halt at the entry",
			agg:  rootDocument(declaredNoNim),
			rel:  releaseFor(false, ghostAttachment, ""),
			want: []releaseFinding{{entity: "Release demo-prod", field: ".spec.boundaries.prod.components.gw.uses.models.ghost"}},
		},
		{
			name: "infra and attachments together on a connection nothing drew halt once",
			agg:  rootDocument(declaredNoNim),
			rel: releaseFor(false, "uses:\n  models:\n    ghost:\n      infra:\n        timeout: 5\n"+
				"      features:\n        credential:\n          type: core/referenced-secret\n", ""),
			want: []releaseFinding{{entity: "Release demo-prod", field: ".spec.boundaries.prod.components.gw.uses.models.ghost"}},
		},
		{
			name: "attachments on a connection the developer drew pass",
			agg:  rootDocument(declaredNoNim),
			rel: releaseFor(false, "uses:\n  models:\n    gpt:\n      features:\n        credential:\n"+
				"          infra:\n            rotation: hourly\n", ""),
		},
		{
			name: "an entry carrying neither configures nothing and passes",
			agg:  rootDocument(declaredNoNim),
			rel:  releaseFor(false, "uses:\n  models:\n    ghost:\n      features: {}\n", ""),
		},
		{
			name: "attachments on a component the composition does not hold halt in the flattening",
			agg:  rootDocument(declaredNoNim),
			rel: releaseFor(false, "", "ghost:\n  uses:\n    models:\n      gpt:\n        features:\n"+
				"          credential:\n            infra:\n              rotation: hourly\n"),
			wantGuard: `release edge ghost/models/gpt sets features on "ghost", which is not a component in the topology`,
		},
		{
			name: "infra and attachments on a component the composition does not hold name both",
			agg:  rootDocument(declaredNoNim),
			rel: releaseFor(false, "", "ghost:\n  uses:\n    models:\n      gpt:\n        infra:\n          timeout: 5\n"+
				"        features:\n          credential:\n            infra:\n              rotation: hourly\n"),
			wantGuard: `release edge ghost/models/gpt sets infra and features on "ghost", which is not a component in the topology`,
		},
		{
			name: "attachments on a component switched off pass",
			agg: strings.Replace(rootDocument(declaredNoNim),
				"      type: app/gateway:1.0.0\n", "      type: app/gateway:1.0.0\n      active: \"false\"\n", 1),
			rel: releaseFor(false, ghostAttachment, ""),
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var err error
			func() {
				defer func() {
					if r := recover(); r != nil {
						t.Fatalf("the build panicked: %v", r)
					}
				}()
				_, err = buildResultWithCatalog(t, connectionCatalog(t), tc.agg, tc.rel, "")
			}()
			switch {
			case tc.wantGuard != "":
				if !errors.Is(err, ErrUnboundReleaseEdgeConfig()) {
					t.Fatalf("the build must halt on the unbound-configuration condition; got %v", err)
				}
				if !strings.Contains(err.Error(), tc.wantGuard) {
					t.Fatalf("the guard's sentence must name what the entry sets.\n got: %v\nwant it to contain: %s",
						err, tc.wantGuard)
				}
			case len(tc.want) > 0:
				var got []releaseFinding
				for _, v := range releaseFindings(t, err, ErrUnboundReleaseEdgeConfig()) {
					got = append(got, releaseFinding{entity: v.Entity, field: v.Field})
				}
				if !reflect.DeepEqual(got, tc.want) {
					t.Errorf("findings = %+v; want %+v", got, tc.want)
				}
			default:
				if err != nil {
					t.Fatalf("the composition must build; got %v", err)
				}
			}
		})
	}
}

// TestReleaseComponentKeySpellingAConnectionAttachmentHalts proves a Release
// component entry's own `features` key spelling an attachment one of the
// component's connections carries — the alias, a dot, and the slot — halts
// rather than folding into that attachment, whichever document put the
// attachment on the connection and whether the component is the Aggregate's own
// or a sub-module's. A dotted key no connection attachment takes is an ordinary
// attachment name and passes.
//
// The finding names the component entry the operator wrote, leads with the key,
// and its remedy names the connection entry the attachment belongs on, so both
// spellings reach the author.
func TestReleaseComponentKeySpellingAConnectionAttachmentHalts(t *testing.T) {
	t.Parallel()

	const declared = "models:\n  gpt:\n    target: gpt\n    features:\n      credential:\n        type: core/referenced-secret\n"
	const bare = "models:\n  gpt:\n    target: gpt\n"
	const keySpellingGPT = "features:\n  gpt.credential:\n    infra:\n      rotation: hourly\n"
	cases := []struct {
		name string
		agg  string
		mod  string
		rel  string
		want []releaseFinding
	}{
		{
			name: "the developer put the attachment on the connection",
			agg:  rootDocument(declared),
			rel:  releaseFor(false, keySpellingGPT, ""),
			want: []releaseFinding{{entity: "Release demo-prod", field: ".spec.boundaries.prod.components.gw"}},
		},
		{
			name: "the operator put the attachment on the connection",
			agg:  rootDocument(bare),
			rel: releaseFor(false, "uses:\n  models:\n    gpt:\n      features:\n        credential:\n"+
				"          type: core/referenced-secret\n"+keySpellingGPT, ""),
			want: []releaseFinding{{entity: "Release demo-prod", field: ".spec.boundaries.prod.components.gw"}},
		},
		{
			name: "a component a sub-module contributes, addressed through its routing entry",
			agg:  moduleRoot,
			mod:  moduleDocument(declared),
			rel:  releaseFor(true, keySpellingGPT, ""),
			want: []releaseFinding{{entity: "Release demo-prod", field: ".spec.boundaries.prod.aggregates.mod.components.gw"}},
		},
		{
			name: "a dotted key no connection attachment takes passes",
			agg:  rootDocument(declared),
			rel:  releaseFor(false, "features:\n  nim.credential:\n    type: core/referenced-secret\n", ""),
		},
		{
			name: "the attachment written only on its connection passes",
			agg:  rootDocument(declared),
			rel: releaseFor(false, "uses:\n  models:\n    gpt:\n      features:\n        credential:\n"+
				"          infra:\n            rotation: hourly\n", ""),
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var err error
			func() {
				defer func() {
					if r := recover(); r != nil {
						t.Fatalf("the build panicked: %v", r)
					}
				}()
				_, err = buildResultWithCatalog(t, connectionCatalog(t), tc.agg, tc.rel, tc.mod)
			}()
			if len(tc.want) == 0 {
				if err != nil {
					t.Fatalf("the composition must build; got %v", err)
				}
				return
			}
			found := releaseFindings(t, err, ErrConnectionAttachmentWrittenOffItsConnection())
			var got []releaseFinding
			for _, v := range found {
				got = append(got, releaseFinding{entity: v.Entity, field: v.Field})
				if !strings.Contains(v.Message, `"gpt.credential"`) {
					t.Errorf("the finding must lead with the key the Release wrote; message %q", v.Message)
				}
				if !strings.Contains(v.Advice, "uses.models.gpt.features.credential") {
					t.Errorf("the remedy must name the connection entry the attachment belongs on; advice %q", v.Advice)
				}
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("findings = %+v; want %+v", got, tc.want)
			}
		})
	}
}

// releaseWithBoundaries writes a Release whose spec.boundaries block is body,
// already indented under it.
func releaseWithBoundaries(body string) string {
	return `apiVersion: aggrega.ai/v1beta1
kind: Release
metadata:
  name: demo-prod
spec:
  boundaries:
` + body
}

// boundaryFinding is where one finding about a boundary-wide key sits and which
// component it names.
type boundaryFinding struct {
	entity, field, component string
}

// TestReleaseBoundaryKeySpellingAConnectionAttachmentHalts proves the rule a
// component entry's key is held to holds one scope out: an attachment a Release
// boundary declares on itself under the name an attachment on one of the
// connections of a component it reaches takes — the alias, a dot, and the slot —
// halts rather than folding into that attachment. It holds whether the key names
// a type or not, whichever document put the attachment on the connection,
// whether the component sits in the boundary or in one nested inside it, and for
// a component a routed sub-module contributes that the boundary never names. A
// dotted key no connection attachment of a reached component takes, and a key
// on a boundary that reaches no component carrying one, are ordinary attachment
// names and pass.
//
// The finding names the boundary's own attachment block, leads with the key,
// names the component whose connection carries the attachment, and its remedy
// names the connection entry the attachment belongs on, so both spellings reach
// the author.
func TestReleaseBoundaryKeySpellingAConnectionAttachmentHalts(t *testing.T) {
	t.Parallel()

	const declared = "models:\n  gpt:\n    target: gpt\n    features:\n      credential:\n        type: core/referenced-secret\n"
	const bare = "models:\n  gpt:\n    target: gpt\n"
	const typedKey = "      features:\n        gpt.credential:\n          type: core/referenced-secret\n" +
		"          infra:\n            rotation: hourly\n"
	const untypedKey = "      features:\n        gpt.credential:\n          infra:\n            rotation: hourly\n"
	const allThree = "      components:\n        gw: {}\n        gpt: {}\n        nim: {}\n"
	cases := []struct {
		name string
		agg  string
		mod  string
		rel  string
		want []boundaryFinding
	}{
		{
			name: "a typed key the developer's connection attachment takes",
			agg:  rootDocument(declared),
			rel:  releaseWithBoundaries("    prod:\n" + typedKey + allThree),
			want: []boundaryFinding{{entity: "Release demo-prod", field: ".spec.boundaries.prod.features", component: "gw"}},
		},
		{
			name: "an untyped key the developer's connection attachment takes",
			agg:  rootDocument(declared),
			rel:  releaseWithBoundaries("    prod:\n" + untypedKey + allThree),
			want: []boundaryFinding{{entity: "Release demo-prod", field: ".spec.boundaries.prod.features", component: "gw"}},
		},
		{
			name: "a key the operator's connection attachment takes",
			agg:  rootDocument(bare),
			rel: releaseWithBoundaries("    prod:\n" + untypedKey + "      components:\n" +
				"        gw:\n          uses:\n            models:\n              gpt:\n                features:\n" +
				"                  credential:\n                    type: core/referenced-secret\n" +
				"        gpt: {}\n        nim: {}\n"),
			want: []boundaryFinding{{entity: "Release demo-prod", field: ".spec.boundaries.prod.features", component: "gw"}},
		},
		{
			name: "a key on an enclosing boundary reaching a component of a nested one",
			agg:  rootDocument(declared),
			rel: releaseWithBoundaries("    prod:\n" + typedKey + "      components:\n        gpt: {}\n        nim: {}\n" +
				"      boundaries:\n        inner:\n          components:\n            gw: {}\n"),
			want: []boundaryFinding{{entity: "Release demo-prod", field: ".spec.boundaries.prod.features", component: "gw"}},
		},
		{
			name: "a component a sub-module contributes, routed in without being named",
			agg:  moduleRoot,
			mod:  moduleDocument(declared),
			rel:  releaseWithBoundaries("    prod:\n" + untypedKey + "      aggregates:\n        mod: {}\n"),
			want: []boundaryFinding{{entity: "Release demo-prod", field: ".spec.boundaries.prod.features", component: "mod.gw"}},
		},
		{
			name: "a dotted key no connection attachment takes passes",
			agg:  rootDocument(declared),
			rel: releaseWithBoundaries("    prod:\n      features:\n        nim.credential:\n" +
				"          type: core/referenced-secret\n" + allThree),
		},
		{
			name: "a key on a boundary reaching no component that carries one passes",
			agg:  rootDocument(declared),
			rel: releaseWithBoundaries("    edge:\n" + typedKey + "      components:\n        gpt: {}\n        nim: {}\n" +
				"    prod:\n      components:\n        gw: {}\n"),
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var err error
			func() {
				defer func() {
					if r := recover(); r != nil {
						t.Fatalf("the build panicked: %v", r)
					}
				}()
				_, err = buildResultWithCatalog(t, connectionCatalog(t), tc.agg, tc.rel, tc.mod)
			}()
			if len(tc.want) == 0 {
				if err != nil {
					t.Fatalf("the composition must build; got %v", err)
				}
				return
			}
			found := releaseFindings(t, err, ErrConnectionAttachmentWrittenOffItsConnection())
			var got []boundaryFinding
			for _, v := range found {
				component := ""
				for _, want := range tc.want {
					if strings.Contains(v.Message, `of component "`+want.component+`"`) {
						component = want.component
					}
				}
				got = append(got, boundaryFinding{entity: v.Entity, field: v.Field, component: component})
				if !strings.HasPrefix(v.Message, `"gpt.credential"`) {
					t.Errorf("the finding must lead with the key the Release wrote; message %q", v.Message)
				}
				if !strings.Contains(v.Advice, "uses.models.gpt.features.credential") ||
					!strings.Contains(v.Advice, "this boundary's features") {
					t.Errorf("the remedy must name the connection entry the attachment belongs on and the "+
						"boundary block to remove it from; advice %q", v.Advice)
				}
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("findings = %+v; want %+v", got, tc.want)
			}
		})
	}
}

// TestReleaseOnlyConnectionAttachmentNamesItsType proves an attachment the
// Release puts on a connection under a slot the developer declares nothing for
// on that connection must name its Feature type, while an entry sizing the
// developer's attachment needs none — for a component the Aggregate declares and
// one a sub-module contributes alike. A component switched off passes: its
// entries are inert by the operator's own choice.
func TestReleaseOnlyConnectionAttachmentNamesItsType(t *testing.T) {
	t.Parallel()

	const sizingNim = "uses:\n  models:\n    nim:\n      features:\n        credential:\n          infra:\n            rotation: hourly\n"
	cases := []struct {
		name string
		agg  string
		mod  string
		rel  string
		want []releaseFinding
	}{
		{
			name: "an attachment only the Release makes, with no type",
			agg:  rootDocument(declaredNoNim),
			rel:  releaseFor(false, sizingNim, ""),
			want: []releaseFinding{{entity: "Release demo-prod", field: ".spec.boundaries.prod.components.gw.uses.models.nim"}},
		},
		{
			name: "an attachment only the Release makes, naming its type",
			agg:  rootDocument(declaredNoNim),
			rel: releaseFor(false, "uses:\n  models:\n    nim:\n      features:\n        credential:\n"+
				"          type: core/referenced-secret\n", ""),
		},
		{
			name: "sizing the attachment the developer declared, with no type",
			agg:  rootDocument(declaredNoNim),
			rel: releaseFor(false, "uses:\n  models:\n    gpt:\n      features:\n        credential:\n"+
				"          infra:\n            rotation: hourly\n", ""),
		},
		{
			name: "a component a sub-module contributes, addressed through its routing entry",
			agg:  moduleRoot,
			mod:  moduleDocument(declaredNoNim),
			rel:  releaseFor(true, sizingNim, ""),
			want: []releaseFinding{{
				entity: "Release demo-prod",
				field:  ".spec.boundaries.prod.aggregates.mod.components.gw.uses.models.nim",
			}},
		},
		{
			name: "a component switched off",
			agg: strings.Replace(rootDocument(declaredNoNim),
				"      type: app/gateway:1.0.0\n", "      type: app/gateway:1.0.0\n      active: \"false\"\n", 1),
			rel: releaseFor(false, sizingNim, ""),
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var err error
			func() {
				defer func() {
					if r := recover(); r != nil {
						t.Fatalf("the build panicked: %v", r)
					}
				}()
				_, err = buildResultWithCatalog(t, connectionCatalog(t), tc.agg, tc.rel, tc.mod)
			}()
			if len(tc.want) == 0 {
				if err != nil {
					t.Fatalf("the composition must build; got %v", err)
				}
				return
			}
			found := releaseFindings(t, err, ErrReleaseConnectionAttachmentUntyped())
			var got []releaseFinding
			for _, v := range found {
				got = append(got, releaseFinding{entity: v.Entity, field: v.Field})
				if !strings.HasPrefix(v.Message, `"credential"`) {
					t.Errorf("the finding must lead with the slot the attachment fills; message %q", v.Message)
				}
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("findings = %+v; want %+v", got, tc.want)
			}
		})
	}
}

// TestReleaseConnectionAttachmentTypeAgreesWithTheDeveloper proves an attachment
// both documents write on one connection must name one Feature: the operator
// naming a different type than the developer named halts at the Release's
// connection entry, naming both types, for a component the Aggregate declares
// and one a sub-module contributes alike. Naming the same type, naming none, and
// an attachment only the Release makes each pass.
//
// Each row carries one fault or none, and the passing rows are the controls that
// move one variable away from a failing one.
func TestReleaseConnectionAttachmentTypeAgreesWithTheDeveloper(t *testing.T) {
	t.Parallel()

	retype := func(featureType string) string {
		return "uses:\n  models:\n    gpt:\n      features:\n        credential:\n          type: " + featureType + "\n"
	}
	cases := []struct {
		name string
		agg  string
		mod  string
		rel  string
		want []releaseFinding
	}{
		{
			name: "the operator names another Feature for the developer's attachment",
			agg:  rootDocument(declaredNoNim),
			rel:  releaseFor(false, retype("core/generated-secret"), ""),
			want: []releaseFinding{{entity: "Release demo-prod", field: ".spec.boundaries.prod.components.gw.uses.models.gpt"}},
		},
		{
			name: "the operator names the developer's Feature",
			agg:  rootDocument(declaredNoNim),
			rel:  releaseFor(false, retype("core/referenced-secret"), ""),
		},
		{
			name: "the operator sizes the developer's attachment naming no Feature",
			agg:  rootDocument(declaredNoNim),
			rel: releaseFor(false, "uses:\n  models:\n    gpt:\n      features:\n        credential:\n"+
				"          infra:\n            rotation: hourly\n", ""),
		},
		{
			name: "an attachment only the Release makes",
			agg:  rootDocument(declaredNoNim),
			rel: releaseFor(false, "uses:\n  models:\n    nim:\n      features:\n        credential:\n"+
				"          type: core/generated-secret\n", ""),
		},
		{
			name: "a component a sub-module contributes, addressed through its routing entry",
			agg:  moduleRoot,
			mod:  moduleDocument(declaredNoNim),
			rel:  releaseFor(true, retype("core/generated-secret"), ""),
			want: []releaseFinding{{
				entity: "Release demo-prod",
				field:  ".spec.boundaries.prod.aggregates.mod.components.gw.uses.models.gpt",
			}},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var err error
			func() {
				defer func() {
					if r := recover(); r != nil {
						t.Fatalf("the build panicked: %v", r)
					}
				}()
				_, err = buildResultWithCatalog(t, connectionCatalog(t), tc.agg, tc.rel, tc.mod)
			}()
			if len(tc.want) == 0 {
				if err != nil {
					t.Fatalf("the composition must build; got %v", err)
				}
				return
			}
			found := releaseFindings(t, err, resolver.ErrFeatureMergeConflict())
			var got []releaseFinding
			for _, v := range found {
				got = append(got, releaseFinding{entity: v.Entity, field: v.Field})
				for _, both := range []string{
					`the Aggregate names "core/referenced-secret"`,
					`the Release names "core/generated-secret"`,
					`for the slot "credential"`,
					`on the connection "gpt"`,
				} {
					if !strings.Contains(v.Message, both) {
						t.Errorf("the finding must name %s; message %q", both, v.Message)
					}
				}
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("findings = %+v; want %+v", got, tc.want)
			}
		})
	}
}

// TestTopologyEdgeAxisEnumeratesEveryDevelopersConnection proves the flattened
// composition's edge axis presents every connection a node's own dependency
// block wrote — for a component a sub-module contributes as for one the
// Aggregate declares — positioned at the connection entry in the document the
// component was written in, with a connection the short form listed stopping at
// its socket. It is the axis the declared-slots rule's later half is invoked
// along, so an axis enumerating nothing would leave that half asked about
// nothing while every build stayed green.
func TestTopologyEdgeAxisEnumeratesEveryDevelopersConnection(t *testing.T) {
	t.Parallel()

	const uses = "models:\n  gpt:\n    target: gpt\n    features:\n      credential:\n        type: core/referenced-secret\n" +
		"  nim:\n    target: nim\nlogs: [gpt]\n"
	result, err := buildResultWithCatalog(t, connectionCatalog(t), moduleRoot, "", moduleDocument(uses))
	if err != nil {
		t.Fatalf("the composition must build; got %v", err)
	}
	axis, ok := result.StageCoordinates.(constraint.EdgeEnumerator)
	if !ok {
		t.Fatalf("the view the build produced does not supply the edge axis (%T)", result.StageCoordinates)
	}
	var got []string
	counting := constraint.Check[constraint.TopologyView, constraint.EdgeSubject]{
		Sentinel: resolver.ErrConnectionAttachmentSlotUndeclared(),
		Severity: constraint.SeverityFatal,
		Run: func(_ constraint.TopologyView, s constraint.EdgeSubject) []constraint.Objection {
			got = append(got, strings.Join([]string{s.NodeID(), s.SocketName(), s.AliasName(), s.SourceFile(), s.Locator()}, "|"))
			return nil
		},
	}
	if _, err := constraint.RunOnEdges(nil, axis, result.StageView, counting); err != nil {
		t.Fatalf("the traversal must not fail over a composition the build accepted; got %v", err)
	}
	want := []string{
		"mod.gw|models|gpt|mod.yaml|.spec.components.gw.uses.models.gpt",
		"mod.gw|models|nim|mod.yaml|.spec.components.gw.uses.models.nim",
		"mod.gw|logs|gpt|mod.yaml|.spec.components.gw.uses.logs",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("the edge axis enumerated\n  %v\nwant\n  %v", got, want)
	}
}
