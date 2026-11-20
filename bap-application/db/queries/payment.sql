-- Queries for DOKU Checkout payment sessions (see migration
-- 017_payment_sessions.sql and internal/dokusvc / internal/dokunotifysvc).

-- name: InsertPaymentSession :one
-- Records that a DOKU checkout session was created for a transaction.
INSERT INTO payment_sessions (transaction_id, invoice_number, doku_token_id, amount)
VALUES ($1, $2, $3, $4)
RETURNING id, transaction_id, invoice_number, doku_token_id, status, gateway_ref, method, amount, paid_at, raw_notification, created_at, updated_at;

-- name: GetPaymentSessionByInvoiceNumber :one
-- Looks up the session a DOKU notification refers to, by the invoice number
-- this BAP generated it with.
SELECT id, transaction_id, invoice_number, doku_token_id, status, gateway_ref, method, amount, paid_at, raw_notification, created_at, updated_at
FROM payment_sessions
WHERE invoice_number = $1;

-- name: GetLatestPaidPaymentSession :one
-- The payment that justifies calling Confirm for this transaction -- there
-- should be at most one PAID session per transaction, but DESC + LIMIT 1
-- guards against a retried checkout leaving more than one row.
SELECT id, transaction_id, invoice_number, doku_token_id, status, gateway_ref, method, amount, paid_at, raw_notification, created_at, updated_at
FROM payment_sessions
WHERE transaction_id = $1
  AND status = 'PAID'
ORDER BY updated_at DESC
LIMIT 1;

-- name: UpdatePaymentSessionStatus :exec
-- Applies the outcome reported by a verified DOKU notification.
UPDATE payment_sessions
SET status = $2,
    gateway_ref = $3,
    method = $4,
    paid_at = $5,
    raw_notification = $6,
    updated_at = NOW()
WHERE invoice_number = $1;
