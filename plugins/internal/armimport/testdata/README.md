# Test data for the ARM template reader

Every template here is written for these tests; none is published by Microsoft.

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
