// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package composecore

import (
	"bytes"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/rendercore"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// TestUnsupportedField_PodGroupBesideAContainerUserOutOfBand proves a pod
// runAsGroup that reaches no part of the service is announced and changes nothing
// compose renders. The main container sets its own runAsUser and no runAsGroup, so
// its user wins and is rendered without a group, where a pod would run that
// container under the pod's group: one warning for the pod's runAsGroup, and the
// service bytes equal those of the same workload authoring no pod group. A
// container that sets a group of its own replaces the pod's, as in a pod, and
// raises none.
func TestUnsupportedField_PodGroupBesideAContainerUserOutOfBand(t *testing.T) {
	t.Parallel()
	workload := func(podGroup *int, containerGroup *int) *types.Workload {
		w := baseDegradeWorkload()
		w.PodSecurityContext = &types.PodSecurityContext{RunAsUser: ptrInt(1000), RunAsGroup: podGroup}
		withMainContainer(w, func(c *types.ContainerSpec) {
			c.SecurityContext = &types.ContainerSecurityContext{RunAsUser: ptrInt(2000), RunAsGroup: containerGroup}
		})
		return w
	}

	bareFiles, bareDiags, err := SynthesizeWorkloadCore(workload(nil, nil), nodeCtxFor("api"))
	if err != nil {
		t.Fatalf("synth without a pod group: %v", err)
	}
	if len(bareDiags) != 0 {
		t.Fatalf("a workload with no pod group raised diagnostics: %+v", bareDiags)
	}

	files, diags, err := SynthesizeWorkloadCore(workload(ptrInt(3000), nil), nodeCtxFor("api"))
	if err != nil {
		t.Fatalf("synth with a pod group beside a container user: %v", err)
	}
	if len(diags) != 1 {
		t.Fatalf("got %d diagnostics, want 1 (all: %+v)", len(diags), diags)
	}
	if diags[0].SourceRef != "podSecurityContext.runAsGroup" || diags[0].Code != rendercore.UnsupportedFieldCode {
		t.Errorf("got %+v, want the unsupported-field warning for podSecurityContext.runAsGroup", diags[0])
	}
	if !bytes.Equal(bareFiles[0].Body, files[0].Body) {
		t.Errorf("the dropped pod group changed rendered bytes (must be out-of-band)\n--- without ---\n%s\n--- with ---\n%s",
			bareFiles[0].Body, files[0].Body)
	}
	if !bytes.Contains(files[0].Body, []byte(`user: "2000"`)) {
		t.Errorf("the container's own user must still be rendered:\n%s", files[0].Body)
	}

	overriddenFiles, overriddenDiags, err := SynthesizeWorkloadCore(workload(ptrInt(3000), ptrInt(4000)), nodeCtxFor("api"))
	if err != nil {
		t.Fatalf("synth with a container group of its own: %v", err)
	}
	if len(overriddenDiags) != 0 {
		t.Errorf("a container group replacing the pod's raised diagnostics: %+v", overriddenDiags)
	}
	if !bytes.Contains(overriddenFiles[0].Body, []byte("2000:4000")) {
		t.Errorf("the container's user and group must be rendered:\n%s", overriddenFiles[0].Body)
	}
}
