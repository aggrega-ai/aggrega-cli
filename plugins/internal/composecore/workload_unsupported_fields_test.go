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

// baseDegradeWorkload is a minimal stateless single-container Workload with none
// of the compose-unsupported fields set — the zero baseline the per-field
// unsupported-field cases mutate.
func baseDegradeWorkload() *types.Workload {
	w := &types.Workload{APIVersion: "aggrega.ai/v1alpha1", Kind: "Workload"}
	c := types.ContainerSpec{Image: "app:1.0"}
	c.Ports.Set("http", types.PortSpec{Port: 8080})
	w.Containers.Set("main", c)
	svc := types.ServiceSpec{Type: "ClusterIP"}
	svc.Ports.Set("http", 8080)
	w.Services.Set("api", svc)
	return w
}

// withMainContainer mutates the "main" container in place (OrderedMap stores by
// value, so it is read, mutated, and written back).
func withMainContainer(w *types.Workload, mut func(c *types.ContainerSpec)) {
	c, _ := w.Containers.Get("main")
	mut(&c)
	w.Containers.Set("main", c)
}

func ptrBool(b bool) *bool { return &b }
func ptrInt(i int) *int    { return &i }

// TestUnsupportedField_PerAuthoredField proves each compose-unsupported authored
// field emits exactly one warning diagnostic with the stable class code, the
// field's authored name as the source ref, and the field name in the message —
// and that leaving the field unset emits none for it.
func TestUnsupportedField_PerAuthoredField(t *testing.T) {
	t.Parallel()
	cases := []struct {
		field string
		set   func(w *types.Workload)
	}{
		{"scheduling", func(w *types.Workload) {
			w.Scheduling = &types.Scheduling{NodeSelector: map[string]string{"disktype": "ssd"}}
		}},
		{"serviceAccount", func(w *types.Workload) { w.ServiceAccount = "deployer" }},
		{"replicas", func(w *types.Workload) { w.Replicas = ptrInt(3) }},
		{"runtimeClassName", func(w *types.Workload) { w.RuntimeClassName = "nvidia" }},
		{"priorityClassName", func(w *types.Workload) { w.PriorityClassName = "high" }},
		{"imagePullSecrets", func(w *types.Workload) { w.ImagePullSecrets = []string{"regcred"} }},
		{"dnsPolicy", func(w *types.Workload) { w.DNSPolicy = "ClusterFirst" }},
		{"dnsConfig.options", func(w *types.Workload) {
			w.DNSConfig = &types.DNSConfig{Options: []any{map[string]any{"name": "ndots"}}}
		}},
		{"runAsNonRoot", func(w *types.Workload) {
			withMainContainer(w, func(c *types.ContainerSpec) {
				c.SecurityContext = &types.ContainerSecurityContext{RunAsNonRoot: ptrBool(true)}
			})
		}},
		{"nvidia.com/gpu", func(w *types.Workload) {
			withMainContainer(w, func(c *types.ContainerSpec) {
				limits := &types.ResourceList{}
				limits.Extended.Set("nvidia.com/gpu", "1")
				c.Resources = &types.ResourceSpec{Limits: limits}
			})
		}},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.field, func(t *testing.T) {
			t.Parallel()

			// Unset baseline: no diagnostic for this field.
			_, baseDiags, err := SynthesizeWorkloadCore(baseDegradeWorkload(), nodeCtxFor("api"))
			if err != nil {
				t.Fatalf("baseline synth: %v", err)
			}
			for _, d := range baseDiags {
				if d.SourceRef == tc.field {
					t.Fatalf("unset %s produced a diagnostic: %+v", tc.field, d)
				}
			}

			// Field set: exactly one diagnostic for it.
			w := baseDegradeWorkload()
			tc.set(w)
			_, diags, err := SynthesizeWorkloadCore(w, nodeCtxFor("api"))
			if err != nil {
				t.Fatalf("synth with %s: %v", tc.field, err)
			}
			var got []string
			for _, d := range diags {
				if d.SourceRef != tc.field {
					continue
				}
				got = append(got, d.SourceRef)
				if d.Severity != abi.SeverityWarn {
					t.Errorf("%s: severity = %q, want warn", tc.field, d.Severity)
				}
				if d.Code != rendercore.UnsupportedFieldCode {
					t.Errorf("%s: code = %q, want %q", tc.field, d.Code, rendercore.UnsupportedFieldCode)
				}
				if !strings.Contains(d.Message, tc.field) {
					t.Errorf("%s: message %q does not name the field", tc.field, d.Message)
				}
				if !strings.Contains(d.Message, "compose") {
					t.Errorf("%s: message %q does not name the target", tc.field, d.Message)
				}
			}
			if len(got) != 1 {
				t.Fatalf("%s: got %d diagnostics for the field, want 1 (all: %+v)", tc.field, len(got), diags)
			}
		})
	}
}

// TestUnsupportedField_ReplicasWarnsOnAnAuthoredDefaultValue proves the warning
// reports that replicas was authored, not that its value differs from the default.
// Replicas defaults to 1, so an explicit `replicas: 1` asks for exactly the
// deployment an omitted field already describes — yet only the authored one warns,
// because the author wrote a field the compose target cannot honor and can choose
// to remove.
//
// The pairing is what pins it. The authored row alone would pass equally against a
// warning that compared the value with the default, and the omitted row alone
// proves only that silence is reachable. Together they fix presence as the property
// that decides, which is what the field's published description states.
func TestUnsupportedField_ReplicasWarnsOnAnAuthoredDefaultValue(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name     string
		replicas *int
		want     int
	}{
		{"omitted", nil, 0},
		{"authored default value", ptrInt(1), 1},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			w := baseDegradeWorkload()
			w.Replicas = tc.replicas

			_, diags, err := SynthesizeWorkloadCore(w, nodeCtxFor("api"))
			if err != nil {
				t.Fatalf("synth: %v", err)
			}
			var got []renderctx.Diagnostic
			for _, d := range diags {
				if d.SourceRef == "replicas" {
					got = append(got, d)
				}
			}
			if len(got) != tc.want {
				t.Fatalf("got %d replicas diagnostics, want %d (all: %+v)", len(got), tc.want, diags)
			}
			for _, d := range got {
				if d.Severity != abi.SeverityWarn {
					t.Errorf("severity = %q, want warn", d.Severity)
				}
				if d.Code != rendercore.UnsupportedFieldCode {
					t.Errorf("code = %q, want %q", d.Code, rendercore.UnsupportedFieldCode)
				}
				if !strings.Contains(d.Message, "replicas") {
					t.Errorf("message %q does not name the field", d.Message)
				}
			}
		})
	}
}

// TestUnsupportedField_OutOfBand proves a diagnostic-producing field does not change
// the rendered fragment bytes — the Invariant-8 out-of-band guarantee.
func TestUnsupportedField_OutOfBand(t *testing.T) {
	t.Parallel()
	base := baseDegradeWorkload()
	baseFiles, baseDiags, err := SynthesizeWorkloadCore(base, nodeCtxFor("api"))
	if err != nil {
		t.Fatalf("baseline synth: %v", err)
	}
	if len(baseDiags) != 0 {
		t.Fatalf("baseline produced diagnostics: %+v", baseDiags)
	}

	withFields := baseDegradeWorkload()
	withFields.Scheduling = &types.Scheduling{NodeSelector: map[string]string{"disktype": "ssd"}}
	withFields.ServiceAccount = "deployer"
	withFields.RuntimeClassName = "nvidia"
	wfFiles, wfDiags, err := SynthesizeWorkloadCore(withFields, nodeCtxFor("api"))
	if err != nil {
		t.Fatalf("synth with fields: %v", err)
	}
	if len(wfDiags) == 0 {
		t.Fatal("expected unsupported-field diagnostics, got none")
	}
	if !bytes.Equal(baseFiles[0].Body, wfFiles[0].Body) {
		t.Errorf("degraded fields changed rendered bytes (must be out-of-band)\n--- base ---\n%s\n--- with fields ---\n%s",
			baseFiles[0].Body, wfFiles[0].Body)
	}
}

// TestUnsupportedField_MultiFieldDeterministic proves a Workload authoring several
// unsupported fields emits one warning each in a deterministic order across runs.
func TestUnsupportedField_MultiFieldDeterministic(t *testing.T) {
	t.Parallel()
	build := func() *types.Workload {
		w := baseDegradeWorkload()
		w.Scheduling = &types.Scheduling{NodeSelector: map[string]string{"disktype": "ssd"}}
		w.ServiceAccount = "deployer"
		w.Replicas = ptrInt(3)
		w.RuntimeClassName = "nvidia"
		w.PriorityClassName = "high"
		withMainContainer(w, func(c *types.ContainerSpec) {
			c.SecurityContext = &types.ContainerSecurityContext{RunAsNonRoot: ptrBool(true)}
			limits := &types.ResourceList{}
			limits.Extended.Set("nvidia.com/gpu", "1")
			c.Resources = &types.ResourceSpec{Limits: limits}
		})
		return w
	}
	_, first, err := SynthesizeWorkloadCore(build(), nodeCtxFor("api"))
	if err != nil {
		t.Fatalf("first synth: %v", err)
	}
	_, second, err := SynthesizeWorkloadCore(build(), nodeCtxFor("api"))
	if err != nil {
		t.Fatalf("second synth: %v", err)
	}
	if len(first) != len(second) {
		t.Fatalf("diagnostic count differs: %d vs %d", len(first), len(second))
	}
	for i := range first {
		if first[i] != second[i] {
			t.Errorf("diagnostic %d differs across runs: %+v vs %+v", i, first[i], second[i])
		}
	}
	// The GPU key is container-scoped and emitted before pod-level fields.
	if first[0].SourceRef != "nvidia.com/gpu" {
		t.Errorf("expected container-scoped nvidia.com/gpu first, got %q", first[0].SourceRef)
	}
}

// TestUnsupportedField_PersistentTemplatePartial proves persistentTemplate emits a
// partial-support warning (worded "does not fully support") sharing the same
// class code, distinct from the full-omission wording.
func TestUnsupportedField_PersistentTemplatePartial(t *testing.T) {
	t.Parallel()
	w := &types.Workload{APIVersion: "aggrega.ai/v1alpha1", Kind: "Workload", Form: "stateful"}
	c := types.ContainerSpec{Image: "store:1.0"}
	c.Ports.Set("api", types.PortSpec{Port: 9000})
	c.VolumeMounts = []types.VolumeMount{{Name: "session", Path: "/var/session"}}
	w.Containers.Set("main", c)
	w.Volumes.Set("session", types.VolumeSpec{PersistentTemplate: &types.PersistentTemplateVolume{Size: "1Gi"}})
	svc := types.ServiceSpec{Type: "ClusterIP"}
	svc.Ports.Set("api", 9000)
	w.Services.Set("internal", svc)

	_, diags, err := SynthesizeWorkloadCore(w, nodeCtxFor("store"))
	if err != nil {
		t.Fatalf("synth: %v", err)
	}
	var found bool
	for _, d := range diags {
		if d.SourceRef != "persistentTemplate" {
			continue
		}
		found = true
		if d.Code != rendercore.UnsupportedFieldCode {
			t.Errorf("code = %q, want %q", d.Code, rendercore.UnsupportedFieldCode)
		}
		if !strings.Contains(d.Message, "does not fully support") {
			t.Errorf("partial-support message %q missing the partial wording", d.Message)
		}
	}
	if !found {
		t.Fatalf("no persistentTemplate diagnostic (all: %+v)", diags)
	}
}

// ephemeralSizeLimitWorkload builds a Workload whose one volume is an ephemeral
// volume, with sizeLimit set to limit and omitted when limit is empty. The main
// container mounts it by default; mountedEphemeralSizeLimitWorkload's false
// variant is the unmounted shape.
func ephemeralSizeLimitWorkload(limit string) *types.Workload {
	return mountedEphemeralSizeLimitWorkload(limit, true)
}

// mountedEphemeralSizeLimitWorkload is ephemeralSizeLimitWorkload with the mount
// made explicit, so a case can cover the declared-but-unmounted shape. The size a
// target cannot carry is dropped either way, so the two shapes must agree about
// the sizeLimit warning and differ only in the separate unmounted advisory.
func mountedEphemeralSizeLimitWorkload(limit string, mounted bool) *types.Workload {
	w := baseDegradeWorkload()
	if mounted {
		withMainContainer(w, func(c *types.ContainerSpec) {
			c.VolumeMounts = []types.VolumeMount{{Name: "scratch", Path: "/tmp/scratch"}}
		})
	}
	w.Volumes.Set("scratch", types.VolumeSpec{Ephemeral: &types.EphemeralVolume{SizeLimit: limit}})
	return w
}

// TestUnsupportedField_EphemeralSizeLimitIndependentOfMounting proves the dropped
// size is reported for a declared volume whatever mounts it. The value is lost the
// same way in both shapes, so a warning wired to the mount rather than to the
// declaration would go quiet on exactly the volumes that are otherwise fine.
func TestUnsupportedField_EphemeralSizeLimitIndependentOfMounting(t *testing.T) {
	t.Parallel()
	const coordinate = "volumes.scratch.ephemeral.sizeLimit"
	for _, mounted := range []bool{true, false} {
		mounted := mounted
		name := "mounted"
		if !mounted {
			name = "unmounted"
		}
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			_, diags, err := SynthesizeWorkloadCore(
				mountedEphemeralSizeLimitWorkload("100Mi", mounted), nodeCtxFor("api"))
			if err != nil {
				t.Fatalf("synth: %v", err)
			}
			var n int
			for _, d := range diags {
				if d.SourceRef == coordinate {
					n++
				}
			}
			if n != 1 {
				t.Fatalf("got %d diagnostics at %s, want 1 (all: %+v)", n, coordinate, diags)
			}
		})
	}
}

// TestUnsupportedField_EphemeralSizeLimit proves an authored ephemeral sizeLimit
// is announced rather than dropped in silence: compose's tmpfs short form carries
// no size, so the value reaches no part of the output. The warning names the field
// at its authored coordinate and carries the class code, and a volume that omits
// the optional field raises nothing — the guard is satisfiable precisely because
// sizeLimit is optional.
func TestUnsupportedField_EphemeralSizeLimit(t *testing.T) {
	t.Parallel()
	const coordinate = "volumes.scratch.ephemeral.sizeLimit"

	// Authored: exactly one diagnostic, at the authored coordinate.
	_, diags, err := SynthesizeWorkloadCore(ephemeralSizeLimitWorkload("100Mi"), nodeCtxFor("api"))
	if err != nil {
		t.Fatalf("synth with sizeLimit: %v", err)
	}
	var got []renderctx.Diagnostic
	for _, d := range diags {
		if d.SourceRef == coordinate {
			got = append(got, d)
		}
	}
	if len(got) != 1 {
		t.Fatalf("got %d diagnostics at %s, want 1 (all: %+v)", len(got), coordinate, diags)
	}
	d := got[0]
	if d.Severity != abi.SeverityWarn {
		t.Errorf("severity = %q, want warn", d.Severity)
	}
	if d.Code != rendercore.UnsupportedFieldCode {
		t.Errorf("code = %q, want %q", d.Code, rendercore.UnsupportedFieldCode)
	}
	if !strings.Contains(d.Message, coordinate) {
		t.Errorf("message %q does not name the authored coordinate", d.Message)
	}
	if !strings.Contains(d.Message, "compose") {
		t.Errorf("message %q does not name the target", d.Message)
	}
	// Full omission, not a reduced form: nothing size-derived reaches the output.
	if strings.Contains(d.Message, "does not fully support") {
		t.Errorf("message %q uses the partial-support wording; the value is dropped outright", d.Message)
	}

	// Omitted: nothing for the field. The volume itself still translates.
	_, bare, err := SynthesizeWorkloadCore(ephemeralSizeLimitWorkload(""), nodeCtxFor("api"))
	if err != nil {
		t.Fatalf("synth without sizeLimit: %v", err)
	}
	for _, d := range bare {
		if strings.Contains(d.SourceRef, "sizeLimit") {
			t.Errorf("an unauthored sizeLimit produced a diagnostic: %+v", d)
		}
	}
}

// TestUnsupportedField_EphemeralSizeLimitOutOfBand proves the warning is
// advisory: the authored sizeLimit changes no rendered byte, so the tmpfs entry a
// reader gets is the same either way.
func TestUnsupportedField_EphemeralSizeLimitOutOfBand(t *testing.T) {
	t.Parallel()
	bareFiles, _, err := SynthesizeWorkloadCore(ephemeralSizeLimitWorkload(""), nodeCtxFor("api"))
	if err != nil {
		t.Fatalf("synth without sizeLimit: %v", err)
	}
	sizedFiles, sizedDiags, err := SynthesizeWorkloadCore(ephemeralSizeLimitWorkload("100Mi"), nodeCtxFor("api"))
	if err != nil {
		t.Fatalf("synth with sizeLimit: %v", err)
	}
	if len(sizedDiags) == 0 {
		t.Fatal("expected a diagnostic for the authored sizeLimit, got none")
	}
	if !bytes.Equal(bareFiles[0].Body, sizedFiles[0].Body) {
		t.Errorf("an authored sizeLimit changed rendered bytes (must be out-of-band)\n--- without ---\n%s\n--- with ---\n%s",
			bareFiles[0].Body, sizedFiles[0].Body)
	}
}

// TestUnsupportedField_EphemeralSizeLimitCarriesTheSuppressibleClass proves the
// warning is suppressible by the same filter as the rest of the class, rather
// than asserting that it belongs to the class. The suppression is keyed on the
// diagnostic's own code, so the property that decides it is that this
// diagnostic's code is byte-equal to the code the class's own constructor
// produces — established here against that constructor rather than against a
// literal, so repointing the constructor at another class fails this test.
func TestUnsupportedField_EphemeralSizeLimitCarriesTheSuppressibleClass(t *testing.T) {
	t.Parallel()
	_, diags, err := SynthesizeWorkloadCore(ephemeralSizeLimitWorkload("100Mi"), nodeCtxFor("api"))
	if err != nil {
		t.Fatalf("synth: %v", err)
	}

	classCode := rendercore.UnsupportedFieldDiagnostic(composeTargetName, "probe").Code
	if classCode == "" {
		t.Fatal("the class constructor produced an empty code; nothing for a filter to key on")
	}

	var found bool
	for _, d := range diags {
		if d.SourceRef != "volumes.scratch.ephemeral.sizeLimit" {
			continue
		}
		found = true
		if d.Code != classCode {
			t.Errorf("code = %q, want the class code %q the suppression filter keys on", d.Code, classCode)
		}
	}
	if !found {
		t.Fatalf("no sizeLimit diagnostic to check the class of (all: %+v)", diags)
	}

	// The filter drops exactly the diagnostics carrying that code. Applying that
	// one predicate leaves nothing behind for this workload, which is what
	// --no-unsupported-field-warnings does to it on the display surface.
	for _, d := range diags {
		if d.Code == classCode {
			continue
		}
		t.Errorf("a diagnostic outside the suppressible class survives: %+v", d)
	}
}

// TestUnsupportedField_RequiredSizeFieldsStaySilent is a load-bearing negative:
// the two persistent variants' size fields are deliberately NOT in the
// unsupported-field class, and this test is what stops them being added later.
//
// The reason is the class's own contract. Every warning in it names something the
// author chose to write and can choose to remove, so the advisory is actionable.
// The entity schema declares size required on both persistent variants, so no
// authoring change can avoid such a warning: it would fire on every stateful
// workload with no remedy but the flag that silences the whole class, including
// the advisories whose job is catching silent configuration loss. The compose
// provider page teaches the storage-sizing difference declaratively instead.
//
// Both variants are covered because a change that reinstated one would very
// likely reinstate the other, and each is exercised with its own required size.
func TestUnsupportedField_RequiredSizeFieldsStaySilent(t *testing.T) {
	t.Parallel()
	cases := []struct {
		variant  string
		spec     types.VolumeSpec
		stateful bool
	}{
		{"persistent", types.VolumeSpec{Persistent: &types.PersistentVolume{Size: "10Gi"}}, false},
		{"persistentTemplate", types.VolumeSpec{PersistentTemplate: &types.PersistentTemplateVolume{Size: "1Gi"}}, true},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.variant, func(t *testing.T) {
			t.Parallel()
			w := baseDegradeWorkload()
			if tc.stateful {
				w.Form = "stateful"
			}
			withMainContainer(w, func(c *types.ContainerSpec) {
				c.VolumeMounts = []types.VolumeMount{{Name: "data", Path: "/var/data"}}
			})
			w.Volumes.Set("data", tc.spec)

			_, diags, err := SynthesizeWorkloadCore(w, nodeCtxFor("store"))
			if err != nil {
				t.Fatalf("synth: %v", err)
			}
			for _, d := range diags {
				if strings.Contains(d.SourceRef, "size") || strings.Contains(d.Message, "size and omitted") {
					t.Errorf("%s.size raised a diagnostic; the required size fields are deliberately silent: %+v",
						tc.variant, d)
				}
			}
		})
	}
}

// TestUnsupportedField_AttachmentAsEnvPartial proves an `as: env` attachment
// entry is announced rather than silently emitted as an empty variable: one
// partial-support warning per env entry, at that entry's authored coordinate,
// and none for the `as: file` entry beside it, which compose renders as a real
// bind-mount.
//
// The fixture is deliberately hostile to the mechanism. Two attachments sit on
// one container with different keys and different delivery modes, a second
// container carries its own env entry, and an init container carries one more.
// A warn-on-every-attachment bug, a warn-once-per-container bug, a
// wrong-coordinate bug, and a collection-prefix bug each fail here; a
// single-attachment fixture would detect none of them.
func TestUnsupportedField_AttachmentAsEnvPartial(t *testing.T) {
	t.Parallel()
	w := baseDegradeWorkload()

	withMainContainer(w, func(c *types.ContainerSpec) {
		c.Attachments.Set("DB_PASSWORD", types.AttachmentRef{From: "password", As: "env"})
		c.Attachments.Set("tls-cert", types.AttachmentRef{From: "tls", As: "file", Path: "/etc/tls/server.crt"})
	})

	agent := types.ContainerSpec{Image: "agent:1.0"}
	agent.Attachments.Set("SIDECAR_TOKEN", types.AttachmentRef{From: "password", As: "env"})
	w.Containers.Set("agent", agent)

	// Deliberately unnamed: a positional init entry need not carry a name, so a
	// coordinate read off ContainerSpec.Name would be hollow here. The index is.
	migrate := types.ContainerSpec{Image: "migrate:1.0"}
	migrate.Attachments.Set("MIGRATE_PASSWORD", types.AttachmentRef{From: "password", As: "env"})
	w.InitContainers = append(w.InitContainers, migrate)

	files, diags, err := SynthesizeWorkloadCore(w, nodeCtxFor("api"))
	if err != nil {
		t.Fatalf("synth: %v", err)
	}

	// Exact sequence, not set membership: the order is the containers in author
	// order followed by the init containers, so an ordering regression fails too.
	// The init entry is located by list index, not by name, matching the field
	// paths the Workload attachment validator reports — an unnamed positional
	// entry has no author-typed name, so an index is the only coordinate that is
	// correct in every case.
	want := []string{
		"containers.main.attachments.DB_PASSWORD",
		"containers.agent.attachments.SIDECAR_TOKEN",
		"initContainers[0].attachments.MIGRATE_PASSWORD",
	}
	got := make([]string, 0, len(diags))
	for _, d := range diags {
		got = append(got, d.SourceRef)
	}
	if len(got) != len(want) {
		t.Fatalf("got %d diagnostics, want %d (all: %+v)", len(got), len(want), diags)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("diagnostic %d source ref = %q, want %q (all: %+v)", i, got[i], want[i], diags)
		}
	}

	for _, d := range diags {
		if d.Severity != abi.SeverityWarn {
			t.Errorf("%s: severity = %q, want warn", d.SourceRef, d.Severity)
		}
		if d.Code != rendercore.UnsupportedFieldCode {
			t.Errorf("%s: code = %q, want %q", d.SourceRef, d.Code, rendercore.UnsupportedFieldCode)
		}
		if !strings.Contains(d.Message, d.SourceRef) {
			t.Errorf("%s: message %q does not name the authored coordinate", d.SourceRef, d.Message)
		}
		if !strings.Contains(d.Message, "does not fully support") {
			t.Errorf("%s: message %q missing the partial-support wording", d.SourceRef, d.Message)
		}
		if !strings.Contains(d.Message, "empty value") {
			t.Errorf("%s: message %q does not say the variable is emitted empty", d.SourceRef, d.Message)
		}
	}

	// The placeholder is kept: the advisory announces the empty variable, it does
	// not replace it. A rendered fragment that dropped the variable would leave
	// the author with nothing to debug from.
	if !bytes.Contains(files[0].Body, []byte(`DB_PASSWORD: ""`)) {
		t.Errorf("rendered fragment lost the empty placeholder variable:\n%s", files[0].Body)
	}
}
