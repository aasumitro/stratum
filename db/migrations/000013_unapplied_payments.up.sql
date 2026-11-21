-- A payment the provider took that settled nothing is marked as such when it is recorded, so an
-- operator's review list does not depend on guessing from invoice states: a payment on an invoice an
-- operator already marked paid is the invoice's only payment row, yet it settled nothing.
ALTER TABLE billing.payments ADD COLUMN unapplied boolean NOT NULL DEFAULT false;

-- Both tables run under FORCE ROW LEVEL SECURITY, which also binds a migrator that owns them but is not
-- a superuser: without lifting it for the backfill, the updates would match no row.
ALTER TABLE billing.payments NO FORCE ROW LEVEL SECURITY;
ALTER TABLE billing.invoices NO FORCE ROW LEVEL SECURITY;

UPDATE billing.payments p
SET unapplied = true
FROM billing.invoices i
WHERE i.id = p.invoice_id AND i.status <> 'paid';

UPDATE billing.payments
SET unapplied = true
WHERE id IN (
    SELECT id FROM (
        SELECT id, row_number() OVER (PARTITION BY invoice_id ORDER BY paid_at, id) AS nth
        FROM billing.payments
    ) ranked
    WHERE nth > 1
);

ALTER TABLE billing.invoices FORCE ROW LEVEL SECURITY;
ALTER TABLE billing.payments FORCE ROW LEVEL SECURITY;

-- Studio's Void clears an add-on increase's pending columns when it voids the invoice gating them, as
-- the app's own void does. Only those two columns, and the table is captured like every other table an
-- operator writes.
GRANT UPDATE (pending_quantity, pending_invoice_id) ON billing.subscription_addons TO stratum_operator;

CREATE TRIGGER audit_operator_change AFTER INSERT OR UPDATE OR DELETE ON billing.subscription_addons
    FOR EACH ROW EXECUTE FUNCTION audit.capture_operator_change();
