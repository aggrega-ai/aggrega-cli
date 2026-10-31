// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package composecore

import (
	"bytes"
	"strings"
	"testing"

	abi "github.com/aggrega-ai/aggrega-abi-go/pkg/abi"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/rendercore"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderctx"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// persistentFieldsWorkload builds a Workload whose one volume, "data", is written
// under variant ("persistent" or "persistentTemplate") with the required size and
// the given optional class and access modes, mounted by the main container so the
// separate unmounted advisory never fires. persistentTemplate is a companion of
// form: stateful, so that variant sets the form.
func persistentFieldsWorkload(t *testing.T, variant, class string, accessModes []string) *types.Workload {
	t.Helper()
	w := baseDegradeWorkload()
	withMainContainer(w, func(c *types.ContainerSpec) {
		c.VolumeMounts = []types.VolumeMount{{Name: "data", Path: "/var/data"}}
	})
	switch variant {
	case "persistent":
		w.Volumes.Set("data", types.VolumeSpec{Persistent: &types.PersistentVolume{
			Size: "10Gi", Class: class, AccessModes: accessModes,
		}})
	case "persistentTemplate":
		w.Form = "stateful"
		w.Volumes.Set("data", types.VolumeSpec{PersistentTemplate: &types.PersistentTemplateVolume{
			Size: "10Gi", Class: class, AccessModes: accessModes,
		}})
	default:
		t.Fatalf("unknown volume variant %q", variant)
	}
	return w
}

// persistentFieldDiags returns, in emitted order, the diagnostics whose source
// ref sits under the volume's own variant key — volumes.data.<variant>. — which
// is where every sub-field advisory of that volume is named. The unmounted
// advisory (volumes.data) and the templating advisory (persistentTemplate) sit
// outside that prefix, so neither can be mistaken for a sub-field report.
func persistentFieldDiags(diags []renderctx.Diagnostic, variant string) []renderctx.Diagnostic {
	prefix := "volumes.data." + variant + "."
	var out []renderctx.Diagnostic
	for _, d := range diags {
		if strings.HasPrefix(d.SourceRef, prefix) {
			out = append(out, d)
		}
	}
	return out
}

// TestPersistentVolumeFields_DroppedSubFieldsReported proves the compose target
// reports each optional persistent-volume sub-field it drops — the storage class
// and the access-mode list, on both persistent variants — one warning per
// authored field at its authored coordinate, and reports nothing for a field left
// out. The rows cover every combination of the two fields on each variant, so a
// warn-on-every-volume bug, a one-field-only bug, a wrong-variant coordinate and
// an out-of-order pair each fail a row of their own.
func TestPersistentVolumeFields_DroppedSubFieldsReported(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name        string
		variant     string
		class       string
		accessModes []string
		want        []string
	}{
		{"persistent, class and accessModes", "persistent", "fast-ssd", []string{"ReadWriteOncePod"},
			[]string{"volumes.data.persistent.class", "volumes.data.persistent.accessModes"}},
		{"persistent, class only", "persistent", "fast-ssd", nil,
			[]string{"volumes.data.persistent.class"}},
		{"persistent, accessModes only", "persistent", "", []string{"ReadWriteMany"},
			[]string{"volumes.data.persistent.accessModes"}},
		{"persistent, neither", "persistent", "", nil, nil},
		{"persistentTemplate, class and accessModes", "persistentTemplate", "fast-ssd", []string{"ReadWriteOnce"},
			[]string{"volumes.data.persistentTemplate.class", "volumes.data.persistentTemplate.accessModes"}},
		{"persistentTemplate, class only", "persistentTemplate", "fast-ssd", nil,
			[]string{"volumes.data.persistentTemplate.class"}},
		{"persistentTemplate, accessModes only", "persistentTemplate", "", []string{"ReadWriteOnce"},
			[]string{"volumes.data.persistentTemplate.accessModes"}},
		{"persistentTemplate, neither", "persistentTemplate", "", nil, nil},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, diags, err := SynthesizeWorkloadCore(
				persistentFieldsWorkload(t, tc.variant, tc.class, tc.accessModes), nodeCtxFor("store"))
			if err != nil {
				t.Fatalf("SynthesizeWorkloadCore: %v", err)
			}
			got := persistentFieldDiags(diags, tc.variant)
			if len(got) != len(tc.want) {
				t.Fatalf("got %d sub-field advisories, want %d %v (all: %+v)", len(got), len(tc.want), tc.want, diags)
			}
			for i, d := range got {
				coordinate := tc.want[i]
				if d.SourceRef != coordinate {
					t.Errorf("advisory %d names %q, want %q", i, d.SourceRef, coordinate)
				}
				if d.Severity != abi.SeverityWarn {
					t.Errorf("%s: severity = %q, want warn", coordinate, d.Severity)
				}
				if !strings.Contains(d.Message, coordinate) {
					t.Errorf("%s: message does not name the authored coordinate: %q", coordinate, d.Message)
				}
				if !strings.Contains(d.Message, `"compose"`) {
					t.Errorf("%s: message does not name the target: %q", coordinate, d.Message)
				}
				// Full omission, not a reduced form: nothing of the value reaches the output.
				if strings.Contains(d.Message, "does not fully support") {
					t.Errorf("%s: message uses the partial-support wording; the value is dropped outright: %q",
						coordinate, d.Message)
				}
			}
		})
	}
}

// TestPersistentVolumeFields_CarryTheSuppressibleClass proves the sub-field
// advisories are silenced by the same filter as the rest of the unsupported-field
// class. The suppression keys on the diagnostic's own code, so the property that
// decides it is that each one's code is byte-equal to the code the class's own
// constructor produces — compared against that constructor rather than a literal,
// so repointing the advisory at another class fails here.
func TestPersistentVolumeFields_CarryTheSuppressibleClass(t *testing.T) {
	t.Parallel()
	classCode := rendercore.UnsupportedFieldDiagnostic(composeTargetName, "probe").Code
	if classCode == "" {
		t.Fatal("the class constructor produced an empty code; nothing for a filter to key on")
	}
	for _, variant := range []string{"persistent", "persistentTemplate"} {
		variant := variant
		t.Run(variant, func(t *testing.T) {
			t.Parallel()
			_, diags, err := SynthesizeWorkloadCore(
				persistentFieldsWorkload(t, variant, "fast-ssd", []string{"ReadWriteOnce"}), nodeCtxFor("store"))
			if err != nil {
				t.Fatalf("SynthesizeWorkloadCore: %v", err)
			}
			got := persistentFieldDiags(diags, variant)
			if len(got) != 2 {
				t.Fatalf("got %d sub-field advisories, want 2 (all: %+v)", len(got), diags)
			}
			for _, d := range got {
				if d.Code != classCode {
					t.Errorf("%s: code = %q, want the class code %q the suppression filter keys on", d.SourceRef, d.Code, classCode)
				}
			}
		})
	}
}

// TestPersistentVolumeFields_OutOfBand proves the advisories change no emitted
// byte: a volume written with a class and access modes renders exactly as the same
// volume written without them, on both variants, so what the author is told was
// dropped is in fact absent from the output and nothing else moved.
func TestPersistentVolumeFields_OutOfBand(t *testing.T) {
	t.Parallel()
	for _, variant := range []string{"persistent", "persistentTemplate"} {
		variant := variant
		t.Run(variant, func(t *testing.T) {
			t.Parallel()
			bareFiles, _, err := SynthesizeWorkloadCore(persistentFieldsWorkload(t, variant, "", nil), nodeCtxFor("store"))
			if err != nil {
				t.Fatalf("synth without class and accessModes: %v", err)
			}
			fullFiles, fullDiags, err := SynthesizeWorkloadCore(
				persistentFieldsWorkload(t, variant, "fast-ssd", []string{"ReadWriteOncePod"}), nodeCtxFor("store"))
			if err != nil {
				t.Fatalf("synth with class and accessModes: %v", err)
			}
			if len(persistentFieldDiags(fullDiags, variant)) == 0 {
				t.Fatal("expected advisories for the authored class and accessModes, got none")
			}
			if len(bareFiles) != len(fullFiles) {
				t.Fatalf("emitted %d files without the fields and %d with them", len(bareFiles), len(fullFiles))
			}
			for i := range bareFiles {
				if !bytes.Equal(bareFiles[i].Body, fullFiles[i].Body) {
					t.Errorf("authored class and accessModes changed %s (must be out-of-band)\n--- without ---\n%s\n--- with ---\n%s",
						bareFiles[i].Name, bareFiles[i].Body, fullFiles[i].Body)
				}
			}
		})
	}
}
