# SPDX-License-Identifier: Apache-2.0
# Copyright 2026 Cognitio s.r.l.
#
# A house rule over the files a build writes, read from a CloudFormation template
# and from an Azure Resource Manager template: a database instance stores its data
# encrypted, and a container image names a fixed version.
#
# It is written twice, here and in house-rule.cel, each on its own from this
# description: a deliberate near-duplicate, so that the two engines reaching one
# verdict is a result and not a copy.
package house

import rego.v1

deny contains msg if {
    some artifact in input.artifacts
    some doc in artifact.documents
    some _, resource in doc.Resources
    resource.Type == "AWS::RDS::DBInstance"
    not resource.Properties.StorageEncrypted == true
    msg := sprintf("%v: a database instance stores its data unencrypted", [artifact.path])
}

# An image names no fixed version when it carries no digest and either ends in
# the tag latest or carries no tag: no ":" after its last "/".
unpinned(image) if {
    not contains(image, "@")
    endswith(image, ":latest")
}

unpinned(image) if {
    not contains(image, "@")
    not regex.match(`:[^/]+$`, image)
}

deny contains msg if {
    some artifact in input.artifacts
    some doc in artifact.documents
    is_object(doc.resources)
    some _, resource in doc.resources
    resource.type == "Microsoft.App/containerApps"
    some container in resource.properties.template.containers
    unpinned(container.image)
    msg := sprintf("%v: a container runs an image with no tag or the tag latest", [artifact.path])
}
