// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package helmcore

import (
	"errors"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/rendercore"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// TestSynthesizeWorkload_SuspendOnTaskHalts verifies suspend on a form: task
// workload halts at the shared companion check the synthesis calls at entry,
// rather than rendering a Job that silently drops it.
func TestSynthesizeWorkload_SuspendOnTaskHalts(t *testing.T) {
	t.Parallel()
	w := &types.Workload{APIVersion: "aggrega.ai/v1beta1", Kind: "Workload", Form: "task", Suspend: true}
	w.Containers.Set("main", types.ContainerSpec{Image: "worker:1.0", RestartPolicy: "OnFailure"})

	files, err := SynthesizeWorkloadCore(w, renderer.AttachmentProvidesFromContext(nil), nodeCtxFor("worker"))
	if !errors.Is(err, rendercore.ErrSuspendRequiresScheduledForm()) {
		t.Fatalf("expected ErrSuspendRequiresScheduledForm; got %v", err)
	}
	if files != nil {
		t.Errorf("a refused workload emits nothing; got %d file(s)", len(files))
	}
}
