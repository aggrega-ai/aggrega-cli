// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package cfnimport

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/plugins/internal/cloudimport"
	"github.com/aggrega-ai/aggrega-cli/plugins/parsercore"
	"github.com/aggrega-ai/aggrega-pdk/go/pkg/aggrega"
)

// parse reads a source as the host's JSON parser does, and refuses it as the
// import socket hands an importer a parser's refusal: the message of its
// halting diagnostic. A request for another syntax fails, so the reader asking
// the host's parser for a template is caught.
func parse(syntax string, source []byte) (json.RawMessage, error) {
	if syntax != "json" {
		return nil, fmt.Errorf("the reader asked for the %q syntax", syntax)
	}
	out := parsercore.ParseJSON(source)
	for _, d := range out.Diagnostics {
		return nil, errors.New(d.Message)
	}
	return out.Tree, nil
}

// fixture returns the bytes of a file under testdata.
func fixture(t testing.TB, name string) []byte {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", filepath.FromSlash(name)))
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// src is a named source of a request.
func src(name string, body []byte) aggrega.ImportSource {
	return aggrega.ImportSource{Name: name, Bytes: body}
}

// fixtures returns the files named under the testdata directory dir, each
// named by its path inside dir, as the host names the files of a directory.
func fixtures(t testing.TB, dir string, names ...string) []aggrega.ImportSource {
	t.Helper()
	out := make([]aggrega.ImportSource, len(names))
	for i, name := range names {
		out[i] = src(name, fixture(t, dir+"/"+name))
	}
	return out
}

// readAll reads sources with the root setting root.
func readAll(root string, sources ...aggrega.ImportSource) (*cloudimport.Graph, []aggrega.Diagnostic) {
	return read(aggrega.ImportInput{Format: formatName, Sources: sources}, cloudimport.Settings{Root: root}, parse)
}

// lit is the literal leaf holding the JSON text s.
func lit(s string) cloudimport.Literal {
	var b bytes.Buffer
	if err := json.Compact(&b, []byte(s)); err != nil {
		panic(err)
	}
	return cloudimport.Literal{JSON: b.Bytes()}
}

// node returns the node of g whose ID is id, failing where there is none.
func node(t *testing.T, g *cloudimport.Graph, id string) *cloudimport.Node {
	t.Helper()
	if g == nil {
		t.Fatalf("no graph holds %s", id)
	}
	for _, n := range g.Nodes {
		if n.ID == id {
			return n
		}
	}
	t.Fatalf("the graph holds no node %s", id)
	return nil
}

// nodeIDs returns the ID of each node of g, in order.
func nodeIDs(g *cloudimport.Graph) []string {
	var out []string
	for _, n := range g.Nodes {
		out = append(out, n.ID)
	}
	return out
}

// valueAt returns the value of the property of body at path, failing where
// body holds none.
func valueAt(t *testing.T, body []cloudimport.Prop, path ...string) cloudimport.Value {
	t.Helper()
	props := body
	for i, seg := range path {
		found := false
		for _, p := range props {
			if p.Name != seg {
				continue
			}
			if i == len(path)-1 {
				return p.Value
			}
			props, found = p.Props, true
			break
		}
		if !found {
			t.Fatalf("the body holds no property %v", path[:i+1])
		}
	}
	t.Fatalf("the body holds no property %v", path)
	return nil
}

// noDiagnostics fails where diags holds any diagnostic.
func noDiagnostics(t *testing.T, diags []aggrega.Diagnostic) {
	t.Helper()
	if len(diags) > 0 {
		t.Fatalf("the read reports %+v, want nothing", diags)
	}
}

// onlyDiagnostic fails unless diags is the one diagnostic want.
func onlyDiagnostic(t *testing.T, diags []aggrega.Diagnostic, want aggrega.Diagnostic) {
	t.Helper()
	if !reflect.DeepEqual(diags, []aggrega.Diagnostic{want}) {
		t.Fatalf("the read reports %+v\nwant only %+v", diags, want)
	}
}

// TestTemplateValuesComeFromTheLeafReader hands the reader a parse that returns
// the YAML parser's tree for any syntax, in which the short form !Ref Db reads
// as the text Db: the node's property is still the long form the template
// reader writes, so no value of a template is taken from the parser socket's
// tree.
func TestTemplateValuesComeFromTheLeafReader(t *testing.T) {
	t.Parallel()
	yamlTree := func(_ string, source []byte) (json.RawMessage, error) {
		out := parsercore.ParseYAML(source)
		for _, d := range out.Diagnostics {
			return nil, errors.New(d.Message)
		}
		return out.Tree, nil
	}
	body := fixture(t, "values/template.yaml")

	tree, err := yamlTree("yaml", body)
	if err != nil {
		t.Fatal(err)
	}
	var parsed struct {
		Resources struct {
			Api struct {
				Properties struct{ DbHost any }
			}
		}
	}
	if err := json.Unmarshal(tree, &parsed); err != nil {
		t.Fatal(err)
	}
	if parsed.Resources.Api.Properties.DbHost != "Db" {
		t.Fatalf("the YAML parser reads !Ref Db as %#v, want the text Db", parsed.Resources.Api.Properties.DbHost)
	}

	g, diags := read(aggrega.ImportInput{Format: formatName, Sources: []aggrega.ImportSource{src("template.yaml", body)}},
		cloudimport.Settings{}, yamlTree)
	noDiagnostics(t, diags)
	if got, want := valueAt(t, node(t, g, "Api").Body, "Properties", "DbHost"), lit(`{"Ref": "Db"}`); !reflect.DeepEqual(got,
		want) {
		t.Errorf("the property DbHost reads %#v, want %#v", got, want)
	}
}

// TestParseFailureHaltsNamingTheTemplate halts parse-failed, naming the file,
// on a source the template reader refuses where it is a template the import
// starts from or reads: the one source handed, the source the root setting
// names, a source whose text holds AWSTemplateFormatVersion, and one a
// template the import reads links.
func TestParseFailureHaltsNamingTheTemplate(t *testing.T) {
	t.Parallel()
	refused := fixture(t, "refused/template.yaml")
	// failed is the halt naming the source name, which breaks off at line.
	failed := func(name, line string) aggrega.Diagnostic {
		return aggrega.Diagnostic{Severity: aggrega.SeverityHalt, Code: "parse-failed",
			Message: `could not parse source "` + name + `" as cloudformation: invalid YAML template: yaml: line ` +
				line + `: did not find expected node content`, SourceRef: name}
	}
	refusedChild := bytes.ReplaceAll(refused, []byte(`AWSTemplateFormatVersion: "2010-09-09"`+"\n"), nil)
	linking := []byte("AWSTemplateFormatVersion: \"2010-09-09\"\nResources:\n  Child:\n" +
		"    Type: AWS::CloudFormation::Stack\n    Properties:\n      TemplateURL: ./child.yaml\n")
	queue := fixture(t, "linked/shared/queue.yaml")

	t.Run("handed alone", func(t *testing.T) {
		t.Parallel()
		g, diags := readAll("", src("template.yaml", refused))
		onlyDiagnostic(t, diags, failed("template.yaml", "5"))
		if g != nil {
			t.Errorf("a halted read gives a graph")
		}
	})
	t.Run("imported on its own", func(t *testing.T) {
		t.Parallel()
		_, diags := read(aggrega.ImportInput{Format: formatName, Name: "stack.yaml", Bytes: refused},
			cloudimport.Settings{}, parse)
		onlyDiagnostic(t, diags, failed("stack.yaml", "5"))
	})
	t.Run("named by the root setting", func(t *testing.T) {
		t.Parallel()
		_, diags := readAll("child.yaml", src("child.yaml", refusedChild), src("queue.yaml", queue))
		onlyDiagnostic(t, diags, failed("child.yaml", "4"))
	})
	t.Run("holding AWSTemplateFormatVersion", func(t *testing.T) {
		t.Parallel()
		_, diags := readAll("", src("template.yaml", refused), src("queue.yaml", queue))
		onlyDiagnostic(t, diags, failed("template.yaml", "5"))
	})
	t.Run("linked by a template", func(t *testing.T) {
		t.Parallel()
		_, diags := readAll("", src("main.yaml", linking), src("child.yaml", refusedChild))
		onlyDiagnostic(t, diags, failed("child.yaml", "4"))
	})
}
