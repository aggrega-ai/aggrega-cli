# Test data for the ARM template reader

Every template here is written for these tests, except those under `avm/`, which Microsoft publishes (see
[Azure Verified Modules](#azure-verified-modules)).

| Path | What it holds |
|---|---|
| `topology/one.json` | A template of language version 1.0 with line and block comments and a trailing comma: a PostgreSQL flexible server with a database nested in its resources, a storage account under a condition, and a container app whose environment reads the server's address through `reference(resourceId(…))`, composes a connection URL with `concat`, reads the storage account's key with `listKeys`, and holds a string escaped with `[[`. Names reach the resources through variables. |
| `topology/two.json` | The topology of `topology/one.json` in language version 2.0: each resource keyed by the symbol the 1.0 reading derives for it, the database written beside its server, references through symbolic names and `format`. The two read into one graph. |
| `windows/main.json` | `topology/one.json` byte for byte as a Windows tool saves it: opening with a UTF-8 byte-order mark, every line ending in a carriage return and a line feed. |
| `linked/main.json` | An entry template deploying `linked/modules/network.json` through a `Microsoft.Resources/deployments` resource linked by `templateLink.relativePath`, and a Key Vault reading the subnet ID the linked template outputs. |
| `linked/modules/network.json` | The linked template: a virtual network and the output naming its subnet. |
| `linked/main.parameters.json` | The parameters file of `linked/main.json`, supplying its one parameter. |
| `linked/notes.json` | A JSON file that is neither a template nor a parameters file. |
| `parameters/one.parameters.json` | A parameters file for `topology/one.json`: two values, and the administrator password read from a Key Vault secret by reference. |
| `refused/tenant.json` | A template of the tenant template schema. |
| `refused/experimental.json` | A template of an experimental language version. |
| `refused/keyed-resources.json` | A template of language version 1.0 whose resources are keyed by name, as only 2.0 keys them. |
| `refused/multiline.json` | A template holding a raw line break inside a string, which JSON does not admit. |

## Goldens

Each `golden/<case>/source/` holds the sources of one import, and `golden/<case>/want/` what the import of them for
the arm target writes: the Aggregate, the Release, the Manifest and the catalog entities drafted, as the importer
lays them out, its diagnostics in `diagnostics.txt` and its decisions in `decisions.txt`. A member written
`{"$avm": "<module>/<version>"}` stands for the compiled template of that Azure Verified Modules module, which the
test writes in its place from `avm/<module>/<version>/main.json`. Run the tests with `UPDATE_GOLDEN=1` to rewrite
`want/`.

| Case | What its sources hold |
|---|---|
| `raw-topology` | A PostgreSQL flexible server holding a database nested in its resources and placed in a subnet of a virtual network the template creates, and a container app reading the server's address into its environment; a parameters file naming the server. |
| `avm-topology` | The topology of `raw-topology` with the server deployed through the Azure Verified Modules module `avm/res/db-for-postgre-sql/flexible-server` 0.16.2, the app reading the module's `fqdn` output. |
| `drafted-from-source` | A Key Vault, a static web app no row maps, and a deployment script reading the vault's address. |
| `wrapped-module` | An Azure Data Explorer cluster deployed through the module `avm/res/kusto/cluster` 0.11.1, whose resource type no row maps, and a container app reading the module's `uri` output into its environment. |
| `existing` | A template of language version 2.0 reading a subnet and a Log Analytics workspace it does not create, from a PostgreSQL flexible server, an Application Insights component and a workbook no row maps. |
| `copy` | Storage accounts repeated a literal number of times, and Service Bus namespaces repeated as many times as a parameter says. |
| `condition` | A Redis cache deployed where one boolean parameter is true, and a Key Vault deployed under another condition. |
| `list-keys` | A storage account and a container app reading the account's key with `listKeys` and its blob endpoint with `reference`. |
| `key-vault-reference` | An Azure SQL logical server and its database written beside it, the administrator's password a secure parameter the parameters file reads from a Key Vault secret. |

## Azure Verified Modules

Each `avm/<module>/<version>/main.json` is the compiled template of an Azure Verified Modules module as the Microsoft
Container Registry serves it: the `application/vnd.ms.bicep.module.layer.v1+json` layer of the image
`mcr.microsoft.com/bicep/avm/res/<module>:<version>`, byte for byte. The modules are built from the
[Azure/bicep-registry-modules](https://github.com/Azure/bicep-registry-modules) repository, under `avm/res/<module>`.

| Path | Module, version | Layer digest |
|---|---|---|
| `avm/db-for-postgre-sql/flexible-server/0.9.1/main.json` | `avm/res/db-for-postgre-sql/flexible-server` 0.9.1: its telemetry named by the readable prefix `46d3xbcp.res.dbforpostgresql-flexibleserver`, written literally | `sha256:b6626d8b322ae6cc1c2bac4749a3224e91c4b59a69e1a4c42270873c90ec9174` |
| `avm/db-for-postgre-sql/flexible-server/0.16.2/main.json` | `avm/res/db-for-postgre-sql/flexible-server` 0.16.2: its telemetry named through the variable `telemetryIdPrefix`, holding the hashed prefix `46d3xbcp.res.568937f`; its own child modules send telemetry too, stating no version | `sha256:1f67e24aa4c91515f37b4d7351c7d3602c72ec7e58acdc2fa66354f7d2d09e9b` |
| `avm/kusto/cluster/0.11.1/main.json` | `avm/res/kusto/cluster` 0.11.1, an Azure Data Explorer cluster: a hashed prefix the module index lists, of a resource type no arm Recipe of the catalog realizes | `sha256:35808cafc1fd44138bd93d6decf37cbcf22132a2b5765aaa492a095355e297e8` |
| `avm/maps/account/0.2.2/main.json` | `avm/res/maps/account` 0.2.2, an Azure Maps account: a readable prefix the module index does not list, of a resource type no arm Recipe of the catalog realizes | `sha256:4c19b24306a528cb252912d7d7a71b52ad30b47438ebc19d03200be48613e2cb` |

`avm/db-for-postgre-sql/flexible-server/main/main.json` is not published: it is the 0.16.2 layer with one edit, the
version its telemetry resource's name states, `replace('0.16.2', '.', '-')`, written as `replace('-..--..-', '.', '-')`,
the placeholder a module built from its source states in place of a version.

These files are licensed under the MIT License:

```
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
