// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package composecore

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/plugincondition"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"github.com/aggrega-ai/aggrega-cli/plugins/internal/quantity"
)

// mustParse parses a quantity a conversion row starts from; a refusal here is a
// broken row, not a conversion result.
func mustParse(t *testing.T, s string) quantity.Quantity {
	t.Helper()
	q, err := quantity.Parse(s)
	if err != nil {
		t.Fatalf("row input %q is not a quantity: %v", s, err)
	}
	return q
}

// TestComposeCPUs converts parsed cpu quantities to compose cpus values. The
// accepted rows cover every form a quantity takes; the refused rows are
// quantities compose has no exact form for.
func TestComposeCPUs(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
		ok   bool
	}{
		{"half a core in millicpu", "500m", "0.5", true},
		{"a tenth in millicpu", "100m", "0.1", true},
		{"one and a half in millicpu", "1500m", "1.5", true},
		{"whole cores in millicpu", "2000m", "2", true},
		{"a quarter in millicpu", "250m", "0.25", true},
		{"one millicpu", "1m", "0.001", true},
		{"zero millicpu", "0m", "0", true},
		{"a plain core count is emitted as written", "0.5", "0.5", true},
		{"a plain whole core count", "2", "2", true},
		{"a plain count keeps its spelling", "0.50", "0.50", true},
		{"a plain count with a leading point", ".5", ".5", true},
		{"an exponent", "1e1", "10", true},
		{"a negative exponent", "5e-1", "0.5", true},
		{"a signed count", "+1", "1", true},
		{"negative zero", "-0", "0", true},
		{"nano cores", "5n", "0.000000005", true},
		{"micro cores", "4u", "0.000004", true},
		{"a binary suffix", "1Ki", "1024", true},

		{"a negative count", "-1", "", false},
		{"a negative millicpu", "-500m", "", false},
		{"finer than a nanocore", "0.1n", "", false},
		{"a plain count finer than a nanocore", "0.0000000001", "", false},
		{"more cores than 64 bits of nanocores", "9223372037", "", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := composeCPUs(mustParse(t, c.in))
			if ok != c.ok || got != c.want {
				t.Errorf("composeCPUs(%q) = (%q, %v), want (%q, %v)", c.in, got, ok, c.want, c.ok)
			}
		})
	}
}

// TestComposeMemory converts parsed memory quantities to exact byte counts.
func TestComposeMemory(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
		ok   bool
	}{
		{"mebibytes", "128Mi", "134217728", true},
		{"a gibibyte", "1Gi", "1073741824", true},
		{"kibibytes", "512Ki", "524288", true},
		{"tebibytes", "2Ti", "2199023255552", true},
		{"decimal megabytes", "128M", "128000000", true},
		{"a decimal gigabyte", "1G", "1000000000", true},
		{"a decimal kilobyte", "1k", "1000", true},
		{"plain bytes", "1048576", "1048576", true},
		{"zero", "0", "0", true},
		{"a fractional mantissa that is whole bytes", "1.5Gi", "1610612736", true},
		{"an exponent", "1e3", "1000", true},
		{"a signed count", "+128", "128", true},
		{"a milli count that is whole bytes", "1000m", "1", true},

		{"half a byte", "0.5", "", false},
		{"a fraction of a byte in milli", "1500m", "", false},
		{"micro bytes", "4u", "", false},
		{"nano bytes", "5n", "", false},
		{"a negative count", "-5", "", false},
		{"past the int64 range", "8Ei", "", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := composeMemory(mustParse(t, c.in))
			if ok != c.ok || got != c.want {
				t.Errorf("composeMemory(%q) = (%q, %v), want (%q, %v)", c.in, got, ok, c.want, c.ok)
			}
		})
	}
}

// TestComposeDeviceCount converts parsed GPU quantities to whole device counts.
func TestComposeDeviceCount(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
		ok   bool
	}{
		{"one", "1", "1", true},
		{"two", "2", "2", true},
		{"zero", "0", "0", true},
		{"a signed count", "+2", "2", true},
		{"a milli count that is whole", "2000m", "2", true},
		{"an exponent", "1e1", "10", true},

		{"half a device", "0.5", "", false},
		{"a fraction in milli", "500m", "", false},
		{"one and a half", "1.5", "", false},
		{"a negative count", "-1", "", false},
		{"past the int64 range", "8Ei", "", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := composeDeviceCount(mustParse(t, c.in))
			if ok != c.ok || got != c.want {
				t.Errorf("composeDeviceCount(%q) = (%q, %v), want (%q, %v)", c.in, got, ok, c.want, c.ok)
			}
		})
	}
}

// gpuSpec builds a resources block whose only content is the given nvidia.com/gpu
// limit and request; an empty string leaves that side unauthored.
func gpuSpec(limit, request string) *types.ResourceSpec {
	r := &types.ResourceSpec{}
	if limit != "" {
		r.Limits = &types.ResourceList{}
		r.Limits.Extended.Set(nvidiaGPUResource, limit)
	}
	if request != "" {
		r.Requests = &types.ResourceList{}
		r.Requests.Extended.Set(nvidiaGPUResource, request)
	}
	return r
}

// TestTranslateComposeResources_NVIDIAGPU proves which count the device entry is
// written from, and that a container asking for no GPU gets no entry. Each row
// names the authored limit and request (empty for unauthored) and the count the
// one entry must carry, or zero for no entry and no deploy block at all.
func TestTranslateComposeResources_NVIDIAGPU(t *testing.T) {
	cases := []struct {
		name           string
		limit, request string
		want           int64
	}{
		{"a limit of one", "1", "", 1},
		{"a limit of two", "2", "", 2},
		{"a request alone", "", "2", 2},
		{"a request equal to the limit", "3", "3", 3},
		{"equal counts spelled differently", "2", "2000m", 2},
		{"a limit of zero asks for no GPU", "0", "", 0},
		{"both zero", "0", "0", 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			deploy, err := translateComposeResources(gpuSpec(c.limit, c.request), "containers.main.resources", "node", false)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if c.want == 0 {
				if deploy != nil {
					t.Fatalf("no GPU asked, yet a deploy block was written: %+v", deploy.Resources)
				}
				return
			}
			if deploy == nil || deploy.Resources == nil || deploy.Resources.Reservations == nil {
				t.Fatal("expected a reservations block")
			}
			if deploy.Resources.Limits != nil {
				t.Errorf("a GPU-only block wrote limits %+v", deploy.Resources.Limits)
			}
			res := deploy.Resources.Reservations
			if res.CPUs != "" || res.Memory != "" {
				t.Errorf("a GPU-only block wrote cpus/memory %+v", res.ResourceQuantities)
			}
			want := []DeviceRequest{{Driver: "nvidia", Count: c.want, Capabilities: []string{"gpu"}}}
			if !reflect.DeepEqual(res.Devices, want) {
				t.Errorf("devices = %#v, want %#v", res.Devices, want)
			}
		})
	}

	// The device entry joins the cpus/memory a request also carries, rather than
	// replacing them.
	r := gpuSpec("1", "")
	r.Requests = &types.ResourceList{CPU: "250m", Memory: "128Mi"}
	deploy, err := translateComposeResources(r, "containers.main.resources", "node", false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	res := deploy.Resources.Reservations
	if res.CPUs != "0.25" || res.Memory != "134217728" || len(res.Devices) != 1 {
		t.Errorf("reservations = %+v, want cpus 0.25, memory 134217728 and one device", res)
	}
}

// TestTranslateComposeResources_NVIDIAGPURefusals proves a GPU count the
// translation cannot state as a whole number of devices halts, naming the
// container's path and the key, rather than writing a guess. One fault per row;
// the positive control is a whole count.
func TestTranslateComposeResources_NVIDIAGPURefusals(t *testing.T) {
	if _, err := translateComposeResources(gpuSpec("1", "1"), "containers.main.resources", "node", false); err != nil {
		t.Fatalf("positive control: a whole count halted: %v", err)
	}

	const limitPath = "containers.main.resources.limits.nvidia.com/gpu"
	const requestPath = "containers.main.resources.requests.nvidia.com/gpu"
	cases := []struct {
		name           string
		limit, request string
		want           []string
	}{
		{"a fractional limit", "0.5", "",
			[]string{"nvidia.com/gpu limit", limitPath, `"0.5"`, "a quantity compose cannot represent exactly", "whole number of devices"}},
		{"a milli limit", "500m", "",
			[]string{"nvidia.com/gpu limit", limitPath, `"500m"`, "a quantity compose cannot represent exactly"}},
		{"a negative request", "", "-1",
			[]string{"nvidia.com/gpu request", requestPath, `"-1"`, "a quantity compose cannot represent exactly"}},
		{"a non-numeric limit", "two", "",
			[]string{"nvidia.com/gpu limit", limitPath, `"two"`, "is not a quantity"}},
		{"a request and a limit that disagree", "2", "1",
			[]string{requestPath, limitPath, "different counts (1 and 2)"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := translateComposeResources(gpuSpec(c.limit, c.request), "containers.main.resources", "node", false)
			if err == nil {
				t.Fatal("the faulty GPU count was written")
			}
			if !errors.Is(err, plugincondition.ErrPluginValueNotRepresentable()) {
				t.Errorf("expected the value-not-representable condition, got %v", err)
			}
			msg := err.Error()
			for _, w := range c.want {
				if !strings.Contains(msg, w) {
					t.Errorf("the refusal does not say %q:\n  %s", w, msg)
				}
			}
			if !strings.Contains(msg, quantityUnconvertible) {
				t.Errorf("the refusal no longer ends with %q:\n  %s", quantityUnconvertible, msg)
			}
		})
	}
}

func TestTranslateComposeResources(t *testing.T) {
	r := &types.ResourceSpec{
		Requests: &types.ResourceList{CPU: "250m", Memory: "128Mi"},
		Limits:   &types.ResourceList{CPU: "1", Memory: "256Mi"},
	}
	deploy, err := translateComposeResources(r, "containers.main.resources", "node", false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if deploy == nil || deploy.Resources == nil {
		t.Fatal("expected a deploy.resources block")
	}
	if deploy.Resources.Reservations.CPUs != "0.25" || deploy.Resources.Reservations.Memory != "134217728" {
		t.Errorf("reservations = %+v", deploy.Resources.Reservations)
	}
	if deploy.Resources.Limits.CPUs != "1" || deploy.Resources.Limits.Memory != "268435456" {
		t.Errorf("limits = %+v", deploy.Resources.Limits)
	}

	// nil spec and empty spec produce no deploy block.
	if d, _ := translateComposeResources(nil, "containers.main.resources", "node", false); d != nil {
		t.Error("nil spec should produce nil deploy")
	}
	if d, _ := translateComposeResources(&types.ResourceSpec{}, "containers.main.resources", "node", false); d != nil {
		t.Error("empty spec should produce nil deploy")
	}
}

// TestTranslateComposeResourcesRefusals holds the two refusals apart. Each row
// carries one faulty quantity in one position; every row must halt with the
// value-not-representable condition, and its sentence must name the resource,
// whether it is a request or a limit, the authored path and the value, and say
// which of the two failures it met. The positive control is the same spec with
// every quantity valid.
func TestTranslateComposeResourcesRefusals(t *testing.T) {
	valid := func() *types.ResourceSpec {
		return &types.ResourceSpec{
			Requests: &types.ResourceList{CPU: "250m", Memory: "128Mi"},
			Limits:   &types.ResourceList{CPU: "1", Memory: "256Mi"},
		}
	}
	if _, err := translateComposeResources(valid(), "containers.main.resources", "node", false); err != nil {
		t.Fatalf("positive control: the valid spec halted: %v", err)
	}

	const notAQuantity = "is not a quantity"
	const notExact = "a quantity compose cannot represent exactly"
	cases := []struct {
		name  string
		set   func(r *types.ResourceSpec)
		want  []string
		other string
	}{
		{"a cpu request that is a word", func(r *types.ResourceSpec) { r.Requests.CPU = "lots" },
			[]string{"cpu request", "containers.main.resources.requests.cpu", `"lots"`, notAQuantity}, notExact},
		{"a memory request with a unit word", func(r *types.ResourceSpec) { r.Requests.Memory = "1 gigabyte" },
			[]string{"memory request", "containers.main.resources.requests.memory", `"1 gigabyte"`, notAQuantity}, notExact},
		{"a cpu limit with a fractional exponent", func(r *types.ResourceSpec) { r.Limits.CPU = "1e1.5" },
			[]string{"cpu limit", "containers.main.resources.limits.cpu", `"1e1.5"`, notAQuantity}, notExact},
		{"a memory limit left as a bare suffix", func(r *types.ResourceSpec) { r.Limits.Memory = "Mi" },
			[]string{"memory limit", "containers.main.resources.limits.memory", `"Mi"`, notAQuantity}, notExact},
		{"a memory request that is a fraction of a byte", func(r *types.ResourceSpec) { r.Requests.Memory = "1500m" },
			[]string{"memory request", "containers.main.resources.requests.memory", `"1500m"`, notExact,
				"whole number of bytes"}, notAQuantity},
		{"a negative cpu limit", func(r *types.ResourceSpec) { r.Limits.CPU = "-1" },
			[]string{"cpu limit", "containers.main.resources.limits.cpu", `"-1"`, notExact,
				"nine decimal places"}, notAQuantity},
		{"a cpu request finer than a nanocore", func(r *types.ResourceSpec) { r.Requests.CPU = "0.1n" },
			[]string{"cpu request", "containers.main.resources.requests.cpu", `"0.1n"`, notExact}, notAQuantity},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := valid()
			c.set(r)
			_, err := translateComposeResources(r, "containers.main.resources", "node", false)
			if err == nil {
				t.Fatal("the faulty quantity converted")
			}
			if !errors.Is(err, plugincondition.ErrPluginValueNotRepresentable()) {
				t.Errorf("expected the value-not-representable condition, got %v", err)
			}
			msg := err.Error()
			for _, w := range c.want {
				if !strings.Contains(msg, w) {
					t.Errorf("the refusal does not say %q:\n  %s", w, msg)
				}
			}
			if strings.Contains(msg, c.other) {
				t.Errorf("the refusal names the other failure (%q) too:\n  %s", c.other, msg)
			}
			if !strings.Contains(msg, quantityUnconvertible) {
				t.Errorf("the refusal no longer ends with %q:\n  %s", quantityUnconvertible, msg)
			}
		})
	}
}
