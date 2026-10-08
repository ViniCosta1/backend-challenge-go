BEGIN;

ALTER TABLE wager_transactions
    DROP CONSTRAINT chk_wager_transactions_reference_schedule,
    ALTER CONSTRAINT fk_wager_transactions_wallet
    NOT DEFERRABLE INITIALLY IMMEDIATE;

COMMIT;
