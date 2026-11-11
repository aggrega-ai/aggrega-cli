// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package serve

import (
	"bytes"
	"encoding/json"
	"io"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/opprogress"
)

// TestTheStepsAnOperationWaitsOnAreLinesOfTheRequestsReport drives the steps an
// operation reports waiting on through the production notification path, on a
// request that asked to be reported on, and reads which of them travel: the ones
// reported while the report is open, each as a line carrying the step's name, and
// none before the report began or after it ended.
func TestTheStepsAnOperationWaitsOnAreLinesOfTheRequestsReport(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer
	ctx, flush := serializerContext(t, &out)
	ctx = withProgressToken(ctx, json.RawMessage(`"tok"`))
	srv := &Server{session: &session{stderr: io.Discard}}
	run := srv.progressFor(ctx)
	opprogress.Begin(ctx, "a step before the report began")(nil)
	run.begin("Doing the thing")
	opprogress.Begin(ctx, "reading the trust policy hub.example advertises")(nil)
	opprogress.Begin(ctx, "fetching hub.example/plugin/cli/moo:1.0.0")(nil)
	run.done()
	opprogress.Begin(ctx, "a step after the report ended")(nil)
	flush()

	var kinds, lines []string
	for _, body := range readAllFrames(t, &out) {
		var f wireFrame
		if err := json.Unmarshal(body, &f); err != nil {
			t.Fatalf("decode frame: %v (body %s)", err, body)
		}
		var p progressFramePayload
		if err := json.Unmarshal(f.Params, &p); err != nil {
			t.Fatalf("decode progress params: %v", err)
		}
		kinds = append(kinds, p.Value.Kind)
		if p.Value.Kind == progressKindReport {
			lines = append(lines, p.Value.Message)
		}
	}
	want := []string{"reading the trust policy hub.example advertises", "fetching hub.example/plugin/cli/moo:1.0.0"}
	if strings.Join(lines, "|") != strings.Join(want, "|") {
		t.Errorf("reported lines: got %q, want %q", lines, want)
	}
	if strings.Join(kinds, ",") != "begin,report,report,end" {
		t.Errorf("frames: got %q, want begin, two reports, end", kinds)
	}
}
