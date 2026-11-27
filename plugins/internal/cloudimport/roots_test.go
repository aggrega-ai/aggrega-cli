// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package cloudimport

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-pdk/go/pkg/aggrega"
)

// paths returns the paths of units, in order.
func paths(units []Unit) []string {
	out := make([]string, 0, len(units))
	for _, u := range units {
		out = append(out, u.Path)
	}
	return out
}

// sourceRefused fails t unless d halts source-refused naming each of names.
func sourceRefused(t *testing.T, d *aggrega.Diagnostic, names ...string) {
	t.Helper()
	if d == nil || d.Code != string(CodeSourceRefused) || d.Severity != aggrega.SeverityHalt {
		t.Fatalf("diagnostic = %+v, want a source-refused halt", d)
	}
	for _, name := range names {
		if !strings.Contains(d.Message, name) {
			t.Fatalf("message %q does not name %s", d.Message, name)
		}
	}
}

func TestSelectRoot(t *testing.T) {
	// a links nested/c, so the candidates are a and b.
	two := []Unit{
		{Path: "b.json"},
		{Path: "a.json", Links: []string{"nested/c.json"}},
		{Path: "nested/c.json"},
	}

	t.Run("one candidate", func(t *testing.T) {
		units := []Unit{{Path: "main.json", Links: []string{"child.json"}}, {Path: "child.json"}}
		root, others, d := SelectRoot(units, "")
		if d != nil || root.Path != "main.json" || len(others) != 0 {
			t.Fatalf("root = %+v, others = %+v, diagnostic = %+v", root, others, d)
		}
	})
	t.Run("two candidates and no root", func(t *testing.T) {
		_, _, d := SelectRoot(two, "")
		sourceRefused(t, d, "a.json", "b.json", "root")
		if strings.Contains(d.Message, "nested/c.json") {
			t.Fatalf("message %q names a linked template as a candidate", d.Message)
		}
	})
	t.Run("root naming one", func(t *testing.T) {
		for _, setting := range []string{"b.json", "./b.json"} {
			root, others, d := SelectRoot(two, setting)
			if d != nil || root.Path != "b.json" || !reflect.DeepEqual(paths(others), []string{"a.json"}) {
				t.Fatalf("root %q: root = %+v, others = %+v, diagnostic = %+v", setting, root, others, d)
			}
		}
	})
	t.Run("root naming no unit", func(t *testing.T) {
		_, _, d := SelectRoot(two, "missing.json")
		sourceRefused(t, d, "a.json", "b.json", "root")
	})
	t.Run("root not a string", func(t *testing.T) {
		_, d := ReadSettings(json.RawMessage(`{"root": 3}`))
		sourceRefused(t, d, "root")
	})
	t.Run("a directory unit's links are relative to the directory itself", func(t *testing.T) {
		units := []Unit{
			{Path: "envs/prod/", Links: []string{"../../modules/db"}},
			{Path: "envs/dev/", Links: []string{"../../modules/db", "./"}},
			{Path: "modules/db/"},
		}
		_, _, d := SelectRoot(units, "")
		sourceRefused(t, d, "envs/prod/", "envs/dev/", "root")
		if strings.Contains(d.Message, "modules/db") {
			t.Fatalf("message %q names a linked directory as a candidate", d.Message)
		}
		root, others, d := SelectRoot(units, "envs/prod")
		if d != nil || root.Path != "envs/prod/" || !reflect.DeepEqual(paths(others), []string{"envs/dev/"}) {
			t.Fatalf("root = %+v, others = %+v, diagnostic = %+v", root, others, d)
		}
		imported := []Unit{{Path: "./", Links: []string{"./modules/db"}}, {Path: "modules/db/"}}
		root, others, d = SelectRoot(imported, "")
		if d != nil || root.Path != "./" || len(others) != 0 {
			t.Fatalf("root = %+v, others = %+v, diagnostic = %+v", root, others, d)
		}
	})
	t.Run("a unit linked by another is never a candidate", func(t *testing.T) {
		// The link is relative to the linking template's own directory.
		units := []Unit{{Path: "env/prod.json", Links: []string{"../shared/net.json"}}, {Path: "shared/net.json"}}
		root, others, d := SelectRoot(units, "")
		if d != nil || root.Path != "env/prod.json" || len(others) != 0 {
			t.Fatalf("root = %+v, others = %+v, diagnostic = %+v", root, others, d)
		}
	})
}
