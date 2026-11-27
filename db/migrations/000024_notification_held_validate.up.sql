-- 000022 added the status check NOT VALID to skip a full scan under ACCESS EXCLUSIVE. VALIDATE takes only
-- SHARE UPDATE EXCLUSIVE, so writes continue while it scans; it cannot fail, since every row predating
-- 000022 already met the narrower check.
ALTER TABLE notification.messages VALIDATE CONSTRAINT messages_status_check;
