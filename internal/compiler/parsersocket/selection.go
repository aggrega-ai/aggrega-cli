// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package parsersocket

import "strings"

// extensionFormat is one entry in the fixed extension→format table: an artifact
// path ending in Suffix is read as Format. The table is declarative, not
// inference — it maps a known file extension to the format that reads it; it
// never inspects an artifact's content to guess.
type extensionFormat struct {
	Suffix string
	Format string
}

// extensionFormats is the fixed, deterministic extension→format fallback table.
// It is evaluated in order, so a compound suffix (.tf.json) is listed before the
// shorter suffix it contains and wins the match. The set is closed: an extension
// absent from the table yields no format, and the caller decides what to do
// rather than the host guessing. New formats extend the table alongside the
// parser plugin that advertises them.
var extensionFormats = []extensionFormat{
	{Suffix: ".tf.json", Format: "json"},
	{Suffix: ".tf", Format: "hcl"},
	{Suffix: ".toml", Format: "toml"},
	{Suffix: ".yaml", Format: "yaml"},
	{Suffix: ".yml", Format: "yaml"},
	{Suffix: ".json", Format: "json"},
}

// FormatForExtension maps an artifact path's extension to the parser format that
// reads it, deterministically. It matches the fixed extension→format table in
// order — a compound suffix wins over the shorter suffix it contains — and
// returns ("", false) for a path whose extension is not in the table. It never
// inspects content; the extension alone selects the format.
func FormatForExtension(path string) (string, bool) {
	for _, ef := range extensionFormats {
		if strings.HasSuffix(path, ef.Suffix) {
			return ef.Format, true
		}
	}
	return "", false
}

// SelectFormat resolves the format for a source through the two declarative
// selection paths, in precedence order: an explicit format the consumer named
// wins outright; otherwise the fixed extension→format fallback applies to the
// artifact path. It returns ("", false) when neither yields a format — an
// unnamed format on a path with an unmapped extension — leaving the choice to
// the caller. There is no third path: the host never inspects an artifact's
// content to guess a format.
func SelectFormat(explicit, path string) (string, bool) {
	if explicit != "" {
		return explicit, true
	}
	return FormatForExtension(path)
}
