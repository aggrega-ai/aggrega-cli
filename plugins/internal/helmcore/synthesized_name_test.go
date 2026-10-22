// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package helmcore

import (
	"regexp"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/rendercore"
	"github.com/aggrega-ai/aggrega-cli/pkg/labels"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// The name grammars a Kubernetes API server holds each emitted name to. A Service
// name is an RFC 1035 label, so it opens with a letter; a pod volume name is an RFC
// 1123 label; a controller and a claim are RFC 1123 subdomains. A label value
// admits underscores, dots and capitals, which is why the selector may keep the
// identity as written.
var (
	rfc1123Label = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`)
	rfc1035Label = regexp.MustCompile(`^[a-z]([-a-z0-9]*[a-z0-9])?$`)
	labelValue   = regexp.MustCompile(`^(([A-Za-z0-9][-A-Za-z0-9_.]*)?[A-Za-z0-9])?$`)
)

func isLabelName(s string, grammar *regexp.Regexp) bool {
	return len(s) <= 63 && grammar.MatchString(s)
}

func isSubdomainName(s string) bool {
	if len(s) == 0 || len(s) > 253 {
		return false
	}
	for _, part := range strings.Split(s, ".") {
		if !isLabelName(part, rfc1123Label) {
			return false
		}
	}
	return true
}

// namingWorkload builds a workload of the given form that emits every kind of
// name the synthesized path composes from its naming basis: the controller, a
// Service per key, a standalone claim and the pod volume referencing it, a
// secret-file volume per slot it is given, and — for a stateful form — a headless
// governing Service and a claim template. A reference form emits its single
// aliasing Service.
//
// The keys are short on purpose. How long a Service key, a volume or a slot may be
// is the author's own budget against the length a name allows; what this fixture
// exercises is the part of every name the identity contributes.
func namingWorkload(form string, fileSlots ...string) *types.Workload {
	w := &types.Workload{APIVersion: "aggrega.ai/v1alpha1", Kind: "Workload", Form: form}
	if form == rendercore.WorkloadFormReference {
		w.Endpoint = "https://db.example.com:5432"
		w.Port = 5432
		return w
	}
	c := types.ContainerSpec{Image: "app:1.0"}
	c.Ports.Set("web", types.PortSpec{Port: 8080})
	for _, slot := range fileSlots {
		c.Attachments.Set(strings.ToUpper(slot), types.AttachmentRef{From: slot, As: "file", Path: "/etc/" + slot})
	}
	c.VolumeMounts = []types.VolumeMount{{Name: "data", Path: "/data"}}
	w.Containers.Set("main", c)
	w.Volumes.Set("data", types.VolumeSpec{Persistent: &types.PersistentVolume{Size: "1Gi"}})
	def := types.ServiceSpec{Type: "ClusterIP"}
	def.Ports.Set("web", 8080)
	w.Services.Set("default", def)
	if form == rendercore.WorkloadFormStateful {
		w.Volumes.Set("cache", types.VolumeSpec{PersistentTemplate: &types.PersistentTemplateVolume{Size: "1Gi"}})
		headless := types.ServiceSpec{Type: "Headless"}
		headless.Ports.Set("web", 8080)
		w.Services.Set("headless", headless)
	}
	return w
}

// namingProvides is the resolved provides of every slot the naming tests hand
// [namingWorkload] to deliver as a file.
func namingProvides() map[string]types.OrderedMap[string] {
	return map[string]types.OrderedMap[string]{
		"ca":                 providesBag("ca-bundle", "ca.crt"),
		"server_certificate": providesBag("server-tls", "tls.crt"),
	}
}

// synthesizedObjects is what one synthesis emitted, decoded by kind.
type synthesizedObjects struct {
	files        map[string]string // file name → kind
	stateless    *Deployment
	statefulSet  *StatefulSet
	services     []Service
	externalName *ExternalNameService
	claims       []PersistentVolumeClaim
}

func decodeSynthesized(t *testing.T, files []rendercore.EmittedFile) synthesizedObjects {
	t.Helper()
	got := synthesizedObjects{files: map[string]string{}}
	for _, f := range files {
		var head struct {
			Kind string `yaml:"kind"`
			Spec struct {
				Type string `yaml:"type"`
			} `yaml:"spec"`
		}
		if err := yaml.Unmarshal(f.Body, &head); err != nil {
			t.Fatalf("decode %s: %v", f.Name, err)
		}
		got.files[f.Name] = head.Kind
		var err error
		switch {
		case head.Kind == "Deployment":
			got.stateless = &Deployment{}
			err = yaml.Unmarshal(f.Body, got.stateless)
		case head.Kind == "StatefulSet":
			got.statefulSet = &StatefulSet{}
			err = yaml.Unmarshal(f.Body, got.statefulSet)
		case head.Kind == "Service" && head.Spec.Type == "ExternalName":
			got.externalName = &ExternalNameService{}
			err = yaml.Unmarshal(f.Body, got.externalName)
		case head.Kind == "Service":
			var svc Service
			err = yaml.Unmarshal(f.Body, &svc)
			got.services = append(got.services, svc)
		case head.Kind == "PersistentVolumeClaim":
			var pvc PersistentVolumeClaim
			err = yaml.Unmarshal(f.Body, &pvc)
			got.claims = append(got.claims, pvc)
		default:
			t.Fatalf("%s carries an unexpected kind %q", f.Name, head.Kind)
		}
		if err != nil {
			t.Fatalf("decode %s as %s: %v", f.Name, head.Kind, err)
		}
	}
	return got
}

// TestSynthesizedWorkload_NamesEveryResourceAfterTheReleaseName drives the helm
// synthesis for a component over the identity classes a legal component name can
// take, in each form that names resources differently, and reads every name the
// emitted documents carry back.
//
// Three properties are asserted together, because each alone is satisfiable by a
// wrong implementation. Every name is one the Kubernetes API server accepts for its
// kind. Every name is composed from ONE basis — the release name, written out
// literally in each row rather than computed, so a derivation reading the identity
// cannot pass by agreeing with itself — and that basis is exactly the name the
// provider reports to the host for the node, so an address a Recipe publishes from
// the report names a Service the node emits. And the selector keeps the identity
// as written while every selector agrees with the pod template it selects, which
// is what lets a label value carry characters a resource name refuses.
//
// The rows whose identity needs no escaping are the byte-identity controls: their
// names are spelled exactly as the node's own label value spells it.
func TestSynthesizedWorkload_NamesEveryResourceAfterTheReleaseName(t *testing.T) {
	t.Parallel()

	longest := strings.Repeat("abcdefghij", 5) + "abc"       // 53, the longest a release name may be
	longTail := strings.Repeat("cdefghijkl", 4) + "cdefghij" // 48

	// Every row delivers the slot `ca` as a file. A row whose basis leaves room
	// delivers `server_certificate` beside it, a slot holding an underscore, which
	// a pod volume name may not carry as written. The two longest identities leave
	// none: their basis and "-secret-ca" already fill the 63 characters a pod
	// volume name may hold.
	shortSlots := []string{"ca"}
	slots := []string{"ca", "server_certificate"}
	// slotSpelling is how each slot is written into a volume name, spelled out
	// literally for the same reason the basis is.
	slotSpelling := map[string]string{"ca": "ca", "server_certificate": "server-zucertificate"}

	cases := []struct {
		identity string
		basis    string
		// asLabel reports that the identity needs no escaping, so its names must be
		// spelled exactly as its label value is.
		asLabel   bool
		fileSlots []string
	}{
		{identity: "kv", basis: "kv", asLabel: true, fileSlots: slots},
		{identity: "synth_kv", basis: "synth-zukv", fileSlots: slots},
		{identity: "synthKv", basis: "synth-zx00004bv", fileSlots: slots},
		{identity: "synth_llm", basis: "synth-zullm", fileSlots: slots},
		{identity: "kvmod.inner_kv", basis: "kvmod-zdinner-zukv", fileSlots: slots},
		{identity: "kvmod.cache", basis: "kvmod-zdcache", asLabel: true, fileSlots: slots},
		{identity: longest, basis: longest, asLabel: true, fileSlots: shortSlots},
		{identity: "ab_" + longTail, basis: "ab-zu" + longTail, fileSlots: shortSlots},
	}
	forms := []string{rendercore.WorkloadFormStateless, rendercore.WorkloadFormStateful, rendercore.WorkloadFormReference}
	provides := namingProvides()

	for _, tc := range cases {
		for _, form := range forms {
			t.Run(tc.identity+"/"+form, func(t *testing.T) {
				t.Parallel()
				nodeCtx := nodeCtxFor(tc.identity)
				identityLabel := rendercore.ProvenanceComponentID(nodeCtx)

				reported := RenderedNameOutput(rendercore.RenderedNameInput{NodeID: tc.identity, Synthesized: true}).Name
				if reported != tc.basis {
					t.Fatalf("the provider reports %q for a synthesized %q, want %q", reported, tc.identity, tc.basis)
				}
				if tc.asLabel && tc.basis != identityLabel {
					t.Fatalf("%q needs no escaping, so its basis %q must be spelled as its label value %q", tc.identity, tc.basis, identityLabel)
				}
				if !labelValue.MatchString(identityLabel) || len(identityLabel) > 63 {
					t.Fatalf("the identity label %q is not a valid label value", identityLabel)
				}

				files, err := SynthesizeWorkloadCore(namingWorkload(form, tc.fileSlots...), provides, nodeCtx)
				if err != nil {
					t.Fatalf("SynthesizeWorkloadCore: %v", err)
				}
				got := decodeSynthesized(t, files)

				for name, kind := range got.files {
					if !strings.HasPrefix(name, tc.basis+"-") {
						t.Errorf("%s file %q is not named after the basis %q", kind, name, tc.basis)
					}
				}

				if form == rendercore.WorkloadFormReference {
					svc := got.externalName
					if svc == nil {
						t.Fatal("no ExternalName Service emitted")
					}
					if svc.Metadata.Name != reported || !isLabelName(svc.Metadata.Name, rfc1035Label) {
						t.Errorf("ExternalName Service %q, want the reported name %q as a valid Service name", svc.Metadata.Name, reported)
					}
					if svc.Metadata.Labels["app"] != identityLabel {
						t.Errorf("ExternalName Service app label %q, want the identity %q", svc.Metadata.Labels["app"], identityLabel)
					}
					if _, ok := got.files[reported+"-service.yaml"]; !ok {
						t.Errorf("no file %q among %v", reported+"-service.yaml", got.files)
					}
					return
				}

				// The controller.
				var controllerName string
				var selector map[string]string
				var pod PodTemplateSpec
				switch form {
				case rendercore.WorkloadFormStateful:
					if got.statefulSet == nil {
						t.Fatal("no StatefulSet emitted")
					}
					controllerName = got.statefulSet.Metadata.Name
					selector = got.statefulSet.Spec.Selector.MatchLabels
					pod = got.statefulSet.Spec.Template
					if want := reported + "-headless"; got.statefulSet.Spec.ServiceName != want {
						t.Errorf("StatefulSet serviceName %q, want %q", got.statefulSet.Spec.ServiceName, want)
					}
					if _, ok := got.files[reported+"-statefulset.yaml"]; !ok {
						t.Errorf("no file %q among %v", reported+"-statefulset.yaml", got.files)
					}
				default:
					if got.stateless == nil {
						t.Fatal("no Deployment emitted")
					}
					controllerName = got.stateless.Metadata.Name
					selector = got.stateless.Spec.Selector.MatchLabels
					pod = got.stateless.Spec.Template
					if _, ok := got.files[reported+"-deployment.yaml"]; !ok {
						t.Errorf("no file %q among %v", reported+"-deployment.yaml", got.files)
					}
				}
				if controllerName != reported || !isSubdomainName(controllerName) {
					t.Errorf("controller %q, want the reported name %q as a valid name", controllerName, reported)
				}
				if selector["app"] != identityLabel || pod.Metadata.Labels["app"] != identityLabel {
					t.Errorf("selector %v / pod labels %v, want app=%q on both", selector, pod.Metadata.Labels, identityLabel)
				}

				// The Services, each "<reported>-<key>", each selecting the pod template.
				wantServices := map[string]bool{reported + "-default": false}
				if form == rendercore.WorkloadFormStateful {
					wantServices[reported+"-headless"] = false
				}
				for _, svc := range got.services {
					if _, ok := wantServices[svc.Metadata.Name]; !ok {
						t.Errorf("unexpected Service %q", svc.Metadata.Name)
						continue
					}
					wantServices[svc.Metadata.Name] = true
					if !isLabelName(svc.Metadata.Name, rfc1035Label) {
						t.Errorf("Service %q is not a name a Kubernetes Service may carry", svc.Metadata.Name)
					}
					for k, v := range svc.Spec.Selector {
						if pod.Metadata.Labels[k] != v {
							t.Errorf("Service %q selects %s=%s, which the pod template does not carry", svc.Metadata.Name, k, v)
						}
					}
					if _, ok := got.files[svc.Metadata.Name+"-service.yaml"]; !ok {
						t.Errorf("no file %q among %v", svc.Metadata.Name+"-service.yaml", got.files)
					}
				}
				for name, seen := range wantServices {
					if !seen {
						t.Errorf("no Service %q emitted", name)
					}
				}

				// The standalone claim, and the pod volume naming it.
				if len(got.claims) != 1 {
					t.Fatalf("claims = %d, want 1", len(got.claims))
				}
				claim := got.claims[0].Metadata.Name
				if want := reported + "-data"; claim != want || !isSubdomainName(claim) {
					t.Errorf("claim %q, want %q as a valid name", claim, want)
				}
				var claimRef string
				var secretVolumes []string
				for _, v := range pod.Spec.Volumes {
					if !isLabelName(v.Name, rfc1123Label) {
						t.Errorf("pod volume %q is not a name a pod volume may carry", v.Name)
					}
					if v.PersistentVolumeClaim != nil {
						claimRef = v.PersistentVolumeClaim.ClaimName
					}
					if v.Secret != nil {
						secretVolumes = append(secretVolumes, v.Name)
					}
				}
				if claimRef != claim {
					t.Errorf("the pod volume claims %q, the emitted claim is %q", claimRef, claim)
				}
				var wantSecretVolumes []string
				for _, slot := range tc.fileSlots {
					wantSecretVolumes = append(wantSecretVolumes, reported+"-secret-"+slotSpelling[slot])
				}
				if !equalStrings(secretVolumes, wantSecretVolumes) {
					t.Errorf("secret-file volumes %v, want %v", secretVolumes, wantSecretVolumes)
				}
				mounts := mountNames(pod.Spec.Containers[0])
				for _, name := range mounts {
					if !isLabelName(name, rfc1123Label) {
						t.Errorf("the container mounts %q, which is not a name a pod volume may carry", name)
					}
				}
				for _, volName := range secretVolumes {
					if !containsName(mounts, volName) {
						t.Errorf("the container mounts %v, not the secret-file volume %q", mounts, volName)
					}
				}
			})
		}
	}
}

// TestSynthesizedWorkload_AnAttachmentKeepsItsComposedIdentity pins the one
// branch that does NOT take the release name. A Feature attachment's synthesized
// workload renders against its host's node identity, so a release name derived
// there would name the attachment's resources after the host; it is named after
// the attachment's composed identity instead, carried as it was composed.
func TestSynthesizedWorkload_AnAttachmentKeepsItsComposedIdentity(t *testing.T) {
	t.Parallel()
	composed := labels.ComposeComponentIdentity(labels.AttachmentScope, 63, "kv", "sidecar")
	nodeCtx := nodeCtxFor("kv")
	nodeCtx.LabelComponentID = composed

	files, err := SynthesizeWorkloadCore(namingWorkload(rendercore.WorkloadFormStateless, "ca"), namingProvides(), nodeCtx)
	if err != nil {
		t.Fatalf("SynthesizeWorkloadCore: %v", err)
	}
	got := decodeSynthesized(t, files)
	if got.stateless == nil || got.stateless.Metadata.Name != composed {
		t.Fatalf("attachment Deployment %v, want it named %q", got.stateless, composed)
	}
	if releaseName(nodeCtx) == composed {
		t.Fatalf("the host's release name %q equals the composed identity, so this case proves nothing", composed)
	}
	if svc := got.services[0].Metadata.Name; svc != composed+"-default" {
		t.Errorf("attachment Service %q, want %q", svc, composed+"-default")
	}
}

// TestRenderedNameOutput_AnswersForEveryInputKindThatNamesAfterTheReleaseName
// pins the answer the provider gives the host, per input kind a Recipe declares.
// Source and a synthesized workload both answer with the release name, because both
// render paths name a component's resources after it; a node declaring neither
// places an artifact set alone, whose resources its own author named, and gets no
// name.
func TestRenderedNameOutput_AnswersForEveryInputKindThatNamesAfterTheReleaseName(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		in   rendercore.RenderedNameInput
		want string
	}{
		{"sourced", rendercore.RenderedNameInput{NodeID: "my_kv", Sourced: true}, "my-zukv"},
		{"synthesized", rendercore.RenderedNameInput{NodeID: "my_kv", Synthesized: true}, "my-zukv"},
		{"both", rendercore.RenderedNameInput{NodeID: "my_kv", Sourced: true, Synthesized: true}, "my-zukv"},
		{"neither", rendercore.RenderedNameInput{NodeID: "my_kv"}, ""},
		{"a basis of its own", rendercore.RenderedNameInput{NodeID: "<marker>", DerivedNameBasis: "kv", Synthesized: true}, "kv"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			out := RenderedNameOutput(tc.in)
			if out.Name != tc.want || out.Error != "" {
				t.Errorf("RenderedNameOutput(%+v) = %+v, want name %q and no error", tc.in, out, tc.want)
			}
		})
	}
}

func containsName(names []string, want string) bool {
	for _, n := range names {
		if n == want {
			return true
		}
	}
	return false
}
