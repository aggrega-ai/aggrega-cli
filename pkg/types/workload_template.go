// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package types

import (
	"errors"

	"go.yaml.in/yaml/v3"
)

// ErrWorkloadTemplateShape fires when a Recipe `workload:` content
// carries a shape outside the two documented modes: not a string
// scalar; not a mapping with a single `$import:` key; or a mapping
// with extra siblings beyond `$import:`.
var ErrWorkloadTemplateShape = errors.New("workload: shape must be a string scalar or {$import: <local-path>}")

// WorkloadTemplate carries the Recipe's authored workload-YAML producer
// with two content-shape-dispatched modes per the file-import
// vocabulary (chapter 07):
//
//   - Mode 1 (inline): a Go template string scalar whose output parses
//     as 'aggrega.ai/v1alpha1 Workload'.
//   - Mode 2 (file import): a YAML map of exactly one key `$import:
//     <local-path>` pointing at a Go-template file.
//
// Library-import mode (oci:// URL) is not supported at this site; the
// envelope schema rejects oci:// prefixes via a `not: pattern` regex
// constraint on the `$import:` value. Authors compose library-shared
// content at the MappingLibrary surface via `valuesMapping:`.
//
// Type name is deliberately WorkloadTemplate (not Workload) because
// pkg/types/workload.go's Workload type carries the rendered Workload
// YAML structure (apiVersion / kind / spec / containers / services).
// Different layers, same domain word; the suffix prevents the
// confusion.
type WorkloadTemplate struct {
	Inline string `json:"-" yaml:"-"`
	Import string `json:"-" yaml:"-"`
}

// IsEmpty reports whether neither mode is populated.
func (w WorkloadTemplate) IsEmpty() bool {
	return w.Inline == "" && w.Import == ""
}

// UnmarshalYAML dispatches via the shared unmarshalImportable helper.
func (w *WorkloadTemplate) UnmarshalYAML(node *yaml.Node) error {
	inline, importTarget, err := unmarshalImportable(node, ErrWorkloadTemplateShape)
	if err != nil {
		return err
	}
	w.Inline = inline
	w.Import = importTarget
	return nil
}

// MarshalYAML emits the populated mode via marshalImportable.
func (w WorkloadTemplate) MarshalYAML() (any, error) {
	return marshalImportable(w.Inline, w.Import)
}
