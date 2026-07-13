// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Filesystem-free Workload → K8s manifest synthesis core for the helm provider.
//
// SynthesizeWorkloadCore is the single byte-identical-critical synthesis path:
// it takes the evaluated Workload, the per-slot secret provides, and the node's
// render context, and returns the K8s manifest files. Both the in-tree host
// engine (through the SynthesizeWorkload adapter, which bridges the host-only
// template context) and the provider Wasm guest (which reconstructs the Workload
// from the payload's workload projection) call this one function, so the two
// synthesis surfaces stay byte-for-byte identical. This file carries no build
// tag: it compiles for the host and for the wasip1 guest alike, so it imports no
// host-only capability (no filesystem, no network).
//
// Translation contract (chapter 16 §3):
//
//   - Workload.Form selects the controller shape: stateless (and empty) →
//     Deployment, stateful → StatefulSet, daemon → DaemonSet, task → Job,
//     scheduled → CronJob with spec.schedule set from the workload's schedule
//     (a 5-field cron expression or an @-macro; a missing or malformed schedule
//     halts loudly via the shared companion validation).
//   - A persistentTemplate volume requires form: stateful (companion
//     validation, single-sourced in rendercore); those volumes become the
//     StatefulSet's volumeClaimTemplates.
//   - One K8s Service per Workload.Services entry.
//   - One PVC per persistent volume (NOT persistentTemplate; those become
//     volumeClaimTemplates inside the StatefulSet spec).
//   - configmap / secret volume variants → K8s volume references by name
//     (no new ConfigMap or Secret resource emitted; those come from
//     artifacts or Features).
//   - ephemeral volume variant → emptyDir { sizeLimit }.
//
// Author-order preservation: containers, services, volumes, container
// ports, container variables, container secrets — all iterated via
// OrderedMap.All() so byte-identical input produces byte-identical
// output across compilations.
//
// Filename convention: per-resource files named
// "<instance_id>-<kind>.yaml" for Deployment/StatefulSet/PVC, and
// "<instance_id>-<service-key>-service.yaml" for Services.
//
// Secrets-as-env pattern: the synthesizer uses `env.valueFrom.secretKeyRef`
// for `as: env` secret entries (one env var per secret entry, naming the
// secret by the slot's `from:` value with the env var named by the
// container-side key). The alternative `envFrom`+secretRef pattern is
// reserved for future bulk-env-from-secret use; the per-entry pattern
// gives Recipe authors finer control over env var naming and is
// consistent across all secret references regardless of whether the slot
// is single-keyed or multi-keyed.

package helm

import (
	"fmt"
	"sort"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/rendercore"
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
// for the helm provider. secretProvides maps each secret slot to its resolved
// provides bag: a workload secrets entry's `from` is the slot; the generated
// Secret's identity (name, key) comes from the slot's provides. The
// referenceability check upstream guarantees every referenced slot carries the
// keys read below.
//
// Returns []EmittedFile{Name, Body} in a deterministic emission order:
// workload object first, then PVCs, then Services (each in author order). The
// order is load-bearing — callers preserve it rather than re-sorting.
func SynthesizeWorkloadCore(
	w *types.Workload,
	secretProvides map[string]types.OrderedMap[string],
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

	labels := buildAggregaLabels(nodeCtx)
	annotations := buildAggregaAnnotations(nodeCtx)
	// componentID is the single naming basis for every synthesized resource —
	// file keys, selector labels, metadata.name, and the per-resource sub-names
	// (containers, PVCs, services). It derives from ProvenanceComponentID:
	// LabelComponentID when set, else the raw NodeID. For a component node
	// LabelComponentID is empty, so this is the raw NodeID (byte-identical to
	// the prior behavior). For a FeatureRecipe-synthesized workload the caller
	// sets LabelComponentID to the sanitized FR-instance id, so the workload is
	// named after the feature instance rather than the host component (whose
	// NodeID the FR node otherwise carries) — this is what keeps an FR's
	// synthesized Deployment from colliding with the host's own workload, and
	// keeps a boundary-scope FR's marker-syntax NodeID out of K8s names.
	componentID := rendercore.ProvenanceComponentID(nodeCtx)
	selectorLabels := map[string]string{"app": componentID}
	resourceLabels := mergeLabels(selectorLabels, labels)
	resourceAnnotations := nilIfEmpty(annotations)
	namespace := rendercore.NamespaceFromAnnotations(nodeCtx.Annotations)

	var out []rendercore.EmittedFile

	// Containers: translate each in author-order.
	containers, err := translateContainers(w, componentID, secretProvides)
	if err != nil {
		return nil, err
	}
	initContainers, err := translateInitContainers(w, componentID, secretProvides)
	if err != nil {
		return nil, err
	}

	// Pod-spec volumes (excludes persistentTemplate variants).
	podVolumes := translatePodVolumes(w, componentID, secretProvides)

	// Pod-level configuration: scheduling, podSecurityContext,
	// serviceAccount, restartPolicy.
	podSpec := PodSpec{
		Containers:     containers,
		InitContainers: initContainers,
		Volumes:        podVolumes,
	}
	applyPodLevelFields(&podSpec, w)

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
	services, err := translateServices(w, componentID, namespace, selectorLabels, resourceLabels, resourceAnnotations)
	if err != nil {
		return nil, err
	}

	// Controller object: one per form. stateless → Deployment, stateful →
	// StatefulSet, daemon → DaemonSet, task → Job, scheduled → CronJob whose
	// spec.schedule is the workload's schedule (validated by the shared
	// companion check above).
	switch form {
	case rendercore.WorkloadFormStateful:
		governingService, gerr := statefulSetGoverningService(services, nodeCtx.NodeID)
		if gerr != nil {
			return nil, gerr
		}
		ss := StatefulSet{
			APIVersion: k8sStatefulSetAPIVersion,
			Kind:       "StatefulSet",
			Metadata:   ObjectMeta{Name: componentID, Namespace: namespace, Labels: resourceLabels, Annotations: resourceAnnotations},
			Spec: StatefulSetSpec{
				Replicas:             &replicas,
				Selector:             &LabelSelector{MatchLabels: selectorLabels},
				ServiceName:          governingService,
				Template:             template,
				VolumeClaimTemplates: translateVolumeClaimTemplates(w, componentID),
			},
		}
		body, err := marshalDeterministic(&ss, nodeCtx.YAMLIndent)
		if err != nil {
			return nil, cerrors.Wrap(err, cerrors.TranslationError, cerrors.SourceLocation{File: nodeCtx.NodeID}, fmt.Sprintf("helm: node %q statefulset marshal", nodeCtx.NodeID))
		}
		out = append(out, rendercore.EmittedFile{
			Name: componentID + "-statefulset.yaml",
			Body: body,
		})
	case rendercore.WorkloadFormDaemon:
		ds := DaemonSet{
			APIVersion: k8sDaemonSetAPIVersion,
			Kind:       "DaemonSet",
			Metadata:   ObjectMeta{Name: componentID, Namespace: namespace, Labels: resourceLabels, Annotations: resourceAnnotations},
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
			Name: componentID + "-daemonset.yaml",
			Body: body,
		})
	case rendercore.WorkloadFormTask:
		job := Job{
			APIVersion: k8sJobAPIVersion,
			Kind:       "Job",
			Metadata:   ObjectMeta{Name: componentID, Namespace: namespace, Labels: resourceLabels, Annotations: resourceAnnotations},
			Spec:       JobSpec{Template: jobValidTemplate(template)},
		}
		body, err := marshalDeterministic(&job, nodeCtx.YAMLIndent)
		if err != nil {
			return nil, cerrors.Wrap(err, cerrors.TranslationError, cerrors.SourceLocation{File: nodeCtx.NodeID}, fmt.Sprintf("helm: node %q job marshal", nodeCtx.NodeID))
		}
		out = append(out, rendercore.EmittedFile{
			Name: componentID + "-job.yaml",
			Body: body,
		})
	case rendercore.WorkloadFormScheduled:
		cron := CronJob{
			APIVersion: k8sCronJobAPIVersion,
			Kind:       "CronJob",
			Metadata:   ObjectMeta{Name: componentID, Namespace: namespace, Labels: resourceLabels, Annotations: resourceAnnotations},
			Spec: CronJobSpec{
				Schedule:    w.Schedule,
				JobTemplate: JobTemplateSpec{Spec: JobSpec{Template: jobValidTemplate(template)}},
			},
		}
		body, err := marshalDeterministic(&cron, nodeCtx.YAMLIndent)
		if err != nil {
			return nil, cerrors.Wrap(err, cerrors.TranslationError, cerrors.SourceLocation{File: nodeCtx.NodeID}, fmt.Sprintf("helm: node %q cronjob marshal", nodeCtx.NodeID))
		}
		out = append(out, rendercore.EmittedFile{
			Name: componentID + "-cronjob.yaml",
			Body: body,
		})
	default:
		dep := Deployment{
			APIVersion: k8sDeploymentAPIVersion,
			Kind:       "Deployment",
			Metadata:   ObjectMeta{Name: componentID, Namespace: namespace, Labels: resourceLabels, Annotations: resourceAnnotations},
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
			Name: componentID + "-deployment.yaml",
			Body: body,
		})
	}

	// PVCs: one per persistent volume (NOT persistentTemplate; those
	// become volumeClaimTemplates inside the StatefulSet spec).
	pvcs := translatePersistentPVCs(w, componentID, namespace, resourceLabels, resourceAnnotations)
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
		return "", cerrors.New(cerrors.TranslationError, cerrors.SourceLocation{File: nodeID},
			"synthesized StatefulSet requires a headless governing Service (declare a service with type: Headless); none found",
			nil, ErrStatefulSetNoHeadlessService)
	default:
		return "", cerrors.New(cerrors.TranslationError, cerrors.SourceLocation{File: nodeID},
			"synthesized StatefulSet declares more than one headless Service; the governing Service is ambiguous (declare exactly one type: Headless service)",
			nil, ErrStatefulSetAmbiguousHeadlessService)
	}
}

// translateContainers walks Workload.Containers in author order and produces
// K8s-shape Container values. Returns an error if any container's
// translation fails (e.g., probe http: with no available port).
func translateContainers(w *types.Workload, nodeID string, secretProvides map[string]types.OrderedMap[string]) ([]Container, error) {
	var out []Container
	for name, c := range w.Containers.All() {
		k8sContainer, err := translateContainer(name, c, nodeID, secretProvides)
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
func translateInitContainers(w *types.Workload, nodeID string, secretProvides map[string]types.OrderedMap[string]) ([]Container, error) {
	if len(w.InitContainers) == 0 {
		return nil, nil
	}
	var out []Container
	for i, c := range w.InitContainers {
		name := c.Name
		if name == "" {
			name = fmt.Sprintf("init-%d", i)
		}
		k8sContainer, err := translateContainer(name, c, nodeID, secretProvides)
		if err != nil {
			return nil, err
		}
		out = append(out, k8sContainer)
	}
	return out, nil
}

// translateContainer maps a single Workload ContainerSpec to a K8s-shape
// Container. Author-order preserved across ports / variables / secrets.
func translateContainer(name string, c types.ContainerSpec, nodeID string, secretProvides map[string]types.OrderedMap[string]) (Container, error) {
	out := Container{
		Name:    sanitizeContainerName(name),
		Image:   c.Image,
		Command: c.Command,
		Args:    c.Args,
	}

	// Container ports (named) — preserves Workload.Containers.<n>.Ports
	// author order.
	for portName, portNum := range c.Ports.All() {
		out.Ports = append(out.Ports, ContainerPort{
			Name:          portName,
			ContainerPort: int32(portNum),
		})
	}

	// Environment variables — preserves Workload.Containers.<n>.Variables
	// author order.
	for varName, varValue := range c.Variables.All() {
		out.Env = append(out.Env, EnvVar{Name: varName, Value: varValue})
	}

	// Secrets — for each entry, use env.valueFrom.secretKeyRef (env mode)
	// or volume+volumeMount (file mode; the volume is added to the pod
	// spec elsewhere). Author order preserved. `ref.From` is the slot; the
	// referenced Secret's name comes from the slot's resolved provides
	// (name). The data key is the entry's explicit `ref.Key` when set —
	// which lets two entries read different keys of one multi-key secret —
	// otherwise the slot's single published `key`. The env-var name stays
	// the entry's map key.
	for secretKey, ref := range c.Secrets.All() {
		provides := secretProvides[ref.From]
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
				Name:      nodeID + "-secret-" + ref.From,
				MountPath: ref.Path,
				ReadOnly:  true,
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
// ResourceSpec to K8s ResourceRequirements.
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
	}
	if r.Limits != nil {
		out.Limits = make(map[string]string)
		if r.Limits.CPU != "" {
			out.Limits["cpu"] = r.Limits.CPU
		}
		if r.Limits.Memory != "" {
			out.Limits["memory"] = r.Limits.Memory
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
		return nil, cerrors.New(cerrors.TranslationError, cerrors.SourceLocation{}, "probe declares no shape (http/tcp/exec)", nil, nil)
	}

	return out, nil
}

// resolveHTTPProbePort returns the first container port name in alphabetical
// order, suitable for use as a named-port reference in httpGet. Returns an
// error if no container ports are declared (TRANSLATION_ERROR).
func resolveHTTPProbePort(containerPorts []ContainerPort) (string, error) {
	if len(containerPorts) == 0 {
		return "", cerrors.New(cerrors.TranslationError, cerrors.SourceLocation{}, "probe http: requires at least one named container port; container declares none", nil, nil)
	}
	names := make([]string, 0, len(containerPorts))
	for _, p := range containerPorts {
		if p.Name != "" {
			names = append(names, p.Name)
		}
	}
	if len(names) == 0 {
		return "", cerrors.New(cerrors.TranslationError, cerrors.SourceLocation{}, "probe http: requires at least one NAMED container port; container ports lack name fields", nil, nil)
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
// Also adds secret-volume entries for container secrets entries that use
// `as: file` — one volume per (container, secret-key) pair, named per
// the convention "<instance_id>-secret-<slot>". The volume name stays
// convention-based (pod-internal); the SecretVolumeSource.SecretName it
// mounts is the generated Secret's name, read from the slot's resolved
// provides.
func translatePodVolumes(w *types.Workload, nodeID string, secretProvides map[string]types.OrderedMap[string]) []Volume {
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
					ClaimName: nodeID + "-" + name,
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
		for _, ref := range c.Secrets.All() {
			if ref.As != "file" {
				continue
			}
			volName := nodeID + "-secret-" + ref.From
			if _, seen := seenSecretVolumes[volName]; seen {
				continue
			}
			seenSecretVolumes[volName] = struct{}{}
			provides := secretProvides[ref.From]
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
		for _, ref := range c.Secrets.All() {
			if ref.As != "file" {
				continue
			}
			volName := nodeID + "-secret-" + ref.From
			if _, seen := seenSecretVolumes[volName]; seen {
				continue
			}
			seenSecretVolumes[volName] = struct{}{}
			provides := secretProvides[ref.From]
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
// Author order preserved.
func translateVolumeClaimTemplates(w *types.Workload, nodeID string) []PersistentVolumeClaim {
	var out []PersistentVolumeClaim
	for name, v := range w.Volumes.All() {
		if v.PersistentTemplate == nil {
			continue
		}
		pvc := PersistentVolumeClaim{
			APIVersion: k8sPVCAPIVersion,
			Kind:       "PersistentVolumeClaim",
			Metadata:   ObjectMeta{Name: name},
			Spec:       buildPVCSpec(v.PersistentTemplate.Size, v.PersistentTemplate.Class),
		}
		_ = nodeID // pvc names inside volumeClaimTemplates are local to the
		// StatefulSet; no nodeID prefix needed.
		out = append(out, pvc)
	}
	return out
}

// translatePersistentPVCs produces standalone PVC resources (one per
// persistent volume; not persistentTemplate). Author order preserved.
func translatePersistentPVCs(w *types.Workload, nodeID, namespace string, labels map[string]string, annotations map[string]string) []PersistentVolumeClaim {
	var out []PersistentVolumeClaim
	for name, v := range w.Volumes.All() {
		if v.Persistent == nil {
			continue
		}
		pvc := PersistentVolumeClaim{
			APIVersion: k8sPVCAPIVersion,
			Kind:       "PersistentVolumeClaim",
			Metadata: ObjectMeta{
				Name:        nodeID + "-" + name,
				Namespace:   namespace,
				Labels:      labels,
				Annotations: annotations,
			},
			Spec: buildPVCSpec(v.Persistent.Size, v.Persistent.Class),
		}
		out = append(out, pvc)
	}
	return out
}

// buildPVCSpec creates the PersistentVolumeClaimSpec from a size + optional
// storage class. AccessModes default to ReadWriteOnce.
func buildPVCSpec(size, class string) PersistentVolumeClaimSpec {
	spec := PersistentVolumeClaimSpec{
		AccessModes: []string{"ReadWriteOnce"},
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
// Service resources. targetPort resolution: look up the port name across
// all containers; error if not found.
func translateServices(w *types.Workload, nodeID, namespace string, selectorLabels, resourceLabels map[string]string, resourceAnnotations map[string]string) ([]Service, error) {
	var out []Service

	containerPortMap := buildContainerPortNameMap(w)

	for svcName, svcSpec := range w.Services.All() {
		ports := []ServicePort{}
		for portName, portNum := range svcSpec.Ports.All() {
			targetPort, ok := containerPortMap[portName]
			if !ok {
				return nil, cerrors.New(cerrors.TranslationError, cerrors.SourceLocation{}, fmt.Sprintf("service %q port %q has no matching container port name", svcName, portName), nil, nil)
			}
			_ = targetPort // present for forward compatibility (numeric backref);
			// today targetPort uses the named-port string, which K8s
			// resolves at runtime to the matching containerPort.
			ports = append(ports, ServicePort{
				Name:       portName,
				Port:       int32(portNum),
				TargetPort: portName,
			})
		}

		svc := Service{
			APIVersion: k8sServiceAPIVersion,
			Kind:       "Service",
			Metadata: ObjectMeta{
				Name:        nodeID + "-" + svcName,
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
		if svcSpec.Type == "Headless" {
			svc.Spec.ClusterIP = "None"
		}
		out = append(out, svc)
	}
	return out, nil
}

// buildContainerPortNameMap collects every container port name → container
// port number across all containers (and init containers). Used to resolve
// service.targetPort references.
func buildContainerPortNameMap(w *types.Workload) map[string]int {
	out := map[string]int{}
	for _, c := range w.Containers.All() {
		for portName, portNum := range c.Ports.All() {
			out[portName] = portNum
		}
	}
	for _, c := range w.InitContainers {
		for portName, portNum := range c.Ports.All() {
			out[portName] = portNum
		}
	}
	return out
}

// translateServiceType maps Workload Service.Type to K8s Service type.
// Headless renders as ClusterIP at the type field; the clusterIP: None
// distinction lives on the Spec.ClusterIP field.
func translateServiceType(t string) string {
	if t == "Headless" {
		return "ClusterIP"
	}
	return t
}

// applyPodLevelFields populates pod-level configuration on a PodSpec from
// Workload.Scheduling, Workload.PodSecurityContext, Workload.ServiceAccount.
// Container-level restartPolicy bubbles up to pod-level when set.
func applyPodLevelFields(spec *PodSpec, w *types.Workload) {
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
	if w.ServiceAccount != "" {
		spec.ServiceAccountName = w.ServiceAccount
	}

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
