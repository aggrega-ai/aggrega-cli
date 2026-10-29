// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package composecore

import (
	"errors"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/rendercore"
	"github.com/aggrega-ai/aggrega-cli/internal/plugincondition"
	"github.com/aggrega-ai/aggrega-cli/internal/redact"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"github.com/aggrega-ai/aggrega-cli/plugins/internal/quantity"
)

// TestTranslateComposeResources_AMarkedWorkloadQuotesNoValue holds both compose
// quantity refusals to the input's marking over the workload. Each row refuses one
// quantity twice: unmarked, quoting the value as it always has, and marked, quoting
// no part of it while still saying which of the two failures it met, naming the
// resource, the role and the path, and keeping the condition.
func TestTranslateComposeResources_AMarkedWorkloadQuotesNoValue(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		set    func(r *types.ResourceSpec)
		quoted []string
		// want is what the marked refusal must say; kept is what both say.
		want []string
		kept []string
	}{
		{
			name:   "a word refused for the placeholder's own reason is quoted as the placeholder",
			set:    func(r *types.ResourceSpec) { r.Limits.CPU = "s3cret-cpu" },
			quoted: []string{"s3cret-cpu"},
			want:   []string{`"` + redact.Placeholder() + `" is not a quantity: it does not begin with a number`},
			kept:   []string{"cpu limit at containers.main.resources.limits.cpu: ", quantity.Shape},
		},
		{
			name:   "a reason quoting part of the value is withheld with it",
			set:    func(r *types.ResourceSpec) { r.Requests.Memory = "512Zsecret" },
			quoted: []string{"512Zsecret", "Zsecret"},
			want:   []string{"not a quantity; " + quantity.ValueWithheld},
			kept:   []string{"memory request at containers.main.resources.requests.memory: ", quantity.Shape},
		},
		{
			name:   "a quantity compose cannot represent is refused without the value",
			set:    func(r *types.ResourceSpec) { r.Limits.Memory = "1500m" },
			quoted: []string{"1500m"},
			want: []string{"memory limit at containers.main.resources.limits.memory is a quantity compose cannot " +
				"represent exactly; " + quantity.ValueWithheld},
			kept: []string{"whole number of bytes"},
		},
		{
			name:   "a GPU count compose cannot represent is refused without the value",
			set:    func(r *types.ResourceSpec) { r.Limits.Extended.Set(nvidiaGPUResource, "1.25") },
			quoted: []string{"1.25"},
			want: []string{"nvidia.com/gpu limit at containers.main.resources.limits.nvidia.com/gpu is a quantity " +
				"compose cannot represent exactly; " + quantity.ValueWithheld},
			kept: []string{"whole number of devices"},
		},
		{
			name: "a GPU request and limit that disagree are refused without either count",
			set: func(r *types.ResourceSpec) {
				r.Limits.Extended.Set(nvidiaGPUResource, "7")
				r.Requests.Extended.Set(nvidiaGPUResource, "3")
			},
			quoted: []string{"(3 and 7)"},
			want:   []string{"ask for different counts; " + quantity.ValueWithheld},
			kept: []string{"containers.main.resources.requests.nvidia.com/gpu",
				"containers.main.resources.limits.nvidia.com/gpu"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			refuse := func(marked bool) string {
				t.Helper()
				r := &types.ResourceSpec{
					Requests: &types.ResourceList{CPU: "250m", Memory: "128Mi"},
					Limits:   &types.ResourceList{CPU: "1", Memory: "256Mi"},
				}
				tc.set(r)
				_, err := translateComposeResources(r, "containers.main.resources", "node", marked)
				if err == nil {
					t.Fatalf("the faulty quantity converted (marked %v)", marked)
				}
				if !errors.Is(err, plugincondition.ErrPluginValueNotRepresentable()) {
					t.Errorf("expected the value-not-representable condition, got %v", err)
				}
				msg := err.Error()
				for _, k := range append(tc.kept, quantityUnconvertible) {
					if !strings.Contains(msg, k) {
						t.Errorf("the refusal (marked %v) does not say %q: %s", marked, k, msg)
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
			for _, w := range tc.want {
				if !strings.Contains(marked, w) {
					t.Errorf("the marked refusal does not say %q: %s", w, marked)
				}
			}
			for _, q := range tc.quoted {
				if strings.Contains(marked, q) {
					t.Errorf("the marked refusal carries %q: %s", q, marked)
				}
			}
		})
	}
}

// TestSynthesizeWorkloadCore_ReadsTheWorkloadsMarkingFromItsContext drives the
// marking the way the guest hands it over — on the render context the synthesis
// is given — so the core's own call into the quantity conversion is what is
// proven, not only the conversion. The refusal is read the way the guest reports
// it, flattened from the whole chain: the synthesis wraps the conversion's refusal
// in a frame naming the node and container, and that frame's own text alone
// carries neither the value nor the placeholder.
func TestSynthesizeWorkloadCore_ReadsTheWorkloadsMarkingFromItsContext(t *testing.T) {
	t.Parallel()
	const value = "s3cret-memory"
	synthesize := func(marked bool) string {
		t.Helper()
		w := minimalWorkload()
		c, _ := w.Containers.Get("main")
		c.Resources = &types.ResourceSpec{Limits: &types.ResourceList{Memory: value}}
		w.Containers.Set("main", c)
		nodeCtx := nodeCtxFor("store")
		nodeCtx.SensitiveWorkload = marked
		_, _, err := SynthesizeWorkloadCore(w, nodeCtx)
		if err == nil {
			t.Fatalf("the refused quantity built (marked %v)", marked)
		}
		return rendercore.FailureOf(err).Message
	}
	if plain := synthesize(false); !strings.Contains(plain, `"`+value+`" is not a quantity`) {
		t.Fatalf("the unmarked refusal does not quote the value, so this test proves nothing: %s", plain)
	}
	marked := synthesize(true)
	if strings.Contains(marked, value) {
		t.Errorf("the marked refusal carries the value: %s", marked)
	}
	if want := `"` + redact.Placeholder() + `" is not a quantity: it does not begin with a number`; !strings.Contains(marked, want) {
		t.Errorf("the marked refusal does not say %q: %s", want, marked)
	}
}
