BEGIN;

-- Deliberately fails if rejection snapshots exist: discarding historical
-- financial results to restore the old constraint would break audit/replay.
ALTER TABLE wager_transactions
    DROP CONSTRAINT chk_wager_transactions_result_by_status,
    ADD CONSTRAINT chk_wager_transactions_result_by_status
        CHECK (
            (status = 'PROCESSED' AND kind <> 'OPENING'
                AND result_balance_amount IS NOT NULL
                AND result_balance_currency IS NOT NULL)
            OR ((status <> 'PROCESSED' OR kind = 'OPENING')
                AND result_balance_amount IS NULL
                AND result_balance_currency IS NULL)
        );

COMMIT;
