// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package cfncore

// The cloud-target annotation keys the cloudformation engine reads to shape a
// cross-stack reference between boundaries. Each key sits under the
// platform-reserved aggrega.ai/ annotation namespace. The values are opaque
// scalars the engine carries verbatim; this core never interprets their content.
const (
	annotationAWSAccount       = "aggrega.ai/aws-account"
	annotationAWSRegion        = "aggrega.ai/aws-region"
	annotationAWSAssumeRoleARN = "aggrega.ai/aws-assume-role-arn"
)

// CloudTargetAnnotations carries the three cloud-target annotation values one
// endpoint of a cross-boundary link declares, as opaque scalar strings. Each
// field is the empty string when the endpoint's placement annotations do not
// carry that key. The values are deterministic pointer strings the engine pipes
// into a cross-stack reference and never resolves against AWS state; this core
// reads them and passes them through without interpreting their content.
type CloudTargetAnnotations struct {
	// AWSAccount is the AWS account the node's boundary (stack) deploys into.
	AWSAccount string
	// AWSRegion is the AWS region the node's boundary (stack) deploys into.
	AWSRegion string
	// AWSAssumeRoleARN is the IAM role ARN a cross-account stack read assumes.
	AWSAssumeRoleARN string
}

// ReadCloudTargetAnnotationsFromMap extracts the three cloud-target annotations
// from an opaque scalar map — the placement annotations a cross-boundary link
// payload carries for one endpoint. A key absent from the map
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
