// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package importops

import (
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/aggrega-ai/aggrega-cli/internal/wasm/embedded"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"

	"github.com/aggrega-ai/aggrega-cli/internal/embeddedentities"
)

// TestNoImporterReadsTheAnswersFileOfADirectorySource holds every importer the
// binary carries to never reading the answers file an import keeps inside a
// directory source: no file pattern any of their recorded signatures declares
// selects import-answers.yaml at the directory's root or beneath it, so the
// directory's files an importer is handed, and those recognition reads, never
// include it. The importers reading a directory — CrewAI, Google ADK and
// LangGraph among them — must be among those read, so the case cannot pass over
// a binary carrying none.
//
// The needle: an importer declaring a pattern that matches every YAML file is
// held to selecting it, so the check fires where a pattern would.
func TestNoImporterReadsTheAnswersFileOfADirectorySource(t *testing.T) {
	embeddedentities.Require(t)
	t.Parallel()
	names := []string{AnswersFileName, "src/support_brief/" + AnswersFileName, "support_agent/" + AnswersFileName}

	needle := types.Plugin{Spec: types.PluginSpec{Importer: &types.PluginImporter{
		Detect: []types.PluginDetectSignature{{Files: []string{"*.yaml"}}}}}}
	for _, name := range names {
		if !selectsFile(needle, name) {
			t.Fatalf("the needle: a pattern matching every YAML file does not select %s, so the check cannot fire", name)
		}
	}

	readsDirectories := map[string]bool{}
	for _, p := range embedded.PublishedPlugins() {
		var entity types.Plugin
		if err := yaml.Unmarshal(p.Entity, &entity); err != nil || entity.Spec.Importer == nil {
			continue
		}
		if declaresFilePatterns(entity) {
			readsDirectories[entity.Coordinate()] = true
		}
		for _, name := range names {
			if selectsFile(entity, name) {
				t.Errorf("%s selects %s from a directory source, so the answers file would be read as the source",
					entity.Coordinate(), name)
			}
		}
	}
	for _, want := range []string{"importer/crewai", "importer/google-adk", "importer/langgraph"} {
		if !readsDirectories[want] {
			t.Errorf("this binary carries no entity of %s declaring file patterns, so nothing here shows it leaves "+
				"the answers file unread: build the core plugins with the repository's build script, which writes "+
				"what the binary carries of each core plugin it does not embed", want)
		}
	}
}
