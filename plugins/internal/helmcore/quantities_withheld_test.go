// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package helmcore

import (
	"errors"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
	"github.com/aggrega-ai/aggrega-cli/internal/redact"
	"github.com/aggrega-ai/aggrega-cli/internal/wasm"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"github.com/aggrega-ai/aggrega-cli/plugins/internal/quantity"
)

// TestSynthesizeWorkload_AMarkedWorkloadsQuantityRefusalQuotesNoValue drives one
// refused quantity per row through the synthesis twice: with the input's marking
// over the workload off, where the refusal quotes the value as it always has, and
// on, where it quotes no part of it. Both keep the node, the field path, the grammar
// and the value-not-representable condition, so only the value moves.
func TestSynthesizeWorkload_AMarkedWorkloadsQuantityRefusalQuotesNoValue(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		value  string
		quoted []string
		// want is what the marked refusal says in the value's place.
		want string
	}{
		{
			name:   "a value refused for the placeholder's own reason is quoted as the placeholder",
			value:  "s3cret-cpu",
			quoted: []string{"s3cret-cpu"},
			want:   `"` + redact.Placeholder() + `" is not a quantity: it does not begin with a number`,
		},
		{
			name:   "a value refused for a reason quoting part of it is withheld with its reason",
			value:  "512Zsecret",
			quoted: []string{"512Zsecret", "Zsecret"},
			want:   "not a quantity; " + quantity.ValueWithheld,
		},
	}
	const path = "containers.main.resources.limits.cpu: "
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			refuse := func(marked bool) string {
				t.Helper()
				w := quantityWorkload()
				c := mainContainer(w)
				c.Resources.Limits.CPU = tc.value
				w.Containers.Set("main", c)
				nodeCtx := nodeCtxFor("store")
				nodeCtx.SensitiveWorkload = marked
				files, err := synthesizeNoPanic(t, w, nodeCtx)
				if err == nil {
					t.Fatalf("the refused quantity %q built (marked %v)", tc.value, marked)
				}
				if !errors.Is(err, wasm.ErrPluginValueNotRepresentable()) {
					t.Errorf("expected the value-not-representable condition, got %v", err)
				}
				if len(files) != 0 {
					t.Errorf("a refused workload emitted %d file(s)", len(files))
				}
				msg := err.Error()
				for _, kept := range []string{`node "store"`, path, quantity.Shape} {
					if !strings.Contains(msg, kept) {
						t.Errorf("the refusal (marked %v) does not say %q: %s", marked, kept, msg)
					}
				}
				return msg
			}

			plain := refuse(false)
			for _, q := range tc.quoted {
				if !strings.Contains(plain, q) {
					t.Fatalf("the unmarked refusal does not carry %q, so this row proves nothing: %s", q, plain)
				}
			}
			marked := refuse(true)
			if !strings.Contains(marked, path+tc.want) {
				t.Errorf("the marked refusal does not say %q: %s", path+tc.want, marked)
			}
			for _, q := range tc.quoted {
				if strings.Contains(marked, q) {
					t.Errorf("the marked refusal carries %q: %s", q, marked)
				}
			}
		})
	}
}

// synthesizeNoPanic runs the synthesis with a caller's context and fails the test,
// rather than crashing it, if the synthesis panics.
func synthesizeNoPanic(t *testing.T, w *types.Workload, nodeCtx renderer.NodeRenderContext) (files []renderer.EmittedFile, err error) {
	t.Helper()
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("SynthesizeWorkloadCore panicked: %v", r)
		}
	}()
	return SynthesizeWorkloadCore(w, renderer.AttachmentProvidesFromContext(nil), nodeCtx)
}
