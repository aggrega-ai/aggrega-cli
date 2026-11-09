// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package helmcore

import (
	"errors"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
	"github.com/aggrega-ai/aggrega-cli/internal/plugincondition"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// quantityWorkload is a stateful workload carrying a valid value in every
// quantity field the synthesis emits: a container's requests and limits (cpu,
// memory and an extended resource), an init container's requests, and one
// volume of each sized variant.
func quantityWorkload() *types.Workload {
	w := &types.Workload{APIVersion: "aggrega.ai/v1beta1", Kind: "Workload", Form: "stateful"}

	c := types.ContainerSpec{Image: "app:1.0"}
	c.Ports.Set("web", types.PortSpec{Port: 8080})
	requests := &types.ResourceList{CPU: "250m", Memory: "128Mi"}
	requests.Extended.Set("nvidia.com/gpu", "1")
	limits := &types.ResourceList{CPU: "1", Memory: "256Mi"}
	limits.Extended.Set("nvidia.com/gpu", "1")
	c.Resources = &types.ResourceSpec{Requests: requests, Limits: limits}
	w.Containers.Set("main", c)

	w.InitContainers = []types.ContainerSpec{{
		Image:     "migrate:1.0",
		Resources: &types.ResourceSpec{Requests: &types.ResourceList{CPU: "100m", Memory: "64Mi"}},
	}}

	w.Volumes.Set("scratch", types.VolumeSpec{Ephemeral: &types.EphemeralVolume{SizeLimit: "64Mi"}})
	w.Volumes.Set("data", types.VolumeSpec{Persistent: &types.PersistentVolume{Size: "1Gi"}})
	w.Volumes.Set("tmpl", types.VolumeSpec{PersistentTemplate: &types.PersistentTemplateVolume{Size: "2Gi"}})

	svc := types.ServiceSpec{Type: "Headless"}
	svc.Ports.Set("web", 8080)
	w.Services.Set("headless", svc)
	return w
}

// mainContainer returns the workload's main container for a row to edit and
// store back — ContainerSpec is held by value in the ordered map.
func mainContainer(w *types.Workload) types.ContainerSpec {
	c, _ := w.Containers.Get("main")
	return c
}

// synthNoPanic runs the synthesis and fails the test, rather than crashing it,
// if the synthesis panics: a refused value must come back as an error.
func synthNoPanic(t *testing.T, w *types.Workload) (files []renderer.EmittedFile, err error) {
	t.Helper()
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("SynthesizeWorkloadCore panicked: %v", r)
		}
	}()
	return SynthesizeWorkloadCore(w, renderer.AttachmentProvidesFromContext(nil), nodeCtxFor("store"))
}

// TestSynthesizeWorkload_EveryEmittedQuantityIsChecked drives one malformed
// value into each quantity field the synthesis emits, one field per row. Every
// row halts with the value-not-representable condition, emits nothing, and
// names the node, the authored field path and the value. The positive control
// is the same workload with every value valid, which builds and carries each
// value verbatim.
func TestSynthesizeWorkload_EveryEmittedQuantityIsChecked(t *testing.T) {
	t.Parallel()

	files, err := synthNoPanic(t, quantityWorkload())
	if err != nil {
		t.Fatalf("positive control: the valid workload halted: %v", err)
	}
	body := string(concatBodies(files))
	for _, kv := range [][2]string{{"cpu", "250m"}, {"memory", "128Mi"}, {"nvidia.com/gpu", "1"}, {"cpu", "1"},
		{"memory", "256Mi"}, {"cpu", "100m"}, {"memory", "64Mi"}, {"sizeLimit", "64Mi"}, {"storage", "1Gi"}, {"storage", "2Gi"}} {
		if !carries(body, kv[0], kv[1]) {
			t.Errorf("positive control: the manifests do not carry %s: %s verbatim:\n%s", kv[0], kv[1], body)
		}
	}

	cases := []struct {
		name  string
		set   func(w *types.Workload)
		path  string
		value string
	}{
		{"cpu request", func(w *types.Workload) {
			c := mainContainer(w)
			c.Resources.Requests.CPU = "lots"
			w.Containers.Set("main", c)
		},
			"containers.main.resources.requests.cpu", "lots"},
		{"memory request", func(w *types.Workload) {
			c := mainContainer(w)
			c.Resources.Requests.Memory = "1 gigabyte"
			w.Containers.Set("main", c)
		}, "containers.main.resources.requests.memory", "1 gigabyte"},
		{"cpu limit", func(w *types.Workload) {
			c := mainContainer(w)
			c.Resources.Limits.CPU = "1.5 cores"
			w.Containers.Set("main", c)
		},
			"containers.main.resources.limits.cpu", "1.5 cores"},
		{"memory limit", func(w *types.Workload) {
			c := mainContainer(w)
			c.Resources.Limits.Memory = "{\n}"
			w.Containers.Set("main", c)
		},
			"containers.main.resources.limits.memory", "{\n}"},
		{"extended-resource request", func(w *types.Workload) {
			c := mainContainer(w)
			c.Resources.Requests.Extended.Set("nvidia.com/gpu", "one")
			w.Containers.Set("main", c)
		}, "containers.main.resources.requests.nvidia.com/gpu", "one"},
		{"extended-resource limit left empty", func(w *types.Workload) {
			c := mainContainer(w)
			c.Resources.Limits.Extended.Set("nvidia.com/gpu", "")
			w.Containers.Set("main", c)
		}, "containers.main.resources.limits.nvidia.com/gpu", ""},
		{"init container cpu request", func(w *types.Workload) { w.InitContainers[0].Resources.Requests.CPU = "m" },
			"initContainers[0].resources.requests.cpu", "m"},
		{"ephemeral sizeLimit", func(w *types.Workload) {
			w.Volumes.Set("scratch", types.VolumeSpec{Ephemeral: &types.EphemeralVolume{SizeLimit: "1e1.5"}})
		}, "volumes.scratch.ephemeral.sizeLimit", "1e1.5"},
		{"persistent size", func(w *types.Workload) {
			w.Volumes.Set("data", types.VolumeSpec{Persistent: &types.PersistentVolume{Size: "10 GB"}})
		}, "volumes.data.persistent.size", "10 GB"},
		{"persistent size left empty", func(w *types.Workload) {
			w.Volumes.Set("data", types.VolumeSpec{Persistent: &types.PersistentVolume{}})
		}, "volumes.data.persistent.size", ""},
		{"persistentTemplate size", func(w *types.Workload) {
			w.Volumes.Set("tmpl", types.VolumeSpec{PersistentTemplate: &types.PersistentTemplateVolume{Size: "Ki"}})
		}, "volumes.tmpl.persistentTemplate.size", "Ki"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			w := quantityWorkload()
			c.set(w)
			files, err := synthNoPanic(t, w)
			if err == nil {
				t.Fatalf("the malformed %s built; the manifests carry it verbatim", c.path)
			}
			if !errors.Is(err, plugincondition.ErrPluginValueNotRepresentable()) {
				t.Errorf("expected the value-not-representable condition, got %v", err)
			}
			if len(files) != 0 {
				t.Errorf("a refused workload emitted %d file(s)", len(files))
			}
			msg := err.Error()
			for _, want := range []string{`node "store"`, c.path + ": ", `"` + strings.ReplaceAll(c.value, "\n", `\n`) + `" is not a quantity`} {
				if !strings.Contains(msg, want) {
					t.Errorf("the refusal does not say %q:\n  %s", want, msg)
				}
			}
		})
	}
}

// TestSynthesizeWorkload_QuantitiesOutsideTheCheck pins what is deliberately
// not judged: an empty cpu, memory or sizeLimit is omitted from the manifests
// rather than emitted, so it is not refused; and every form the grammar admits
// is carried verbatim, including the n and u suffixes and an exponent.
func TestSynthesizeWorkload_QuantitiesOutsideTheCheck(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		set  func(w *types.Workload)
		want [][2]string
	}{
		{"empty cpu and memory are omitted", func(w *types.Workload) {
			c := mainContainer(w)
			c.Resources.Requests.CPU = ""
			c.Resources.Requests.Memory = ""
			w.Containers.Set("main", c)
		}, nil},
		{"an empty sizeLimit is omitted", func(w *types.Workload) {
			w.Volumes.Set("scratch", types.VolumeSpec{Ephemeral: &types.EphemeralVolume{}})
		}, nil},
		{"nano and micro suffixes", func(w *types.Workload) {
			c := mainContainer(w)
			c.Resources.Requests.CPU = "5n"
			c.Resources.Requests.Memory = "4u"
			w.Containers.Set("main", c)
		}, [][2]string{{"cpu", "5n"}, {"memory", "4u"}}},
		{"a fractional binary quantity and an exponent", func(w *types.Workload) {
			c := mainContainer(w)
			c.Resources.Limits.Memory = "1.5Gi"
			c.Resources.Limits.CPU = "1e3"
			w.Containers.Set("main", c)
		}, [][2]string{{"memory", "1.5Gi"}, {"cpu", "1e3"}}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			w := quantityWorkload()
			c.set(w)
			files, err := synthNoPanic(t, w)
			if err != nil {
				t.Fatalf("a value outside the check halted: %v", err)
			}
			body := string(concatBodies(files))
			for _, kv := range c.want {
				if !carries(body, kv[0], kv[1]) {
					t.Errorf("the manifests do not carry %s: %s verbatim:\n%s", kv[0], kv[1], body)
				}
			}
		})
	}
}

// carries reports whether a rendered manifest holds key: value, with the value
// plain or quoted — the marshaller quotes a value that would otherwise read
// back as a number, which is the same string either way.
func carries(body, key, value string) bool {
	return strings.Contains(body, key+": "+value+"\n") || strings.Contains(body, key+": \""+value+"\"\n")
}

// TestSynthesizeWorkload_ReferenceFormChecksNoQuantity — a reference workload
// emits only an ExternalName Service, none of the quantity fields, so a
// quantity it happens to carry is not judged.
func TestSynthesizeWorkload_ReferenceFormChecksNoQuantity(t *testing.T) {
	t.Parallel()
	w := &types.Workload{APIVersion: "aggrega.ai/v1beta1", Kind: "Workload", Form: "reference", Endpoint: "db.example.net"}
	w.Volumes.Set("data", types.VolumeSpec{Persistent: &types.PersistentVolume{Size: "lots"}})

	if _, err := synthNoPanic(t, w); err != nil {
		t.Fatalf("a reference workload judged a quantity it does not emit: %v", err)
	}
}
