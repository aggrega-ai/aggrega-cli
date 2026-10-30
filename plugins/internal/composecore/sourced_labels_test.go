// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package composecore

import (
	"strings"
	"testing"
)

// sourcedServiceLabels renders source for node web and returns the labels of
// service svc as the assembled file reads them: the emitted fragment is decoded
// through the same decode the merge applies to every fragment.
func sourcedServiceLabels(t *testing.T, source, svc string) map[string]*string {
	t.Helper()
	out, err := RenderSourcedCore([]byte(source), "web-fragment.yaml", nil, nodeCtxFor("web"))
	if err != nil {
		t.Fatalf("RenderSourcedCore: %v", err)
	}
	const name = "web.compose-fragment.yaml"
	body, ok := out.Files[name]
	if !ok {
		t.Fatalf("no %s in the render", name)
	}
	frag, err := decodeFragment(body, "web", name)
	if err != nil {
		t.Fatalf("emitted fragment does not decode: %v\n%s", err, body)
	}
	s, ok := frag.Services[svc]
	if !ok {
		t.Fatalf("no service %q in the fragment:\n%s", svc, body)
	}
	return s.Labels
}

// TestSourcedComposeKeepsEveryAuthoredLabel proves a sourced service keeps every
// label its author wrote beside the provenance labels: the list form's key=value
// entries join the mapping as Compose reads them — an entry with no `=` as a label
// with an empty value — and a number or a true/false value in the mapping form is
// held as the text it is written as, while a null stays a null. An authored key
// the provenance set also writes takes the provenance value in either form.
func TestSourcedComposeKeepsEveryAuthoredLabel(t *testing.T) {
	t.Parallel()
	const head = "services:\n  web:\n    image: \"nginx:1.27\"\n"
	type label struct {
		value string
		null  bool
	}
	cases := []struct {
		name   string
		labels string
		want   map[string]label
	}{
		{
			"list form",
			"    labels:\n      - \"team=payments\"\n      - \"tier=web\"\n      - \"note=a=b\"\n      - \"flag\"\n",
			map[string]label{"team": {value: "payments"}, "tier": {value: "web"}, "note": {value: "a=b"}, "flag": {value: ""}},
		},
		{
			"mapping form with a number, a true/false value and a null",
			"    labels:\n      team: payments\n      replicas-hint: 2\n      public: true\n      unset: null\n",
			map[string]label{"team": {value: "payments"}, "replicas-hint": {value: "2"}, "public": {value: "true"}, "unset": {null: true}},
		},
		{
			"list form naming a provenance key",
			"    labels:\n      - \"aggrega.ai/component-id=mine\"\n      - \"team=payments\"\n",
			map[string]label{"aggrega.ai/component-id": {value: "web"}, "team": {value: "payments"}},
		},
		{
			"mapping form naming a provenance key",
			"    labels:\n      aggrega.ai/component-id: mine\n      team: payments\n",
			map[string]label{"aggrega.ai/component-id": {value: "web"}, "team": {value: "payments"}},
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := sourcedServiceLabels(t, head+tc.labels, "web")
			for k, w := range tc.want {
				v, ok := got[k]
				switch {
				case !ok:
					t.Errorf("label %q is missing: %v", k, labelText(got))
				case w.null && v != nil:
					t.Errorf("label %q = %q, want a null", k, *v)
				case !w.null && (v == nil || *v != w.value):
					t.Errorf("label %q = %s, want %q", k, labelText(map[string]*string{k: v}), w.value)
				}
			}
			if _, ok := got["aggrega.ai/aggregate-name"]; !ok {
				t.Errorf("the provenance labels are missing: %v", labelText(got))
			}
		})
	}
}

// TestSourcedComposeLabelsInAShapeComposeDoesNotTakeAreRefused proves a labels
// value Compose takes in neither form, and a list entry that is not text, halt the
// render naming the service, rather than being replaced by the provenance labels.
// One fault per row; the positive control is the list-form row of the test above.
func TestSourcedComposeLabelsInAShapeComposeDoesNotTakeAreRefused(t *testing.T) {
	t.Parallel()
	const head = "services:\n  web:\n    image: \"nginx:1.27\"\n"
	cases := []struct {
		name   string
		labels string
		want   string
	}{
		{"labels written as text", "    labels: team\n",
			"services.web.labels: text is written where a list or a map is expected"},
		{"a list entry written as a number", "    labels:\n      - \"team=payments\"\n      - 5\n",
			"services.web.labels: entry 2: a number is written where text is expected"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := RenderSourcedCore([]byte(head+tc.labels), "web-fragment.yaml", nil, nodeCtxFor("web"))
			if err == nil {
				t.Fatal("the labels value was accepted")
			}
			if text := fullText(err); !strings.Contains(text, tc.want) {
				t.Errorf("refusal does not say %q:\n%s", tc.want, text)
			}
		})
	}
}

// labelText renders a labels mapping for a failure message, a null as null.
func labelText(m map[string]*string) string {
	parts := make([]string, 0, len(m))
	for k, v := range m {
		if v == nil {
			parts = append(parts, k+"=null")
			continue
		}
		parts = append(parts, k+"="+*v)
	}
	return strings.Join(parts, ", ")
}
