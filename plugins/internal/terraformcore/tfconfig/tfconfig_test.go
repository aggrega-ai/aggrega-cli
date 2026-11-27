// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package tfconfig_test

import (
	"testing"

	"github.com/aggrega-ai/aggrega-cli/plugins/internal/terraformcore/tfconfig"
)

// TestParseConfigFileReadsNativeAndJSON pins which file names are configuration files and in which
// syntax, the one classification both the parse check and the walk read.
func TestParseConfigFileReadsNativeAndJSON(t *testing.T) {
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
			_, diags, ok := tfconfig.ParseConfigFile(tc.file, []byte(tc.content))
			if ok != tc.ok {
				t.Fatalf("ok = %v, want %v", ok, tc.ok)
			}
			if diags.HasErrors() != tc.wantErrs {
				t.Errorf("errors = %v, want %v: %v", diags.HasErrors(), tc.wantErrs, diags)
			}
		})
	}
}

// TestSyntaxNamesOnlyConfigurationFiles pins that only a .tf or .tf.json name is a
// configuration file, and which of the two is the JSON variant.
func TestSyntaxNamesOnlyConfigurationFiles(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name     string
		wantJSON bool
		wantOK   bool
	}{
		{name: "main.tf", wantOK: true},
		{name: "x.tf.json", wantJSON: true, wantOK: true},
		{name: "terraform.tfvars"},
	} {
		gotJSON, gotOK := tfconfig.Syntax(tc.name)
		if gotJSON != tc.wantJSON || gotOK != tc.wantOK {
			t.Errorf("Syntax(%q) = (%v, %v), want (%v, %v)", tc.name, gotJSON, gotOK, tc.wantJSON, tc.wantOK)
		}
	}
}
