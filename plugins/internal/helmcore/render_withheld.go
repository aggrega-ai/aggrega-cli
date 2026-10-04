// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package helmcore

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/rendercore"
	"github.com/aggrega-ai/aggrega-cli/internal/redact"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"helm.sh/helm/v3/pkg/chart"
)

// ChartLoader loads a fresh copy of the chart a render reads. A render prunes the
// dependency charts its values disable from the chart it is given, so a second
// render of the same node needs a chart of its own.
type ChartLoader func() (*chart.Chart, error)

// RenderChartWithholding renders a chart exactly as [RenderLoadedChart] does, and
// keeps out of a failure's text every value the node's input marks sensitive.
//
// marked is the node's sensitivity marking over its values: the keys of values the
// render input names as carrying a datum an author declared sensitive. A render
// that succeeds, and a render whose values carry no marked key, are
// RenderLoadedChart's own — one run, its output, its failure, byte for byte.
//
// A render that fails while its values carry a marked key is run again on a fresh
// chart, every marked value replaced by the placeholder. When that run fails the
// same way — at the same stage, for the same registered condition, and at the same
// template position or on the same values-schema findings — its failure is the
// report: a chart's own sentence survives, with the placeholder where the value
// stood. When it does not — the chart took another branch, or failed elsewhere,
// because what it did depended on what the value holds — the report names the node
// and, where Helm or the schema states one, where the render failed, and says the
// failure text is withheld.
//
// Once a marked value is in play the text reported is never the first run's: it is
// a run that never saw a marked value, or a sentence composed of identities that
// never quotes one. The comparison only decides which of the two a reader gets, so
// an imprecise comparison costs a reader the chart's own sentence and never prints
// a value.
func RenderChartWithholding(
	ctx context.Context,
	load ChartLoader,
	values map[string]any,
	nodeCtx rendercore.NodeRenderContext,
	validate ValuesValidator,
	marked map[string]string,
) (map[string][]byte, error) {
	ch, err := load()
	if err != nil {
		return nil, err
	}
	out, err := RenderLoadedChart(ctx, ch, values, nodeCtx, validate)
	if err == nil {
		return out, nil
	}
	substituted, ok := substituteMarked(values, marked)
	if !ok {
		return nil, err
	}
	again, loadErr := load()
	if loadErr != nil {
		return nil, withheldChartFailure(err, nodeCtx.NodeID)
	}
	if _, againErr := RenderLoadedChart(ctx, again, substituted, nodeCtx, validate); againErr != nil && sameChartFailure(err, againErr) {
		return nil, againErr
	}
	return nil, withheldChartFailure(err, nodeCtx.NodeID)
}

// substituteMarked returns a copy of values with every marked key's value replaced
// by the placeholder, and whether any marked key was present to replace. values is
// never written to.
func substituteMarked(values map[string]any, marked map[string]string) (map[string]any, bool) {
	var out map[string]any
	for key := range marked {
		if _, present := values[key]; !present {
			continue
		}
		if out == nil {
			out = make(map[string]any, len(values))
			for k, v := range values {
				out[k] = v
			}
		}
		out[key] = redact.Placeholder()
	}
	return out, out != nil
}

// chartFailureWithheld is the sentence a chart render's failure carries in place
// of its own text when that text depends on a value the node's input marks
// sensitive.
const chartFailureWithheld = "failure text withheld: the chart's failure depends on a value this node's input marks sensitive"

// sameChartFailure reports whether two failed renders of one node failed the same
// way: the same stage, the same registered condition (or none in both), and then,
// by what the stage reports, the same values-schema findings, the same template
// position, or the same text.
func sameChartFailure(first, second error) bool {
	firstStage, firstCause := chartFailureStage(first)
	secondStage, secondCause := chartFailureStage(second)
	if firstStage != secondStage {
		return false
	}
	firstCode, firstCoded := cerrors.CodeOf(first)
	secondCode, secondCoded := cerrors.CodeOf(second)
	if firstCoded != secondCoded || firstCode != secondCode {
		return false
	}
	var firstSchema, secondSchema *valuesSchemaFailure
	if errors.As(firstCause, &firstSchema) {
		return errors.As(secondCause, &secondSchema) &&
			strings.Join(firstSchema.sortedFindings(), "\n") == strings.Join(secondSchema.sortedFindings(), "\n")
	}
	if position := templatePosition(firstCause); position != "" {
		return position == templatePosition(secondCause)
	}
	return firstCause.Error() == secondCause.Error()
}

// chartFailureStage returns the stage a failed render names — its outermost frame,
// with the cause that frame repeats trimmed off — and that cause. A failure
// carrying no frame is its own cause and names no stage.
func chartFailureStage(err error) (string, error) {
	var ce *cerrors.CompilerError
	if !errors.As(err, &ce) {
		return "", err
	}
	cause := ce.Unwrap()
	if cause == nil {
		return ce.Source().String() + ": " + ce.Message(), ce
	}
	return ce.Source().String() + ": " + strings.TrimSuffix(ce.Message(), ": "+cause.Error()), cause
}

// templatePosition returns the template, line and column Helm reports a template
// failed at, and the empty string where the failure carries none. Helm spells a
// chart's own fail or required as "execution error at (<file>:<line>:<col>): …"
// and any other fault as the template engine's own "template:
// <file>:<line>:<col>: executing …". The position is the chart's own text — a file
// name and a place in it — and never data the template read; the action the
// engine names after it is not taken, because a template rendered from a value
// carries that value in its action.
func templatePosition(err error) string {
	msg := err.Error()
	if rest, ok := strings.CutPrefix(msg, "execution error at ("); ok {
		if end := strings.Index(rest, "): "); end >= 0 {
			return rest[:end]
		}
		return ""
	}
	if rest, ok := strings.CutPrefix(msg, "template: "); ok && strings.Contains(rest, ": executing ") {
		if end := strings.Index(rest, ": "); end >= 0 {
			return rest[:end]
		}
	}
	return ""
}

// withheldChartCause is a chart render's failure reported without its text: where
// it failed, as far as Helm or the schema states that without a value, and the
// fixed sentence. It unwraps to the registered condition the failure carried and
// to nothing else, so the condition's codes cross the socket while no text the
// render composed from a value does.
type withheldChartCause struct {
	position  string
	condition error
}

func (c *withheldChartCause) Error() string {
	if c.position == "" {
		return chartFailureWithheld
	}
	return c.position + ": " + chartFailureWithheld
}

func (c *withheldChartCause) Unwrap() error { return c.condition }

// withheldChartFailure builds the withheld report for a failed render of nodeID.
// It is composed of identities alone — the node, Helm's template position, the
// schema findings' locations and keywords — and never reuses the failing frame,
// because a frame can carry text derived from a value: a rendered resource's name
// in an output-collision frame, a parse failure's excerpt in a document frame.
func withheldChartFailure(err error, nodeID string) error {
	cause := &withheldChartCause{}
	var condition cerrors.Identity
	if errors.As(err, &condition) {
		cause.condition = condition
	}
	_, stageCause := chartFailureStage(err)
	var schema *valuesSchemaFailure
	if errors.As(stageCause, &schema) && len(schema.findings) > 0 {
		cause.position = "values do not meet the chart's schema at " + strings.Join(schema.sortedFindings(), ", ")
	} else if position := templatePosition(stageCause); position != "" {
		cause.position = "execution error at (" + position + ")"
	}
	category := cerrors.TranslationError
	var ce *cerrors.CompilerError
	if errors.As(err, &ce) {
		category = ce.Category()
	}
	return cerrors.Wrap(cause, category, cerrors.SourceLocation{File: nodeID}, fmt.Sprintf("helm: node %q", nodeID))
}
