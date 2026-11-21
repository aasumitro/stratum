-- The date a scheduled plan downgrade or add-on decrease takes effect. It is the period end the
-- schedule was made against, and stays put when paying the renewal moves period_end on, so the
-- change applies at the boundary the owner was shown rather than at the next renewal invoice.
ALTER TABLE billing.subscriptions ADD COLUMN scheduled_effective_at timestamptz;

-- FORCE ROW LEVEL SECURITY also binds a migrator that owns the table but is not a superuser: lifted for
-- the backfill, or it would match no row.
ALTER TABLE billing.subscriptions NO FORCE ROW LEVEL SECURITY;

UPDATE billing.subscriptions s
SET scheduled_effective_at = s.period_end
WHERE s.scheduled_plan IS NOT NULL
   OR EXISTS (
       SELECT 1 FROM billing.subscription_addons sa
       WHERE sa.subscription_id = s.id AND sa.scheduled_quantity IS NOT NULL
   );

ALTER TABLE billing.subscriptions FORCE ROW LEVEL SECURITY;
