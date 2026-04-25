// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package validator_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/cognitio/aggrega-cli/pkg/types"
	"github.com/cognitio/aggrega-cli/pkg/validator"
)

func minimalWorkload() *types.Workload {
	w := &types.Workload{
		APIVersion: "aggrega.ai/v1alpha1",
		Kind:       "Workload",
	}
	w.Containers.Set("main", types.ContainerSpec{Image: "app:1.0"})
	return w
}

// ----- Schema unification -----

func TestValidateWorkload_Minimal_Passes(t *testing.T) {
	t.Parallel()
	w := minimalWorkload()
	if err := validator.ValidateWorkload(w, "", "", ""); err != nil {
		t.Fatalf("expected pass, got %v", err)
	}
}

func TestValidateWorkload_Nil_NoError(t *testing.T) {
	t.Parallel()
	if err := validator.ValidateWorkload(nil, "", "", ""); err != nil {
		t.Errorf("nil Workload should pass, got %v", err)
	}
}

func TestValidateWorkload_BadAPIVersion_Fails(t *testing.T) {
	t.Parallel()
	w := minimalWorkload()
	w.APIVersion = "wrong/v0"
	err := validator.ValidateWorkload(w, "", "", "")
	if !errors.Is(err, validator.ErrWorkloadValidation) {
		t.Fatalf("expected ErrWorkloadValidation, got %v", err)
	}
}

func TestValidateWorkload_BadKind_Fails(t *testing.T) {
	t.Parallel()
	w := minimalWorkload()
	w.Kind = "NotWorkload"
	err := validator.ValidateWorkload(w, "", "", "")
	if !errors.Is(err, validator.ErrWorkloadValidation) {
		t.Fatalf("expected ErrWorkloadValidation, got %v", err)
	}
}

func TestValidateWorkload_BadServiceType_Fails(t *testing.T) {
	t.Parallel()
	w := minimalWorkload()
	w.Services.Set("api", types.ServiceSpec{Type: "InvalidType"})
	err := validator.ValidateWorkload(w, "", "", "")
	if !errors.Is(err, validator.ErrWorkloadValidation) {
		t.Fatalf("expected ErrWorkloadValidation, got %v", err)
	}
}

func TestValidateWorkload_PortOutOfRange_Fails(t *testing.T) {
	t.Parallel()
	w := minimalWorkload()
	main, _ := w.Containers.Get("main")
	main.Ports.Set("web", 70000) // > 65535
	w.Containers.Set("main", main)
	err := validator.ValidateWorkload(w, "", "", "")
	if !errors.Is(err, validator.ErrWorkloadValidation) {
		t.Fatalf("expected ErrWorkloadValidation, got %v", err)
	}
}

func TestValidateWorkload_PortZero_Fails(t *testing.T) {
	t.Parallel()
	w := minimalWorkload()
	main, _ := w.Containers.Get("main")
	main.Ports.Set("web", 0) // < 1
	w.Containers.Set("main", main)
	err := validator.ValidateWorkload(w, "", "", "")
	if !errors.Is(err, validator.ErrWorkloadValidation) {
		t.Fatalf("expected ErrWorkloadValidation, got %v", err)
	}
}

func TestValidateWorkload_NegativeReplicas_Fails(t *testing.T) {
	t.Parallel()
	w := minimalWorkload()
	neg := -1
	w.Replicas = &neg
	err := validator.ValidateWorkload(w, "", "", "")
	if !errors.Is(err, validator.ErrWorkloadValidation) {
		t.Fatalf("expected ErrWorkloadValidation, got %v", err)
	}
}

func TestValidateWorkload_BadRestartPolicy_Fails(t *testing.T) {
	t.Parallel()
	w := minimalWorkload()
	main, _ := w.Containers.Get("main")
	main.RestartPolicy = "Sometimes"
	w.Containers.Set("main", main)
	err := validator.ValidateWorkload(w, "", "", "")
	if !errors.Is(err, validator.ErrWorkloadValidation) {
		t.Fatalf("expected ErrWorkloadValidation, got %v", err)
	}
}

func TestValidateWorkload_AllServiceTypesValid(t *testing.T) {
	t.Parallel()
	for _, svcType := range []string{"ClusterIP", "NodePort", "LoadBalancer", "Headless"} {
		w := minimalWorkload()
		w.Services.Set("api", types.ServiceSpec{Type: svcType})
		if err := validator.ValidateWorkload(w, "", "", ""); err != nil {
			t.Errorf("service type %q should pass, got %v", svcType, err)
		}
	}
}

// ----- Error message format -----

func TestValidateWorkload_ErrorMessageFormat(t *testing.T) {
	t.Parallel()
	w := minimalWorkload()
	w.Services.Set("api", types.ServiceSpec{Type: "Invalid"})
	err := validator.ValidateWorkload(w, "recipe.yaml", "ops/test", "1.0.0")
	if err == nil {
		t.Fatal("expected error")
	}
	msg := err.Error()
	if !strings.HasPrefix(msg, "CATALOG_ERROR: Workload") {
		t.Errorf("must start with CATALOG_ERROR prefix, got: %q", msg)
	}
	if !strings.Contains(msg, "ops/test:1.0.0") {
		t.Errorf("must contain recipe name:version, got: %q", msg)
	}
	if !strings.Contains(msg, "recipe.yaml") {
		t.Errorf("must contain recipe path, got: %q", msg)
	}
	if !strings.Contains(msg, "schema validation failed") {
		t.Errorf("must contain stage tag, got: %q", msg)
	}
}

// ----- Disjunction: probe shapes -----

func TestValidateWorkload_ProbeHTTP_Passes(t *testing.T) {
	t.Parallel()
	w := minimalWorkload()
	main, _ := w.Containers.Get("main")
	main.Health = &types.HealthSpec{Ready: &types.ProbeSpec{HTTP: "/healthz"}}
	w.Containers.Set("main", main)
	if err := validator.ValidateWorkload(w, "", "", ""); err != nil {
		t.Errorf("http probe should pass, got %v", err)
	}
}

func TestValidateWorkload_ProbeTCP_Passes(t *testing.T) {
	t.Parallel()
	w := minimalWorkload()
	main, _ := w.Containers.Get("main")
	main.Health = &types.HealthSpec{Ready: &types.ProbeSpec{TCP: 8080}}
	w.Containers.Set("main", main)
	if err := validator.ValidateWorkload(w, "", "", ""); err != nil {
		t.Errorf("tcp probe should pass, got %v", err)
	}
}

func TestValidateWorkload_ProbeExec_Passes(t *testing.T) {
	t.Parallel()
	w := minimalWorkload()
	main, _ := w.Containers.Get("main")
	main.Health = &types.HealthSpec{Ready: &types.ProbeSpec{Exec: []string{"/check.sh"}}}
	w.Containers.Set("main", main)
	if err := validator.ValidateWorkload(w, "", "", ""); err != nil {
		t.Errorf("exec probe should pass, got %v", err)
	}
}

func TestValidateWorkload_ProbeMultipleShapes_Fails(t *testing.T) {
	t.Parallel()
	w := minimalWorkload()
	main, _ := w.Containers.Get("main")
	// Set http AND tcp simultaneously — disjunction violation.
	main.Health = &types.HealthSpec{Ready: &types.ProbeSpec{HTTP: "/health", TCP: 8080}}
	w.Containers.Set("main", main)
	err := validator.ValidateWorkload(w, "", "", "")
	if !errors.Is(err, validator.ErrWorkloadValidation) {
		t.Fatalf("multi-shape probe should fail; got %v", err)
	}
}

// ----- Disjunction: volume variants -----

func TestValidateWorkload_VolumeMultipleVariants_Fails(t *testing.T) {
	t.Parallel()
	w := minimalWorkload()
	w.Volumes.Set("vol", types.VolumeSpec{
		Ephemeral:  &types.EphemeralVolume{SizeLimit: "10Mi"},
		Persistent: &types.PersistentVolume{Size: "1Gi"},
	})
	err := validator.ValidateWorkload(w, "", "", "")
	if !errors.Is(err, validator.ErrWorkloadValidation) {
		t.Fatalf("multi-variant volume should fail; got %v", err)
	}
}

// ----- Findings carry the field path -----

func TestValidateWorkload_FindingsCarryFieldPath(t *testing.T) {
	t.Parallel()
	w := minimalWorkload()
	w.Services.Set("api", types.ServiceSpec{Type: "Bogus"})
	err := validator.ValidateWorkload(w, "", "", "")
	if err == nil {
		t.Fatal("expected error")
	}
	msg := err.Error()
	// Findings emit field paths; should mention the offending location.
	if !strings.Contains(strings.ToLower(msg), "type") {
		t.Errorf("error should reference the offending field, got: %q", msg)
	}
}
