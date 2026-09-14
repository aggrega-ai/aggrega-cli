// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package validateops

import (
	"context"
	"fmt"
	"os"

	"github.com/aggrega-ai/aggrega-cli/internal/wasm"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"github.com/aggrega-ai/aggrega-cli/pkg/validator/envelope"

	"go.yaml.in/yaml/v3"
)

// PluginBinaryResult is the structured outcome of plugin-binary validation.
//
// Diagnostics is the ordered report — the identity header (plugin path and
// declared type in binary-only mode; entity path and kind in full mode), the
// per-check status lines, and the advisory notice lines. Notices repeats the
// advisory notices as bare strings for a caller that summarizes rather than
// prints. Binary reports which mode ran: binary-only checks a raw .wasm against
// its own self-report; full mode additionally cross-checks the binary against a
// Plugin entity's declared surface.
type PluginBinaryResult struct {
	Valid        bool
	Binary       bool
	PluginPath   string
	DeclaredType string
	EntityPath   string
	Kind         string
	Diagnostics  []string
	Notices      []string
}

// ValidatePluginBinary runs the plugin scope: it validates a raw .wasm against
// its own describe() self-report and, when a Plugin entity path is supplied,
// additionally cross-checks the binary against the entity's declared surface.
// The check is read-only. It returns the first blocking error alongside the
// partial result accumulated up to that point.
//
// Two modes select on entityPath:
//
//   - Binary-only (entityPath == ""): declaredType is required and is checked
//     against the binary's self-reported plugin type. No entity surface is read.
//   - Full (entityPath != ""): the file must be a Plugin entity. It is schema-
//     gated, then the binary is cross-checked against the entity's declared
//     capability claim, command mirror, and host-function record through the one
//     publish validation door, so `validate` and publish apply the identical
//     check. The declared type comes from the entity; a declaredType that
//     disagrees halts, since a Plugin carries a single declared type.
//
// maxMemoryPages is the memory cap applied when the entity declares none (or in
// binary-only mode), supplied by the caller.
//
// ctx is the caller's own, carried to the guest validation below. A guest call
// whose context ends unwinds rather than being noticed only once the guest next
// returns, so ending the caller's run ends the load and the describe it drives.
func ValidatePluginBinary(ctx context.Context, pluginPath, declaredType, entityPath string, maxMemoryPages uint32) (PluginBinaryResult, error) {
	var res PluginBinaryResult
	wasmBytes, err := os.ReadFile(pluginPath)
	if err != nil {
		return res, fmt.Errorf("read .wasm %q: %w", pluginPath, err)
	}

	if entityPath == "" {
		res.Binary = true
		if declaredType == "" {
			return res, cerrors.Unidentified(cerrors.OpsError, cerrors.SourceLocation{},
				"binary-only plugin validation needs a declared type; pass --type <plugin-type>, or add a Plugin entity argument to take the type from the entity",
				nil, nil)
		}
		res.PluginPath = pluginPath
		res.DeclaredType = declaredType
		res.Diagnostics = append(res.Diagnostics, "Plugin: "+pluginPath, "Declared type: "+declaredType)
		if _, err := wasm.ValidatePlugin(ctx, wasmBytes, wasm.ValidateOptions{
			Identity:       pluginPath,
			DeclaredType:   declaredType,
			MaxMemoryPages: maxMemoryPages,
		}); err != nil {
			return res, err
		}
		res.Diagnostics = append(res.Diagnostics, "Describe: valid", "Type: valid")
		res.Valid = true
		return res, nil
	}

	data, err := os.ReadFile(entityPath)
	if err != nil {
		return res, fmt.Errorf("read %q: %w", entityPath, err)
	}
	var preamble struct {
		Kind string `yaml:"kind"`
	}
	if err := yaml.Unmarshal(data, &preamble); err != nil {
		return res, fmt.Errorf("parse YAML preamble in %q: %w", entityPath, err)
	}
	kind, ok := knownEntityKind(preamble.Kind)
	if !ok || kind != envelope.KindPlugin {
		return res, fmt.Errorf("--plugin cross-checks a Plugin entity, but %q declares kind %q", entityPath, preamble.Kind)
	}

	res.EntityPath = entityPath
	res.Kind = string(kind)
	res.Diagnostics = append(res.Diagnostics, "Entity: "+entityPath, "Kind:   "+string(kind))

	if _, verr := envelope.ValidateBytes(kind, data, entityPath); verr != nil {
		return res, fmt.Errorf("schema: %w", verr)
	}
	res.Diagnostics = append(res.Diagnostics, "Schema: valid")

	var entity types.Plugin
	if err := yaml.Unmarshal(data, &entity); err != nil {
		return res, fmt.Errorf("parse Plugin at %q: %w", entityPath, err)
	}
	// One declared type: the entity's metadata.pluginType is authoritative, so a
	// declaredType that disagrees is an authoring contradiction, not a second
	// source.
	if declaredType != "" && declaredType != entity.Metadata.PluginType {
		return res, cerrors.Unidentified(cerrors.OpsError, cerrors.SourceLocation{},
			fmt.Sprintf("--type %q disagrees with the entity's metadata.pluginType %q; a Plugin has one declared type — drop --type or fix the entity",
				declaredType, entity.Metadata.PluginType),
			nil, nil)
	}

	// The same entity → EntityInputs mapping the publish path builds, so validate
	// and publish can never apply a divergent cross-check. An entity declaring no
	// runtime block falls back to the describe-time memory headroom exactly as
	// publish does.
	maxPages := maxMemoryPages
	if entity.Spec.Runtime != nil && entity.Spec.Runtime.MaxMemoryPages != 0 {
		maxPages = entity.Spec.Runtime.MaxMemoryPages
	}
	report, err := wasm.ValidatePlugin(ctx, wasmBytes, wasm.ValidateOptions{
		Identity:       entity.Metadata.Name + ":" + entity.Metadata.Version,
		DeclaredType:   entity.Metadata.PluginType,
		MaxMemoryPages: maxPages,
		Entity:         resolver.PluginEntityInputs(entity),
	})
	if err != nil {
		return res, err
	}
	res.Diagnostics = append(res.Diagnostics, "Plugin: valid")
	// Advisory only, exactly as publish reports: the binary offers more than the
	// entity curates. Under-declaration is legitimate and never fails the check.
	for _, notice := range report.Notices {
		res.Notices = append(res.Notices, notice)
		res.Diagnostics = append(res.Diagnostics, "  notice: "+notice)
	}
	res.Valid = true
	return res, nil
}
