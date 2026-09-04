// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Filesystem-free per-(path, provider) merge core for the compose provider.
// Reads per-node fragments (Intermediate=true EmittedFiles produced by
// SynthesizeWorkloadCore and by sourced-mode rendering), deep-merges into a
// single docker-compose.yaml at the dispatch path.
//
// AssembleBoundaryCore takes exactly the inputs the merge consumes — the per-node
// fragments, the per-node annotations, the per-node deployment wave, the IR edges,
// the boundary-path fallback, and the yaml indent — rather than a host-shaped
// carrier the caller would have to build, so the compose provider plugin reaches
// the merge directly. This file carries no build tag: it compiles for the host
// and for the wasip1 guest alike, so it imports no host-only capability (no
// filesystem, no network).
//
// Merge contract.
//
//   - Per-boundary network. For each contributing node, a docker network is
//     declared whose name is derived from the `aggrega.ai/namespace` annotation
//     on the node, or from the boundary path when no namespace annotation is
//     present. The derivation writes out every character a compose network name
//     does not accept rather than substituting for it, so two identities reach
//     one network only where they are the same identity. Every service joins its
//     author-boundary's network.
//   - Service collisions. If two different nodes contribute services of
//     the same name, AssembleBoundaryCore returns TRANSLATION_ERROR naming
//     every contributing (boundary, nodeID, serviceName) tuple. A service
//     is refused on the name alone, which is where it differs from the
//     body-carrying sections below: a compose service is a runtime
//     singleton, so two nodes each wanting a container of their own reach
//     one container even where their two definitions agree, and the loss is
//     real however identical the bodies are.
//   - Top-level volumes. Per-node volume entries union across the dispatch
//     group. Two nodes may declare one name, because a named volume is one
//     physical volume and two identical declarations both ask for exactly
//     that — so identical bodies merge into the single entry they describe.
//     Bodies that differ are a disagreement about that one volume, and
//     AssembleBoundaryCore returns TRANSLATION_ERROR naming the volume and
//     every contributing (boundary, nodeID) tuple rather than keeping one
//     body and discarding the other.
//   - Top-level configs. Per-node config declarations union the same way and
//     under the same conflict rule. Every service-level configs: reference
//     must resolve to one of them once the union is complete, or
//     AssembleBoundaryCore returns TRANSLATION_ERROR naming each unresolved
//     reference — the union is what makes the check honest, since one node
//     may reference a config a different node in the dispatch group
//     declares.
//   - depends_on inference. For each IR edge whose source and target both
//     contribute services to this dispatch group, record an ungated
//     dependency on the target service on the source service. It joins
//     whatever the fragment already carried — a synthesized init-container
//     chain — through DependsOn, which owns the merge rule and the emitted
//     shape.
//   - depends_on from the deployment wave. When the contributing nodes carry
//     more than one distinct wave, each service waits on every service of the
//     immediately-preceding populated wave, gated on that service's health
//     where it declares a healthcheck and on its start where it does not.
//     Compose resolves the remaining ordering transitively, so a three-wave
//     group chains rather than fanning out to every earlier wave. A group whose
//     nodes all carry one wave records nothing, so a dispatch group where no
//     boundary declared a wave emits byte-identical output.
//   - Author order. Map-shaped top-level fields (services, volumes,
//     networks) are alphabetised by yaml.v3 on marshal — the
//     alphabetical-by-derivation-key principle for synthesized
//     collections where no author order applies upstream. Within a
//     service, per-service collections (ports, depends_on, volumes,
//     networks) are kept in stable order: edges and IR nodes iterate
//     alphabetically; ports retain the order their fragment carried.

package composecore

import (
	"fmt"
	"sort"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/rendercore"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderctx"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/namesegment"
)

// AssembledOutputFile is the assembled compose file path emitted by
// AssembleBoundaryCore at the dispatch path.
const AssembledOutputFile = "docker-compose.yaml"

// sectionContrib records one (boundary, nodeID) tuple that contributed an entry
// to a section of the merged compose output. Used by collision detection to
// name every contributor in TRANSLATION_ERROR output. It carries no body:
// whether a shared name is a collision at all is the contributing section's own
// question, and only the answer reaches the reporting side.
type sectionContrib struct {
	boundary string
	nodeID   string
}

// bodyContrib records one contributor's declaration of a named entry in a
// section whose entries carry a body — the declaring party, and the bytes that
// entry would be emitted as.
//
// The emitted form rather than the parsed struct is what a conflict is decided
// on, and the reason is that it is the property actually at stake: two bodies
// emitting the same bytes have lost nothing between them, whatever their
// in-memory shapes. Comparing the structs instead would report a conflict
// between a contributor that wrote an empty `driver_opts:` mapping and one that
// wrote no `driver_opts:` at all, because a nil map and an empty map are
// distinct values — while `omitempty` emits neither, so the two declarations are
// the same declaration as far as the assembled document is concerned. It also
// keeps the comparison honest as the shape grows: a field added to Volume or
// Config is compared because it is emitted, with nothing here to update.
type bodyContrib struct {
	party   sectionContrib
	emitted string
}

// collisionClass carries the words one section's collision halt is worded with:
// the headline naming what went wrong, the noun each colliding entry is named
// by, and the remedy the author is pointed at (empty where the class admits
// none).
type collisionClass struct {
	headline  string
	entryNoun string
	remedy    string
}

// AssembleBoundaryCore reads per-node fragments and emits a single
// docker-compose.yaml at the dispatch path. intermediatesByNode is keyed by
// nodeID; nodeAnnotations gives each node's merged annotation map (read for the
// `aggrega.ai/namespace` network discriminator); nodeSyncWave gives each node's
// deployment wave, keyed the same way, for wave-derived ordering (a node with no
// entry carries the field's documented default of 0); edges is the IR's directed
// `uses` edges for depends_on inference; boundaryPath is the identity the
// network name falls back to when a node carries no namespace annotation;
// yamlIndent is the indentation width for the assembled artifact.
//
// It returns the assembled files, the advisories the merge raised, and an error.
// The advisories are out-of-band: every one of them leaves the emitted bytes
// exactly as they would have been without it.
func AssembleBoundaryCore(
	intermediatesByNode map[string][]rendercore.EmittedFile,
	nodeAnnotations map[string]map[string]string,
	nodeSyncWave map[string]int,
	edges []rendercore.IREdgeInfo,
	boundaryPath string,
	yamlIndent int,
) (map[string][]byte, []renderctx.Diagnostic, error) {
	merged := ComposeFile{
		Services: map[string]Service{},
		Volumes:  map[string]Volume{},
		Networks: map[string]Network{},
		Configs:  map[string]Config{},
	}

	// Track which (boundary, nodeID) contributed each service for collision
	// reporting. boundaryByService allows depends_on inference to look up a
	// service's owning boundary without re-walking the input.
	contribsByService := map[string][]sectionContrib{}
	boundaryByService := map[string]string{}

	// Track every contributor of each top-level volume and config name, with
	// the bytes it declared, so a name two contributors disagree about is
	// refused rather than resolved by whichever of them the walk reaches last.
	contribsByVolume := map[string][]bodyContrib{}
	contribsByConfig := map[string][]bodyContrib{}

	// waveByService records the deployment wave of the node that contributed
	// each service, so wave-derived ordering reads a service's wave without
	// re-walking the input. A node contributes every service it synthesized —
	// its own and each init-container service — so all of them share its wave.
	waveByService := map[string]int{}

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
			return nil, nil, err
		}
		if frag == nil {
			continue
		}

		networkName := networkNameByNode[nid]
		merged.Networks[networkName] = Network{}

		party := sectionContrib{boundary: networkName, nodeID: nid}

		// Volumes: union, recording each contributor's emitted body so a
		// name two of them disagree about is refused after the walk. The
		// assignment stays last-writer because it is only reached for names
		// every contributor emitted identically, where either body is the
		// one entry both of them described.
		for k, v := range frag.Volumes {
			emitted, err := marshalDeterministic(v, yamlIndent)
			if err != nil {
				return nil, nil, cerrors.Wrap(err, cerrors.TranslationError, cerrors.SourceLocation{File: nid}, fmt.Sprintf("compose: node %q volume %q: marshal for collision comparison", nid, k))
			}
			contribsByVolume[k] = append(contribsByVolume[k], bodyContrib{party: party, emitted: string(emitted)})
			merged.Volumes[k] = v
		}

		// Configs: union (top-level config declarations, e.g. the
		// compose-bridge gateway's generated nginx config), under the same
		// conflict rule and for the same reason.
		for k, v := range frag.Configs {
			emitted, err := marshalDeterministic(v, yamlIndent)
			if err != nil {
				return nil, nil, cerrors.Wrap(err, cerrors.TranslationError, cerrors.SourceLocation{File: nid}, fmt.Sprintf("compose: node %q config %q: marshal for collision comparison", nid, k))
			}
			contribsByConfig[k] = append(contribsByConfig[k], bodyContrib{party: party, emitted: string(emitted)})
			merged.Configs[k] = v
		}

		// Services: collision-detect, then merge with the network attached.
		for svcName, svc := range frag.Services {
			contribsByService[svcName] = append(contribsByService[svcName], party)
			boundaryByService[svcName] = networkName
			waveByService[svcName] = nodeSyncWave[nid]

			// Append the network to the service's networks list. A service
			// that already carries a network_mode keeps it instead, because
			// compose forbids combining networks: with network_mode: and the
			// synthesizer never sets both. Two shapes reach here that way: a
			// sidecar sharing the main service's namespace, and a workload
			// declaring it runs on the node's network.
			if svc.NetworkMode == "" {
				svc.Networks = appendUnique(svc.Networks, networkName)
			}
			merged.Services[svcName] = svc
		}
	}

	// Collision checks, in the order the sections are declared in the emitted
	// document so the reported halt is the same one whatever the input order.
	if err := checkServiceCollisions(contribsByService); err != nil {
		return nil, nil, err
	}
	if err := checkBodyCollisions("volume", contribsByVolume); err != nil {
		return nil, nil, err
	}
	if err := checkBodyCollisions("config", contribsByConfig); err != nil {
		return nil, nil, err
	}

	// Every service-level configs: reference resolves to a declared top-level
	// configs: entry, or the build halts.
	if err := checkConfigReferences(merged); err != nil {
		return nil, nil, err
	}

	// depends_on inference: walk IR edges, project onto contributing
	// services. Edge endpoints not in the dispatch group are skipped.
	applyDependsOn(&merged, edges, boundaryByService)

	// depends_on from the deployment wave, recorded after the edge-derived
	// entries so the two layers keep their own order (DependsOn emits in the
	// order dependencies are recorded).
	diags := applyWaveOrdering(&merged, waveByService)

	body, err := marshalDeterministic(&merged, yamlIndent)
	if err != nil {
		return nil, diags, cerrors.Wrap(err, cerrors.TranslationError, cerrors.SourceLocation{}, "compose: assemble-boundary marshal")
	}

	return map[string][]byte{AssembledOutputFile: body}, diags, nil
}

// applyWaveOrdering records the ordering a declared deployment wave expresses, as
// `depends_on` between services of the merged file, and returns one advisory per
// service whose ordering could only be expressed in a weaker form.
//
// Reach. Compose `depends_on` is service-to-service inside one file, and one
// dispatch path produces one file, so this orders every service in the merged
// output and nothing outside it. Contributors that merge into a different file
// are unreachable from here and are not approximated.
//
// Fan-out. Services are grouped by their contributing node's wave. Each service
// waits on every service of the immediately-preceding populated wave, never on
// every earlier wave: compose resolves the rest transitively, so a three-wave
// group chains through the middle wave and the emitted lists stay proportional
// to one wave rather than to the whole group.
//
// Condition. A wave means the earlier group is up before the later one starts,
// which is readiness rather than process launch, so the wait is gated on the
// target's health where the target declares a `healthcheck`. A service without
// one can never report healthy, so gating on health there would emit a wait
// nothing can satisfy and the merged file would never come up; those waits are
// gated on the target's start instead, and each such target is named in a
// returned advisory so the author learns which part of the wave they declared is
// honoured only as start ordering. Nothing is refused and nothing is rewritten:
// the weaker condition is visible in the emitted file beside the stronger one.
//
// Order. Targets are sorted alphabetically before they are recorded, for the same
// reason applyDependsOn sorts its own: a set of same-wave services carries no
// author order, so alphabetical is what makes the output stable. Recording runs
// after the edge-derived pass, so the emitted layers are the fragment's own
// init-container chain, then the edge-derived entries, then these. Where a wave
// names a target the service already depends on through an edge, DependsOn keeps
// the single entry it already has and supplies the condition to it, so the wave's
// requirement is expressed without a duplicate entry.
//
// A group whose services all carry one wave records nothing at all, so output for
// a dispatch group where no boundary declared a wave is unchanged.
func applyWaveOrdering(merged *ComposeFile, waveByService map[string]int) []renderctx.Diagnostic {
	servicesByWave := map[int][]string{}
	for svcName := range waveByService {
		if _, contributes := merged.Services[svcName]; !contributes {
			continue
		}
		servicesByWave[waveByService[svcName]] = append(servicesByWave[waveByService[svcName]], svcName)
	}
	if len(servicesByWave) < 2 {
		return nil
	}

	waves := make([]int, 0, len(servicesByWave))
	for w := range servicesByWave {
		waves = append(waves, w)
	}
	sort.Ints(waves)
	for _, w := range waves {
		sort.Strings(servicesByWave[w])
	}

	// Targets whose ordering degraded to start-only, collected in the order the
	// waves and then the service names are walked, so the advisory list is
	// deterministic. A target reached from more than one later service is named
	// once: the fact is about the target, not about each dependent.
	var startOnly []string
	seenStartOnly := map[string]struct{}{}

	for i := 1; i < len(waves); i++ {
		predecessors := servicesByWave[waves[i-1]]
		for _, svcName := range servicesByWave[waves[i]] {
			svc := merged.Services[svcName]
			for _, target := range predecessors {
				condition := conditionStarted
				if merged.Services[target].Healthcheck != nil {
					condition = conditionHealthy
				} else if _, seen := seenStartOnly[target]; !seen {
					seenStartOnly[target] = struct{}{}
					startOnly = append(startOnly, target)
				}
				svc.DependsOn.Require(target, condition)
			}
			merged.Services[svcName] = svc
		}
	}

	if len(startOnly) == 0 {
		return nil
	}
	diags := make([]renderctx.Diagnostic, 0, len(startOnly))
	for _, target := range startOnly {
		diags = append(diags, rendercore.PartialSupportDiagnostic(composeTargetName, "syncWave", fmt.Sprintf(
			"service %q declares no healthcheck, so services in later waves wait for it to start rather than for it to report healthy",
			target)))
	}
	return diags
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

// networkNameEncoding is the segment encoding a compose network name is built
// with.
//
// A compose network is shared L3 connectivity, and the top-level networks:
// block unions its entries by name — so two identities arriving at one name
// become one network both sets of services sit on, with nothing in the emitted
// file to show that two were meant. The derivation therefore encodes the
// identity rather than substituting for the characters compose does not accept,
// which is what keeps identities that differ at all on separate networks.
//
// The alphabet is stated here rather than taken from the encoding, because it
// is a property of what compose accepts in a network name rather than of the
// encoding: a compose network name carries letters, digits, dots, underscores
// and hyphens. The hyphen is the encoding's own — it opens every escape — so an
// alphabet may not admit it; everything else compose accepts is carried
// through, which is what keeps an ordinary name readable.
var networkNameEncoding = namesegment.NewEncoding('-', isNetworkNameRune)

// isNetworkNameRune reports whether a character is carried through the encoding
// as itself: an ASCII letter, a digit, a dot or an underscore. Every other
// character is escaped.
func isNetworkNameRune(r rune) bool {
	return r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' ||
		r >= '0' && r <= '9' || r == '.' || r == '_'
}

// deriveNodeNetworkName resolves the compose network a node lands on, from its
// `aggrega.ai/namespace` annotation value (empty when absent) and the boundary
// path a node with no such annotation falls back to.
//
// The derivation is injective over each branch: two identities produce one
// network name only where they are the same identity. Every character compose
// does not accept in a network name is written out rather than substituted for,
// so identities differing only in case or only in punctuation stay on separate
// networks — where a fold onto a single admitted character would have merged
// them, silently, into shared connectivity nobody declared. An absent boundary
// path takes no literal of its own: it yields the encoding's absent-segment
// mark, which carries a selector standing for no character and so cannot be
// produced by any identity an author wrote.
//
// One class this deliberately does not close: a node whose namespace is X and a
// node with no namespace under a boundary path spelled X still reach one
// network, because both branches encode into the same space. That is a property
// of which identity is selected rather than of how it is written out.
//
// Both the per-node network-assignment loop and the compose-bridge
// cross-network opt-in filter call THIS function — the network identity is the
// resolved name, never the raw namespace string — so the bridge's "is this edge
// cross-network?" decision can never drift from where the nodes are actually
// assigned. Splitting two identities that used to fold together therefore moves
// an edge between them from same-network to cross-network.
func deriveNodeNetworkName(namespace, boundaryPath string) string {
	if namespace != "" {
		return networkNameEncoding.Segment(namespace)
	}
	return networkNameEncoding.Segment(boundaryPath)
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

// reportNameCollisions is the one home for the shape of a collision halt: how a
// contributing party is named, how entries and parties are ordered, how the
// message is built, and which severity it carries. Every collision-checked
// section of the merged document reports through here, so two of them cannot
// word the same kind of halt differently.
//
// What it deliberately does not own is which shared names count as collisions.
// That predicate is each section's own and they genuinely differ — see
// checkServiceCollisions and checkBodyCollisions — so unifying it would either
// refuse a volume two contributors agree about or accept a service two
// contributors both meant to own.
//
// Entries are reported in name order and each entry's parties in descriptor
// order, so the message is a function of the collisions alone and not of the
// order the walk happened to find them in.
func reportNameCollisions(class collisionClass, colliding map[string][]sectionContrib) error {
	if len(colliding) == 0 {
		return nil
	}
	names := make([]string, 0, len(colliding))
	for name := range colliding {
		names = append(names, name)
	}
	sort.Strings(names)

	parts := make([]string, 0, len(names))
	for _, name := range names {
		descriptors := make([]string, 0, len(colliding[name]))
		for _, party := range colliding[name] {
			descriptors = append(descriptors, fmt.Sprintf("boundary=%q node=%q", party.boundary, party.nodeID))
		}
		sort.Strings(descriptors)
		parts = append(parts, fmt.Sprintf("%s %q contributed by [%s]", class.entryNoun, name, strings.Join(descriptors, ", ")))
	}

	message := fmt.Sprintf("compose AssembleBoundary: %s: %s", class.headline, strings.Join(parts, "; "))
	if class.remedy != "" {
		message += "; " + class.remedy
	}
	return cerrors.New(cerrors.TranslationError, cerrors.SourceLocation{}, message, nil, nil)
}

// checkServiceCollisions walks contribsByService and returns
// TRANSLATION_ERROR when any service is contributed by more than one
// (boundary, node) tuple.
//
// The predicate is the shared name alone, with no comparison of what the two
// contributors declared, and that is deliberate rather than the weaker form of
// the body-carrying sections' check. A compose service is a runtime singleton:
// two nodes that each synthesized a service `web` each wanted a container, and
// they get one between them, so a contributor's intent is lost even where the
// two definitions are byte-identical. Nothing here is recoverable by comparing
// bodies, which is why this predicate cannot be folded into the one below.
func checkServiceCollisions(contribsByService map[string][]sectionContrib) error {
	colliding := map[string][]sectionContrib{}
	for svc, contribs := range contribsByService {
		if len(contribs) <= 1 {
			continue
		}
		colliding[svc] = contribs
	}
	return reportNameCollisions(collisionClass{
		headline:  "service-name collisions across contributors",
		entryNoun: "service",
	}, colliding)
}

// checkBodyCollisions walks one body-carrying section's contributors and returns
// TRANSLATION_ERROR for every name whose contributors did not all declare the
// same thing. entryNoun is the singular the section's entries are named by in
// the halt ("volume", "config").
//
// The predicate is a disagreement rather than a shared name, because a shared
// name is legitimate here in a way it is not for a service: a named volume or
// config is one object, so two contributors declaring it identically have both
// asked for exactly the object the merged document ends up with and nothing is
// lost. Two contributors declaring it differently is a disagreement about that
// one object, and resolving it by keeping either body discards the other
// contributor's declaration from a build that would otherwise succeed.
//
// Bodies are compared as the bytes they would be emitted as — see bodyContrib
// for why that, rather than the parsed values.
func checkBodyCollisions(entryNoun string, contribsByName map[string][]bodyContrib) error {
	colliding := map[string][]sectionContrib{}
	for name, contribs := range contribsByName {
		if !bodiesDisagree(contribs) {
			continue
		}
		parties := make([]sectionContrib, 0, len(contribs))
		for _, c := range contribs {
			parties = append(parties, c.party)
		}
		colliding[name] = parties
	}
	return reportNameCollisions(collisionClass{
		headline:  fmt.Sprintf("conflicting %s declarations across contributors", entryNoun),
		entryNoun: entryNoun,
		remedy:    fmt.Sprintf("two contributors declaring one %s name must declare it identically, or one of them must declare a different name", entryNoun),
	}, colliding)
}

// bodiesDisagree reports whether the contributors of one name emitted anything
// other than the same bytes. A single contributor never disagrees, and neither
// does any number of contributors that all emitted the same body.
func bodiesDisagree(contribs []bodyContrib) bool {
	if len(contribs) <= 1 {
		return false
	}
	for _, c := range contribs[1:] {
		if c.emitted != contribs[0].emitted {
			return true
		}
	}
	return false
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
