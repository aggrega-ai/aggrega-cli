// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package helmcore

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// suspendWorkload builds the scheduled workload the form-scheduled golden was
// rendered from, holding its schedule or not.
func suspendWorkload(suspend bool) *types.Workload {
	w := &types.Workload{APIVersion: "aggrega.ai/v1alpha1", Kind: "Workload", Form: "scheduled", Schedule: "@daily", Suspend: suspend}
	w.Containers.Set("main", types.ContainerSpec{Image: "reporter:1.0", Command: []string{"/report"}})
	return w
}

// TestSynthesizeWorkload_UnsuspendedScheduledRendersAsBefore is the additive
// proof. omitempty elides a false bool, so a scheduled workload that does not
// hold its schedule must render byte-for-byte as the committed form-scheduled
// golden, the output of the same workload naming no suspend at all. The golden
// is read directly rather than through goldenCheck, so no update path can
// rewrite the baseline this compares against.
func TestSynthesizeWorkload_UnsuspendedScheduledRendersAsBefore(t *testing.T) {
	t.Parallel()
	want, err := os.ReadFile(filepath.Join("testdata", "workload", "form-scheduled.golden.yaml"))
	if err != nil {
		t.Fatalf("read golden: %v", err)
	}
	files, err := SynthesizeWorkloadCore(suspendWorkload(false), renderer.AttachmentProvidesFromContext(nil), nodeCtxFor("nightly"))
	if err != nil {
		t.Fatalf("SynthesizeWorkload: %v", err)
	}
	if got := concatBodies(files); string(got) != string(want) {
		t.Errorf("an unsuspended scheduled workload must render as before\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

// TestSynthesizeWorkload_SuspendReachesTheEmittedCronJob is the invocation
// proof: the held schedule is read off the rendered CronJob rather than off the
// parsed value. The suspended body is the unsuspended body with exactly one line
// added — spec.suspend: true, beside spec.schedule — so the assertion pins both
// that the field arrives and that nothing else moves with it.
func TestSynthesizeWorkload_SuspendReachesTheEmittedCronJob(t *testing.T) {
	t.Parallel()
	plain, err := SynthesizeWorkloadCore(suspendWorkload(false), renderer.AttachmentProvidesFromContext(nil), nodeCtxFor("nightly"))
	if err != nil {
		t.Fatalf("SynthesizeWorkload (unsuspended): %v", err)
	}
	held, err := SynthesizeWorkloadCore(suspendWorkload(true), renderer.AttachmentProvidesFromContext(nil), nodeCtxFor("nightly"))
	if err != nil {
		t.Fatalf("SynthesizeWorkload (suspended): %v", err)
	}
	if len(held) != 1 || held[0].Name != "nightly-cronjob.yaml" {
		t.Fatalf("a suspended scheduled workload emits one CronJob file; got %d file(s): %+v", len(held), held)
	}

	var doc struct {
		Kind string `yaml:"kind"`
		Spec struct {
			Schedule string `yaml:"schedule"`
			Suspend  *bool  `yaml:"suspend"`
		} `yaml:"spec"`
	}
	if err := yaml.Unmarshal(held[0].Body, &doc); err != nil {
		t.Fatalf("parse emitted CronJob: %v\n%s", err, held[0].Body)
	}
	if doc.Kind != "CronJob" || doc.Spec.Schedule != "@daily" || doc.Spec.Suspend == nil || !*doc.Spec.Suspend {
		t.Fatalf("want a CronJob keeping spec.schedule @daily with spec.suspend: true; got:\n%s", held[0].Body)
	}

	plainBody := string(plain[0].Body)
	const scheduleLine = "schedule: '@daily'\n"
	at := strings.Index(plainBody, scheduleLine)
	if at < 0 {
		t.Fatalf("unsuspended CronJob carries no schedule line; got:\n%s", plainBody)
	}
	lineStart := strings.LastIndex(plainBody[:at], "\n") + 1
	indent := plainBody[lineStart:at]
	insertAt := at + len(scheduleLine)
	want := plainBody[:insertAt] + indent + "suspend: true\n" + plainBody[insertAt:]
	if string(held[0].Body) != want {
		t.Errorf("suspending must add spec.suspend: true and move nothing else\n--- got ---\n%s\n--- want ---\n%s", held[0].Body, want)
	}
}
