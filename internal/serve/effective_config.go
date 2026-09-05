// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package serve

import (
	"context"
)

// effectiveConfigurationSetting is one configuration setting on the wire: its
// dotted name, its effective value rendered as a stable string, and the layer
// that determined it.
//
// Source is a config file's absolute path, an environment variable behind an
// "env:" prefix, or the built-in-defaults label — the same three shapes the
// command line attributes a setting to, produced by the same call, so the two
// surfaces cannot report different origins for the same setting.
//
// It carries no position within the source. A setting attributed to a config
// file does have one available, and it is deliberately not published here: an
// editor is a surface a person reads, and where in a file a setting sits is not
// yet something this daemon offers a person. Adding it is a separate step, not
// an omission to repair.
type effectiveConfigurationSetting struct {
	Field  string `json:"field"`
	Value  string `json:"value"`
	Source string `json:"source"`
}

// effectiveConfigurationResult is the aggrega/effectiveConfiguration response:
// the layers that contributed, lowest precedence first, and every attributed
// setting with its effective value and originating layer.
//
// Both fields are always arrays, empty rather than absent, so a client reads a
// workspace running entirely on the built-in defaults as an empty cascade
// rather than as a missing field.
//
// A Source on a setting is spelled exactly as the matching entry in Layers, so
// the two join directly. A setting the defaults determined names no layer in
// Layers, because the defaults are not a layer that contributed.
type effectiveConfigurationResult struct {
	Layers   []string                        `json:"layers"`
	Settings []effectiveConfigurationSetting `json:"settings"`
}

// dispatchEffectiveConfiguration reports the configuration in force for this
// session and where each setting came from.
//
// Takes no params: the answer is a property of the workspace the daemon
// resolved at startup. It reports that startup resolution rather than resolving
// again, so what a client reads is the configuration the daemon is itself
// acting on — the same resolution the handshake's workspace root and
// configuration layer describe.
//
// The attribution is not computed here. It is the workspace resolution's own
// per-value provenance, which the command line reports under the same call, so
// one implementation answers both surfaces and neither can drift from the
// other.
//
// The attributed set is narrower than the configuration schema, and a client
// must not read this response as covering every setting. It is a fixed
// enumeration, so a setting can be real, documented and carry a default while
// appearing nowhere here; a list or map is attributed as a whole to the layer
// that last changed it rather than per entry; and the signing requirement,
// which composes so that any layer turning it on wins, is attributed to the
// first layer that turned it on rather than the last that mentioned it.
func (s *Server) dispatchEffectiveConfiguration(ctx context.Context, req *jsonRPCRequest) error {
	if s.resolvedWorkspace == nil {
		return s.writeError(ctx, req.ID, jsonRPCInternalError,
			"the daemon holds no resolved workspace to report configuration for")
	}

	origins, err := s.resolvedWorkspace.ValueOrigins()
	if err != nil {
		return s.writeError(ctx, req.ID, jsonRPCInternalError,
			"read the effective configuration: "+err.Error())
	}

	settings := make([]effectiveConfigurationSetting, 0, len(origins))
	for _, o := range origins {
		settings = append(settings, effectiveConfigurationSetting{
			Field:  o.Field,
			Value:  o.Value,
			Source: o.Source,
		})
	}

	layers := s.resolvedWorkspace.ContributingLayers()
	if layers == nil {
		layers = []string{}
	}

	return s.writeResult(ctx, req.ID, effectiveConfigurationResult{
		Layers:   layers,
		Settings: settings,
	})
}
