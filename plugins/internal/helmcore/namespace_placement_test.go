// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package helmcore_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"reflect"
	"sort"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"

	abi "github.com/aggrega-ai/aggrega-abi-go/pkg/abi"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/rendercore"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
	"github.com/aggrega-ai/aggrega-cli/plugins/internal/helmcore"
)

// placementChart is a chart writing every case the namespace placement tells
// apart: namespaced resources writing no namespace, one written empty and two
// written null in its two spellings, one pinning a namespace of its own, and
// cluster-scoped resources writing none and one written empty.
var placementChart = map[string]string{
	"Chart.yaml": "apiVersion: v2\nname: web\nversion: 0.1.0\n",
	"templates/deployment.yaml": `apiVersion: apps/v1
kind: Deployment
metadata:
  name: web
spec:
  selector:
    matchLabels:
      app: web
  template:
    metadata:
      labels:
        app: web
    spec:
      containers:
        - name: server
          image: example/web:1
          ports:
            - name: http
              containerPort: 8080
`,
	"templates/service.yaml": `apiVersion: v1
kind: Service
metadata:
  name: web
spec:
  selector:
    app: web
  ports:
    - port: 80
      targetPort: http
`,
	"templates/pinned.yaml": `apiVersion: v1
kind: ConfigMap
metadata:
  name: pinned
  namespace: kube-system
data:
  k: v
`,
	"templates/empty.yaml": `apiVersion: v1
kind: ConfigMap
metadata:
  name: empty
  namespace: ""
data:
  k: v
`,
	"templates/null.yaml": `apiVersion: v1
kind: Secret
metadata:
  name: nulled
  namespace: null
---
apiVersion: v1
kind: ServiceAccount
metadata:
  name: tilde
  namespace: ~
`,
	"templates/cluster.yaml": `apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: reader
rules: []
---
apiVersion: storage.k8s.io/v1
kind: StorageClass
metadata:
  name: fast
  namespace: ""
provisioner: example.com/fast
`,
}

// renderPlaced renders a chart for node api, with namespace declared on its
// boundary unless it is empty, through the render the provider guest calls.
func renderPlaced(t *testing.T, files map[string]string, namespace string) helmcore.ChartRender {
	t.Helper()
	nc := nodeCtx("api", "in-memory")
	if namespace != "" {
		nc.Annotations = map[string]string{renderer.AnnotationNamespace: namespace}
	}
	rendered, err := helmcore.RenderChartWithholding(context.Background(), chartLoaderFor(files), map[string]any{}, nc,
		helmcore.NewHermeticValuesValidator(forbidFetch(t)), nil)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	return rendered
}

// emitted is what one emitted document writes at metadata.namespace: absent, or
// present with its tag and text.
type emitted struct {
	file    string
	present bool
	tag     string
	value   string
}

// emittedNamespaces reads each emitted file's documents and keys what each writes
// at metadata.namespace by "<kind>/<name>".
func emittedNamespaces(t *testing.T, files map[string][]byte) map[string]emitted {
	t.Helper()
	out := map[string]emitted{}
	for path, content := range files {
		dec := yaml.NewDecoder(bytes.NewReader(content))
		for {
			var doc yaml.Node
			err := dec.Decode(&doc)
			if errors.Is(err, io.EOF) {
				break
			}
			if err != nil {
				t.Fatalf("decode %s: %v", path, err)
			}
			var kind, name string
			e := emitted{file: path}
			root := doc.Content[0]
			for i := 0; i+1 < len(root.Content); i += 2 {
				switch root.Content[i].Value {
				case "kind":
					kind = root.Content[i+1].Value
				case "metadata":
					md := root.Content[i+1]
					for j := 0; j+1 < len(md.Content); j += 2 {
						switch md.Content[j].Value {
						case "name":
							name = md.Content[j+1].Value
						case "namespace":
							e.present, e.tag, e.value = true, md.Content[j+1].Tag, md.Content[j+1].Value
						}
					}
				}
			}
			out[kind+"/"+name] = e
		}
	}
	return out
}

// TestRenderChart_PlacesEveryNamespacedResourceWritingNoneInTheDeclaredNamespace
// renders a chart in a boundary declaring the namespace shop and reads, resource by
// resource, the namespace it is emitted with and the file it is emitted to. A
// namespaced resource writing no namespace, or writing it empty or null, is placed
// in shop and named with the namespace segment; one pinning its own keeps it;
// cluster-scoped resources keep exactly what the chart wrote. Every kind here is a
// built-in one, so the render raises no advisory.
func TestRenderChart_PlacesEveryNamespacedResourceWritingNoneInTheDeclaredNamespace(t *testing.T) {
	t.Parallel()

	rendered := renderPlaced(t, placementChart, "shop")
	got := emittedNamespaces(t, rendered.Files)

	for _, want := range []struct {
		resource, file string
		present        bool
		value          string
	}{
		{"Deployment/web", "api-deployment-shop-web.yaml", true, "shop"},
		{"Service/web", "api-service-shop-web.yaml", true, "shop"},
		{"ConfigMap/empty", "api-configmap-shop-empty.yaml", true, "shop"},
		{"Secret/nulled", "api-secret-shop-nulled.yaml", true, "shop"},
		{"ServiceAccount/tilde", "api-serviceaccount-shop-tilde.yaml", true, "shop"},
		{"ConfigMap/pinned", "api-configmap-kube-system-pinned.yaml", true, "kube-system"},
		{"ClusterRole/reader", "api-clusterrole-reader.yaml", false, ""},
		{"StorageClass/fast", "api-storageclass-fast.yaml", true, ""},
	} {
		e, ok := got[want.resource]
		if !ok {
			t.Errorf("%s was not emitted; emitted %v", want.resource, sortedEmitted(got))
			continue
		}
		if e.file != want.file {
			t.Errorf("%s is emitted to %q, want %q", want.resource, e.file, want.file)
		}
		if e.present != want.present || e.value != want.value {
			t.Errorf("%s writes namespace present=%v %q, want present=%v %q", want.resource, e.present, e.value, want.present, want.value)
		}
		if e.present && e.value != "" && e.tag != "!!str" {
			t.Errorf("%s writes its namespace tagged %s, want a string", want.resource, e.tag)
		}
	}
	if len(rendered.Advisories) != 0 {
		t.Errorf("a render of built-in kinds raised advisories: %v", rendered.Advisories)
	}
}

// TestRenderChart_PlacesNothingWhereNoNamespaceIsDeclared renders the same chart in
// a boundary declaring no namespace. Every document writes exactly what the chart
// wrote — nothing where it wrote nothing, the empty and null values as written, the
// cluster default nowhere — and the render raises no advisory.
func TestRenderChart_PlacesNothingWhereNoNamespaceIsDeclared(t *testing.T) {
	t.Parallel()

	rendered := renderPlaced(t, placementChart, "")
	got := emittedNamespaces(t, rendered.Files)

	for _, want := range []struct {
		resource string
		present  bool
		tag      string
		value    string
	}{
		{"Deployment/web", false, "", ""},
		{"Service/web", false, "", ""},
		{"ConfigMap/empty", true, "!!str", ""},
		{"Secret/nulled", true, "!!null", ""},
		{"ServiceAccount/tilde", true, "!!null", ""},
		{"ConfigMap/pinned", true, "!!str", "kube-system"},
		{"ClusterRole/reader", false, "", ""},
		{"StorageClass/fast", true, "!!str", ""},
	} {
		e, ok := got[want.resource]
		if !ok {
			t.Errorf("%s was not emitted; emitted %v", want.resource, sortedEmitted(got))
			continue
		}
		// A null keeps its tag; the spelling the encoder writes it in is not what
		// this row is about.
		value := e.value
		if e.tag == "!!null" {
			value = ""
		}
		if e.present != want.present || e.tag != want.tag || value != want.value {
			t.Errorf("%s writes namespace present=%v %s %q, want present=%v %s %q",
				want.resource, e.present, e.tag, e.value, want.present, want.tag, want.value)
		}
	}
	if e := got["Deployment/web"]; e.file != "api-deployment-web.yaml" {
		t.Errorf("Deployment/web is emitted to %q, want api-deployment-web.yaml", e.file)
	}
	if len(rendered.Advisories) != 0 {
		t.Errorf("a render with no namespace declared raised advisories: %v", rendered.Advisories)
	}
}

// TestRenderChart_APlacedResourceIsEmittedAsOneWhoseChartWritesTheNamespace renders
// two charts in one boundary declaring shop: one writing no namespace, one writing
// the release namespace itself. The two emit the same files under the same names,
// byte for byte, which is what placing a resource before its identity is derived
// promises.
func TestRenderChart_APlacedResourceIsEmittedAsOneWhoseChartWritesTheNamespace(t *testing.T) {
	t.Parallel()

	unplaced := map[string]string{
		"Chart.yaml":                placementChart["Chart.yaml"],
		"templates/deployment.yaml": placementChart["templates/deployment.yaml"],
		"templates/service.yaml":    placementChart["templates/service.yaml"],
	}
	selfPlaced := map[string]string{"Chart.yaml": placementChart["Chart.yaml"]}
	for _, name := range []string{"templates/deployment.yaml", "templates/service.yaml"} {
		selfPlaced[name] = strings.Replace(unplaced[name], "  name: web\n", "  name: web\n  namespace: {{ .Release.Namespace }}\n", 1)
	}

	placed := renderPlaced(t, unplaced, "shop").Files
	written := renderPlaced(t, selfPlaced, "shop").Files
	if !reflect.DeepEqual(placed, written) {
		t.Errorf("a placed chart emits\n%s\nand a chart writing the namespace itself emits\n%s", dump(placed), dump(written))
	}
	if _, ok := placed["api-deployment-shop-web.yaml"]; !ok {
		t.Errorf("the placed Deployment is not named with the namespace segment: %v", sortedFiles(placed))
	}
}

// TestRenderChart_ClassifiesACustomKindByTheCustomResourceDefinitionInItsRender
// renders custom resources whose kinds a CustomResourceDefinition in the same
// render declares: one in crds/, which is emitted after every template, declaring
// its kind namespaced, and one in a template sorting after the resource, declaring
// its kind cluster-scoped. The namespaced kind is placed and the cluster-scoped one
// is not, so both definitions were read before the resources they classify. Two
// definitions giving one kind different scopes classify it as neither, so its
// resource is left and reported.
func TestRenderChart_ClassifiesACustomKindByTheCustomResourceDefinitionInItsRender(t *testing.T) {
	t.Parallel()

	crd := func(plural, kind, scope string) string {
		return `apiVersion: apiextensions.k8s.io/v1
kind: CustomResourceDefinition
metadata:
  name: ` + plural + `.example.com
spec:
  group: example.com
  names:
    kind: ` + kind + `
    plural: ` + plural + `
  scope: ` + scope + `
  versions:
    - name: v1
      served: true
      storage: true
`
	}
	resource := func(kind, name string) string {
		return "apiVersion: example.com/v1\nkind: " + kind + "\nmetadata:\n  name: " + name + "\n"
	}
	rendered := renderPlaced(t, map[string]string{
		"Chart.yaml":                  placementChart["Chart.yaml"],
		"crds/widgets.yaml":           crd("widgets", "Widget", "Namespaced"),
		"templates/a-widget.yaml":     resource("Widget", "w"),
		"templates/b-gadget.yaml":     resource("Gadget", "g"),
		"templates/c-twin.yaml":       resource("Twin", "t"),
		"templates/y-twin-crds.yaml":  crd("twins", "Twin", "Namespaced") + "---\n" + strings.Replace(crd("twins", "Twin", "Cluster"), "twins.example.com", "twins-again.example.com", 1),
		"templates/z-gadget-crd.yaml": crd("gadgets", "Gadget", "Cluster"),
	}, "shop")
	got := emittedNamespaces(t, rendered.Files)

	if e := got["Widget/w"]; !e.present || e.value != "shop" || e.file != "api-widget-shop-w.yaml" {
		t.Errorf("Widget/w, declared namespaced by the definition in crds/, is emitted %+v; want it in shop", e)
	}
	if e := got["Gadget/g"]; e.present || e.file != "api-gadget-g.yaml" {
		t.Errorf("Gadget/g, declared cluster-scoped by a definition in a later template, is emitted %+v; want no namespace", e)
	}
	if e := got["Twin/t"]; e.present {
		t.Errorf("Twin/t, given two scopes by two definitions, is emitted %+v; want it left without a namespace", e)
	}
	for _, definition := range []string{"CustomResourceDefinition/widgets.example.com", "CustomResourceDefinition/gadgets.example.com"} {
		if e := got[definition]; e.present {
			t.Errorf("%s is cluster-scoped and is emitted with a namespace: %+v", definition, e)
		}
	}
	if len(rendered.Advisories) != 1 || !strings.Contains(rendered.Advisories[0].Message, `kind "Twin" in API group "example.com"`) {
		t.Errorf("advisories = %v, want exactly the one naming the doubly declared Twin", rendered.Advisories)
	}
}

// TestRenderChart_LeavesAKindItCannotClassifyAndReportsItOncePerKind renders kinds
// that are neither built in nor declared by a definition in the render. Each
// resource of such a kind writing no namespace is left as it is, and the render
// reports each kind once — two ServiceMonitors raise one report — naming the kind
// with its API group, in the order the render met them. A resource of an
// unclassified kind that writes a namespace keeps it and raises nothing, and a
// boundary declaring no namespace raises nothing at all.
func TestRenderChart_LeavesAKindItCannotClassifyAndReportsItOncePerKind(t *testing.T) {
	t.Parallel()

	chart := map[string]string{
		"Chart.yaml": placementChart["Chart.yaml"],
		"templates/monitor.yaml": "apiVersion: monitoring.coreos.com/v1\nkind: ServiceMonitor\nmetadata:\n  name: a\n" +
			"---\napiVersion: monitoring.coreos.com/v1\nkind: ServiceMonitor\nmetadata:\n  name: b\n",
		"templates/thing.yaml": "apiVersion: example.org/v1\nkind: Thing\nmetadata:\n  name: t\n" +
			"---\napiVersion: v1\nkind: Mystery\nmetadata:\n  name: m\n" +
			"---\nkind: Orphan\nmetadata:\n  name: o\n" +
			"---\napiVersion: example.net/v1\nkind: Pinned\nmetadata:\n  name: p\n  namespace: elsewhere\n",
		"templates/wrapped.yaml": "apiVersion: v1\nkind: List\nitems:\n  - apiVersion: v1\n    kind: ConfigMap\n    metadata:\n      name: inner\n",
	}

	rendered := renderPlaced(t, chart, "shop")
	got := emittedNamespaces(t, rendered.Files)
	for _, resource := range []string{"ServiceMonitor/a", "ServiceMonitor/b", "Thing/t", "Mystery/m", "Orphan/o"} {
		if e, ok := got[resource]; !ok || e.present {
			t.Errorf("%s is emitted %+v (emitted: %v); want it left without a namespace", resource, e, ok)
		}
	}
	if e := got["Pinned/p"]; e.value != "elsewhere" {
		t.Errorf("Pinned/p writes its own namespace and is emitted %+v", e)
	}
	// A List is a kind of its own here: it is reported, and what it wraps is not
	// placed.
	if list, ok := got["List/"]; !ok || strings.Contains(string(rendered.Files[list.file]), "namespace:") {
		t.Errorf("the List is emitted %+v (emitted: %v); want it and the ConfigMap it wraps left without a namespace", list, ok)
	}

	wantNamed := []string{
		`kind "ServiceMonitor" in API group "monitoring.coreos.com"`,
		`kind "Thing" in API group "example.org"`,
		`kind "Mystery" in the core API group`,
		`kind "Orphan" whose documents name no apiVersion`,
		`kind "List" in the core API group`,
	}
	if len(rendered.Advisories) != len(wantNamed) {
		t.Fatalf("raised %d advisories, want %d, one per kind:\n%v", len(rendered.Advisories), len(wantNamed), rendered.Advisories)
	}
	for i, d := range rendered.Advisories {
		if !strings.Contains(d.Message, wantNamed[i]) {
			t.Errorf("advisory %d = %q, want it to name %s", i, d.Message, wantNamed[i])
		}
		if !strings.Contains(d.Message, `namespace "shop"`) {
			t.Errorf("advisory %d does not name the namespace the resources were not placed in: %q", i, d.Message)
		}
		if d.Severity != abi.SeverityWarn || d.Code != "" || strings.Contains(d.Message, "\n") {
			t.Errorf("advisory %d is %+v; want one warn-severity line carrying no class code", i, d)
		}
	}

	if quiet := renderPlaced(t, chart, ""); len(quiet.Advisories) != 0 {
		t.Errorf("a render with no namespace declared raised advisories: %v", quiet.Advisories)
	}
}

// TestRenderChart_TheRenderFactsReportThePlacedNamespace reads the render facts
// off a placed render: the namespace each entry carries is the one the emitted
// document now writes.
func TestRenderChart_TheRenderFactsReportThePlacedNamespace(t *testing.T) {
	t.Parallel()

	rendered := renderPlaced(t, placementChart, "shop")
	paths := sortedFiles(rendered.Files)
	files := make([]rendercore.RenderedFile, 0, len(paths))
	for _, p := range paths {
		files = append(files, rendercore.RenderedFile{Path: p, Content: rendered.Files[p]})
	}
	assertFacts(t, reportedFactsUnder(t, files, "shop"), map[string]string{
		"pod_template_labels":  `[{"kind":"Deployment","name":"web","namespace":"shop","labels":{"app":"web"}}]`,
		"controller_selectors": `[{"kind":"Deployment","name":"web","namespace":"shop","match_labels":{"app":"web"}}]`,
		"service_selectors":    `[{"name":"web","namespace":"shop","selector":{"app":"web"}}]`,
		"container_ports":      `[{"kind":"Deployment","name":"web","namespace":"shop","container":"server","ports":[{"name":"http","container_port":8080}]}]`,
	})
}

func sortedFiles(files map[string][]byte) []string {
	out := make([]string, 0, len(files))
	for p := range files {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

func sortedEmitted(m map[string]emitted) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func dump(files map[string][]byte) string {
	var b strings.Builder
	for _, p := range sortedFiles(files) {
		b.WriteString("--- " + p + "\n" + string(files[p]))
	}
	return b.String()
}
