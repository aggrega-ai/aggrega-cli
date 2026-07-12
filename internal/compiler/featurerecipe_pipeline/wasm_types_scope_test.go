// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package featurerecipe_pipeline

import (
	"reflect"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// TestDocumentScopeSingleSourced pins the FRP wire scope vocabulary to the
// Plugin entity's scope vocabulary. The DocumentScope consts are equal to
// types.Scope* by construction (untyped-const conversion), so the first two
// checks are guards against an accidental de-linking of the consts. The json
// struct tags on DocumentSet are string literals the compiler cannot const-fold
// against types.Scope*, so this parity assertion is the mechanism that keeps
// them from drifting — a hand-edited tag that diverges from the scope
// vocabulary fails here.
func TestDocumentScopeSingleSourced(t *testing.T) {
	if string(DocumentScopeOwn) != types.ScopeOwn {
		t.Errorf("DocumentScopeOwn = %q, want types.ScopeOwn %q", DocumentScopeOwn, types.ScopeOwn)
	}
	if string(DocumentScopeHost) != types.ScopeHost {
		t.Errorf("DocumentScopeHost = %q, want types.ScopeHost %q", DocumentScopeHost, types.ScopeHost)
	}

	dt := reflect.TypeOf(DocumentSet{})
	cases := []struct {
		field string
		want  string
	}{
		{"Own", types.ScopeOwn},
		{"Host", types.ScopeHost},
	}
	for _, c := range cases {
		f, ok := dt.FieldByName(c.field)
		if !ok {
			t.Fatalf("DocumentSet has no field %s", c.field)
		}
		name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
		if name != c.want {
			t.Errorf("DocumentSet.%s json tag name = %q, want %q", c.field, name, c.want)
		}
	}
}
