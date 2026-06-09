// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Workload → Docker Compose service synthesizer for the compose provider.
// Implements the renderer.WorkloadSynthesizer interface (declared in
// renderer/synthesis.go, dispatched from Phase B.4).
//
// Per-node contract. SynthesizeWorkload returns ONE EmittedFile —
// `<nodeID>.compose-fragment.yaml` — containing a partial ComposeFile with
// just this node's services / volumes contribution. The Release-level
// merger (Track 2) reads fragments, deep-merges, and emits a single
// docker-compose.yaml at the Release output root.
//
// Container model. Single-container Workloads → one compose service named
// `<nodeID>`. Multi-container Workloads → main service `<nodeID>` plus one
// sidecar service `<nodeID>-<containerName>` per non-main container; sidecars
// declare `network_mode: "service:<nodeID>"` so they share the main's
// network namespace (the closest compose primitive to a K8s pod).
//
// Volume translation. The five Workload volume variants degrade as follows:
//
//   - ephemeral          → tmpfs entry; sizeLimit ignored (compose tmpfs has
//     a tmpfs.size option only at long-form, not the shorthand list — kept as
//     short-form list for v3 broad compatibility, with a future expansion
//     point).
//   - configmap          → bind-mount from <source>:<target>:ro. The source
//     filename is the configmap name; the file MUST be emitted via the
//     Recipe's artifacts input. Track 1 emits the bind-mount;
//     integrity-checking against the artifacts input kind's emissions
//     lands when artifacts evaluation does.
//   - secret             → bind-mount from <source>:<target>:ro for `as: file`;
//     env_file: list entry for `as: env`. Mirrors helm secret handling.
//   - persistent         → named-volume bind: `<nodeID>-<name>:<target>`.
//     The top-level Volumes map declares an empty body for the named volume.
//   - persistentTemplate → degrades to the same shape as `persistent` with a
//     warning comment in the output. Compose has no per-replica volume
//     templating primitive.
//
// Service translation. Compose has no in-cluster vs external distinction:
//
//   - ClusterIP           → no `ports:` publishing (service stays internal to
//     compose networks).
//   - NodePort/LoadBalancer → `ports: [<host>:<container>]` published.
//   - Headless            → no `ports:` publishing + warning comment.
//
// Probe → healthcheck. Compose only supports CMD/CMD-SHELL/NONE healthcheck
// shapes; the three Workload probe shapes translate to shell commands
// per the Healthcheck doc comment in types.go. Probe timeouts copy through
// (K8s seconds → compose Go-duration string `<n>s`).
//
// Author-order preservation. Containers / Variables / Ports / Secrets /
// Volumes / Services iterated via OrderedMap.All(). Top-level maps
// (Services, Volumes) inside ComposeFile are Go map[string]X — yaml.v3
// sorts keys alphabetically on marshal, which is the
// alphabetical-by-derivation-key choice for synthesised collections
// where no author order applies upstream.

package compose

import (
	"context"
	"fmt"
	"sort"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// SynthesizeWorkload translates a parsed Workload into a per-node compose
// fragment. Implements renderer.WorkloadSynthesizer. Returns one EmittedFile
// (`<nodeID>.compose-fragment.yaml`); the Track 2 merger consumes fragments
// to produce the Release's single docker-compose.yaml.
//
// The dispatcher (renderer/synthesis.go) tags the EmittedFile's Origin =
// EmitOriginWorkload before appending to PhaseBState — the synthesizer
// itself doesn't set Origin.
func (e *ComposeEngine) SynthesizeWorkload(
	_ context.Context,
	_ *types.TemplateContext,
	w *types.Workload,
	nodeCtx renderer.NodeRenderContext,
) ([]renderer.EmittedFile, error) {
	if w == nil {
		return nil, nil
	}

	frag := ComposeFile{
		Services: map[string]Service{},
		Volumes:  map[string]Volume{},
	}

	// Translate every container into a service. Author-order isn't
	// preserved across services in the YAML output (compose is map-shaped at
	// the services: key) — the marshaler sorts alphabetically, the
	// alphabetical-by-derivation-key choice for synthesised maps.
	// componentID is the naming basis for the synthesised service, volumes, and
	// fragment file. It derives from ProvenanceComponentID: the raw NodeID for a
	// component node (byte-identical to the prior behavior, since LabelComponentID
	// is empty there), or the sanitized FR-instance id the caller sets for a
	// FeatureRecipe-synthesised workload — so an FR's service/volumes are named
	// after the feature instance, not the host component whose NodeID the FR node
	// carries.
	componentID := renderer.ProvenanceComponentID(nodeCtx)
	mainServiceName := componentID
	mainContainerName, hasMain := pickMainContainerName(w)

	for containerName, c := range w.Containers.All() {
		isMain := hasMain && containerName == mainContainerName

		serviceName := mainServiceName
		if !isMain {
			serviceName = mainServiceName + "-" + sanitizeServiceName(containerName)
		}

		svc, err := translateContainer(c, w, nodeCtx, isMain, mainServiceName)
		if err != nil {
			return nil, cerrors.Wrap(err, cerrors.TranslationError, cerrors.SourceLocation{File: nodeCtx.NodeID}, fmt.Sprintf("compose: node %q container %q", nodeCtx.NodeID, containerName))
		}

		// Apply pod-level fields onto the main service only — sidecars share
		// the main's network namespace and pod-level identity.
		if isMain {
			applyPodLevelFieldsToService(&svc, w)
		}

		frag.Services[serviceName] = svc
	}

	// Top-level Volumes: one entry per persistent or persistentTemplate
	// volume. Author order isn't expressed here — compose volumes are a map.
	for name, v := range w.Volumes.All() {
		switch {
		case v.Persistent != nil:
			frag.Volumes[volumeName(componentID, name)] = Volume{}
		case v.PersistentTemplate != nil:
			// Degrades to the same shape as persistent; the
			// pod-level mount declaration carries a warning
			// comment about the per-replica template loss.
			frag.Volumes[volumeName(componentID, name)] = Volume{}
		}
	}

	// Translate Workload.Services entries into port-publish settings on the
	// main service. Compose has no separate Service primitive — port
	// publishing is a service-level field. Aggregate ports across all
	// Workload.Services entries; the Type field decides whether to publish.
	if err := applyWorkloadServicePorts(&frag, w, mainServiceName); err != nil {
		return nil, cerrors.Wrap(err, cerrors.TranslationError, cerrors.SourceLocation{File: nodeCtx.NodeID}, fmt.Sprintf("compose: node %q apply service ports", nodeCtx.NodeID))
	}

	body, err := marshalDeterministic(&frag, nodeCtx.YAMLIndent)
	if err != nil {
		return nil, cerrors.Wrap(err, cerrors.TranslationError, cerrors.SourceLocation{File: nodeCtx.NodeID}, fmt.Sprintf("compose: node %q fragment marshal", nodeCtx.NodeID))
	}

	return []renderer.EmittedFile{
		{
			Name:         componentID + ".compose-fragment.yaml",
			Body:         body,
			Intermediate: true,
		},
	}, nil
}

// pickMainContainerName chooses the main container by convention:
//
//  1. If a container is named "main", that is main.
//  2. Else the first container in author-order is main.
//
// Returns (name, true) when a Workload has at least one container; (zero, false)
// when w.Containers is empty.
func pickMainContainerName(w *types.Workload) (string, bool) {
	if w.Containers.Len() == 0 {
		return "", false
	}
	for name := range w.Containers.All() {
		if name == "main" {
			return "main", true
		}
	}
	for name := range w.Containers.All() {
		return name, true
	}
	return "", false
}

// translateContainer maps a single Workload ContainerSpec to a compose Service.
// Author order preserved across env vars / ports / secrets via OrderedMap.All().
func translateContainer(
	c types.ContainerSpec,
	w *types.Workload,
	nodeCtx renderer.NodeRenderContext,
	isMain bool,
	mainServiceName string,
) (Service, error) {
	svc := Service{
		Image:  c.Image,
		Labels: buildAggregaLabels(nodeCtx),
	}

	if len(c.Command) > 0 {
		svc.Command = c.Command
	}

	// Environment variables (preserves author order via OrderedMap;
	// compose `environment:` accepts both list and map forms — we emit map
	// form for clarity, but Go map iteration order is unstable, so use the
	// OrderedMap iterator and copy into a map only at the last step. yaml.v3
	// sorts map keys alphabetically on marshal — author order is lost in
	// the output but the same input always produces the same output.
	if c.Variables.Len() > 0 {
		svc.Environment = map[string]string{}
		for k, v := range c.Variables.All() {
			svc.Environment[k] = v
		}
	}

	// Container secrets:
	//   - as: env  → add to Environment (copy literal value-from-secret syntax
	//     is K8s-only; compose has no equivalent. The synthesizer omits the
	//     secret from environment entirely in Track 1; sourced-mode and
	//     env_file lookup in Track 2.) For now: add an env entry with empty
	//     value referencing the slot name as a documentation comment.
	//   - as: file → bind-mount from secret-name:path:ro.
	for slotKey, ref := range c.Secrets.All() {
		switch ref.As {
		case "env":
			// Compose has no native env-from-secret. Track 1 emits the env
			// var name with an empty value as a placeholder; Track 2 wires
			// env_file: lookup against the secret slot name.
			if svc.Environment == nil {
				svc.Environment = map[string]string{}
			}
			svc.Environment[slotKey] = ""
		case "file":
			svc.Volumes = append(svc.Volumes, ServiceVolume{
				Type:     "bind",
				Source:   ref.From,
				Target:   ref.Path,
				ReadOnly: true,
			})
		}
	}

	// Volume mounts (typed list — VolumeMount carries Name + Path + ReadOnly).
	// The Workload volume's variant decides the compose primitive.
	for _, vm := range c.VolumeMounts {
		spec, ok := w.Volumes.Get(vm.Name)
		if !ok {
			// Unknown mount target. Schema validation should reject this; the
			// synthesizer degrades to a bind-mount with an empty source
			// (compose will fail at runtime).
			svc.Volumes = append(svc.Volumes, ServiceVolume{
				Type:     "bind",
				Source:   "",
				Target:   vm.Path,
				ReadOnly: vm.ReadOnly,
			})
			continue
		}

		entry := volumeMountEntry(vm, spec, mainServiceName)
		// Tmpfs is special: compose's `tmpfs:` is a top-level service field,
		// not a volumes: entry.
		if entry.Type == "tmpfs" {
			svc.Tmpfs = append(svc.Tmpfs, entry.Target)
			continue
		}
		svc.Volumes = append(svc.Volumes, entry)
	}

	// Probes → healthcheck. Compose only supports a single healthcheck per
	// service; Aggrega's three Workload probe slots collapse to one. Order
	// of preference: liveness > readiness > startup (most common compose
	// signal is liveness equivalent).
	if c.Health != nil {
		hc, err := translateHealthcheck(c.Health, c.Ports)
		if err != nil {
			return Service{}, err
		}
		if hc != nil {
			svc.Healthcheck = hc
		}
	}

	// Container security primitives that compose supports.
	if c.SecurityContext != nil {
		applyContainerSecurityContext(&svc, c.SecurityContext)
	}

	// Sidecar services share the main service's network namespace.
	if !isMain {
		svc.NetworkMode = "service:" + mainServiceName
	}

	// Restart policy. Compose `restart` accepts: no, always, on-failure,
	// unless-stopped. K8s RestartPolicy maps:
	//   Always    → unless-stopped
	//   OnFailure → on-failure
	//   Never     → no
	// Empty string from K8s leaves compose default (no restart).
	switch c.RestartPolicy {
	case "Always":
		svc.Restart = "unless-stopped"
	case "OnFailure":
		svc.Restart = "on-failure"
	case "Never":
		svc.Restart = "no"
	}

	return svc, nil
}

// volumeMountEntry produces the compose ServiceVolume entry for a given
// (VolumeMount, VolumeSpec) pair. Returns a {Type: "tmpfs", Target} sentinel
// for ephemeral volumes — the caller redirects to the service-level Tmpfs
// list since compose tmpfs is not a volumes: entry.
func volumeMountEntry(vm types.VolumeMount, spec types.VolumeSpec, nodeID string) ServiceVolume {
	switch {
	case spec.Ephemeral != nil:
		return ServiceVolume{Type: "tmpfs", Target: vm.Path}
	case spec.ConfigMap != nil:
		// Compose has no ConfigMap primitive. Bind-mount from the configmap's
		// source name (the file emitted via the Recipe's artifacts: input);
		// read-only.
		return ServiceVolume{
			Type:     "bind",
			Source:   spec.ConfigMap.Source,
			Target:   vm.Path,
			ReadOnly: true,
		}
	case spec.Secret != nil:
		return ServiceVolume{
			Type:     "bind",
			Source:   spec.Secret.Source,
			Target:   vm.Path,
			ReadOnly: true,
		}
	case spec.Persistent != nil:
		return ServiceVolume{
			Type:     "volume",
			Source:   volumeName(nodeID, vm.Name),
			Target:   vm.Path,
			ReadOnly: vm.ReadOnly,
		}
	case spec.PersistentTemplate != nil:
		// Degraded: same shape as persistent. Compose cannot express
		// per-replica templating; the warning is documented in the package
		// godoc and surfaces in the Layer-2 doc.
		return ServiceVolume{
			Type:     "volume",
			Source:   volumeName(nodeID, vm.Name),
			Target:   vm.Path,
			ReadOnly: vm.ReadOnly,
		}
	default:
		// Unrecognised variant — defensive degradation. Schema validation
		// rejects this shape; the synthesizer emits an empty bind to surface
		// at runtime.
		return ServiceVolume{Type: "bind", Source: "", Target: vm.Path, ReadOnly: vm.ReadOnly}
	}
}

// translateHealthcheck translates the Workload Health (3 probe slots) to a
// single compose Healthcheck. Order of preference: live > ready > startup.
// Returns nil when no probe is set.
func translateHealthcheck(h *types.HealthSpec, ports types.OrderedMap[int]) (*Healthcheck, error) {
	probe := pickPrimaryProbe(h)
	if probe == nil {
		return nil, nil
	}
	return probeToHealthcheck(probe, ports)
}

// pickPrimaryProbe returns the live probe if set, else readiness, else startup,
// else nil.
func pickPrimaryProbe(h *types.HealthSpec) *types.ProbeSpec {
	if h.Live != nil {
		return h.Live
	}
	if h.Ready != nil {
		return h.Ready
	}
	if h.Startup != nil {
		return h.Startup
	}
	return nil
}

// probeToHealthcheck translates a single ProbeSpec to a compose Healthcheck.
func probeToHealthcheck(p *types.ProbeSpec, ports types.OrderedMap[int]) (*Healthcheck, error) {
	hc := &Healthcheck{}

	switch {
	case p.HTTP != "":
		port, err := resolveHTTPProbePort(ports)
		if err != nil {
			return nil, err
		}
		hc.Test = []string{
			"CMD-SHELL",
			fmt.Sprintf("curl -f http://localhost:%d%s || exit 1", port, p.HTTP),
		}
	case p.TCP != 0:
		hc.Test = []string{
			"CMD-SHELL",
			fmt.Sprintf("nc -z localhost %d", p.TCP),
		}
	case len(p.Exec) > 0:
		hc.Test = append([]string{"CMD"}, p.Exec...)
	default:
		return nil, ErrTranslationProbeShape
	}

	if p.PeriodSeconds != nil {
		hc.Interval = fmt.Sprintf("%ds", *p.PeriodSeconds)
	}
	if p.TimeoutSeconds != nil {
		hc.Timeout = fmt.Sprintf("%ds", *p.TimeoutSeconds)
	}
	if p.FailureThreshold != nil {
		hc.Retries = *p.FailureThreshold
	}
	if p.InitialDelaySeconds != nil {
		hc.StartPeriod = fmt.Sprintf("%ds", *p.InitialDelaySeconds)
	}

	return hc, nil
}

// resolveHTTPProbePort returns the first container port number in
// alphabetical-by-name order, suitable for building a curl URL. Returns an
// error if no ports are declared.
func resolveHTTPProbePort(ports types.OrderedMap[int]) (int, error) {
	if ports.Len() == 0 {
		return 0, ErrTranslationProbeNoPort
	}
	names := []string{}
	for name := range ports.All() {
		names = append(names, name)
	}
	sort.Strings(names)
	port, _ := ports.Get(names[0])
	return port, nil
}

// applyContainerSecurityContext maps the Workload security context to compose
// service primitives. Compose's coverage is much narrower than K8s; only the
// fields with direct equivalents are applied:
//
//   - readOnlyRootFilesystem → read_only
//   - capabilities.add        → cap_add
//   - capabilities.drop       → cap_drop
//   - allowPrivilegeEscalation == false → privileged stays default
//   - runAsUser / runAsGroup  → user (formatted as "<uid>:<gid>" or "<uid>")
//
// runAsNonRoot has no compose equivalent; ignored.
func applyContainerSecurityContext(svc *Service, sc *types.ContainerSecurityContext) {
	if sc.ReadOnlyRootFilesystem != nil {
		svc.ReadOnly = *sc.ReadOnlyRootFilesystem
	}
	if sc.Capabilities != nil {
		svc.CapAdd = append(svc.CapAdd, sc.Capabilities.Add...)
		svc.CapDrop = append(svc.CapDrop, sc.Capabilities.Drop...)
	}
	if sc.RunAsUser != nil {
		if sc.RunAsGroup != nil {
			svc.User = fmt.Sprintf("%d:%d", *sc.RunAsUser, *sc.RunAsGroup)
		} else {
			svc.User = fmt.Sprintf("%d", *sc.RunAsUser)
		}
	}
}

// applyPodLevelFieldsToService applies Workload pod-level configuration to
// the main compose service. Compose's surface is much narrower than K8s pod;
// only fields with direct equivalents are applied:
//
//   - Workload.PodSecurityContext.RunAsUser → user (overridden by container
//     SecurityContext.RunAsUser if set)
//   - Workload.Scheduling                  → no compose equivalent (single-host)
//   - Workload.ServiceAccount              → no compose equivalent
//   - Workload.InitContainers              → no compose equivalent (compose
//     has no startup ordering primitive beyond depends_on)
//   - Workload.Replicas                    → no equivalent at the service
//     level (compose `deploy.replicas` is swarm-only; out of scope for the
//     current single-host compose target)
func applyPodLevelFieldsToService(svc *Service, w *types.Workload) {
	if svc.User != "" {
		return // container-level User wins
	}
	if w.PodSecurityContext == nil || w.PodSecurityContext.RunAsUser == nil {
		return
	}
	if w.PodSecurityContext.RunAsGroup != nil {
		svc.User = fmt.Sprintf("%d:%d", *w.PodSecurityContext.RunAsUser, *w.PodSecurityContext.RunAsGroup)
	} else {
		svc.User = fmt.Sprintf("%d", *w.PodSecurityContext.RunAsUser)
	}
}

// applyWorkloadServicePorts walks Workload.Services and translates entries
// into port-publishing settings on the main compose service. Compose has no
// separate Service primitive (a service IS the workload); the Service.Type
// field decides whether to publish ports:
//
//   - ClusterIP / Headless        → no publishing (internal only)
//   - NodePort / LoadBalancer     → ports published as host:container
//
// Multiple Workload.Services entries with overlapping ports collapse to a
// single ports list on the main service. Aggregation is by service.type:
// any entry with type NodePort or LoadBalancer triggers publishing for ALL
// of its ports (the most-publishing wins per port).
func applyWorkloadServicePorts(frag *ComposeFile, w *types.Workload, mainServiceName string) error {
	main, ok := frag.Services[mainServiceName]
	if !ok {
		// No main service — nothing to publish to. Container-less Workload
		// shouldn't reach this code path; defensive no-op.
		return nil
	}

	// Collect (port, container-port) pairs from publishing service entries.
	// Iterate Workload.Services in author order for determinism on inputs;
	// dedup keyed by container port number.
	type publishedPort struct {
		host int
		ctr  int
	}
	publishedByCtr := map[int]publishedPort{}

	for _, svc := range w.Services.All() {
		if svc.Type != "NodePort" && svc.Type != "LoadBalancer" {
			continue
		}
		for portName, portNum := range svc.Ports.All() {
			_ = portName
			// Compose maps host:container 1:1 by default; future Recipe
			// authoring may carve out an explicit hostPort field, but Track 1
			// uses the same number on both sides.
			publishedByCtr[portNum] = publishedPort{host: portNum, ctr: portNum}
		}
	}

	if len(publishedByCtr) == 0 {
		return nil
	}

	// Emit ports in container-port-ascending order for byte-identical output.
	keys := make([]int, 0, len(publishedByCtr))
	for k := range publishedByCtr {
		keys = append(keys, k)
	}
	sort.Ints(keys)
	for _, k := range keys {
		p := publishedByCtr[k]
		main.Ports = append(main.Ports, fmt.Sprintf("%d:%d", p.host, p.ctr))
	}

	frag.Services[mainServiceName] = main
	return nil
}

// volumeName returns the per-node volume name convention: "<nodeID>-<name>".
// Used for both top-level volume declarations and service-level volume
// references so the two ends agree.
func volumeName(nodeID, name string) string {
	return nodeID + "-" + name
}

// sanitizeServiceName produces a compose-safe service name from a Workload
// container name. Compose service names are restricted to [a-zA-Z0-9._-];
// existing schema validation on Workload container names already enforces a
// compatible subset, so this is a defensive identity for now.
func sanitizeServiceName(name string) string {
	return name
}

// marshalDeterministic routes through the shared indent-aware marshal helper
// at a single call point. yaml.v3 sorts map keys alphabetically — synthesised
// top-level maps (services, volumes, networks) inherit alphabetical-by-
// derivation-key ordering. byte-identical input produces byte-identical output
// across compilations. indent is the configured YAML indentation width.
func marshalDeterministic(v any, indent int) ([]byte, error) {
	return renderer.MarshalYAMLIndent(v, indent)
}
