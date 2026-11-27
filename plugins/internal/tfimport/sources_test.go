// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package tfimport

import (
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/plugins/internal/cloudimport"
	"github.com/aggrega-ai/aggrega-pdk/go/pkg/aggrega"
)

// fixture returns the files of the testdata directory dir, each named by its
// slash-separated path inside dir, as a directory import hands them.
func fixture(t testing.TB, dir string) []file {
	t.Helper()
	root := filepath.Join("testdata", filepath.FromSlash(dir))
	var out []file
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		out = append(out, file{Name: filepath.ToSlash(rel), Bytes: b})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// unitPaths returns the paths of units, in order.
func unitPaths(units []cloudimport.Unit) []string {
	out := make([]string, 0, len(units))
	for _, u := range units {
		out = append(out, u.Path)
	}
	return out
}

// sourceRefused fails t unless d halts source-refused naming each of names.
func sourceRefused(t *testing.T, d *aggrega.Diagnostic, names ...string) {
	t.Helper()
	want := cloudimport.Report(cloudimport.CodeSourceRefused, "")
	if d == nil || d.Code != want.Code || d.Severity != want.Severity {
		t.Fatalf("diagnostic = %+v, want a source-refused halt", d)
	}
	for _, name := range names {
		if !strings.Contains(d.Message, name) {
			t.Fatalf("message %q does not name %s", d.Message, name)
		}
	}
}

func TestClassify(t *testing.T) {
	cases := []struct {
		name string
		want sourceKind
	}{
		{"main.tf", kindConfig},
		{"envs/prod/network.tf", kindConfig},
		{"main.tf.json", kindConfig},
		{"cdk.tf.json", kindConfig},
		{"terraform.tfvars", kindVarsAuto},
		{"terraform.tfvars.json", kindVarsAuto},
		{"envs/prod/terraform.tfvars", kindVarsAuto},
		{"shared.auto.tfvars", kindVarsAuto},
		{"shared.auto.tfvars.json", kindVarsAuto},
		{"prod.tfvars", kindVarsOther},
		{"prod.tfvars.json", kindVarsOther},
		{"envs/dev/dev.tfvars", kindVarsOther},
		{".terraform.lock.hcl", kindLock},
		{"envs/prod/.terraform.lock.hcl", kindLock},
		{".terraform/modules/modules.json", kindInstalled},
		{"envs/prod/.terraform/modules/modules.json", kindInstalled},
		{".terraform/modules/db/main.tf", kindSkipped},
		{".terraform/modules/db/terraform.tfvars", kindSkipped},
		{".terraform/modules/db/.terraform/modules/modules.json", kindSkipped},
		{".terraform/providers/registry.opentofu.org/hashicorp/aws/5.0.0/linux_amd64/terraform-provider-aws", kindSkipped},
		{".terraform/environment", kindSkipped},
		{"envs/prod/.terraform/terraform.tfstate", kindSkipped},
		{"README.md", kindOther},
		{"scripts/deploy.sh", kindOther},
		{"main.tf.bak", kindOther},
		{"terraform.tfstate", kindOther},
	}
	for _, c := range cases {
		if got := classify(c.name); got != c.want {
			t.Errorf("classify(%q) = %d, want %d", c.name, got, c.want)
		}
	}
}

// TestUnitsOnePerConfigurationDirectory forms one unit per directory holding
// configuration, its path ending in a slash, linking each directory a literal
// local module source of it reaches inside the import.
func TestUnitsOnePerConfigurationDirectory(t *testing.T) {
	files := append(fixture(t, "layout"),
		file{Name: "envs/prod/prod.tfvars", Bytes: []byte("name = \"orders\"\n")},
		file{Name: "README.md", Bytes: []byte("notes\n")})
	got := units(files)
	// SelectRoot resolves a link against the directory holding the unit's
	// own, so each link is written from there: envs/prod's
	// ../../modules/db is prod/../../modules/db, which is ../modules/db.
	want := []cloudimport.Unit{
		{Path: "envs/dev/", Links: []string{"../modules/db"}},
		{Path: "envs/prod/", Links: []string{"../modules/db"}},
		{Path: "modules/db/"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("units = %+v, want %+v", got, want)
	}

	got = units(fixture(t, "single"))
	want = []cloudimport.Unit{
		{Path: "./", Links: []string{"modules/queue"}},
		{Path: "modules/queue/"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("units = %+v, want %+v", got, want)
	}
}

// TestUnitsLinkOnlyLiteralLocalSourcesInsideTheImport links neither a
// registry address, nor a source the configuration computes, nor a path
// leaving the imported directory; two calls of one directory link it once.
func TestUnitsLinkOnlyLiteralLocalSourcesInsideTheImport(t *testing.T) {
	main := `module "a" {
  source = "./modules/a"
}
module "again" {
  source = "./modules/a"
}
module "registry" {
  source = "terraform-aws-modules/rds/aws"
}
module "computed" {
  source = "./modules/${var.name}"
}
module "outside" {
  source = "../elsewhere"
}
`
	jsonMain := `{"module": {"b": {"source": "./modules/b"}, "templated": {"source": "./modules/${var.name}"}}}`
	got := units([]file{
		{Name: "main.tf", Bytes: []byte(main)},
		{Name: "more.tf.json", Bytes: []byte(jsonMain)},
	})
	want := []cloudimport.Unit{{Path: "./", Links: []string{"modules/a", "modules/b"}}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("units = %+v, want %+v", got, want)
	}
}

func TestRootOneCandidate(t *testing.T) {
	root, others, d := selectRoot(fixture(t, "single"), cloudimport.Settings{})
	if d != nil || root.Path != "./" || len(others) != 0 {
		t.Fatalf("root = %+v, others = %+v, diagnostic = %+v", root, others, d)
	}
}

func TestRootSeveralCandidatesAndNoRootRefused(t *testing.T) {
	_, _, d := selectRoot(fixture(t, "layout"), cloudimport.Settings{})
	sourceRefused(t, d, "envs/prod", "envs/dev", "root")
	if strings.Contains(d.Message, "modules/db") {
		t.Fatalf("message %q names the called module as a candidate", d.Message)
	}
}

func TestRootSettingNamesOneCandidate(t *testing.T) {
	for _, setting := range []string{"envs/prod", "envs/prod/", "./envs/prod"} {
		root, others, d := selectRoot(fixture(t, "layout"), cloudimport.Settings{Root: setting})
		if d != nil || root.Path != "envs/prod/" || !reflect.DeepEqual(unitPaths(others), []string{"envs/dev/"}) {
			t.Fatalf("root %q: root = %+v, others = %+v, diagnostic = %+v", setting, root, others, d)
		}
	}
}

func TestRootSettingNamesTheImportedDirectory(t *testing.T) {
	root, others, d := selectRoot(fixture(t, "single"), cloudimport.Settings{Root: "."})
	if d != nil || root.Path != "./" || len(others) != 0 {
		t.Fatalf("root = %+v, others = %+v, diagnostic = %+v", root, others, d)
	}
}

func TestRootOnlyVariableFilesRefused(t *testing.T) {
	_, _, d := selectRoot(fixture(t, "varsonly"), cloudimport.Settings{})
	sourceRefused(t, d, ".tf", ".tf.json")
}
