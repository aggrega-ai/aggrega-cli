# Tier-4 fixture: an inconsistent Manifest mapping beside an unrelated diagnostic

Carries two independent faults at once, which is the point of it:

1. **A Manifest mapping fault.** `manifest.yaml` maps `example/unused-server` onto
   `example-elsewhere-recipe`, whose `spec.implements` names a different type. The
   mapping-table consistency gate reports it.
2. **A parameter-contract fault.** The Aggregate declares
   `spec.parameters.port` as a required integer; the Release supplies the string
   `"not-an-integer"`. The resolution stage reports it, positioned at the `port`
   key in the Aggregate.

The mapped type is one no component *instance* uses, so the two faults do not
interact: the mapping fault cannot be the reason the parameter fault appears or
disappears.

It does still need a Component of its own in the catalog, and that is not
incidental. The mapping table drives source descent, so a mapped type with no
Component behind it fails the closure walk on an unresolvable URI — which is a
resolution failure, and therefore lands the fixture in exactly the truncated
single-diagnostic state the test exists to detect. `catalog/components/` carries
it for that reason.

The daemon must publish **both**, each on its own document: the mapping fault on
the Manifest, the parameter fault on the Aggregate. That is what the fixture exists to hold: the
mapping verdict reaches the daemon as a finding on the resolved closure rather
than as a resolution failure, and a resolution failure at that position replaces
a file's entire diagnostic set with one entry at line 1 and returns. Were the
verdict a failure, the parameter diagnostic — and every other thing the daemon had
to say about the buffer — would vanish on every keystroke. A test asserting only
that the mapping diagnostic appears would pass under that regression; asserting
the parameter diagnostic survives alongside it is what does not.
