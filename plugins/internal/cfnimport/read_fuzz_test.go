// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package cfnimport

import (
	"io/fs"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/plugins/internal/cloudimport"
	"github.com/aggrega-ai/aggrega-pdk/go/pkg/aggrega"
)

// FuzzReadTemplate reads a fuzzed document as the template the import is
// handed alone, beside another template, as a template the root links, and as
// the parameters file beside a template; it
// is seeded with every file of the testdata. No input panics, two reads of
// one input give one result, and the graph read gives its edges and its keys.
func FuzzReadTemplate(f *testing.F) {
	err := filepath.WalkDir("testdata", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, err := filepath.Rel("testdata", p)
		if err != nil {
			return err
		}
		f.Add(fixture(f, filepath.ToSlash(rel)))
		return nil
	})
	if err != nil {
		f.Fatal(err)
	}
	linking := []byte("AWSTemplateFormatVersion: \"2010-09-09\"\nResources:\n  Child:\n" +
		"    Type: AWS::CloudFormation::Stack\n    Properties:\n      TemplateURL: ./child.yaml\n")
	params := fixture(f, "params/template.yaml")
	f.Fuzz(func(t *testing.T, doc []byte) {
		requests := [][]aggrega.ImportSource{
			{src("template.yaml", doc)},
			{src("template.yaml", doc), src("other.yaml", params)},
			{src("main.yaml", linking), src("child.yaml", doc)},
			{src("template.yaml", params), src("params.json", doc)},
		}
		for _, sources := range requests {
			in := aggrega.ImportInput{Format: formatName, Sources: sources}
			g1, d1 := read(in, cloudimport.Settings{}, parse)
			g2, d2 := read(in, cloudimport.Settings{}, parse)
			if !reflect.DeepEqual(g1, g2) || !reflect.DeepEqual(d1, d2) {
				t.Fatalf("two reads of one input differ")
			}
			if g1 == nil {
				continue
			}
			cloudimport.Edges(g1)
			cloudimport.Format{Key: Key}.Keys(g1.Nodes)
		}
	})
}
