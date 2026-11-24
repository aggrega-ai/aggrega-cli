// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package main

import (
	"bytes"
	"encoding/csv"
	"errors"
	"fmt"
	gofmt "go/format"
	"io"
	"os"
	"regexp"
	"slices"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/plugins/internal/armimport"
)

// Where the Azure Verified Modules programme publishes its index of Bicep
// resource modules, the file the index is read from, and the telemetry prefix
// that marks a resource module.
const (
	avmIndexRepository = "https://github.com/Azure/Azure-Verified-Modules"
	avmIndexFile       = "docs/static/module-indexes/BicepResourceModules.csv"
	avmResourcePrefix  = "46d3xbcp.res."
)

// avmColumns are the columns of the module index file the generator reads.
var avmColumns = []string{"ProviderNamespace", "ResourceType", "ModuleName", "TelemetryIdPrefix"}

// commitName matches a full commit name.
var commitName = regexp.MustCompile(`^[0-9a-f]{40}$`)

// avmLicense is the licence the programme's repository carries, which the
// generated index reproduces with its copyright holder.
const avmLicense = `MIT License

Copyright (c) Microsoft Corporation.

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all
copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
SOFTWARE`

// runAVMIndex reads the module index file at csvPath, which is the file avmIndexFile
// names at commit of the programme's repository, and writes the Go source of
// the index to out. Nothing is written where the file or the commit is refused.
func runAVMIndex(csvPath, commit, out string) error {
	if !commitName.MatchString(commit) {
		return fmt.Errorf("-avm-commit names %q, which is not a full commit name of %s: name the 40 hexadecimal "+
			"characters of the commit the file was downloaded at", commit, avmIndexRepository)
	}
	f, err := os.Open(csvPath)
	if err != nil {
		return fmt.Errorf("read the module index file: %w", err)
	}
	defer f.Close()
	mods, err := readAVMIndex(f)
	if err != nil {
		return fmt.Errorf("read %s: %w", csvPath, err)
	}
	src, err := renderAVMIndex(mods, commit)
	if err != nil {
		return err
	}
	if out == "" {
		_, err = os.Stdout.Write(src)
		return err
	}
	return os.WriteFile(out, src, 0o644)
}

// readAVMIndex reads a module index file as the programme publishes it, and
// returns one module for each row whose telemetry prefix is a resource
// module's, ordered by prefix. A row of a resource module that names no
// module, namespace or type, or repeats a prefix or a module, is refused,
// naming it; so is a file lacking a column the generator reads.
func readAVMIndex(r io.Reader) ([]armimport.AVMModule, error) {
	cr := csv.NewReader(r)
	header, err := cr.Read()
	if errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("the file holds no header row naming %s", strings.Join(avmColumns, ", "))
	}
	if err != nil {
		return nil, err
	}
	header[0] = strings.TrimPrefix(header[0], "\ufeff")
	at := map[string]int{}
	for _, name := range avmColumns {
		i := slices.Index(header, name)
		if i < 0 {
			return nil, fmt.Errorf("the file has no column %s (its columns are %s)", name, strings.Join(header, ", "))
		}
		at[name] = i
	}
	var mods []armimport.AVMModule
	prefixes, modules := map[string]bool{}, map[string]bool{}
	for {
		rec, err := cr.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, err
		}
		field := func(name string) string { return strings.TrimSpace(rec[at[name]]) }
		prefix := field("TelemetryIdPrefix")
		if !strings.HasPrefix(prefix, avmResourcePrefix) {
			continue
		}
		m := armimport.AVMModule{Prefix: prefix, Module: field("ModuleName"), Namespace: field("ProviderNamespace"),
			Type: field("ResourceType")}
		for _, c := range []struct{ value, what string }{{m.Module, "module"}, {m.Namespace, "provider namespace"},
			{m.Type, "resource type"}} {
			if c.value == "" {
				return nil, fmt.Errorf("the row of the prefix %s names no %s", prefix, c.what)
			}
		}
		if prefixes[prefix] {
			return nil, fmt.Errorf("the prefix %s is in the file twice", prefix)
		}
		if modules[m.Module] {
			return nil, fmt.Errorf("the module %s is in the file twice, the second time under the prefix %s",
				m.Module, prefix)
		}
		prefixes[prefix], modules[m.Module] = true, true
		mods = append(mods, m)
	}
	slices.SortFunc(mods, func(a, b armimport.AVMModule) int { return strings.Compare(a.Prefix, b.Prefix) })
	return mods, nil
}

// renderAVMIndex writes mods, ordered as given, as the Go source of the
// armimport package that holds the index, naming commit as the commit of the
// programme's repository the file was read at and carrying the licence the
// repository states.
func renderAVMIndex(mods []armimport.AVMModule, commit string) ([]byte, error) {
	var b bytes.Buffer
	b.WriteString("// SPDX-License-Identifier: Apache-2.0\n")
	b.WriteString("// Copyright 2026 Cognitio s.r.l.\n\n")
	b.WriteString("// Code generated by cloudtable from the Azure Verified Modules module index. DO NOT EDIT.\n//\n")
	fmt.Fprintf(&b, "// Source: %s\n// File: %s\n// Commit: %s (gitleaks:allow a public commit name)\n//\n", avmIndexRepository,
		avmIndexFile, commit)
	b.WriteString("// Regenerate it deliberately, from a copy of the file downloaded at the commit to name, from the\n")
	b.WriteString("// aggrega-cli module root; no test fetches it:\n//\n")
	b.WriteString("//\tgo run ./plugins/internal/cloudimport/cloudtable -avm-index BicepResourceModules.csv " +
		"-avm-commit COMMIT -out plugins/internal/armimport/avmindex_gen.go\n//\n")
	b.WriteString("// This file is generated and licensed Apache-2.0; the rows below are derived from an MIT-licensed file,\n")
	b.WriteString("// whose notice follows:\n//\n")
	for _, line := range strings.Split(avmLicense, "\n") {
		if line == "" {
			b.WriteString("//\n")
		} else {
			b.WriteString("//\t" + line + "\n")
		}
	}
	b.WriteString("\npackage armimport\n\n")
	b.WriteString("// AVMIndexCommit is the commit of the Azure Verified Modules repository whose module index\n")
	b.WriteString("// file the index was generated from.\n")
	fmt.Fprintf(&b, "const AVMIndexCommit = %q // gitleaks:allow a public commit name\n\n", commit)
	b.WriteString("// avmIndex is the programme's resource modules, one row for each telemetry prefix, ordered by\n")
	b.WriteString("// prefix.\n")
	b.WriteString("var avmIndex = []AVMModule{\n")
	for _, m := range mods {
		fmt.Fprintf(&b, "{Prefix: %q, Module: %q, Namespace: %q, Type: %q},\n", m.Prefix, m.Module, m.Namespace, m.Type)
	}
	b.WriteString("}\n")
	formatted, err := gofmt.Source(b.Bytes())
	if err != nil {
		return nil, fmt.Errorf("format the generated index: %w", err)
	}
	return formatted, nil
}
