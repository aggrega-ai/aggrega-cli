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

func slotsWith(entries ...struct {
	name string
	slot types.SecretSlot
}) types.OrderedMap[types.SecretSlot] {
	out := types.OrderedMap[types.SecretSlot]{}
	for _, e := range entries {
		out.Set(e.name, e.slot)
	}
	return out
}

// ----- Pass cases -----

func TestValidateWorkloadSecrets_AllowlistedSlot_Passes(t *testing.T) {
	t.Parallel()
	slots := slotsWith(
		struct {
			name string
			slot types.SecretSlot
		}{"db-password", types.SecretSlot{Required: true, Inject: []string{"env", "file"}}},
	)

	w := &types.Workload{}
	c := types.ContainerSpec{Image: "app:1.0"}
	c.Secrets.Set("DB_PASS", types.SecretRef{From: "db-password", As: "env"})
	w.Containers.Set("main", c)

	if err := validator.ValidateWorkloadSecrets(w, slots, "", "", ""); err != nil {
		t.Fatalf("expected pass, got %v", err)
	}
}

func TestValidateWorkloadSecrets_FileDelivery_Passes(t *testing.T) {
	t.Parallel()
	slots := slotsWith(
		struct {
			name string
			slot types.SecretSlot
		}{"tls-cert", types.SecretSlot{Required: true, Inject: []string{"file"}}},
	)

	w := &types.Workload{}
	c := types.ContainerSpec{Image: "app:1.0"}
	c.Secrets.Set("TLS", types.SecretRef{From: "tls-cert", As: "file", Path: "/etc/tls/cert.pem"})
	w.Containers.Set("main", c)

	if err := validator.ValidateWorkloadSecrets(w, slots, "", "", ""); err != nil {
		t.Fatalf("expected pass, got %v", err)
	}
}

func TestValidateWorkloadSecrets_NoSecrets_Passes(t *testing.T) {
	t.Parallel()
	slots := types.OrderedMap[types.SecretSlot]{}
	w := &types.Workload{}
	w.Containers.Set("main", types.ContainerSpec{Image: "app:1.0"})

	if err := validator.ValidateWorkloadSecrets(w, slots, "", "", ""); err != nil {
		t.Errorf("workload with no secrets should pass, got %v", err)
	}
}

func TestValidateWorkloadSecrets_NilWorkload_NoError(t *testing.T) {
	t.Parallel()
	slots := types.OrderedMap[types.SecretSlot]{}
	if err := validator.ValidateWorkloadSecrets(nil, slots, "", "", ""); err != nil {
		t.Errorf("nil Workload should pass, got %v", err)
	}
}

// ----- Fail cases -----

func TestValidateWorkloadSecrets_UndeclaredSlot_Fails(t *testing.T) {
	t.Parallel()
	slots := slotsWith(
		struct {
			name string
			slot types.SecretSlot
		}{"db-password", types.SecretSlot{Inject: []string{"env"}}},
	)

	w := &types.Workload{}
	c := types.ContainerSpec{Image: "app:1.0"}
	c.Secrets.Set("OTHER", types.SecretRef{From: "not-declared", As: "env"})
	w.Containers.Set("main", c)

	err := validator.ValidateWorkloadSecrets(w, slots, "", "", "")
	if !errors.Is(err, validator.ErrWorkloadSecretsSchema) {
		t.Fatalf("expected ErrWorkloadSecretsSchema, got %v", err)
	}
	if !strings.Contains(err.Error(), "not-declared") {
		t.Errorf("error should name the undeclared slot: %v", err)
	}
}

func TestValidateWorkloadSecrets_AsNotInAllowlist_Fails(t *testing.T) {
	t.Parallel()
	slots := slotsWith(
		struct {
			name string
			slot types.SecretSlot
		}{"db-password", types.SecretSlot{Inject: []string{"env"}}}, // file NOT in allowlist
	)

	w := &types.Workload{}
	c := types.ContainerSpec{Image: "app:1.0"}
	c.Secrets.Set("DB_PASS", types.SecretRef{From: "db-password", As: "file", Path: "/etc/db"})
	w.Containers.Set("main", c)

	err := validator.ValidateWorkloadSecrets(w, slots, "", "", "")
	if !errors.Is(err, validator.ErrWorkloadSecretsSchema) {
		t.Fatalf("expected ErrWorkloadSecretsSchema, got %v", err)
	}
	if !strings.Contains(err.Error(), `"file"`) {
		t.Errorf("error should name the disallowed mechanism, got: %v", err)
	}
}

func TestValidateWorkloadSecrets_BlackboxSlot_Fails(t *testing.T) {
	t.Parallel()
	// Blackbox slot: Inject is nil/empty. Reserved for Recipe-internal
	// plumbing (Helm existingSecret etc.); cannot be referenced from a
	// Workload secrets entry.
	slots := slotsWith(
		struct {
			name string
			slot types.SecretSlot
		}{"opaque-token", types.SecretSlot{Required: true, Inject: nil}},
	)

	w := &types.Workload{}
	c := types.ContainerSpec{Image: "app:1.0"}
	c.Secrets.Set("TOKEN", types.SecretRef{From: "opaque-token", As: "env"})
	w.Containers.Set("main", c)

	err := validator.ValidateWorkloadSecrets(w, slots, "", "", "")
	if !errors.Is(err, validator.ErrWorkloadSecretsSchema) {
		t.Fatalf("expected ErrWorkloadSecretsSchema, got %v", err)
	}
	if !strings.Contains(err.Error(), "blackbox") {
		t.Errorf("error should name the blackbox classification, got: %v", err)
	}
}

func TestValidateWorkloadSecrets_InitContainerWalked(t *testing.T) {
	t.Parallel()
	slots := slotsWith(
		struct {
			name string
			slot types.SecretSlot
		}{"db-password", types.SecretSlot{Inject: []string{"env"}}},
	)

	c := types.ContainerSpec{Image: "migrator:1.0"}
	c.Secrets.Set("DB_PASS", types.SecretRef{From: "not-declared", As: "env"})

	w := &types.Workload{InitContainers: []types.ContainerSpec{c}}

	err := validator.ValidateWorkloadSecrets(w, slots, "", "", "")
	if !errors.Is(err, validator.ErrWorkloadSecretsSchema) {
		t.Fatalf("expected ErrWorkloadSecretsSchema for initContainer ref, got %v", err)
	}
	if !strings.Contains(err.Error(), ".initContainers[0]") {
		t.Errorf("error should reference initContainers[0] path, got: %v", err)
	}
}

func TestValidateWorkloadSecrets_MultipleFindings_AllSurfaced(t *testing.T) {
	t.Parallel()
	slots := slotsWith(
		struct {
			name string
			slot types.SecretSlot
		}{"db-password", types.SecretSlot{Inject: []string{"env"}}},
	)

	c := types.ContainerSpec{Image: "app:1.0"}
	c.Secrets.Set("DB_PASS", types.SecretRef{From: "missing-1", As: "env"})  // undeclared
	c.Secrets.Set("OTHER", types.SecretRef{From: "db-password", As: "file"}) // wrong as

	w := &types.Workload{}
	w.Containers.Set("main", c)

	err := validator.ValidateWorkloadSecrets(w, slots, "", "", "")
	if !errors.Is(err, validator.ErrWorkloadSecretsSchema) {
		t.Fatalf("expected ErrWorkloadSecretsSchema, got %v", err)
	}
	msg := err.Error()
	if !strings.Contains(msg, "missing-1") {
		t.Errorf("first finding should mention missing-1: %v", err)
	}
	if !strings.Contains(msg, `"file"`) {
		t.Errorf("second finding should mention disallowed file mechanism: %v", err)
	}
}

// ----- Error message format -----

func TestValidateWorkloadSecrets_ErrorMessageFormat(t *testing.T) {
	t.Parallel()
	slots := slotsWith(
		struct {
			name string
			slot types.SecretSlot
		}{"db", types.SecretSlot{Inject: []string{"env"}}},
	)
	w := &types.Workload{}
	c := types.ContainerSpec{Image: "app:1.0"}
	c.Secrets.Set("X", types.SecretRef{From: "absent", As: "env"})
	w.Containers.Set("main", c)

	err := validator.ValidateWorkloadSecrets(w, slots, "recipe.yaml", "ops/test", "1.0.0")
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
	if !strings.Contains(msg, "secretsSchema validation failed") {
		t.Errorf("must contain stage tag, got: %q", msg)
	}
}
