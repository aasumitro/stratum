-- The exact body of an outbound webhook delivery, so a retry resends the same bytes instead of a
-- {id, type} payload rebuilt from the row. Nullable: deliveries recorded before it keep the rebuilt
-- retry. The delivery retention sweep removes it with the row.
ALTER TABLE organization.webhook_deliveries ADD COLUMN payload bytea;
