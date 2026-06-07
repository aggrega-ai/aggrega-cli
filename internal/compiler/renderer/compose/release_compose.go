// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Per-(path, provider) merge for the compose provider. Reads per-node
// fragments (Intermediate=true EmittedFiles produced by SynthesizeWorkload
// and by sourced-mode Render) from BoundaryInfo, deep-merges into a single
// docker-compose.yaml at the dispatch path.
//
// Merge contract.
//
//   - Per-boundary network. For each contributing boundary in the dispatch
//     group, a docker network is declared whose name is the
//     `aggrega.ai/namespace` annotation on the boundary's first node
//     (sorted by NodeID), or the sanitised boundary path when no namespace
//     annotation is present. Every service joins its author-boundary's
//     network.
//   - Service collisions. If two different nodes contribute services of
//     the same name, AssembleBoundary returns TRANSLATION_ERROR naming
//     every contributing (boundary, nodeID, serviceName) tuple.
//   - Top-level volumes. Per-node volume entries union across the dispatch
//     group; name collisions silently merge (named volumes are content-
//     free in the current shape, so a collision means the same physical
//     volume).
//   - depends_on inference. For each IR edge whose source and target both
//     contribute services to this dispatch group, add `depends_on:
//     [<target-service>]` to the source service.
//   - Author order. Map-shaped top-level fields (services, volumes,
//     networks) are alphabetised by yaml.v3 on marshal — the
//     alphabetical-by-derivation-key principle for synthesised
//     collections where no author order applies upstream. Within a
//     service, lists (ports, depends_on, volumes, networks) are kept in
//     stable order: edges and IR nodes iterate alphabetically; ports
//     retain the order their fragment carried.

package compose

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
)

const (
	// AnnotationNamespace is the per-node annotation that supplies the docker
	// network name for the boundary the node lives in. All nodes in a
	// boundary should carry the same value; the merger reads from the
	// alphabetically-first node and ignores the rest.
	AnnotationNamespace = "aggrega.ai/namespace"

	// AssembledOutputFile is the assembled compose file path emitted by
	// AssembleBoundary at the dispatch path.
	AssembledOutputFile = "docker-compose.yaml"
)

// serviceContrib records one (boundary, nodeID) tuple that contributed a
// service to the merged compose output. Used by collision detection to
// name every contributor in TRANSLATION_ERROR output.
type serviceContrib struct {
	boundary string
	nodeID   string
}

// AssembleBoundary implements renderer.BoundaryAssembler. Reads per-node
// fragments from info.IntermediatesByNode and emits a single
// docker-compose.yaml at the dispatch path. nestPerNode is always false
// for compose — the assembled file is flat at the dispatch root.
func (e *ComposeEngine) AssembleBoundary(_ context.Context, info renderer.BoundaryInfo) (map[string][]byte, bool, error) {
	merged := ComposeFile{
		Services: map[string]Service{},
		Volumes:  map[string]Volume{},
		Networks: map[string]Network{},
	}

	// Track which (boundary, nodeID) contributed each service for collision
	// reporting. boundaryByService allows depends_on inference to look up a
	// service's owning boundary without re-walking the input.
	contribsByService := map[string][]serviceContrib{}
	boundaryByService := map[string]string{}

	// Iterate contributing boundaries in alphabetical order for output
	// determinism. Within each boundary, iterate nodes alphabetically.
	bps := append([]string(nil), info.ContributingBoundaries...)
	sort.Strings(bps)

	// Group node IDs per contributing boundary by inspecting the
	// IntermediatesByNode map's nodes' annotations and attributing each
	// node to its boundary path. The orchestrator records boundary
	// membership through the per-node BoundaryPath in the IR; here we
	// rely on the annotation map's keys (which match node IDs) and on the
	// per-boundary network name derivation from any-node-in-boundary.
	//
	// Compose's input shape doesn't expose per-node BoundaryPath directly
	// (BoundaryPath is per-component context, not per-engine input). We
	// derive boundary attribution via the annotation map: each node has
	// annotations including `aggrega.ai/namespace` from its boundary's
	// cascade. Boundaries with the same namespace value share a network;
	// boundaries with different namespace values get distinct networks.
	//
	// Per-boundary node grouping: collect every nodeID with an
	// IntermediatesByNode entry, sorted; per-node annotation map gives
	// the per-boundary namespace. For determinism, fall back to
	// `default` when the namespace annotation is absent (matches today's
	// per-boundary behavior).

	// Sorted list of every contributing node.
	allNodeIDs := make([]string, 0, len(info.IntermediatesByNode))
	for nid := range info.IntermediatesByNode {
		allNodeIDs = append(allNodeIDs, nid)
	}
	sort.Strings(allNodeIDs)

	// Determine the network for each node: read its annotation, fall
	// back to a per-boundary default. The first node alphabetically in
	// each contributing boundary fixes the network name (matching the
	// pre-revision deriveNetworkName behaviour).
	networkNameByNode := map[string]string{}
	{
		// Group nodes per-boundary using the per-node annotations'
		// boundary-path discriminator if present. The orchestrator's
		// pendingDispatch.contributingBoundaries lists every boundary
		// that contributed; use that plus the annotation namespace
		// lookup to derive the network name.
		// In the current input shape, the namespace annotation is
		// per-component (set during boundary annotation cascade), so
		// we read it directly per-node.
		for _, nid := range allNodeIDs {
			ann := info.NodeAnnotations[nid]
			networkNameByNode[nid] = deriveNodeNetworkName(ann[AnnotationNamespace], info.BoundaryPath)
		}
	}

	for _, nid := range allNodeIDs {
		fragments := info.IntermediatesByNode[nid]
		frag, err := parseNodeFragment(nid, fragments)
		if err != nil {
			return nil, false, err
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
		return nil, false, err
	}

	// depends_on inference: walk IR edges, project onto contributing
	// services. Edge endpoints not in the dispatch group are skipped.
	applyDependsOn(&merged, info.Edges, boundaryByService)

	body, err := marshalDeterministic(&merged)
	if err != nil {
		return nil, false, cerrors.Wrap(err, cerrors.TranslationError, cerrors.SourceLocation{}, "compose: assemble-boundary marshal")
	}

	return map[string][]byte{AssembledOutputFile: body}, false, nil
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
func parseNodeFragment(nodeID string, intermediates []renderer.EmittedFile) (*ComposeFile, error) {
	if len(intermediates) == 0 {
		return nil, nil
	}
	out := &ComposeFile{
		Services: map[string]Service{},
		Volumes:  map[string]Volume{},
		Networks: map[string]Network{},
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

// applyDependsOn projects IR edges onto contributing services and adds
// `depends_on: [<target-service>]` entries to the source service. Edges
// whose endpoints are not both contributing-service names are skipped (the
// edge points at a node served by a different provider). Depends_on entries
// sorted alphabetically for output stability.
//
// Today's emit shape is the short-form list (just service names); the
// long-form `{ <target>: { condition: service_healthy } }` shape requires a
// type-shape extension on Service.DependsOn that is reserved for a future
// intent. The short form is honest and works for compose; service_healthy
// gating is a future enhancement once Service.DependsOn becomes a map.
func applyDependsOn(merged *ComposeFile, edges []renderer.IREdgeInfo, boundaryByService map[string]string) {
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
		svc.DependsOn = append(svc.DependsOn, ordered...)
		merged.Services[svcName] = svc
	}
}
