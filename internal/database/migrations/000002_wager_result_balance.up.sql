BEGIN;

ALTER TABLE wager_transactions
    DROP CONSTRAINT chk_wager_transactions_result_by_status,
    ADD CONSTRAINT chk_wager_transactions_result_by_status
        CHECK (
            (kind <> 'OPENING' AND status = 'PROCESSED'
                AND result_balance_amount IS NOT NULL
                AND result_balance_currency IS NOT NULL)
            OR (kind <> 'OPENING' AND status = 'REJECTED')
            OR ((kind = 'OPENING' OR status NOT IN ('PROCESSED', 'REJECTED'))
                AND result_balance_amount IS NULL
                AND result_balance_currency IS NULL)
        );

COMMIT;
