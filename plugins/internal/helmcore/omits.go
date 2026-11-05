// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package helmcore

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	chart "helm.sh/helm/v4/pkg/chart/v2"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/rendercore"
	"github.com/aggrega-ai/aggrega-cli/internal/plugincondition"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
)

// omitSelection is one chart render's omit entries and how many documents each has
// matched so far. It is this provider's one home of what an omit entry selects:
// every rendered document of the chart passes through it — a templated document and
// a crds/ file alike, because both are emitted through one path — and a document
// any entry matches is left out of what the render returns.
//
// An entry matches a document by the document's own words, the ones its emitted
// file carries: its kind equals the document's kind, its name equals the document's
// metadata.name, and, only where the entry names a namespace, that namespace equals
// the one the document is in. That is the namespace the document writes, or the
// one the render writes into it from the node's declared namespace where the chart
// left it without one — which is what the emitted file then carries — and never the
// cluster default a render without a declared namespace falls back to: a document
// the render leaves without a namespace is in none, and an entry naming one does not
// match it.
//
// Only where the entry names a subchart, the document must also have been rendered
// by that subchart: from its own templates/ or crds/ directory, read off the path
// the render keyed the document's source by. A subchart is named by its own name, or
// by the alias its parent's dependency gives it, and a nested one by the names from
// the outermost joined with "/". A document the chart renders itself came from no
// subchart, and one a subchart nested inside the named one renders came from that
// nested subchart, so neither matches. An entry naming no subchart matches a document
// wherever it came from.
//
// A document carrying no kind or no metadata.name has no identity an entry can
// name, so it matches no entry, whatever the entry says.
type omitSelection struct {
	entries []rendercore.ResolvedOmit
	counts  []int
	// charts is every chart of the render, the chart itself first, each with the path
	// its sources are keyed under and the subchart name an entry names it by.
	charts []renderedChart
}

// renderedChart is one chart of a render — the chart itself or one of its subcharts
// at any depth — with the path the render keys its sources under and the name an
// omit entry names it by, empty for the chart itself.
type renderedChart struct {
	fullPath string
	subchart string
}

// newOmitSelection prepares the selection for a render handed entries, over the
// chart as it renders: its dependencies already processed, so a subchart its values
// disable is not among the subcharts it renders. A render handed none gets a
// selection that leaves nothing out and reports no counts.
//
// Every entry naming a subchart is checked before any document is matched, and the
// first, in the order the entries arrived, naming a subchart the chart does not
// render is refused, naming the subcharts it does render: such an entry can match
// nothing, and the halt names what it could have named instead.
func newOmitSelection(entries []rendercore.ResolvedOmit, ch *chart.Chart, nodeID string) (*omitSelection, error) {
	s := &omitSelection{entries: entries}
	if len(entries) == 0 {
		return s, nil
	}
	s.counts = make([]int, len(entries))
	s.charts = renderedChartsOf(ch)
	for _, entry := range entries {
		if entry.Entry.Subchart == "" || s.renders(entry.Entry.Subchart) {
			continue
		}
		return nil, unknownSubchartErr(entry, s.charts, nodeID)
	}
	return s, nil
}

// renderedChartsOf lists the chart and every subchart it renders, depth first in the
// order each chart lists its dependencies.
func renderedChartsOf(ch *chart.Chart) []renderedChart {
	var out []renderedChart
	var walk func(c *chart.Chart, subchart string)
	walk = func(c *chart.Chart, subchart string) {
		out = append(out, renderedChart{fullPath: c.ChartFullPath(), subchart: subchart})
		for _, dep := range c.Dependencies() {
			name := dep.Name()
			if subchart != "" {
				name = subchart + "/" + name
			}
			walk(dep, name)
		}
	}
	if ch != nil {
		walk(ch, "")
	}
	return out
}

// renders reports whether the chart renders the named subchart.
func (s *omitSelection) renders(subchart string) bool {
	for _, c := range s.charts {
		if c.subchart != "" && c.subchart == subchart {
			return true
		}
	}
	return false
}

// subchartOf is the subchart a source of the render came from, read off the path
// the render keys it by: a template is keyed under its chart's full path followed by
// templates/, and a crds/ file under its chart's full path followed by crds/. The
// chart whose full path is followed by one of those is the one that rendered it; a
// chart's own templates and crds/ files are never under a subchart's path, because
// every subchart's path continues with charts/. Empty for a source the chart itself
// renders, and for one no chart of the render claims.
func (s *omitSelection) subchartOf(sourcePath string) string {
	for _, c := range s.charts {
		rest, under := strings.CutPrefix(sourcePath, c.fullPath+"/")
		if under && (strings.HasPrefix(rest, "templates/") || strings.HasPrefix(rest, "crds/")) {
			return c.subchart
		}
	}
	return ""
}

// leaveOut reports whether a document is left out, counting it against every entry
// that matches it. subchart is the subchart the document's source came from, empty
// for one the chart renders itself. A document two entries match is counted once for
// each of them, because each entry's count is the number of documents that entry
// matched, whether or not another entry matched them too.
func (s *omitSelection) leaveOut(d renderedDocument, subchart string) bool {
	if len(s.entries) == 0 || d.kind == "" || d.name == "" {
		return false
	}
	matched := false
	for i, entry := range s.entries {
		if omitMatches(entry.Entry, d, subchart) {
			s.counts[i]++
			matched = true
		}
	}
	return matched
}

// omitted returns one count per entry the render was handed, in the order the
// entries arrived, and nil for a render handed none.
func (s *omitSelection) omitted() []int {
	return s.counts
}

// omitMatches reports whether one entry names one document, rendered by the named
// subchart.
func omitMatches(entry rendercore.OmitEntry, d renderedDocument, subchart string) bool {
	if entry.Kind != d.kind || entry.Name != d.name {
		return false
	}
	if entry.Namespace != "" && entry.Namespace != d.namespace {
		return false
	}
	return entry.Subchart == "" || entry.Subchart == subchart
}

// unknownSubchartErr refuses an entry naming a subchart the chart does not render,
// naming the entry by its kind and name, the subchart it names, and the subcharts
// the chart does render, in name order. A field the entry withholds is never quoted.
func unknownSubchartErr(entry rendercore.ResolvedOmit, charts []renderedChart, nodeID string) error {
	var rendered []string
	for _, c := range charts {
		if c.subchart != "" {
			rendered = append(rendered, strconv.Quote(c.subchart))
		}
	}
	sort.Strings(rendered)
	renders := "it renders no subchart at all"
	if len(rendered) > 0 {
		renders = "the subcharts it renders are " + strings.Join(rendered, ", ")
	}
	return cerrors.Wrap(plugincondition.ErrPluginRequiredInputUnresolved(),
		cerrors.TranslationError, cerrors.SourceLocation{Instance: nodeID},
		fmt.Sprintf("helm: node %q: an omits entry (kind %s, name %s) names subchart %s, and the chart renders no subchart of that name: %s; name one of those, or give the entry a when that applies it only where that subchart renders",
			nodeID,
			rendercore.OmitFieldText(entry.Entry.Kind, entry.Withheld.Kind),
			rendercore.OmitFieldText(entry.Entry.Name, entry.Withheld.Name),
			rendercore.OmitFieldText(entry.Entry.Subchart, entry.Withheld.Subchart),
			renders))
}
