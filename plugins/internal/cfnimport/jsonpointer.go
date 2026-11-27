// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package cfnimport

import (
	"strings"

	"github.com/aggrega-ai/aggrega-pdk/go/pkg/importer"
)

// jsonPointer is [importer.Pointer] for the reader's hot paths, which name every
// node of a template: a token holding neither "~" nor "/" needs no escaping and
// is written as it is, so the escaping the plugin kit builds a table for on
// each call is built only for the token that needs it. The pointer it writes is
// the one the plugin kit writes.
func jsonPointer(tokens ...string) string {
	var b strings.Builder
	for _, t := range tokens {
		if strings.ContainsAny(t, "~/") {
			b.WriteString(importer.Pointer(t))
			continue
		}
		b.WriteString("/")
		b.WriteString(t)
	}
	return b.String()
}
