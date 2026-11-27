// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package cfnimport

import (
	"bytes"
	"encoding/json"
	"slices"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/plugins/internal/cfncore/cfntemplate"
	"github.com/aggrega-ai/aggrega-cli/plugins/internal/cloudimport"
	"github.com/aggrega-ai/aggrega-cli/plugins/internal/draftyaml"
	"github.com/aggrega-ai/aggrega-cli/plugins/internal/schemadraft"
	"github.com/aggrega-ai/aggrega-pdk/go/pkg/importer"
)

const (
	// serverlessTransform is the transform of the AWS Serverless Application
	// Model, SAM: when the stack deploys, it merges the template's Globals
	// into its serverless resources and expands each into the resources it
	// stands for.
	serverlessTransform = "AWS::Serverless-2016-10-31"
	// serverlessPrefix opens the name of each type the serverless transform
	// expands.
	serverlessPrefix = "AWS::Serverless::"
	// functionType, stateMachineType and connectorType are the serverless
	// types of a function, a state machine and a connector.
	functionType     = "AWS::Serverless::Function"
	stateMachineType = "AWS::Serverless::StateMachine"
	connectorType    = "AWS::Serverless::Connector"
	// globalsSection is the section of a template the serverless transform
	// reads the Globals from.
	globalsSection = "Globals"
)

// globalsProperties are the properties the serverless transform reads from
// the Globals for each resource type, keyed as the Globals name it, the type
// serverlessPrefix followed by the key. No other key and no other property is
// read: the transform refuses them.
var globalsProperties = []struct {
	key   string
	props []string
}{
	{"Api", []string{"AccessLogSetting", "Auth", "BinaryMediaTypes", "CacheClusterEnabled", "CacheClusterSize",
		"CanarySetting", "Cors", "DefinitionUri", "Domain", "EndpointConfiguration", "EndpointAccessMode",
		"GatewayResponses", "MethodSettings", "MinimumCompressionSize", "Name", "OpenApiVersion", "PropagateTags",
		"SecurityPolicy", "TracingEnabled", "Variables"}},
	{"CapacityProvider", []string{"InstanceRequirements", "KmsKeyArn", "LoggingConfig", "ManagedResourceTags",
		"OperatorRole", "PropagateTags", "ScalingConfig", "Tags", "VpcConfig"}},
	{"Function", []string{"AcceleratorConfig", "Architectures", "AssumeRolePolicyDocument", "AutoPublishAlias",
		"AutoPublishAliasAllProperties", "CapacityProviderConfig", "CodeSigningConfigArn", "CodeUri",
		"DeadLetterQueue", "DeploymentPreference", "Description", "DurableConfig", "Environment", "EphemeralStorage",
		"EventInvokeConfig", "FileSystemConfigs", "FunctionScalingConfig", "FunctionUrlConfig", "Handler",
		"KmsKeyArn", "Layers", "LoggingConfig", "MemorySize", "PermissionsBoundary", "PropagateTags",
		"ProvisionedConcurrencyConfig", "PublishToLatestPublished", "RecursiveLoop", "ReservedConcurrentExecutions",
		"RolePath", "Runtime", "RuntimeConfig", "RuntimeManagementConfig", "SnapStart", "SourceKMSKeyArn", "Tags",
		"TenancyConfig", "Timeout", "Tracing", "VersionDeletionPolicy", "VpcConfig"}},
	{"HttpApi", []string{"AccessLogSettings", "Auth", "PropagateTags", "StageVariables", "Tags"}},
	{"SimpleTable", []string{"SSESpecification"}},
	{"StateMachine", []string{"PropagateTags"}},
	{"MicrovmImage", []string{"BaseImageArn", "BaseImageVersion", "BuildRoleArn", "Hooks", "EnvironmentVariables",
		"EgressNetworkConnectors", "CpuConfigurations", "Resources", "AdditionalOsCapabilities", "Logging", "Tags",
		"PropagateTags"}},
	{"NetworkConnector", []string{"OperatorRole", "Tags", "PropagateTags"}},
	{"WebFunction", []string{"Runtime", "Timeout", "EnvironmentVariables", "ExecutionRoleArn", "AuthType",
		"KmsKeyArn", "LoggingConfig", "ScalingConfig", "ThrottleConfig", "MaxConcurrencyPerEnvironment",
		"EgressNetworkConnectorArn", "EndpointType", "AutoDeploymentMode", "ReplicaRegions", "Tags"}},
}

// globalsRead returns the properties the serverless transform reads from the
// Globals for the key, false where it reads none for it.
func globalsRead(key string) ([]string, bool) {
	for _, g := range globalsProperties {
		if g.key == key {
			return g.props, true
		}
	}
	return nil, false
}

// mergeOp is how the serverless transform merges a value of the Globals with
// the resource's value of one property where both are objects or both lists.
type mergeOp int

const (
	// mergeDeep merges two objects member by member and writes a list as the
	// global items followed by the resource's.
	mergeDeep mergeOp = iota
	// mergeReplace takes the resource's value.
	mergeReplace
	// mergePrune takes the members the resource writes, where it writes any,
	// each merged with the global member of its name.
	mergePrune
)

// mergeOps are the paths, a Globals key followed by the path of a property
// under it, at which the serverless transform merges otherwise than deeply:
// a function runs on one architecture, so the resource's list replaces the
// global one.
var mergeOps = []struct {
	path []string
	op   mergeOp
}{
	{[]string{"Function", "Architectures"}, mergeReplace},
	{[]string{"CapacityProvider", "InstanceRequirements", "Architectures"}, mergeReplace},
	{[]string{"CapacityProvider", "ManagedResourceTags"}, mergePrune},
}

// mergeOpAt returns how the serverless transform merges at path.
func mergeOpAt(path []string) mergeOp {
	for _, m := range mergeOps {
		if slices.Equal(m.path, path) {
			return m.op
		}
	}
	return mergeDeep
}

// serverless reports whether t declares the serverless transform.
func (t *template) serverless() bool {
	return slices.ContainsFunc(declaredTransforms(t.top.get("Transform")), func(tr transform) bool {
		return tr.name == serverlessTransform
	})
}

// globalsNotes returns the note naming the Globals t declares where it does
// not declare the serverless transform, the one transform reading them, so
// no resource takes them; t named as subject names it.
func (t *template) globalsNotes(subject string) []string {
	if raw := t.top.get(globalsSection); raw == nil || isNull(raw) || t.serverless() {
		return nil
	}
	return []string{subject + " declares Globals, which the transform " + serverlessTransform + " alone reads, and " +
		"not that transform, so no resource takes them."}
}

// typeGlobals are the properties the Globals give the resources of one type.
type typeGlobals struct {
	// key names the type, serverlessPrefix followed by it.
	key string
	// props are the properties the transform reads, as the Globals write
	// them.
	props obj
}

// serverlessResources merges the Globals of t into its serverless resources
// as the serverless transform merges them when the stack deploys, once for t,
// before any node is read of them. Where t declares the transform, each
// resource of a type its Globals name takes the properties they give that
// type ([reader.globals]) merged with its own: a primitive — text, a number, a
// boolean, null, or an intrinsic function — the resource writes replaces the
// global one; an object's members are merged, the resource's member merged
// with the global member of its name; a list is the global items followed by
// the resource's; values of differing kinds take the resource's; and at the
// paths of mergeOps the transform's own rule holds instead. A resource
// writing no Properties takes the global ones; one writing Properties as null
// takes none, as the transform reads it.
func (r *reader) serverlessResources(t *template) {
	if t.serverlessRead {
		return
	}
	t.serverlessRead = true
	if !t.serverless() {
		return
	}
	globals := r.globals(t)
	for i, e := range t.resources {
		res, isObject := objectOf(e.Value)
		typ, _ := schemadraft.JSONText(res.get("Type"))
		if !isObject || !strings.HasPrefix(typ, serverlessPrefix) {
			continue
		}
		var global obj
		for _, g := range globals {
			if serverlessPrefix+g.key == typ {
				global = g.props
			}
		}
		if len(global) == 0 {
			continue
		}
		own := res.get("Properties")
		if own == nil {
			own = json.RawMessage("{}")
		}
		ownProps, _ := objectOf(own)
		for _, g := range global {
			if ownProps.get(g.Key) == nil {
				if t.fromGlobals == nil {
					t.fromGlobals = map[string][]string{}
				}
				t.fromGlobals[e.Key] = append(t.fromGlobals[e.Key], g.Key)
			}
		}
		props := merged(global.encoded(), own, []string{strings.TrimPrefix(typ, serverlessPrefix)})
		t.resources[i].Value = res.with("Properties", props).encoded()
	}
}

// globals returns the properties the Globals of t, which declares the
// serverless transform, give each resource type, in the order they name the
// types: of each key naming a type the transform reads Globals for, the
// properties it reads for that type. What the transform refuses — Globals
// written as no object of each type's properties by the type's name, a key
// naming no type it reads Globals for, a type's properties written as no
// object, and a property it does not read for the type — is merged into no
// resource, and is reported value-not-carried, naming where it is written.
func (r *reader) globals(t *template) []typeGlobals {
	raw := t.top.get(globalsSection)
	if raw == nil || isNull(raw) {
		return nil
	}
	refused := func(at, what, fix string) {
		r.warnAt(cloudimport.CodeValueNotCarried, t.file, at, "the template "+t.file+" writes "+what+", which "+
			"the transform "+serverlessTransform+" refuses when the stack deploys, so no resource takes them: "+fix)
	}
	section, isObject := objectOf(raw)
	if !isObject || isIntrinsic(section) {
		at := importer.Pointer(globalsSection)
		refused(at, "its Globals at "+at+" as no object of each resource type's properties by the type's name",
			"write the properties of each resource type under its name")
		return nil
	}
	var out []typeGlobals
	for _, e := range section {
		at := importer.Pointer(globalsSection, e.Key)
		read, known := globalsRead(e.Key)
		if !known {
			r.warnAt(cloudimport.CodeValueNotCarried, t.file, at, "the template "+t.file+" writes Globals for "+e.Key+
				" at "+at+", which names no resource type the transform "+serverlessTransform+" reads Globals for "+
				"and is refused when the stack deploys, so no resource takes them: write them on each resource they "+
				"are meant for")
			continue
		}
		props, isObject := objectOf(e.Value)
		if !isObject || isIntrinsic(props) {
			refused(at, "Globals for "+e.Key+" at "+at+" as no object of properties by their names",
				"write each property under its name")
			continue
		}
		g := typeGlobals{key: e.Key}
		for _, p := range props {
			if slices.Contains(read, p.Key) {
				g.props = append(g.props, p)
				continue
			}
			here := at + importer.Pointer(p.Key)
			r.warnAt(cloudimport.CodeValueNotCarried, t.file, here, "the template "+t.file+" writes "+p.Key+
				" under Globals for "+e.Key+" at "+here+", which the transform "+serverlessTransform+" does not read "+
				"from Globals for an "+serverlessPrefix+e.Key+" and refuses when the stack deploys, so no resource "+
				"takes it: write it on each resource it is meant for")
		}
		out = append(out, g)
	}
	return out
}

// isIntrinsic reports whether o, the members of an object, is an intrinsic
// function: one member named Ref, Condition, or Fn:: and a function's name.
func isIntrinsic(o obj) bool {
	return len(o) == 1 && cfntemplate.IsIntrinsicKey(o[0].Key)
}

// mergeToken is what the serverless transform reads a value as when it
// merges it.
type mergeToken int

const (
	mergePrimitive mergeToken = iota
	mergeObject
	mergeList
)

// tokenOf returns what the serverless transform reads raw as: an object, a
// list, or a primitive — text, a number, a boolean, null, or an intrinsic
// function, which the transform merges as it merges text.
func tokenOf(raw json.RawMessage) mergeToken {
	switch kind(raw) {
	case '{':
		if o, _ := objectOf(raw); isIntrinsic(o) {
			return mergePrimitive
		}
		return mergeObject
	case '[':
		return mergeList
	}
	return mergePrimitive
}

// merged returns the value a resource takes where the Globals write global
// and the resource writes local, at path, as [reader.serverlessResources]
// says.
func merged(global, local json.RawMessage, path []string) json.RawMessage {
	token := tokenOf(global)
	if token != tokenOf(local) || token == mergePrimitive {
		return local
	}
	op := mergeOpAt(path)
	if op == mergeReplace {
		return local
	}
	if token == mergeList {
		globalItems, _ := itemsOf(global)
		localItems, _ := itemsOf(local)
		return encodedItems(append(globalItems, localItems...))
	}
	g, _ := objectOf(global)
	l, _ := objectOf(local)
	var out obj
	if op != mergePrune || len(l) == 0 {
		for _, e := range g {
			if l.get(e.Key) == nil {
				out = append(out, e)
			}
		}
	}
	for _, e := range l {
		if gv := g.get(e.Key); gv != nil {
			e.Value = merged(gv, e.Value, append(slices.Clone(path), e.Key))
		}
		out = append(out, e)
	}
	return out.encoded()
}

// with returns o with the member named key holding value, added where o has
// none.
func (o obj) with(key string, value json.RawMessage) obj {
	out := slices.Clone(o)
	for i := range out {
		if out[i].Key == key {
			out[i].Value = value
			return out
		}
	}
	return append(out, importer.ObjectEntry{Key: key, Value: value})
}

// encoded returns o as a JSON object, its members sorted by key as the
// template reader writes them.
func (o obj) encoded() json.RawMessage {
	sorted := slices.Clone(o)
	slices.SortStableFunc(sorted, func(a, b importer.ObjectEntry) int { return strings.Compare(a.Key, b.Key) })
	var b bytes.Buffer
	b.WriteByte('{')
	for i, e := range sorted {
		if i > 0 {
			b.WriteByte(',')
		}
		key, _ := json.Marshal(e.Key)
		b.Write(key)
		b.WriteByte(':')
		b.Write(e.Value)
	}
	b.WriteByte('}')
	return b.Bytes()
}

// encodedItems returns items as a JSON array.
func encodedItems(items []json.RawMessage) json.RawMessage {
	var b bytes.Buffer
	b.WriteByte('[')
	for i, item := range items {
		if i > 0 {
			b.WriteByte(',')
		}
		b.Write(item)
	}
	b.WriteByte(']')
	return b.Bytes()
}

// notTaken are the properties of a serverless function the Recipe deploying a
// function takes none of, each with what it writes and the fix.
var notTaken = []struct {
	property, does, fix string
}{
	{"AutoPublishAlias", "from which SAM publishes a version of the function and an alias naming it whenever the " +
		"function changes", "the Recipe deploying a function publishes no alias, so call the function itself, or " +
		"write the alias by hand"},
	{"CodeUri", "the function's code as a .zip package", codeFix},
	{"DeploymentPreference", "from which SAM writes an AWS::CodeDeploy::Application and an " +
		"AWS::CodeDeploy::DeploymentGroup shifting the alias to each new version", "the Recipe deploying a function " +
		"shifts no traffic, so each change is deployed at once; shift traffic with AWS CodeDeploy by hand"},
	{"InlineCode", "the function's code written in the template", codeFix},
	{"Policies", "from which SAM writes an execution role for the function", "the Recipe takes an existing role: " +
		"infra.overrides.Role, so grant what the policies allow in the role the operator supplies there"},
}

// codeFix is the fix of a serverless function's code the Recipe deploying a
// function does not take.
const codeFix = "the Recipe deploying a function runs a container image, infra.image, which is left for the " +
	"operator, so build the code into an image in Amazon ECR"

// serverlessFunctions records, on each serverless function of g, what the
// Recipe deploying a function, which a row maps it to, does not take of it:
// each property of notTaken it writes, or takes from the Globals, reported
// value-not-carried where it is written — under the Globals where the
// function takes it from them — and, for a function whose code is a .zip
// package, the decision saying that the Recipe runs a container image
// infra.image names, left for the operator. A function drafted from the
// source keeps them all, so none is reported where no row maps it.
func (r *reader) serverlessFunctions(g *cloudimport.Graph) {
	for _, n := range g.Nodes {
		if n.Type != functionType {
			continue
		}
		h := r.home[n]
		if h.s == nil {
			continue
		}
		props := propAt(n.Body, []string{"Properties"})
		if props == nil {
			continue
		}
		for _, p := range notTaken {
			if propAt(props.Props, []string{p.property}) == nil {
				continue
			}
			file := h.s.t.file
			at := importer.Pointer("Resources", h.id, "Properties", p.property)
			where := "the template " + file + " writes " + p.property + " on the function " + h.id + " at " + at
			if slices.Contains(h.s.t.fromGlobals[h.id], p.property) {
				at = importer.Pointer(globalsSection, "Function", p.property)
				where = "the template " + file + " writes " + p.property + " under Globals for Function at " + at +
					", which the function " + h.id + " takes"
			}
			d := cloudimport.Report(cloudimport.CodeValueNotCarried, where+", "+p.does+", so it is not carried: "+
				p.fix)
			d.SourceRef = file + "#" + at
			n.ReportsIfMapped = append(n.ReportsIfMapped, d)
		}
		if code, ok := textAt(props.Props, "CodeUri"); ok {
			n.SaysIfMapped = append(n.SaysIfMapped, "the Recipe deploying a function runs a container image, "+
				"infra.image, which is left for the operator: build the code at "+draftyaml.Comment(code)+", its "+
				"CodeUri, into an image in Amazon ECR and set infra.image to it")
		} else if propAt(props.Props, []string{"CodeUri"}) != nil || propAt(props.Props, []string{"InlineCode"}) != nil {
			n.SaysIfMapped = append(n.SaysIfMapped, "the Recipe deploying a function runs a container image, "+
				"infra.image, which is left for the operator: build the function's code into an image in Amazon "+
				"ECR and set infra.image to it")
		}
	}
}
