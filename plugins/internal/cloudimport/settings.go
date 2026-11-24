// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package cloudimport

import (
	"encoding/json"

	"github.com/aggrega-ai/aggrega-pdk/go/pkg/aggrega"
	"github.com/aggrega-ai/aggrega-pdk/go/pkg/importer"
)

const rootSetting = "root"

// Settings are the settings the core reads. The importer kit reads its own
// settings from the same document.
type Settings struct {
	// Root is the path, inside the imported directory, of the source the
	// import starts from; empty where the setting is not set.
	Root string
}

// ReadSettings reads the root setting off the importer's settings document. A
// document that is not an object, or sets no root, leaves it empty; where the
// document sets it more than once, the last is read. A root that is not text
// halts source-refused.
func ReadSettings(raw json.RawMessage) (Settings, *aggrega.Diagnostic) {
	var value json.RawMessage
	for _, e := range importer.ObjectEntries(raw) {
		if e.Key == rootSetting {
			value = e.Value
		}
	}
	if value == nil {
		return Settings{}, nil
	}
	var read any
	if json.Unmarshal(value, &read) != nil {
		return Settings{}, rootNotText()
	}
	root, ok := read.(string)
	if !ok {
		return Settings{}, rootNotText()
	}
	return Settings{Root: root}, nil
}

// rootNotText halts on a root setting that is not text.
func rootNotText() *aggrega.Diagnostic {
	d := Report(CodeSourceRefused, "the root setting is not text: write it as the path, inside the imported "+
		"directory, of the source the import starts from")
	return &d
}
