// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package cfncore

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	frp "github.com/aggrega-ai/aggrega-cli/internal/compiler/featurerecipe_pipeline"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/rendercore"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
)

// Handle field names the producer's link_publish exposes for the consumer's
// link_resolve. stackName carries the producer stack a cross-account read
// targets; an exportName entry per output key carries the cross-stack export a
// same-account read imports. All are deterministic pointer strings the compiler
// pipes and never resolves against AWS state.
const (
	handleStackNameKey     = "stackName"
	handleExportNamePrefix = "exportName."
)

// ErrLinkConsumerStackMissing is the loud failure a cross-boundary link resolve
// raises when the consuming endpoint's assembled files carry no CloudFormation
// stack template to inject the reference into. Without the consumer's stack the
// resolve cannot locate the placeholder it must replace.
func ErrLinkConsumerStackMissing() error { return errLinkConsumerStackMissing }

var errLinkConsumerStackMissing = cerrors.Register("cloudformation-provider-cfncore/link-consumer-stack-missing", cerrors.Meta{
	ShortCode: "E0231",
	Message:   "cloudformation-provider: cross-boundary link resolve received no consumer stack template",
})

// ErrLinkSentinelInterpolated is the loud failure a cross-boundary link resolve
// raises when the placeholder for an injected value survives only inside a
// larger string rather than as a whole scalar. A CloudFormation cross-reference
// is an intrinsic object, which can only replace a whole value; a placeholder
// spliced into a longer string cannot be swapped for an intrinsic. The build
// halts rather than emit a reference in the wrong shape.
func ErrLinkSentinelInterpolated() error { return errLinkSentinelInterpolated }

var errLinkSentinelInterpolated = cerrors.Register("cloudformation-provider-cfncore/link-sentinel-interpolated", cerrors.Meta{
	ShortCode: "E0232",
	Message:   "cloudformation-provider: cross-boundary link value is interpolated into a larger string; only a whole-scalar injection can carry a cross-stack reference",
})

// ErrLinkExportNameShadowed is the loud failure a cross-boundary link publish
// raises when an export name it derives for an injected output is already
// declared by another Outputs entry in the same assembled stack. An Outputs
// entry's Export name is carried through as authored, so it can spell a name the
// derivation also produces for this build — leaving the stack declaring one
// export name twice, from two places, only one of which the author wrote.
//
// Only the coincidence halts, never the authoring: declaring an export name on
// an Outputs entry is supported, and one that no derived name matches publishes
// unremarked.
//
// The diagnostic leans on the export name, because that is the one string both
// sides spell identically — the author typed it and the derivation reproduced it,
// which is what makes the collision a collision — so it doubles as the term an
// author can search their own files for. The stack Output the declaration sits
// under is named as this core's own, since the authored entry it was built from
// cannot be read back out of it ([authoredExport]).
func ErrLinkExportNameShadowed() error { return errLinkExportNameShadowed }

var errLinkExportNameShadowed = cerrors.Register("cloudformation-provider-cfncore/link-export-name-shadowed", cerrors.Meta{
	ShortCode: "E0249",
	Message:   "cloudformation-provider: an export name a cross-boundary link derives is already declared by another Outputs entry in the same stack",
})

// LinkPublish produces the producer-side wiring for one cross-boundary link on
// the consumed (target) endpoint's side: for each output key the consuming edge
// injects, it adds an Export to that key's stack Output and records the export
// name — plus the producer stack name — in the published handle the consumer
// resolves against. The Output itself already exists from the provides-to-Outputs
// projection at assembly; the publish adds only the Export block that makes the
// Output importable across stacks. It is addressed by the same node-and-entry
// name the projection gave it ([OutputLogicalID]), which the consumed node's id
// and the output key together determine — so the publish reaches the right
// producer's output even where another node in the same stack exports the same
// key.
//
// The export and stack names carry the consumed endpoint's deployment identity
// as their leading scope segment, so two distinct deployments sharing an account
// and region — each with the same boundary path and component — produce distinct
// export and stack names. Every name is a deterministic pointer string the
// compiler pipes and never resolves against AWS state.
//
// The consumed endpoint's assembled files are read to check each derived export
// name against the export names the stack's other Outputs entries already
// declare, and a match halts ([ErrLinkExportNameShadowed]). The check is scoped
// to this build: what a name meets in an account and region it is deployed into
// is a property of the deployment, which nothing here can observe. Files
// carrying no stack template yield no comparison, which is the answer whenever
// the host has no assembled producer output to hand the publish.
func LinkPublish(edge rendercore.LinkEdge, producerFiles []rendercore.RenderedFile) (rendercore.LinkPublishOutput, error) {
	keys := injectedOutputKeys(edge)
	var authored []authoredExport
	if stack, ok := findStackFile(producerFiles); ok {
		authored = authoredExportNames(stack)
	}
	handle := map[string]string{
		handleStackNameKey: StackName(edge.Target.Deployment, edge.Target.Boundary),
	}
	ops := make([]frp.JSONPatchOp, 0, len(keys))
	var shadowed []string
	for _, outputKey := range keys {
		exportName := ExportName(edge.Target.Deployment, edge.Target.Boundary, edge.Target.Node, outputKey)
		ownOutputName := OutputLogicalID(edge.Target.Node, outputKey)
		for _, a := range authored {
			// The entry this publish writes to is skipped rather than compared:
			// it holds either the provides-to-Outputs projection this Export
			// attaches to, or an Export this same publish already wrote for
			// another edge sharing the consumed endpoint — the host applies each
			// publish's wiring to the group before the next edge is published,
			// so the second edge reads the first one's Export back. Neither is a
			// name declared independently of this derivation, and comparing
			// against them would halt on edge order.
			if a.outputName == ownOutputName || a.name != exportName {
				continue
			}
			shadowed = append(shadowed, fmt.Sprintf(
				"the link derives the export name %q for provides key %q of component %q in boundary %q of deployment %q, and another Outputs entry of the same stack already declares that same export name — that declaration sits under the stack output %q, a name this provider assembles rather than one a Recipe wrote",
				exportName, outputKey, edge.Target.Node, edge.Target.Boundary, edge.Target.Deployment, a.outputName))
		}
		handle[handleExportNamePrefix+outputKey] = exportName
		ops = append(ops, frp.JSONPatchOp{
			Scope: frp.DocumentScopeOwn,
			Key:   StackTemplateName,
			Op:    "add",
			Path:  "/Outputs/" + rfc6901Escape(ownOutputName) + "/Export",
			Value: map[string]any{"Name": exportName},
		})
	}
	if len(shadowed) > 0 {
		return rendercore.LinkPublishOutput{}, fmt.Errorf("%w: %s; each shadowing Outputs entry spells its export name exactly as the halt does, so searching your Recipes for that name reaches the entry — rename the export name the entry declares, or remove that entry's Export block and let the link publish the export",
			ErrLinkExportNameShadowed(), strings.Join(shadowed, "; "))
	}
	out := rendercore.LinkPublishOutput{Handle: handle}
	if len(ops) > 0 {
		out.Patches = []rendercore.LinkPatch{{File: StackTemplateName, Ops: ops}}
	}
	return out, nil
}

// LinkResolve produces the consumer-side wiring for one cross-boundary link on
// the consuming (source) endpoint's side: for each output key the edge injects,
// it locates the deterministic placeholder the host stamped into the consuming
// stack and replaces it with a CloudFormation cross-stack reference. Both ends'
// placement annotations select the reference shape: only when both ends declare
// a placement (account or region) and those placements differ is the value read
// with Fn::GetStackOutput against the producer stack, output, region, and
// assume-role — the producer (target) side supplies the region and assume-role,
// and the output is named as the producer stack named it ([OutputLogicalID]);
// otherwise the value is imported with Fn::ImportValue against the published
// export name. A placement declared on only one end, or matching placements on
// both, resolves to the same-placement import. Every reference field is a
// deterministic pointer string the compiler pipes and the runtime resolves.
//
// A placeholder that survives only inside a larger string halts loudly: a
// cross-reference is an intrinsic object and can replace only a whole scalar. A
// placeholder absent from the consumer stack contributes no operation — the
// consumer did not render that injected value, so there is nothing to wire.
func LinkResolve(edge rendercore.LinkEdge, handle map[string]string, consumerFiles []rendercore.RenderedFile) (rendercore.LinkResolveOutput, error) {
	stack, ok := findStackFile(consumerFiles)
	if !ok {
		return rendercore.LinkResolveOutput{}, ErrLinkConsumerStackMissing()
	}
	var doc any
	if err := json.Unmarshal(stack, &doc); err != nil {
		return rendercore.LinkResolveOutput{}, fmt.Errorf("cloudformation-provider: parse consumer stack template: %w", err)
	}

	targetAnn := ReadCloudTargetAnnotationsFromMap(edge.TargetAnnotations)
	sourceAnn := ReadCloudTargetAnnotationsFromMap(edge.SourceAnnotations)
	targetDeclared := targetAnn.AWSAccount != "" || targetAnn.AWSRegion != ""
	sourceDeclared := sourceAnn.AWSAccount != "" || sourceAnn.AWSRegion != ""
	differ := targetAnn.AWSAccount != sourceAnn.AWSAccount || targetAnn.AWSRegion != sourceAnn.AWSRegion
	crossAccountOrRegion := targetDeclared && sourceDeclared && differ

	ops := make([]frp.JSONPatchOp, 0)
	for _, outputKey := range injectedOutputKeys(edge) {
		sentinel := rendercore.CrossBoundaryLinkSentinel(edge.Source.Node, edge.Target.Node, edge.Socket, outputKey)
		path, whole, interpolated := findSentinelPath(doc, sentinel)
		if !whole {
			if interpolated {
				return rendercore.LinkResolveOutput{}, fmt.Errorf("%w: output key %q on socket %q", ErrLinkSentinelInterpolated(), outputKey, edge.Socket)
			}
			// The consumer did not render this injected value; nothing to wire.
			continue
		}
		var value any
		if crossAccountOrRegion {
			value = map[string]any{"Fn::GetStackOutput": map[string]any{
				"StackName":  handle[handleStackNameKey],
				"OutputName": OutputLogicalID(edge.Target.Node, outputKey),
				"Region":     targetAnn.AWSRegion,
				"RoleArn":    targetAnn.AWSAssumeRoleARN,
			}}
		} else {
			value = map[string]any{"Fn::ImportValue": handle[handleExportNamePrefix+outputKey]}
		}
		ops = append(ops, frp.JSONPatchOp{
			Scope: frp.DocumentScopeOwn,
			Key:   StackTemplateName,
			Op:    "replace",
			Path:  path,
			Value: value,
		})
	}
	out := rendercore.LinkResolveOutput{}
	if len(ops) > 0 {
		out.Patches = []rendercore.LinkPatch{{File: StackTemplateName, Ops: ops}}
	}
	return out, nil
}

// injectedOutputKeys returns the sorted, de-duplicated set of consumed output
// keys the edge injects across its env, file, and arg mechanisms — the keys the
// producer must export and the consumer must resolve. Sorting makes the publish
// operations and handle entries byte-identical across runs of identical input.
func injectedOutputKeys(edge rendercore.LinkEdge) []string {
	seen := map[string]struct{}{}
	for _, mech := range []string{"env", "file", "arg"} {
		keymap, ok := edge.Inject.Get(mech)
		if !ok {
			continue
		}
		for _, outputKey := range keymap.All() {
			seen[outputKey] = struct{}{}
		}
	}
	keys := make([]string, 0, len(seen))
	for k := range seen {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// findStackFile returns the assembled CloudFormation stack template's bytes from
// a consuming endpoint's files, and whether it was present.
func findStackFile(files []rendercore.RenderedFile) ([]byte, bool) {
	for _, f := range files {
		if f.Path == StackTemplateName {
			return f.Content, true
		}
	}
	return nil, false
}

// authoredExport is one export name an assembled stack declares, paired with the
// name of the stack Output carrying it.
//
// Only the export name is authored. The output name is this core's own, built at
// assembly from the component and the entry an author wrote ([OutputLogicalID]),
// and that build is not injective — it drops every separator and capitalises the
// following letter, so entries as different as db_endpoint, db-endpoint and
// dbEndpoint all reach one output name. The authored spelling therefore cannot be
// read back out of it, which is why a diagnostic built from this pair leans on
// the export name and never presents the output name as something an author
// wrote.
type authoredExport struct {
	outputName string
	name       string
}

// authoredExportNames returns the export names an assembled stack's Outputs
// entries declare, ordered by the stack's own Output names so a diagnostic built
// from them reads the same across runs of identical input.
//
// Every reading that is not an export name spelled as a string yields no entry
// rather than an error: a stack with no Outputs section declares none, an Output
// with no Export block declares none, and an Export whose Name is an intrinsic
// resolves at deploy time rather than here, so this core cannot know what it
// spells and does not guess. A template that does not parse likewise yields
// none — the publish needs no document to author its own wiring, and a stack
// this core cannot read is reported where it is read to be written, when the
// publish's wiring is applied to it.
func authoredExportNames(stack []byte) []authoredExport {
	var doc any
	if err := json.Unmarshal(stack, &doc); err != nil {
		return nil
	}
	root, ok := doc.(map[string]any)
	if !ok {
		return nil
	}
	outputs, ok := root["Outputs"].(map[string]any)
	if !ok {
		return nil
	}
	found := make([]authoredExport, 0, len(outputs))
	for _, outputName := range sortedKeys(outputs) {
		body, ok := outputs[outputName].(map[string]any)
		if !ok {
			continue
		}
		export, ok := body["Export"].(map[string]any)
		if !ok {
			continue
		}
		if name, ok := export["Name"].(string); ok {
			found = append(found, authoredExport{outputName: outputName, name: name})
		}
	}
	return found
}

// findSentinelPath walks a decoded JSON document for the placeholder token and
// returns the RFC 6901 pointer to the first whole-scalar occurrence (map keys
// walked in sorted order so the choice is deterministic), whether such a
// whole-scalar occurrence exists, and whether the token appears only inside a
// larger string. A whole-scalar occurrence is the replaceable target; an
// interpolated-only occurrence cannot carry an intrinsic and the caller halts.
func findSentinelPath(doc any, sentinel string) (path string, whole bool, interpolated bool) {
	var walk func(node any, ptr string)
	found := false
	sawInterpolated := false
	foundPath := ""
	walk = func(node any, ptr string) {
		if found {
			return
		}
		switch v := node.(type) {
		case string:
			if v == sentinel {
				found = true
				foundPath = ptr
				return
			}
			if strings.Contains(v, sentinel) {
				sawInterpolated = true
			}
		case map[string]any:
			keys := make([]string, 0, len(v))
			for k := range v {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				walk(v[k], ptr+"/"+rfc6901Escape(k))
				if found {
					return
				}
			}
		case []any:
			for i, item := range v {
				walk(item, ptr+"/"+strconv.Itoa(i))
				if found {
					return
				}
			}
		}
	}
	walk(doc, "")
	return foundPath, found, sawInterpolated
}

// rfc6901Escape escapes a JSON Pointer reference token per RFC 6901: '~' becomes
// "~0" and '/' becomes "~1", so a key carrying either character addresses the
// intended location.
func rfc6901Escape(token string) string {
	token = strings.ReplaceAll(token, "~", "~0")
	token = strings.ReplaceAll(token, "/", "~1")
	return token
}
