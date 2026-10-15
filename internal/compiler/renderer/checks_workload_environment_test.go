// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"errors"
	"reflect"
	"slices"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/constraint"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"github.com/aggrega-ai/aggrega-cli/pkg/validator"
)

// The rule that one container names each environment variable once is declared
// as three values, one per document that authors a Workload, and this file runs
// each through the framework's own traversal over a fact stub.
//
// The stub embeds the view interface and overrides only the three facts the rule
// reads. The embedded interface is nil, so any other accessor panics rather than
// answering a zero value — which turns "the predicate reads its declared facts and
// nothing else" into a property this file enforces.
//
// Every record below is the objection's file, locator and detail values in raise
// order, compared as an ordered list: the order the rule raises in is the order a
// reader meets the findings in, and it is part of what the rule promises.

// workloadEnvironmentFactStub answers the three environment-name facts, each keyed
// the way its declaration states, and nothing else.
type workloadEnvironmentFactStub struct {
	constraint.HydratedView

	node       map[string][]constraint.ContainerEnvironmentNames
	attachment map[string][]constraint.ContainerEnvironmentNames
	boundary   map[string][]constraint.ContainerEnvironmentNames
}

func (s workloadEnvironmentFactStub) ParsedNodeWorkloadEnvironmentNames(
	node string,
) []constraint.ContainerEnvironmentNames {
	return s.node[factKey(node)]
}

func (s workloadEnvironmentFactStub) AttachmentParsedWorkloadEnvironmentNames(
	node, attachment string,
) []constraint.ContainerEnvironmentNames {
	return s.attachment[factKey(node, attachment)]
}

func (s workloadEnvironmentFactStub) BoundaryAttachmentParsedWorkloadEnvironmentNames(
	boundary, attachment string,
) []constraint.ContainerEnvironmentNames {
	return s.boundary[factKey(boundary, attachment)]
}

// mainContainer and initContainer build one container's names the way the fact
// answers them.
func mainContainer(name string, variables, envAttachments []string) constraint.ContainerEnvironmentNames {
	return constraint.ContainerEnvironmentNames{Name: name, Variables: variables, EnvAttachments: envAttachments}
}

func initContainer(position int, variables, envAttachments []string) constraint.ContainerEnvironmentNames {
	return constraint.ContainerEnvironmentNames{
		Init: true, Position: position, Variables: variables, EnvAttachments: envAttachments,
	}
}

// workloadEnvironmentRecords renders objections as file|locator|detail records, in
// raise order.
func workloadEnvironmentRecords(objections []constraint.Objection) []string {
	out := make([]string, 0, len(objections))
	for _, o := range objections {
		out = append(out, objectionRecord(o))
	}
	return out
}

// assertWorkloadEnvironmentObjections holds what a value raised to the rule's
// identity and remedy, whatever the row.
func assertWorkloadEnvironmentObjections(t *testing.T, raised []constraint.Objection) {
	t.Helper()
	for _, o := range raised {
		if !errors.Is(o.Sentinel(), validator.ErrDuplicateContainerEnvironmentVariable()) {
			t.Errorf("an objection carries sentinel %v; want the duplicate-environment-variable condition",
				o.Sentinel())
		}
		if o.Advice() != workloadEnvironmentAdvice {
			t.Errorf("an objection carries advice %q; want %q", o.Advice(), workloadEnvironmentAdvice)
		}
	}
}

// TestTheNodesOwnWorkloadEnvironmentRule runs the value declared over a node's own
// Recipe through the traversal the render invokes it with, row by row.
//
// The rows hold the rule's whole grid: a pair on a main container and on an init
// container raise; distinct names, the same name in two different containers, and
// a container whose only same-named entry is mounted as a file stay silent; and
// several pairs raise once each, in author order. The first row is the positive
// control: a stub that answered nothing would fail it.
func TestTheNodesOwnWorkloadEnvironmentRule(t *testing.T) {
	t.Parallel()

	const file = "recipes/web.yaml"
	at := file + "|.spec.workload|"
	for _, row := range []struct {
		name       string
		containers []constraint.ContainerEnvironmentNames
		want       []string
	}{
		{
			name: "a main container naming one variable both ways raises",
			containers: []constraint.ContainerEnvironmentNames{
				mainContainer("main", []string{"APP_MODE", "DB_PASSWORD"}, []string{"DB_PASSWORD"}),
			},
			want: []string{at + "DB_PASSWORD,main"},
		},
		{
			name: "an init container naming one variable both ways raises, named by its place in the list",
			containers: []constraint.ContainerEnvironmentNames{
				mainContainer("main", []string{"APP_MODE"}, nil),
				initContainer(1, []string{"TOKEN"}, []string{"TOKEN"}),
			},
			want: []string{at + "TOKEN,initContainers[1]"},
		},
		{
			// The fact carries only the entries delivered as env, so a file-mounted
			// entry sharing a variable's name reaches the rule as no entry at all.
			name: "an entry mounted as a file sharing a variable's name is silent",
			containers: []constraint.ContainerEnvironmentNames{
				mainContainer("main", []string{"CERT"}, nil),
			},
			want: nil,
		},
		{
			name: "distinct names are silent",
			containers: []constraint.ContainerEnvironmentNames{
				mainContainer("main", []string{"APP_MODE"}, []string{"DB_PASSWORD"}),
			},
			want: nil,
		},
		{
			name: "one name in two different containers is silent",
			containers: []constraint.ContainerEnvironmentNames{
				mainContainer("main", []string{"DB_PASSWORD"}, nil),
				mainContainer("sidecar", nil, []string{"DB_PASSWORD"}),
			},
			want: nil,
		},
		{
			name: "several pairs raise once each, in author order",
			containers: []constraint.ContainerEnvironmentNames{
				// zeta is authored first, so it is reported first; within it the
				// attachments' order decides, not the variables'.
				mainContainer("zeta", []string{"B", "A"}, []string{"A", "B"}),
				mainContainer("alpha", []string{"C"}, []string{"C"}),
				initContainer(0, []string{"D"}, []string{"D"}),
			},
			want: []string{
				at + "A,zeta",
				at + "B,zeta",
				at + "C,alpha",
				at + "D,initContainers[0]",
			},
		},
	} {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			var facts constraint.HydratedView = workloadEnvironmentFactStub{
				node: map[string][]constraint.ContainerEnvironmentNames{factKey("web"): row.containers},
			}
			axis := oneRenderedWorkload{nodeID: "web", sourceFile: file}
			raised, err := constraint.RunOnNodeTemplateFields(nil, axis, axis, facts, WorkloadEnvironmentVariableNamedOnce)
			if err != nil {
				t.Fatalf("the traversal returned %v with no cancellation supplied", err)
			}
			if got := workloadEnvironmentRecords(raised); !slices.Equal(got, row.want) {
				t.Errorf("raised %q; want %q", got, row.want)
			}
			assertWorkloadEnvironmentObjections(t, raised)
		})
	}
}

// TestTheWorkloadEnvironmentRuleCaptionsWhatTheSentenceDoesNotName reads the
// detail the way a message spells it: the variable bare, because the registered
// sentence names an environment variable; the container captioned; and, on an
// attachment's Workload, the attachment captioned after it.
func TestTheWorkloadEnvironmentRuleCaptionsWhatTheSentenceDoesNotName(t *testing.T) {
	t.Parallel()

	pair := []constraint.ContainerEnvironmentNames{
		mainContainer("main", []string{"DB_PASSWORD"}, []string{"DB_PASSWORD"}),
		initContainer(0, []string{"TOKEN"}, []string{"TOKEN"}),
	}
	var facts constraint.HydratedView = workloadEnvironmentFactStub{
		node:       map[string][]constraint.ContainerEnvironmentNames{factKey("web"): pair},
		attachment: map[string][]constraint.ContainerEnvironmentNames{factKey("web", "mon"): pair},
	}

	own := oneRenderedWorkload{nodeID: "web", sourceFile: "recipes/web.yaml"}
	raised, err := constraint.RunOnNodeTemplateFields(nil, own, own, facts, WorkloadEnvironmentVariableNamedOnce)
	if err != nil || len(raised) != 2 {
		t.Fatalf("the node's own value raised %d objection(s), err %v; want 2", len(raised), err)
	}
	if got, want := raised[0].RenderedDetail(), []string{`"DB_PASSWORD"`, `container "main"`}; !slices.Equal(got, want) {
		t.Errorf("a main container's pair reads %q; want %q", got, want)
	}
	if got, want := raised[1].RenderedDetail(), []string{`"TOKEN"`, `init container "initContainers[0]"`}; !slices.Equal(got, want) {
		t.Errorf("an init container's pair reads %q; want %q", got, want)
	}

	onNode := oneRenderedAttachmentWorkload{
		key: nodeAttachmentKey{node: "web", attachment: "mon"}, sourceFile: "feature-recipes/mon.yaml",
	}
	raised, err = constraint.RunOnNodeAttachmentTemplateFields(
		nil, onNode, onNode, onNode, facts, AttachmentWorkloadEnvironmentVariableNamedOnce)
	if err != nil || len(raised) != 2 {
		t.Fatalf("the attachment's value raised %d objection(s), err %v; want 2", len(raised), err)
	}
	if got, want := raised[0].RenderedDetail(),
		[]string{`"DB_PASSWORD"`, `container "main"`, `attachment "mon"`}; !slices.Equal(got, want) {
		t.Errorf("an attachment's pair reads %q; want %q", got, want)
	}
}

// TestTheAttachmentWorkloadEnvironmentRulesReadTheirOwnOwner runs the two values
// declared over a FeatureRecipe's Workload, and proves each reads the facts keyed
// by its own owner.
//
// The host node's own Workload carries a pair in every row and each attachment's
// carries none, or the other way round, so a value reading the node-keyed fact
// would raise where the attachment's Workload is clean and stay silent where it is
// not. Both directions are driven.
func TestTheAttachmentWorkloadEnvironmentRulesReadTheirOwnOwner(t *testing.T) {
	t.Parallel()

	pair := []constraint.ContainerEnvironmentNames{
		mainContainer("main", []string{"DB_PASSWORD"}, []string{"DB_PASSWORD"}),
	}
	clean := []constraint.ContainerEnvironmentNames{
		mainContainer("main", []string{"APP_MODE"}, []string{"DB_PASSWORD"}),
	}

	onNode := oneRenderedAttachmentWorkload{
		key: nodeAttachmentKey{node: "web", attachment: "mon"}, sourceFile: "feature-recipes/mon.yaml",
	}
	onBoundary := oneRenderedBoundaryAttachmentWorkload{
		key: boundaryAttachmentKey{boundary: "cluster", attachment: "bridge"}, sourceFile: "feature-recipes/bridge.yaml",
	}

	for _, row := range []struct {
		name        string
		hostHas     []constraint.ContainerEnvironmentNames
		ownerHas    []constraint.ContainerEnvironmentNames
		wantOnNode  []string
		wantOnBound []string
	}{
		{
			name: "the owner's pair raises while the host is clean", hostHas: clean, ownerHas: pair,
			wantOnNode:  []string{"feature-recipes/mon.yaml|.spec.workload|DB_PASSWORD,main,mon"},
			wantOnBound: []string{"feature-recipes/bridge.yaml|.spec.workload|DB_PASSWORD,main,bridge"},
		},
		{
			name: "the host's pair does not raise for a clean owner", hostHas: pair, ownerHas: clean,
			wantOnNode: nil, wantOnBound: nil,
		},
	} {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			var facts constraint.HydratedView = workloadEnvironmentFactStub{
				node:       map[string][]constraint.ContainerEnvironmentNames{factKey("web"): row.hostHas},
				attachment: map[string][]constraint.ContainerEnvironmentNames{factKey("web", "mon"): row.ownerHas},
				boundary:   map[string][]constraint.ContainerEnvironmentNames{factKey("cluster", "bridge"): row.ownerHas},
			}

			raised, err := constraint.RunOnNodeAttachmentTemplateFields(
				nil, onNode, onNode, onNode, facts, AttachmentWorkloadEnvironmentVariableNamedOnce)
			if err != nil {
				t.Fatalf("the node-carried traversal returned %v", err)
			}
			if got := workloadEnvironmentRecords(raised); !slices.Equal(got, row.wantOnNode) {
				t.Errorf("the node-carried attachment's value raised %q; want %q", got, row.wantOnNode)
			}
			assertWorkloadEnvironmentObjections(t, raised)

			raised, err = constraint.RunOnBoundaryAttachmentTemplateFields(
				nil, onBoundary, onBoundary, onBoundary, facts, BoundaryAttachmentWorkloadEnvironmentVariableNamedOnce)
			if err != nil {
				t.Fatalf("the boundary-rooted traversal returned %v", err)
			}
			if got := workloadEnvironmentRecords(raised); !slices.Equal(got, row.wantOnBound) {
				t.Errorf("the boundary-scope attachment's value raised %q; want %q", got, row.wantOnBound)
			}
			assertWorkloadEnvironmentObjections(t, raised)
		})
	}
}

// workloadFieldsAxis presents a node and a list of its Recipe's template fields,
// for the one test below that needs a field other than the Workload's.
type workloadFieldsAxis struct {
	node   string
	fields []string
}

func (a workloadFieldsAxis) Nodes() []constraint.NodeInstance {
	return []constraint.NodeInstance{{ID: a.node}}
}

func (a workloadFieldsAxis) NodeTemplateFields(node string) []constraint.TemplateFieldCoordinate {
	if node != a.node {
		return nil
	}
	out := make([]constraint.TemplateFieldCoordinate, 0, len(a.fields))
	for _, field := range a.fields {
		out = append(out, constraint.TemplateFieldCoordinate{
			Value: field, SourceFile: "recipes/web.yaml", AuthoredKeys: []string{field},
		})
	}
	return out
}

// TestTheWorkloadEnvironmentRuleActsOnTheWorkloadFieldAlone pins that a value
// handed a wider template-field axis still asks about the Workload once: invoked
// over three fields of one Recipe, it raises one pair once, positioned at the
// Workload key, and nothing at the other two.
func TestTheWorkloadEnvironmentRuleActsOnTheWorkloadFieldAlone(t *testing.T) {
	t.Parallel()

	var facts constraint.HydratedView = workloadEnvironmentFactStub{
		node: map[string][]constraint.ContainerEnvironmentNames{factKey("web"): {
			mainContainer("main", []string{"DB_PASSWORD"}, []string{"DB_PASSWORD"}),
		}},
	}
	axis := workloadFieldsAxis{node: "web", fields: []string{"valuesMapping", recipeFieldWorkload, "artifacts"}}
	raised, err := constraint.RunOnNodeTemplateFields(nil, axis, axis, facts, WorkloadEnvironmentVariableNamedOnce)
	if err != nil {
		t.Fatalf("the traversal returned %v", err)
	}
	want := []string{"recipes/web.yaml|.spec.workload|DB_PASSWORD,main"}
	if got := workloadEnvironmentRecords(raised); !slices.Equal(got, want) {
		t.Errorf("raised %q over three fields; want %q — once, at the Workload key", got, want)
	}
}

// TestWorkloadEnvironmentNamesReadsNamesInAuthorOrder holds the fact's production
// to its declaration: main containers in author order, then init containers by
// position; within each, the plain variables in author order and the attachments
// entries delivered as env in author order, with an entry mounted as a file left
// out. The workload carries values nobody reads, which the answer must not carry.
func TestWorkloadEnvironmentNamesReadsNamesInAuthorOrder(t *testing.T) {
	t.Parallel()

	if got := workloadEnvironmentNames(nil); got != nil {
		t.Errorf("a render that parsed no workload answers %+v; want nil", got)
	}

	zeta := types.ContainerSpec{Image: "app:1"}
	zeta.Variables.Set("B", "plain-b")
	zeta.Variables.Set("A", "plain-a")
	zeta.Attachments.Set("CERT", types.AttachmentRef{From: "tls", As: "file", Path: "/etc/tls/cert"})
	zeta.Attachments.Set("A", types.AttachmentRef{From: "credential", As: "env"})

	alpha := types.ContainerSpec{Image: "sidecar:1"}
	alpha.Variables.Set("MODE", "standard")

	migrate := types.ContainerSpec{Name: "migrate", Image: "app:1"}
	migrate.Attachments.Set("TOKEN", types.AttachmentRef{From: "credential", As: "env"})
	migrate.Variables.Set("TOKEN", "plain")

	w := &types.Workload{}
	w.Containers.Set("zeta", zeta)
	w.Containers.Set("alpha", alpha)
	w.InitContainers = []types.ContainerSpec{{Image: "busybox:1"}, migrate}

	want := []constraint.ContainerEnvironmentNames{
		{Name: "zeta", Variables: []string{"B", "A"}, EnvAttachments: []string{"A"}},
		{Name: "alpha", Variables: []string{"MODE"}},
		{Init: true, Position: 0},
		{Init: true, Position: 1, Variables: []string{"TOKEN"}, EnvAttachments: []string{"TOKEN"}},
	}
	if got := workloadEnvironmentNames(w); !reflect.DeepEqual(got, want) {
		t.Errorf("the fact answers\n  %+v\nwant\n  %+v", got, want)
	}
}
