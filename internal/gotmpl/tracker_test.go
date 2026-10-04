// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package gotmpl

import (
	"reflect"
	"testing"
	"text/template/parse"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

func TestCollectTemplateFieldAccesses_Basic(t *testing.T) {
	t.Parallel()
	tmpl := `{{ .values.port }}{{ .infra.replicas }}`
	paths, err := CollectTemplateFieldAccesses(tmpl)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []string{".infra.replicas", ".values.port"}
	if !reflect.DeepEqual(paths, want) {
		t.Errorf("paths: got %v, want %v", paths, want)
	}
}

func TestCollectTemplateFieldAccesses_NestedControlFlow(t *testing.T) {
	t.Parallel()
	tmpl := `
{{- if .values.enable -}}
  {{ .values.host }}:{{ .infra.port }}
{{- else -}}
  {{ .values.fallback }}
{{- end -}}
`
	paths, err := CollectTemplateFieldAccesses(tmpl)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Order-independent membership check
	want := map[string]bool{
		".values.enable":   true,
		".values.host":     true,
		".infra.port":      true,
		".values.fallback": true,
	}
	for _, p := range paths {
		if !want[p] {
			t.Errorf("unexpected path: %q", p)
		}
		delete(want, p)
	}
	for p := range want {
		t.Errorf("missing path: %q", p)
	}
}

func TestCollectTemplateFieldAccesses_Range(t *testing.T) {
	t.Parallel()
	// When `range .values` is used, the whole-object iteration marks every key as read.
	tmpl := `{{ range .values }}x{{ end }}`
	paths, err := CollectTemplateFieldAccesses(tmpl)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	found := false
	for _, p := range paths {
		if p == ".values" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected .values (whole-object), got %v", paths)
	}
}

func TestCollectTemplateFieldAccesses_RootVariableRecordsTheRootSpelling(t *testing.T) {
	t.Parallel()
	// $ names the context root under any rebinding, so $.values.port is a read of
	// the same key .values.port is and must be recorded under the same spelling —
	// otherwise a census consumer comparing against ".values.port" misses it.
	// $own names a value the template declared and reaches no authored key.
	tmpl := `{{ range .infra.list }}{{ $.values.port }}{{ end }}{{ $own := 1 }}{{ $own }}`
	paths, err := CollectTemplateFieldAccesses(tmpl)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []string{".infra.list", ".values.port"}
	if !reflect.DeepEqual(paths, want) {
		t.Errorf("paths: got %v, want %v", paths, want)
	}
}

func TestCollectTemplateFieldAccesses_ChainRecordsBothHalves(t *testing.T) {
	t.Parallel()
	// A defensive wrapper reads the receiver's path as well as the trailing field.
	// Recording only the trailing key would report the receiver's key unread and
	// would hide it from any check that must withhold it.
	paths, err := CollectTemplateFieldAccesses(`{{ (default dict .values.tls).ca }}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []string{".ca", ".values.tls"}
	if !reflect.DeepEqual(paths, want) {
		t.Errorf("paths: got %v, want %v", paths, want)
	}
}

func TestUnreadKeys_RootVariableCountsAsRead(t *testing.T) {
	t.Parallel()
	// The census and the read-set share one walk, so a key read only through the
	// $-anchored form is reported read rather than unused.
	refs, err := CollectTemplateFieldAccesses(`{{ range .infra.list }}{{ $.values.image }}{{ end }}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	data := mkData("image", "myapp:v1", "port", 8080)
	unread := UnreadKeys(refs, ".values", data)
	if !reflect.DeepEqual(unread, []string{"port"}) {
		t.Errorf("only the genuinely unread key should be reported; got %v", unread)
	}
}

func TestCollectTemplateFieldAccesses_EmptyTemplate(t *testing.T) {
	t.Parallel()
	paths, err := CollectTemplateFieldAccesses("")
	if err != nil || paths != nil {
		t.Errorf("empty template should return (nil, nil), got (%v, %v)", paths, err)
	}
}

func TestCollectTemplateFieldAccesses_ParseError(t *testing.T) {
	t.Parallel()
	_, err := CollectTemplateFieldAccesses(`{{ unclosed`)
	if err == nil {
		t.Error("expected parse error")
	}
}

func mkData(pairs ...any) types.OrderedMap[any] {
	m := types.OrderedMap[any]{}
	for i := 0; i+1 < len(pairs); i += 2 {
		m.Set(pairs[i].(string), pairs[i+1])
	}
	return m
}

func TestUnreadKeys_BasicGhost(t *testing.T) {
	t.Parallel()
	// Acceptance scenario: valuesMapping reads only .image; Values: {image, port} → port unused.
	refs := []string{".values.image"}
	data := mkData("image", "myapp:v1", "port", 8080)
	unread := UnreadKeys(refs, ".values", data)
	want := []string{"port"}
	if !reflect.DeepEqual(unread, want) {
		t.Errorf("got %v, want %v", unread, want)
	}
}

func TestUnreadKeys_AllRead(t *testing.T) {
	t.Parallel()
	refs := []string{".values.image", ".values.port"}
	data := mkData("image", "x", "port", 80)
	unread := UnreadKeys(refs, ".values", data)
	if len(unread) != 0 {
		t.Errorf("expected no unread, got %v", unread)
	}
}

func TestUnreadKeys_WholeObjectReadSkipsAll(t *testing.T) {
	t.Parallel()
	refs := []string{".values"}
	data := mkData("image", "x", "port", 80, "unused", 1)
	unread := UnreadKeys(refs, ".values", data)
	if unread != nil {
		t.Errorf("whole-object read should skip all, got %v", unread)
	}
}

func TestUnreadKeys_NestedAccessMarksTopLevelKey(t *testing.T) {
	t.Parallel()
	refs := []string{".values.db.host", ".values.db.port"}
	data := mkData("db", map[string]any{}, "other", "x")
	unread := UnreadKeys(refs, ".values", data)
	want := []string{"other"}
	if !reflect.DeepEqual(unread, want) {
		t.Errorf("got %v, want %v", unread, want)
	}
}

func TestUnreadKeys_RootWithoutLeadingDot(t *testing.T) {
	t.Parallel()
	refs := []string{".values.image"}
	data := mkData("image", "x", "port", 80)
	unread := UnreadKeys(refs, "values", data)
	if !reflect.DeepEqual(unread, []string{"port"}) {
		t.Errorf("got %v", unread)
	}
}

func TestUnreadKeys_EmptyData(t *testing.T) {
	t.Parallel()
	unread := UnreadKeys([]string{".values.x"}, ".values", types.OrderedMap[any]{})
	if unread != nil {
		t.Errorf("expected nil for empty data, got %v", unread)
	}
}

// TestCollectFromNode_AllNodeKinds pins the parse-AST walker by
// constructing a template that exercises every dispatch arm.
func TestCollectFromNode_AllNodeKinds(t *testing.T) {
	t.Parallel()
	src := `{{ .alpha }} {{ if .beta }}{{ .gamma }}{{ else }}{{ .delta }}{{ end }} {{ range .epsilon }}{{ . }}{{ end }} {{ with .zeta }}{{ .eta }}{{ end }} {{ template "tpl" . }}`
	// We need a *parse.ListNode root to feed collectFromNode.
	trees, err := parse.Parse("t", src, "{{", "}}")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	c := &census{paths: map[string]bool{}}
	root := trees["t"].Root
	collectFromNode(root, c, true)
	for _, k := range []string{".alpha", ".beta", ".gamma", ".delta", ".epsilon", ".zeta", ".eta"} {
		if !c.paths[k] {
			t.Errorf("expected %q in seen; got %v", k, c.paths)
		}
	}
	// Nil ListNode.
	collectFromNode((*parse.ListNode)(nil), c, true)
	// Nil node.
	collectFromNode(nil, c, true)
}

// TestCollectFromPipe_NilGuard pins the pipe-walker's nil-guard.
func TestCollectFromPipe_NilGuard(t *testing.T) {
	t.Parallel()
	c := &census{paths: map[string]bool{}}
	collectFromPipe(nil, c, true)
	if len(c.paths) != 0 || c.wholeData {
		t.Errorf("nil pipe should record nothing; got %v (whole data %v)", c.paths, c.wholeData)
	}
}

// TestTreeDataReads_TheWholeDataHandedOn is the table for the one read that names no
// field. A row whose want is true must report the data root; a row whose want is
// false must not, and every row's field paths must be exactly what the field-access
// census reports, because the two readings come from one walk and differ in that
// entry alone.
func TestTreeDataReads_TheWholeDataHandedOn(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		text string
		want bool
	}{
		{"a bare dot handed to a function", `{{ fail (toJson .) }}`, true},
		{"a bare dot printed", `{{ . }}`, true},
		{"a bare $ inside a range body", `{{ range .values.list }}{{ fail (printf "%v" $) }}{{ end }}`, true},
		{"a bare $ inside a with body", `{{ with .values.tls }}{{ toJson $ }}{{ end }}`, true},
		{"the whole data bound to a variable", `{{ $all := . }}{{ fail (toJson $all) }}`, true},
		{"the whole data handed to a template invoked by name", `{{ define "x" }}{{ .values.port }}{{ end }}{{ template "x" . }}`, true},
		{"a bare dot in an else branch", `{{ if .values.on }}on{{ else }}{{ toJson . }}{{ end }}`, true},
		{"a bare dot as the pipeline a range iterates", `{{ range $k, $v := . }}{{ $k }}{{ end }}`, true},
		{"a bare dot as the pipeline a with names", `{{ with . }}{{ toJson . }}{{ end }}`, true},
		{"the dot only as a field root", `{{ fail (printf "port %v" .values.port) }}`, false},
		{"a range element printed", `{{ range .values.list }}{{ . }}{{ end }}`, false},
		{"a with body's dot handed on", `{{ with .values.tls }}{{ toJson . }}{{ end }}`, false},
		{"a variable the template declared from a field", `{{ $p := .values.port }}{{ fail (printf "%v" $p) }}`, false},
		{"a $-anchored field read", `{{ $.values.port }}`, false},
		{"no action at all", `plain text`, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			root, err := ParseTree(c.text, "{{", "}}")
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			reads := TreeDataReads(root)
			fields := TreeFieldAccesses(root)
			got := len(reads) > 0 && reads[0] == dataRoot
			if got != c.want {
				t.Errorf("whole data reported = %v, want %v (reads %v)", got, c.want, reads)
			}
			rest := reads
			if got {
				rest = reads[1:]
			}
			if !reflect.DeepEqual(rest, fields) {
				t.Errorf("the field paths must be the field-access census's own:\nreads:  %v\nfields: %v", rest, fields)
			}
			for _, f := range fields {
				if f == dataRoot {
					t.Errorf("the field-access census must never report the data root: %v", fields)
				}
			}
		})
	}
}
