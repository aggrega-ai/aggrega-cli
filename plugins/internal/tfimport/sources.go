// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package tfimport

import (
	"path"
	"slices"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/plugins/internal/cloudimport"
	"github.com/aggrega-ai/aggrega-cli/plugins/internal/terraformcore/tfconfig"
	"github.com/aggrega-ai/aggrega-cli/plugins/internal/tfimport/tfaddr"
	"github.com/aggrega-ai/aggrega-pdk/go/pkg/aggrega"
	"github.com/hashicorp/hcl/v2"
)

// file is one source the import is handed: its slash-separated path inside
// the imported directory, or the name of the file imported on its own, and
// its bytes.
type file struct {
	Name  string
	Bytes []byte
}

// sourceKind is what a handed file is to a Terraform configuration.
type sourceKind int

// The kinds of a handed file.
const (
	// kindOther is a file Terraform does not load, a configuration file's name
	// it skips among them.
	kindOther sourceKind = iota
	// kindConfig is a configuration file, .tf or .tf.json, outside .terraform/.
	kindConfig
	// kindOverride is an override file, outside .terraform/, whose blocks
	// Terraform merges into the configuration's blocks of the same address.
	kindOverride
	// kindVarsAuto is a variable file Terraform loads on its own:
	// terraform.tfvars, terraform.tfvars.json, *.auto.tfvars and
	// *.auto.tfvars.json.
	kindVarsAuto
	// kindVarsOther is any other variable file, *.tfvars or *.tfvars.json,
	// which Terraform reads only where it is named.
	kindVarsOther
	// kindLock is the dependency lock file, .terraform.lock.hcl.
	kindLock
	// kindInstalled is the record of the modules terraform init installed,
	// .terraform/modules/modules.json beside the module that called them.
	kindInstalled
	// kindSkipped is any other file beneath a .terraform/ directory: the
	// installed modules' files and the providers terraform init fetched.
	kindSkipped
)

// dotTerraform is the directory terraform init writes into, beside the
// module it initialises.
const dotTerraform = ".terraform"

// installedRecord is the path, beneath .terraform/, of the record of the
// installed modules.
const installedRecord = "modules/modules.json"

// classify returns what the file named name is: a file beneath a .terraform/
// directory is the record of the installed modules, or skipped; any other is
// told by its name.
func classify(name string) sourceKind {
	if _, beneath, ok := underDotTerraform(name); ok {
		if beneath == installedRecord {
			return kindInstalled
		}
		return kindSkipped
	}
	base := path.Base(name)
	switch {
	case base == ".terraform.lock.hcl":
		return kindLock
	case base == "terraform.tfvars", base == "terraform.tfvars.json",
		strings.HasSuffix(base, ".auto.tfvars"), strings.HasSuffix(base, ".auto.tfvars.json"):
		return kindVarsAuto
	case strings.HasSuffix(base, ".tfvars"), strings.HasSuffix(base, ".tfvars.json"):
		return kindVarsOther
	}
	switch _, ok := tfconfig.Syntax(name); {
	case !ok, tfconfig.IsIgnoredFile(name):
		return kindOther
	case tfconfig.IsOverrideFile(name):
		return kindOverride
	}
	return kindConfig
}

// underDotTerraform splits name at its first directory named .terraform: the
// directory holding that one, "." for the imported directory, and the path
// beneath it; false where no directory of name is .terraform.
func underDotTerraform(name string) (holder, beneath string, ok bool) {
	segments := strings.Split(name, "/")
	for i, s := range segments[:len(segments)-1] {
		if s == dotTerraform {
			return path.Clean(strings.Join(segments[:i], "/")), strings.Join(segments[i+1:], "/"), true
		}
	}
	return "", "", false
}

// unitPath writes the directory dir as a unit's path, ending in a slash: the
// imported directory itself is "./".
func unitPath(dir string) string {
	return path.Clean(dir) + "/"
}

// units returns one unit for each directory holding configuration outside
// .terraform/, in path order, each linking the directories its literal local
// module sources reach inside the import, in name order, each as the source
// writes it. A unit's path is its directory's, ending in a slash
// ("envs/prod/", "./" for the imported directory).
func units(files []file) []cloudimport.Unit {
	us, _ := readUnits(files)
	return us
}

// readUnits returns the units the files form, as units does, and the importer
// kit's parse-failed for each configuration file that does not parse, whose
// links are unread, in name order.
func readUnits(files []file) ([]cloudimport.Unit, []aggrega.Diagnostic) {
	var dirs []string
	byDir := map[string][]file{}
	for _, f := range files {
		if classify(f.Name) != kindConfig {
			continue
		}
		dir := path.Dir(f.Name)
		if _, seen := byDir[dir]; !seen {
			dirs = append(dirs, dir)
		}
		byDir[dir] = append(byDir[dir], f)
	}
	slices.Sort(dirs)
	out := make([]cloudimport.Unit, 0, len(dirs))
	var refused []aggrega.Diagnostic
	for _, dir := range dirs {
		var links []string
		for _, f := range byDir[dir] {
			read, diags := readFile(f)
			if diags.HasErrors() {
				refused = append(refused, parseFailed(f, diags))
			}
			for _, b := range read.blocks {
				if b.Kind != "module" {
					continue
				}
				source, ok := moduleSource(b.Body)
				if !ok || tfaddr.Parse(source).Kind != tfaddr.Local {
					continue
				}
				if target := path.Join(dir, source); target == ".." || strings.HasPrefix(target, "../") {
					continue
				}
				links = append(links, path.Clean(source))
			}
		}
		slices.Sort(links)
		out = append(out, cloudimport.Unit{Path: unitPath(dir), Links: slices.Compact(links)})
	}
	slices.SortStableFunc(refused, func(a, b aggrega.Diagnostic) int { return strings.Compare(a.SourceRef, b.SourceRef) })
	return out, refused
}

// sourceSchema reads a module call's source.
var sourceSchema = &hcl.BodySchema{Attributes: []hcl.AttributeSchema{{Name: "source"}}}

// moduleSource returns the source a module call writes as a literal string;
// false where it writes none or computes it.
func moduleSource(body hcl.Body) (string, bool) {
	content, _, _ := body.PartialContent(sourceSchema)
	if content == nil {
		return "", false
	}
	attr, ok := content.Attributes["source"]
	if !ok {
		return "", false
	}
	return literalString(attr.Expr)
}

// selectRoot returns the directory the import starts from, the root module,
// and the other candidates, through the core's rule over the units the files
// form: one candidate, or the one the root setting names. A request holding
// no configuration file outside .terraform/ halts source-refused naming the
// files a module is. Where the rule halts and a configuration file does not
// parse, so that the links it writes are unread, the halt is the importer
// kit's parse-failed for each such file, in name order, rather than the
// rule's.
func selectRoot(files []file, s cloudimport.Settings) (cloudimport.Unit, []cloudimport.Unit, []aggrega.Diagnostic) {
	us, refused := readUnits(files)
	if len(us) == 0 {
		return cloudimport.Unit{}, nil, []aggrega.Diagnostic{cloudimport.Report(cloudimport.CodeSourceRefused,
			"the import holds no Terraform configuration file: a module is the .tf and .tf.json files of one "+
				"directory, outside .terraform/; import the directory holding the root module's .tf or .tf.json "+
				"files")}
	}
	root, others, halt := cloudimport.SelectRoot(us, s.Root)
	if halt == nil {
		return root, others, nil
	}
	if len(refused) == 0 {
		return cloudimport.Unit{}, nil, []aggrega.Diagnostic{*halt}
	}
	return cloudimport.Unit{}, nil, refused
}
