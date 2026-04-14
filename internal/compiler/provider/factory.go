// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package provider

import (
	"errors"
	"fmt"

	"github.com/cognitio/aggrega-cli/internal/compiler/provider/compose"
	"github.com/cognitio/aggrega-cli/internal/compiler/provider/helm"
)

// ErrUnsupportedProvider is returned when no Engine is registered for the given provider name.
var ErrUnsupportedProvider = errors.New("unsupported provider")

// GetEngine returns the Engine implementation for a Release scope's provider field.
func GetEngine(providerName string) (Engine, error) {
	switch providerName {
	case "helm":
		return &helm.HelmEngine{}, nil
	case "compose":
		return &compose.ComposeEngine{}, nil
	default:
		return nil, fmt.Errorf("%w: %q", ErrUnsupportedProvider, providerName)
	}
}
