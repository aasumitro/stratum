-- How many times the hourly reconcile has re-sent a due schedule's apply message, and when it
-- may send the next one (an hourly tick up to 5 minutes early still counts). A schedule whose apply fails every time is re-sent at
-- a doubling interval, at most a day apart, instead of dead-lettering again every hour. Both reset
-- whenever the schedule is written, cleared, applied or moved.
ALTER TABLE billing.subscriptions
    ADD COLUMN schedule_resend_count integer NOT NULL DEFAULT 0,
    ADD COLUMN schedule_resend_at timestamptz;
