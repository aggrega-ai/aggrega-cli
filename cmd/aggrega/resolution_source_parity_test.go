// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

// Where one resolution took each reference from, named alike on every surface
// that reports it.
//
// A person running `aggrega pull --detail` reads, per reference, whether the run
// fetched it from a registry, reused a cached copy, found it on the local
// filesystem, served it from inside the binary, or took it as external. A client
// asking the daemon for a build that resolves first, and an assistant calling the
// pull tool, are owed the same fact about the same resolution, in the same words.
//
// The expectation is the text the command line actually printed, parsed here, and
// each answer is decoded here by field name rather than through the producer's own
// types. So the comparison is between what two surfaces emitted, and a change that
// moved one of them — a source dropped from an answer, or read from the wrong
// field — cannot pass by moving the expectation along with it.
//
// Every run starts from its own fresh copy of the fixture: no lockfile, and an
// empty cache. Running the surfaces over one shared copy would let a later run read
// what an earlier one cached, and a source that differed for that reason would be a
// fact about the order of the runs rather than about either surface.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/mcp"
	"github.com/aggrega-ai/aggrega-cli/internal/serve"
	"github.com/aggrega-ai/aggrega-cli/pkg/closure"
)

// sourceParityFixture is one starting state the surfaces are compared over.
type sourceParityFixture struct {
	name string
	// write lays the fixture down under a fresh workspace root and returns the
	// Release's path relative to that root.
	write func(t *testing.T, root string) string
	// mustName is a source at least one reference of this fixture must be
	// reported under, which is what makes the fixture about the source it is
	// named for.
	mustName closure.ResolutionSource
}

// sourceParityFixtures is the starting states the comparisons below run over in
// every test run. The Release resolving from a registry is compared by the same
// two requirements in resolution_source_registry_integration_test.go, because
// the loopback registry it publishes to is a harness of the integration tier.
func sourceParityFixtures() []sourceParityFixture {
	return []sourceParityFixture{{
		name:     "a Release resolving from a catalog directory",
		write:    writeCatalogResolutionFixture,
		mustName: closure.SourceLocal,
	}}
}

// sourceParityRow is one reference a surface reported, reduced to the two facts
// every surface reports alike: the reference, and where the run took it from.
//
// The kind is left out deliberately. The pull detail column derives it from where
// the reference was written, and the structured answers from the kind the
// resolution recorded, so the two can word one reference's kind differently
// without either being wrong — and that is not what is compared here.
type sourceParityRow struct {
	uri    string
	source string
}

// String renders a row the way a failure names it.
func (r sourceParityRow) String() string {
	source := r.source
	if source == "" {
		source = "(no source)"
	}
	return r.uri + " ← " + source
}

// sourceParityWireRow is one entry of an answer's resolution report, decoded by the
// key names a client binds to.
type sourceParityWireRow struct {
	URI    string `json:"uri"`
	Source string `json:"source"`
}

// writeCatalogResolutionFixture lays down a Release whose dependencies all resolve
// from the workspace's own catalog directory: a Component drawing on a shared
// schema document, the Recipe realising it, and the helm provider seeded from the
// copy the binary carries. The workspace is offline, so no run over it can reach a
// registry whatever it is asked to do.
func writeCatalogResolutionFixture(t *testing.T, root string) string {
	t.Helper()
	writeFixture(t, filepath.Join(root, ".aggrega-config.yaml"), `offline: true
workspace:
  catalog_dirs:
    - ./catalog
`)

	catalogRoot := filepath.Join(root, "catalog")
	writeFixture(t, filepath.Join(catalogRoot, "libraries", "base-types.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: SchemaLibrary
metadata:
  name: BaseTypes
  version: "1.0.0"
spec:
  schemas:
    PortSpec:
      port: { type: integer, default: 8080 }
`)
	writeFixture(t, filepath.Join(catalogRoot, "components", "web.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Component
metadata:
  name: demo/web
  version: "1.0.0"
  labels:
    component.aggrega.ai/type: "server"
spec:
  schema:
    values:
      port: { type: integer, default: 8080 }
      ports:
        $ref: "BaseTypes:1.0.0#/schemas/PortSpec"
`)
	writeFixture(t, filepath.Join(catalogRoot, "recipes", "web.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Recipe
metadata:
  name: demo-web-helm
  version: "1.0.0"
spec:
  capability: helm
  implements: demo/web
  workload: |
    apiVersion: aggrega.ai/v1alpha1
    kind: Workload
    replicas: 1
    containers:
      web:
        image: demo-web:latest
        ports:
          http: {{ .values.port }}
`)
	writeHelmRendererPlugin(t, filepath.Join(catalogRoot, "providers"))

	writeFixture(t, filepath.Join(root, "aggregate.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Aggregate
metadata:
  name: shop
  version: "1.0.0"
spec:
  components:
    web:
      type: demo/web
      values:
        port: 8080
`)
	writeFixture(t, filepath.Join(root, "manifest.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Manifest
metadata:
  name: shop-manifest
  version: "1.0.0"
spec:
  mappings:
    components:
      type:
        demo/web: demo-web-helm
`)
	writeFixture(t, filepath.Join(root, "release.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Release
metadata:
  name: shop-release
  environment: testing
  version: "1.0.0"
spec:
  aggregate: ./aggregate.yaml
  manifest: ./manifest.yaml
  boundaries:
    main:
      directory: ./dist/main
      components:
        web: {}
`)
	return "release.yaml"
}

// recognisedSourceWord reports whether word is one of the sources the pull detail
// column documents.
func recognisedSourceWord(word string) bool {
	switch closure.ResolutionSource(word) {
	case closure.SourceFetchedFresh, closure.SourceCacheReuse, closure.SourceLocal,
		closure.SourceInBinary, closure.SourceExternal:
		return true
	}
	return false
}

// withinWorkspace rewrites a reference that spells the fixture's own root, so two
// fresh copies of one fixture name the same reference the same way.
func withinWorkspace(uri, root string) string {
	return strings.ReplaceAll(uri, root, "<workspace>")
}

// pullDetailSources runs `aggrega pull --detail` over a fresh copy of the fixture
// and returns what its table printed, one row per reference.
func pullDetailSources(t *testing.T, fx sourceParityFixture) []sourceParityRow {
	t.Helper()
	root := t.TempDir()
	release := fx.write(t, root)

	var out, diag bytes.Buffer
	if err := pullRun(context.Background(), pullConfig{
		ws:          resolveTempWorkspace(t, tempWorkspaceOptions{Root: root}),
		ReleasePath: filepath.Join(root, release),
		Detail:      true,
		Out:         &out,
		DiagOut:     &diag,
	}); err != nil {
		t.Fatalf("aggrega pull --detail refused the fixture: %v\ndiagnostics:\n%s", err, diag.String())
	}
	return parsePullDetail(t, out.String(), root)
}

// parsePullDetail reads the rows of the table `aggrega pull --detail` prints.
//
// Each row opens with the source and names the reference in its third column. A
// row whose first column is not a source the command documents fails here rather
// than being compared: a row printed with no source shifts every column left, and
// would otherwise be read as a different reference under a different source.
func parsePullDetail(t *testing.T, printed, root string) []sourceParityRow {
	t.Helper()
	lines := strings.Split(printed, "\n")
	start := -1
	for i, line := range lines {
		if strings.HasPrefix(line, "resolved ") && strings.HasSuffix(line, "(this pull):") {
			start = i + 1
			break
		}
	}
	if start < 0 {
		t.Fatalf("aggrega pull --detail printed no resolution table:\n%s", printed)
	}
	var rows []sourceParityRow
	for _, line := range lines[start:] {
		if !strings.HasPrefix(line, "  ") {
			break
		}
		fields := strings.Fields(line)
		if len(fields) < 3 {
			t.Fatalf("a row of the pull detail table carries %d column(s), fewer than a source, a kind and a "+
				"reference: %q", len(fields), line)
		}
		if !recognisedSourceWord(fields[0]) {
			t.Fatalf("a row of the pull detail table opens with %q, which is not a source the command documents, "+
				"so the row cannot be compared: %q", fields[0], line)
		}
		rows = append(rows, sourceParityRow{uri: withinWorkspace(fields[2], root), source: fields[0]})
	}
	if len(rows) == 0 {
		t.Fatalf("aggrega pull --detail printed a resolution table with no rows:\n%s", printed)
	}
	return rows
}

// daemonAnswer sends one request to the daemon through its own framing, over the
// workspace at root, and returns the result it answered with.
//
// It goes through serve.Run rather than a handler, so the request is routed and
// answered exactly as a client's would be. Standard input is written in full before
// the daemon starts and is never appended to, so the daemon reads the one request,
// reaches the end of its input, finishes the work it accepted, and exits.
func daemonAnswer(t *testing.T, root, method string, params map[string]any) json.RawMessage {
	t.Helper()
	body, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": method, "params": params})
	if err != nil {
		t.Fatalf("marshal the %s request: %v", method, err)
	}
	var stdin bytes.Buffer
	fmt.Fprintf(&stdin, "Content-Length: %d\r\n\r\n", len(body))
	stdin.Write(body)

	var stdout, stderr bytes.Buffer
	if rerr := serve.Run(context.Background(), serve.RunConfig{
		Stdin:           &stdin,
		Stdout:          &stdout,
		Stderr:          &stderr,
		WorkspaceRoot:   root,
		CompilerVersion: provenanceCompilerVersion(),
		Toolchain:       toolchainStamp(),
	}); rerr != nil {
		t.Fatalf("the daemon exited with an error: %v\nstderr: %s", rerr, stderr.String())
	}

	frame := daemonFrameAnswering(t, stdout.Bytes(), 1)
	if raw, refused := frame["error"]; refused {
		t.Fatalf("%s was refused: %s\nstderr: %s", method, raw, stderr.String())
	}
	result, answered := frame["result"]
	if !answered {
		t.Fatalf("the answer to %s carries neither a result nor an error: %s", method, stdout.String())
	}
	return result
}

// daemonFrameAnswering returns the framed message in the daemon's output that
// answers the request with the given id.
//
// The output is read frame by frame rather than taking the first one, because a
// build publishes its positioned findings as notifications ahead of its answer.
func daemonFrameAnswering(t *testing.T, out []byte, id int) map[string]json.RawMessage {
	t.Helper()
	const header = "Content-Length: "
	want := fmt.Sprint(id)
	rest := out
	for {
		start := bytes.Index(rest, []byte(header))
		if start < 0 {
			break
		}
		rest = rest[start+len(header):]
		end := bytes.Index(rest, []byte("\r\n\r\n"))
		if end < 0 {
			t.Fatalf("a framing header in the daemon's output is unterminated: %q", out)
		}
		var length int
		if _, err := fmt.Sscanf(string(rest[:end]), "%d", &length); err != nil {
			t.Fatalf("a framing header in the daemon's output carries no length: %q", rest[:end])
		}
		payload := rest[end+4:]
		if len(payload) < length {
			t.Fatalf("a frame declares %d bytes and carries %d: %q", length, len(payload), out)
		}
		var frame map[string]json.RawMessage
		if err := json.Unmarshal(payload[:length], &frame); err != nil {
			t.Fatalf("decode a frame of the daemon's output: %v\nframe: %s", err, payload[:length])
		}
		if string(bytes.TrimSpace(frame["id"])) == want {
			return frame
		}
		rest = payload[length:]
	}
	t.Fatalf("the daemon wrote no answer to request %d: %q", id, out)
	return nil
}

// daemonBuildSources asks the daemon for a build of a fresh copy of the fixture
// that resolves its dependencies first, and returns the source each entry of the
// answer's resolution report names.
func daemonBuildSources(t *testing.T, fx sourceParityFixture) []sourceParityRow {
	t.Helper()
	root := t.TempDir()
	release := fx.write(t, root)

	raw := daemonAnswer(t, root, "aggrega/build", map[string]any{
		"workspaceRoot": root,
		"release":       release,
		"autoPull":      true,
	})
	var result struct {
		DependencyResolution *struct {
			Entities  []sourceParityWireRow `json:"entities"`
			Libraries []sourceParityWireRow `json:"libraries"`
			Plugins   []sourceParityWireRow `json:"plugins"`
		} `json:"dependencyResolution"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatalf("decode the build's answer: %v\nanswer: %s", err, raw)
	}
	report := result.DependencyResolution
	if report == nil {
		t.Fatalf("the build resolved its dependencies first and its answer reports no resolution: %s", raw)
	}
	var rows []sourceParityRow
	for _, section := range [][]sourceParityWireRow{report.Entities, report.Libraries, report.Plugins} {
		for _, e := range section {
			rows = append(rows, sourceParityRow{uri: withinWorkspace(e.URI, root), source: e.Source})
		}
	}
	return rows
}

// pullToolSources calls the assistant-facing pull tool over a fresh copy of the
// fixture, through the tool protocol's own message loop, and returns the source
// each entry of its resolved list names.
func pullToolSources(t *testing.T, fx sourceParityFixture) []sourceParityRow {
	t.Helper()
	root := t.TempDir()
	release := fx.write(t, root)

	body, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/call",
		"params": map[string]any{
			"name": "pull",
			"arguments": map[string]string{
				"release":        filepath.Join(root, release),
				"workspace_root": root,
			},
		},
	})
	if err != nil {
		t.Fatalf("marshal the pull call: %v", err)
	}
	var stdin bytes.Buffer
	stdin.Write(body)
	stdin.WriteByte('\n')

	var stdout, stderr bytes.Buffer
	// The session is given no command surface: a tool derived from the command tree
	// re-invokes the running binary, which under test is this test binary.
	if rerr := mcp.Run(context.Background(), mcp.Config{
		Stdin:         &stdin,
		Stdout:        &stdout,
		Stderr:        &stderr,
		ServerVersion: provenanceCompilerVersion(),
		Toolchain:     toolchainStamp(),
		WorkspaceRoot: root,
	}); rerr != nil {
		t.Fatalf("the tool session exited with an error: %v\nstderr: %s", rerr, stderr.String())
	}

	type toolReply struct {
		ID     json.RawMessage `json:"id"`
		Result *struct {
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
			StructuredContent json.RawMessage `json:"structuredContent"`
			IsError           bool            `json:"isError"`
		} `json:"result"`
		Error json.RawMessage `json:"error"`
	}
	// The session's output is read message by message rather than taking the first,
	// so a notification written ahead of the answer is passed over.
	var envelope *toolReply
	for _, line := range bytes.Split(stdout.Bytes(), []byte("\n")) {
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}
		var reply toolReply
		if err := json.Unmarshal(line, &reply); err != nil {
			t.Fatalf("decode a message of the tool session: %v\nmessage: %s", err, line)
		}
		if string(bytes.TrimSpace(reply.ID)) == "1" {
			envelope = &reply
			break
		}
	}
	if envelope == nil {
		t.Fatalf("the tool session wrote no answer to the pull call\nstdout: %s\nstderr: %s", stdout.String(), stderr.String())
	}
	if envelope.Error != nil {
		t.Fatalf("the pull call was refused at the protocol level: %s", envelope.Error)
	}
	if envelope.Result == nil {
		t.Fatalf("the answer to the pull call carries neither a result nor an error: %s", stdout.String())
	}
	if envelope.Result.IsError {
		text := ""
		if len(envelope.Result.Content) > 0 {
			text = envelope.Result.Content[0].Text
		}
		t.Fatalf("the pull tool refused the fixture: %s", text)
	}
	var payload struct {
		Resolved []sourceParityWireRow `json:"resolved"`
	}
	if err := json.Unmarshal(envelope.Result.StructuredContent, &payload); err != nil {
		t.Fatalf("decode the pull tool's structured result: %v\nresult: %s", err, envelope.Result.StructuredContent)
	}
	rows := make([]sourceParityRow, 0, len(payload.Resolved))
	for _, e := range payload.Resolved {
		rows = append(rows, sourceParityRow{uri: withinWorkspace(e.URI, root), source: e.Source})
	}
	return rows
}

// requireSourcesThatCanDisagree fails unless the command line's rows could tell a
// surface that reported the right source from one that did not.
//
// Two things make a comparison able to fail. The fixture must be reported under
// the source it exists for, or it is not the fixture it says it is. And its rows
// must carry more than one source between them, or a surface that printed one
// constant word for every reference would match them all.
func requireSourcesThatCanDisagree(t *testing.T, fx sourceParityFixture, printed []sourceParityRow) {
	t.Helper()
	words := map[string]bool{}
	for _, r := range printed {
		words[r.source] = true
	}
	if !words[string(fx.mustName)] {
		t.Fatalf("precondition: aggrega pull --detail reported no reference of %s as %q, so the fixture no longer "+
			"exercises the source it exists for; it printed %v", fx.name, fx.mustName, printed)
	}
	if len(words) < 2 {
		t.Fatalf("precondition: every reference of %s was reported under one source, so a surface printing that one "+
			"word for everything would pass; it printed %v", fx.name, printed)
	}
}

// sourceMismatches compares two populations of rows as multisets and names every
// row one side reports and the other does not.
//
// It is a set difference rather than a count: two sides short by the same number
// of rows agree on a count, and the row that went missing is the only part of a
// failure anyone can act on.
func sourceMismatches(want, got []sourceParityRow) (missing, unexpected []string) {
	counts := map[sourceParityRow]int{}
	for _, r := range want {
		counts[r]++
	}
	for _, r := range got {
		counts[r]--
	}
	for r, n := range counts {
		for ; n > 0; n-- {
			missing = append(missing, r.String())
		}
		for ; n < 0; n++ {
			unexpected = append(unexpected, r.String())
		}
	}
	sort.Strings(missing)
	sort.Strings(unexpected)
	return missing, unexpected
}

// assertSameSources fails naming every row on which a surface's answer and the
// command line's table disagree.
func assertSameSources(t *testing.T, surface string, printed, answered []sourceParityRow) {
	t.Helper()
	if len(answered) == 0 {
		t.Fatalf("%s reported no reference at all, so there is no source to compare", surface)
	}
	missing, unexpected := sourceMismatches(printed, answered)
	if len(missing) == 0 && len(unexpected) == 0 {
		return
	}
	t.Errorf("%s names where the resolution took its references from differently from aggrega pull --detail "+
		"for the same Release.\n  printed by the command line and not answered:\n    %s\n  answered and not "+
		"printed by the command line:\n    %s",
		surface, strings.Join(orNone(missing), "\n    "), strings.Join(orNone(unexpected), "\n    "))
}

// orNone keeps an empty list readable in a failure message.
func orNone(rows []string) []string {
	if len(rows) == 0 {
		return []string{"(none)"}
	}
	return rows
}

// requireBuildNamesSourcesAsPullDetail drives the daemon's build of the fixture's
// Release that resolves its dependencies first, and requires each entry of the
// answer's resolution report to name the source `aggrega pull --detail` prints for
// the same reference, over the same starting state.
//
// It is the one statement of that requirement, so every fixture it runs over —
// in whichever tier the fixture's harness lives — is held to the same comparison.
func requireBuildNamesSourcesAsPullDetail(t *testing.T, fx sourceParityFixture) {
	printed := pullDetailSources(t, fx)
	requireSourcesThatCanDisagree(t, fx, printed)
	t.Logf("aggrega pull --detail printed %v", printed)

	answered := daemonBuildSources(t, fx)
	t.Logf("the daemon's build answered %v", answered)
	assertSameSources(t, "the daemon's build", printed, answered)
}

// requirePullToolNamesSourcesAsPullDetail is the same requirement on the
// assistant-facing surface: each entry of the pull tool's resolved list names the
// source `aggrega pull --detail` prints for the same reference, over the same
// starting state.
func requirePullToolNamesSourcesAsPullDetail(t *testing.T, fx sourceParityFixture) {
	printed := pullDetailSources(t, fx)
	requireSourcesThatCanDisagree(t, fx, printed)
	t.Logf("aggrega pull --detail printed %v", printed)

	answered := pullToolSources(t, fx)
	t.Logf("the pull tool answered %v", answered)
	assertSameSources(t, "the pull tool", printed, answered)
}

// TestBuildResolutionNamesEachSourceAsPullDetailDoes holds the daemon's build to
// requireBuildNamesSourcesAsPullDetail over every fixture of this tier.
func TestBuildResolutionNamesEachSourceAsPullDetailDoes(t *testing.T) {
	for _, fx := range sourceParityFixtures() {
		t.Run(fx.name, func(t *testing.T) { requireBuildNamesSourcesAsPullDetail(t, fx) })
	}
}

// TestPullToolResolutionNamesEachSourceAsPullDetailDoes holds the pull tool to
// requirePullToolNamesSourcesAsPullDetail over every fixture of this tier.
func TestPullToolResolutionNamesEachSourceAsPullDetailDoes(t *testing.T) {
	for _, fx := range sourceParityFixtures() {
		t.Run(fx.name, func(t *testing.T) { requirePullToolNamesSourcesAsPullDetail(t, fx) })
	}
}

// TestReleaseClosureListingNamesNoSource is the other half of where the source
// rides. A listing of what a Release depends on describes its resolved state, not
// what one run did, so none of its entries names a source — the key is absent from
// the wire, not present and empty. It is read off the raw entries for that reason:
// decoding into a value would map an absent key and an empty one onto the same
// field.
func TestReleaseClosureListingNamesNoSource(t *testing.T) {
	for _, fx := range sourceParityFixtures() {
		t.Run(fx.name, func(t *testing.T) {
			root := t.TempDir()
			release := fx.write(t, root)

			// The listing answers only for a Release whose dependencies have been
			// resolved, so the lockfile is written first.
			var out, diag bytes.Buffer
			if err := pullRun(context.Background(), pullConfig{
				ws:          resolveTempWorkspace(t, tempWorkspaceOptions{Root: root}),
				ReleasePath: filepath.Join(root, release),
				Out:         &out,
				DiagOut:     &diag,
			}); err != nil {
				t.Fatalf("the resolution the listing needs refused: %v\ndiagnostics:\n%s", err, diag.String())
			}

			raw := daemonAnswer(t, root, "aggrega/releaseClosure", map[string]any{
				"workspaceRoot": root,
				"release":       release,
			})
			var listing struct {
				Entities  []map[string]json.RawMessage `json:"entities"`
				Libraries []map[string]json.RawMessage `json:"libraries"`
				Plugins   []map[string]json.RawMessage `json:"plugins"`
			}
			if err := json.Unmarshal(raw, &listing); err != nil {
				t.Fatalf("decode the listing: %v\nlisting: %s", err, raw)
			}

			entries := 0
			for _, section := range []struct {
				name    string
				entries []map[string]json.RawMessage
			}{
				{"entities", listing.Entities},
				{"libraries", listing.Libraries},
				{"plugins", listing.Plugins},
			} {
				for _, entry := range section.entries {
					entries++
					if value, carried := entry["source"]; carried {
						t.Errorf("the listing's %s entry %s names a source (%s), which describes one run rather "+
							"than what the Release depends on", section.name, entry["uri"], value)
					}
				}
			}
			if entries == 0 {
				t.Fatalf("precondition: the listing carries no entry, so the absence above asserts nothing: %s", raw)
			}
		})
	}
}
