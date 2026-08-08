// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package validator_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"github.com/aggrega-ai/aggrega-cli/pkg/validator"
)

func slotsWith(entries ...struct {
	name string
	slot types.AttachmentSlot
}) types.OrderedMap[types.AttachmentSlot] {
	out := types.OrderedMap[types.AttachmentSlot]{}
	for _, e := range entries {
		out.Set(e.name, e.slot)
	}
	return out
}

// ----- Pass cases -----

func TestValidateWorkloadAttachments_AllowlistedSlot_Passes(t *testing.T) {
	t.Parallel()
	slots := slotsWith(
		struct {
			name string
			slot types.AttachmentSlot
		}{"db-password", types.AttachmentSlot{Required: true, Inject: []string{"env", "file"}}},
	)

	w := &types.Workload{}
	c := types.ContainerSpec{Image: "app:1.0"}
	c.Attachments.Set("DB_PASS", types.AttachmentRef{From: "db-password", As: "env"})
	w.Containers.Set("main", c)

	if err := validator.ValidateWorkloadAttachments(w, slots, "", "", ""); err != nil {
		t.Fatalf("expected pass, got %v", err)
	}
}

func TestValidateWorkloadAttachments_FileDelivery_Passes(t *testing.T) {
	t.Parallel()
	slots := slotsWith(
		struct {
			name string
			slot types.AttachmentSlot
		}{"tls-cert", types.AttachmentSlot{Required: true, Inject: []string{"file"}}},
	)

	w := &types.Workload{}
	c := types.ContainerSpec{Image: "app:1.0"}
	c.Attachments.Set("TLS", types.AttachmentRef{From: "tls-cert", As: "file", Path: "/etc/tls/cert.pem"})
	w.Containers.Set("main", c)

	if err := validator.ValidateWorkloadAttachments(w, slots, "", "", ""); err != nil {
		t.Fatalf("expected pass, got %v", err)
	}
}

func TestValidateWorkloadAttachments_NoSecrets_Passes(t *testing.T) {
	t.Parallel()
	slots := types.OrderedMap[types.AttachmentSlot]{}
	w := &types.Workload{}
	w.Containers.Set("main", types.ContainerSpec{Image: "app:1.0"})

	if err := validator.ValidateWorkloadAttachments(w, slots, "", "", ""); err != nil {
		t.Errorf("workload with no secrets should pass, got %v", err)
	}
}

func TestValidateWorkloadAttachments_NilWorkload_NoError(t *testing.T) {
	t.Parallel()
	slots := types.OrderedMap[types.AttachmentSlot]{}
	if err := validator.ValidateWorkloadAttachments(nil, slots, "", "", ""); err != nil {
		t.Errorf("nil Workload should pass, got %v", err)
	}
}

// ----- Fail cases -----

func TestValidateWorkloadAttachments_UndeclaredSlot_Fails(t *testing.T) {
	t.Parallel()
	slots := slotsWith(
		struct {
			name string
			slot types.AttachmentSlot
		}{"db-password", types.AttachmentSlot{Inject: []string{"env"}}},
	)

	w := &types.Workload{}
	c := types.ContainerSpec{Image: "app:1.0"}
	c.Attachments.Set("OTHER", types.AttachmentRef{From: "not-declared", As: "env"})
	w.Containers.Set("main", c)

	err := validator.ValidateWorkloadAttachments(w, slots, "", "", "")
	if !errors.Is(err, validator.ErrWorkloadFeaturesSchema()) {
		t.Fatalf("expected ErrWorkloadFeaturesSchema, got %v", err)
	}
	if !strings.Contains(err.Error(), "not-declared") {
		t.Errorf("error should name the undeclared slot: %v", err)
	}
}

func TestValidateWorkloadAttachments_AsNotInAllowlist_Fails(t *testing.T) {
	t.Parallel()
	slots := slotsWith(
		struct {
			name string
			slot types.AttachmentSlot
		}{"db-password", types.AttachmentSlot{Inject: []string{"env"}}}, // file NOT in allowlist
	)

	w := &types.Workload{}
	c := types.ContainerSpec{Image: "app:1.0"}
	c.Attachments.Set("DB_PASS", types.AttachmentRef{From: "db-password", As: "file", Path: "/etc/db"})
	w.Containers.Set("main", c)

	err := validator.ValidateWorkloadAttachments(w, slots, "", "", "")
	if !errors.Is(err, validator.ErrWorkloadFeaturesSchema()) {
		t.Fatalf("expected ErrWorkloadFeaturesSchema, got %v", err)
	}
	if !strings.Contains(err.Error(), `"file"`) {
		t.Errorf("error should name the disallowed mechanism, got: %v", err)
	}
}

func TestValidateWorkloadAttachments_BlackboxSlot_Fails(t *testing.T) {
	t.Parallel()
	// Blackbox slot: Inject is nil/empty. Reserved for Recipe-internal
	// plumbing (Helm existingSecret etc.); cannot be referenced from a
	// Workload secrets entry.
	slots := slotsWith(
		struct {
			name string
			slot types.AttachmentSlot
		}{"opaque-token", types.AttachmentSlot{Required: true, Inject: nil}},
	)

	w := &types.Workload{}
	c := types.ContainerSpec{Image: "app:1.0"}
	c.Attachments.Set("TOKEN", types.AttachmentRef{From: "opaque-token", As: "env"})
	w.Containers.Set("main", c)

	err := validator.ValidateWorkloadAttachments(w, slots, "", "", "")
	if !errors.Is(err, validator.ErrWorkloadFeaturesSchema()) {
		t.Fatalf("expected ErrWorkloadFeaturesSchema, got %v", err)
	}
	if !strings.Contains(err.Error(), "blackbox") {
		t.Errorf("error should name the blackbox classification, got: %v", err)
	}
}

func TestValidateWorkloadAttachments_InitContainerWalked(t *testing.T) {
	t.Parallel()
	slots := slotsWith(
		struct {
			name string
			slot types.AttachmentSlot
		}{"db-password", types.AttachmentSlot{Inject: []string{"env"}}},
	)

	c := types.ContainerSpec{Image: "migrator:1.0"}
	c.Attachments.Set("DB_PASS", types.AttachmentRef{From: "not-declared", As: "env"})

	w := &types.Workload{InitContainers: []types.ContainerSpec{c}}

	err := validator.ValidateWorkloadAttachments(w, slots, "", "", "")
	if !errors.Is(err, validator.ErrWorkloadFeaturesSchema()) {
		t.Fatalf("expected ErrWorkloadFeaturesSchema for initContainer ref, got %v", err)
	}
	if !strings.Contains(err.Error(), ".initContainers[0]") {
		t.Errorf("error should reference initContainers[0] path, got: %v", err)
	}
}

func TestValidateWorkloadAttachments_MultipleFindings_AllSurfaced(t *testing.T) {
	t.Parallel()
	slots := slotsWith(
		struct {
			name string
			slot types.AttachmentSlot
		}{"db-password", types.AttachmentSlot{Inject: []string{"env"}}},
	)

	c := types.ContainerSpec{Image: "app:1.0"}
	c.Attachments.Set("DB_PASS", types.AttachmentRef{From: "missing-1", As: "env"})  // undeclared
	c.Attachments.Set("OTHER", types.AttachmentRef{From: "db-password", As: "file"}) // wrong as

	w := &types.Workload{}
	w.Containers.Set("main", c)

	err := validator.ValidateWorkloadAttachments(w, slots, "", "", "")
	if !errors.Is(err, validator.ErrWorkloadFeaturesSchema()) {
		t.Fatalf("expected ErrWorkloadFeaturesSchema, got %v", err)
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

func TestValidateWorkloadAttachments_ErrorMessageFormat(t *testing.T) {
	t.Parallel()
	slots := slotsWith(
		struct {
			name string
			slot types.AttachmentSlot
		}{"db", types.AttachmentSlot{Inject: []string{"env"}}},
	)
	w := &types.Workload{}
	c := types.ContainerSpec{Image: "app:1.0"}
	c.Attachments.Set("X", types.AttachmentRef{From: "absent", As: "env"})
	w.Containers.Set("main", c)

	err := validator.ValidateWorkloadAttachments(w, slots, "recipe.yaml", "test", "1.0.0")
	if err == nil {
		t.Fatal("expected error")
	}
	msg := err.Error()
	if !strings.HasPrefix(msg, "CATALOG_ERROR: Workload") {
		t.Errorf("must start with CATALOG_ERROR prefix, got: %q", msg)
	}
	if !strings.Contains(msg, "test:1.0.0") {
		t.Errorf("must contain recipe name:version, got: %q", msg)
	}
	if !strings.Contains(msg, "featuresSchema validation failed") {
		t.Errorf("must contain stage tag, got: %q", msg)
	}
}
