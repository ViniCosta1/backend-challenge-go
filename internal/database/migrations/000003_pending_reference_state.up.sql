BEGIN;

-- Existing pending records become immediately eligible for durable recovery.
UPDATE wager_transactions
SET reference_next_attempt_at = updated_at
WHERE status = 'PENDING_REFERENCE' AND reference_next_attempt_at IS NULL;

ALTER TABLE wager_transactions
    ADD CONSTRAINT chk_wager_transactions_reference_schedule
        CHECK (
            (status = 'PENDING_REFERENCE'
                AND reference_next_attempt_at IS NOT NULL
                AND reference_next_attempt_at >= created_at)
            OR (status <> 'PENDING_REFERENCE' AND reference_next_attempt_at IS NULL)
        );

-- Reservation precedes FOR UPDATE. Deferring this FK avoids competing
-- KEY SHARE locks acquired by INSERT before both writers upgrade the wallet lock.
-- Referential integrity is still enforced at commit.
ALTER TABLE wager_transactions
    ALTER CONSTRAINT fk_wager_transactions_wallet
    DEFERRABLE INITIALLY DEFERRED;

COMMIT;
