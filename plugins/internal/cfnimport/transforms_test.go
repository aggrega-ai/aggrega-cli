// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package cfnimport

import (
	"reflect"
	"slices"
	"testing"

	"github.com/aggrega-ai/aggrega-pdk/go/pkg/aggrega"
)

// refusedFix is what a refused transform's halt says to do.
const refusedFix = "import the processed template CloudFormation writes for the deployed stack (aws cloudformation " +
	"get-template --template-stage Processed), or write into the template what the transform writes"

// refused is the halt on the template file at the JSON pointer at.
func refused(file, at, message string) aggrega.Diagnostic {
	return aggrega.Diagnostic{Severity: aggrega.SeverityHalt, Code: "source-refused", Message: message,
		SourceRef: file + "#" + at}
}

// transformed is a template declaring transform, written as YAML, holding
// one queue.
func transformed(transform string) string {
	return "AWSTemplateFormatVersion: \"2010-09-09\"\nTransform: " + transform +
		"\nResources:\n  Queue: {Type: AWS::SQS::Queue}\n"
}

// TestTransformsTheImportReadsAreNamed reads a template under each transform
// whose template the import reads as written, the transform named in the
// notes.
func TestTransformsTheImportReadsAreNamed(t *testing.T) {
	t.Parallel()
	names := []string{"AWS::LanguageExtensions", "AWS::SecretsManager-2020-07-23", "AWS::SecretsManager-2024-09-16",
		"AWS::ServiceCatalog", "AWS::Serverless-2016-10-31"}
	for _, name := range names {
		g, diags := readNested("", src("template.yaml", []byte(transformed(name))))
		noDiagnostics(t, diags)
		if want := []string{"The template declares the transform " + name + "."}; !slices.Equal(g.Notes, want) {
			t.Errorf("the notes read %q, want %q", g.Notes, want)
		}
	}
	g, diags := readNested("", src("template.yaml", []byte(transformed("[AWS::LanguageExtensions, "+
		"AWS::SecretsManager-2024-09-16, AWS::Serverless-2016-10-31]"))))
	noDiagnostics(t, diags)
	if want := []string{"The template declares the transforms AWS::LanguageExtensions, AWS::SecretsManager-2024-09-16, " +
		"AWS::Serverless-2016-10-31."}; !slices.Equal(g.Notes, want) {
		t.Errorf("the notes read %q, want %q", g.Notes, want)
	}
}

// TestTransformsTheImportCannotReadAreRefused halts source-refused on a
// template declaring AWS::Include, AWS::CodeDeployBlueGreen or a macro of the
// account, naming the transform, beside a transform the import reads, and in a
// template a nested stack deploys.
func TestTransformsTheImportCannotReadAreRefused(t *testing.T) {
	t.Parallel()
	include := "inserts a template fragment it reads from Amazon S3 when the stack deploys"
	for name, c := range map[string]struct {
		transform, at, says string
	}{
		"AWS::Include": {"{Name: AWS::Include, Parameters: {Location: 's3://bucket/fragment.yaml'}}", "/Transform",
			"the transform AWS::Include, which " + include},
		"AWS::CodeDeployBlueGreen": {"[AWS::Serverless-2016-10-31, AWS::CodeDeployBlueGreen]", "/Transform/1",
			"the transform AWS::CodeDeployBlueGreen, which rewrites the template to deploy Amazon ECS services " +
				"blue/green through AWS CodeDeploy when the stack deploys"},
		"a macro": {"OrdersMacro", "/Transform", "the transform OrdersMacro, a macro of the account, which rewrites " +
			"the template when the stack deploys"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			g, diags := readNested("", src("template.yaml", []byte(transformed(c.transform))))
			onlyDiagnostic(t, diags, refused("template.yaml", c.at, "the template template.yaml declares "+c.says+
				", so the import cannot read the template CloudFormation deploys: "+refusedFix))
			if g != nil {
				t.Errorf("a refused read gives a graph")
			}
		})
	}
	t.Run("in a nested stack's template", func(t *testing.T) {
		t.Parallel()
		main := "AWSTemplateFormatVersion: \"2010-09-09\"\nResources:\n  Network:\n    Type: AWS::CloudFormation::Stack\n" +
			"    Properties: {TemplateURL: net.yaml}\n"
		_, diags := readNested("", src("main.yaml", []byte(main)), src("net.yaml", []byte(transformed("OrdersMacro"))))
		onlyDiagnostic(t, diags, refused("net.yaml", "/Transform", "the template net.yaml declares the transform "+
			"OrdersMacro, a macro of the account, which rewrites the template when the stack deploys, so the import "+
			"cannot read the template CloudFormation deploys: "+refusedFix))
	})
}

// TestTransformFunctionIsRefused halts source-refused on Fn::Transform,
// naming the transform it calls and where: AWS::Include written as a member
// beside others, and a macro written as a value in its short form.
func TestTransformFunctionIsRefused(t *testing.T) {
	t.Parallel()
	for name, c := range map[string]struct {
		properties, at, says string
	}{
		"AWS::Include": {"\n      'Fn::Transform': {Name: AWS::Include, Parameters: {Location: 's3://b/f.yaml'}}" +
			"\n      VisibilityTimeout: 30",
			"/Resources/Queue/Properties/Fn::Transform", "the transform AWS::Include with Fn::Transform at " +
				"/Resources/Queue/Properties/Fn::Transform, which inserts a template fragment it reads from Amazon S3 " +
				"when the stack deploys"},
		"a macro": {"\n      Tags: !Transform {Name: TagMacro, Parameters: {}}",
			"/Resources/Queue/Properties/Tags/Fn::Transform", "the transform TagMacro with Fn::Transform at " +
				"/Resources/Queue/Properties/Tags/Fn::Transform, a macro of the account, which rewrites the template " +
				"when the stack deploys"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			text := "AWSTemplateFormatVersion: \"2010-09-09\"\nResources:\n  Queue:\n    Type: AWS::SQS::Queue\n" +
				"    Properties:" + c.properties + "\n"
			g, diags := readNested("", src("template.yaml", []byte(text)))
			onlyDiagnostic(t, diags, refused("template.yaml", c.at, "the template template.yaml calls "+c.says+
				", so the import cannot read the template CloudFormation deploys: "+refusedFix))
			if g != nil {
				t.Errorf("a refused read gives a graph")
			}
		})
	}
}

// TestForEachKeyIsRefused halts source-refused on each Fn::ForEach loop of a
// template read under AWS::LanguageExtensions, naming it where it is written,
// in the order the template writes its sections.
func TestForEachKeyIsRefused(t *testing.T) {
	t.Parallel()
	text := `AWSTemplateFormatVersion: "2010-09-09"
Transform: AWS::LanguageExtensions
Resources:
  Fn::ForEach::Queues:
    - Name
    - [Orders, Payments]
    - '${Name}Queue': {Type: AWS::SQS::Queue}
  Table: {Type: AWS::DynamoDB::Table}
Outputs:
  Fn::ForEach::Urls:
    - Name
    - [Orders, Payments]
    - '${Name}Url': {Value: !Ref Table}
`
	loop := func(name, at string) aggrega.Diagnostic {
		return refused("template.yaml", at, "the template template.yaml writes the loop "+name+" of the transform "+
			"AWS::LanguageExtensions at "+at+", which generates what it writes from a collection when the stack "+
			"deploys, so the import cannot read the template CloudFormation deploys: "+refusedFix)
	}
	g, diags := readNested("", src("template.yaml", []byte(text)))
	want := []aggrega.Diagnostic{loop("Fn::ForEach::Urls", "/Outputs/Fn::ForEach::Urls"),
		loop("Fn::ForEach::Queues", "/Resources/Fn::ForEach::Queues")}
	if !reflect.DeepEqual(diags, want) {
		t.Fatalf("the read reports\n%+v\nwant\n%+v", diags, want)
	}
	if g != nil {
		t.Errorf("a refused read gives a graph")
	}
}
