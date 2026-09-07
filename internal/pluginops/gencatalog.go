// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package pluginops

import (
	"bytes"
	"context"
	"io"

	"github.com/aggrega-ai/aggrega-cli/internal/authoredcontent"
	"github.com/aggrega-ai/aggrega-cli/internal/gencatalog"
)

// GenCatalogOptions is what a catalog generation decides, independently of who
// asked for it.
//
// FromEmbedded and PluginPath are the two selection modes: every guest embedded
// in this binary, or one guest at a named path. OverlayPath is a directory in the
// first mode and a single file in the second, because an overlay is matched to a
// guest by identity when there are several and there is nothing to match when
// there is one.
//
// Report hands the generated content back instead of writing it. A generated
// entity is a catalog document the author owns and goes on editing, so a caller
// that keeps its own document model puts the content there — where it is visible
// before it is saved, and can be amended or undone.
type GenCatalogOptions struct {
	FromEmbedded bool
	PluginPath   string
	OutputDir    string
	OverlayPath  string
	Report       bool
}

// GenCatalogResult is what a catalog generation produced.
//
// Exactly one half is filled, and which one says which mode ran. Documents
// carries the generated entities and the file each is for, and is filled only in
// report mode — where nothing was created or modified. Notices carries what the
// generation said about the writes it performed, and is filled only when it wrote.
type GenCatalogResult struct {
	Documents []authoredcontent.Document `json:"documents,omitempty"`
	Notices   string                     `json:"notices,omitempty"`
}

// GenCatalog emits one Plugin entity per selected guest, merging the
// machine-derived contract fields from the guest's self-report with an authored
// per-plugin overlay and the self-report's version. Re-running over the same
// inputs produces byte-identical output.
//
// 🔴 This is a delegation and deliberately holds no generation logic. The
// implementation lives in one place and was already importable when this package
// was written, so lifting it would have moved working code for no gain and copying
// it would have been the dis-unification a second implementation always is. What
// this adds is the operation's entry point, so all four plugin operations are
// reached the same way from every surface rather than three through here and one
// through a package a caller has to know to find.
//
// Whatever the generation emits is copied to out when out is non-nil, so a
// terminal sees exactly what it saw before. In report mode the emitted form is
// also decoded into Documents — with that form's own decoder, the matched pair of
// the encoder that produced it, rather than by re-parsing its text — so a surface
// answering with values gets values and one answering with text gets text.
func GenCatalog(ctx context.Context, opts GenCatalogOptions, out io.Writer) (GenCatalogResult, error) {
	var emitted bytes.Buffer
	err := gencatalog.Run(ctx, gencatalog.Options{
		FromEmbedded: opts.FromEmbedded,
		PluginPath:   opts.PluginPath,
		OutputDir:    opts.OutputDir,
		OverlayPath:  opts.OverlayPath,
		Delivery:     authoredcontent.DeliveryFor(opts.Report),
	}, &emitted)

	// The emitted bytes are handed on even when the generation failed part way, so
	// a caller watching a terminal sees what it managed to say before the halt
	// rather than losing it to the error path.
	if out != nil {
		if _, werr := out.Write(emitted.Bytes()); werr != nil && err == nil {
			return GenCatalogResult{}, werr
		}
	}
	if err != nil {
		return GenCatalogResult{}, err
	}

	if !opts.Report {
		return GenCatalogResult{Notices: emitted.String()}, nil
	}
	docs, derr := authoredcontent.Decode(emitted.Bytes())
	if derr != nil {
		return GenCatalogResult{}, derr
	}
	return GenCatalogResult{Documents: docs}, nil
}
