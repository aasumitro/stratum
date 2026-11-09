DROP TRIGGER audit_operator_change ON billing.subscription_addons;
REVOKE UPDATE (pending_quantity, pending_invoice_id) ON billing.subscription_addons FROM stratum_operator;
ALTER TABLE billing.payments DROP COLUMN unapplied;
