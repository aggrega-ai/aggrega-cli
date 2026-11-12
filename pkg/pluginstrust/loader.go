// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package pluginstrust

import (
	"fmt"
	"os"

	"github.com/aggrega-ai/aggrega-cli/pkg/validator/envelope"
	"go.yaml.in/yaml/v3"
)

// Load reads the trust record at path, validates it against the embedded JSON
// Schema, and returns the typed value.
//
// Return contract:
//   - A read failure, a schema-validation failure, or a typed-unmarshal failure
//     returns (nil, err).
//   - On success it returns (*Trust, nil).
//
// A schema violation is returned as the structured envelope violation the
// validating layer produced, unchanged — an *envelope.Error carrying every
// finding with its position in the file. No condition of this package's own is
// registered for it: "this document failed its kind's schema" is one condition
// with one home, and a second sentinel meaning the same thing would give a
// caller two things to match on for one failure.
//
// File absence is returned as the underlying read error, so a caller can
// recognise it with errors.Is against fs.ErrNotExist and decide what absence
// means for it. It means no resolution has recorded anything yet, which is a
// first-run state rather than a fault: the record is created by the resolution
// that first has something to record, not authored ahead of it.
func Load(path string) (*Trust, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("pluginstrust: read %s: %w", path, err)
	}

	if _, err := envelope.ValidateBytes(envelope.KindPluginsTrust, data, path); err != nil {
		return nil, err
	}

	t := &Trust{}
	if err := yaml.Unmarshal(data, t); err != nil {
		return nil, fmt.Errorf("pluginstrust: typed unmarshal %s: %w", path, err)
	}
	return t, nil
}
