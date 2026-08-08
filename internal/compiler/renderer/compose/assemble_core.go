// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Filesystem-free per-(path, provider) merge core for the compose provider.
// Reads per-node fragments (Intermediate=true EmittedFiles produced by
// SynthesizeWorkloadCore and by sourced-mode rendering), deep-merges into a
// single docker-compose.yaml at the dispatch path.
//
// AssembleBoundaryCore takes exactly the inputs the merge consumes — the per-node
// fragments, the per-node annotations, the IR edges, the boundary-path fallback,
// and the yaml indent — so both the in-tree host engine (through the
// AssembleBoundary adapter, which projects the host BoundaryInfo) and the
// provider Wasm guest call this one function. This file carries no build tag: it
// compiles for the host and for the wasip1 guest alike, so it imports no
// host-only capability (no filesystem, no network).
//
// Merge contract.
//
//   - Per-boundary network. For each contributing node, a docker network is
//     declared whose name is the `aggrega.ai/namespace` annotation on the node,
//     or the sanitised boundary path when no namespace annotation is present.
//     Every service joins its author-boundary's network.
//   - Service collisions. If two different nodes contribute services of
//     the same name, AssembleBoundaryCore returns TRANSLATION_ERROR naming
//     every contributing (boundary, nodeID, serviceName) tuple.
//   - Top-level volumes. Per-node volume entries union across the dispatch
//     group; name collisions silently merge (named volumes are content-
//     free in the current shape, so a collision means the same physical
//     volume).
//   - Top-level configs. Per-node config declarations union the same way.
//     Every service-level configs: reference must resolve to one of them
//     once the union is complete, or AssembleBoundaryCore returns
//     TRANSLATION_ERROR naming each unresolved reference — the union is
//     what makes the check honest, since one node may reference a config a
//     different node in the dispatch group declares.
//   - depends_on inference. For each IR edge whose source and target both
//     contribute services to this dispatch group, record an ungated
//     dependency on the target service on the source service. It joins
//     whatever the fragment already carried — a synthesised init-container
//     chain — through DependsOn, which owns the merge rule and the emitted
//     shape.
//   - Author order. Map-shaped top-level fields (services, volumes,
//     networks) are alphabetised by yaml.v3 on marshal — the
//     alphabetical-by-derivation-key principle for synthesised
//     collections where no author order applies upstream. Within a
//     service, per-service collections (ports, depends_on, volumes,
//     networks) are kept in stable order: edges and IR nodes iterate
//     alphabetically; ports retain the order their fragment carried.

package compose

import (
	"fmt"
	"sort"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/rendercore"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
)

// AssembledOutputFile is the assembled compose file path emitted by
// AssembleBoundaryCore at the dispatch path.
const AssembledOutputFile = "docker-compose.yaml"

// serviceContrib records one (boundary, nodeID) tuple that contributed a
// service to the merged compose output. Used by collision detection to
// name every contributor in TRANSLATION_ERROR output.
type serviceContrib struct {
	boundary string
	nodeID   string
}

// AssembleBoundaryCore reads per-node fragments and emits a single
// docker-compose.yaml at the dispatch path. intermediatesByNode is keyed by
// nodeID; nodeAnnotations gives each node's merged annotation map (read for the
// `aggrega.ai/namespace` network discriminator); edges is the IR's directed
// `uses` edges for depends_on inference; boundaryPath is the network-name
// fallback used when a node carries no namespace annotation; yamlIndent is the
// indentation width for the assembled artifact.
func AssembleBoundaryCore(
	intermediatesByNode map[string][]rendercore.EmittedFile,
	nodeAnnotations map[string]map[string]string,
	edges []rendercore.IREdgeInfo,
	boundaryPath string,
	yamlIndent int,
) (map[string][]byte, error) {
	merged := ComposeFile{
		Services: map[string]Service{},
		Volumes:  map[string]Volume{},
		Networks: map[string]Network{},
		Configs:  map[string]Config{},
	}

	// Track which (boundary, nodeID) contributed each service for collision
	// reporting. boundaryByService allows depends_on inference to look up a
	// service's owning boundary without re-walking the input.
	contribsByService := map[string][]serviceContrib{}
	boundaryByService := map[string]string{}

	// Sorted list of every contributing node.
	allNodeIDs := make([]string, 0, len(intermediatesByNode))
	for nid := range intermediatesByNode {
		allNodeIDs = append(allNodeIDs, nid)
	}
	sort.Strings(allNodeIDs)

	// Determine the network for each node: read its annotation, fall
	// back to a per-boundary default. The namespace annotation is
	// per-component (set during the boundary annotation cascade), so
	// we read it directly per-node.
	networkNameByNode := map[string]string{}
	for _, nid := range allNodeIDs {
		ann := nodeAnnotations[nid]
		networkNameByNode[nid] = deriveNodeNetworkName(ann[rendercore.AnnotationNamespace], boundaryPath)
	}

	for _, nid := range allNodeIDs {
		fragments := intermediatesByNode[nid]
		frag, err := parseNodeFragment(nid, fragments)
		if err != nil {
			return nil, err
		}
		if frag == nil {
			continue
		}

		networkName := networkNameByNode[nid]
		merged.Networks[networkName] = Network{}

		// Volumes: union (later contributors silently overwrite empty
		// bodies — both are `<name>: {}` in the current shape).
		for k, v := range frag.Volumes {
			merged.Volumes[k] = v
		}

		// Configs: union (top-level config declarations, e.g. the
		// compose-bridge gateway's generated nginx config).
		for k, v := range frag.Configs {
			merged.Configs[k] = v
		}

		// Services: collision-detect, then merge with the network attached.
		for svcName, svc := range frag.Services {
			contribsByService[svcName] = append(contribsByService[svcName], serviceContrib{boundary: networkName, nodeID: nid})
			boundaryByService[svcName] = networkName

			// Append the network to the service's networks list. Sidecars
			// with network_mode: service:<main> keep network_mode (compose
			// forbids combining networks: with network_mode:); the
			// synthesizer never sets both.
			if svc.NetworkMode == "" {
				svc.Networks = appendUnique(svc.Networks, networkName)
			}
			merged.Services[svcName] = svc
		}
	}

	// Collision check.
	if err := checkServiceCollisions(contribsByService); err != nil {
		return nil, err
	}

	// Every service-level configs: reference resolves to a declared top-level
	// configs: entry, or the build halts.
	if err := checkConfigReferences(merged); err != nil {
		return nil, err
	}

	// depends_on inference: walk IR edges, project onto contributing
	// services. Edge endpoints not in the dispatch group are skipped.
	applyDependsOn(&merged, edges, boundaryByService)

	body, err := marshalDeterministic(&merged, yamlIndent)
	if err != nil {
		return nil, cerrors.Wrap(err, cerrors.TranslationError, cerrors.SourceLocation{}, "compose: assemble-boundary marshal")
	}

	return map[string][]byte{AssembledOutputFile: body}, nil
}

// parseNodeFragment locates Intermediate fragments in a node's intermediate-
// file list and parses them as ComposeFile, merging into one struct.
// Returns (nil, nil) when no fragments are present (defensive — should not
// occur for compose nodes since SynthesizeWorkload always emits exactly
// one).
//
// The compose fragment file's name is `<nodeID>.compose-fragment.yaml`. If
// a future version produces multiple fragments per node, the loop merges
// them into one ComposeFile struct.
func parseNodeFragment(nodeID string, intermediates []rendercore.EmittedFile) (*ComposeFile, error) {
	if len(intermediates) == 0 {
		return nil, nil
	}
	out := &ComposeFile{
		Services: map[string]Service{},
		Volumes:  map[string]Volume{},
		Networks: map[string]Network{},
		Configs:  map[string]Config{},
	}
	for _, f := range intermediates {
		var frag ComposeFile
		if err := yaml.Unmarshal(f.Body, &frag); err != nil {
			return nil, cerrors.Wrap(err, cerrors.TranslationError, cerrors.SourceLocation{File: nodeID}, fmt.Sprintf("compose: node %q fragment %q: parse", nodeID, f.Name))
		}
		for k, v := range frag.Services {
			out.Services[k] = v
		}
		for k, v := range frag.Volumes {
			out.Volumes[k] = v
		}
		for k, v := range frag.Networks {
			out.Networks[k] = v
		}
		for k, v := range frag.Configs {
			out.Configs[k] = v
		}
	}
	return out, nil
}

// sanitizeNetworkName produces a docker-compose-safe network name. Compose
// allows [a-zA-Z0-9._-]; this function replaces any other character with `_`
// and lowercases for stability.
func sanitizeNetworkName(s string) string {
	out := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z', c >= '0' && c <= '9', c == '.', c == '_', c == '-':
			out = append(out, c)
		case c >= 'A' && c <= 'Z':
			out = append(out, c+('a'-'A'))
		default:
			out = append(out, '_')
		}
	}
	if len(out) == 0 {
		return "default"
	}
	return string(out)
}

// deriveNodeNetworkName resolves the compose network a node lands on, from its
// `aggrega.ai/namespace` annotation value (empty when absent) and the boundary
// path used for the fallback. Three branches: a non-empty namespace is
// sanitized; otherwise an empty boundary path yields "default"; otherwise the
// boundary path is sanitized. Both the per-node network-assignment loop and the
// compose-bridge cross-network opt-in filter call THIS function — the network
// identity is the resolved name, never the raw namespace string — so the
// bridge's "is this edge cross-network?" decision can never drift from where
// the nodes are actually assigned.
func deriveNodeNetworkName(namespace, boundaryPath string) string {
	if namespace != "" {
		return sanitizeNetworkName(namespace)
	}
	if boundaryPath == "" {
		return "default"
	}
	return sanitizeNetworkName(boundaryPath)
}

// appendUnique appends s to list only when list does not already contain s.
func appendUnique(list []string, s string) []string {
	for _, existing := range list {
		if existing == s {
			return list
		}
	}
	return append(list, s)
}

// checkServiceCollisions walks contribsByService and returns
// TRANSLATION_ERROR when any service is contributed by more than one
// (boundary, node) tuple. The error message names every contributor in
// alphabetical order so error output is deterministic.
func checkServiceCollisions(contribsByService map[string][]serviceContrib) error {
	type collision struct {
		service     string
		descriptors []string
	}
	var collisions []collision
	for svc, contribs := range contribsByService {
		if len(contribs) <= 1 {
			continue
		}
		descriptors := make([]string, 0, len(contribs))
		for _, c := range contribs {
			descriptors = append(descriptors, fmt.Sprintf("boundary=%q node=%q", c.boundary, c.nodeID))
		}
		sort.Strings(descriptors)
		collisions = append(collisions, collision{service: svc, descriptors: descriptors})
	}
	if len(collisions) == 0 {
		return nil
	}
	sort.Slice(collisions, func(i, j int) bool { return collisions[i].service < collisions[j].service })
	parts := make([]string, 0, len(collisions))
	for _, c := range collisions {
		parts = append(parts, fmt.Sprintf("service %q contributed by [%s]", c.service, strings.Join(c.descriptors, ", ")))
	}
	return cerrors.New(cerrors.TranslationError, cerrors.SourceLocation{}, fmt.Sprintf("compose AssembleBoundary: service-name collisions across contributors: %s", strings.Join(parts, "; ")), nil, nil)
}

// checkConfigReferences walks every service's configs: references and halts when
// one names a config the assembled output does not declare at the top level. A
// configmap volume becomes such a reference, and the file behind it comes from a
// Recipe's artifacts: input, whose compose fragment carries the matching
// top-level entry — so an unresolved reference means the artifact is missing and
// the mount would be dangling at run time.
//
// The check runs over the fully assembled boundary because that is the only
// point where both halves are present: a per-node check would reject a
// reference whose config another node in the same dispatch group declares.
// Services are walked in alphabetical order and each service's references in
// their emitted order, so the reported set is deterministic.
func checkConfigReferences(merged ComposeFile) error {
	svcNames := make([]string, 0, len(merged.Services))
	for name := range merged.Services {
		svcNames = append(svcNames, name)
	}
	sort.Strings(svcNames)

	var unresolved []string
	for _, name := range svcNames {
		for _, ref := range merged.Services[name].Configs {
			if _, declared := merged.Configs[ref.Source]; declared {
				continue
			}
			unresolved = append(unresolved, fmt.Sprintf("service %q references config %q mounted at %q", name, ref.Source, ref.Target))
		}
	}
	if len(unresolved) == 0 {
		return nil
	}
	return cerrors.New(cerrors.TranslationError, cerrors.SourceLocation{},
		fmt.Sprintf("compose AssembleBoundary: %s; declare each one as a top-level configs: entry from the Recipe's artifacts: input", strings.Join(unresolved, "; ")),
		nil, ErrTranslationConfigMapMissingArtifact())
}

// applyDependsOn projects IR edges onto contributing services and records an
// ungated dependency on each edge's target service. Edges whose endpoints are
// not both contributing-service names are skipped (the edge points at a node
// served by a different provider).
//
// Targets are sorted alphabetically before they are recorded. That sort is this
// producer's own ordering contract, not the field's: a set of edges carries no
// author order, so alphabetical is what makes the output stable. DependsOn emits
// in the order dependencies are recorded, so a service that also waits on an
// init chain keeps that chain's authored order ahead of these entries rather
// than interleaving with them.
//
// Edge-derived dependencies are recorded without a condition, so a service
// depending only on edges emits the short-form list. Gating them on the target's
// health is expressible now that the field carries a condition, and is a
// separate decision about what a `uses` edge means.
func applyDependsOn(merged *ComposeFile, edges []rendercore.IREdgeInfo, boundaryByService map[string]string) {
	addedByService := map[string]map[string]struct{}{}
	for _, edge := range edges {
		_, srcContributes := boundaryByService[edge.Source]
		_, tgtContributes := boundaryByService[edge.Target]
		if !srcContributes || !tgtContributes {
			continue
		}
		if _, seen := addedByService[edge.Source]; !seen {
			addedByService[edge.Source] = map[string]struct{}{}
		}
		addedByService[edge.Source][edge.Target] = struct{}{}
	}
	for svcName, targets := range addedByService {
		svc := merged.Services[svcName]
		ordered := make([]string, 0, len(targets))
		for t := range targets {
			ordered = append(ordered, t)
		}
		sort.Strings(ordered)
		for _, target := range ordered {
			svc.DependsOn.Require(target, "")
		}
		merged.Services[svcName] = svc
	}
}
