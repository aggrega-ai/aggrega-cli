// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package terraformcore_test

import (
	"reflect"
	"strings"
	"testing"

	abi "github.com/aggrega-ai/aggrega-abi/go/pkg/abi"
	"github.com/aggrega-ai/aggrega-cli/plugins/internal/terraformcore"
)

// tree builds a module tree from path/content pairs, in the order given, which
// is the order the walk lists a directory's files in.
func tree(t *testing.T, pairs ...string) []terraformcore.ConfigFile {
	t.Helper()
	if len(pairs)%2 != 0 {
		t.Fatalf("tree needs path/content pairs; got %d arguments", len(pairs))
	}
	out := make([]terraformcore.ConfigFile, 0, len(pairs)/2)
	for i := 0; i < len(pairs); i += 2 {
		out = append(out, terraformcore.ConfigFile{Path: pairs[i], Content: []byte(pairs[i+1])})
	}
	return out
}

// TestFetchedModuleCalls pins which module calls the walk reports, per shape of
// tree. Each row is one scenario: the files the engine loads are the root's and
// those a local source reaches from there, so a row is red when the walk reads a
// file the engine would not load, misses one it would, misclassifies a source, or
// fails to stop at a cycle.
func TestFetchedModuleCalls(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name  string
		files []terraformcore.ConfigFile
		want  []terraformcore.FetchedModuleCall
	}{
		{
			name: "a chain of local sources is followed to the call at its end",
			files: tree(t,
				"main.tf", "module \"a\" {\n  source = \"./modules/a\"\n}\n",
				"modules/a/main.tf", "module \"b\" {\n  source = \"../b\"\n}\n",
				"modules/b/main.tf", "module \"net\" {\n  source  = \"terraform-aws-modules/vpc/aws\"\n  version = \"5.0.0\"\n}\n",
			),
			want: []terraformcore.FetchedModuleCall{
				{File: "modules/b/main.tf", Line: 2, Name: "net", Source: `"terraform-aws-modules/vpc/aws"`},
			},
		},
		{
			name: "a chain of local sources with no remote call reports nothing",
			files: tree(t,
				"main.tf", "module \"a\" {\n  source = \"./modules/a\"\n}\n",
				"modules/a/main.tf", "module \"b\" {\n  source = \"../b\"\n}\n",
				"modules/b/main.tf", "resource \"null_resource\" \"x\" {}\n",
			),
			want: nil,
		},
		{
			name: "each remote form is reported, in declaration order",
			files: tree(t,
				"main.tf", strings.Join([]string{
					"module \"registry\" {\n  source = \"terraform-aws-modules/rds/aws\"\n}",
					"module \"git\" {\n  source = \"git::https://example.com/vpc.git?ref=v1.2.0\"\n}",
					"module \"github\" {\n  source = \"github.com/hashicorp/example\"\n}",
					"module \"archive\" {\n  source = \"https://example.com/vpc-module.zip\"\n}",
					"module \"bucket\" {\n  source = \"s3::https://s3-eu-west-1.amazonaws.com/examplecorp-terraform-modules/vpc.zip\"\n}",
					"module \"gcs\" {\n  source = \"gcs::https://www.googleapis.com/storage/v1/modules/foomodule.zip\"\n}",
					"module \"absolute\" {\n  source = \"/srv/modules/vpc\"\n}",
				}, "\n")+"\n",
			),
			want: []terraformcore.FetchedModuleCall{
				{File: "main.tf", Line: 2, Name: "registry", Source: `"terraform-aws-modules/rds/aws"`},
				{File: "main.tf", Line: 5, Name: "git", Source: `"git::https://example.com/vpc.git?ref=v1.2.0"`},
				{File: "main.tf", Line: 8, Name: "github", Source: `"github.com/hashicorp/example"`},
				{File: "main.tf", Line: 11, Name: "archive", Source: `"https://example.com/vpc-module.zip"`},
				{File: "main.tf", Line: 14, Name: "bucket", Source: `"s3::https://s3-eu-west-1.amazonaws.com/examplecorp-terraform-modules/vpc.zip"`},
				{File: "main.tf", Line: 17, Name: "gcs", Source: `"gcs::https://www.googleapis.com/storage/v1/modules/foomodule.zip"`},
				{File: "main.tf", Line: 20, Name: "absolute", Source: `"/srv/modules/vpc"`},
			},
		},
		{
			name: "the JSON configuration syntax is read and followed like native syntax",
			files: tree(t,
				"main.tf.json", "{\n  \"module\": {\n    \"vpc\": {\n      \"source\": \"terraform-aws-modules/vpc/aws\"\n    },\n    \"local\": {\n      \"source\": \"./sub\"\n    }\n  }\n}\n",
				"sub/inner.tf.json", "{\n  \"module\": {\n    \"store\": {\n      \"source\": \"git::https://example.com/store.git\"\n    }\n  }\n}\n",
			),
			want: []terraformcore.FetchedModuleCall{
				{File: "main.tf.json", Line: 4, Name: "vpc", Source: `"terraform-aws-modules/vpc/aws"`},
				{File: "sub/inner.tf.json", Line: 4, Name: "store", Source: `"git::https://example.com/store.git"`},
			},
		},
		{
			name: "a cycle of local sources stops, and each directory is read once",
			files: tree(t,
				"main.tf", "module \"a\" {\n  source = \"./a\"\n}\n",
				"a/main.tf", "module \"back\" {\n  source = \"../\"\n}\nmodule \"self\" {\n  source = \"./\"\n}\nmodule \"again\" {\n  source = \"../a\"\n}\nmodule \"net\" {\n  source = \"terraform-aws-modules/vpc/aws\"\n}\n",
			),
			want: []terraformcore.FetchedModuleCall{
				{File: "a/main.tf", Line: 11, Name: "net", Source: `"terraform-aws-modules/vpc/aws"`},
			},
		},
		{
			name: "a local source climbing out of the tree is reported, and one staying inside is followed",
			files: tree(t,
				"main.tf", "module \"shared\" {\n  source = \"../shared\"\n}\nmodule \"a\" {\n  source = \"./modules/a\"\n}\n",
				"modules/a/main.tf", "module \"sibling\" {\n  source = \"../b\"\n}\nmodule \"far\" {\n  source = \"../../../elsewhere\"\n}\n",
				"modules/b/main.tf", "",
			),
			want: []terraformcore.FetchedModuleCall{
				{File: "main.tf", Line: 2, Name: "shared", Source: `"../shared"`, OutsideTree: true},
				{File: "modules/a/main.tf", Line: 5, Name: "far", Source: `"../../../elsewhere"`, OutsideTree: true},
			},
		},
		{
			name: "a remote call in a directory nothing reaches is not read",
			files: tree(t,
				"examples/basic/main.tf", "module \"db\" {\n  source = \"../../\"\n}\nmodule \"vpc\" {\n  source = \"terraform-aws-modules/vpc/aws\"\n}\n",
				"main.tf", "module \"inner\" {\n  source = \"./modules/inner\"\n}\n",
				"modules/inner/main.tf", "resource \"null_resource\" \"x\" {}\n",
				"tests/main.tftest.hcl", "run \"x\" {}\n",
			),
			want: nil,
		},
		{
			name: "a backslash local source is followed",
			files: tree(t,
				"main.tf", "module \"a\" {\n  source = \".\\\\sub\"\n}\n",
				"sub/main.tf", "module \"net\" {\n  source = \"terraform-aws-modules/vpc/aws\"\n}\n",
			),
			want: []terraformcore.FetchedModuleCall{
				{File: "sub/main.tf", Line: 2, Name: "net", Source: `"terraform-aws-modules/vpc/aws"`},
			},
		},
		{
			name: "a provider requirement's source is not a module call",
			files: tree(t,
				"versions.tf", "terraform {\n  required_providers {\n    aws = {\n      source  = \"hashicorp/aws\"\n      version = \">= 5.0\"\n    }\n  }\n}\n",
			),
			want: nil,
		},
		{
			name: "a source the engine evaluates is judged by its leading literal text",
			files: tree(t,
				"main.tf", "module \"tmpl\" {\n  source = \"git::https://example.com/${var.repo}.git\"\n}\nmodule \"ref\" {\n  source = var.path\n}\nmodule \"localtmpl\" {\n  source = \"./${var.dir}\"\n}\nmodule \"interp\" {\n  source = \"${var.whole}\"\n}\n",
			),
			want: []terraformcore.FetchedModuleCall{
				{File: "main.tf", Line: 2, Name: "tmpl", Source: `"git::https://example.com/${var.repo}.git"`},
			},
		},
		{
			name: "a JSON template is judged by its leading literal text, not read as a literal",
			files: tree(t,
				"main.tf.json", "{\n  \"module\": {\n    \"tmpl\": {\n      \"source\": \"git::https://example.com/${var.repo}.git\"\n    },\n    \"localtmpl\": {\n      \"source\": \"./${var.dir}\"\n    },\n    \"interp\": {\n      \"source\": \"${var.whole}\"\n    }\n  }\n}\n",
			),
			want: []terraformcore.FetchedModuleCall{
				{File: "main.tf.json", Line: 4, Name: "tmpl", Source: `"git::https://example.com/${var.repo}.git"`},
			},
		},
		{
			name: "a file that does not parse is read for what it parsed and does not stop the walk",
			files: tree(t,
				"broken.tf", "module \"x\" {\n  source = \n",
				"main.tf", "module \"net\" {\n  source = \"terraform-aws-modules/vpc/aws\"\n}\n",
				"README.md", "module \"doc\" { source = \"terraform-aws-modules/vpc/aws\" }\n",
			),
			want: []terraformcore.FetchedModuleCall{
				{File: "main.tf", Line: 2, Name: "net", Source: `"terraform-aws-modules/vpc/aws"`},
			},
		},
		{
			name:  "an empty tree reports nothing",
			files: nil,
			want:  nil,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var got []terraformcore.FetchedModuleCall
			func() {
				defer func() {
					if r := recover(); r != nil {
						t.Fatalf("FetchedModuleCalls panicked: %v", r)
					}
				}()
				got = terraformcore.FetchedModuleCalls(tc.files)
			}()
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("FetchedModuleCalls:\n got: %#v\nwant: %#v", got, tc.want)
			}
		})
	}
}

// TestFetchedModuleCallDiagnostic pins the warning one call raises: warn severity,
// the class code, no location of its own so the host attributes it to the node,
// and a message naming the file, the line, the block and the source as written.
func TestFetchedModuleCallDiagnostic(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name     string
		call     terraformcore.FetchedModuleCall
		contains []string
	}{
		{
			name:     "a module address",
			call:     terraformcore.FetchedModuleCall{File: "modules/b/main.tf", Line: 7, Name: "net", Source: `"terraform-aws-modules/vpc/aws"`},
			contains: []string{`module "net"`, "modules/b/main.tf", "line 7", `"terraform-aws-modules/vpc/aws"`, "downloads when the configuration is initialised"},
		},
		{
			name:     "a local path outside the tree",
			call:     terraformcore.FetchedModuleCall{File: "main.tf", Line: 2, Name: "shared", Source: `"../shared"`, OutsideTree: true},
			contains: []string{`module "shared"`, "main.tf", "line 2", `"../shared"`, "resolves outside the module this build placed"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			d := tc.call.Diagnostic()
			if d.Severity != abi.SeverityWarn {
				t.Errorf("severity = %q, want %q", d.Severity, abi.SeverityWarn)
			}
			// The literal is the published class name tooling matches on, so it is
			// pinned here rather than read back from the constant that produces it.
			if d.Code != "module-fetched-at-init" {
				t.Errorf("code = %q, want %q", d.Code, "module-fetched-at-init")
			}
			if d.Location != nil {
				t.Errorf("location = %#v, want none, so the host attributes the warning to the node", d.Location)
			}
			for _, s := range tc.contains {
				if !strings.Contains(d.Message, s) {
					t.Errorf("message %q does not contain %q", d.Message, s)
				}
			}
		})
	}
	if got := terraformcore.ModuleFetchedAtInitDiagnostics(nil); got != nil {
		t.Errorf("ModuleFetchedAtInitDiagnostics(nil) = %#v, want nil", got)
	}
}

// TestParseConfigFile pins which file names are configuration files and in which
// syntax, the one classification both the parse check and the walk read.
func TestParseConfigFile(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name     string
		file     string
		content  string
		ok       bool
		wantErrs bool
	}{
		{name: "native syntax", file: "main.tf", content: "variable \"x\" {}\n", ok: true},
		{name: "JSON syntax", file: "main.tf.json", content: "{\"variable\": {\"x\": {}}}\n", ok: true},
		{name: "native syntax that does not parse", file: "main.tf", content: "variable \"x\" {\n", ok: true, wantErrs: true},
		{name: "JSON syntax that does not parse", file: "main.tf.json", content: "{", ok: true, wantErrs: true},
		{name: "a file the engine does not load", file: "README.md", content: "# x\n", ok: false},
		{name: "a test file the engine does not load at initialisation", file: "x.tftest.hcl", content: "run \"x\" {}\n", ok: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, diags, ok := terraformcore.ParseConfigFile(tc.file, []byte(tc.content))
			if ok != tc.ok {
				t.Fatalf("ok = %v, want %v", ok, tc.ok)
			}
			if diags.HasErrors() != tc.wantErrs {
				t.Errorf("errors = %v, want %v: %v", diags.HasErrors(), tc.wantErrs, diags)
			}
		})
	}
}
