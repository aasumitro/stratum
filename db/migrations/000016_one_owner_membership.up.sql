-- At most one owner membership per organization. Ownership changes go through a transfer that
-- demotes the old owner before promoting the new one under the organization row lock; this makes
-- the database refuse any path that would leave two owners.
--
-- Rows left by races before those paths took the lock are reconciled first, so the index can be
-- built: organizations.owner_id names the owner. Where the owner_id member has a membership, any other
-- owner membership becomes admin, and theirs becomes an active owner (an owner can never be
-- suspended). An organization whose owner_id has no membership is left as it is. Both tables run under
-- FORCE ROW LEVEL SECURITY, which also binds a migrator that owns them but is not a superuser: lifted
-- for the reconciliation.
ALTER TABLE organization.memberships   NO FORCE ROW LEVEL SECURITY;
ALTER TABLE organization.organizations NO FORCE ROW LEVEL SECURITY;

UPDATE organization.memberships m
SET role = 'admin'
FROM organization.organizations o
WHERE o.id = m.organization_id AND m.role = 'owner' AND m.auth_sub <> o.owner_id
  AND EXISTS (
      SELECT 1 FROM organization.memberships x
      WHERE x.organization_id = o.id AND x.auth_sub = o.owner_id);

UPDATE organization.memberships m
SET role = 'owner', status = 'active'
FROM organization.organizations o
WHERE o.id = m.organization_id AND m.auth_sub = o.owner_id
  AND (m.role <> 'owner' OR m.status <> 'active');

ALTER TABLE organization.organizations FORCE ROW LEVEL SECURITY;
ALTER TABLE organization.memberships   FORCE ROW LEVEL SECURITY;

CREATE UNIQUE INDEX memberships_one_owner_per_organization
    ON organization.memberships (organization_id)
    WHERE role = 'owner';
