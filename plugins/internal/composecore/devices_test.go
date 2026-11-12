// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package composecore

import (
	"reflect"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

// fragmentWithDevices is a contributor's compose fragment whose service reserves
// devices in the forms the synthesizer never writes itself: a count of all, a
// device_ids list, an extra capability and driver options. It is authored as text
// so the test exercises the parse path a sourced compose file or an artifacts:
// body arrives through.
const fragmentWithDevices = `services:
  trainer:
    image: trainer:1.0
    deploy:
      resources:
        reservations:
          memory: "1073741824"
          devices:
            - driver: nvidia
              count: all
              capabilities:
                - gpu
                - utility
            - driver: nvidia
              device_ids:
                - "0"
                - "3"
              capabilities:
                - gpu
              options:
                virtualization: "false"
`

// TestAssembleBoundary_CarriesAFragmentsDeviceEntriesAsWritten proves the merge
// assembly writes a fragment's reservation devices as the fragment wrote them,
// the way it writes the cpus and memory beside them: every entry, in order, with
// a count of all kept as the word, a device_ids list kept, and the options
// carried. A device entry the model could not hold would be dropped here without
// a sound, which is the failure this test exists to catch.
func TestAssembleBoundary_CarriesAFragmentsDeviceEntriesAsWritten(t *testing.T) {
	t.Parallel()
	assembled, err := assembleOne(t, modelFragment("alpha", fragmentWithDevices))
	if err != nil {
		t.Fatalf("assemble: %v", err)
	}

	var out ComposeFile
	if err := yaml.Unmarshal([]byte(assembled), &out); err != nil {
		t.Fatalf("parse assembled file: %v\n%s", err, assembled)
	}
	svc := out.Services["trainer"]
	if svc.Deploy == nil || svc.Deploy.Resources == nil || svc.Deploy.Resources.Reservations == nil {
		t.Fatalf("the reservations block did not survive the merge:\n%s", assembled)
	}
	res := svc.Deploy.Resources.Reservations
	if res.Memory != "1073741824" {
		t.Errorf("reservations memory = %q, want the fragment's 1073741824", res.Memory)
	}
	want := []DeviceRequest{
		{Driver: "nvidia", Count: "all", Capabilities: []string{"gpu", "utility"}},
		{
			Driver:       "nvidia",
			DeviceIDs:    []string{"0", "3"},
			Capabilities: []string{"gpu"},
			Options:      map[string]any{"virtualization": "false"},
		},
	}
	if !reflect.DeepEqual(res.Devices, want) {
		t.Errorf("devices = %#v\nwant %#v\nassembled:\n%s", res.Devices, want, assembled)
	}
	if !strings.Contains(assembled, "count: all") {
		t.Errorf("count: all did not reach the assembled file as the word:\n%s", assembled)
	}
}
