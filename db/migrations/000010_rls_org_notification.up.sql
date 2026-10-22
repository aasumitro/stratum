-- Row-level security for the organization and notification schemas, mirroring billing's
-- (000004/000008). Policies key on two transaction-local settings:
--   app.organization_id  set by db.WithOrgTx for organization-keyed operations
--   app.auth_sub         set by db.WithAuthTx for a user's own rows across organizations
-- Unset, every policy matches nothing. NULLIF is required: a pooled connection that once set the
-- value locally reads it back as '' (not NULL) afterwards, and ''::uuid would raise instead of
-- matching nothing. The worker/webhook/unscoped roles bypass RLS; stratum_app does not.

ALTER TABLE organization.organizations      ENABLE ROW LEVEL SECURITY;
ALTER TABLE organization.organizations      FORCE ROW LEVEL SECURITY;
ALTER TABLE organization.memberships        ENABLE ROW LEVEL SECURITY;
ALTER TABLE organization.memberships        FORCE ROW LEVEL SECURITY;
ALTER TABLE organization.invitations        ENABLE ROW LEVEL SECURITY;
ALTER TABLE organization.invitations        FORCE ROW LEVEL SECURITY;
ALTER TABLE organization.webhook_endpoints  ENABLE ROW LEVEL SECURITY;
ALTER TABLE organization.webhook_endpoints  FORCE ROW LEVEL SECURITY;
ALTER TABLE organization.webhook_deliveries ENABLE ROW LEVEL SECURITY;
ALTER TABLE organization.webhook_deliveries FORCE ROW LEVEL SECURITY;
ALTER TABLE notification.messages           ENABLE ROW LEVEL SECURITY;
ALTER TABLE notification.messages           FORCE ROW LEVEL SECURITY;
ALTER TABLE notification.preferences        ENABLE ROW LEVEL SECURITY;
ALTER TABLE notification.preferences        FORCE ROW LEVEL SECURITY;
-- Worker-only idempotency table: enabled with no policy at all, which denies every
-- non-BYPASSRLS role.
ALTER TABLE notification.processed_events   ENABLE ROW LEVEL SECURITY;
ALTER TABLE notification.processed_events   FORCE ROW LEVEL SECURITY;

-- An organization is visible inside its own scope, or to a member reading their own
-- organizations (GET /organizations). Inserts must be scoped to the new organization's id, which
-- the creating transaction sets before inserting it.
CREATE POLICY org_read ON organization.organizations FOR SELECT
    USING (id = NULLIF(current_setting('app.organization_id', true), '')::uuid
           OR EXISTS (SELECT 1 FROM organization.memberships m
                      WHERE m.organization_id = organizations.id
                        AND m.auth_sub = current_setting('app.auth_sub', true)));
CREATE POLICY org_insert ON organization.organizations FOR INSERT
    WITH CHECK (id = NULLIF(current_setting('app.organization_id', true), '')::uuid);
CREATE POLICY org_update ON organization.organizations FOR UPDATE
    USING (id = NULLIF(current_setting('app.organization_id', true), '')::uuid)
    WITH CHECK (id = NULLIF(current_setting('app.organization_id', true), '')::uuid);
CREATE POLICY org_delete ON organization.organizations FOR DELETE
    USING (id = NULLIF(current_setting('app.organization_id', true), '')::uuid);

-- Never references organizations: the organizations read policy references memberships, and a
-- policy cycle between the two would recurse.
CREATE POLICY membership_isolation ON organization.memberships
    USING (organization_id = NULLIF(current_setting('app.organization_id', true), '')::uuid
           OR auth_sub = current_setting('app.auth_sub', true))
    WITH CHECK (organization_id = NULLIF(current_setting('app.organization_id', true), '')::uuid);

CREATE POLICY invitation_isolation ON organization.invitations
    USING (organization_id = NULLIF(current_setting('app.organization_id', true), '')::uuid)
    WITH CHECK (organization_id = NULLIF(current_setting('app.organization_id', true), '')::uuid);

CREATE POLICY webhook_endpoint_isolation ON organization.webhook_endpoints
    USING (organization_id = NULLIF(current_setting('app.organization_id', true), '')::uuid)
    WITH CHECK (organization_id = NULLIF(current_setting('app.organization_id', true), '')::uuid);

CREATE POLICY webhook_delivery_isolation ON organization.webhook_deliveries
    USING (endpoint_id IN (SELECT id FROM organization.webhook_endpoints
                           WHERE organization_id = NULLIF(current_setting('app.organization_id', true), '')::uuid))
    WITH CHECK (endpoint_id IN (SELECT id FROM organization.webhook_endpoints
                                WHERE organization_id = NULLIF(current_setting('app.organization_id', true), '')::uuid));

CREATE POLICY message_isolation ON notification.messages
    USING (auth_sub = current_setting('app.auth_sub', true))
    WITH CHECK (auth_sub = current_setting('app.auth_sub', true));

CREATE POLICY preference_isolation ON notification.preferences
    USING (auth_sub = current_setting('app.auth_sub', true))
    WITH CHECK (auth_sub = current_setting('app.auth_sub', true));
