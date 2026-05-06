// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package helmchart

import (
	"github.com/cognitio/aggrega-cli/internal/compiler/renderer"
)

// Compile-time assertion: the helm-chart engine satisfies
// renderer.ExternalArtifactsHandler. The marker dispatches Phase B.3 to
// the external-handler branch — inline `artifacts:` rejected; `artifactsFrom:`
// no-op at B.3 (the engine's pre-template injection path consumes those
// files outside Phase B). A regression breaking this satisfaction would
// surface as a build error here rather than a silent dispatch fall-through
// in evaluateArtifacts.
var _ renderer.ExternalArtifactsHandler = (*HelmEngine)(nil)
