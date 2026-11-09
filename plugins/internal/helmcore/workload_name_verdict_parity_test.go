// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package helmcore

import (
	"regexp"
	"strconv"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/rendercore"
	"github.com/aggrega-ai/aggrega-cli/pkg/labels"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// wouldBe reads the length a refusing verdict says the derived name would have.
var wouldBe = regexp.MustCompile(`would be (\d+)`)

// TestWorkloadNameVerdicts_MeasureTheNamesTheSynthesizerEmits is the parity guard
// between the two sides that each decide a workload-dependent name: the verdicts
// judge a name from a proposed identity and whether it was authored, while the
// synthesizer names its resources from a render context. They choose the
// derivation from different carries — the authored flag on one side, the presence
// of a composed identity on the other — so this drives both through the real
// synthesis entry and asserts the CronJob and each Service the synthesizer emits
// are exactly as long as the verdicts said they would be.
//
// Both rows are over every bound on purpose, because only a refusal reports the
// length it measured. One row is an identity a person wrote, which the
// synthesizer names after its release name; the other carries a settled composed
// identity, which names an attachment's resources as it stands.
func TestWorkloadNameVerdicts_MeasureTheNamesTheSynthesizerEmits(t *testing.T) {
	t.Parallel()

	keys := []string{"default", "metricsexporter"}
	workload := &types.Workload{
		APIVersion: "aggrega.ai/v1beta1",
		Kind:       "Workload",
		Form:       rendercore.WorkloadFormScheduled,
		Schedule:   "@daily",
	}
	c := types.ContainerSpec{Image: "app:1.0"}
	c.Ports.Set("web", types.PortSpec{Port: 8080})
	workload.Containers.Set("main", c)
	for _, key := range keys {
		svc := types.ServiceSpec{Type: "ClusterIP"}
		svc.Ports.Set("web", 8080)
		workload.Services.Set(key, svc)
	}

	authoredIdentity := "nightly_" + strings.Repeat("r", 50)
	composedIdentity := labels.ComposeComponentIdentity(labels.AttachmentScope, labels.Unbounded, "host", strings.Repeat("n", 50))

	for _, tc := range []struct {
		scenario string
		nodeCtx  rendercore.NodeRenderContext
		in       rendercore.NameBoundInput
	}{
		{
			scenario: "an authored identity",
			nodeCtx:  nodeCtxFor(authoredIdentity),
			in:       rendercore.NameBoundInput{Proposed: authoredIdentity, Authored: true},
		},
		{
			scenario: "a settled composed identity",
			nodeCtx: func() rendercore.NodeRenderContext {
				ctx := nodeCtxFor("host")
				ctx.LabelComponentID = composedIdentity
				return ctx
			}(),
			in: rendercore.NameBoundInput{Proposed: composedIdentity},
		},
	} {
		t.Run(tc.scenario, func(t *testing.T) {
			t.Parallel()
			tc.in.WorkloadForm = workload.Form
			tc.in.ServiceKeys = keys

			files, err := SynthesizeWorkloadCore(workload, nil, tc.nodeCtx)
			if err != nil {
				t.Fatalf("SynthesizeWorkloadCore: %v", err)
			}
			var cronJobs, services []string
			for _, f := range files {
				var head struct {
					Kind     string `yaml:"kind"`
					Metadata struct {
						Name string `yaml:"name"`
					} `yaml:"metadata"`
				}
				if err := yaml.Unmarshal(f.Body, &head); err != nil {
					t.Fatalf("decode %s: %v", f.Name, err)
				}
				switch head.Kind {
				case "CronJob":
					cronJobs = append(cronJobs, head.Metadata.Name)
				case "Service":
					services = append(services, head.Metadata.Name)
				}
			}
			if len(cronJobs) != 1 || len(services) != len(keys) {
				t.Fatalf("emitted CronJobs %v and Services %v; want one CronJob and one Service per key %v", cronJobs, services, keys)
			}

			cron, derived := CronJobNameVerdict(tc.in)
			if !derived || cron.Usable {
				t.Fatalf("the fixture's CronJob is over the bound, so the verdict must refuse it; derived=%v usable=%v", derived, cron.Usable)
			}
			if got, want := len(cronJobs[0]), measuredLength(t, cron.Reason); got != want {
				t.Errorf("the emitted CronJob %q is %d characters; the verdict measured %d", cronJobs[0], got, want)
			}

			svc := ServiceNameVerdict(tc.in)
			if svc.Usable {
				t.Fatal("the fixture's Services are over the bound, so the verdict must refuse them")
			}
			// The verdict reports the key with the least room; Services are emitted in
			// the order the workload declares their keys.
			reported := -1
			for i, key := range keys {
				if strings.Contains(svc.Reason, strconv.Quote(key)) {
					reported = i
				}
			}
			if reported < 0 {
				t.Fatalf("the Service verdict names none of the keys %v: %s", keys, svc.Reason)
			}
			if got, want := len(services[reported]), measuredLength(t, svc.Reason); got != want {
				t.Errorf("the emitted Service %q is %d characters; the verdict measured %d", services[reported], got, want)
			}
		})
	}
}

// measuredLength reads the length a refusal's reason reports.
func measuredLength(t *testing.T, reason string) int {
	t.Helper()
	m := wouldBe.FindStringSubmatch(reason)
	if m == nil {
		t.Fatalf("the refusal reports no measured length: %s", reason)
	}
	n, err := strconv.Atoi(m[1])
	if err != nil {
		t.Fatalf("the measured length %q is not a number: %v", m[1], err)
	}
	return n
}
