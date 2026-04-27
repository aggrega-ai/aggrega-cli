// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Release-level merge for the compose provider. Reads per-node fragments
// (Intermediate=true EmittedFiles produced by SynthesizeWorkload and by
// sourced-mode Render) from ReleaseInfo, deep-merges into a single
// docker-compose.yaml at the Release output root.
//
// Merge contract.
//
//   - Per-boundary network. For each boundary contributing to the Release,
//     a docker network is declared whose name is the
//     `aggrega.ai/namespace` annotation on the boundary's first node (sorted
//     by NodeID), or the sanitised boundary path when no namespace
//     annotation is present. Every service joins its boundary's network.
//   - Service collisions. If two different nodes contribute services of
//     the same name, ComposeRelease returns TRANSLATION_ERROR naming every
//     contributing (boundary, nodeID, serviceName) tuple.
//   - Top-level volumes. Per-node volume entries union across the Release;
//     name collisions silently merge (named volumes are content-free in
//     the current shape, so a collision means the same physical volume).
//   - depends_on inference. For each IR edge whose source and target both
//     contribute to this ReleaseComposer, add `depends_on: { <target>: ... }`
//     to the source service. Condition is `service_healthy` when the target
//     service has a healthcheck declared; `service_started` otherwise.
//   - Author order. Map-shaped top-level fields (services, volumes, networks)
//     are alphabetised by yaml.v3 on marshal — the
//     alphabetical-by-derivation-key principle for synthesised collections
//     where no author order applies upstream. Within a service, lists
//     (ports, depends_on, volumes, networks) are kept in stable order:
//     edges and IR nodes iterate alphabetically; ports retain the order
//     their fragment carried.

package compose

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/cognitio/aggrega-cli/internal/compiler/renderer"
	"github.com/cognitio/aggrega-cli/pkg/types"
)

const (
	// AnnotationNamespace is the per-node annotation that supplies the docker
	// network name for the boundary the node lives in. All nodes in a
	// boundary should carry the same value; the merger reads from the
	// alphabetically-first node and ignores the rest.
	AnnotationNamespace = "aggrega.ai/namespace"

	// ReleaseOutputFile is the final compose file path at the Release
	// output root.
	ReleaseOutputFile = "docker-compose.yaml"
)

// serviceContrib records one (boundary, nodeID) tuple that contributed a
// service to the merged Release. Used by collision detection to name every
// contributor in TRANSLATION_ERROR output.
type serviceContrib struct {
	boundary string
	nodeID   string
}

// ComposeRelease implements renderer.ReleaseComposer. Reads per-node
// fragments from info.Boundaries and emits a single docker-compose.yaml at
// the Release output root.
func (e *ComposeEngine) ComposeRelease(_ context.Context, info renderer.ReleaseInfo) (map[string][]byte, error) {
	merged := ComposeFile{
		Services: map[string]Service{},
		Volumes:  map[string]Volume{},
		Networks: map[string]Network{},
	}

	// Track which (boundary, nodeID) contributed each service for collision
	// reporting. boundaryByService allows depends_on inference to look up a
	// service's owning network without re-walking the boundary tree.
	contribsByService := map[string][]serviceContrib{}
	boundaryByService := map[string]string{}

	for _, bi := range info.Boundaries {
		networkName := deriveNetworkName(bi)
		merged.Networks[networkName] = Network{}

		for _, node := range bi.Nodes {
			frag, err := parseNodeFragment(node)
			if err != nil {
				return nil, err
			}
			if frag == nil {
				continue
			}

			// Volumes: union (later contributors silently overwrite empty
			// bodies — both are `<name>: {}` in the current shape).
			for k, v := range frag.Volumes {
				merged.Volumes[k] = v
			}

			// Services: collision-detect, then merge with the boundary
			// network attached.
			for svcName, svc := range frag.Services {
				contribsByService[svcName] = append(contribsByService[svcName], serviceContrib{boundary: bi.BoundaryPath, nodeID: node.NodeID})
				boundaryByService[svcName] = bi.BoundaryPath

				// Append the boundary network to the service's networks
				// list. Sidecars with network_mode: service:<main> keep
				// network_mode (compose forbids combining networks: with
				// network_mode:); the synthesizer never sets both.
				if svc.NetworkMode == "" {
					svc.Networks = appendUnique(svc.Networks, networkName)
				}
				merged.Services[svcName] = svc
			}
		}
	}

	// Collision check.
	if err := checkServiceCollisions(contribsByService); err != nil {
		return nil, err
	}

	// depends_on inference: walk graph edges, project onto contributing
	// services. service_healthy when the target has a healthcheck;
	// service_started otherwise. The ReleaseComposer interface does not
	// receive the IR edges directly today's `depends_on:` field on Service
	// is a flat []string; full long-form (with condition object) lands when
	// the schema gains it. For now: emit `service_started` form via
	// flat list; healthcheck-based gating arrives with the long-form
	// extension.
	applyDependsOn(&merged, info.Edges, boundaryByService)

	body, err := marshalDeterministic(&merged)
	if err != nil {
		return nil, fmt.Errorf("compose: release-compose marshal: %w", err)
	}

	return map[string][]byte{ReleaseOutputFile: body}, nil
}

// parseNodeFragment locates the single Intermediate fragment in a node's
// IntermediateFiles list and parses it as ComposeFile. Returns (nil, nil)
// when no fragment is present (defensive — should not occur for compose
// nodes since SynthesizeWorkload always emits exactly one).
//
// The compose fragment file's name is `<nodeID>.compose-fragment.yaml`. If a
// future version produces multiple fragments per node, the loop merges them
// into one ComposeFile struct.
func parseNodeFragment(node renderer.ReleaseNodeInfo) (*ComposeFile, error) {
	if len(node.IntermediateFiles) == 0 {
		return nil, nil
	}
	out := &ComposeFile{
		Services: map[string]Service{},
		Volumes:  map[string]Volume{},
		Networks: map[string]Network{},
	}
	for _, f := range node.IntermediateFiles {
		var frag ComposeFile
		if err := yaml.Unmarshal(f.Body, &frag); err != nil {
			return nil, fmt.Errorf("compose: node %q fragment %q: parse: %w", node.NodeID, f.Name, err)
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

// deriveNetworkName picks the docker network name for one boundary. Reads
// the alphabetically-first node's `aggrega.ai/namespace` annotation; falls
// back to a sanitised boundary path when the annotation is unset. Empty
// boundary path (the implicit "default" boundary) becomes "default".
func deriveNetworkName(bi renderer.ReleaseBoundaryInfo) string {
	nodeIDs := make([]string, 0, len(bi.Annotations))
	for nid := range bi.Annotations {
		nodeIDs = append(nodeIDs, nid)
	}
	sort.Strings(nodeIDs)
	for _, nid := range nodeIDs {
		ann := bi.Annotations[nid]
		if v, ok := ann[AnnotationNamespace]; ok && v != "" {
			return sanitizeNetworkName(v)
		}
	}
	if bi.BoundaryPath == "" {
		return "default"
	}
	return sanitizeNetworkName(bi.BoundaryPath)
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
	return fmt.Errorf("TRANSLATION_ERROR: compose Release merge: service-name collisions across boundaries: %s", strings.Join(parts, "; "))
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
func applyDependsOn(merged *ComposeFile, edges []types.IREdge, boundaryByService map[string]string) {
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
