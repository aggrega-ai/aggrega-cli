// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package terraform

import (
	"github.com/aggrega-ai/aggrega-cli/plugins/internal/schemadraft"
	"github.com/aggrega-ai/aggrega-pdk/go/pkg/aggrega"
	"github.com/aggrega-ai/aggrega-pdk/go/pkg/importer"
)

// Write adapts WriteModule to schemadraft.Writer: it drafts r as a local module
// of r's type, requiring the provider the type was read from, with r's inputs
// as its variables and r's provides as its outputs. Each provides key mapped to
// no attribute is published as the fail Unmapped.Value gives, so a build halts
// on it until an author maps it, and the module declares no output for it.
func Write(r schemadraft.Resource) (importer.RecipeDraft, []aggrega.Diagnostic) {
	m := Module{Type: r.Type, Inputs: r.Inputs, Outputs: r.Provides}
	if r.Type.Provider != nil {
		m.Provider = *r.Type.Provider
	}
	d, diags := WriteModule(m)
	for _, u := range r.Unmapped {
		d.Provides = append(d.Provides, importer.Entry{Key: u.Key, Value: u.Value(r.Type)})
	}
	return d, diags
}
