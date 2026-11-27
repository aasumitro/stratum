ALTER TABLE billing.subscriptions
    DROP COLUMN schedule_resend_at,
    DROP COLUMN schedule_resend_count;
