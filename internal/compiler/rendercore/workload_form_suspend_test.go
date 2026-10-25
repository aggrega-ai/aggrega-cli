// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package rendercore

import (
	"errors"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// TestValidateWorkloadForm_SuspendRequiresScheduled covers the refusing half of
// the suspend rule: suspend on every form other than scheduled — the omitted
// form included, which reads as stateless — halts with
// ErrSuspendRequiresScheduledForm, while an explicit false, the default, draws
// nothing on any form. The scheduled rows in
// TestValidateWorkloadForm_SuspendOnScheduled are this table's positive control.
func TestValidateWorkloadForm_SuspendRequiresScheduled(t *testing.T) {
	t.Parallel()
	for _, form := range []string{"", WorkloadFormStateless, WorkloadFormStateful, WorkloadFormDaemon, WorkloadFormTask} {
		w := &types.Workload{APIVersion: "aggrega.ai/v1alpha1", Kind: "Workload", Form: form, Suspend: true}
		if err := ValidateWorkloadForm(w, "node"); !errors.Is(err, ErrSuspendRequiresScheduledForm()) {
			t.Errorf("form %q with suspend: want ErrSuspendRequiresScheduledForm, got %v", form, err)
		}

		unset := &types.Workload{APIVersion: "aggrega.ai/v1alpha1", Kind: "Workload", Form: form, Suspend: false}
		if err := ValidateWorkloadForm(unset, "node"); err != nil {
			t.Errorf("form %q with suspend false should pass, got %v", form, err)
		}
	}

	// A reference workload carries its endpoint, so the only fault in the row is
	// the suspend.
	ref := &types.Workload{APIVersion: "aggrega.ai/v1alpha1", Kind: "Workload", Form: WorkloadFormReference, Endpoint: "db.example.com", Suspend: true}
	if err := ValidateWorkloadForm(ref, "node"); !errors.Is(err, ErrSuspendRequiresScheduledForm()) {
		t.Errorf("reference form with suspend: want ErrSuspendRequiresScheduledForm, got %v", err)
	}
}
