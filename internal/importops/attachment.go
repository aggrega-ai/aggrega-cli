// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package importops

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/aggrega-ai/aggrega-abi-go/pkg/abi"

	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// AttachmentInputs resolves the consumer-scoped with-config for the importer
// serving format: it selects the plugins.importer attachment that applies to
// this format, reads that attachment's with.sources into named sources and
// marshals its with.settings, and returns only that attachment's inputs — never
// another attachment's.
//
// A workspace with no matching attachment, or a matching attachment declaring no
// with, threads nothing (nil, nil).
func AttachmentInputs(rw *workspaceconfig.ResolvedWorkspace, format string) ([]abi.ImportSource, json.RawMessage, error) {
	if rw == nil {
		return nil, nil, nil
	}
	ref, ok := selectImporterAttachment(rw.Config().Plugins.Importer, format)
	if !ok {
		return nil, nil, nil
	}
	return WithInputs(ref.With)
}

// selectImporterAttachment picks the plugins.importer attachment whose
// with-config applies to format: an attachment naming the import:<format>
// capability wins outright; otherwise a sole attachment applies (the
// single-importer shape, which names one importer without a per-format
// capability). More than one attachment with no capability match is ambiguous for
// this format, so none is selected and the importer runs with no with-config
// rather than guessing which attachment's inputs to thread — the resolution of
// WHICH importer runs is the catalog's job, this only decides whose with-config
// is threaded.
func selectImporterAttachment(refs []workspaceconfig.PluginRef, format string) (workspaceconfig.PluginRef, bool) {
	// The importer capability vocabulary ("import:<format>") is authored on the
	// attachment's capability field; it mirrors the catalog's importCapabilityPrefix.
	capability := "import:" + format
	for _, ref := range refs {
		if ref.Capability == capability {
			return ref, true
		}
	}
	if len(refs) == 1 {
		return refs[0], true
	}
	return workspaceconfig.PluginRef{}, false
}

// WithInputs builds the consumer-scoped importer inputs from one attachment's
// with-config: with.sources read into base-name-keyed, name-sorted ImportSource
// entries (live files, modeled on the governance verdict roles' source reading),
// and with.settings marshalled to a deterministic JSON document (Go's
// map-key-sorted encoding).
//
// Both are optional; a with declaring neither yields (nil, nil). It reads only
// the passed attachment's with, so one importer attachment's inputs never leak
// into another's invocation.
func WithInputs(with *workspaceconfig.PluginRefWith) ([]abi.ImportSource, json.RawMessage, error) {
	if with == nil {
		return nil, nil, nil
	}
	sources, err := importSources(with.Sources)
	if err != nil {
		return nil, nil, err
	}
	settings, err := importSettings(with.Settings)
	if err != nil {
		return nil, nil, err
	}
	return sources, settings, nil
}

// importSources reads the attachment's declared source patterns into named
// import sources. Each pattern is glob-expanded; a pattern matching no file is a
// loud miss (a misconfigured attachment, not a silent empty import). Each matched
// file's bytes are carried under its base name (never an absolute host path — for
// portability and determinism), and the result is name-sorted so the wire is
// byte-identical across runs. Returns nil when the attachment declares no sources.
func importSources(patterns []string) ([]abi.ImportSource, error) {
	if len(patterns) == 0 {
		return nil, nil
	}
	var sources []abi.ImportSource
	for _, pattern := range patterns {
		matches, err := filepath.Glob(pattern)
		if err != nil {
			return nil, fmt.Errorf("expand import source pattern %q: %w", pattern, err)
		}
		if len(matches) == 0 {
			return nil, fmt.Errorf("import source %q matched no files", pattern)
		}
		for _, m := range matches {
			content, rerr := os.ReadFile(m)
			if rerr != nil {
				return nil, fmt.Errorf("read import source %q: %w", m, rerr)
			}
			sources = append(sources, abi.ImportSource{Name: filepath.Base(m), Bytes: content})
		}
	}
	sort.Slice(sources, func(i, j int) bool { return sources[i].Name < sources[j].Name })
	return sources, nil
}

// importSettings marshals the attachment's free-form settings object to the
// opaque options document the importer reads. Go's encoding/json sorts map keys,
// so the document is byte-deterministic. Returns nil when the attachment declares
// no settings, which the host omits from the wire payload.
func importSettings(settings map[string]any) (json.RawMessage, error) {
	if len(settings) == 0 {
		return nil, nil
	}
	raw, err := json.Marshal(settings)
	if err != nil {
		return nil, fmt.Errorf("marshal import settings: %w", err)
	}
	return raw, nil
}
