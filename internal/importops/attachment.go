// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

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

// attachmentURIs returns the uri of each plugins.importer attachment, in list
// order — the form the importer resolution reads the attachments in, so the
// position it reports back indexes refs directly.
func attachmentURIs(refs []workspaceconfig.PluginRef) []string {
	if len(refs) == 0 {
		return nil
	}
	uris := make([]string, len(refs))
	for i, ref := range refs {
		uris[i] = ref.URI
	}
	return uris
}

// attachedSource is one file an attachment's with.sources names: the named source
// the importer is handed, and the path the file was read from, which a refusal
// names it by.
type attachedSource struct {
	source abi.ImportSource
	path   string
}

// attachmentInputs returns the with-config the attachment at position at in refs
// hands the importer it attaches: that attachment's with.sources read into named
// sources and its with.settings marshalled, and never another attachment's.
//
// at is the position the importer resolution reported, and it is the only
// thing that names the attachment: there is no fallback to a sole attachment
// and no reading of the requested format here, so an attachment naming a
// different importer, or naming nothing the catalog holds, hands nothing to
// this import. A negative at — no attachment attaches the resolved importer —
// threads nothing (nil, nil), as does an attachment declaring no with. A
// position past the end of refs names no attachment the workspace declares, so it
// is refused rather than read as none.
func attachmentInputs(refs []workspaceconfig.PluginRef, at int) ([]attachedSource, json.RawMessage, error) {
	if at < 0 {
		return nil, nil, nil
	}
	if at >= len(refs) {
		return nil, nil, fmt.Errorf("internal: the importer resolution named importer attachment %d, but the "+
			"workspace declares %d", at, len(refs))
	}
	return withInputs(refs[at].With)
}

// withInputs builds the consumer-scoped importer inputs from one attachment's
// with-config: with.sources read into base-name-keyed, name-sorted named sources,
// each beside the path it was read from (live files, modeled on the governance
// verdict roles' source reading),
// and with.settings marshalled to a deterministic JSON document (Go's
// map-key-sorted encoding).
//
// Both are optional; a with declaring neither yields (nil, nil). It reads only
// the passed attachment's with, so one importer attachment's inputs never leak
// into another's invocation.
func withInputs(with *workspaceconfig.PluginRefWith) ([]attachedSource, json.RawMessage, error) {
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
// portability and determinism), beside the path it was matched at, and the result
// is name-sorted so the wire is byte-identical across runs. Returns nil when the
// attachment declares no sources.
func importSources(patterns []string) ([]attachedSource, error) {
	if len(patterns) == 0 {
		return nil, nil
	}
	var sources []attachedSource
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
			sources = append(sources, attachedSource{
				source: abi.ImportSource{Name: filepath.Base(m), Bytes: content},
				path:   m,
			})
		}
	}
	sort.Slice(sources, func(i, j int) bool { return sources[i].source.Name < sources[j].source.Name })
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
