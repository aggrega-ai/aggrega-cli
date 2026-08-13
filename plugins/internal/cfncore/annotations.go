// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package cfncore

import "github.com/aggrega-ai/aggrega-cli/internal/compiler/renderctx"

// The cloud-target annotation keys the cloudformation engine reads to shape a
// cross-stack reference between boundaries. Each key sits under the
// platform-reserved aggrega.ai/ annotation namespace. The values are opaque
// scalars the engine carries verbatim; this core never interprets their content.
const (
	annotationAWSAccount       = "aggrega.ai/aws-account"
	annotationAWSRegion        = "aggrega.ai/aws-region"
	annotationAWSAssumeRoleARN = "aggrega.ai/aws-assume-role-arn"
)

// CloudTargetAnnotations carries the three cloud-target annotation values a
// render context supplies for one node, as opaque scalar strings. Each field is
// the empty string when the annotation is absent from both the node-scoped
// Release bucket and the boundary bucket. The values are deterministic pointer
// strings the engine pipes into a cross-stack reference and never resolves
// against AWS state; this core reads them and passes them through without
// interpreting their content.
type CloudTargetAnnotations struct {
	// AWSAccount is the AWS account the node's boundary (stack) deploys into.
	AWSAccount string
	// AWSRegion is the AWS region the node's boundary (stack) deploys into.
	AWSRegion string
	// AWSAssumeRoleARN is the IAM role ARN a cross-account stack read assumes.
	AWSAssumeRoleARN string
}

// ReadCloudTargetAnnotations extracts the three cloud-target annotations from a
// render context. It reads the node-scoped Release annotation bucket and the
// boundary annotation bucket, applying component-over-boundary precedence: for
// each key the first bucket in precedence order that declares it — the
// node-scoped Release bucket, then the boundary bucket — supplies the value, and
// a key absent from both yields the empty string. The returned values stay
// opaque: this function does not inspect or validate their content.
func ReadCloudTargetAnnotations(ctx renderctx.RenderContext) CloudTargetAnnotations {
	return CloudTargetAnnotations{
		AWSAccount:       resolveCloudTargetAnnotation(ctx, annotationAWSAccount),
		AWSRegion:        resolveCloudTargetAnnotation(ctx, annotationAWSRegion),
		AWSAssumeRoleARN: resolveCloudTargetAnnotation(ctx, annotationAWSAssumeRoleARN),
	}
}

// ReadCloudTargetAnnotationsFromMap extracts the three cloud-target annotations
// from an opaque scalar map — the placement annotations a cross-boundary link
// payload carries for the consumed (peer) endpoint. A key absent from the map
// yields the empty string. The values stay opaque: this function does not
// inspect or validate their content; it reads the three keys the cross-stack
// reference shape needs and passes them through unresolved.
func ReadCloudTargetAnnotationsFromMap(m map[string]string) CloudTargetAnnotations {
	return CloudTargetAnnotations{
		AWSAccount:       m[annotationAWSAccount],
		AWSRegion:        m[annotationAWSRegion],
		AWSAssumeRoleARN: m[annotationAWSAssumeRoleARN],
	}
}

// resolveCloudTargetAnnotation returns the value for one annotation key under
// component-over-boundary precedence: the node-scoped Release bucket wins over
// the boundary bucket, and a key present in neither yields the empty string.
func resolveCloudTargetAnnotation(ctx renderctx.RenderContext, key string) string {
	if v, ok := ctx.Annotations.Release.Get(key); ok {
		return v
	}
	if v, ok := ctx.Boundary.Annotations.Get(key); ok {
		return v
	}
	return ""
}
