// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"reflect"
	"testing"

	"github.com/cognitio/aggrega-cli/pkg/types"
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
