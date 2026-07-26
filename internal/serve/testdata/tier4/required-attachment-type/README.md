# tier4 fixture — required attachment types

Two root components, each pulling in a Component catalog entity that declares a
presence mandate (`features.aggrega.ai/requiredType.<label>`):

- `unmonitored` (`example/needs-exporter`) declares a well-formed mandate for
  `example/exporter` and attaches nothing, so the mandate is unmet.
- `broken` (`example/broken-requirement`) declares a mandate whose value is not
  a Feature type URI, so the declaration itself is malformed.

The fixture exists so both diagnostics can be observed on the language-server
path — an unmet mandate and a malformed declaration are distinct classes and
must both reach the editor, positioned on the component instance that pulled
the type in.
