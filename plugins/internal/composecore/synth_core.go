// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Filesystem-free Workload → Docker Compose service-fragment synthesis core for
// the compose provider.
//
// SynthesizeWorkloadCore is the single byte-identical-critical synthesis path:
// it takes the evaluated Workload and the node's render context and returns the
// per-node compose fragment. Both the in-tree host engine (through the
// SynthesizeWorkload adapter) and the provider Wasm guest (which reconstructs the
// Workload from the payload's workload projection) call this one function, so the
// two synthesis surfaces stay byte-for-byte identical. This file carries no build
// tag: it compiles for the host and for the wasip1 guest alike, so it imports no
// host-only capability (no filesystem, no network).
//
// Container model. Single-container Workloads → one compose service named
// `<nodeID>`. Multi-container Workloads → main service `<nodeID>` plus one
// sidecar service `<nodeID>-<containerName>` per non-main container; sidecars
// declare `network_mode: "service:<nodeID>"` so they share the main's
// network namespace (the closest compose primitive to a K8s pod). Each init
// container adds one more service on the same `<nodeID>-<containerName>` shape,
// configured to run once and not restart; the init services form a chain in
// authored order and every long-running service waits for the last of them to
// complete.
//
// Author-order preservation. Containers / Variables / Ports / Attachments /
// Volumes / Services iterated via OrderedMap.All(). Top-level maps
// (Services, Volumes) inside ComposeFile are Go map[string]X — yaml.v3
// sorts keys alphabetically on marshal, which is the
// alphabetical-by-derivation-key choice for synthesised collections
// where no author order applies upstream.

package composecore

import (
	"fmt"
	"sort"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/rendercore"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderctx"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/namesegment"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// composeTargetName is the user-facing name of this render target, used in the
// unsupported-field warnings emitted for authored Workload fields compose cannot honor.
const composeTargetName = "compose"

// networkModeHost is the compose network_mode value that puts a service in the
// host node's own network namespace. It is one of the values compose's
// network_mode accepts; the other one this core emits is the "service:<name>"
// form a sidecar takes to share the main service's namespace.
const networkModeHost = "host"

// SynthesizeWorkloadCore translates a parsed Workload into a per-node compose
// fragment for the compose provider. Returns one EmittedFile
// (`<nodeID>.compose-fragment.yaml`, Intermediate=true); the boundary merger
// (AssembleBoundaryCore) consumes fragments to produce the single
// docker-compose.yaml at the dispatch path.
//
// Compose's synthesis reads no per-slot attachment provides: an `as: env`
// attachment entry emits the env-var name with an empty value and raises a
// partial-support advisory naming that entry, and an `as: file` entry
// bind-mounts from the slot name — neither consumes the resolved Secret
// identity the helm core reads. The core therefore takes only the Workload and
// the node context.
func SynthesizeWorkloadCore(
	w *types.Workload,
	nodeCtx rendercore.NodeRenderContext,
) ([]rendercore.EmittedFile, []renderctx.Diagnostic, error) {
	// Companion validation (provider-agnostic): a persistentTemplate volume
	// requires form: stateful. Single-sourced in rendercore and called at the
	// entry of every provider synth core.
	if err := rendercore.ValidateWorkloadForm(w, nodeCtx.NodeID); err != nil {
		return nil, nil, err
	}

	// compose renders stateless and stateful workloads as services (stateful
	// adds the named volume). daemon, scheduled, and task have no clean
	// single-host compose mapping and halt loudly.
	switch rendercore.NormalizeWorkloadForm(w.Form) {
	case rendercore.WorkloadFormReference:
		// A reference workload has no compute, and compose has no ExternalName
		// analog. The endpoint (and optional credential) reach consumers through
		// the recipe's provides — the injection contract — so compose emits no
		// fragment for the reference node itself.
		return nil, nil, nil
	case rendercore.WorkloadFormStateless, rendercore.WorkloadFormStateful:
		// supported
	default:
		return nil, nil, cerrors.New(cerrors.TranslationError, cerrors.SourceLocation{File: nodeCtx.NodeID},
			fmt.Sprintf("compose: workload form %q has no compose mapping; compose supports stateless and stateful only", rendercore.NormalizeWorkloadForm(w.Form)),
			nil, ErrUnsupportedComposeForm())
	}

	// Unsupported-field diagnostics accumulate here in a deterministic order: per
	// container in author order (that container's dropped extended resources,
	// then its `as: env` attachment placeholders, then its runAsNonRoot), then
	// the main container's pod-level drops, then the same per-container entries
	// for each init container in authored order, then the per-volume entries — a
	// dropped size, a reduced-fidelity variant, and an unmounted declaration each
	// being a separate fact about one volume. They ride the render-output ABI back
	// to the host out-of-band; the rendered fragment bytes are unaffected.
	var diags []renderctx.Diagnostic

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
	componentID := rendercore.ProvenanceComponentID(nodeCtx)
	mainServiceName := componentID
	mainContainerName, hasMain := pickMainContainerName(w)

	// appServiceNames collects the long-running services in author order so the
	// init-container wait is wired without iterating the services map, which
	// would put Go's map order on the path to the rendered bytes.
	appServiceNames := make([]string, 0, w.Containers.Len())

	for containerName, c := range w.Containers.All() {
		isMain := hasMain && containerName == mainContainerName

		role := roleSidecar
		serviceName := mainServiceName + "-" + sanitizeServiceName(containerName)
		if isMain {
			role = roleMain
			serviceName = mainServiceName
		}

		svc, cdiags, err := translateContainer(c, "containers."+containerName, w, nodeCtx, role, mainServiceName)
		if err != nil {
			return nil, nil, cerrors.Wrap(err, cerrors.TranslationError, cerrors.SourceLocation{File: nodeCtx.NodeID}, fmt.Sprintf("compose: node %q container %q", nodeCtx.NodeID, containerName))
		}
		diags = append(diags, cdiags...)

		// Apply pod-level fields onto the main service only — sidecars share
		// the main's network namespace and pod-level identity.
		if isMain {
			diags = append(diags, applyPodLevelFieldsToService(&svc, w)...)
		}

		frag.Services[serviceName] = svc
		appServiceNames = append(appServiceNames, serviceName)
	}

	// Init containers become run-to-completion services the long-running services
	// wait on. Synthesised after the containers above so a name a container
	// already claimed is detected rather than overwritten.
	initDiags, err := synthesizeInitServices(&frag, w, nodeCtx, mainServiceName, appServiceNames)
	if err != nil {
		return nil, nil, err
	}
	diags = append(diags, initDiags...)

	// Top-level Volumes: one entry per persistent or persistentTemplate
	// volume. Author order isn't expressed here — compose volumes are a map.
	//
	// The same walk reports a declared volume no container mounts, and any part of
	// a variant the translation cannot carry. This is the only site that sees every
	// volume variant, which is what makes it the right one: the switch below emits a
	// top-level entry for the two persistent variants, so an unmounted volume of
	// those kinds at least reaches the output unused, while a configmap, secret or
	// ephemeral volume is expressed solely at a container's mount point and so
	// leaves no trace at all when nothing mounts it. Reporting from here covers
	// both without a second rule, and covers a volume whatever mounts it — a
	// dropped size is dropped just the same when a container does mount the volume.
	mounted := mountedVolumeNames(w)
	for name, v := range w.Volumes.All() {
		switch {
		case v.Persistent != nil:
			frag.Volumes[volumeName(componentID, name)] = Volume{}
		case v.PersistentTemplate != nil:
			// Degrades to the same shape as persistent: compose cannot
			// express per-replica templating, so the volume collapses to a
			// single shared persistent volume. A partial-support warning
			// records the reduced fidelity out-of-band.
			frag.Volumes[volumeName(componentID, name)] = Volume{}
			diags = append(diags, rendercore.PartialSupportDiagnostic(composeTargetName, "persistentTemplate",
				"per-replica volume templating is not available and the volume degrades to a shared persistent volume"))
		}

		// An ephemeral volume becomes a service-level tmpfs entry, whose short form
		// carries no size, so an authored sizeLimit reaches no part of the output.
		// Reported only when it is authored: the field is optional, so a volume that
		// leaves it out has lost nothing and nothing to act on. Checked here rather
		// than as a branch of the switch above, because the switch decides what the
		// volume emits and this decides nothing — keeping it outside is what makes
		// the advisory unable to perturb a single rendered byte.
		if v.Ephemeral != nil && v.Ephemeral.SizeLimit != "" {
			diags = append(diags, rendercore.UnsupportedFieldDiagnostic(composeTargetName,
				"volumes."+name+".ephemeral.sizeLimit"))
		}

		// A volume nothing mounts is a distinct fact from any fidelity loss in
		// the variant itself, so both may be reported for one volume.
		if !mounted[name] {
			diags = append(diags, unmountedVolumeDiagnostic(name, v))
		}
	}

	// Translate Workload.Services entries into port-publish settings on the
	// main service. Compose has no separate Service primitive — port
	// publishing is a service-level field. Aggregate ports across all
	// Workload.Services entries; the Type field decides whether to publish.
	if err := applyWorkloadServicePorts(&frag, w, mainServiceName); err != nil {
		return nil, nil, cerrors.Wrap(err, cerrors.TranslationError, cerrors.SourceLocation{File: nodeCtx.NodeID}, fmt.Sprintf("compose: node %q apply service ports", nodeCtx.NodeID))
	}

	body, err := marshalDeterministic(&frag, nodeCtx.YAMLIndent)
	if err != nil {
		return nil, nil, cerrors.Wrap(err, cerrors.TranslationError, cerrors.SourceLocation{File: nodeCtx.NodeID}, fmt.Sprintf("compose: node %q fragment marshal", nodeCtx.NodeID))
	}

	return []rendercore.EmittedFile{
		{
			Name:         componentID + ".compose-fragment.yaml",
			Body:         body,
			Intermediate: true,
		},
	}, diags, nil
}

// mountedVolumeNames returns the set of volume names some container of the
// workload mounts. Init containers count: their mounts are translated alongside
// the long-running containers', so a volume mounted only by an init container is
// wired and must not be reported as unmounted.
//
// The result is used for membership tests only and never iterated, so it carries
// no author order to preserve.
func mountedVolumeNames(w *types.Workload) map[string]bool {
	mounted := make(map[string]bool)
	for _, c := range w.Containers.All() {
		for _, vm := range c.VolumeMounts {
			mounted[vm.Name] = true
		}
	}
	for _, c := range w.InitContainers {
		for _, vm := range c.VolumeMounts {
			mounted[vm.Name] = true
		}
	}
	return mounted
}

// unmountedVolumeDiagnostic builds the advisory for a volume the workload
// declares that no container mounts. The consequence differs by variant, so the
// message states the one that applies: a configmap, secret or ephemeral volume
// reaches the output only through a container's mount point and so is absent
// entirely, whereas a persistent volume still becomes a named volume that
// nothing then uses.
func unmountedVolumeDiagnostic(name string, v types.VolumeSpec) renderctx.Diagnostic {
	mountPointOnly := ""
	switch {
	case v.ConfigMap != nil:
		mountPointOnly = "configmap"
	case v.Secret != nil:
		mountPointOnly = "secret"
	case v.Ephemeral != nil:
		mountPointOnly = "ephemeral"
	}

	consequence := "so the named volume is created and nothing uses it"
	if mountPointOnly != "" {
		consequence = fmt.Sprintf(
			"and this target expresses a %s volume only at a container's mount point, so nothing is emitted for it",
			mountPointOnly)
	}

	return rendercore.PartialSupportDiagnostic(composeTargetName, "volumes."+name,
		fmt.Sprintf("no container in this workload mounts it, %s; mount it from a container, or remove it", consequence))
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

// containerRole is the part a container plays in the workload, which the shared
// container translation cannot read off the ContainerSpec itself. It decides two
// things: whether the synthesised service shares the main service's network
// namespace, and whether the service runs to completion.
type containerRole int

const (
	// roleMain is the container whose service carries the node's identity, its
	// published ports, and the workload's pod-level configuration.
	roleMain containerRole = iota
	// roleSidecar is a non-main long-running container. Its service shares the
	// main service's network namespace — the closest compose primitive to the
	// shared-network model a Kubernetes pod gives co-located containers.
	roleSidecar
	// roleInit is a container that runs once and must finish before the
	// workload's long-running services start. Its service joins the boundary
	// network in its own right rather than sharing the main service's namespace:
	// the main service waits on the init service, so a shared namespace — which
	// is itself a dependency on the main service — would close that wait into a
	// cycle compose refuses to start.
	roleInit
)

// synthesizeInitServices translates each init container into its own
// run-to-completion service and wires the ordering both ways: each init service
// after the first waits for its predecessor to complete, and every long-running
// service named in appServiceNames waits for the last one. That mirrors the
// authoring contract, where the init containers run to completion in the order
// they are written and every other container starts after all of them.
//
// Init services are named on the same `<component>-<container>` shape as
// sidecars, so they share one namespace with them; a name already taken halts
// rather than overwriting the service that claimed it. An unnamed entry falls
// back to `init-<index>`, the name the authoring contract documents for a
// positional entry — the same fallback the Kubernetes-shaped targets emit, and
// it must stay the same on both so one authored workload yields one set of
// container names everywhere.
//
// Pod-level configuration is deliberately not applied. An init service is a
// non-main service, and this synthesiser applies pod-level fields to the main
// service only; a third policy would be a new rule with nothing behind it, and
// re-running the pod-level pass per init container would also repeat every
// pod-level diagnostic once per init container.
func synthesizeInitServices(
	frag *ComposeFile,
	w *types.Workload,
	nodeCtx rendercore.NodeRenderContext,
	mainServiceName string,
	appServiceNames []string,
) ([]renderctx.Diagnostic, error) {
	if len(w.InitContainers) == 0 {
		return nil, nil
	}

	var diags []renderctx.Diagnostic
	var previous string

	for i, c := range w.InitContainers {
		containerName := c.Name
		if containerName == "" {
			containerName = fmt.Sprintf("init-%d", i)
		}
		serviceName := mainServiceName + "-" + sanitizeServiceName(containerName)

		if _, taken := frag.Services[serviceName]; taken {
			return nil, cerrors.New(cerrors.TranslationError, cerrors.SourceLocation{File: nodeCtx.NodeID},
				fmt.Sprintf("compose: node %q init container %q needs the service name %q, which another container of the same workload already uses; give every entry of containers and initContainers a distinct name",
					nodeCtx.NodeID, containerName, serviceName),
				nil, nil)
		}

		svc, cdiags, err := translateContainer(c, fmt.Sprintf("initContainers[%d]", i), w, nodeCtx, roleInit, mainServiceName)
		if err != nil {
			return nil, cerrors.Wrap(err, cerrors.TranslationError, cerrors.SourceLocation{File: nodeCtx.NodeID}, fmt.Sprintf("compose: node %q init container %q", nodeCtx.NodeID, containerName))
		}
		diags = append(diags, cdiags...)

		if previous != "" {
			svc.DependsOn.Require(previous, conditionCompletedSuccessfully)
		}

		frag.Services[serviceName] = svc
		previous = serviceName
	}

	// Every long-running service waits on the last init service. Waiting on the
	// last one is sufficient because the init services form a chain, and it keeps
	// one dependency per waiter instead of one per init container.
	for _, name := range appServiceNames {
		svc := frag.Services[name]
		svc.DependsOn.Require(previous, conditionCompletedSuccessfully)
		frag.Services[name] = svc
	}

	return diags, nil
}

// translateContainer maps a single Workload ContainerSpec to a compose Service.
// Author order preserved across env vars / ports / attachments via OrderedMap.All().
// role carries the part the container plays; every container reaches the same
// image, command, environment, volume-mount and security translation regardless
// of it.
//
// containerPath is the authored path to this container within the Workload —
// `containers.<name>` for a named container, `initContainers[<i>]` for a
// positional init entry. The caller that iterated the collection supplies it
// rather than this function deriving it, because a named container's name is the
// collection key rather than a field of ContainerSpec, and an init entry's
// coordinate is its list index rather than any name. The two spellings mirror
// the container-path prefixes the Workload attachment validator builds its
// finding paths from (that surface prefixes them with a `.` separator), so one
// authored entry is located the same way whether a build rejects it or degrades
// it.
//
// containerPath reaches the diagnostics raised here and nothing else, so it
// never affects the rendered bytes.
func translateContainer(
	c types.ContainerSpec,
	containerPath string,
	w *types.Workload,
	nodeCtx rendercore.NodeRenderContext,
	role containerRole,
	mainServiceName string,
) (Service, []renderctx.Diagnostic, error) {
	var diags []renderctx.Diagnostic

	svc := Service{
		Image:  rendercore.ApplyImageRegistry(c.Image, rendercore.ImageRegistryFromAnnotations(nodeCtx.Annotations)),
		Labels: buildAggregaLabels(nodeCtx),
	}

	// Lifecycle hooks have no compose primitive. A silently-unrun postStart/
	// preStop hook is a correctness surprise, so a declared lifecycle halts
	// loudly rather than drop the hook (the loud-halt discipline for a shape
	// compose cannot express).
	if c.Lifecycle != nil {
		return Service{}, nil, cerrors.New(cerrors.TranslationError, cerrors.SourceLocation{File: nodeCtx.NodeID},
			fmt.Sprintf("compose: node %q declares container lifecycle hooks; compose has no lifecycle-hook primitive", nodeCtx.NodeID),
			nil, ErrUnsupportedComposeLifecycle())
	}

	// Container resource requests/limits → compose deploy.resources
	// (reservations/limits). Honored by docker compose in non-swarm mode.
	deploy, err := translateComposeResources(c.Resources, nodeCtx.NodeID)
	if err != nil {
		return Service{}, nil, err
	}
	svc.Deploy = deploy

	// Extended (vendor/domain-scoped) resource quantities such as nvidia.com/gpu
	// have no single-host compose analog — only cpu and memory map to
	// deploy.resources — so each authored extended key is dropped with a warning.
	diags = append(diags, extendedResourceDiagnostics(c.Resources)...)

	// envFrom (bulk env from a ConfigMap or Secret object) has no compose
	// primitive; a source reference is not a host file, so any env_file
	// reinterpretation would silently change meaning. Halt loudly.
	if len(c.EnvFrom) > 0 {
		return Service{}, nil, cerrors.New(cerrors.TranslationError, cerrors.SourceLocation{File: nodeCtx.NodeID},
			fmt.Sprintf("compose: node %q declares container envFrom; compose has no env-from-object primitive", nodeCtx.NodeID),
			nil, ErrUnsupportedComposeEnvFrom())
	}

	// imagePullPolicy → compose pull_policy (schema-validated enum).
	switch c.ImagePullPolicy {
	case "":
		// unset — leave the compose default
	case "Always":
		svc.PullPolicy = "always"
	case "Never":
		svc.PullPolicy = "never"
	case "IfNotPresent":
		svc.PullPolicy = "if_not_present"
	default:
		return Service{}, nil, cerrors.New(cerrors.TranslationError, cerrors.SourceLocation{File: nodeCtx.NodeID},
			fmt.Sprintf("compose: node %q unknown imagePullPolicy %q", nodeCtx.NodeID, c.ImagePullPolicy),
			nil, nil)
	}

	// K8s command/args map to compose entrypoint/command, not one-to-one:
	// K8s `command` overrides the image ENTRYPOINT, which is compose
	// `entrypoint`; K8s `args` supplies arguments to the entrypoint, which is
	// compose `command` (overriding the image CMD).
	if len(c.Command) > 0 {
		svc.Entrypoint = c.Command
	}
	if len(c.Args) > 0 {
		svc.Command = c.Args
	}

	// Environment variables. Compose `environment:` accepts both list and map
	// forms — we emit map form for clarity, backed by an OrderedMap so the
	// rendered key order is the container's author order (Variables then, below,
	// attachments), matching the helm engine's ordered env for the same Workload.
	for k, v := range c.Variables.All() {
		svc.Environment.Set(k, v)
	}

	// Container attachments:
	//   - as: env  → add an Environment entry keyed by the attachment key with
	//     an empty value, and raise a partial-support advisory naming the entry.
	//     Value-from-reference env syntax is Kubernetes-only, and compose cannot
	//     source an environment variable from an attachment, so the name is
	//     emitted and the value left empty; populating it from the attachment's
	//     resolved material through a compose env-file is not yet implemented.
	//   - as: file → bind-mount the slot name at the entry's path, read-only.
	for slotKey, ref := range c.Attachments.All() {
		switch ref.As {
		case "env":
			// The placeholder is deliberately kept rather than dropped, and the
			// advisory is what makes it honest: an empty variable the author is
			// told about is debuggable, whereas a variable that simply never
			// appears leaves nothing to debug from.
			svc.Environment.Set(slotKey, "")
			diags = append(diags, rendercore.PartialSupportDiagnostic(composeTargetName,
				containerPath+".attachments."+slotKey,
				"compose cannot source an environment variable from an attachment, so the variable is emitted with an empty value"))
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
		switch entry.Type {
		case volumeRedirectTmpfs:
			// compose's `tmpfs:` is a top-level service field, not a
			// volumes: entry.
			svc.Tmpfs = append(svc.Tmpfs, entry.Target)
		case volumeRedirectConfig:
			// A configmap is delivered through compose's own configs:
			// primitive, so the mount becomes a service-level reference to
			// the config of that name rather than a volumes: entry.
			svc.Configs = append(svc.Configs, ServiceConfig{Source: entry.Source, Target: entry.Target})
		default:
			svc.Volumes = append(svc.Volumes, entry)
		}
	}

	// Probes → healthcheck. Compose only supports a single healthcheck per
	// service; the three Workload probe slots collapse to one. Order
	// of preference: liveness > readiness > startup (most common compose
	// signal is liveness equivalent). A collapse that discards an authored slot
	// raises a partial-support warning naming the discarded slots, so authored
	// intent that reaches no part of the output is reported rather than lost
	// silently.
	if c.Health != nil {
		hc, hdiags, err := translateHealthcheck(c.Health, c.Ports, containerPath+".health")
		if err != nil {
			return Service{}, nil, err
		}
		diags = append(diags, hdiags...)
		if hc != nil {
			svc.Healthcheck = hc
		}
	}

	// Container security primitives that compose supports.
	if c.SecurityContext != nil {
		diags = append(diags, applyContainerSecurityContext(&svc, c.SecurityContext)...)
	}

	// Sidecar services share the main service's network namespace. An init
	// service must not — see roleInit.
	if role == roleSidecar {
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

	// An init service never restarts, whatever restart policy the container
	// carries: a service that comes back up never reaches the completed state the
	// services waiting on it are gated on, so a restarting init service would
	// hold the whole workload at the starting line.
	if role == roleInit {
		svc.Restart = "no"
	}

	return svc, diags, nil
}

// extendedResourceDiagnostics returns one unsupported-field warning per distinct
// extended (vendor/domain-scoped) resource key authored on the container's
// requests or limits — keys such as nvidia.com/gpu that have no single-host
// compose analog (only cpu and memory map to deploy.resources). Keys are
// reported in a deterministic order: limits in author order, then any
// requests-only keys in author order, each key deduplicated across the two.
func extendedResourceDiagnostics(r *types.ResourceSpec) []renderctx.Diagnostic {
	if r == nil {
		return nil
	}
	var diags []renderctx.Diagnostic
	seen := map[string]bool{}
	emit := func(rl *types.ResourceList) {
		if rl == nil {
			return
		}
		for key := range rl.Extended.All() {
			if seen[key] {
				continue
			}
			seen[key] = true
			diags = append(diags, rendercore.UnsupportedFieldDiagnostic(composeTargetName, key))
		}
	}
	emit(r.Limits)
	emit(r.Requests)
	return diags
}

// Redirect markers volumeMountEntry returns in the Type field for the two
// variants compose expresses through a service-level field rather than a
// volumes: entry. Neither value is a compose volumes: type, so a marker that
// reached the emitted bytes would be invalid compose — which is what makes
// handling them at the call site the only correct handling, and why the marker
// strings have one home here rather than one per site.
const (
	volumeRedirectTmpfs  = "tmpfs"
	volumeRedirectConfig = "config"
)

// volumeMountEntry produces the compose ServiceVolume entry for a given
// (VolumeMount, VolumeSpec) pair. Two variants return a redirect marker in the
// Type field instead of a volumes: entry, and the caller moves each to the
// service-level field that expresses it: an ephemeral volume returns
// volumeRedirectTmpfs (compose tmpfs is a service field, not a volumes: entry),
// and a configmap volume returns volumeRedirectConfig carrying the configmap
// name in Source and the mount path in Target.
func volumeMountEntry(vm types.VolumeMount, spec types.VolumeSpec, nodeID string) ServiceVolume {
	switch {
	case spec.Ephemeral != nil:
		return ServiceVolume{Type: volumeRedirectTmpfs, Target: vm.Path}
	case spec.ConfigMap != nil:
		// A configmap is delivered through compose's own configs: primitive,
		// referenced by the name the configmap volume declares — the same name
		// the Recipe's artifacts: input declares as a top-level configs: entry.
		// The name is piped through and nothing reads the config's content, so
		// the reference here mirrors the ConfigMap reference a Kubernetes target
		// emits for the same authored volume. The mount's readOnly flag has no
		// counterpart: a configs: reference carries the config name and the
		// mount path, and nothing else.
		return ServiceVolume{
			Type:   volumeRedirectConfig,
			Source: spec.ConfigMap.Source,
			Target: vm.Path,
		}
	case spec.Secret != nil:
		// A secret volume stays a host-path bind mount. Delivering it as a
		// configs: entry would mean writing the secret's value into the emitted
		// compose file in plaintext, and the compiler never resolves a secret
		// into plaintext — a secret stays a pointer through every stage of the
		// build. So the two variants diverge deliberately: the operator makes
		// the path reachable on the compose host.
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

// translateHealthcheck translates the Workload health block (3 probe slots) to a
// single compose Healthcheck. Order of preference: live > ready > startup.
// Returns nil when no probe is set.
//
// A compose service carries one healthcheck, so an author who declares more than
// one probe slot keeps only the preferred one and the rest reach no part of the
// output. healthPath is the authored path to the health block, used solely to
// locate the partial-support warning that reports the dropped slots by name; it
// never affects the rendered bytes. Returns no diagnostic when nothing is
// dropped, which is every health block declaring a single probe slot.
func translateHealthcheck(h *types.HealthSpec, ports types.OrderedMap[types.PortSpec], healthPath string) (*Healthcheck, []renderctx.Diagnostic, error) {
	probe, selected := pickPrimaryProbe(h)
	if probe == nil {
		return nil, nil, nil
	}

	hc, err := probeToHealthcheck(probe, ports)
	if err != nil {
		return nil, nil, err
	}

	var diags []renderctx.Diagnostic
	if dropped := droppedProbeSlots(h, selected); len(dropped) > 0 {
		diags = append(diags, rendercore.PartialSupportDiagnostic(composeTargetName, healthPath,
			fmt.Sprintf("a service carries a single healthcheck, so only the %s probe is translated and the %s dropped",
				selected, describeDroppedSlots(dropped))))
	}
	return hc, diags, nil
}

// pickPrimaryProbe returns the live probe if set, else readiness, else startup,
// else nil, together with the authored name of the slot it selected ("" when no
// slot is set). The name is returned rather than left for a caller to recover by
// comparing the result against each field: this function is the one place the
// preference order is expressed, so it is also the one place that can say which
// slot the order chose.
func pickPrimaryProbe(h *types.HealthSpec) (*types.ProbeSpec, string) {
	if h.Live != nil {
		return h.Live, "live"
	}
	if h.Ready != nil {
		return h.Ready, "ready"
	}
	if h.Startup != nil {
		return h.Startup, "startup"
	}
	return nil, ""
}

// droppedProbeSlots returns the authored names of every probe slot the health
// block declares that the given selected slot is not — the slots whose authored
// intent reaches no part of the rendered output.
//
// Slots are visited in the order the health block declares them (ready, live,
// startup), which is fixed at compile time because they are named struct fields
// rather than map entries. No map is on this path, so the reported order carries
// no iteration freedom.
func droppedProbeSlots(h *types.HealthSpec, selected string) []string {
	slots := []struct {
		name  string
		probe *types.ProbeSpec
	}{
		{"ready", h.Ready},
		{"live", h.Live},
		{"startup", h.Startup},
	}

	var dropped []string
	for _, s := range slots {
		if s.probe != nil && s.name != selected {
			dropped = append(dropped, s.name)
		}
	}
	return dropped
}

// describeDroppedSlots renders a dropped-slot list as the tail of a sentence:
// "ready probe is", "ready and startup probes are". Written for any length so a
// further probe slot needs no change here.
//
// The empty list returns the empty string rather than indexing off the front of
// the slice. Its caller only builds a message when at least one slot was dropped,
// so this total behavior is what keeps that guard from being the only thing
// standing between an empty list and a panic.
func describeDroppedSlots(names []string) string {
	switch len(names) {
	case 0:
		return ""
	case 1:
		return names[0] + " probe is"
	}
	return strings.Join(names[:len(names)-1], ", ") + " and " + names[len(names)-1] + " probes are"
}

// probeToHealthcheck translates a single ProbeSpec to a compose Healthcheck.
func probeToHealthcheck(p *types.ProbeSpec, ports types.OrderedMap[types.PortSpec]) (*Healthcheck, error) {
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
		return nil, ErrTranslationProbeShape()
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
func resolveHTTPProbePort(ports types.OrderedMap[types.PortSpec]) (int, error) {
	if ports.Len() == 0 {
		return 0, ErrTranslationProbeNoPort()
	}
	names := []string{}
	for name := range ports.All() {
		names = append(names, name)
	}
	sort.Strings(names)
	port, _ := ports.Get(names[0])
	return port.Port, nil
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
// runAsNonRoot has no compose equivalent; an authored value is dropped with a
// unsupported-field warning. Returns one diagnostic per authored-and-dropped field.
func applyContainerSecurityContext(svc *Service, sc *types.ContainerSecurityContext) []renderctx.Diagnostic {
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

	var diags []renderctx.Diagnostic
	if sc.RunAsNonRoot != nil {
		diags = append(diags, rendercore.UnsupportedFieldDiagnostic(composeTargetName, "runAsNonRoot"))
	}
	return diags
}

// applyPodLevelFieldsToService applies Workload pod-level configuration to
// the main compose service. Compose's surface is much narrower than K8s pod;
// only fields with direct equivalents are applied:
//
//   - Workload.PodSecurityContext.RunAsUser → user (overridden by container
//     SecurityContext.RunAsUser if set)
//   - Workload.HostNetwork                 → network_mode: host
//   - Workload.Scheduling                  → no compose equivalent (single-host)
//   - Workload.ServiceAccount              → no compose equivalent
//   - Workload.Replicas                    → no equivalent at the service
//     level (compose `deploy.replicas` is swarm-only; out of scope for the
//     current single-host compose target)
//
// Workload.InitContainers is honored, and by synthesizeInitServices rather than
// here: an init container becomes its own run-to-completion service, so it is not
// a field applied onto an existing service.
//
// Every authored pod-level field compose cannot honor — scheduling,
// serviceAccount, replicas, runtimeClassName, priorityClassName,
// imagePullSecrets, dnsPolicy, and dnsConfig.options — is dropped with a
// unsupported-field warning. Returns one diagnostic per authored-and-dropped field, in
// a deterministic order.
func applyPodLevelFieldsToService(svc *Service, w *types.Workload) []renderctx.Diagnostic {
	applyPodUser(svc, w)

	// terminationGracePeriodSeconds → stop_grace_period ("<n>s").
	if w.TerminationGracePeriodSeconds != nil {
		svc.StopGracePeriod = fmt.Sprintf("%ds", *w.TerminationGracePeriodSeconds)
	}

	// dnsConfig nameservers/searches → dns / dns_search (author order). dnsPolicy
	// and dnsConfig.options have no compose analog and are dropped with a warning.
	if w.DNSConfig != nil {
		svc.DNS = w.DNSConfig.Nameservers
		svc.DNSSearch = w.DNSConfig.Searches
	}

	// hostAliases → extra_hosts, one "hostname:ip" entry per hostname, in
	// author order.
	for _, ha := range w.HostAliases {
		for _, host := range ha.Hostnames {
			svc.ExtraHosts = append(svc.ExtraHosts, host+":"+ha.IP)
		}
	}

	// hostNetwork → network_mode: host. The boundary merger reads a set
	// network_mode as the signal to leave the service off the boundary network,
	// because compose refuses a service declaring both, so setting this here is
	// the whole change: nothing else attaches the network.
	if w.HostNetwork {
		svc.NetworkMode = networkModeHost
	}

	var diags []renderctx.Diagnostic
	if w.Scheduling != nil {
		diags = append(diags, rendercore.UnsupportedFieldDiagnostic(composeTargetName, "scheduling"))
	}
	if w.ServiceAccount != "" {
		diags = append(diags, rendercore.UnsupportedFieldDiagnostic(composeTargetName, "serviceAccount"))
	}
	if w.Replicas != nil {
		diags = append(diags, rendercore.UnsupportedFieldDiagnostic(composeTargetName, "replicas"))
	}
	if w.RuntimeClassName != "" {
		diags = append(diags, rendercore.UnsupportedFieldDiagnostic(composeTargetName, "runtimeClassName"))
	}
	if w.PriorityClassName != "" {
		diags = append(diags, rendercore.UnsupportedFieldDiagnostic(composeTargetName, "priorityClassName"))
	}
	if len(w.ImagePullSecrets) > 0 {
		diags = append(diags, rendercore.UnsupportedFieldDiagnostic(composeTargetName, "imagePullSecrets"))
	}
	if w.DNSPolicy != "" {
		diags = append(diags, rendercore.UnsupportedFieldDiagnostic(composeTargetName, "dnsPolicy"))
	}
	if w.DNSConfig != nil && len(w.DNSConfig.Options) > 0 {
		diags = append(diags, rendercore.UnsupportedFieldDiagnostic(composeTargetName, "dnsConfig.options"))
	}
	// An init container becomes its own service, and that service joins the
	// boundary network in its own right rather than sharing this one's namespace
	// — sharing it would make this service's wait on the init service circular.
	// So the workload's own service runs on the node's network while its init
	// services do not, which is narrower than the pod shape, and the difference
	// is reported rather than left for the author to discover at run time.
	// Reported only when both are authored: with no init containers there is no
	// difference to report.
	if w.HostNetwork && len(w.InitContainers) > 0 {
		diags = append(diags, rendercore.PartialSupportDiagnostic(composeTargetName, "hostNetwork",
			"the workload's own service runs on the node's network while each init container's service joins the boundary network in its own right"))
	}
	return diags
}

// applyPodUser sets the compose service user from the pod-level security
// context, unless a container-level User already won.
func applyPodUser(svc *Service, w *types.Workload) {
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
//
// A published port carries the transport declared on the container port its
// name resolves to, as compose's `/protocol` suffix. A port with no declared
// transport publishes as it always has, with no suffix, so compose's own
// default stays in force. A transport compose cannot spell halts.
func applyWorkloadServicePorts(frag *ComposeFile, w *types.Workload, mainServiceName string) error {
	main, ok := frag.Services[mainServiceName]
	if !ok {
		// No main service — nothing to publish to. Container-less Workload
		// shouldn't reach this code path; defensive no-op.
		return nil
	}

	containerPorts := w.ContainerPortsByName()

	// Collect (port, container-port) pairs from publishing service entries.
	// Iterate Workload.Services in author order for determinism on inputs;
	// dedup keyed by container port number.
	type publishedPort struct {
		host  int
		ctr   int
		proto string
	}
	publishedByCtr := map[int]publishedPort{}

	for _, svc := range w.Services.All() {
		if svc.Type != "NodePort" && svc.Type != "LoadBalancer" {
			continue
		}
		for portName, portNum := range svc.Ports.All() {
			// The transport belongs to the container port this entry names,
			// which is the port's one declaration site. An entry naming no
			// declared container port resolves to the zero value, whose
			// protocol is empty — the same no-suffix publish as before, and
			// the missing-name condition itself is reported by the helm side's
			// own resolution rather than invented here.
			suffix, err := composePortProtocolSuffix(containerPorts[portName].Protocol)
			if err != nil {
				return err
			}
			// Compose maps host:container 1:1: the same number is used on
			// both sides. An explicit per-port host-port authoring field is
			// not implemented.
			publishedByCtr[portNum] = publishedPort{host: portNum, ctr: portNum, proto: suffix}
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
		main.Ports = append(main.Ports, fmt.Sprintf("%d:%d%s", p.host, p.ctr, p.proto))
	}

	frag.Services[mainServiceName] = main
	return nil
}

// composePortProtocolSuffix maps a declared transport to the suffix a compose
// published port carries, and returns the empty string when nothing was
// declared so the entry publishes exactly as it did before ports could carry a
// transport at all. A transport outside compose's tcp/udp vocabulary halts.
func composePortProtocolSuffix(protocol string) (string, error) {
	switch protocol {
	case "":
		return "", nil
	case "TCP":
		return "/tcp", nil
	case "UDP":
		return "/udp", nil
	default:
		return "", ErrUnsupportedComposePortProtocol()
	}
}

// volumeNameEncoding is the segment encoding a compose volume name is built
// with.
//
// A compose volume is shared storage, and the top-level volumes: block unions
// contributions by name — so two authored volumes arriving at one name become
// one volume both containers write into, with nothing in the build to report
// it. The derivation therefore encodes each part rather than concatenating the
// two, and joins the encoded parts with a sequence neither can produce.
//
// The alphabet is stated here rather than taken from the encoding, because it
// is a property of what compose accepts in a volume name rather than of the
// encoding: a compose volume name carries letters, digits, dots, underscores
// and hyphens. The hyphen is the encoding's own — it opens every escape and
// composes the join — so an alphabet may not admit it; everything else compose
// accepts is carried through, which is what keeps an ordinary name readable.
var volumeNameEncoding = namesegment.NewEncoding(isVolumeNameRune)

// isVolumeNameRune reports whether a character is carried through the encoding
// as itself: an ASCII letter, a digit, a dot or an underscore. Every other
// character is escaped.
func isVolumeNameRune(r rune) bool {
	return r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' ||
		r >= '0' && r <= '9' || r == '.' || r == '_'
}

// volumeName derives the compose volume name for one authored volume of one
// node, from the node's identity and the name the author gave the volume. Both
// the top-level volume declaration and every service-level reference to it are
// built here, so the two ends agree.
//
// The derivation is injective: two calls produce one name only where both the
// node identity and the volume name match. Each part is encoded rather than
// carried through, so parts differing only in punctuation or in case stay
// distinct, and the two-hyphen join is a sequence no encoded part contains, so
// a name cannot be re-divided into a different pair — which a single hyphen
// could not promise, since either part may carry one. The name is byte-stable
// across builds with identical input.
func volumeName(nodeID, name string) string {
	return volumeNameEncoding.Name(nodeID, name)
}

// sanitizeServiceName carries a Workload container name through as the compose
// service name it contributes to.
//
// It is an identity, and the name says what it is reserved for rather than what
// it does: compose service names admit letters, digits, dots, underscores and
// hyphens, and this is the one place a narrowing would belong if one were added.
//
// What it does not do is the part worth stating, because assuming otherwise is
// how a caller comes to trust it. A container name is declared as a string with
// no character constraint on it, so nothing upstream narrows what arrives here
// and nothing here narrows it either — a container name carrying a character a
// compose service name does not admit reaches the emitted file as authored. The
// name is also joined to the node's identity by a single hyphen at the two call
// sites that build an init service name, and a hyphen is a character a container
// name may carry, so two distinct pairs can reach one service name and compose
// unions services by name.
func sanitizeServiceName(name string) string {
	return name
}

// marshalDeterministic routes through the shared indent-aware marshal helper
// at a single call point. yaml.v3 sorts map keys alphabetically — synthesised
// top-level maps (services, volumes, networks) inherit alphabetical-by-
// derivation-key ordering. byte-identical input produces byte-identical output
// across compilations. indent is the configured YAML indentation width.
func marshalDeterministic(v any, indent int) ([]byte, error) {
	return rendercore.MarshalYAMLIndent(v, indent)
}
