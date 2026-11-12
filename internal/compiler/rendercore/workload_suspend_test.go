// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package rendercore

import (
	"encoding/json"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// TestWorkloadProjection_SuspendRidesABI proves Workload.Suspend survives both
// encodings of the host→guest projection. Each encodes the Workload with
// json.Marshal and decodes it into types.Workload, so the field crosses with no
// codec change of its own; this exercises both round trips rather than
// asserting it, and the false row proves an unsuspended workload decodes as one
// that never carried the field.
func TestWorkloadProjection_SuspendRidesABI(t *testing.T) {
	t.Parallel()
	for _, suspend := range []bool{true, false} {
		w := types.Workload{APIVersion: "aggrega.ai/v1beta1", Kind: "Workload", Form: WorkloadFormScheduled, Schedule: "@daily", Suspend: suspend}
		w.Containers.Set("main", types.ContainerSpec{Image: "app:1.0"})
		proj := WorkloadProjection{Workload: w}

		enc, err := json.Marshal(proj)
		if err != nil {
			t.Fatalf("json marshal (suspend=%v): %v", suspend, err)
		}
		var viaJSON WorkloadProjection
		if err := json.Unmarshal(enc, &viaJSON); err != nil {
			t.Fatalf("json unmarshal (suspend=%v): %v", suspend, err)
		}
		if viaJSON.Workload.Suspend != suspend {
			t.Errorf("Suspend did not survive the JSON projection round trip: got %v, want %v (wire: %s)", viaJSON.Workload.Suspend, suspend, enc)
		}

		cb, err := proj.EncodeCBOR()
		if err != nil {
			t.Fatalf("cbor encode (suspend=%v): %v", suspend, err)
		}
		var viaCBOR WorkloadProjection
		if err := viaCBOR.DecodeCBOR(cb); err != nil {
			t.Fatalf("cbor decode (suspend=%v): %v", suspend, err)
		}
		if viaCBOR.Workload.Suspend != suspend {
			t.Errorf("Suspend did not survive the CBOR projection round trip: got %v, want %v", viaCBOR.Workload.Suspend, suspend)
		}
	}
}

// TestValidateWorkloadForm_SuspendOnScheduled covers the accepting half of the
// suspend rule: a scheduled workload passes the shared form check whether it is
// suspended or not.
func TestValidateWorkloadForm_SuspendOnScheduled(t *testing.T) {
	t.Parallel()
	for _, suspend := range []bool{true, false} {
		w := &types.Workload{APIVersion: "aggrega.ai/v1beta1", Kind: "Workload", Form: WorkloadFormScheduled, Schedule: "0 3 * * *", Suspend: suspend}
		if err := ValidateWorkloadForm(w, "node"); err != nil {
			t.Errorf("scheduled with suspend=%v should pass, got %v", suspend, err)
		}
	}
}
