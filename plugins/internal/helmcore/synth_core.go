// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Filesystem-free Workload → K8s manifest synthesis core for the helm provider.
//
// SynthesizeWorkloadCore is the single byte-identical-critical synthesis path:
// it takes the evaluated Workload, the per-slot secret provides, and the node's
// render context, and returns the K8s manifest files. The helm provider Wasm
// guest, which reconstructs the Workload from the payload's workload projection,
// calls this one function, and nothing re-implements it, so a node's synthesized
// bytes have a single producer. This file carries no build tag: it compiles for
// the host and for the wasip1 guest alike, so it imports no host-only capability
// (no filesystem, no network).
//
// Translation contract (chapter 16 §3):
//
//   - Workload.Form selects the controller shape: stateless (and empty) →
//     Deployment, stateful → StatefulSet, daemon → DaemonSet, task → Job,
//     scheduled → CronJob with spec.schedule set from the workload's schedule
//     (a 5-field cron expression or an @-macro; a missing or malformed schedule
//     halts loudly via the shared companion validation), and spec.suspend: true
//     where the workload is suspended.
//   - A persistentTemplate volume requires form: stateful (companion
//     validation, single-sourced in rendercore); those volumes become the
//     StatefulSet's volumeClaimTemplates.
//   - One K8s Service per Workload.Services entry. An entry of type Loopback
//     halts, since a K8s Service cannot bind to a host's loopback address
//     (checkServiceTypes).
//   - One PVC per persistent volume (NOT persistentTemplate; those become
//     volumeClaimTemplates inside the StatefulSet spec).
//   - configmap / secret volume variants → K8s volume references by name
//     (no new ConfigMap or Secret resource emitted; those come from
//     artifacts or Features).
//   - ephemeral volume variant → emptyDir { sizeLimit }.
//   - Resource quantities (cpu, memory, extended resources, volume sizes) are
//     emitted verbatim, after each has been checked against the shared quantity
//     grammar; a value outside it halts the build (checkEmittedQuantities).
//
// Author-order preservation: containers, services, volumes, container
// ports, container variables, container attachments — all iterated via
// OrderedMap.All() so byte-identical input produces byte-identical
// output across compilations. A container's env lists its Secret-read
// attachment entries before its plain variables, each kind in author order
// (translateContainer says why).
//
// Filename convention: per-resource files named after the naming basis
// (synthesizedNameBasis) — "<basis>-<kind>.yaml" for the controller,
// "<basis>-<volume>-pvc.yaml" for a standalone claim, and
// "<basis>-<service-key>-service.yaml" for a Service.
//
// Secrets-as-env pattern: the synthesizer uses `env.valueFrom.secretKeyRef`
// for `as: env` secret entries (one env var per secret entry, naming the
// secret by the slot's `from:` value with the env var named by the
// container-side key). The alternative `envFrom`+secretRef pattern is
// reserved for future bulk-env-from-secret use; the per-entry pattern
// gives Recipe authors finer control over env var naming and is
// consistent across all secret references regardless of whether the slot
// is single-keyed or multi-keyed.

package helmcore

import (
	"fmt"
	"sort"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/rendercore"
	"github.com/aggrega-ai/aggrega-cli/internal/plugincondition"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

const (
	k8sStatefulSetAPIVersion = "apps/v1"
	k8sDaemonSetAPIVersion   = "apps/v1"
	k8sJobAPIVersion         = "batch/v1"
	k8sCronJobAPIVersion     = "batch/v1"
	k8sPVCAPIVersion         = "v1"
)

// SynthesizeWorkloadCore translates a parsed Workload into K8s manifest files
// for the helm provider. attachmentProvides maps each attachment slot to its
// resolved provides bag: a workload attachments entry's `from` is the slot; the generated
// Secret's identity (name, key) comes from the slot's provides. The
// referenceability check upstream guarantees every referenced slot carries the
// keys read below.
//
// Returns []EmittedFile{Name, Body} in a deterministic emission order:
// workload object first, then PVCs, then Services (each in author order). The
// order is load-bearing — callers preserve it rather than re-sorting.
func SynthesizeWorkloadCore(
	w *types.Workload,
	attachmentProvides map[string]types.OrderedMap[string],
	nodeCtx rendercore.NodeRenderContext,
) ([]rendercore.EmittedFile, error) {
	// Companion validation (provider-agnostic): a persistentTemplate volume
	// requires form: stateful. Single-sourced in rendercore and called at the
	// entry of every provider synth core.
	if err := rendercore.ValidateWorkloadForm(w, nodeCtx.NodeID); err != nil {
		return nil, err
	}

	// The controller shape is chosen by the workload's form; empty is
	// stateless.
	form := rendercore.NormalizeWorkloadForm(w.Form)

	// A reference workload has no compute: it emits a single ExternalName
	// Service aliasing an in-cluster name to the external endpoint, named after
	// the basis alone rather than after a key. No controller, no PVCs, no Secret
	// (the credential stays a pointer carried to consumers via provides, per the
	// AOT boundary).
	if !namesServicesByKey(form) {
		return synthesizeReference(w, nodeCtx)
	}

	// Every resource quantity the translation below copies into the manifests
	// is checked against the quantity grammar before any of it is translated,
	// so a value the cluster would refuse halts the build instead of reaching
	// the chart. A workload the input marks as produced from a declared value
	// is refused without quoting the value.
	if err := checkEmittedQuantities(w, nodeCtx.NodeID, nodeCtx.SensitiveWorkload); err != nil {
		return nil, err
	}

	// A service type the rendered Service cannot express halts here, before any
	// Service is translated, rather than reaching the chart as a type the
	// cluster would refuse.
	if err := checkServiceTypes(w, nodeCtx.NodeID); err != nil {
		return nil, err
	}

	labels := buildAggregaLabels(nodeCtx)
	annotations := buildAggregaAnnotations(nodeCtx)
	// componentID is what every synthesized resource BELONGS to, and it is read
	// only where that is the question: the selector label below, and the
	// provenance labels it merges with. A label value admits the identity as
	// written, underscores and capitals included, so the selector and the pod
	// template it selects keep agreeing whatever the component is called.
	componentID := rendercore.ProvenanceComponentID(nodeCtx)
	// name is what every synthesized resource is CALLED — metadata.name, the file
	// it is written to, and the prefix of every Service, standalone claim and
	// secret-file volume. See synthesizedNameBasis for why the two differ.
	name := synthesizedNameBasis(nodeCtx)
	selectorLabels := map[string]string{"app": componentID}
	resourceLabels := mergeLabels(selectorLabels, labels)
	resourceAnnotations := nilIfEmpty(annotations)
	namespace := rendercore.NamespaceFromAnnotations(nodeCtx.Annotations)
	saAnnotation := rendercore.ServiceAccountFromAnnotations(nodeCtx.Annotations)
	imageRegistry := rendercore.ImageRegistryFromAnnotations(nodeCtx.Annotations)
	pullSecretAnnotation := rendercore.ImagePullSecretFromAnnotations(nodeCtx.Annotations)

	var out []rendercore.EmittedFile

	// Containers: translate each in author-order.
	containers, err := translateContainers(w, name, attachmentProvides, imageRegistry)
	if err != nil {
		return nil, err
	}
	initContainers, err := translateInitContainers(w, name, attachmentProvides, imageRegistry)
	if err != nil {
		return nil, err
	}

	// Pod-spec volumes (excludes persistentTemplate variants).
	podVolumes := translatePodVolumes(w, name, attachmentProvides)

	// Pod-level configuration: scheduling, podSecurityContext,
	// serviceAccount, restartPolicy.
	podSpec := PodSpec{
		Containers:     containers,
		InitContainers: initContainers,
		Volumes:        podVolumes,
	}
	applyPodLevelFields(&podSpec, w, saAnnotation, pullSecretAnnotation)

	template := PodTemplateSpec{
		Metadata: ObjectMeta{Labels: resourceLabels},
		Spec:     podSpec,
	}

	replicas := int32(1)
	if w.Replicas != nil {
		replicas = int32(*w.Replicas)
	}

	// Services are translated before the workload object so a synthesized
	// StatefulSet can reference its governing headless Service by that Service's
	// own rendered name — reusing it (rather than re-deriving the string) keeps
	// the StatefulSet's serviceName and the emitted Service from ever drifting.
	services, err := translateServices(w, name, namespace, selectorLabels, resourceLabels, resourceAnnotations)
	if err != nil {
		return nil, err
	}

	// Controller object: one per form. stateless → Deployment, stateful →
	// StatefulSet, daemon → DaemonSet, task → Job, scheduled → CronJob whose
	// spec.schedule is the workload's schedule (validated by the shared
	// companion check above). Whether a form emits a CronJob is read through
	// emitsCronJob, the predicate the CronJob name verdict reads too.
	switch {
	case form == rendercore.WorkloadFormStateful:
		governingService, gerr := statefulSetGoverningService(services, nodeCtx.NodeID)
		if gerr != nil {
			return nil, gerr
		}
		ss := StatefulSet{
			APIVersion: k8sStatefulSetAPIVersion,
			Kind:       "StatefulSet",
			Metadata:   ObjectMeta{Name: name, Namespace: namespace, Labels: resourceLabels, Annotations: resourceAnnotations},
			Spec: StatefulSetSpec{
				Replicas:             &replicas,
				Selector:             &LabelSelector{MatchLabels: selectorLabels},
				ServiceName:          governingService,
				Template:             template,
				VolumeClaimTemplates: translateVolumeClaimTemplates(w),
			},
		}
		body, err := marshalDeterministic(&ss, nodeCtx.YAMLIndent)
		if err != nil {
			return nil, cerrors.Wrap(err, cerrors.TranslationError, cerrors.SourceLocation{File: nodeCtx.NodeID}, fmt.Sprintf("helm: node %q statefulset marshal", nodeCtx.NodeID))
		}
		out = append(out, rendercore.EmittedFile{
			Name: name + "-statefulset.yaml",
			Body: body,
		})
	case form == rendercore.WorkloadFormDaemon:
		ds := DaemonSet{
			APIVersion: k8sDaemonSetAPIVersion,
			Kind:       "DaemonSet",
			Metadata:   ObjectMeta{Name: name, Namespace: namespace, Labels: resourceLabels, Annotations: resourceAnnotations},
			Spec: DaemonSetSpec{
				Selector: &LabelSelector{MatchLabels: selectorLabels},
				Template: template,
			},
		}
		body, err := marshalDeterministic(&ds, nodeCtx.YAMLIndent)
		if err != nil {
			return nil, cerrors.Wrap(err, cerrors.TranslationError, cerrors.SourceLocation{File: nodeCtx.NodeID}, fmt.Sprintf("helm: node %q daemonset marshal", nodeCtx.NodeID))
		}
		out = append(out, rendercore.EmittedFile{
			Name: name + "-daemonset.yaml",
			Body: body,
		})
	case form == rendercore.WorkloadFormTask:
		job := Job{
			APIVersion: k8sJobAPIVersion,
			Kind:       "Job",
			Metadata:   ObjectMeta{Name: name, Namespace: namespace, Labels: resourceLabels, Annotations: resourceAnnotations},
			Spec:       JobSpec{Template: jobValidTemplate(template)},
		}
		body, err := marshalDeterministic(&job, nodeCtx.YAMLIndent)
		if err != nil {
			return nil, cerrors.Wrap(err, cerrors.TranslationError, cerrors.SourceLocation{File: nodeCtx.NodeID}, fmt.Sprintf("helm: node %q job marshal", nodeCtx.NodeID))
		}
		out = append(out, rendercore.EmittedFile{
			Name: name + "-job.yaml",
			Body: body,
		})
	case emitsCronJob(form):
		cron := CronJob{
			APIVersion: k8sCronJobAPIVersion,
			Kind:       "CronJob",
			Metadata:   ObjectMeta{Name: name, Namespace: namespace, Labels: resourceLabels, Annotations: resourceAnnotations},
			Spec: CronJobSpec{
				Schedule:    w.Schedule,
				Suspend:     w.Suspend,
				JobTemplate: JobTemplateSpec{Spec: JobSpec{Template: jobValidTemplate(template)}},
			},
		}
		body, err := marshalDeterministic(&cron, nodeCtx.YAMLIndent)
		if err != nil {
			return nil, cerrors.Wrap(err, cerrors.TranslationError, cerrors.SourceLocation{File: nodeCtx.NodeID}, fmt.Sprintf("helm: node %q cronjob marshal", nodeCtx.NodeID))
		}
		out = append(out, rendercore.EmittedFile{
			Name: name + "-cronjob.yaml",
			Body: body,
		})
	default:
		dep := Deployment{
			APIVersion: k8sDeploymentAPIVersion,
			Kind:       "Deployment",
			Metadata:   ObjectMeta{Name: name, Namespace: namespace, Labels: resourceLabels, Annotations: resourceAnnotations},
			Spec: DeploymentSpec{
				Replicas: &replicas,
				Selector: &LabelSelector{MatchLabels: selectorLabels},
				Template: template,
			},
		}
		body, err := marshalDeterministic(&dep, nodeCtx.YAMLIndent)
		if err != nil {
			return nil, cerrors.Wrap(err, cerrors.TranslationError, cerrors.SourceLocation{File: nodeCtx.NodeID}, fmt.Sprintf("helm: node %q deployment marshal", nodeCtx.NodeID))
		}
		out = append(out, rendercore.EmittedFile{
			Name: name + "-deployment.yaml",
			Body: body,
		})
	}

	// PVCs: one per persistent volume (NOT persistentTemplate; those
	// become volumeClaimTemplates inside the StatefulSet spec).
	pvcs := translatePersistentPVCs(w, name, namespace, resourceLabels, resourceAnnotations)
	for _, pvc := range pvcs {
		body, err := marshalDeterministic(&pvc, nodeCtx.YAMLIndent)
		if err != nil {
			return nil, cerrors.Wrap(err, cerrors.TranslationError, cerrors.SourceLocation{File: nodeCtx.NodeID}, fmt.Sprintf("helm: node %q pvc marshal", nodeCtx.NodeID))
		}
		out = append(out, rendercore.EmittedFile{
			Name: pvc.Metadata.Name + "-pvc.yaml",
			Body: body,
		})
	}

	// Services: emit (in author-order) the services translated above.
	for _, svc := range services {
		body, err := marshalDeterministic(&svc, nodeCtx.YAMLIndent)
		if err != nil {
			return nil, cerrors.Wrap(err, cerrors.TranslationError, cerrors.SourceLocation{File: nodeCtx.NodeID}, fmt.Sprintf("helm: node %q service marshal", nodeCtx.NodeID))
		}
		out = append(out, rendercore.EmittedFile{
			Name: svc.Metadata.Name + "-service.yaml",
			Body: body,
		})
	}

	return out, nil
}

// synthesizeReference emits the render shape for a form: reference workload —
// one ExternalName Service aliasing an in-cluster name (the naming basis) to the
// external endpoint host, with the optional port. It emits no controller and no
// Secret: the credential is a pointer the recipe's provides carry to consumers
// (AOT boundary — the secret value is never resolved here).
func synthesizeReference(w *types.Workload, nodeCtx rendercore.NodeRenderContext) ([]rendercore.EmittedFile, error) {
	labels := buildAggregaLabels(nodeCtx)
	annotations := buildAggregaAnnotations(nodeCtx)
	componentID := rendercore.ProvenanceComponentID(nodeCtx)
	name := synthesizedNameBasis(nodeCtx)
	resourceLabels := mergeLabels(map[string]string{"app": componentID}, labels)
	namespace := rendercore.NamespaceFromAnnotations(nodeCtx.Annotations)

	spec := ExternalNameServiceSpec{
		Type:         "ExternalName",
		ExternalName: externalNameHost(w.Endpoint),
	}
	if w.Port != 0 {
		spec.Ports = []ExternalNamePort{{Port: int32(w.Port)}}
	}
	svc := ExternalNameService{
		APIVersion: k8sServiceAPIVersion,
		Kind:       "Service",
		Metadata:   ObjectMeta{Name: name, Namespace: namespace, Labels: resourceLabels, Annotations: nilIfEmpty(annotations)},
		Spec:       spec,
	}
	body, err := marshalDeterministic(&svc, nodeCtx.YAMLIndent)
	if err != nil {
		return nil, cerrors.Wrap(err, cerrors.TranslationError, cerrors.SourceLocation{File: nodeCtx.NodeID}, fmt.Sprintf("helm: node %q externalname service marshal", nodeCtx.NodeID))
	}
	return []rendercore.EmittedFile{{Name: name + "-service.yaml", Body: body}}, nil
}

// synthesizedNameBasis is the one name every resource the synthesized path emits
// for a node is called by: its metadata.name, the file it is written to, and the
// prefix of every Service, standalone claim and secret-file volume it declares.
//
// For a component it is the node's release name, [releaseName] — the derivation a
// chart's own templates render against, and the name [RenderedNameOutput] reports
// for the node. So a Recipe publishing an address from that report hands its
// consumers the name the node's Services actually carry, whichever input kind
// rendered them, and no second encoding exists to drift from the first. The
// release-name alphabet admits only lowercase letters and digits and escapes
// everything else, which is what a Kubernetes resource name accepts: an identity
// carrying an underscore or a capital contributes a part every such name admits,
// and one carrying neither is spelled exactly as the node's own label value
// spells it.
//
// For a Feature attachment it is the attachment's composed identity, as
// [rendercore.ProvenanceComponentID] reports it, and deliberately not a release
// name. An attachment's render carries its HOST's node identity, so a release name
// derived from it would name the attachment's workload after the host and collide
// with the host's own; the composed identity carries the attachment's own key and
// is what keeps the two apart.
//
// Label values do not read this. The selector and the provenance labels say what
// a resource belongs to, and a label value admits the identity as written.
func synthesizedNameBasis(nodeCtx rendercore.NodeRenderContext) string {
	if nodeCtx.LabelComponentID != "" {
		return synthesizedNameFor(rendercore.ProvenanceComponentID(nodeCtx), true)
	}
	return synthesizedNameFor(rendercore.TargetNameBasis(nodeCtx), false)
}

// synthesizedNameFor is the one derivation of that name from an identity and the
// state the identity is in: a composed identity, already in an alphabet every
// resource name admits, names the resources as it stands; any other identity
// names them as the release name derived from it, through [ReleaseNameFor].
//
// It is held apart from the render context so the answer to the name-bound
// question measures the very name the resources will carry. That answer holds a
// proposed identity and whether it was composed, not a context, and a second
// spelling of this choice there would agree with this one on the day it was
// written and let a CronJob or a Service be judged under one name and emitted
// under another.
func synthesizedNameFor(identity string, composed bool) string {
	if composed {
		return identity
	}
	return ReleaseNameFor(identity, "")
}

// serviceNameFor is the one spelling of the name a Service this target
// synthesizes carries: the naming basis and the Service's key, joined by a
// hyphen.
//
// The Service-name verdict measures the name this returns rather than adding up
// its parts, so the name judged and the name emitted are one value: a change to
// the join here moves both, and no arithmetic elsewhere is left describing the
// old one.
func serviceNameFor(basis, key string) string {
	return basis + "-" + key
}

// namesServicesByKey reports whether a workload of this form names its Services
// after the basis and each key. A reference workload does not: it emits one
// Service aliasing an external endpoint, named after the basis alone. The
// synthesizer dispatches on it and the Service-name verdict reads it, so the
// form the verdict skips is the form that emits no keyed Service.
func namesServicesByKey(form string) bool {
	return rendercore.NormalizeWorkloadForm(form) != rendercore.WorkloadFormReference
}

// emitsCronJob reports whether a workload of this form is synthesized as a
// CronJob. The synthesizer dispatches on it and the CronJob-name verdict reads
// it, so the verdict judges a CronJob name exactly where one is emitted.
func emitsCronJob(form string) bool {
	return rendercore.NormalizeWorkloadForm(form) == rendercore.WorkloadFormScheduled
}

// externalNameHost extracts the bare DNS host from an endpoint that may carry a
// scheme, a path, or a port. K8s externalName must be a bare host.
func externalNameHost(endpoint string) string {
	s := endpoint
	if i := strings.Index(s, "://"); i >= 0 {
		s = s[i+3:]
	}
	if i := strings.IndexByte(s, '/'); i >= 0 {
		s = s[:i]
	}
	if i := strings.IndexByte(s, ':'); i >= 0 {
		s = s[:i]
	}
	return s
}

// jobValidTemplate returns a copy of the pod template with a Job-valid
// restartPolicy. A Job (and a CronJob's per-run Job) pod template restartPolicy
// must be Never or OnFailure; Always (the pod default) is invalid. An author-set
// OnFailure is preserved; every other value is pinned to Never. Shared by the
// task (Job) and scheduled (CronJob) arms so the restartPolicy rule lives in one
// place.
func jobValidTemplate(template PodTemplateSpec) PodTemplateSpec {
	if template.Spec.RestartPolicy != "OnFailure" {
		template.Spec.RestartPolicy = "Never"
	}
	return template
}

// statefulSetGoverningService returns the rendered name of the single headless
// Service that governs a synthesized StatefulSet, set as spec.serviceName. A
// StatefulSet requires exactly one headless governing Service; a headless
// Service is the one translateServices marked clusterIP: None. Reusing the
// rendered Service's name (not re-deriving it) guarantees serviceName references
// a Service that is actually emitted. Zero or several headless Services halt
// loudly rather than emit a StatefulSet K8s would reject.
func statefulSetGoverningService(services []Service, nodeID string) (string, error) {
	var name string
	count := 0
	for _, svc := range services {
		if svc.Spec.ClusterIP == "None" {
			name = svc.Metadata.Name
			count++
		}
	}
	switch count {
	case 1:
		return name, nil
	case 0:
		return "", cerrors.Wrap(plugincondition.ErrPluginRequiredInputUnresolved(),
			cerrors.TranslationError, cerrors.SourceLocation{File: nodeID},
			"synthesized StatefulSet requires a headless governing Service (declare a service with type: Headless); none found: synthesized StatefulSet has no headless governing Service")
	default:
		return "", cerrors.Wrap(plugincondition.ErrPluginRequiredInputUnresolved(),
			cerrors.TranslationError, cerrors.SourceLocation{File: nodeID},
			"synthesized StatefulSet declares more than one headless Service; the governing Service is ambiguous (declare exactly one type: Headless service): synthesized StatefulSet has more than one headless Service")
	}
}

// translateContainers walks Workload.Containers in author order and produces
// K8s-shape Container values. Returns an error if any container's
// translation fails (e.g., probe http: with no available port). basis is the
// naming basis the workload's resources are called by (synthesizedNameBasis).
func translateContainers(w *types.Workload, basis string, attachmentProvides map[string]types.OrderedMap[string], imageRegistry string) ([]Container, error) {
	var out []Container
	for name, c := range w.Containers.All() {
		k8sContainer, err := translateContainer(name, c, basis, attachmentProvides, imageRegistry)
		if err != nil {
			return nil, err
		}
		out = append(out, k8sContainer)
	}
	return out, nil
}

// translateInitContainers walks Workload.InitContainers (positional list),
// preserving the authored order the sequential init sequence relies on. An
// entry's authored name is honored as the emitted container name; an entry
// without a name falls back to "init-<index>", where the index is the entry's
// position in the list.
func translateInitContainers(w *types.Workload, basis string, attachmentProvides map[string]types.OrderedMap[string], imageRegistry string) ([]Container, error) {
	if len(w.InitContainers) == 0 {
		return nil, nil
	}
	var out []Container
	for i, c := range w.InitContainers {
		name := c.Name
		if name == "" {
			name = fmt.Sprintf("init-%d", i)
		}
		k8sContainer, err := translateContainer(name, c, basis, attachmentProvides, imageRegistry)
		if err != nil {
			return nil, err
		}
		out = append(out, k8sContainer)
	}
	return out, nil
}

// translateContainer maps a single Workload ContainerSpec to a K8s-shape
// Container. Ports, attachments, variables, envFrom sources and volume mounts
// each keep their authored order.
//
// The env list carries two kinds of entry, and the Secret-read ones come first:
// every attachment delivered `as: env`, in authored order, then every plain
// variable, in authored order. Kubernetes expands a `$(NAME)` reference in an
// env value from the entries listed before it in the same container, never
// from one listed after it, so this order lets a plain value compose a
// Secret-read value by reference — a database URL written as
// `postgres://app:$(DB_PASSWORD)@db:5432/app` — while the password itself stays
// a secretKeyRef. A plain value may also reference any variable listed before
// it. The compose provider writes the same sequence, and
// TestEnvOrderParity_ComposeMatchesHelm holds the two providers to it.
//
// When imageRegistry is non-empty, the container image ref is relocated under
// that registry prefix (regular and init containers share this single site, so
// the env order above holds for init containers too).
func translateContainer(name string, c types.ContainerSpec, basis string, attachmentProvides map[string]types.OrderedMap[string], imageRegistry string) (Container, error) {
	out := Container{
		Name:            sanitizeContainerName(name),
		Image:           rendercore.ApplyImageRegistry(c.Image, imageRegistry),
		ImagePullPolicy: c.ImagePullPolicy,
		Command:         c.Command,
		Args:            c.Args,
	}

	// Container ports (named) — preserves Workload.Containers.<n>.Ports
	// author order. A declared transport is emitted verbatim; an undeclared one
	// emits no protocol key, leaving the cluster's own default in force.
	for portName, port := range c.Ports.All() {
		out.Ports = append(out.Ports, ContainerPort{
			Name:          portName,
			ContainerPort: int32(port.Port),
			Protocol:      port.Protocol,
		})
	}

	// Secrets — for each entry, use env.valueFrom.secretKeyRef (env mode)
	// or volume+volumeMount (file mode; the volume is added to the pod
	// spec elsewhere). Author order preserved. `ref.From` is the slot; the
	// referenced Secret's name comes from the slot's resolved provides
	// (name). The data key is the entry's explicit `ref.Key` when set —
	// which lets two entries read different keys of one multi-key secret —
	// otherwise the slot's single published `key`. The env-var name stays
	// the entry's map key.
	//
	// 🔴 This loop runs BEFORE the plain variables below, so every Secret-read
	// env entry precedes every plain one and a plain value can reference it as
	// `$(NAME)`. Moving it after the variables would leave such a reference
	// unexpanded at runtime while the build still succeeds. File-mode mounts
	// land here too, so they still precede the declared volumeMounts.
	for secretKey, ref := range c.Attachments.All() {
		provides := attachmentProvides[ref.From]
		switch ref.As {
		case "env":
			secretName, _ := provides.Get("name")
			dataKey := ref.Key
			if dataKey == "" {
				dataKey, _ = provides.Get("key")
			}
			out.Env = append(out.Env, EnvVar{
				Name: secretKey,
				ValueFrom: &EnvVarSource{
					SecretKeyRef: &SecretKeySelector{
						Name: secretName,
						Key:  dataKey,
					},
				},
			})
		case "file":
			out.VolumeMounts = append(out.VolumeMounts, VolumeMount{
				Name:      secretVolumeName(basis, ref.From),
				MountPath: ref.Path,
				ReadOnly:  true,
			})
		}
	}

	// Plain environment variables, in Workload.Containers.<n>.Variables author
	// order, after every Secret-read entry above.
	for varName, varValue := range c.Variables.All() {
		out.Env = append(out.Env, EnvVar{Name: varName, Value: varValue})
	}

	// Bulk env-from-source (envFrom), author order preserved. A configMap
	// source becomes a configMapRef by name; a secret source becomes a
	// secretRef whose name resolves from the slot's provides. Each carries the
	// entry's optional prefix.
	for _, ef := range c.EnvFrom {
		switch {
		case ef.ConfigMap != "":
			out.EnvFrom = append(out.EnvFrom, EnvFromSource{
				ConfigMapRef: &LocalObjectReference{Name: ef.ConfigMap},
				Prefix:       ef.Prefix,
			})
		case ef.Secret != "":
			provides := attachmentProvides[ef.Secret]
			secretName, _ := provides.Get("name")
			out.EnvFrom = append(out.EnvFrom, EnvFromSource{
				SecretRef: &LocalObjectReference{Name: secretName},
				Prefix:    ef.Prefix,
			})
		}
	}

	// Resources (typed pointer; non-nil only when Workload declared them).
	if c.Resources != nil {
		out.Resources = translateResources(c.Resources)
	}

	// VolumeMounts (typed list).
	for _, vm := range c.VolumeMounts {
		out.VolumeMounts = append(out.VolumeMounts, VolumeMount{
			Name:      vm.Name,
			MountPath: vm.Path,
			SubPath:   vm.SubPath,
			ReadOnly:  vm.ReadOnly,
		})
	}

	// Probes (3 disjunctive shapes per probe).
	if c.Health != nil {
		ready, err := translateProbe(c.Health.Ready, out.Ports)
		if err != nil {
			return Container{}, cerrors.Wrap(err, cerrors.TranslationError, cerrors.SourceLocation{File: name}, fmt.Sprintf("container %q readiness probe", name))
		}
		out.ReadinessProbe = ready

		live, err := translateProbe(c.Health.Live, out.Ports)
		if err != nil {
			return Container{}, cerrors.Wrap(err, cerrors.TranslationError, cerrors.SourceLocation{File: name}, fmt.Sprintf("container %q liveness probe", name))
		}
		out.LivenessProbe = live

		startup, err := translateProbe(c.Health.Startup, out.Ports)
		if err != nil {
			return Container{}, cerrors.Wrap(err, cerrors.TranslationError, cerrors.SourceLocation{File: name}, fmt.Sprintf("container %q startup probe", name))
		}
		out.StartupProbe = startup
	}

	// Lifecycle (postStart/preStop hooks).
	if c.Lifecycle != nil {
		out.Lifecycle = translateLifecycle(c.Lifecycle)
	}

	// Container security context.
	if c.SecurityContext != nil {
		out.SecurityContext = translateContainerSecurityContext(c.SecurityContext)
	}

	return out, nil
}

// translateResources copies requests/limits maps from the Workload's
// ResourceSpec to K8s ResourceRequirements. The values are copied verbatim;
// checkEmittedQuantities has already refused any that is not a quantity.
func translateResources(r *types.ResourceSpec) *ResourceRequirements {
	out := &ResourceRequirements{}
	if r.Requests != nil {
		out.Requests = make(map[string]string)
		if r.Requests.CPU != "" {
			out.Requests["cpu"] = r.Requests.CPU
		}
		if r.Requests.Memory != "" {
			out.Requests["memory"] = r.Requests.Memory
		}
		for k, v := range r.Requests.Extended.All() {
			out.Requests[k] = v
		}
	}
	if r.Limits != nil {
		out.Limits = make(map[string]string)
		if r.Limits.CPU != "" {
			out.Limits["cpu"] = r.Limits.CPU
		}
		if r.Limits.Memory != "" {
			out.Limits["memory"] = r.Limits.Memory
		}
		for k, v := range r.Limits.Extended.All() {
			out.Limits[k] = v
		}
	}
	return out
}

// translateProbe maps a Workload ProbeSpec to a K8s Probe. Returns nil for
// nil input. Returns an error if http: is set but no container port is
// available for resolution.
func translateProbe(p *types.ProbeSpec, containerPorts []ContainerPort) (*Probe, error) {
	if p == nil {
		return nil, nil
	}
	out := &Probe{
		TimeoutSeconds:      ptrToInt32(p.TimeoutSeconds),
		InitialDelaySeconds: ptrToInt32(p.InitialDelaySeconds),
		PeriodSeconds:       ptrToInt32(p.PeriodSeconds),
		FailureThreshold:    ptrToInt32(p.FailureThreshold),
		SuccessThreshold:    ptrToInt32(p.SuccessThreshold),
	}

	switch {
	case p.HTTP != "":
		port, err := resolveHTTPProbePort(containerPorts)
		if err != nil {
			return nil, err
		}
		out.HTTPGet = &HTTPGetAction{Path: p.HTTP, Port: port}
	case p.TCP != 0:
		out.TCPSocket = &TCPSocketAction{Port: int32(p.TCP)}
	case len(p.Exec) > 0:
		out.Exec = &ExecAction{Command: p.Exec}
	default:
		return nil, cerrors.Unidentified(cerrors.TranslationError, cerrors.SourceLocation{}, "probe declares no shape (http/tcp/exec)", nil, nil)
	}

	return out, nil
}

// resolveHTTPProbePort returns the first container port name in alphabetical
// order, suitable for use as a named-port reference in httpGet. Returns an
// error if no container ports are declared (TRANSLATION_ERROR).
func resolveHTTPProbePort(containerPorts []ContainerPort) (string, error) {
	if len(containerPorts) == 0 {
		return "", cerrors.Unidentified(cerrors.TranslationError, cerrors.SourceLocation{}, "probe http: requires at least one named container port; container declares none", nil, nil)
	}
	names := make([]string, 0, len(containerPorts))
	for _, p := range containerPorts {
		if p.Name != "" {
			names = append(names, p.Name)
		}
	}
	if len(names) == 0 {
		return "", cerrors.Unidentified(cerrors.TranslationError, cerrors.SourceLocation{}, "probe http: requires at least one NAMED container port; container ports lack name fields", nil, nil)
	}
	sort.Strings(names)
	return names[0], nil
}

// translateLifecycle maps Workload LifecycleSpec to K8s Lifecycle.
func translateLifecycle(l *types.LifecycleSpec) *Lifecycle {
	out := &Lifecycle{}
	if l.PostStart != nil {
		out.PostStart = translateLifecycleHandler(l.PostStart)
	}
	if l.PreStop != nil {
		out.PreStop = translateLifecycleHandler(l.PreStop)
	}
	return out
}

func translateLifecycleHandler(h *types.LifecycleHookSpec) *LifecycleHandler {
	out := &LifecycleHandler{}
	if len(h.Exec) > 0 {
		out.Exec = &ExecAction{Command: h.Exec}
	}
	if h.HTTPGet != nil {
		out.HTTPGet = h.HTTPGet
	}
	return out
}

// translateContainerSecurityContext copies the container-level security
// identity to its K8s shape.
func translateContainerSecurityContext(s *types.ContainerSecurityContext) *ContainerSecurityContext {
	out := &ContainerSecurityContext{
		RunAsUser:                ptrToInt64FromInt(s.RunAsUser),
		RunAsGroup:               ptrToInt64FromInt(s.RunAsGroup),
		RunAsNonRoot:             s.RunAsNonRoot,
		ReadOnlyRootFilesystem:   s.ReadOnlyRootFilesystem,
		AllowPrivilegeEscalation: s.AllowPrivilegeEscalation,
	}
	if s.Capabilities != nil {
		out.Capabilities = &Capabilities{
			Add:  s.Capabilities.Add,
			Drop: s.Capabilities.Drop,
		}
	}
	return out
}

// translatePodVolumes walks Workload.Volumes and produces pod-spec Volumes.
// Excludes persistentTemplate variants (those become volumeClaimTemplates
// on the StatefulSet, not pod-spec volumes). Author order preserved.
//
// Also adds secret-volume entries for container attachments entries that use
// `as: file` — one volume per slot, named per the convention
// "<basis>-secret-<slot>" with the slot written out by secretVolumeName.
// The volume name stays convention-based (pod-internal); the
// SecretVolumeSource.SecretName it mounts is the generated Secret's name, read
// from the slot's resolved provides.
func translatePodVolumes(w *types.Workload, basis string, attachmentProvides map[string]types.OrderedMap[string]) []Volume {
	var out []Volume

	for name, v := range w.Volumes.All() {
		switch {
		case v.Ephemeral != nil:
			out = append(out, Volume{
				Name: name,
				EmptyDir: &EmptyDirVolumeSource{
					SizeLimit: v.Ephemeral.SizeLimit,
				},
			})
		case v.ConfigMap != nil:
			out = append(out, Volume{
				Name: name,
				ConfigMap: &ConfigMapVolumeSource{
					Name: v.ConfigMap.Source,
				},
			})
		case v.Secret != nil:
			out = append(out, Volume{
				Name: name,
				Secret: &SecretVolumeSource{
					SecretName: v.Secret.Source,
				},
			})
		case v.Persistent != nil:
			out = append(out, Volume{
				Name: name,
				PersistentVolumeClaim: &PersistentVolumeClaimVolumeSource{
					ClaimName: persistentClaimName(basis, name),
				},
			})
			// persistentTemplate intentionally omitted — handled at
			// StatefulSet.Spec.VolumeClaimTemplates.
		}
	}

	// Add secret-as-file volumes (one per unique slot referenced via
	// `as: file` across all containers). Author-order is established by
	// container iteration; deduplicated by slot name.
	seenSecretVolumes := map[string]struct{}{}
	for _, c := range w.Containers.All() {
		for _, ref := range c.Attachments.All() {
			if ref.As != "file" {
				continue
			}
			volName := secretVolumeName(basis, ref.From)
			if _, seen := seenSecretVolumes[volName]; seen {
				continue
			}
			seenSecretVolumes[volName] = struct{}{}
			provides := attachmentProvides[ref.From]
			secretName, _ := provides.Get("name")
			out = append(out, Volume{
				Name: volName,
				Secret: &SecretVolumeSource{
					SecretName: secretName,
				},
			})
		}
	}
	for _, c := range w.InitContainers {
		for _, ref := range c.Attachments.All() {
			if ref.As != "file" {
				continue
			}
			volName := secretVolumeName(basis, ref.From)
			if _, seen := seenSecretVolumes[volName]; seen {
				continue
			}
			seenSecretVolumes[volName] = struct{}{}
			provides := attachmentProvides[ref.From]
			secretName, _ := provides.Get("name")
			out = append(out, Volume{
				Name: volName,
				Secret: &SecretVolumeSource{
					SecretName: secretName,
				},
			})
		}
	}

	return out
}

// translateVolumeClaimTemplates produces the StatefulSet.spec.
// volumeClaimTemplates list, one entry per persistentTemplate volume.
// Author order preserved. A claim template is named by its volume alone: the
// StatefulSet controller prefixes each claim it creates from one, so no naming
// basis is written here.
func translateVolumeClaimTemplates(w *types.Workload) []PersistentVolumeClaim {
	var out []PersistentVolumeClaim
	for name, v := range w.Volumes.All() {
		if v.PersistentTemplate == nil {
			continue
		}
		pvc := PersistentVolumeClaim{
			APIVersion: k8sPVCAPIVersion,
			Kind:       "PersistentVolumeClaim",
			Metadata:   ObjectMeta{Name: name},
			Spec:       buildPVCSpec(v.PersistentTemplate.Size, v.PersistentTemplate.Class, v.PersistentTemplate.AccessModes),
		}
		out = append(out, pvc)
	}
	return out
}

// persistentClaimName is the name of the standalone claim a persistent volume is
// backed by. The claim itself and the pod volume referencing it both read it here,
// so the reference always names the claim that is emitted.
func persistentClaimName(basis, volume string) string {
	return basis + "-" + volume
}

// translatePersistentPVCs produces standalone PVC resources (one per
// persistent volume; not persistentTemplate). Author order preserved.
func translatePersistentPVCs(w *types.Workload, basis, namespace string, labels map[string]string, annotations map[string]string) []PersistentVolumeClaim {
	var out []PersistentVolumeClaim
	for name, v := range w.Volumes.All() {
		if v.Persistent == nil {
			continue
		}
		pvc := PersistentVolumeClaim{
			APIVersion: k8sPVCAPIVersion,
			Kind:       "PersistentVolumeClaim",
			Metadata: ObjectMeta{
				Name:        persistentClaimName(basis, name),
				Namespace:   namespace,
				Labels:      labels,
				Annotations: annotations,
			},
			Spec: buildPVCSpec(v.Persistent.Size, v.Persistent.Class, v.Persistent.AccessModes),
		}
		out = append(out, pvc)
	}
	return out
}

// buildPVCSpec creates the PersistentVolumeClaimSpec from a size + optional
// storage class + optional access modes. When accessModes is empty it defaults
// to [ReadWriteOnce]; an author-supplied list is emitted verbatim (author
// order preserved).
func buildPVCSpec(size, class string, accessModes []string) PersistentVolumeClaimSpec {
	modes := accessModes
	if len(modes) == 0 {
		modes = []string{"ReadWriteOnce"}
	}
	spec := PersistentVolumeClaimSpec{
		AccessModes: modes,
		Resources: ResourceRequirements{
			Requests: map[string]string{"storage": size},
		},
	}
	if class != "" {
		c := class
		spec.StorageClassName = &c
	}
	return spec
}

// translateServices walks Workload.Services in author order and produces K8s
// Service resources, each named by [serviceNameFor]. targetPort resolution:
// look up the port name across all containers; error if not found.
func translateServices(w *types.Workload, basis, namespace string, selectorLabels, resourceLabels map[string]string, resourceAnnotations map[string]string) ([]Service, error) {
	var out []Service

	containerPortMap := w.ContainerPortsByName()

	for svcName, svcSpec := range w.Services.All() {
		ports := []ServicePort{}
		for portName, portNum := range svcSpec.Ports.All() {
			targetPort, ok := containerPortMap[portName]
			if !ok {
				return nil, cerrors.Unidentified(cerrors.TranslationError, cerrors.SourceLocation{}, fmt.Sprintf("service %q port %q has no matching container port name", svcName, portName), nil, nil)
			}
			_ = targetPort.Port // present for forward compatibility (numeric backref);
			// today targetPort uses the named-port string, which K8s
			// resolves at runtime to the matching containerPort.
			//
			// The transport comes from the container port this entry names,
			// which is the port's one declaration site, so a service exposure
			// cannot disagree with the port the container opened.
			ports = append(ports, ServicePort{
				Name:       portName,
				Port:       int32(portNum),
				TargetPort: portName,
				Protocol:   targetPort.Protocol,
			})
		}

		svc := Service{
			APIVersion: k8sServiceAPIVersion,
			Kind:       "Service",
			Metadata: ObjectMeta{
				Name:        serviceNameFor(basis, svcName),
				Namespace:   namespace,
				Labels:      resourceLabels,
				Annotations: resourceAnnotations,
			},
			Spec: ServiceSpec{
				Type:     translateServiceType(svcSpec.Type),
				Selector: selectorLabels,
				Ports:    ports,
			},
		}
		if svcSpec.Type == rendercore.WorkloadServiceTypeHeadless {
			svc.Spec.ClusterIP = "None"
		}
		out = append(out, svc)
	}
	return out, nil
}

// translateServiceType maps Workload Service.Type to K8s Service type.
// Headless renders as ClusterIP at the type field; the clusterIP: None
// distinction lives on the Spec.ClusterIP field. Loopback never reaches here:
// checkServiceTypes refuses it before any Service is translated.
func translateServiceType(t string) string {
	if t == rendercore.WorkloadServiceTypeHeadless {
		return rendercore.WorkloadServiceTypeClusterIP
	}
	return t
}

// applyPodLevelFields populates pod-level configuration on a PodSpec from
// Workload.Scheduling, Workload.PodSecurityContext, Workload.ServiceAccount.
// Container-level restartPolicy bubbles up to pod-level when set.
//
// Well-known annotations that mirror a workload field resolve by cardinality,
// each in exactly one place here:
//   - saAnnotation (aggrega.ai/service-account, a scalar) overrides
//     Workload.ServiceAccount when set — the operator boundary is last-writer.
//   - pullSecretAnnotation (aggrega.ai/image-pull-secret, a list value) unions
//     into imagePullSecrets, appended after the workload's entries in author
//     order and skipped if already present — a scalar override would silently
//     drop recipe-declared pull secrets.
func applyPodLevelFields(spec *PodSpec, w *types.Workload, saAnnotation, pullSecretAnnotation string) {
	if w.Scheduling != nil {
		spec.NodeSelector = w.Scheduling.NodeSelector
		spec.Affinity = w.Scheduling.Affinity
		spec.Tolerations = w.Scheduling.Tolerations
		spec.TopologySpreadConstraints = w.Scheduling.TopologySpreadConstraints
	}
	if w.PodSecurityContext != nil {
		spec.SecurityContext = &PodSecurityContext{
			RunAsUser:          ptrToInt64FromInt(w.PodSecurityContext.RunAsUser),
			RunAsGroup:         ptrToInt64FromInt(w.PodSecurityContext.RunAsGroup),
			FSGroup:            ptrToInt64FromInt(w.PodSecurityContext.FSGroup),
			SupplementalGroups: intsToInt64s(w.PodSecurityContext.SupplementalGroups),
		}
	}
	// ServiceAccount precedence, resolved in exactly one place: the
	// aggrega.ai/service-account annotation (operator boundary) wins over the
	// recipe-layer workload.serviceAccount field when both are set.
	sa := w.ServiceAccount
	if saAnnotation != "" {
		sa = saAnnotation
	}
	if sa != "" {
		spec.ServiceAccountName = sa
	}
	// imagePullSecrets: the recipe field entries verbatim in author order (so
	// output is unchanged when no annotation is set), then the operator-boundary
	// annotation unioned in — appended only if it is not already a field entry.
	fieldPullSecret := false
	for _, name := range w.ImagePullSecrets {
		spec.ImagePullSecrets = append(spec.ImagePullSecrets, LocalObjectReference{Name: name})
		if name == pullSecretAnnotation {
			fieldPullSecret = true
		}
	}
	if pullSecretAnnotation != "" && !fieldPullSecret {
		spec.ImagePullSecrets = append(spec.ImagePullSecrets, LocalObjectReference{Name: pullSecretAnnotation})
	}
	spec.PriorityClassName = w.PriorityClassName
	spec.RuntimeClassName = w.RuntimeClassName
	if w.TerminationGracePeriodSeconds != nil {
		v := int64(*w.TerminationGracePeriodSeconds)
		spec.TerminationGracePeriodSeconds = &v
	}
	spec.DNSPolicy = w.DNSPolicy
	if w.DNSConfig != nil {
		spec.DNSConfig = &PodDNSConfig{
			Nameservers: w.DNSConfig.Nameservers,
			Searches:    w.DNSConfig.Searches,
			Options:     w.DNSConfig.Options,
		}
	}
	for _, ha := range w.HostAliases {
		spec.HostAliases = append(spec.HostAliases, HostAlias{IP: ha.IP, Hostnames: ha.Hostnames})
	}
	spec.HostNetwork = w.HostNetwork

	// RestartPolicy: bubble up the first container's restartPolicy when
	// it's OnFailure or Never (Always is the K8s default; emitting
	// it explicitly would change the rendered output for the common case).
	for _, c := range w.Containers.All() {
		if c.RestartPolicy == "OnFailure" || c.RestartPolicy == "Never" {
			spec.RestartPolicy = c.RestartPolicy
			break
		}
	}
}

// mergeLabels combines two label maps; later wins on key collision.
func mergeLabels(base, overlay map[string]string) map[string]string {
	out := make(map[string]string, len(base)+len(overlay))
	for k, v := range base {
		out[k] = v
	}
	for k, v := range overlay {
		out[k] = v
	}
	return out
}

// ptrToInt32 returns the int32 value or 0 for nil pointers (callers using
// `omitempty` suppress the 0).
func ptrToInt32(p *int) int32 {
	if p == nil {
		return 0
	}
	return int32(*p)
}

// ptrToInt64FromInt converts a *int to *int64 (K8s shapes for runAsUser,
// runAsGroup, fsGroup are int64).
func ptrToInt64FromInt(p *int) *int64 {
	if p == nil {
		return nil
	}
	v := int64(*p)
	return &v
}

// intsToInt64s converts []int to []int64 for K8s SupplementalGroups shape.
func intsToInt64s(s []int) []int64 {
	if len(s) == 0 {
		return nil
	}
	out := make([]int64, len(s))
	for i, v := range s {
		out[i] = int64(v)
	}
	return out
}

// marshalDeterministic routes through the shared indent-aware marshal helper
// at a single call point. yaml.v3's MarshalYAML is invoked recursively on each
// typed K8s shape; OrderedMap fields preserve author order via
// OrderedMap.MarshalYAML. byte-identical input produces byte-identical output
// across compilations. indent is the configured YAML indentation width.
func marshalDeterministic(v any, indent int) ([]byte, error) {
	return rendercore.MarshalYAMLIndent(v, indent)
}
