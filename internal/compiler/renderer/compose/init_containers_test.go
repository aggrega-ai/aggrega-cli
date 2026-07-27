// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package compose

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/rendercore"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// twoInitContainerWorkload is a single-container workload seeded by two init
// containers. The first writes a file into a shared volume the main container
// also mounts; the second reads it. Both mount the same configmap volume, which
// is what proves the configuration an init container carries survives.
func twoInitContainerWorkload() *types.Workload {
	w := &types.Workload{APIVersion: "aggrega.ai/v1alpha1", Kind: "Workload"}

	main := types.ContainerSpec{Image: "platform:1.0"}
	main.Ports.Set("http", 5678)
	main.VolumeMounts = []types.VolumeMount{{Name: "data", Path: "/home/node/.n8n"}}
	w.Containers.Set("main", main)

	// Authored order is significant: import-seed runs first, verify-seed second.
	seed := types.ContainerSpec{
		Name:    "import-seed",
		Image:   "platform:1.0",
		Command: []string{"/bin/sh", "-c"},
		Args:    []string{"platform import:workflow --input=/seed/workflow.json"},
		VolumeMounts: []types.VolumeMount{
			{Name: "seed", Path: "/seed", ReadOnly: true},
			{Name: "data", Path: "/home/node/.n8n"},
		},
	}
	seed.Variables.Set("SEED_SOURCE", "/seed/workflow.json")

	verify := types.ContainerSpec{
		Name:         "verify-seed",
		Image:        "platform:1.0",
		Command:      []string{"/bin/sh", "-c"},
		Args:         []string{"test -f /home/node/.n8n/database.sqlite"},
		VolumeMounts: []types.VolumeMount{{Name: "data", Path: "/home/node/.n8n"}},
	}

	w.InitContainers = []types.ContainerSpec{seed, verify}

	w.Volumes.Set("seed", types.VolumeSpec{ConfigMap: &types.ConfigMapVolume{Source: "workflow-seed"}})
	w.Volumes.Set("data", types.VolumeSpec{Persistent: &types.PersistentVolume{Size: "2Gi"}})

	svc := types.ServiceSpec{Type: "ClusterIP"}
	svc.Ports.Set("http", 5678)
	w.Services.Set("platform", svc)

	w.Form = "stateful"
	return w
}

// TestSynthesizeWorkload_InitContainersChain pins the whole emitted shape: one
// service per init container in authored order, a chain where the second waits
// for the first to complete, the main service waiting for the last, and every
// init service's volume mounts translated.
func TestSynthesizeWorkload_InitContainersChain(t *testing.T) {
	t.Parallel()
	files, _, err := SynthesizeWorkloadCore(twoInitContainerWorkload(), nodeCtxFor("platform"))
	if err != nil {
		t.Fatalf("synth: %v", err)
	}
	goldenCheck(t, "init-containers-chain", files[0].Body)
}

// TestSynthesizeWorkload_InitContainerVolumeMountTranslated reads the emitted
// bytes back and asserts each init service carries the mounts its container
// declared, with the configmap mount translated to the same read-only bind the
// main-container path produces. A dropped mount is the failure this capability
// exists to remove, so it is asserted on the output rather than inferred from
// the call.
func TestSynthesizeWorkload_InitContainerVolumeMountTranslated(t *testing.T) {
	t.Parallel()
	files, _, err := SynthesizeWorkloadCore(twoInitContainerWorkload(), nodeCtxFor("platform"))
	if err != nil {
		t.Fatalf("synth: %v", err)
	}

	var frag ComposeFile
	if err := yaml.Unmarshal(files[0].Body, &frag); err != nil {
		t.Fatalf("parse fragment: %v", err)
	}

	seed, ok := frag.Services["platform-import-seed"]
	if !ok {
		t.Fatalf("no init service for the first init container; services: %v", serviceNames(frag))
	}
	wantMounts := []ServiceVolume{
		{Type: "bind", Source: "workflow-seed", Target: "/seed", ReadOnly: true},
		{Type: "volume", Source: "platform-data", Target: "/home/node/.n8n"},
	}
	if len(seed.Volumes) != len(wantMounts) {
		t.Fatalf("init service mounts = %+v, want %+v", seed.Volumes, wantMounts)
	}
	for i := range wantMounts {
		if seed.Volumes[i] != wantMounts[i] {
			t.Errorf("mount %d = %+v, want %+v", i, seed.Volumes[i], wantMounts[i])
		}
	}

	// The configuration the init container carries reaches it: the env var it
	// declared and the command that reads the mounted file both survive.
	if got, _ := seed.Environment.Get("SEED_SOURCE"); got != "/seed/workflow.json" {
		t.Errorf("init service SEED_SOURCE = %q, want /seed/workflow.json", got)
	}
	if len(seed.Entrypoint) == 0 || len(seed.Command) == 0 {
		t.Errorf("init service lost its command: entrypoint=%v command=%v", seed.Entrypoint, seed.Command)
	}

	// The shared data volume is mounted by the init services and by the main
	// service, so what an init container writes is what the main service reads.
	for _, name := range []string{"platform", "platform-import-seed", "platform-verify-seed"} {
		svc := frag.Services[name]
		var found bool
		for _, v := range svc.Volumes {
			if v.Source == "platform-data" && v.Target == "/home/node/.n8n" {
				found = true
			}
		}
		if !found {
			t.Errorf("service %q does not mount the shared data volume: %+v", name, svc.Volumes)
		}
	}
}

// TestSynthesizeWorkload_InitContainersRunToCompletion asserts the ordering and
// the run-once configuration on the parsed output: the chain condition, the main
// service's wait, and that no init service restarts.
func TestSynthesizeWorkload_InitContainersRunToCompletion(t *testing.T) {
	t.Parallel()
	files, _, err := SynthesizeWorkloadCore(twoInitContainerWorkload(), nodeCtxFor("platform"))
	if err != nil {
		t.Fatalf("synth: %v", err)
	}
	var frag ComposeFile
	if err := yaml.Unmarshal(files[0].Body, &frag); err != nil {
		t.Fatalf("parse fragment: %v", err)
	}

	first := frag.Services["platform-import-seed"]
	if first.DependsOn.Len() != 0 {
		t.Errorf("the first init service waits on nothing; got %d dependencies", first.DependsOn.Len())
	}

	second := frag.Services["platform-verify-seed"]
	assertWaitsFor(t, "platform-verify-seed", second, "platform-import-seed")

	main := frag.Services["platform"]
	assertWaitsFor(t, "platform", main, "platform-verify-seed")

	for _, name := range []string{"platform-import-seed", "platform-verify-seed"} {
		if got := frag.Services[name].Restart; got != "no" {
			t.Errorf("init service %q restart = %q, want no", name, got)
		}
		// An init service joins the boundary network in its own right; sharing
		// the main service's namespace would make the main service's wait
		// circular.
		if got := frag.Services[name].NetworkMode; got != "" {
			t.Errorf("init service %q network_mode = %q, want unset", name, got)
		}
	}
}

// assertWaitsFor asserts svc waits for target with the completion condition, by
// re-marshaling the field and reading the emitted long form.
func assertWaitsFor(t *testing.T, name string, svc Service, target string) {
	t.Helper()
	body, err := yaml.Marshal(svc.DependsOn)
	if err != nil {
		t.Fatalf("%s: marshal depends_on: %v", name, err)
	}
	got := string(body)
	if !strings.Contains(got, target+":") {
		t.Errorf("%s: depends_on does not name %q:\n%s", name, target, got)
	}
	if !strings.Contains(got, conditionCompletedSuccessfully) {
		t.Errorf("%s: depends_on on %q is not gated on completion:\n%s", name, target, got)
	}
}

// TestSynthesizeWorkload_InitContainerUnnamedFallsBackToIndex proves an unnamed
// positional entry takes the documented init-<index> name.
func TestSynthesizeWorkload_InitContainerUnnamedFallsBackToIndex(t *testing.T) {
	t.Parallel()
	w := baseDegradeWorkload()
	w.InitContainers = []types.ContainerSpec{{Image: "prep:1.0"}, {Image: "prep:2.0"}}

	files, _, err := SynthesizeWorkloadCore(w, nodeCtxFor("api"))
	if err != nil {
		t.Fatalf("synth: %v", err)
	}
	var frag ComposeFile
	if err := yaml.Unmarshal(files[0].Body, &frag); err != nil {
		t.Fatalf("parse fragment: %v", err)
	}
	for _, name := range []string{"api-init-0", "api-init-1"} {
		if _, ok := frag.Services[name]; !ok {
			t.Errorf("no service %q; services: %v", name, serviceNames(frag))
		}
	}
	assertWaitsFor(t, "api-init-1", frag.Services["api-init-1"], "api-init-0")
	assertWaitsFor(t, "api", frag.Services["api"], "api-init-1")
}

// TestSynthesizeWorkload_InitContainersEmitNoUnsupportedFieldWarning proves the
// drop warning is gone: a field the target now honors must not be reported as
// dropped.
func TestSynthesizeWorkload_InitContainersEmitNoUnsupportedFieldWarning(t *testing.T) {
	t.Parallel()
	w := baseDegradeWorkload()
	w.InitContainers = []types.ContainerSpec{{Name: "prep", Image: "prep:1.0"}}

	_, diags, err := SynthesizeWorkloadCore(w, nodeCtxFor("api"))
	if err != nil {
		t.Fatalf("synth: %v", err)
	}
	for _, d := range diags {
		if d.SourceRef == "initContainers" {
			t.Errorf("initContainers is honored but still reported as unsupported: %+v", d)
		}
	}
}

// TestSynthesizeWorkload_InitContainersSidecarsAlsoWait proves parity with the
// authoring contract on a multi-container workload: init containers finish
// before every long-running container, sidecars included, not just the main one.
func TestSynthesizeWorkload_InitContainersSidecarsAlsoWait(t *testing.T) {
	t.Parallel()
	w := baseDegradeWorkload()
	side := types.ContainerSpec{Image: "log-shipper:1.0"}
	w.Containers.Set("shipper", side)
	w.InitContainers = []types.ContainerSpec{{Name: "prep", Image: "prep:1.0"}}

	files, _, err := SynthesizeWorkloadCore(w, nodeCtxFor("api"))
	if err != nil {
		t.Fatalf("synth: %v", err)
	}
	var frag ComposeFile
	if err := yaml.Unmarshal(files[0].Body, &frag); err != nil {
		t.Fatalf("parse fragment: %v", err)
	}
	assertWaitsFor(t, "api", frag.Services["api"], "api-prep")
	assertWaitsFor(t, "api-shipper", frag.Services["api-shipper"], "api-prep")
}

// TestSynthesizeWorkload_InitWaitSurvivesPortPublishing proves the main
// service's wait is not clobbered by the later pass that publishes its ports.
// That pass reads the main service, adds ports, and writes it back, so a wait
// recorded before it would vanish if the two ever stopped operating on the same
// value.
func TestSynthesizeWorkload_InitWaitSurvivesPortPublishing(t *testing.T) {
	t.Parallel()
	w := baseDegradeWorkload()
	published := types.ServiceSpec{Type: "NodePort"}
	published.Ports.Set("http", 8080)
	w.Services.Set("api", published)
	w.InitContainers = []types.ContainerSpec{{Name: "prep", Image: "prep:1.0"}}

	files, _, err := SynthesizeWorkloadCore(w, nodeCtxFor("api"))
	if err != nil {
		t.Fatalf("synth: %v", err)
	}
	var frag ComposeFile
	if err := yaml.Unmarshal(files[0].Body, &frag); err != nil {
		t.Fatalf("parse fragment: %v", err)
	}
	main := frag.Services["api"]
	if len(main.Ports) == 0 {
		t.Fatalf("main service lost its published ports: %+v", main)
	}
	assertWaitsFor(t, "api", main, "api-prep")
}

// TestSynthesizeWorkload_InitContainerNameCollision_Halts proves a name an
// ordinary container already claimed halts loudly instead of one service
// silently overwriting the other.
func TestSynthesizeWorkload_InitContainerNameCollision_Halts(t *testing.T) {
	t.Parallel()
	w := baseDegradeWorkload()
	w.Containers.Set("prep", types.ContainerSpec{Image: "sidecar:1.0"})
	w.InitContainers = []types.ContainerSpec{{Name: "prep", Image: "prep:1.0"}}

	_, _, err := SynthesizeWorkloadCore(w, nodeCtxFor("api"))
	if err == nil {
		t.Fatal("expected a halt for an init container colliding with a container name")
	}
	for _, want := range []string{"TRANSLATION_ERROR", `"prep"`, `"api-prep"`, "initContainers"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("halt message must contain %q, got: %v", want, err)
		}
	}
}

// TestSynthesizeWorkload_InitContainerDuplicateName_Halts proves two init
// containers sharing one name halt rather than collapsing into one service.
func TestSynthesizeWorkload_InitContainerDuplicateName_Halts(t *testing.T) {
	t.Parallel()
	w := baseDegradeWorkload()
	w.InitContainers = []types.ContainerSpec{
		{Name: "prep", Image: "prep:1.0"},
		{Name: "prep", Image: "prep:2.0"},
	}

	_, _, err := SynthesizeWorkloadCore(w, nodeCtxFor("api"))
	if err == nil {
		t.Fatal("expected a halt for two init containers with the same name")
	}
	if !strings.Contains(err.Error(), "TRANSLATION_ERROR") {
		t.Errorf("expected TRANSLATION_ERROR, got: %v", err)
	}
}

// TestSynthesizeWorkload_InitContainerReachesSharedTranslationHalts proves an
// init container goes through the same container translation the other
// containers do: each shape that halts for an ordinary container halts for an
// init container too, which is what a separate init-specific translator would
// silently stop doing.
func TestSynthesizeWorkload_InitContainerReachesSharedTranslationHalts(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name     string
		init     types.ContainerSpec
		sentinel error
		want     string
	}{
		{
			name:     "lifecycle",
			init:     types.ContainerSpec{Name: "prep", Image: "prep:1.0", Lifecycle: &types.LifecycleSpec{}},
			sentinel: ErrUnsupportedComposeLifecycle,
			want:     "lifecycle-hook primitive",
		},
		{
			name:     "envFrom",
			init:     types.ContainerSpec{Name: "prep", Image: "prep:1.0", EnvFrom: []types.EnvFromSource{{ConfigMap: "settings"}}},
			sentinel: ErrUnsupportedComposeEnvFrom,
			want:     "env-from-object primitive",
		},
		{
			name: "imagePullPolicy",
			init: types.ContainerSpec{Name: "prep", Image: "prep:1.0", ImagePullPolicy: "Sometimes"},
			want: "unknown imagePullPolicy",
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			w := baseDegradeWorkload()
			w.InitContainers = []types.ContainerSpec{tc.init}
			_, _, err := SynthesizeWorkloadCore(w, nodeCtxFor("api"))
			if err == nil {
				t.Fatalf("an init container declaring %s must halt as an ordinary container does", tc.name)
			}
			if tc.sentinel != nil && !errors.Is(err, tc.sentinel) {
				t.Errorf("halt does not carry the same sentinel the container path raises: %v", err)
			}
			if chain := errorChainText(err); !strings.Contains(chain, tc.want) {
				t.Errorf("halt = %q, want it to contain %q", chain, tc.want)
			}
			if !strings.Contains(err.Error(), "init container") {
				t.Errorf("halt must name the init container it came from, got: %v", err)
			}
		})
	}
}

// errorChainText joins every message in an error chain so an assertion can read
// a cause the outermost message does not restate.
func errorChainText(err error) string {
	var parts []string
	for e := err; e != nil; e = errors.Unwrap(e) {
		parts = append(parts, e.Error())
	}
	return strings.Join(parts, " | ")
}

// TestSynthesizeWorkload_InitContainerRegistryRewriteReaches proves the shared
// translation's image-registry rewrite reaches init containers — the concrete
// thing a parallel init translator would have drifted on.
func TestSynthesizeWorkload_InitContainerRegistryRewriteReaches(t *testing.T) {
	t.Parallel()
	w := baseDegradeWorkload()
	w.InitContainers = []types.ContainerSpec{{Name: "prep", Image: "prep:1.0"}}

	nodeCtx := nodeCtxFor("api")
	nodeCtx.Annotations = map[string]string{rendercore.AnnotationImageRegistry: "registry.example.com"}

	files, _, err := SynthesizeWorkloadCore(w, nodeCtx)
	if err != nil {
		t.Fatalf("synth: %v", err)
	}
	var frag ComposeFile
	if err := yaml.Unmarshal(files[0].Body, &frag); err != nil {
		t.Fatalf("parse fragment: %v", err)
	}
	got := frag.Services["api-prep"].Image
	if !strings.HasPrefix(got, "registry.example.com/") {
		t.Errorf("init service image = %q, want the registry prefix applied", got)
	}
}

// TestSynthesizeWorkload_InitContainersDeterministic proves two runs of the same
// input produce identical bytes — the field carries an insertion-ordered slice,
// not a map, so nothing map-ordered reaches the output.
func TestSynthesizeWorkload_InitContainersDeterministic(t *testing.T) {
	t.Parallel()
	first, _, err := SynthesizeWorkloadCore(twoInitContainerWorkload(), nodeCtxFor("platform"))
	if err != nil {
		t.Fatalf("first synth: %v", err)
	}
	for i := 0; i < 24; i++ {
		next, _, err := SynthesizeWorkloadCore(twoInitContainerWorkload(), nodeCtxFor("platform"))
		if err != nil {
			t.Fatalf("run %d synth: %v", i, err)
		}
		if !bytes.Equal(first[0].Body, next[0].Body) {
			t.Fatalf("run %d differs from the first\n--- first ---\n%s\n--- run %d ---\n%s", i, first[0].Body, i, next[0].Body)
		}
	}
}

// TestComposeAssemble_InitChainAndEdgeDependency proves the two orderings coexist
// in one field in the assembled output: the init wait the synthesiser recorded
// survives the fragment's marshal-and-parse round trip, and the edge-derived
// dependency the assembler adds lands after it rather than interleaving with it.
// It also pins that a service with no init chain keeps the short form.
func TestComposeAssemble_InitChainAndEdgeDependency(t *testing.T) {
	t.Parallel()

	api := baseDegradeWorkload()
	api.InitContainers = []types.ContainerSpec{
		{Name: "migrate", Image: "migrate:1.0"},
		{Name: "warm-cache", Image: "warm:1.0"},
	}

	db := &types.Workload{APIVersion: "aggrega.ai/v1alpha1", Kind: "Workload"}
	dc := types.ContainerSpec{Image: "postgres:15"}
	dc.Ports.Set("pg", 5432)
	db.Containers.Set("main", dc)
	dbSvc := types.ServiceSpec{Type: "ClusterIP"}
	dbSvc.Ports.Set("pg", 5432)
	db.Services.Set("db", dbSvc)

	files, err := AssembleBoundaryCore(
		map[string][]rendercore.EmittedFile{
			"api": makeFragment(t, "api", api, nil),
			"db":  makeFragment(t, "db", db, nil),
		},
		map[string]map[string]string{},
		[]rendercore.IREdgeInfo{{Source: "api", Target: "db"}},
		"default",
		0,
	)
	if err != nil {
		t.Fatalf("assemble: %v", err)
	}
	goldenCheckAssembled(t, "init-chain-and-edge-dependency", files[AssembledOutputFile])
}

// TestDependsOn_EmitShapeAndOrder pins the two forms and the insertion order the
// field promises: condition-less entries keep the short list, one condition
// promotes the whole field to the long form with the condition-less entries
// carrying the specification's stated short-form equivalent, and the order the
// dependencies were recorded in survives.
func TestDependsOn_EmitShapeAndOrder(t *testing.T) {
	t.Parallel()

	var short DependsOn
	short.Require("db", "")
	short.Require("cache", "")
	got, err := yaml.Marshal(short)
	if err != nil {
		t.Fatalf("marshal short: %v", err)
	}
	if want := "- db\n- cache\n"; string(got) != want {
		t.Errorf("condition-less field = %q, want the short list %q", got, want)
	}

	var mixed DependsOn
	mixed.Require("api-prep", conditionCompletedSuccessfully)
	mixed.Require("db", "")
	got, err = yaml.Marshal(mixed)
	if err != nil {
		t.Fatalf("marshal mixed: %v", err)
	}
	want := "api-prep:\n    condition: service_completed_successfully\ndb:\n    condition: service_started\n"
	if string(got) != want {
		t.Errorf("mixed field =\n%q\nwant\n%q", got, want)
	}
}

// TestDependsOn_RequireIsIdempotentPerService pins the merge rule two producers
// rely on: the first call fixes the emitted position, and a later call supplies
// a condition only when none is recorded.
func TestDependsOn_RequireIsIdempotentPerService(t *testing.T) {
	t.Parallel()

	// An ungated wait recorded first is upgraded by a later gated one, in place.
	var upgrade DependsOn
	upgrade.Require("db", "")
	upgrade.Require("cache", "")
	upgrade.Require("db", conditionCompletedSuccessfully)
	if upgrade.Len() != 2 {
		t.Fatalf("Require must not duplicate a service; got %d entries", upgrade.Len())
	}
	got, err := yaml.Marshal(upgrade)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	want := "db:\n    condition: service_completed_successfully\ncache:\n    condition: service_started\n"
	if string(got) != want {
		t.Errorf("upgraded field =\n%q\nwant\n%q", got, want)
	}

	// A recorded condition is not weakened by a later ungated wait.
	var keep DependsOn
	keep.Require("db", conditionCompletedSuccessfully)
	keep.Require("db", "")
	got, err = yaml.Marshal(keep)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if want := "db:\n    condition: service_completed_successfully\n"; string(got) != want {
		t.Errorf("field = %q, want the recorded condition kept: %q", got, want)
	}
}

// TestDependsOn_RoundTripsBothForms proves a fragment survives the marshal the
// synthesiser does and the parse the assembler does, in both emitted forms — the
// hop where a lost condition would silently become an ungated wait.
func TestDependsOn_RoundTripsBothForms(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		body string
		want []dependsOnEntry
	}{
		{
			name: "short form",
			body: "depends_on:\n  - db\n  - cache\n",
			want: []dependsOnEntry{{service: "db"}, {service: "cache"}},
		},
		{
			name: "long form",
			body: "depends_on:\n  api-prep:\n    condition: service_completed_successfully\n  db:\n    condition: service_started\n",
			want: []dependsOnEntry{
				{service: "api-prep", condition: conditionCompletedSuccessfully},
				{service: "db", condition: conditionStarted},
			},
		},
		{
			name: "long form with fields the struct does not model",
			body: "depends_on:\n  db:\n    condition: service_healthy\n    required: true\n",
			want: []dependsOnEntry{{service: "db", condition: "service_healthy"}},
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var svc Service
			if err := yaml.Unmarshal([]byte(tc.body), &svc); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			if len(svc.DependsOn.entries) != len(tc.want) {
				t.Fatalf("entries = %+v, want %+v", svc.DependsOn.entries, tc.want)
			}
			for i := range tc.want {
				if svc.DependsOn.entries[i] != tc.want[i] {
					t.Errorf("entry %d = %+v, want %+v", i, svc.DependsOn.entries[i], tc.want[i])
				}
			}
		})
	}

	// A shape that is neither form is rejected rather than silently read as empty.
	var svc Service
	if err := yaml.Unmarshal([]byte("depends_on: db\n"), &svc); err == nil {
		t.Error("a scalar depends_on must be rejected, not read as no dependencies")
	}
}

// TestDependsOn_OmittedWhenEmpty proves the omitempty tag reaches the field. The
// reflective emptiness check yaml.v3 would otherwise use skips unexported
// fields, so it would judge a populated field empty and drop it from the output.
func TestDependsOn_OmittedWhenEmpty(t *testing.T) {
	t.Parallel()

	body, err := yaml.Marshal(Service{Image: "app:1.0"})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(body), "depends_on") {
		t.Errorf("a service with no dependencies must omit depends_on:\n%s", body)
	}

	svc := Service{Image: "app:1.0"}
	svc.DependsOn.Require("db", "")
	body, err = yaml.Marshal(svc)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(body), "depends_on") {
		t.Errorf("a service with a dependency must emit depends_on:\n%s", body)
	}
}

// TestDependsOn_OneProducerPath guards the single home for the service-ordering
// expression: the entry list is reachable only from the file that declares it,
// so a new producer has to go through Require and inherit its merge rule and its
// emitted shape instead of appending its own.
func TestDependsOn_OneProducerPath(t *testing.T) {
	t.Parallel()

	const (
		declaringFile = "types.go"
		guardFile     = "init_containers_test.go"
	)

	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read package dir: %v", err)
	}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || name == declaringFile || name == guardFile {
			continue
		}
		src, err := os.ReadFile(filepath.Join(".", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		for _, forbidden := range []string{"DependsOn.entries", "dependsOnEntry{"} {
			if bytes.Contains(src, []byte(forbidden)) {
				t.Errorf("%s reaches the depends_on entry list directly (%q); record dependencies through DependsOn.Require so the merge rule and the emitted shape stay in %s",
					name, forbidden, declaringFile)
			}
		}
	}
}

func serviceNames(frag ComposeFile) []string {
	out := make([]string, 0, len(frag.Services))
	for name := range frag.Services {
		out = append(out, name)
	}
	return out
}
