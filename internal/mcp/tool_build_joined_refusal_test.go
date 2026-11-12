// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package mcp

// The build tool's answer to a refusal the pipeline run raises before anything
// renders — the composition of the graph refusing — and to one joining several
// findings: the refusal entry points where its first finding points, each finding
// is listed where it points, and the text says truthfully at which stage the run
// refused.
//
// Every expectation is read from the fixture this file writes or from the
// condition's own registration, never from the code under test.

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/topology"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/validator"
)

// The two sentences the build tool opens a refusal by the pipeline run with. They
// are the behaviour under test, so they are written here rather than read from
// the code.
const (
	refusedBeforeRendering = "The build refused before rendering anything; nothing was written to disk."
	failedWhileRendering   = "The build failed during rendering; nothing was written to disk."
)

// releaseEntryFault writes one Release entry per alias into the frontend's apis
// socket, each naming a target nothing answers to, and returns each entry's key
// and the target it names.
func releaseEntryFault(t *testing.T, releasePath string, aliases ...string) (keys, tokens []string) {
	t.Helper()
	var b strings.Builder
	b.WriteString("        frontend:\n          uses:\n            apis:\n")
	for _, a := range aliases {
		b.WriteString("              " + a + ":\n                target: no-" + a + "\n")
		keys = append(keys, ".spec.boundaries.frontend-ns.components.frontend.uses.apis."+a)
		tokens = append(tokens, "no-"+a)
	}
	b.WriteString("          infra:\n")
	writeRefusalFault(t, releasePath, "        frontend:\n          infra:\n", b.String())
	return keys, tokens
}

// TestBuildToolPlacesEachFindingOfARefusalAndNamesTheStageThatRefused holds the
// build tool's answer to the stage that refused and to every finding the refusal
// states: one fault per row, the control first.
func TestBuildToolPlacesEachFindingOfARefusalAndNamesTheStageThatRefused(t *testing.T) {
	for _, tc := range []struct {
		name string
		// write puts the fault in place and returns the document every finding
		// points into, each finding's key and the token its words carry.
		write     func(t *testing.T, releasePath, recipePath string) (document string, keys, tokens []string)
		condition error
		sentence  string
	}{
		{
			name: "a Release that renders",
			write: func(t *testing.T, releasePath, recipePath string) (string, []string, []string) {
				return "", nil, nil
			},
		},
		{
			name: "a Release entry naming a target nothing answers to",
			write: func(t *testing.T, releasePath, recipePath string) (string, []string, []string) {
				keys, tokens := releaseEntryFault(t, releasePath, "other")
				return releasePath, keys, tokens
			},
			condition: topology.ErrDanglingUsesTarget(),
			sentence:  refusedBeforeRendering,
		},
		{
			name: "two such entries, joined into one refusal",
			write: func(t *testing.T, releasePath, recipePath string) (string, []string, []string) {
				keys, tokens := releaseEntryFault(t, releasePath, "other", "another")
				return releasePath, keys, tokens
			},
			condition: topology.ErrDanglingUsesTarget(),
			sentence:  refusedBeforeRendering,
		},
		{
			name: "a Workload naming a slot its Component does not declare",
			write: func(t *testing.T, releasePath, recipePath string) (string, []string, []string) {
				writeRefusalFault(t, recipePath, "        image: api-server:latest\n",
					"        image: api-server:latest\n"+
						"        attachments:\n"+
						"          ADMIN_TOKEN:\n"+
						"            from: admin_keyy\n"+
						"            as: env\n")
				return recipePath, []string{".spec.workload"}, []string{"admin_keyy"}
			},
			condition: validator.ErrWorkloadFeaturesSchema(),
			sentence:  failedWhileRendering,
		},
	} {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			root, catalogDir, releasePath, recipePath := renderRefusalWorkspace(t)
			document, keys, tokens := tc.write(t, releasePath, recipePath)

			res := callTool(t, newTestServer(t), "build", buildArgs{
				Release:       releasePath,
				WorkspaceRoot: root,
				CatalogDirs:   []string{catalogDir},
			})
			payload := unmarshalBuildPayload(t, res)
			text := res.Content[0].Text

			if tc.condition == nil {
				if res.IsError || payload.Refusal != nil {
					t.Fatalf("the control refused: %s", text)
				}
				return
			}
			if !res.IsError {
				t.Fatalf("the fault did not refuse: %s", text)
			}
			if first := strings.SplitN(text, "\n", 2)[0]; first != tc.sentence {
				t.Errorf("the answer opens %q; the stage that refused is told as %q", first, tc.sentence)
			}

			code, _ := cerrors.CodeOf(tc.condition)
			shortCode, _ := cerrors.ShortCodeOf(tc.condition)
			entry := payload.Refusal
			if entry == nil {
				t.Fatalf("the refusal carries no structured entry:\n%s", text)
			}
			check := func(what string, got buildRefusal, key, token string) {
				t.Helper()
				line, column := authoredKeyPosition(t, document, key[strings.LastIndex(key, ".")+1:]+":")
				if filepath.Clean(told(got.File)) != filepath.Clean(document) || got.KeyPath != key ||
					got.Line != line || got.Column != column {
					t.Errorf("%s names %s %q at %d:%d; want %s %q at %d:%d",
						what, told(got.File), got.KeyPath, got.Line, got.Column, document, key, line, column)
				}
				if got.Code != code || got.ShortCode != shortCode || got.Severity != "halt" {
					t.Errorf("%s names %q %q %q; the condition is %q %q at the halting tier",
						what, got.Code, got.ShortCode, got.Severity, code, shortCode)
				}
				if !strings.Contains(got.Message, token) {
					t.Errorf("%s is not in its own words (%q): %s", what, token, got.Message)
				}
			}

			// The refusal entry: where its first finding points, worded with every one.
			check("the refusal entry", *entry, keys[0], tokens[0])
			for _, token := range tokens {
				if !strings.Contains(entry.Message, token) || !strings.Contains(text, token) {
					t.Errorf("the refusal drops the finding naming %q:\nentry: %s\ntext:\n%s", token, entry.Message, text)
				}
			}

			// Each finding, listed where it points — only where there are several.
			if len(keys) == 1 {
				if len(entry.Findings) != 0 {
					t.Errorf("a refusal stating one finding lists findings: %+v", entry.Findings)
				}
				return
			}
			if len(entry.Findings) != len(keys) {
				t.Fatalf("the refusal lists %d finding(s); it joins %d: %+v", len(entry.Findings), len(keys), entry.Findings)
			}
			for i := range keys {
				check("finding "+keys[i], entry.Findings[i], keys[i], tokens[i])
				for j, other := range tokens {
					if j != i && strings.Contains(entry.Findings[i].Message, other) {
						t.Errorf("finding %d carries another finding's words (%q): %s", i, other, entry.Findings[i].Message)
					}
				}
			}
		})
	}
}
