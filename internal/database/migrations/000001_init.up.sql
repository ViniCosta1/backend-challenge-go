CREATE TABLE wallets (
    id UUID PRIMARY KEY,
    player_id UUID NOT NULL,
    currency VARCHAR(3) NOT NULL,
    balance_amount BIGINT NOT NULL,
    version BIGINT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,

    CONSTRAINT uq_wallets_player_currency
        UNIQUE (player_id, currency),
    CONSTRAINT chk_wallets_currency
        CHECK (currency = 'BRL'),
    CONSTRAINT chk_wallets_balance_non_negative
        CHECK (balance_amount >= 0),
    CONSTRAINT chk_wallets_version_positive
        CHECK (version >= 1),
    CONSTRAINT chk_wallets_timestamps
        CHECK (updated_at >= created_at)
);

CREATE TABLE wager_transactions (
    id UUID PRIMARY KEY,

    provider_id TEXT,
    external_transaction_id TEXT,
    idempotency_key TEXT,
    payload_hash VARCHAR(64),

    wallet_id UUID NOT NULL,
    player_id UUID NOT NULL,

    round_id TEXT,
    game_id TEXT,

    kind VARCHAR(16) NOT NULL,
    money_amount BIGINT NOT NULL,
    currency VARCHAR(3) NOT NULL,

    reference_external_transaction_id TEXT,
    reference_transaction_id UUID,

    status VARCHAR(32) NOT NULL,
    failure_code TEXT,

    result_balance_amount BIGINT,
    result_balance_currency VARCHAR(3),

    reference_attempts INTEGER NOT NULL DEFAULT 0,
    reference_next_attempt_at TIMESTAMPTZ,

    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,

    CONSTRAINT fk_wager_transactions_wallet
        FOREIGN KEY (wallet_id)
        REFERENCES wallets (id),
    CONSTRAINT fk_wager_transactions_reference
        FOREIGN KEY (reference_transaction_id)
        REFERENCES wager_transactions (id),

    CONSTRAINT chk_wager_transactions_kind
        CHECK (kind IN (
            'OPENING',
            'BET',
            'WIN',
            'LOSS',
            'REFUND',
            'ROLLBACK'
        )),
    CONSTRAINT chk_wager_transactions_status
        CHECK (status IN (
            'PENDING',
            'PENDING_REFERENCE',
            'PROCESSED',
            'REJECTED',
            'FAILED'
        )),
    CONSTRAINT chk_wager_transactions_currency
        CHECK (currency = 'BRL'),
    CONSTRAINT chk_wager_transactions_reference_attempts
        CHECK (reference_attempts >= 0),
    CONSTRAINT chk_wager_transactions_timestamps
        CHECK (updated_at >= created_at),

    CONSTRAINT chk_wager_transactions_money_amount
        CHECK (
            (kind = 'LOSS' AND money_amount = 0)
            OR
            (
                kind IN ('OPENING', 'BET', 'WIN', 'REFUND', 'ROLLBACK')
                AND money_amount > 0
            )
        ),
    CONSTRAINT chk_wager_transactions_reversal_reference
        CHECK (
            kind NOT IN ('REFUND', 'ROLLBACK')
            OR reference_external_transaction_id IS NOT NULL
        ),
    CONSTRAINT chk_wager_transactions_origin_metadata
        CHECK (
            (
                kind = 'OPENING'
                AND provider_id IS NULL
                AND external_transaction_id IS NULL
                AND idempotency_key IS NULL
                AND payload_hash IS NULL
                AND round_id IS NULL
                AND game_id IS NULL
                AND reference_external_transaction_id IS NULL
                AND reference_transaction_id IS NULL
                AND status = 'PROCESSED'
            )
            OR
            (
                kind <> 'OPENING'
                AND provider_id IS NOT NULL
                AND external_transaction_id IS NOT NULL
                AND idempotency_key IS NOT NULL
                AND payload_hash IS NOT NULL
                AND round_id IS NOT NULL
                AND game_id IS NOT NULL
            )
        ),
    CONSTRAINT chk_wager_transactions_reference_pair
        CHECK (
            reference_transaction_id IS NULL
            OR reference_external_transaction_id IS NOT NULL
        ),
    CONSTRAINT chk_wager_transactions_pending_reference
        CHECK (
            status <> 'PENDING_REFERENCE'
            OR reference_external_transaction_id IS NOT NULL
        ),
    CONSTRAINT chk_wager_transactions_processed_reference
        CHECK (
            status <> 'PROCESSED'
            OR reference_external_transaction_id IS NULL
            OR reference_transaction_id IS NOT NULL
        ),

    CONSTRAINT chk_wager_transactions_result_pair
        CHECK (
            (
                result_balance_amount IS NULL
                AND result_balance_currency IS NULL
            )
            OR
            (
                result_balance_amount IS NOT NULL
                AND result_balance_currency IS NOT NULL
                AND result_balance_amount >= 0
                AND result_balance_currency = 'BRL'
            )
        ),
    CONSTRAINT chk_wager_transactions_result_by_status
        CHECK (
            (
                status = 'PROCESSED'
                AND kind <> 'OPENING'
                AND result_balance_amount IS NOT NULL
                AND result_balance_currency IS NOT NULL
            )
            OR
            (
                (status <> 'PROCESSED' OR kind = 'OPENING')
                AND result_balance_amount IS NULL
                AND result_balance_currency IS NULL
            )
        ),
    CONSTRAINT chk_wager_transactions_failure_code
        CHECK (
            (
                status IN ('REJECTED', 'FAILED')
                AND failure_code IS NOT NULL
            )
            OR
            (
                status IN ('PENDING', 'PENDING_REFERENCE', 'PROCESSED')
                AND failure_code IS NULL
            )
        )
);

CREATE UNIQUE INDEX ux_wager_transactions_provider_external_transaction
    ON wager_transactions (provider_id, external_transaction_id)
    WHERE provider_id IS NOT NULL;

CREATE UNIQUE INDEX ux_wager_transactions_provider_idempotency_key
    ON wager_transactions (provider_id, idempotency_key)
    WHERE provider_id IS NOT NULL;

CREATE UNIQUE INDEX ux_wager_transactions_opening_wallet
    ON wager_transactions (wallet_id)
    WHERE kind = 'OPENING';

CREATE UNIQUE INDEX ux_wager_transactions_processed_reversal_reference
    ON wager_transactions (reference_transaction_id)
    WHERE status = 'PROCESSED'
      AND kind IN ('REFUND', 'ROLLBACK')
      AND reference_transaction_id IS NOT NULL;

CREATE INDEX ix_wager_transactions_wallet_id
    ON wager_transactions (wallet_id);

CREATE INDEX ix_wager_transactions_pending_reference_next_attempt
    ON wager_transactions (reference_next_attempt_at, id)
    WHERE status = 'PENDING_REFERENCE';

CREATE TABLE wallet_ledger_entries (
    id UUID PRIMARY KEY,
    wallet_id UUID NOT NULL,
    transaction_id UUID NOT NULL,
    direction VARCHAR(8) NOT NULL,
    money_amount BIGINT NOT NULL,
    currency VARCHAR(3) NOT NULL,
    balance_before BIGINT NOT NULL,
    balance_after BIGINT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL,

    CONSTRAINT fk_wallet_ledger_entries_wallet
        FOREIGN KEY (wallet_id)
        REFERENCES wallets (id),
    CONSTRAINT fk_wallet_ledger_entries_transaction
        FOREIGN KEY (transaction_id)
        REFERENCES wager_transactions (id),
    CONSTRAINT uq_wallet_ledger_entries_wallet_transaction
        UNIQUE (wallet_id, transaction_id),
    CONSTRAINT chk_wallet_ledger_entries_direction
        CHECK (direction IN ('DEBIT', 'CREDIT')),
    CONSTRAINT chk_wallet_ledger_entries_money_positive
        CHECK (money_amount > 0),
    CONSTRAINT chk_wallet_ledger_entries_currency
        CHECK (currency = 'BRL'),
    CONSTRAINT chk_wallet_ledger_entries_balances_non_negative
        CHECK (balance_before >= 0 AND balance_after >= 0),
    CONSTRAINT chk_wallet_ledger_entries_balance_equation
        CHECK (
            (
                direction = 'DEBIT'
                AND balance_after = balance_before - money_amount
            )
            OR
            (
                direction = 'CREDIT'
                AND balance_after = balance_before + money_amount
            )
        )
);

CREATE INDEX ix_wallet_ledger_entries_wallet_created_id
    ON wallet_ledger_entries (wallet_id, created_at, id);

CREATE FUNCTION prevent_wallet_ledger_entries_mutation()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
    RAISE EXCEPTION 'wallet_ledger_entries is append-only; % is not allowed', TG_OP
        USING ERRCODE = '55000';
END;
$$;

CREATE TRIGGER trg_wallet_ledger_entries_append_only
BEFORE UPDATE OR DELETE ON wallet_ledger_entries
FOR EACH ROW
EXECUTE FUNCTION prevent_wallet_ledger_entries_mutation();

CREATE TABLE inbox_messages (
    consumer_name TEXT NOT NULL,
    message_id TEXT NOT NULL,
    payload_hash VARCHAR(64) NOT NULL,
    received_at TIMESTAMPTZ NOT NULL,
    completed_at TIMESTAMPTZ,

    CONSTRAINT pk_inbox_messages
        PRIMARY KEY (consumer_name, message_id),
    CONSTRAINT chk_inbox_messages_timestamps
        CHECK (completed_at IS NULL OR completed_at >= received_at)
);

CREATE TABLE outbox_events (
    event_id UUID PRIMARY KEY,
    aggregate_id UUID NOT NULL,
    event_type TEXT NOT NULL,
    correlation_id TEXT NOT NULL,
    causation_id TEXT,
    version INTEGER NOT NULL,
    payload JSONB NOT NULL,
    occurred_at TIMESTAMPTZ NOT NULL,
    attempts INTEGER NOT NULL DEFAULT 0,
    next_attempt_at TIMESTAMPTZ NOT NULL,
    published_at TIMESTAMPTZ,

    CONSTRAINT chk_outbox_events_type
        CHECK (event_type IN (
            'WagerTransactionProcessed',
            'WagerTransactionRejected',
            'WalletBalanceChanged',
            'WagerTransactionPendingReference'
        )),
    CONSTRAINT chk_outbox_events_version
        CHECK (version >= 1),
    CONSTRAINT chk_outbox_events_attempts
        CHECK (attempts >= 0),
    CONSTRAINT chk_outbox_events_next_attempt
        CHECK (next_attempt_at >= occurred_at),
    CONSTRAINT chk_outbox_events_published_at
        CHECK (published_at IS NULL OR published_at >= occurred_at)
);

CREATE INDEX ix_outbox_events_pending_next_attempt
    ON outbox_events (next_attempt_at, event_id)
    WHERE published_at IS NULL;
