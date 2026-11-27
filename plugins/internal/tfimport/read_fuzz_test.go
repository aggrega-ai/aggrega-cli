// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package tfimport

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/plugins/internal/cloudimport"
)

// FuzzReadDir reads a fuzzed document as a .tf and as a .tf.json file of the
// imported directory, as the file of a directory a module call of the root
// reaches, and beneath .terraform/ beside a variable file holding it; it is
// seeded with every file of the testdata. No input panics, and two reads of
// one input give one result.
func FuzzReadDir(f *testing.F) {
	err := filepath.WalkDir("testdata", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		f.Add(b)
		return nil
	})
	if err != nil {
		f.Fatal(err)
	}
	calling := []byte("module \"child\" {\n  source = \"./child\"\n}\n")
	f.Fuzz(func(t *testing.T, doc []byte) {
		requests := [][]file{
			{{Name: "main.tf", Bytes: doc}},
			{{Name: "main.tf.json", Bytes: doc}},
			{{Name: "main.tf", Bytes: calling}, {Name: "child/main.tf", Bytes: doc}},
			{{Name: "main.tf", Bytes: calling}, {Name: "child/main.tf.json", Bytes: doc}},
			{{Name: "main.tf", Bytes: calling}, {Name: ".terraform/modules/child/main.tf", Bytes: doc},
				{Name: "terraform.tfvars", Bytes: doc}},
		}
		for _, files := range requests {
			if once, twice := readAll(files), readAll(files); once != twice {
				t.Fatalf("two reads of one input differ:\n%s\n%s", once, twice)
			}
		}
	})
}

// readAll reads files as an import does, choosing the root through
// selectRoot and reading each unit's directory, and writes what it reads as
// text.
func readAll(files []file) string {
	var b strings.Builder
	root, others, d := selectRoot(files, cloudimport.Settings{})
	fmt.Fprintf(&b, "root %+v, others %+v, diagnostic %+v\n", root, others, d)
	for _, u := range units(files) {
		cfg, diags := readDir(u.Path, files)
		fmt.Fprintf(&b, "%s: %+v\n", u.Path, diags)
		if cfg != nil {
			b.WriteString(render(cfg.Blocks, true))
			fmt.Fprintf(&b, "not read: %q\n", cfg.NotRead)
		}
	}
	return b.String()
}
