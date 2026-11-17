// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package armcore

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"testing"

	frp "github.com/aggrega-ai/aggrega-cli/internal/compiler/featurerecipe_pipeline"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/rendercore"
	"github.com/aggrega-ai/aggrega-cli/internal/plugincondition"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

const (
	// hostExpression is the host the datastore provides: an expression over its
	// own resource Db, which only the producer's template declares.
	hostExpression = "[reference('Db').properties.fullyQualifiedDomainName]"
	// hostOutput is the output the producer exposes the host under:
	// OutputName("payments-db", "host").
	hostOutput = "payments_zhdb__host"
	// linkedDeployment is the name the producer's template is deployed under:
	// DeploymentName("orders", "stack-a/arm").
	linkedDeployment = "aggrega--orders--stack-a-zsarm"
	// deploymentNamePointer is where the producer's template records that name.
	deploymentNamePointer = "/metadata/aggrega.ai~1deployment-name"
)

// testLink is the link from orders-api, in boundary stack-b/arm, to payments-db,
// in boundary stack-a/arm, on the database socket. The datastore provides host
// as an expression and port as literal text; the edge injects host as DB_HOST.
func testLink() rendercore.LinkEdge {
	var provides types.OrderedMap[string]
	provides.Set("host", hostExpression)
	provides.Set("port", "5432")
	edge := rendercore.LinkEdge{
		Source:   rendercore.LinkEndpoint{Node: "orders-api", Boundary: "stack-b/arm", Provider: TargetName, Release: "orders"},
		Target:   rendercore.LinkEndpoint{Node: "payments-db", Boundary: "stack-a/arm", Provider: TargetName, Release: "orders"},
		Socket:   "database",
		Provides: provides,
	}
	return injecting(edge, "DB_HOST", "host")
}

// injecting returns edge with its inject mapping replaced by env entries, given
// as pairs of variable and provides key.
func injecting(edge rendercore.LinkEdge, pairs ...string) rendercore.LinkEdge {
	var env types.OrderedMap[string]
	for i := 0; i+1 < len(pairs); i += 2 {
		env.Set(pairs[i], pairs[i+1])
	}
	var inject types.OrderedMap[types.OrderedMap[string]]
	if env.Len() > 0 {
		inject.Set("env", env)
	}
	edge.Inject = inject
	return edge
}

// providing returns edge with one provides key set to text, keeping the others.
func providing(edge rendercore.LinkEdge, key, text string) rendercore.LinkEdge {
	var provides types.OrderedMap[string]
	for k, v := range edge.Provides.All() {
		provides.Set(k, v)
	}
	provides.Set(key, text)
	edge.Provides = provides
	return edge
}

// placeholder is the placeholder the host stamps for one provides key of
// testLink in the consumer's output.
func placeholder(key string) string {
	return rendercore.CrossBoundaryLinkSentinel("orders-api", "payments-db", "database", key)
}

// templateFiles writes a resource group template holding the given sections as
// the one file a link export is handed.
func templateFiles(t *testing.T, sections map[string]any) []rendercore.RenderedFile {
	t.Helper()
	template := map[string]any{
		sectionSchema:          SchemaResourceGroup,
		sectionLanguageVersion: LanguageVersion,
		sectionContentVersion:  ContentVersion,
	}
	for k, v := range sections {
		template[k] = v
	}
	content, err := encodeTemplate(template)
	if err != nil {
		t.Fatal(err)
	}
	return []rendercore.RenderedFile{{Path: TemplateFileName, Content: content}}
}

// producerFiles is the producer's template: the resource Db the provides
// expressions read, beside the given sections.
func producerFiles(t *testing.T, sections map[string]any) []rendercore.RenderedFile {
	t.Helper()
	all := map[string]any{sectionResources: map[string]any{"Db": map[string]any{
		"type": "Microsoft.DBforPostgreSQL/flexibleServers", "apiVersion": "2024-08-01", "name": "payments-db",
		"properties": map[string]any{"version": "16"},
	}}}
	for k, v := range sections {
		all[k] = v
	}
	return templateFiles(t, all)
}

// consumerFiles is the consumer's template: the resource App, whose properties
// and other fields are given, beside the given sections.
func consumerFiles(t *testing.T, properties, appFields, sections map[string]any) []rendercore.RenderedFile {
	t.Helper()
	app := map[string]any{"type": "Microsoft.App/containerApps", "apiVersion": "2024-03-01", "name": "orders-api", "properties": properties}
	for k, v := range appFields {
		app[k] = v
	}
	all := map[string]any{sectionResources: map[string]any{"App": app}}
	for k, v := range sections {
		all[k] = v
	}
	return templateFiles(t, all)
}

// published publishes edge against the plain producer template and returns the
// handle the consumer resolves against.
func published(t *testing.T, edge rendercore.LinkEdge) map[string]string {
	t.Helper()
	out, err := LinkPublish(edge, producerFiles(t, nil))
	if err != nil {
		t.Fatalf("LinkPublish: %v", err)
	}
	return out.Handle
}

// templateOf decodes the one template file.
func templateOf(t *testing.T, files []rendercore.RenderedFile) map[string]any {
	t.Helper()
	var doc map[string]any
	if err := json.Unmarshal(files[0].Content, &doc); err != nil {
		t.Fatal(err)
	}
	return doc
}

// applied applies the patches to the template file as the host applies them,
// each add and replace landing at its JSON pointer, and returns the file again.
func applied(t *testing.T, files []rendercore.RenderedFile, patches []rendercore.LinkPatch) []rendercore.RenderedFile {
	t.Helper()
	doc := templateOf(t, files)
	for _, p := range patches {
		if p.File != TemplateFileName {
			t.Fatalf("a patch names the file %q, want %q", p.File, TemplateFileName)
		}
		for _, op := range p.Ops {
			if op.Scope != frp.DocumentScopeOwn || op.Key != TemplateFileName {
				t.Fatalf("an operation addresses scope %q key %q, want %q %q", op.Scope, op.Key, frp.DocumentScopeOwn, TemplateFileName)
			}
			setAt(t, doc, op)
		}
	}
	content, err := encodeTemplate(doc)
	if err != nil {
		t.Fatal(err)
	}
	return []rendercore.RenderedFile{{Path: TemplateFileName, Content: content}}
}

// setAt applies one add or replace operation to doc.
func setAt(t *testing.T, doc map[string]any, op frp.JSONPatchOp) {
	t.Helper()
	tokens := strings.Split(strings.TrimPrefix(op.Path, "/"), "/")
	for i, tok := range tokens {
		tokens[i] = strings.ReplaceAll(strings.ReplaceAll(tok, "~1", "/"), "~0", "~")
	}
	var parent any = doc
	for _, tok := range tokens[:len(tokens)-1] {
		switch x := parent.(type) {
		case map[string]any:
			parent = x[tok]
		case []any:
			i, err := strconv.Atoi(tok)
			if err != nil || i >= len(x) {
				t.Fatalf("%s: no element %s", op.Path, tok)
			}
			parent = x[i]
		default:
			t.Fatalf("%s: no container at %s", op.Path, tok)
		}
	}
	last := tokens[len(tokens)-1]
	switch x := parent.(type) {
	case map[string]any:
		_, present := x[last]
		if op.Op == "add" && present || op.Op == "replace" && !present || op.Op != "add" && op.Op != "replace" {
			t.Fatalf("%s %s: the member is present: %v", op.Op, op.Path, present)
		}
		x[last] = op.Value
	case []any:
		i, err := strconv.Atoi(last)
		if op.Op != "replace" || err != nil || i >= len(x) {
			t.Fatalf("%s %s: no element to replace", op.Op, op.Path)
		}
		x[i] = op.Value
	default:
		t.Fatalf("%s: no container", op.Path)
	}
}

// opsOf returns every operation of the patches, failing the test when a patch
// names another file.
func opsOf(t *testing.T, patches []rendercore.LinkPatch) []frp.JSONPatchOp {
	t.Helper()
	var ops []frp.JSONPatchOp
	for _, p := range patches {
		if p.File != TemplateFileName {
			t.Fatalf("a patch names the file %q, want %q", p.File, TemplateFileName)
		}
		ops = append(ops, p.Ops...)
	}
	return ops
}

// stringOutput is the body of a string output carrying value.
func stringOutput(value string) map[string]any {
	return map[string]any{"type": "string", "value": value}
}

func TestLinkPublishAddsAStringOutput(t *testing.T) {
	t.Parallel()
	edge := injecting(testLink(), "DB_HOST", "host", "DB_PORT", "port")
	wantOutputs := map[string]any{
		hostOutput:            stringOutput("[string(reference('Db').properties.fullyQualifiedDomainName)]"),
		"payments_zhdb__port": stringOutput("5432"),
	}
	wantHandle := map[string]string{
		"deploymentName":  linkedDeployment,
		"outputName.host": hostOutput,
		"outputName.port": "payments_zhdb__port",
	}

	t.Run("a template with no outputs gains the outputs member", func(t *testing.T) {
		t.Parallel()
		files := producerFiles(t, nil)
		out, err := LinkPublish(edge, files)
		if err != nil {
			t.Fatalf("LinkPublish: %v", err)
		}
		if !reflect.DeepEqual(out.Handle, wantHandle) {
			t.Errorf("handle = %v, want %v", out.Handle, wantHandle)
		}
		ops := opsOf(t, out.Patches)
		if len(ops) == 0 || ops[0].Op != "add" || ops[0].Path != "/outputs" {
			t.Fatalf("want the first operation to add /outputs, got %+v", ops)
		}
		got := templateOf(t, applied(t, files, out.Patches))
		if !jsonEqual(got[sectionOutputs], wantOutputs) {
			t.Errorf("outputs = %s, want %s", mustJSON(t, got[sectionOutputs]), mustJSON(t, wantOutputs))
		}
	})

	t.Run("a template with outputs gains one member per key", func(t *testing.T) {
		t.Parallel()
		authored := stringOutput("[reference('Db').properties.version]")
		files := producerFiles(t, map[string]any{sectionOutputs: map[string]any{"payments_zhdb__version": authored}})
		out, err := LinkPublish(edge, files)
		if err != nil {
			t.Fatalf("LinkPublish: %v", err)
		}
		var adds []string
		for _, op := range opsOf(t, out.Patches) {
			if strings.HasPrefix(op.Path, "/outputs") {
				adds = append(adds, op.Op+" "+op.Path)
			}
		}
		want := []string{"add /outputs/" + hostOutput, "add /outputs/payments_zhdb__port"}
		if !reflect.DeepEqual(adds, want) {
			t.Errorf("output operations = %v, want %v", adds, want)
		}
		got := templateOf(t, applied(t, files, out.Patches))[sectionOutputs].(map[string]any)
		if !jsonEqual(got["payments_zhdb__version"], authored) {
			t.Errorf("the authored output changed: %s", mustJSON(t, got["payments_zhdb__version"]))
		}
		for name, body := range wantOutputs {
			if !jsonEqual(got[name], body) {
				t.Errorf("output %s = %s, want %s", name, mustJSON(t, got[name]), mustJSON(t, body))
			}
		}
	})
}

func TestLinkPublishReusesOneOutputForTwoConsumers(t *testing.T) {
	t.Parallel()
	first := testLink()
	second := testLink()
	second.Source = rendercore.LinkEndpoint{Node: "billing-api", Boundary: "stack-c/arm", Provider: TargetName, Release: "orders"}

	files := producerFiles(t, nil)
	out1, err := LinkPublish(first, files)
	if err != nil {
		t.Fatalf("first LinkPublish: %v", err)
	}
	files = applied(t, files, out1.Patches)
	out2, err := LinkPublish(second, files)
	if err != nil {
		t.Fatalf("second LinkPublish: %v", err)
	}
	if len(out2.Patches) != 0 {
		t.Errorf("the second consumer's publish wrote %+v, want nothing: the output and the name are already there", out2.Patches)
	}
	if !reflect.DeepEqual(out1.Handle, out2.Handle) {
		t.Errorf("the two consumers read different identities: %v and %v", out1.Handle, out2.Handle)
	}
	outputs := templateOf(t, files)[sectionOutputs].(map[string]any)
	if len(outputs) != 1 || outputs[hostOutput] == nil {
		t.Errorf("want the one output %s, got %s", hostOutput, mustJSON(t, outputs))
	}
}

func TestLinkPublishConflictingOutputHalts(t *testing.T) {
	t.Parallel()
	// The datastore's fragment writes its own output host, which lands under the
	// name the link writes the host under, with the expression as written.
	files := producerFiles(t, map[string]any{sectionOutputs: map[string]any{hostOutput: stringOutput(hostExpression)}})
	_, err := LinkPublish(testLink(), files)
	wantCondition(t, err, plugincondition.ErrPluginOutputNameCollision(), "fullyQualifiedDomainName")
	for _, want := range []string{hostOutput, `"host"`, `"payments-db"`, `"stack-a/arm"`} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the halt does not name %s: %v", want, err)
		}
	}
}

func TestLinkPublishOutputCapHalts(t *testing.T) {
	t.Parallel()
	outputs := func(n int) map[string]any {
		m := map[string]any{}
		for i := 0; i < n; i++ {
			m[fmt.Sprintf("other__o%d", i)] = stringOutput("x")
		}
		return m
	}
	if _, err := LinkPublish(testLink(), producerFiles(t, map[string]any{sectionOutputs: outputs(63)})); err != nil {
		t.Fatalf("the 64th output halts: %v", err)
	}
	_, err := LinkPublish(testLink(), producerFiles(t, map[string]any{sectionOutputs: outputs(64)}))
	wantCondition(t, err, plugincondition.ErrPluginCannotExpressInput())
	for _, want := range []string{"65 outputs", "64 outputs", "split the boundary"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the halt does not say %q: %v", want, err)
		}
	}
}

func TestLinkPublishRecordsTheDeploymentName(t *testing.T) {
	t.Parallel()
	metadataOps := func(ops []frp.JSONPatchOp) []frp.JSONPatchOp {
		var out []frp.JSONPatchOp
		for _, op := range ops {
			if strings.HasPrefix(op.Path, "/metadata") {
				out = append(out, op)
			}
		}
		return out
	}

	t.Run("a template with no metadata gains the metadata member", func(t *testing.T) {
		t.Parallel()
		files := producerFiles(t, nil)
		out, err := LinkPublish(testLink(), files)
		if err != nil {
			t.Fatalf("LinkPublish: %v", err)
		}
		if got := out.Handle["deploymentName"]; got != linkedDeployment {
			t.Errorf("handle deploymentName = %q, want %q", got, linkedDeployment)
		}
		ops := metadataOps(opsOf(t, out.Patches))
		if len(ops) != 1 || ops[0].Op != "add" || ops[0].Path != "/metadata" {
			t.Fatalf("want one add of /metadata, got %+v", ops)
		}
		got := templateOf(t, applied(t, files, out.Patches))[sectionMetadata]
		if !jsonEqual(got, map[string]any{DeploymentNameMetadataKey: linkedDeployment}) {
			t.Errorf("metadata = %s", mustJSON(t, got))
		}
	})

	t.Run("a template with metadata gains one key", func(t *testing.T) {
		t.Parallel()
		files := producerFiles(t, map[string]any{sectionMetadata: map[string]any{"owner": "payments"}})
		out, err := LinkPublish(testLink(), files)
		if err != nil {
			t.Fatalf("LinkPublish: %v", err)
		}
		ops := metadataOps(opsOf(t, out.Patches))
		if len(ops) != 1 || ops[0].Op != "add" || ops[0].Path != deploymentNamePointer || ops[0].Value != linkedDeployment {
			t.Fatalf("want one add of %s, got %+v", deploymentNamePointer, ops)
		}
		got := templateOf(t, applied(t, files, out.Patches))[sectionMetadata]
		if !jsonEqual(got, map[string]any{"owner": "payments", DeploymentNameMetadataKey: linkedDeployment}) {
			t.Errorf("metadata = %s", mustJSON(t, got))
		}
	})

	t.Run("a template that records the name is left as it is", func(t *testing.T) {
		t.Parallel()
		files := producerFiles(t, map[string]any{sectionMetadata: map[string]any{DeploymentNameMetadataKey: linkedDeployment}})
		out, err := LinkPublish(testLink(), files)
		if err != nil {
			t.Fatalf("LinkPublish: %v", err)
		}
		if ops := metadataOps(opsOf(t, out.Patches)); len(ops) != 0 {
			t.Errorf("want no metadata operation, got %+v", ops)
		}
	})
}

func TestLinkPublishConflictingDeploymentNameHalts(t *testing.T) {
	t.Parallel()
	files := producerFiles(t, map[string]any{sectionMetadata: map[string]any{DeploymentNameMetadataKey: "payments-by-hand"}})
	_, err := LinkPublish(testLink(), files)
	wantCondition(t, err, plugincondition.ErrPluginOutputNameCollision(), "payments-by-hand")
	for _, want := range []string{DeploymentNameMetadataKey, `"stack-a/arm"`} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the halt does not name %s: %v", want, err)
		}
	}
}

func TestLinkPublishLiteralReadKeyAddsNoOutput(t *testing.T) {
	t.Parallel()
	edge := injecting(testLink())
	edge.ReadProvidesKeys = []string{"port"}
	files := producerFiles(t, nil)
	out, err := LinkPublish(edge, files)
	if err != nil {
		t.Fatalf("LinkPublish: %v", err)
	}
	for _, op := range opsOf(t, out.Patches) {
		if strings.HasPrefix(op.Path, "/outputs") {
			t.Errorf("a literal read key wrote an output: %+v", op)
		}
	}
	if _, ok := out.Handle["outputName.port"]; ok {
		t.Errorf("the handle names an output for a literal read key: %v", out.Handle)
	}

	// A read key whose text is an expression is read through an output, as an
	// injected key is.
	edge.ReadProvidesKeys = []string{"host", "port"}
	out, err = LinkPublish(edge, files)
	if err != nil {
		t.Fatalf("LinkPublish: %v", err)
	}
	outputs := templateOf(t, applied(t, files, out.Patches))[sectionOutputs]
	if !jsonEqual(outputs, map[string]any{hostOutput: stringOutput("[string(reference('Db').properties.fullyQualifiedDomainName)]")}) {
		t.Errorf("outputs = %s, want only the host", mustJSON(t, outputs))
	}
}

func TestLinkPublishSecretHalts(t *testing.T) {
	t.Parallel()
	parameters := map[string]any{
		"adminPassword": map[string]any{"type": "securestring"},
		"settings":      map[string]any{"$ref": "#/definitions/secretSettings"},
		"location":      map[string]any{"type": "string"},
	}
	definitions := map[string]any{"secretSettings": map[string]any{"type": "secureObject"}}
	files := producerFiles(t, map[string]any{sectionParameters: parameters, sectionDefinitions: definitions})
	cases := []struct{ name, text string }{
		{"a list function", "[listKeys('Db', '2023-01-01').keys[0].value]"},
		{"secure outputs of a nested template", "[listOutputsWithSecureValues('Db', '2025-04-01').password]"},
		{"a securestring parameter", "[parameters('adminPassword')]"},
		{"a securestring parameter spelled in another case", "[format('{0}', parameters('ADMINPASSWORD'))]"},
		{"a secureObject parameter by its definition", "[parameters('settings').key]"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			_, err := LinkPublish(providing(testLink(), "host", c.text), files)
			wantCondition(t, err, plugincondition.ErrPluginValueNotRepresentable(), "adminPassword", "listKeys", "settings")
			for _, want := range []string{
				`"host"`, `"payments-db"`,
				"a secret read by another deployment lands in its history",
				"store it in Key Vault",
				"core/referenced-secret on both ends",
			} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("the halt does not say %q: %v", want, err)
				}
			}
		})
	}
	if _, err := LinkPublish(providing(testLink(), "host", "[parameters('location')]"), files); err != nil {
		t.Errorf("a parameter that is not secure halts: %v", err)
	}
}

// defaultReference is the read of the host output the resolve writes with the
// placement defaults.
const defaultReference = "reference(format('/subscriptions/{0}/resourceGroups/{1}/providers/Microsoft.Resources/deployments/{2}', subscription().subscriptionId, resourceGroup().name, 'aggrega--orders--stack-a-zsarm'), '2025-04-01').outputs.payments_zhdb__host.value"

func TestLinkResolveWholePlaceholder(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name        string
		annotations map[string]string
		want        string
	}{
		{"the defaults", nil, "[" + defaultReference + "]"},
		{"both annotations given", map[string]string{
			AnnotationSubscription:  "00000000-0000-0000-0000-000000000001",
			AnnotationResourceGroup: "rg-payments",
		}, "[reference(format('/subscriptions/{0}/resourceGroups/{1}/providers/Microsoft.Resources/deployments/{2}', '00000000-0000-0000-0000-000000000001', 'rg-payments', 'aggrega--orders--stack-a-zsarm'), '2025-04-01').outputs.payments_zhdb__host.value]"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			edge := testLink()
			edge.TargetAnnotations = c.annotations
			// The consumer's own placement does not shape the read.
			edge.SourceAnnotations = map[string]string{AnnotationSubscription: "consumer-sub", AnnotationResourceGroup: "consumer-rg"}
			files := consumerFiles(t,
				map[string]any{"settings": map[string]any{"host": placeholder("host")}}, nil,
				map[string]any{sectionOutputs: map[string]any{"orders_zhapi__dbhost": stringOutput(placeholder("host"))}})
			out, err := LinkResolve(edge, published(t, edge), files)
			if err != nil {
				t.Fatalf("LinkResolve: %v", err)
			}
			ops := opsOf(t, out.Patches)
			want := []string{
				"replace /outputs/orders_zhapi__dbhost/value",
				"replace /resources/App/properties/settings/host",
			}
			var got []string
			for _, op := range ops {
				got = append(got, op.Op+" "+op.Path)
				if op.Value != c.want {
					t.Errorf("%s is replaced with %v\nwant %s", op.Path, op.Value, c.want)
				}
			}
			if !reflect.DeepEqual(got, want) {
				t.Errorf("operations = %v, want %v", got, want)
			}
		})
	}
}

func TestLinkResolveFormatArgument(t *testing.T) {
	t.Parallel()
	edge := testLink()
	// Another link's placeholder in the same string is that link's to resolve.
	other := rendercore.CrossBoundaryLinkSentinel("orders-api", "audit-log", "audit", "endpoint")
	composed := "[format('postgres://{0}:5432/?audit={1}', '" + placeholder("host") + "', '" + other + "')]"
	files := consumerFiles(t, map[string]any{"env": []any{map[string]any{"name": "DATABASE_URL", "value": composed}}}, nil, nil)
	out, err := LinkResolve(edge, published(t, edge), files)
	if err != nil {
		t.Fatalf("LinkResolve: %v", err)
	}
	ops := opsOf(t, out.Patches)
	if len(ops) != 1 {
		t.Fatalf("want one operation, got %+v", ops)
	}
	want := "[format('postgres://{0}:5432/?audit={1}', " + defaultReference + ", '" + other + "')]"
	if ops[0].Op != "replace" || ops[0].Path != "/resources/App/properties/env/0/value" || ops[0].Value != want {
		t.Errorf("got %s %s = %v\nwant replace /resources/App/properties/env/0/value = %s", ops[0].Op, ops[0].Path, ops[0].Value, want)
	}
}

func TestLinkResolvePlaceholderOutsidePropertiesHalts(t *testing.T) {
	t.Parallel()
	whole := placeholder("host")
	composed := "[format('https://{0}', '" + placeholder("host") + "')]"
	cases := []struct {
		name                string
		appFields, sections map[string]any
		pointer             string
	}{
		{"a tag", map[string]any{"tags": map[string]any{"endpoint": whole}}, nil, "/resources/App/tags/endpoint"},
		{"a resource's name", map[string]any{"name": composed}, nil, "/resources/App/name"},
		{"a variable", nil, map[string]any{sectionVariables: map[string]any{"endpoint": composed}}, "/variables/endpoint"},
		{"a parameter's default", nil, map[string]any{sectionParameters: map[string]any{"endpoint": map[string]any{"type": "string", "defaultValue": whole}}}, "/parameters/endpoint/defaultValue"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			edge := testLink()
			files := consumerFiles(t, map[string]any{}, c.appFields, c.sections)
			_, err := LinkResolve(edge, published(t, edge), files)
			wantCondition(t, err, plugincondition.ErrPluginValueNotRepresentable(), "fullyQualifiedDomainName")
			for _, want := range []string{c.pointer, `"stack-b/arm"`, `"host"`, `"payments-db"`, "reference()", "inside a resource's properties"} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("the halt does not say %q: %v", want, err)
				}
			}
		})
	}
}

func TestLinkResolveValueInLiteralTextHalts(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name, text string
		says       []string
		silent     []string
	}{
		// The assembly leaves text that opens with [[ as written, so a value read
		// from another template stays inside it.
		{"text that opens with [[", "[[db " + placeholder("host") + "]",
			[]string{"opens with [[", "does not open with [["}, nil},
		// Literal text the assembly did not write as a format() expression, such
		// as text written into the template after it was assembled.
		{"literal text that is not an expression", "db at " + placeholder("host"),
			[]string{"literal text", "format() expression"}, []string{"[["}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			edge := testLink()
			files := consumerFiles(t, map[string]any{"label": c.text}, nil, nil)
			_, err := LinkResolve(edge, published(t, edge), files)
			wantCondition(t, err, plugincondition.ErrPluginValueNotRepresentable(), "db at", "[[db")
			for _, want := range append([]string{"/resources/App/properties/label", `"host"`, `"payments-db"`}, c.says...) {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("the halt does not say %q: %v", want, err)
				}
			}
			for _, absent := range c.silent {
				if strings.Contains(err.Error(), absent) {
					t.Errorf("the halt says %q, which is not true of this text: %v", absent, err)
				}
			}
		})
	}
}

func TestLinkPublishSplicedCallHalts(t *testing.T) {
	t.Parallel()
	const spliced = "mysql://[reference('Db').properties.fullyQualifiedDomainName]:3306"
	functions := []any{map[string]any{"namespace": "naming", "members": map[string]any{
		"host": map[string]any{"output": map[string]any{"type": "string", "value": "db"}},
	}}}
	cases := []struct {
		name, text string
		inject     bool
	}{
		{"a read key", spliced, false},
		{"an injected key", spliced, true},
		{"a call to a function the producing template declares", "mysql://[NAMING.host()]:3306", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			edge := injecting(providing(testLink(), "url", c.text))
			if c.inject {
				edge = injecting(edge, "DATABASE_URL", "url")
			} else {
				edge.ReadProvidesKeys = []string{"url"}
			}
			_, err := LinkPublish(edge, producerFiles(t, map[string]any{sectionFunctions: functions}))
			wantCondition(t, err, plugincondition.ErrPluginValueNotRepresentable(), "mysql://", "fullyQualifiedDomainName", "NAMING")
			for _, want := range []string{
				`"url"`, `"payments-db"`, `"database"`,
				"splices a template call into literal text",
				"unevaluated",
				"[format('postgres://{0}:5432', reference('Db').outputs.fqdn.value)]",
			} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("the halt does not say %q: %v", want, err)
				}
			}
		})
	}
}

func TestLinkPublishBracketedLiteralIsWrittenBack(t *testing.T) {
	t.Parallel()
	cases := []struct{ name, text string }{
		{"bracketed text that is no call", "match [a-z]+ against array[0] in a [test] value"},
		{"a call to a function no template declares", "mysql://[naming.host()]:3306"},
		{"text that opens with [[", "[[reference('Db').properties.version]"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			edge := injecting(providing(testLink(), "pattern", c.text))
			edge.ReadProvidesKeys = []string{"pattern"}
			out, err := LinkPublish(edge, producerFiles(t, nil))
			if err != nil {
				t.Fatalf("LinkPublish: %v", err)
			}
			for _, op := range opsOf(t, out.Patches) {
				if strings.HasPrefix(op.Path, "/outputs") {
					t.Errorf("literal text wrote an output: %+v", op)
				}
			}
			files := consumerFiles(t, map[string]any{"pattern": placeholder("pattern")}, nil, nil)
			resolved, err := LinkResolve(edge, out.Handle, files)
			if err != nil {
				t.Fatalf("LinkResolve: %v", err)
			}
			ops := opsOf(t, resolved.Patches)
			if len(ops) != 1 || ops[0].Path != "/resources/App/properties/pattern" || ops[0].Value != c.text {
				t.Errorf("write-back = %+v, want %q at /resources/App/properties/pattern", ops, c.text)
			}
		})
	}
}

func TestLinkPublishInjectedKeyAbsentFromProvidesHalts(t *testing.T) {
	t.Parallel()
	edge := injecting(testLink(), "DB_HOST", "host", "DB_REPLICA", "replica")
	_, err := LinkPublish(edge, producerFiles(t, nil))
	wantCondition(t, err, plugincondition.ErrPluginRequiredInputUnresolved())
	for _, want := range []string{`"replica"`, `"orders-api"`, `"payments-db"`, `"database"`} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the halt does not name %s: %v", want, err)
		}
	}
}

func TestLinkPublishAndResolveHaltOnATemplateThatCannotBeRead(t *testing.T) {
	t.Parallel()
	file := func(content string) []rendercore.RenderedFile {
		return []rendercore.RenderedFile{{Path: TemplateFileName, Content: []byte(content)}}
	}
	cases := []struct {
		name  string
		files []rendercore.RenderedFile
		cond  error
		says  []string
	}{
		{"absent", []rendercore.RenderedFile{{Path: "other.json", Content: []byte("{}")}},
			plugincondition.ErrPluginRequiredInputUnresolved(), []string{TemplateFileName, "no template to wire"}},
		{"not JSON", file(`{"resources": {"Db": `),
			plugincondition.ErrPluginValueNotRepresentable(), []string{TemplateFileName, "does not parse", "one JSON object"}},
		{"not an object", file(`["resources"]`),
			plugincondition.ErrPluginValueNotRepresentable(), []string{TemplateFileName, "one JSON object"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			edge := testLink()
			_, err := LinkPublish(edge, c.files)
			wantCondition(t, err, c.cond, `"Db"`)
			_, resolveErr := LinkResolve(edge, map[string]string{"deploymentName": linkedDeployment, "outputName.host": hostOutput}, c.files)
			wantCondition(t, resolveErr, c.cond, `"Db"`)
			for _, e := range []error{err, resolveErr} {
				for _, want := range append([]string{`"orders-api"`, `"payments-db"`}, c.says...) {
					if !strings.Contains(e.Error(), want) {
						t.Errorf("the halt does not say %q: %v", want, e)
					}
				}
			}
		})
	}
}

func TestLinkPublishSectionThatIsNotAnObjectHalts(t *testing.T) {
	t.Parallel()
	for _, section := range []string{sectionOutputs, sectionMetadata} {
		t.Run(section, func(t *testing.T) {
			t.Parallel()
			_, err := LinkPublish(testLink(), producerFiles(t, map[string]any{section: []any{"x"}}))
			wantCondition(t, err, plugincondition.ErrPluginValueNotRepresentable())
			for _, want := range []string{`"` + section + `"`, `"stack-a/arm"`, "object"} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("the halt does not say %q: %v", want, err)
				}
			}
		})
	}
}

func TestLinkResolveExpressionThatDoesNotParseHalts(t *testing.T) {
	t.Parallel()
	edge := testLink()
	// The two arguments of format() are not separated by a comma.
	files := consumerFiles(t, map[string]any{"url": "[format('https://{0}' '" + placeholder("host") + "')]"}, nil, nil)
	_, err := LinkResolve(edge, published(t, edge), files)
	wantCondition(t, err, plugincondition.ErrPluginValueNotRepresentable())
	for _, want := range []string{"/resources/App/properties/url", `"stack-b/arm"`, "does not parse"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the halt does not say %q: %v", want, err)
		}
	}
}

func TestLinkResolveUnreadableOutputNameHalts(t *testing.T) {
	t.Parallel()
	edge := testLink()
	files := consumerFiles(t, map[string]any{"host": placeholder("host")}, nil, nil)
	handle := map[string]string{"deploymentName": linkedDeployment, "outputName.host": "payments db/host"}
	_, err := LinkResolve(edge, handle, files)
	wantCondition(t, err, plugincondition.ErrPluginValueNotRepresentable())
	for _, want := range []string{`"outputName.host"`, `"orders-api"`, `"payments-db"`, `"database"`} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the halt does not say %q: %v", want, err)
		}
	}
}

func TestLinkResolveWritesBackLiteralReadKeys(t *testing.T) {
	t.Parallel()
	edge := providing(providing(injecting(testLink()), "label", "[[blue]"), "owner", "o'brien")
	edge.ReadProvidesKeys = []string{"port", "label", "owner"}
	format := func(text, key string) string {
		return "[format('" + text + "{0}', '" + placeholder(key) + "')]"
	}
	properties := map[string]any{"settings": map[string]any{
		"port":        placeholder("port"),
		"label":       placeholder("label"),
		"portURL":     format("db:", "port"),
		"labelText":   format("colour ", "label"),
		"ownerText":   format("owned by ", "owner"),
		"twoKeysOnce": "[format('{0}:{1}', '" + placeholder("owner") + "', '" + placeholder("port") + "')]",
	}}
	// Literal text may be written anywhere, so a read key in a tag is written back.
	files := consumerFiles(t, properties, map[string]any{"tags": map[string]any{"port": placeholder("port")}}, nil)
	out, err := LinkResolve(edge, published(t, edge), files)
	if err != nil {
		t.Fatalf("LinkResolve: %v", err)
	}
	want := map[string]any{
		"/resources/App/properties/settings/port":        "5432",
		"/resources/App/properties/settings/label":       "[[blue]",
		"/resources/App/properties/settings/portURL":     "[format('db:{0}', '5432')]",
		"/resources/App/properties/settings/labelText":   "[format('colour {0}', '[blue]')]",
		"/resources/App/properties/settings/ownerText":   "[format('owned by {0}', 'o''brien')]",
		"/resources/App/properties/settings/twoKeysOnce": "[format('{0}:{1}', 'o''brien', '5432')]",
		"/resources/App/tags/port":                       "5432",
	}
	got := map[string]any{}
	for _, op := range opsOf(t, out.Patches) {
		if op.Op != "replace" {
			t.Errorf("%s %s, want replace", op.Op, op.Path)
		}
		if _, dup := got[op.Path]; dup {
			t.Errorf("%s is replaced twice", op.Path)
		}
		got[op.Path] = op.Value
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("write-back:\n got %v\nwant %v", got, want)
	}
	// The whole string is the literal written for ARM, and reads back as the
	// value the datastore published.
	if ArmLiteral("[blue]") != "[[blue]" {
		t.Fatal("the label is not ARM's own escape of [blue]")
	}
}

func TestLinkResolveReadKeyAbsentFromProvidesHalts(t *testing.T) {
	t.Parallel()
	edge := testLink()
	edge.ReadProvidesKeys = []string{"replica"}
	files := consumerFiles(t, map[string]any{"replica": placeholder("replica")}, nil, nil)
	_, err := LinkResolve(edge, map[string]string{"deploymentName": linkedDeployment, "outputName.host": hostOutput}, files)
	wantCondition(t, err, plugincondition.ErrPluginRequiredInputUnresolved())
	for _, want := range []string{`"replica"`, `"orders-api"`, `"payments-db"`, `"database"`} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the halt does not name %s: %v", want, err)
		}
	}
}

func TestLinkResolveRefusesAProducerDeployedAtAWiderScope(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		scope, words string
	}{
		{"subscription", "subscription scope"},
		{"managementGroup", "management-group scope"},
	} {
		t.Run(c.scope, func(t *testing.T) {
			t.Parallel()
			edge := testLink()
			edge.TargetAnnotations = map[string]string{AnnotationDeploymentScope: c.scope, AnnotationResourceGroup: "rg-payments"}
			_, publishErr := LinkPublish(edge, producerFiles(t, nil))
			files := consumerFiles(t, map[string]any{"settings": map[string]any{"host": placeholder("host")}}, nil, nil)
			_, resolveErr := LinkResolve(edge, map[string]string{handleDeploymentName: linkedDeployment, handleOutputNamePrefix + "host": hostOutput}, files)
			for side, err := range map[string]error{"publish": publishErr, "resolve": resolveErr} {
				wantCondition(t, err, plugincondition.ErrPluginCannotExpressInput(), "rg-payments")
				for _, want := range []string{
					"arm links to a producer deployed at resource-group scope; this producer's boundary deploys at " + c.words,
					`"orders-api"`, `"payments-db"`, `"database"`, `"stack-a/arm"`,
				} {
					if !strings.Contains(err.Error(), want) {
						t.Errorf("%s: the halt does not say %q: %v", side, want, err)
					}
				}
				for _, banned := range []string{"yet", "not available"} {
					if strings.Contains(err.Error(), banned) {
						t.Errorf("%s: the halt says %q: %v", side, banned, err)
					}
				}
			}
		})
	}
	t.Run("a resource group producer is linked", func(t *testing.T) {
		t.Parallel()
		edge := testLink()
		edge.TargetAnnotations = map[string]string{AnnotationDeploymentScope: "resourceGroup"}
		if _, err := LinkPublish(edge, producerFiles(t, nil)); err != nil {
			t.Errorf("LinkPublish: %v", err)
		}
	})
}

// subscriptionConsumerFiles assembles the consumer at subscription scope: its
// application, whose settings read the host, as resource group content, and the
// given subscription content beside it.
func subscriptionConsumerFiles(t *testing.T, subscriptionContent string) []rendercore.RenderedFile {
	t.Helper()
	app := "resources:\n  App:\n    type: Microsoft.App/containerApps\n    apiVersion: '2024-03-01'\n    name: orders-api\n    properties:\n      settings:\n        host: " + placeholder("host") + "\n"
	contribs := []Contribution{subscriptionBoundary(contributes("orders-api", 0, app))}
	if subscriptionContent != "" {
		contribs = append(contribs, subscriptionBoundary(contributes("watcher", 0, subscriptionContent)))
	}
	out, err := Assemble(contribs)
	if err != nil {
		t.Fatalf("Assemble: %v", err)
	}
	return out.Files
}

func TestLinkResolveReadsInsideTheResourceGroupLevel(t *testing.T) {
	t.Parallel()
	edge := testLink()
	files := subscriptionConsumerFiles(t, "")
	out, err := LinkResolve(edge, published(t, edge), files)
	if err != nil {
		t.Fatalf("LinkResolve: %v", err)
	}
	ops := opsOf(t, out.Patches)
	path := "/resources/" + LevelResourceGroupSymbol + "/properties/template/resources/App/properties/settings/host"
	// Inside the level, resourceGroup() is the level's own resource group.
	if len(ops) != 1 || ops[0].Op != "replace" || ops[0].Path != path || ops[0].Value != "["+defaultReference+"]" {
		t.Fatalf("operations = %+v\nwant replace %s = [%s]", ops, path, defaultReference)
	}
	resolved := templateOf(t, applied(t, files, out.Patches))
	if strings.Contains(mustJSON(t, resolved), rendercore.CrossBoundaryLinkSentinelPrefix) {
		t.Errorf("a placeholder remains:\n%s", mustJSON(t, resolved))
	}
}

func TestLinkResolveAtSubscriptionScopeNeedsTheProducersResourceGroup(t *testing.T) {
	t.Parallel()
	watcher := subscriptionSchemaLine + "resources:\n  Watch:\n    type: Microsoft.Insights/activityLogAlerts\n    apiVersion: '2020-10-01'\n    name: watch\n    location: global\n    properties:\n      description: " + placeholder("host") + "\n"
	pointer := "/resources/Watch/properties/description"

	edge := testLink()
	files := subscriptionConsumerFiles(t, watcher)
	_, err := LinkResolve(edge, published(t, edge), files)
	wantCondition(t, err, plugincondition.ErrPluginRequiredInputUnresolved(), "fullyQualifiedDomainName")
	for _, want := range []string{pointer, `"host"`, `"payments-db"`, AnnotationResourceGroup, "subscription scope"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the halt does not say %q: %v", want, err)
		}
	}

	edge.TargetAnnotations = map[string]string{AnnotationResourceGroup: "rg-payments"}
	out, err := LinkResolve(edge, published(t, edge), files)
	if err != nil {
		t.Fatalf("LinkResolve with the producer's resource group: %v", err)
	}
	want := map[string]any{
		pointer: "[reference(format('/subscriptions/{0}/resourceGroups/{1}/providers/Microsoft.Resources/deployments/{2}', subscription().subscriptionId, 'rg-payments', 'aggrega--orders--stack-a-zsarm'), '2025-04-01').outputs.payments_zhdb__host.value]",
		"/resources/" + LevelResourceGroupSymbol + "/properties/template/resources/App/properties/settings/host": "[reference(format('/subscriptions/{0}/resourceGroups/{1}/providers/Microsoft.Resources/deployments/{2}', subscription().subscriptionId, 'rg-payments', 'aggrega--orders--stack-a-zsarm'), '2025-04-01').outputs.payments_zhdb__host.value]",
	}
	got := map[string]any{}
	for _, op := range opsOf(t, out.Patches) {
		got[op.Path] = op.Value
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("operations:\n got %v\nwant %v", got, want)
	}
}

// managementGroupConsumerFiles assembles the consumer at management-group scope:
// an application whose settings read the host as resource group content, and a
// watcher whose description reads it as subscription content.
func managementGroupConsumerFiles(t *testing.T) []rendercore.RenderedFile {
	t.Helper()
	app := "resources:\n  App:\n    type: Microsoft.App/containerApps\n    apiVersion: '2024-03-01'\n    name: orders-api\n    properties:\n      settings:\n        host: " + placeholder("host") + "\n"
	watcher := subscriptionSchemaLine + "resources:\n  Watch:\n    type: Microsoft.Insights/activityLogAlerts\n    apiVersion: '2020-10-01'\n    name: watch\n    location: global\n    properties:\n      description: " + placeholder("host") + "\n"
	out, err := Assemble([]Contribution{
		managementGroupBoundary(contributes("orders-api", 0, app)),
		managementGroupBoundary(contributes("watcher", 0, watcher)),
	})
	if err != nil {
		t.Fatalf("Assemble: %v", err)
	}
	return out.Files
}

func TestLinkResolveReadsInsideTheLevelsOfAManagementGroupTemplate(t *testing.T) {
	t.Parallel()
	files := managementGroupConsumerFiles(t)

	// The level a subscription template's watcher sits in has no resource group
	// of its own, so the producer's must be named.
	edge := testLink()
	_, err := LinkResolve(edge, published(t, edge), files)
	wantCondition(t, err, plugincondition.ErrPluginRequiredInputUnresolved(), "fullyQualifiedDomainName")
	for _, want := range []string{"/resources/" + LevelSubscriptionSymbol + "/properties/template/resources/Watch/properties/description", `"payments-db"`, AnnotationResourceGroup, "subscription scope"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the halt does not say %q: %v", want, err)
		}
	}

	edge.TargetAnnotations = map[string]string{AnnotationResourceGroup: "rg-payments"}
	out, err := LinkResolve(edge, published(t, edge), files)
	if err != nil {
		t.Fatalf("LinkResolve: %v", err)
	}
	read := "[reference(format('/subscriptions/{0}/resourceGroups/{1}/providers/Microsoft.Resources/deployments/{2}', subscription().subscriptionId, 'rg-payments', 'aggrega--orders--stack-a-zsarm'), '2025-04-01').outputs.payments_zhdb__host.value]"
	got := map[string]any{}
	for _, op := range opsOf(t, out.Patches) {
		got[op.Path] = op.Value
	}
	want := map[string]any{
		"/resources/" + LevelSubscriptionSymbol + "/properties/template/resources/Watch/properties/description":  read,
		"/resources/" + LevelResourceGroupSymbol + "/properties/template/resources/App/properties/settings/host": read,
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("operations:\n got %v\nwant %v", got, want)
	}
	if resolved := mustJSON(t, templateOf(t, applied(t, files, out.Patches))); strings.Contains(resolved, rendercore.CrossBoundaryLinkSentinelPrefix) {
		t.Errorf("a placeholder remains:\n%s", resolved)
	}
}

func TestLinkResolveAtManagementGroupScopeNeedsTheProducersSubscription(t *testing.T) {
	t.Parallel()
	policy := managementGroupSchemaLine + "resources:\n  Policy:\n    type: Microsoft.Authorization/policyDefinitions\n    apiVersion: '2023-04-01'\n    name: policy\n    properties:\n      description: " + placeholder("host") + "\n"
	out, err := Assemble([]Contribution{managementGroupBoundary(contributes("policy", 0, policy))})
	if err != nil {
		t.Fatalf("Assemble: %v", err)
	}
	edge := testLink()
	edge.TargetAnnotations = map[string]string{AnnotationResourceGroup: "rg-payments"}
	_, err = LinkResolve(edge, published(t, edge), out.Files)
	wantCondition(t, err, plugincondition.ErrPluginRequiredInputUnresolved(), "fullyQualifiedDomainName")
	for _, want := range []string{"/resources/Policy/properties/description", `"payments-db"`, AnnotationSubscription, "management-group scope"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the halt does not say %q: %v", want, err)
		}
	}
	edge.TargetAnnotations[AnnotationSubscription] = "sub-payments"
	if _, err := LinkResolve(edge, published(t, edge), out.Files); err != nil {
		t.Errorf("LinkResolve with the producer's subscription and resource group: %v", err)
	}
}
