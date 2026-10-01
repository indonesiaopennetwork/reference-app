-- ION's TradeSettlement schema now defines settlementBasis itself, as
-- ON_CONFIRMATION | AFTER_SHIPMENT | AFTER_DELIVERY | AFTER_RETURN_WINDOW,
-- and rejects the names this app used before (DELIVERY, SHIPMENT,
-- RETURN_WINDOW_EXPIRY). Rename them in stored contracts so the messages
-- built from them validate. The message log is left as it was sent.

UPDATE contracts SET contract_attributes = replace(replace(replace(contract_attributes::text, '"settlementBasis": "DELIVERY"', '"settlementBasis": "AFTER_DELIVERY"'), '"settlementBasis": "SHIPMENT"', '"settlementBasis": "AFTER_SHIPMENT"'), '"settlementBasis": "RETURN_WINDOW_EXPIRY"', '"settlementBasis": "AFTER_RETURN_WINDOW"')::jsonb
 WHERE contract_attributes::text LIKE '%"settlementBasis"%';

UPDATE settlements SET settlement_attributes = replace(replace(replace(settlement_attributes::text, '"settlementBasis": "DELIVERY"', '"settlementBasis": "AFTER_DELIVERY"'), '"settlementBasis": "SHIPMENT"', '"settlementBasis": "AFTER_SHIPMENT"'), '"settlementBasis": "RETURN_WINDOW_EXPIRY"', '"settlementBasis": "AFTER_RETURN_WINDOW"')::jsonb
 WHERE settlement_attributes::text LIKE '%"settlementBasis"%';

---- create above / drop below ----

UPDATE contracts SET contract_attributes = replace(replace(replace(contract_attributes::text, '"settlementBasis": "AFTER_DELIVERY"', '"settlementBasis": "DELIVERY"'), '"settlementBasis": "AFTER_SHIPMENT"', '"settlementBasis": "SHIPMENT"'), '"settlementBasis": "AFTER_RETURN_WINDOW"', '"settlementBasis": "RETURN_WINDOW_EXPIRY"')::jsonb
 WHERE contract_attributes::text LIKE '%"settlementBasis"%';

UPDATE settlements SET settlement_attributes = replace(replace(replace(settlement_attributes::text, '"settlementBasis": "AFTER_DELIVERY"', '"settlementBasis": "DELIVERY"'), '"settlementBasis": "AFTER_SHIPMENT"', '"settlementBasis": "SHIPMENT"'), '"settlementBasis": "AFTER_RETURN_WINDOW"', '"settlementBasis": "RETURN_WINDOW_EXPIRY"')::jsonb
 WHERE settlement_attributes::text LIKE '%"settlementBasis"%';
