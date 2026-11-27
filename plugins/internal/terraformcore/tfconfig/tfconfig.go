// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

// Package tfconfig reads Terraform configuration files: which file names the
// engine loads, and the parse of one file in the syntax its name declares.
//
// The package is a leaf: it depends on HCL and on nothing else of the compiler,
// so every surface that reads a configuration file, a plugin guest included,
// calls here and the reading is decided in one place.
package tfconfig

import (
	"path"
	"strings"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	hcljson "github.com/hashicorp/hcl/v2/json"
)

// ParseConfigFile parses one Terraform configuration file in the syntax its name
// declares — a name ending in .tf.json as the JSON variant, one ending in .tf as
// native syntax — and reports false for any other name, which is not a
// configuration file the engine loads. The parse resolves nothing and evaluates
// nothing; the returned file may be nil when the bytes do not parse at all.
func ParseConfigFile(name string, content []byte) (*hcl.File, hcl.Diagnostics, bool) {
	jsonSyntax, ok := Syntax(name)
	switch {
	case !ok:
		return nil, nil, false
	case jsonSyntax:
		f, d := hcljson.Parse(content, name)
		return f, d, true
	default:
		f, d := hclsyntax.ParseConfig(content, name, hcl.Pos{Line: 1, Column: 1})
		return f, d, true
	}
}

// IsIgnoredFile reports whether the engine skips a file of a module directory
// by its name alone, whatever its extension: a name starting with ".", ending
// in "~", or starting and ending with "#", as editors name their hidden, backup
// and autosave files. The name may carry its directory; its last element is
// read.
func IsIgnoredFile(name string) bool {
	base := path.Base(name)
	return strings.HasPrefix(base, ".") || strings.HasSuffix(base, "~") ||
		strings.HasPrefix(base, "#") && strings.HasSuffix(base, "#")
}

// IsOverrideFile reports whether a configuration file name is an override
// file — override.tf, override.tf.json, or a name ending in _override.tf or
// _override.tf.json — whose blocks the engine merges into the blocks of the
// same address the module's other files declare, rather than loading them as
// blocks of their own. The name may carry its directory; its last element is
// read.
func IsOverrideFile(name string) bool {
	base := path.Base(name)
	for _, ext := range []string{".tf.json", ".tf"} {
		if stem, ok := strings.CutSuffix(base, ext); ok {
			return stem == "override" || strings.HasSuffix(stem, "_override")
		}
	}
	return false
}

// Syntax reports whether a file name is one of the configuration files the
// engine loads and, when it is, whether it is written in the JSON variant.
func Syntax(name string) (jsonSyntax, ok bool) {
	switch {
	case strings.HasSuffix(name, ".tf.json"):
		return true, true
	case strings.HasSuffix(name, ".tf"):
		return false, true
	default:
		return false, false
	}
}
