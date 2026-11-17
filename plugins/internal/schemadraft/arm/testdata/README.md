# Bicep type file fixtures

## The file from Azure/bicep-types-az

`managedidentity-types.json` is the file
`generated/msi/microsoft.managedidentity/2024-11-30/types.json` of the repository
[Azure/bicep-types-az](https://github.com/Azure/bicep-types-az), at the commit
`672f5befa67776d06b9153b62693e34cfac6704a`, copied byte for byte. It holds the resource types
`Microsoft.ManagedIdentity/userAssignedIdentities`, its child
`Microsoft.ManagedIdentity/userAssignedIdentities/federatedIdentityCredentials`, and
`Microsoft.ManagedIdentity/identities`, which is writable at no scope. Its descriptions are Microsoft's. It is
distributed under the repository's licence, whose notice follows:

```text
MIT License

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
SOFTWARE
```

## The synthetic files

The other fixtures are synthetic: each is written in the format of a Bicep `types.json`, and every description in
them is written for these tests. The types they describe, under the `Microsoft.Example` namespace, do not exist.

- `vault.json` holds `Microsoft.Example/vaults` and its child `Microsoft.Example/vaults/keys`. A vault has a
  required `location`, a `name` with a pattern and bounds on its length, an open `tags` object, a required closed
  `sku`, an `identity` whose `principalId` and `tenantId` are read back, and a read-back `etag`. Its `properties`
  hold a required `tenantId`, a sensitive write-only `adminPassword`, a boolean, a deploy-time constant, an array
  of objects holding an array of an extensible enum, a write-only `createMode`, and the read-back `vaultUri`,
  `provisioningState` and `endpoints` object. A resource function stands beside the types.
- `shapes.json` holds `Microsoft.Example/shapes`, whose `properties`, a deploy-time constant, hold one member for
  each flag and each kind of type the reader reads, a member whose type refers back to itself, a chain of objects
  deeper than a draft lifts, and a member of a kind the reader does not know; and `Microsoft.Example/scripts`, whose
  body is a discriminated object whose kinds give `properties` two shapes.
- `scopes.json` holds one body under several types, each writable at a different set of scopes: tenant alone,
  extension alone, tenant and extension, subscription alone, management group alone, every scope, the tenant,
  management group and subscription scopes, and none.
- `golden/vault.component.yaml` and `golden/vault.recipe.yaml` are the Component and the Recipe an import of
  `Microsoft.Example/vaults` from `vault.json` drafts. `TestArmGolden` compares a draft with them; running it with
  `UPDATE_GOLDEN=1` writes the draft into them instead.
