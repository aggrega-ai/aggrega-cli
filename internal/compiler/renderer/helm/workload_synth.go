// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Workload → K8s manifest synthesizer for the helm provider. Implements the
// renderer.WorkloadSynthesizer interface (declared in renderer/synthesis.go,
// dispatched from Phase B.4).
//
// Translation contract (chapter 16 §3):
//
//   - Workload.Volumes contains any persistentTemplate → emit StatefulSet;
//     otherwise → Deployment.
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
// "<instance_id>-<service-key>-service.yaml" for Services. Matches the
// existing sourceless.go naming pattern.
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
	"context"
	"fmt"
	"sort"

	"gopkg.in/yaml.v3"

	"github.com/cognitio/aggrega-cli/internal/compiler/renderer"
	"github.com/cognitio/aggrega-cli/pkg/types"
)

const (
	k8sStatefulSetAPIVersion = "apps/v1"
	k8sPVCAPIVersion         = "v1"
)

// SynthesizeWorkload translates a parsed Workload into K8s manifest files for
// the helm provider. Implements renderer.WorkloadSynthesizer; called by
// Phase B.4 when the bound Recipe declares workload: or workloadFrom:.
//
// Returns []EmittedFile{Name, Body}; the dispatcher (renderer/synthesis.go)
// tags Origin = EmitOriginWorkload before appending to PhaseBState.
func (e *HelmEngine) SynthesizeWorkload(
	_ context.Context,
	_ *types.TemplateContext,
	w *types.Workload,
	nodeCtx renderer.NodeRenderContext,
) ([]renderer.EmittedFile, error) {
	if w == nil {
		return nil, nil
	}

	// Decide top-level resource: StatefulSet if any volume is
	// persistentTemplate; else Deployment.
	useStatefulSet := workloadHasPersistentTemplate(w)

	labels := buildAggreraLabels(nodeCtx)
	selectorLabels := map[string]string{"app": nodeCtx.NodeID}
	resourceLabels := mergeLabels(selectorLabels, labels)
	namespace := renderer.NamespaceFromAnnotations(nodeCtx.Annotations)

	var out []renderer.EmittedFile

	// Containers: translate each in author-order.
	containers, err := translateContainers(w, nodeCtx.NodeID)
	if err != nil {
		return nil, err
	}
	initContainers, err := translateInitContainers(w, nodeCtx.NodeID)
	if err != nil {
		return nil, err
	}

	// Pod-spec volumes (excludes persistentTemplate variants).
	podVolumes := translatePodVolumes(w, nodeCtx.NodeID)

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

	if useStatefulSet {
		ss := StatefulSet{
			APIVersion: k8sStatefulSetAPIVersion,
			Kind:       "StatefulSet",
			Metadata:   ObjectMeta{Name: nodeCtx.NodeID, Namespace: namespace, Labels: resourceLabels},
			Spec: StatefulSetSpec{
				Replicas:             &replicas,
				Selector:             &LabelSelector{MatchLabels: selectorLabels},
				Template:             template,
				VolumeClaimTemplates: translateVolumeClaimTemplates(w, nodeCtx.NodeID),
			},
		}
		body, err := marshalDeterministic(&ss)
		if err != nil {
			return nil, fmt.Errorf("helm: node %q statefulset marshal: %w", nodeCtx.NodeID, err)
		}
		out = append(out, renderer.EmittedFile{
			Name: nodeCtx.NodeID + "-statefulset.yaml",
			Body: body,
		})
	} else {
		dep := Deployment{
			APIVersion: k8sDeploymentAPIVersion,
			Kind:       "Deployment",
			Metadata:   ObjectMeta{Name: nodeCtx.NodeID, Namespace: namespace, Labels: resourceLabels},
			Spec: DeploymentSpec{
				Replicas: &replicas,
				Selector: &LabelSelector{MatchLabels: selectorLabels},
				Template: template,
			},
		}
		body, err := marshalDeterministic(&dep)
		if err != nil {
			return nil, fmt.Errorf("helm: node %q deployment marshal: %w", nodeCtx.NodeID, err)
		}
		out = append(out, renderer.EmittedFile{
			Name: nodeCtx.NodeID + "-deployment.yaml",
			Body: body,
		})
	}

	// PVCs: one per persistent volume (NOT persistentTemplate; those
	// become volumeClaimTemplates inside the StatefulSet spec).
	pvcs := translatePersistentPVCs(w, nodeCtx.NodeID, namespace, resourceLabels)
	for _, pvc := range pvcs {
		body, err := marshalDeterministic(&pvc)
		if err != nil {
			return nil, fmt.Errorf("helm: node %q pvc marshal: %w", nodeCtx.NodeID, err)
		}
		out = append(out, renderer.EmittedFile{
			Name: pvc.Metadata.Name + "-pvc.yaml",
			Body: body,
		})
	}

	// Services: one per Workload.Services entry, in author-order.
	services, err := translateServices(w, nodeCtx.NodeID, namespace, selectorLabels, resourceLabels)
	if err != nil {
		return nil, err
	}
	for _, svc := range services {
		body, err := marshalDeterministic(&svc)
		if err != nil {
			return nil, fmt.Errorf("helm: node %q service marshal: %w", nodeCtx.NodeID, err)
		}
		out = append(out, renderer.EmittedFile{
			Name: svc.Metadata.Name + "-service.yaml",
			Body: body,
		})
	}

	return out, nil
}

// workloadHasPersistentTemplate reports whether any volume on the Workload
// uses the persistentTemplate variant. Triggers StatefulSet selection.
func workloadHasPersistentTemplate(w *types.Workload) bool {
	for _, v := range w.Volumes.All() {
		if v.PersistentTemplate != nil {
			return true
		}
	}
	return false
}

// translateContainers walks Workload.Containers in author order and produces
// K8s-shape Container values. Returns an error if any container's
// translation fails (e.g., probe http: with no available port).
func translateContainers(w *types.Workload, nodeID string) ([]Container, error) {
	var out []Container
	for name, c := range w.Containers.All() {
		k8sContainer, err := translateContainer(name, c, nodeID)
		if err != nil {
			return nil, err
		}
		out = append(out, k8sContainer)
	}
	return out, nil
}

// translateInitContainers walks Workload.InitContainers (positional list).
// initContainer naming convention: "init-<index>" when no other identity is
// available — Workload's InitContainers is a list of ContainerSpec values
// without explicit names today.
func translateInitContainers(w *types.Workload, nodeID string) ([]Container, error) {
	if len(w.InitContainers) == 0 {
		return nil, nil
	}
	var out []Container
	for i, c := range w.InitContainers {
		k8sContainer, err := translateContainer(fmt.Sprintf("init-%d", i), c, nodeID)
		if err != nil {
			return nil, err
		}
		out = append(out, k8sContainer)
	}
	return out, nil
}

// translateContainer maps a single Workload ContainerSpec to a K8s-shape
// Container. Author-order preserved across ports / variables / secrets.
func translateContainer(name string, c types.ContainerSpec, nodeID string) (Container, error) {
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
	// spec elsewhere). Author order preserved.
	for secretKey, ref := range c.Secrets.All() {
		switch ref.As {
		case "env":
			out.Env = append(out.Env, EnvVar{
				Name: secretKey,
				ValueFrom: &EnvVarSource{
					SecretKeyRef: &SecretKeySelector{
						Name: ref.From,
						Key:  secretKey,
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
			return Container{}, fmt.Errorf("container %q readiness probe: %w", name, err)
		}
		out.ReadinessProbe = ready

		live, err := translateProbe(c.Health.Live, out.Ports)
		if err != nil {
			return Container{}, fmt.Errorf("container %q liveness probe: %w", name, err)
		}
		out.LivenessProbe = live

		startup, err := translateProbe(c.Health.Startup, out.Ports)
		if err != nil {
			return Container{}, fmt.Errorf("container %q startup probe: %w", name, err)
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
		return nil, fmt.Errorf("probe declares no shape (http/tcp/exec)")
	}

	return out, nil
}

// resolveHTTPProbePort returns the first container port name in alphabetical
// order, suitable for use as a named-port reference in httpGet. Returns an
// error if no container ports are declared (TRANSLATION_ERROR).
func resolveHTTPProbePort(containerPorts []ContainerPort) (string, error) {
	if len(containerPorts) == 0 {
		return "", fmt.Errorf("TRANSLATION_ERROR: probe http: requires at least one named container port; container declares none")
	}
	names := make([]string, 0, len(containerPorts))
	for _, p := range containerPorts {
		if p.Name != "" {
			names = append(names, p.Name)
		}
	}
	if len(names) == 0 {
		return "", fmt.Errorf("TRANSLATION_ERROR: probe http: requires at least one NAMED container port; container ports lack name fields")
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
// the convention "<instance_id>-secret-<slot>".
func translatePodVolumes(w *types.Workload, nodeID string) []Volume {
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
			out = append(out, Volume{
				Name: volName,
				Secret: &SecretVolumeSource{
					SecretName: ref.From,
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
			out = append(out, Volume{
				Name: volName,
				Secret: &SecretVolumeSource{
					SecretName: ref.From,
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
func translatePersistentPVCs(w *types.Workload, nodeID, namespace string, labels map[string]string) []PersistentVolumeClaim {
	var out []PersistentVolumeClaim
	for name, v := range w.Volumes.All() {
		if v.Persistent == nil {
			continue
		}
		pvc := PersistentVolumeClaim{
			APIVersion: k8sPVCAPIVersion,
			Kind:       "PersistentVolumeClaim",
			Metadata: ObjectMeta{
				Name:      nodeID + "-" + name,
				Namespace: namespace,
				Labels:    labels,
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
func translateServices(w *types.Workload, nodeID, namespace string, selectorLabels, resourceLabels map[string]string) ([]Service, error) {
	var out []Service

	containerPortMap := buildContainerPortNameMap(w)

	for svcName, svcSpec := range w.Services.All() {
		ports := []ServicePort{}
		for portName, portNum := range svcSpec.Ports.All() {
			targetPort, ok := containerPortMap[portName]
			if !ok {
				return nil, fmt.Errorf("TRANSLATION_ERROR: service %q port %q has no matching container port name", svcName, portName)
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
				Name:      nodeID + "-" + svcName,
				Namespace: namespace,
				Labels:    resourceLabels,
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

// marshalDeterministic wraps yaml.Marshal at a single call point. yaml.v3's
// MarshalYAML is invoked recursively on each typed K8s shape; OrderedMap
// fields preserve author order via OrderedMap.MarshalYAML. byte-identical
// input produces byte-identical output across compilations.
func marshalDeterministic(v any) ([]byte, error) {
	return yaml.Marshal(v)
}
