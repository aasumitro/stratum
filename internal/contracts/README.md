Package contracts defines the interfaces and projection types that
modules use to talk to each other. This package is the ONLY legal
cross-module import — internal/modules/billing may import
internal/contracts, but must never import internal/modules/org directly.

Two rules keep this package from becoming a dumping ground:

1. Contracts define projections, not domain models. OrgInfo below is
   NOT org.domain.Org — it's the minimal subset of org data that other
   modules are allowed to depend on. If billing needs a new org field,
   add it here deliberately; don't widen the projection just because
   it would be convenient.

2. Every interface here is implemented by exactly one module's
   module.go and consumed by N other modules. The implementation lives
   in the owning module; this package only holds the shape.