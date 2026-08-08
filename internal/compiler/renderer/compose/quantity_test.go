// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package compose

import (
	"errors"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

func TestK8sCPUToComposeCPUs(t *testing.T) {
	cases := []struct {
		in      string
		want    string
		wantErr bool
	}{
		{"500m", "0.5", false},
		{"100m", "0.1", false},
		{"1500m", "1.5", false},
		{"2000m", "2", false},
		{"250m", "0.25", false},
		{"1m", "0.001", false},
		{"0.5", "0.5", false},
		{"2", "2", false},
		{"1.25", "1.25", false},
		{"", "", true}, // empty is handled by the caller; direct call rejects
		{"abc", "", true},
		{"m", "", true},
		{"-1", "", true},
	}
	for _, c := range cases {
		got, err := k8sCPUToComposeCPUs(c.in)
		if c.wantErr {
			if err == nil {
				t.Errorf("k8sCPUToComposeCPUs(%q) = %q, want error", c.in, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("k8sCPUToComposeCPUs(%q) unexpected error: %v", c.in, err)
			continue
		}
		if got != c.want {
			t.Errorf("k8sCPUToComposeCPUs(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestK8sMemToComposeBytes(t *testing.T) {
	cases := []struct {
		in      string
		want    string
		wantErr bool
	}{
		{"128Mi", "134217728", false},
		{"1Gi", "1073741824", false},
		{"512Ki", "524288", false},
		{"2Ti", "2199023255552", false},
		{"128M", "128000000", false},
		{"1G", "1000000000", false},
		{"1k", "1000", false},
		{"1048576", "1048576", false}, // plain bytes
		{"0", "0", false},
		{"1.5Gi", "", true}, // fractional mantissa halts
		{"128Xi", "", true}, // unknown suffix
		{"abc", "", true},
		{"-5", "", true},
	}
	for _, c := range cases {
		got, err := k8sMemToComposeBytes(c.in)
		if c.wantErr {
			if err == nil {
				t.Errorf("k8sMemToComposeBytes(%q) = %q, want error", c.in, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("k8sMemToComposeBytes(%q) unexpected error: %v", c.in, err)
			continue
		}
		if got != c.want {
			t.Errorf("k8sMemToComposeBytes(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestTranslateComposeResources(t *testing.T) {
	r := &types.ResourceSpec{
		Requests: &types.ResourceList{CPU: "250m", Memory: "128Mi"},
		Limits:   &types.ResourceList{CPU: "1", Memory: "256Mi"},
	}
	deploy, err := translateComposeResources(r, "node")
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
	if d, _ := translateComposeResources(nil, "node"); d != nil {
		t.Error("nil spec should produce nil deploy")
	}
	if d, _ := translateComposeResources(&types.ResourceSpec{}, "node"); d != nil {
		t.Error("empty spec should produce nil deploy")
	}

	// An unconvertible quantity halts with the sentinel.
	_, err = translateComposeResources(&types.ResourceSpec{Limits: &types.ResourceList{Memory: "1.5Gi"}}, "node")
	if !errors.Is(err, ErrUnconvertibleComposeQuantity()) {
		t.Errorf("expected ErrUnconvertibleComposeQuantity, got %v", err)
	}
}
