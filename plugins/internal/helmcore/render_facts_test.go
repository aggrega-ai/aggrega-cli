// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package helmcore_test

import (
	"context"
	"encoding/json"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/rendercore"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"github.com/aggrega-ai/aggrega-cli/plugins/internal/helmcore"
)

// renderFactNames are the facts the helm provider documents, in the order it
// reports them.
var renderFactNames = []string{"pod_template_labels", "controller_selectors", "service_selectors", "container_ports"}

// reportedFacts runs the report over files and returns each fact's value as the
// JSON the guest hands the host, keyed by name, after asserting the four facts are
// all there, in the documented order, and each a list.
func reportedFacts(t *testing.T, files []rendercore.RenderedFile) map[string]string {
	t.Helper()
	_, facts, err := helmcore.ReportEmitted(files, "default")
	if err != nil {
		t.Fatalf("ReportEmitted: %v", err)
	}
	names := make([]string, 0, len(facts))
	out := make(map[string]string, len(facts))
	for _, f := range facts {
		names = append(names, f.Name)
		if !strings.HasPrefix(string(f.Value), "[") {
			t.Errorf("fact %q is %s, and every render fact is a list", f.Name, f.Value)
		}
		out[f.Name] = string(f.Value)
	}
	if !reflect.DeepEqual(names, renderFactNames) {
		t.Fatalf("reported facts %v, want %v", names, renderFactNames)
	}
	return out
}

// controllerDoc is a controller of the given kind whose selector and pod template
// carry the labels a network policy reads, and whose container opens one port.
func controllerDoc(apiVersion, kind, name string) string {
	return `apiVersion: ` + apiVersion + `
kind: ` + kind + `
metadata:
  name: ` + name + `
  labels:
    owner: platform
spec:
  selector:
    matchLabels:
      app: ` + name + `
  template:
    metadata:
      labels:
        app: ` + name + `
        version: v2
    spec:
      containers:
        - name: main
          image: example/app:1
          ports:
            - name: http
              containerPort: 8080
              protocol: TCP
`
}

// controllerFacts is what controllerDoc reports.
func controllerFacts(kind, name string) map[string]string {
	return map[string]string{
		"pod_template_labels":  `[{"kind":"` + kind + `","name":"` + name + `","labels":{"app":"` + name + `","version":"v2"}}]`,
		"controller_selectors": `[{"kind":"` + kind + `","name":"` + name + `","match_labels":{"app":"` + name + `"}}]`,
		"service_selectors":    `[]`,
		"container_ports":      `[{"kind":"` + kind + `","name":"` + name + `","container":"main","ports":[{"name":"http","container_port":8080,"protocol":"TCP"}]}]`,
	}
}

// noFacts is every fact reported empty.
var noFacts = map[string]string{
	"pod_template_labels":  `[]`,
	"controller_selectors": `[]`,
	"service_selectors":    `[]`,
	"container_ports":      `[]`,
}

// TestReportEmitted_ReportsThePodFactsOfEachKindItReads holds what each kind of
// emitted document contributes to the four facts.
//
// The rows are each workload kind the provider reads, by its kind and API group —
// a bare Pod, which is its own pod template and selects nothing; the four apps
// controllers and a Job, whose template and selector sit under spec; a CronJob,
// whose pod template is its Job template's, one level further down — and a
// Service. Then the documents it must not read as workloads: a ConfigMap, a custom
// resource sharing a workload's kind name in another group, and a document naming
// no apiVersion. Then what a document leaves out — a Job relying on the generated
// selector, a Service with no selector, a container declaring no ports, a resource
// named by generateName — which its entry leaves out too, beside a part written
// empty, which is reported empty. And an empty render, which reports every fact as
// an empty list.
func TestReportEmitted_ReportsThePodFactsOfEachKindItReads(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name  string
		files []rendercore.RenderedFile
		want  map[string]string
	}{
		{
			name: "a bare Pod is its own pod template and selects nothing",
			files: []rendercore.RenderedFile{file("pod.yaml", `apiVersion: v1
kind: Pod
metadata:
  name: probe
  labels:
    app: probe
spec:
  containers:
    - name: main
      image: example/app:1
      ports:
        - name: http
          containerPort: 8080
`)},
			want: map[string]string{
				"pod_template_labels":  `[{"kind":"Pod","name":"probe","labels":{"app":"probe"}}]`,
				"controller_selectors": `[]`,
				"service_selectors":    `[]`,
				"container_ports":      `[{"kind":"Pod","name":"probe","container":"main","ports":[{"name":"http","container_port":8080}]}]`,
			},
		},
		{
			name:  "a Deployment",
			files: []rendercore.RenderedFile{file("d.yaml", controllerDoc("apps/v1", "Deployment", "api"))},
			want:  controllerFacts("Deployment", "api"),
		},
		{
			name:  "a StatefulSet",
			files: []rendercore.RenderedFile{file("s.yaml", controllerDoc("apps/v1", "StatefulSet", "db"))},
			want:  controllerFacts("StatefulSet", "db"),
		},
		{
			name:  "a DaemonSet",
			files: []rendercore.RenderedFile{file("ds.yaml", controllerDoc("apps/v1", "DaemonSet", "agent"))},
			want:  controllerFacts("DaemonSet", "agent"),
		},
		{
			name:  "a ReplicaSet",
			files: []rendercore.RenderedFile{file("rs.yaml", controllerDoc("apps/v1", "ReplicaSet", "cache"))},
			want:  controllerFacts("ReplicaSet", "cache"),
		},
		{
			name:  "a Job written with its own selector",
			files: []rendercore.RenderedFile{file("j.yaml", controllerDoc("batch/v1", "Job", "migrate"))},
			want:  controllerFacts("Job", "migrate"),
		},
		{
			name: "a CronJob's pod template is its Job template's",
			files: []rendercore.RenderedFile{file("cj.yaml", `apiVersion: batch/v1
kind: CronJob
metadata:
  name: nightly
spec:
  schedule: "0 3 * * *"
  jobTemplate:
    spec:
      selector:
        matchLabels:
          job: nightly
      template:
        metadata:
          labels:
            job: nightly
            app: reports
        spec:
          restartPolicy: OnFailure
          containers:
            - name: run
              image: example/reports:1
              ports:
                - containerPort: 9090
`)},
			want: map[string]string{
				"pod_template_labels":  `[{"kind":"CronJob","name":"nightly","labels":{"job":"nightly","app":"reports"}}]`,
				"controller_selectors": `[{"kind":"CronJob","name":"nightly","match_labels":{"job":"nightly"}}]`,
				"service_selectors":    `[]`,
				"container_ports":      `[{"kind":"CronJob","name":"nightly","container":"run","ports":[{"container_port":9090}]}]`,
			},
		},
		{
			name: "a Service",
			files: []rendercore.RenderedFile{file("svc.yaml", `apiVersion: v1
kind: Service
metadata:
  name: api
spec:
  selector:
    tier: web
    app: api
  ports:
    - port: 80
      targetPort: http
`)},
			want: map[string]string{
				"pod_template_labels":  `[]`,
				"controller_selectors": `[]`,
				"service_selectors":    `[{"name":"api","selector":{"tier":"web","app":"api"}}]`,
				"container_ports":      `[]`,
			},
		},
		{
			name: "documents that are not workloads or Services: a ConfigMap, a custom resource sharing a kind name, no apiVersion",
			files: []rendercore.RenderedFile{
				file("cm.yaml", "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: settings\ndata:\n  mode: fast\n"),
				file("cr.yaml", strings.Replace(controllerDoc("apps/v1", "Deployment", "custom"), "apps/v1", "example.com/v1", 1)),
				file("bare.yaml", strings.Replace(controllerDoc("apps/v1", "Deployment", "bare"), "apiVersion: apps/v1\n", "", 1)),
				file("svc.yaml", "apiVersion: serving.example.com/v1\nkind: Service\nmetadata:\n  name: other\nspec:\n  selector:\n    app: x\n"),
			},
			want: noFacts,
		},
		{
			name: "a part the document leaves out is left out of its entry, and a part written empty is reported empty",
			files: []rendercore.RenderedFile{
				file("job.yaml", `apiVersion: batch/v1
kind: Job
metadata:
  generateName: seed-
spec:
  template:
    metadata: {}
    spec:
      restartPolicy: Never
      containers:
        - name: seed
          image: example/seed:1
`),
				file("svc.yaml", `apiVersion: v1
kind: Service
metadata:
  name: external
spec:
  type: ExternalName
  externalName: db.example.com
---
apiVersion: v1
kind: Service
metadata:
  name: manual
spec:
  selector: {}
`),
				file("pod.yaml", `apiVersion: v1
kind: Pod
metadata:
  name: quiet
  labels: {}
spec:
  containers:
    - name: main
      image: example/app:1
      ports: []
    - name: side
      image: example/side:1
      ports:
        - containerPort: "8443"
`),
			},
			want: map[string]string{
				"pod_template_labels":  `[{"kind":"Job"},{"kind":"Pod","name":"quiet","labels":{}}]`,
				"controller_selectors": `[{"kind":"Job"}]`,
				"service_selectors":    `[{"name":"external"},{"name":"manual","selector":{}}]`,
				"container_ports":      `[{"kind":"Pod","name":"quiet","container":"main","ports":[]},{"kind":"Pod","name":"quiet","container":"side","ports":[{"container_port":"8443"}]}]`,
			},
		},
		{
			name:  "an empty render reports every fact, empty",
			files: nil,
			want:  noFacts,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := reportedFacts(t, tc.files)
			for _, name := range renderFactNames {
				if got[name] != tc.want[name] {
					t.Errorf("%s =\n  %s\nwant\n  %s", name, got[name], tc.want[name])
				}
			}
		})
	}
}

// TestReportEmitted_EntriesFollowTheEmittedOrder holds the order: entries in the
// order the render emitted the documents — file by file, and document by document
// inside a file — and every label map in the order its document writes the keys.
// The names are chosen against alphabetical order, so a reader that sorted
// anything would read differently.
func TestReportEmitted_EntriesFollowTheEmittedOrder(t *testing.T) {
	t.Parallel()

	got := reportedFacts(t, []rendercore.RenderedFile{
		file("a.yaml", controllerDoc("apps/v1", "Deployment", "zeta")+"---\n"+`apiVersion: v1
kind: Service
metadata:
  name: zeta
spec:
  selector:
    zone: east
    app: zeta
`),
		file("b.yaml", controllerDoc("apps/v1", "StatefulSet", "alpha")+"---\n"+`apiVersion: v1
kind: Pod
metadata:
  name: mid
  labels:
    z: "1"
    a: "2"
spec:
  initContainers:
    - name: proxy
      image: example/proxy:1
      ports:
        - containerPort: 15001
  containers:
    - name: app
      image: example/app:1
      ports:
        - name: web
          containerPort: 80
        - name: admin
          containerPort: 9000
          protocol: UDP
`),
	})

	want := map[string]string{
		"pod_template_labels": `[{"kind":"Deployment","name":"zeta","labels":{"app":"zeta","version":"v2"}},` +
			`{"kind":"StatefulSet","name":"alpha","labels":{"app":"alpha","version":"v2"}},` +
			`{"kind":"Pod","name":"mid","labels":{"z":"1","a":"2"}}]`,
		"controller_selectors": `[{"kind":"Deployment","name":"zeta","match_labels":{"app":"zeta"}},` +
			`{"kind":"StatefulSet","name":"alpha","match_labels":{"app":"alpha"}}]`,
		"service_selectors": `[{"name":"zeta","selector":{"zone":"east","app":"zeta"}}]`,
		"container_ports": `[{"kind":"Deployment","name":"zeta","container":"main","ports":[{"name":"http","container_port":8080,"protocol":"TCP"}]},` +
			`{"kind":"StatefulSet","name":"alpha","container":"main","ports":[{"name":"http","container_port":8080,"protocol":"TCP"}]},` +
			`{"kind":"Pod","name":"mid","container":"proxy","ports":[{"container_port":15001}]},` +
			`{"kind":"Pod","name":"mid","container":"app","ports":[{"name":"web","container_port":80},{"name":"admin","container_port":9000,"protocol":"UDP"}]}]`,
	}
	for _, name := range renderFactNames {
		if got[name] != want[name] {
			t.Errorf("%s =\n  %s\nwant\n  %s", name, got[name], want[name])
		}
	}

	again := reportedFacts(t, []rendercore.RenderedFile{
		file("a.yaml", controllerDoc("apps/v1", "Deployment", "zeta")),
	})
	if again["pod_template_labels"] != `[{"kind":"Deployment","name":"zeta","labels":{"app":"zeta","version":"v2"}}]` {
		t.Errorf("the same document read twice reports differently: %s", again["pod_template_labels"])
	}
}

// TestReportEmitted_TheFactsAndTheDeclaredNamesReadOneWalk is the parity between
// the two things the guest reports about one render: the resource names it
// declares and the facts it reports come from the same walk over the same parsed
// documents, never from two readers each parsing the files.
//
// For every kind both cover, the names the facts carry, in order, are exactly the
// names declared in that kind's space. The rows are the inputs on which two
// readers part: several documents in one file, a document that does not parse
// (the walk stops reading that file there, so the document after it reaches
// neither), and a document that is not a mapping. A reader parsing the files a
// second way — one document per file, or one that skips a document it cannot read
// and carries on — reads a different set and turns a row red.
func TestReportEmitted_TheFactsAndTheDeclaredNamesReadOneWalk(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name  string
		files []rendercore.RenderedFile
		// want is, per kind, the names both reports must carry in order.
		want map[string][]string
	}{
		{
			name: "several documents in one file",
			files: []rendercore.RenderedFile{
				file("all.yaml", controllerDoc("apps/v1", "Deployment", "one")+"---\n"+
					controllerDoc("apps/v1", "Deployment", "two")+"---\n"+
					"apiVersion: v1\nkind: Service\nmetadata:\n  name: front\nspec:\n  selector:\n    app: one\n"),
			},
			want: map[string][]string{"Deployment": {"one", "two"}, "Service": {"front"}},
		},
		{
			name: "a document that does not parse ends the reading of its file, and the next file is read",
			files: []rendercore.RenderedFile{
				file("broken.yaml", controllerDoc("apps/v1", "Deployment", "before")+"---\n"+
					"kind: [unclosed\n---\n"+
					controllerDoc("apps/v1", "Deployment", "after")),
				file("next.yaml", controllerDoc("apps/v1", "Deployment", "next")),
			},
			want: map[string][]string{"Deployment": {"before", "next"}},
		},
		{
			name: "a document that is not a mapping is skipped by both",
			files: []rendercore.RenderedFile{
				file("mixed.yaml", "- a\n- b\n---\n"+controllerDoc("apps/v1", "DaemonSet", "agent")+"---\njust text\n"),
			},
			want: map[string][]string{"DaemonSet": {"agent"}},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			declared, facts, err := helmcore.ReportEmitted(tc.files, "default")
			if err != nil {
				t.Fatalf("ReportEmitted: %v", err)
			}
			factNames := namesInFacts(t, facts)
			for kind, want := range tc.want {
				names, _ := declared.Get("resource-names:" + kind + "--default")
				if !reflect.DeepEqual(names, want) {
					t.Errorf("declared %s names %v, want %v", kind, names, want)
				}
				if !reflect.DeepEqual(factNames[kind], want) {
					t.Errorf("the facts name %s %v, want %v — the same documents the names were declared from",
						kind, factNames[kind], want)
				}
			}
			for kind := range factNames {
				if _, expected := tc.want[kind]; !expected {
					t.Errorf("the facts name a %s %v that no row expects", kind, factNames[kind])
				}
			}
		})
	}
}

// namesInFacts reads, per kind, the resource names the facts carry: a workload's
// from its pod template entry, a Service's from its selector entry.
func namesInFacts(t *testing.T, facts []rendercore.RenderFact) map[string][]string {
	t.Helper()
	out := map[string][]string{}
	for _, f := range facts {
		switch f.Name {
		case "pod_template_labels":
			var entries []struct{ Kind, Name string }
			if err := json.Unmarshal(f.Value, &entries); err != nil {
				t.Fatalf("decode %s: %v", f.Name, err)
			}
			for _, e := range entries {
				out[e.Kind] = append(out[e.Kind], e.Name)
			}
		case "service_selectors":
			var entries []struct{ Name string }
			if err := json.Unmarshal(f.Value, &entries); err != nil {
				t.Fatalf("decode %s: %v", f.Name, err)
			}
			for _, e := range entries {
				out["Service"] = append(out["Service"], e.Name)
			}
		}
	}
	return out
}

// TestReportEmitted_OverEachRenderPath reads the facts off what each of the three
// helm render paths really emits: a chart's rendered manifests, a synthesized
// Workload's, and an author's manifests placed as artifacts. Each path's own core
// produces the files, handed over in the order the guest hands them on, so the
// facts are read off the documents a build writes rather than off a hand-written
// stand-in.
//
// The chart and the artifacts are the author's own documents, stamped with
// provenance in their top-level metadata only, so their pod template labels,
// selectors and ports read exactly as written. The synthesized Workload's are what
// the synthesis emits: its selector, its Service and its port.
func TestReportEmitted_OverEachRenderPath(t *testing.T) {
	t.Parallel()

	const authored = `apiVersion: apps/v1
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
        tier: front
        app: web
    spec:
      containers:
        - name: server
          image: example/web:1
          ports:
            - name: http
              containerPort: 8080
---
apiVersion: v1
kind: Service
metadata:
  name: web
spec:
  selector:
    app: web
  ports:
    - port: 80
      targetPort: http
`
	authoredFacts := map[string]string{
		"pod_template_labels":  `[{"kind":"Deployment","name":"web","labels":{"tier":"front","app":"web"}}]`,
		"controller_selectors": `[{"kind":"Deployment","name":"web","match_labels":{"app":"web"}}]`,
		"service_selectors":    `[{"name":"web","selector":{"app":"web"}}]`,
		"container_ports":      `[{"kind":"Deployment","name":"web","container":"server","ports":[{"name":"http","container_port":8080}]}]`,
	}

	t.Run("a chart", func(t *testing.T) {
		t.Parallel()
		ch := chartFromFiles(t, map[string]string{
			"Chart.yaml":         "apiVersion: v2\nname: web\nversion: 0.1.0\n",
			"templates/web.yaml": authored,
		})
		rendered, err := helmcore.RenderLoadedChart(context.Background(), ch, map[string]any{},
			nodeCtx("api", "in-memory"), helmcore.NewHermeticValuesValidator(forbidFetch(t)))
		if err != nil {
			t.Fatalf("render the chart: %v", err)
		}
		// The guest hands a chart's files on in lexical path order.
		paths := make([]string, 0, len(rendered))
		for p := range rendered {
			paths = append(paths, p)
		}
		sort.Strings(paths)
		files := make([]rendercore.RenderedFile, 0, len(paths))
		for _, p := range paths {
			files = append(files, rendercore.RenderedFile{Path: p, Content: rendered[p]})
		}
		assertFacts(t, reportedFacts(t, files), authoredFacts)
	})

	t.Run("a synthesized Workload", func(t *testing.T) {
		t.Parallel()
		w := &types.Workload{APIVersion: "aggrega.ai/v1alpha1", Kind: "Workload"}
		c := types.ContainerSpec{Image: "example/app:1"}
		c.Ports.Set("web", types.PortSpec{Port: 8080})
		w.Containers.Set("main", c)
		svc := types.ServiceSpec{Type: "ClusterIP"}
		svc.Ports.Set("web", 8080)
		w.Services.Set("web", svc)

		emitted, err := helmcore.SynthesizeWorkloadCore(w, nil, nodeCtx("api", nil))
		if err != nil {
			t.Fatalf("synthesize: %v", err)
		}
		files := make([]rendercore.RenderedFile, 0, len(emitted))
		for _, f := range emitted {
			files = append(files, rendercore.RenderedFile{Path: f.Name, Content: f.Body})
		}
		got := reportedFacts(t, files)
		assertFacts(t, got, map[string]string{
			"controller_selectors": `[{"kind":"Deployment","name":"api","match_labels":{"app":"api"}}]`,
			"service_selectors":    `[{"name":"api-web","selector":{"app":"api"}}]`,
			"container_ports":      `[{"kind":"Deployment","name":"api","container":"main","ports":[{"name":"web","container_port":8080}]}]`,
		})
		var templates []struct {
			Kind   string
			Name   string
			Labels types.OrderedMap[string]
		}
		if err := json.Unmarshal([]byte(got["pod_template_labels"]), &templates); err != nil {
			t.Fatalf("decode pod_template_labels: %v", err)
		}
		if len(templates) != 1 || templates[0].Kind != "Deployment" || templates[0].Name != "api" {
			t.Fatalf("pod_template_labels = %s, want the one Deployment api", got["pod_template_labels"])
		}
		if app, _ := templates[0].Labels.Get("app"); app != "api" {
			t.Errorf("the synthesized pod template's labels %s carry app=%q; the selector it is matched by is app=api",
				got["pod_template_labels"], app)
		}
	})

	t.Run("an author's manifests placed as artifacts", func(t *testing.T) {
		t.Parallel()
		body, warning, err := rendercore.SpliceArtifactProvenance("web.yaml", []byte(authored),
			map[string]string{"aggrega.ai/component-id": "api"}, map[string]string{"aggrega.ai/boundary-path": "app"},
			true, 2, false)
		if err != nil || warning != "" {
			t.Fatalf("place the artifact: warning %q, error %v", warning, err)
		}
		if !strings.Contains(string(body), "aggrega.ai/component-id") {
			t.Fatalf("the placed artifact carries no provenance, so this row does not read a placed document:\n%s", body)
		}
		assertFacts(t, reportedFacts(t, []rendercore.RenderedFile{{Path: "web.yaml", Content: body}}), authoredFacts)
	})
}

// assertFacts compares the facts a row names; a fact the row does not name is
// not compared.
func assertFacts(t *testing.T, got, want map[string]string) {
	t.Helper()
	for _, name := range renderFactNames {
		w, named := want[name]
		if named && got[name] != w {
			t.Errorf("%s =\n  %s\nwant\n  %s", name, got[name], w)
		}
	}
}

// TestReportEmitted_DeclaresTheSameNamesAsTheNamesReaderAlone holds that reading
// the facts beside the names changes nothing the names reader declares: the
// guest's report and the names reader on its own give one declaration.
func TestReportEmitted_DeclaresTheSameNamesAsTheNamesReaderAlone(t *testing.T) {
	t.Parallel()

	files := []rendercore.RenderedFile{
		file("a.yaml", controllerDoc("apps/v1", "Deployment", "api")+"---\n"+svcAlpha),
		file("b.yaml", "kind: Widget\nmetadata:\n  name: w\n  namespace: other\n"),
	}
	declared, _, err := helmcore.ReportEmitted(files, "fallback")
	if err != nil {
		t.Fatalf("ReportEmitted: %v", err)
	}
	alone := helmcore.DeclaredResourceNames(files, "fallback")
	gotJSON, _ := json.Marshal(declared)
	wantJSON, _ := json.Marshal(alone)
	if string(gotJSON) != string(wantJSON) {
		t.Errorf("the report declares %s, the names reader alone %s", gotJSON, wantJSON)
	}
	if declared.Len() != 3 {
		t.Errorf("declared %d spaces (%s), want 3: a Deployment and a Service in the fallback namespace, a Widget in its own", declared.Len(), gotJSON)
	}
}
