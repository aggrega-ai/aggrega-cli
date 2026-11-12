// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package main

import (
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/plugintype"
)

// TestHostExportIdentsCoverEveryListedExport reads the real registry and requires
// one identifier per listed export, in registry order, each derived from the
// export's own name. Counting both sides would pass with one missing on each, so
// the comparison is per name.
func TestHostExportIdentsCoverEveryListedExport(t *testing.T) {
	t.Parallel()

	idents, err := collectHostExportIdents(plugintype.Entries())
	if err != nil {
		t.Fatalf("the real registry should yield identifiers; got %v", err)
	}
	var want []string
	for _, e := range plugintype.Entries() {
		for _, name := range e.HostExports {
			want = append(want, string(e.Type)+"/"+name)
		}
	}
	if len(want) == 0 {
		t.Fatal("the registry lists no host export, so this test proves nothing")
	}
	var got []string
	for _, id := range idents {
		got = append(got, string(id.pluginType)+"/"+id.export)
		if id.ident != pascal(id.export)+"Export" {
			t.Errorf("export %q is declared as %s; want %s", id.export, id.ident, pascal(id.export)+"Export")
		}
	}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("identifiers cover %v; want every listed export in registry order, %v", got, want)
	}
}

// TestHostExportIdentsRefuseWhatCannotBeDeclared drives the two refusals over a
// registry built to trip each, beside a control over the real one: an empty name,
// and two names deriving one identifier. Either would emit a file declaring an
// identifier that names nothing, or two declarations that collide.
func TestHostExportIdentsRefuseWhatCannotBeDeclared(t *testing.T) {
	t.Parallel()

	if _, err := collectHostExportIdents(plugintype.Entries()); err != nil {
		t.Fatalf("the control over the real registry should yield identifiers; got %v", err)
	}
	linter, ok := plugintype.Lookup(plugintype.Linter)
	if !ok || len(linter.HostExports) == 0 {
		t.Fatal("the linter type lists no host export to repeat")
	}

	cases := []struct {
		name     string
		registry []plugintype.Entry
		wantSubs []string
	}{
		{
			name:     "an empty name",
			registry: withHostExports(plugintype.Parser, []string{""}),
			wantSubs: []string{string(plugintype.Parser), "empty name"},
		},
		{
			name:     "one name listed under two plugin types",
			registry: withHostExports(plugintype.Parser, []string{linter.HostExports[0]}),
			wantSubs: []string{string(plugintype.Linter), string(plugintype.Parser), linter.HostExports[0]},
		},
		{
			name:     "two spellings one identifier cannot tell apart",
			registry: withHostExports(plugintype.Parser, []string{"one_name", "oneName"}),
			wantSubs: []string{"one_name", "oneName", "OneNameExport"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := collectHostExportIdents(tc.registry)
			if err == nil {
				t.Fatal("identifiers were derived from a list built to be refused, so the refusal cannot fail")
			}
			for _, want := range tc.wantSubs {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("the refusal does not name %q: %v", want, err)
				}
			}
		})
	}
}
