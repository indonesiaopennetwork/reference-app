-- 019_qr_order_lines.sql
-- The buyer now chooses which of a packet's items to order, and how many.
-- An order keeps its own lines (candidate lines with the chosen quantities);
-- orders quoted before this fall back to their candidate's lines.
ALTER TABLE qr_orders ADD COLUMN lines JSONB;

---- create above / drop below ----

ALTER TABLE qr_orders DROP COLUMN lines;
