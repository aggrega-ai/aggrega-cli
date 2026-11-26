// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package cfntemplate

import (
	"fmt"
	"sync"

	"github.com/aggrega-ai/aggrega-cli/pkg/anchorcycle"
	"github.com/awslabs/goformation/v7/intrinsics"
	"go.yaml.in/yaml/v3"
)

// intrinsicsMu serializes every call into the intrinsics package. Its YAML
// processing registers its tag handlers into a package-level map on every call and
// reads that map while decoding, so two concurrent calls race on it and the runtime
// aborts the process. [Normalize] is the package's only call into it, so holding
// the lock there makes every caller safe, whatever goroutine it runs on.
var intrinsicsMu sync.Mutex

// Normalize reads one template, YAML with short forms or JSON, as CloudFormation
// reads it and returns sorted-key long-form JSON with a trailing newline. No
// intrinsic is evaluated: a Ref or GetAtt survives as its long-form pointer object,
// so the conversion is deterministic across runs.
//
// A quoted "!Ref X" stays a string. An unquoted date stays the text the template
// wrote. An anchor aliased inside its own content is refused, naming the anchor.
//
// A template that needs no rewrite reaches the intrinsics reader as the bytes it
// was given.
func Normalize(content []byte) ([]byte, error) {
	intrinsicsMu.Lock()
	defer intrinsicsMu.Unlock()
	prepared, err := prepare(content)
	if err != nil {
		return nil, err
	}
	out, err := intrinsics.ProcessYAML(prepared, &intrinsics.ProcessorOptions{NoProcess: true})
	if err != nil {
		return nil, err
	}
	return append(out, '\n'), nil
}

// prepare returns content with the rewrites the intrinsics reader does not make
// applied to its node tree, or content itself when none applies. Bytes that do not
// parse come back unchanged, so the parse failure surfaces from the intrinsics
// reader.
func prepare(content []byte) ([]byte, error) {
	var root yaml.Node
	if err := yaml.Unmarshal(content, &root); err != nil {
		return content, nil
	}
	if anchor, found := anchorcycle.Find(&root); found {
		return nil, fmt.Errorf("invalid YAML template: %s", anchorcycle.Explain(anchor))
	}
	if !retagTimestamps(&root) {
		return content, nil
	}
	out, err := yaml.Marshal(&root)
	if err != nil {
		return content, nil
	}
	return out, nil
}

// retagTimestamps rewrites every plain, untagged scalar under n that YAML resolves
// as a timestamp as a double-quoted string, so the text the template wrote
// survives, and reports whether it changed any. It walks Content only, never an
// alias.
func retagTimestamps(n *yaml.Node) bool {
	changed := false
	if n.Kind == yaml.ScalarNode {
		if n.Style == 0 && n.ShortTag() == "!!timestamp" {
			n.Tag = "!!str"
			n.Style = yaml.DoubleQuotedStyle
			changed = true
		}
		return changed
	}
	for _, c := range n.Content {
		if retagTimestamps(c) {
			changed = true
		}
	}
	return changed
}
