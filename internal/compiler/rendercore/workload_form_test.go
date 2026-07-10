// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package rendercore

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// TestWorkloadProjection_FormRidesABI proves the Workload.Form field survives
// the host→guest projection codec round-trip. The projection encodes the
// Workload via json.Marshal and decodes it via json.Decode into types.Workload,
// so a plain json-tagged field rides free with no codec change; this exercises
// that path end-to-end rather than asserting it.
func TestWorkloadProjection_FormRidesABI(t *testing.T) {
	t.Parallel()
	for _, form := range []string{
		WorkloadFormStateless,
		WorkloadFormStateful,
		WorkloadFormDaemon,
		WorkloadFormScheduled,
		WorkloadFormTask,
	} {
		w := types.Workload{APIVersion: "aggrega.ai/v1alpha1", Kind: "Workload", Form: form}
		c := types.ContainerSpec{Image: "app:1.0"}
		w.Containers.Set("main", c)

		proj := WorkloadProjection{Workload: w}
		enc, err := json.Marshal(proj)
		if err != nil {
			t.Fatalf("marshal (%s): %v", form, err)
		}

		var back WorkloadProjection
		if err := json.Unmarshal(enc, &back); err != nil {
			t.Fatalf("unmarshal (%s): %v", form, err)
		}
		if back.Workload.Form != form {
			t.Errorf("Form did not survive projection round-trip: got %q, want %q (wire: %s)", back.Workload.Form, form, enc)
		}
	}
}

// TestValidateWorkloadForm covers the companion constraint: a persistentTemplate
// volume requires form: stateful; every other form (including empty) halts.
func TestValidateWorkloadForm(t *testing.T) {
	t.Parallel()

	withPersistentTemplate := func(form string) *types.Workload {
		w := &types.Workload{APIVersion: "aggrega.ai/v1alpha1", Kind: "Workload", Form: form}
		w.Volumes.Set("data", types.VolumeSpec{PersistentTemplate: &types.PersistentTemplateVolume{Size: "1Gi"}})
		return w
	}

	// stateful + persistentTemplate → allowed.
	if err := ValidateWorkloadForm(withPersistentTemplate(WorkloadFormStateful), "node"); err != nil {
		t.Errorf("stateful with persistentTemplate should pass, got %v", err)
	}

	// non-stateful (and empty) + persistentTemplate → halt.
	for _, form := range []string{"", WorkloadFormStateless, WorkloadFormDaemon, WorkloadFormTask} {
		if err := ValidateWorkloadForm(withPersistentTemplate(form), "node"); !errors.Is(err, ErrPersistentTemplateRequiresStatefulForm) {
			t.Errorf("form %q with persistentTemplate: want ErrPersistentTemplateRequiresStatefulForm, got %v", form, err)
		}
	}

	// stateless with no persistentTemplate → allowed.
	plain := &types.Workload{APIVersion: "aggrega.ai/v1alpha1", Kind: "Workload"}
	plain.Volumes.Set("data", types.VolumeSpec{Persistent: &types.PersistentVolume{Size: "1Gi"}})
	if err := ValidateWorkloadForm(plain, "node"); err != nil {
		t.Errorf("stateless with plain persistent volume should pass, got %v", err)
	}
}

// TestValidateWorkloadForm_Schedule covers the scheduled-form companion rules:
// form: scheduled requires a valid schedule; a schedule requires form:
// scheduled; a malformed schedule on form: scheduled halts.
func TestValidateWorkloadForm_Schedule(t *testing.T) {
	t.Parallel()

	scheduled := func(schedule string) *types.Workload {
		return &types.Workload{APIVersion: "aggrega.ai/v1alpha1", Kind: "Workload", Form: WorkloadFormScheduled, Schedule: schedule}
	}

	// scheduled + valid macro → allowed.
	for _, macro := range []string{"@hourly", "@daily", "@weekly", "@monthly", "@yearly"} {
		if err := ValidateWorkloadForm(scheduled(macro), "node"); err != nil {
			t.Errorf("scheduled with macro %q should pass, got %v", macro, err)
		}
	}

	// scheduled + valid 5-field expression → allowed.
	for _, expr := range []string{"0 0 * * *", "*/5 * * * *", "0 9-17 * * 1-5", "15,45 * * * *"} {
		if err := ValidateWorkloadForm(scheduled(expr), "node"); err != nil {
			t.Errorf("scheduled with expression %q should pass, got %v", expr, err)
		}
	}

	// scheduled + empty schedule → ErrScheduledFormRequiresSchedule.
	if err := ValidateWorkloadForm(scheduled(""), "node"); !errors.Is(err, ErrScheduledFormRequiresSchedule) {
		t.Errorf("scheduled with no schedule: want ErrScheduledFormRequiresSchedule, got %v", err)
	}

	// schedule set on a non-scheduled form → ErrScheduleRequiresScheduledForm.
	for _, form := range []string{"", WorkloadFormStateless, WorkloadFormStateful, WorkloadFormDaemon, WorkloadFormTask} {
		w := &types.Workload{APIVersion: "aggrega.ai/v1alpha1", Kind: "Workload", Form: form, Schedule: "@daily"}
		if err := ValidateWorkloadForm(w, "node"); !errors.Is(err, ErrScheduleRequiresScheduledForm) {
			t.Errorf("form %q with a schedule: want ErrScheduleRequiresScheduledForm, got %v", form, err)
		}
	}

	// scheduled + malformed schedule → ErrInvalidCronSchedule.
	for _, bad := range []string{
		"0 0 * *",           // 4 fields
		"0 0 * * * *",       // 6 fields (Quartz seconds)
		"0 0 * * ?",         // illegal char '?'
		"@annually",         // unrecognized macro
		"@reboot",           // unrecognized macro
		"@midnight",         // unrecognized macro
		"@every 5m",         // unrecognized macro form
		"abc def ghi jk lm", // alphabetic fields
	} {
		if err := ValidateWorkloadForm(scheduled(bad), "node"); !errors.Is(err, ErrInvalidCronSchedule) {
			t.Errorf("scheduled with malformed schedule %q: want ErrInvalidCronSchedule, got %v", bad, err)
		}
	}
}
