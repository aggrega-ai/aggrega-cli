// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package errors

import (
	"fmt"
	"testing"
)

// codedFailure is a failure reporting a condition's identifiers, standing in for a
// registered identity: CodedError is a matching interface and is open, so the chain
// reads a code off it exactly as it does off a registration.
type codedFailure struct{ short string }

func (c codedFailure) Error() string          { return "the condition's sentence" }
func (c codedFailure) ErrorCode() string      { return "probe-condition" }
func (c codedFailure) ErrorShortCode() string { return c.short }

// pluginRaised is a failure a plugin's guest raised, carrying the condition it
// chose and the plugin that raised it.
type pluginRaised struct {
	codedFailure
	plugin string
}

func (p pluginRaised) ErrorRaisingPlugin() string { return p.plugin }

// TestHeadedCauseChain_WritesTheConditionsCodeIntoTheHead renders one-line
// failures and reads where the condition's short code lands: into the bracket of
// the first envelope in the chain, as the terminal's head carries it, qualified by
// the plugin that raised it where one did, and nowhere at all where the chain names
// no condition — which renders exactly as CauseChain renders it.
func TestHeadedCauseChain_WritesTheConditionsCodeIntoTheHead(t *testing.T) {
	t.Parallel()

	inner := Wrap(codedFailure{short: "E9001"}, CatalogError, SourceLocation{}, "assertions")
	for _, row := range []struct {
		name string
		err  error
		want string
	}{
		{
			name: "the first envelope's bracket carries the code",
			err:  fmt.Errorf("render: %w", Wrap(inner, CatalogError, SourceLocation{}, "rendering")),
			want: "render: [CATALOG_ERROR E9001] rendering: [CATALOG_ERROR] assertions: the condition's sentence",
		},
		{
			name: "an envelope with a source keeps it after the head",
			err:  Wrap(codedFailure{short: "E9001"}, DeveloperError, SourceLocation{File: "aggregate.yaml"}, "refused"),
			want: "[DEVELOPER_ERROR E9001] aggregate.yaml: refused: the condition's sentence",
		},
		{
			name: "a code a plugin raised is qualified by the plugin",
			err: Wrap(pluginRaised{codedFailure{short: "E9002"}, "provider/compose"}, TranslationError,
				SourceLocation{}, "rendering"),
			want: "[TRANSLATION_ERROR provider/compose/E9002] rendering: the condition's sentence",
		},
		{
			name: "a chain with no envelope opens with the code in a bracket of its own",
			err:  fmt.Errorf("stopped: %w", codedFailure{short: "E9001"}),
			want: "[E9001] stopped: the condition's sentence",
		},
		{
			name: "a chain naming no condition renders as CauseChain does",
			err:  fmt.Errorf("render: %w", Wrap(fmt.Errorf("plain"), CatalogError, SourceLocation{}, "rendering")),
			want: "render: [CATALOG_ERROR] rendering: plain",
		},
	} {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			if got := HeadedCauseChain(row.err); got != row.want {
				t.Errorf("HeadedCauseChain\n got: %s\nwant: %s", got, row.want)
			}
		})
	}
	if HeadedCauseChain(nil) != "" {
		t.Error("a nil error rendered text")
	}
}

// TestQualifiedShortCodeOf_QualifiesOnlyAPluginRaisedCode reads the token every
// surface prints for a condition: the bare code for the compiler's own, the code
// behind the plugin that raised it for a guest's, and nothing for a chain naming
// no condition.
func TestQualifiedShortCodeOf_QualifiesOnlyAPluginRaisedCode(t *testing.T) {
	t.Parallel()

	for _, row := range []struct {
		name string
		err  error
		want string
	}{
		{"the compiler's own condition", fmt.Errorf("x: %w", codedFailure{short: "E9001"}), "E9001"},
		{"a condition a plugin raised", fmt.Errorf("x: %w", pluginRaised{codedFailure{short: "E9002"}, "linter/publishing"}),
			"linter/publishing/E9002"},
		{"a plugin recorded with no code to qualify", pluginRaised{codedFailure{short: ""}, "linter/publishing"}, ""},
		{"a chain naming no condition", fmt.Errorf("plain"), ""},
	} {
		if got := QualifiedShortCodeOf(row.err); got != row.want {
			t.Errorf("%s: QualifiedShortCodeOf = %q, want %q", row.name, got, row.want)
		}
	}
}
