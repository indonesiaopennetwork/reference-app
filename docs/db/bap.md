# bap

## Tables

| Name                                                            | Columns | Comment                                                                                                                                                        | Type       |
| --------------------------------------------------------------- | ------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------- | ---------- |
| [public.bap_identities](#publicbap_identities)               | 6       | Registered BAP subscribers. Seeded at startup from BAP_ID / BAP_URI env vars.                                                                                  | BASE TABLE |
| [public.transactions](#publictransactions)                   | 9       | One row per Beckn transactionId initiated by this BAP. Status advances as select → init → confirm actions progress.                                            | BASE TABLE |
| [public.contract_snapshots](#publiccontract_snapshots)       | 7       | Stores the full contract JSON received from the BPP at each on_* callback. The BAP uses this to track the evolving contract state through the Beckn lifecycle. | BASE TABLE |
| [public.beckn_message_log](#publicbeckn_message_log)         | 17      | Immutable audit log of all Beckn protocol messages received or sent by this BAP.                                                                               | BASE TABLE |
| [public.reconciliations](#publicreconciliations)             | 16      |                                                                                                                                                                | BASE TABLE |
| [public.discover_requests](#publicdiscover_requests)         | 3       |                                                                                                                                                                | BASE TABLE |
| [public.grievances](#publicgrievances)                       | 27      | Order-level grievances (ION Grievance v1, /support and /on_support): current state, rebuilt from grievance_events.                                             | BASE TABLE |
| [public.grievance_events](#publicgrievance_events)           | 12      | Every grievance event (IONGrievance) received or sent; the record grievances, grievance_resolutions and grievance_executions are rebuilt from.                 | BASE TABLE |
| [public.grievance_resolutions](#publicgrievance_resolutions) | 10      | Resolution versions proposed for a grievance, each with the decision on it.                                                                                    | BASE TABLE |
| [public.grievance_executions](#publicgrievance_executions)   | 7       | Executed resolution actions, with their execution references.                                                                                                  | BASE TABLE |

## Stored procedures and functions

| Name                                             | ReturnType | Arguments                                                                 | Type     |
| ------------------------------------------------ | ---------- | ------------------------------------------------------------------------- | -------- |
| public.uuid_nil                                  | uuid       |                                                                           | FUNCTION |
| public.uuid_ns_dns                               | uuid       |                                                                           | FUNCTION |
| public.uuid_ns_url                               | uuid       |                                                                           | FUNCTION |
| public.uuid_ns_oid                               | uuid       |                                                                           | FUNCTION |
| public.uuid_ns_x500                              | uuid       |                                                                           | FUNCTION |
| public.uuid_generate_v1                          | uuid       |                                                                           | FUNCTION |
| public.uuid_generate_v1mc                        | uuid       |                                                                           | FUNCTION |
| public.uuid_generate_v3                          | uuid       | namespace uuid, name text                                                 | FUNCTION |
| public.uuid_generate_v4                          | uuid       |                                                                           | FUNCTION |
| public.uuid_generate_v5                          | uuid       | namespace uuid, name text                                                 | FUNCTION |
| public.set_limit                                 | float4     | real                                                                      | FUNCTION |
| public.show_limit                                | float4     |                                                                           | FUNCTION |
| public.show_trgm                                 | _text      | text                                                                      | FUNCTION |
| public.similarity                                | float4     | text, text                                                                | FUNCTION |
| public.similarity_op                             | bool       | text, text                                                                | FUNCTION |
| public.word_similarity                           | float4     | text, text                                                                | FUNCTION |
| public.word_similarity_op                        | bool       | text, text                                                                | FUNCTION |
| public.word_similarity_commutator_op             | bool       | text, text                                                                | FUNCTION |
| public.similarity_dist                           | float4     | text, text                                                                | FUNCTION |
| public.word_similarity_dist_op                   | float4     | text, text                                                                | FUNCTION |
| public.word_similarity_dist_commutator_op        | float4     | text, text                                                                | FUNCTION |
| public.gtrgm_in                                  | gtrgm      | cstring                                                                   | FUNCTION |
| public.gtrgm_out                                 | cstring    | gtrgm                                                                     | FUNCTION |
| public.gtrgm_consistent                          | bool       | internal, text, smallint, oid, internal                                   | FUNCTION |
| public.gtrgm_distance                            | float8     | internal, text, smallint, oid, internal                                   | FUNCTION |
| public.gtrgm_compress                            | internal   | internal                                                                  | FUNCTION |
| public.gtrgm_decompress                          | internal   | internal                                                                  | FUNCTION |
| public.gtrgm_penalty                             | internal   | internal, internal, internal                                              | FUNCTION |
| public.gtrgm_picksplit                           | internal   | internal, internal                                                        | FUNCTION |
| public.gtrgm_union                               | gtrgm      | internal, internal                                                        | FUNCTION |
| public.gtrgm_same                                | internal   | gtrgm, gtrgm, internal                                                    | FUNCTION |
| public.gin_extract_value_trgm                    | internal   | text, internal                                                            | FUNCTION |
| public.gin_extract_query_trgm                    | internal   | text, internal, smallint, internal, internal, internal, internal          | FUNCTION |
| public.gin_trgm_consistent                       | bool       | internal, smallint, text, integer, internal, internal, internal, internal | FUNCTION |
| public.gin_trgm_triconsistent                    | char       | internal, smallint, text, integer, internal, internal, internal           | FUNCTION |
| public.strict_word_similarity                    | float4     | text, text                                                                | FUNCTION |
| public.strict_word_similarity_op                 | bool       | text, text                                                                | FUNCTION |
| public.strict_word_similarity_commutator_op      | bool       | text, text                                                                | FUNCTION |
| public.strict_word_similarity_dist_op            | float4     | text, text                                                                | FUNCTION |
| public.strict_word_similarity_dist_commutator_op | float4     | text, text                                                                | FUNCTION |
| public.gtrgm_options                             | void       | internal                                                                  | FUNCTION |

## Enums

| Name                      | Values                                                                                                                                                                                                              |
| ------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| public.ack_status         | ACK, NACK                                                                                                                                                                                                           |
| public.beckn_action       | cancel, confirm, discover, init, on_cancel, on_confirm, on_discover, on_init, on_rate, on_reconcile, on_select, on_status, on_support, on_track, on_update, rate, reconcile, select, status, support, track, update |
| public.message_direction  | INBOUND, OUTBOUND                                                                                                                                                                                                   |
| public.transaction_status | CANCELLED, CANCEL_SENT, COMPLETE, CONFIRMED, CONFIRM_SENT, DISCOVER_RECEIVED, DISCOVER_SENT, INIT_RECEIVED, INIT_SENT, QUOTE_RECEIVED, RATE_SENT, SELECT_SENT, STATUS_SENT, SUPPORT_SENT, UPDATE_SENT               |

## Relations

```mermaid
erDiagram

"public.transactions" }o--|| "public.bap_identities" : "FOREIGN KEY (bap_id) REFERENCES bap_identities(bap_id)"
"public.contract_snapshots" }o--|| "public.transactions" : "FOREIGN KEY (transaction_id) REFERENCES transactions(transaction_id)"
"public.grievance_events" }o--|| "public.grievances" : "FOREIGN KEY (grievance_id) REFERENCES grievances(grievance_id) ON DELETE CASCADE"
"public.grievance_resolutions" }o--|| "public.grievances" : "FOREIGN KEY (grievance_id) REFERENCES grievances(grievance_id) ON DELETE CASCADE"
"public.grievance_executions" }o--|| "public.grievances" : "FOREIGN KEY (grievance_id) REFERENCES grievances(grievance_id) ON DELETE CASCADE"

"public.bap_identities" {
  text bap_id ""
  text bap_uri ""
  text__ network_ids ""
  boolean is_active ""
  timestamp_with_time_zone created_at ""
  timestamp_with_time_zone updated_at ""
}
"public.transactions" {
  uuid transaction_id ""
  text bap_id FK ""
  text network_id ""
  text bpp_id ""
  text bpp_uri ""
  transaction_status status ""
  timestamp_with_time_zone created_at ""
  timestamp_with_time_zone updated_at ""
  text buyer_subject ""
}
"public.contract_snapshots" {
  uuid id ""
  uuid transaction_id FK ""
  beckn_action action ""
  text contract_id "The UUID the BPP assigned to the contract (populated from on_init onwards)."
  jsonb contract ""
  timestamp_with_time_zone created_at ""
  timestamp_with_time_zone updated_at ""
}
"public.beckn_message_log" {
  uuid id ""
  uuid transaction_id ""
  uuid message_id ""
  beckn_action action ""
  message_direction direction ""
  text bap_id ""
  text bap_uri ""
  text bpp_id ""
  text bpp_uri ""
  text network_id ""
  text url ""
  ack_status ack_status ""
  jsonb request_payload ""
  jsonb response_payload ""
  text error_message ""
  integer processing_duration_ms ""
  timestamp_with_time_zone created_at ""
}
"public.reconciliations" {
  text id ""
  text bpp_id ""
  date settlement_date ""
  text status ""
  jsonb claimed_summary ""
  jsonb computed_summary ""
  jsonb contracts ""
  jsonb payout ""
  jsonb request_payload ""
  jsonb response_payload ""
  text error_message ""
  timestamp_with_time_zone created_at ""
  timestamp_with_time_zone updated_at ""
  text direction ""
  uuid request_message_id ""
  text transaction_id ""
}
"public.discover_requests" {
  uuid transaction_id ""
  text buyer_subject ""
  timestamp_with_time_zone created_at ""
}
"public.grievances" {
  text grievance_id ""
  uuid transaction_id ""
  text order_id ""
  text bap_id ""
  text bap_uri ""
  text bpp_id ""
  text bpp_uri ""
  text network_id ""
  text originating_np_id ""
  text originating_side ""
  text category ""
  text description ""
  jsonb affected ""
  jsonb related_references ""
  text policy_ref ""
  text case_status ""
  text last_event_type ""
  timestamp_with_time_zone last_event_at ""
  timestamp_with_time_zone opened_at ""
  timestamp_with_time_zone acknowledged_at ""
  timestamp_with_time_zone resolved_at ""
  timestamp_with_time_zone closed_at ""
  jsonb channels ""
  timestamp_with_time_zone seen_at ""
  text buyer_subject ""
  timestamp_with_time_zone created_at ""
  timestamp_with_time_zone updated_at ""
}
"public.grievance_events" {
  uuid id ""
  bigint seq ""
  text grievance_id FK ""
  uuid message_id ""
  text action ""
  text direction ""
  text event_type ""
  text delivery ""
  jsonb attributes ""
  text error ""
  timestamp_with_time_zone occurred_at ""
  timestamp_with_time_zone created_at ""
}
"public.grievance_resolutions" {
  text grievance_id FK ""
  text proposal_id ""
  integer version ""
  text proposed_by_np_id ""
  text authorization_type ""
  jsonb resolution ""
  text decision_result ""
  text decided_by_np_id ""
  timestamp_with_time_zone decided_at ""
  text decision_reason ""
}
"public.grievance_executions" {
  text grievance_id FK ""
  text proposal_id ""
  integer version ""
  text action_id ""
  text executed_by_np_id ""
  text execution_reference ""
  timestamp_with_time_zone completed_at ""
}
```

---

## public.bap_identities

### Description

Registered BAP subscribers. Seeded at startup from BAP_ID / BAP_URI env vars.

### Columns

| Name        | Type                     | Default      | Nullable | Children                                      | Parents | Comment |
| ----------- | ------------------------ | ------------ | -------- | --------------------------------------------- | ------- | ------- |
| bap_id      | text                     |              | false    | [public.transactions](#publictransactions) |         |         |
| bap_uri     | text                     |              | false    |                                               |         |         |
| network_ids | text[]                   | '{}'::text[] | false    |                                               |         |         |
| is_active   | boolean                  | true         | false    |                                               |         |         |
| created_at  | timestamp with time zone | now()        | false    |                                               |         |         |
| updated_at  | timestamp with time zone | now()        | false    |                                               |         |         |

### Constraints

| Name                | Type        | Definition           |
| ------------------- | ----------- | -------------------- |
| bap_identities_pkey | PRIMARY KEY | PRIMARY KEY (bap_id) |

### Indexes

| Name                | Definition                                                                            |
| ------------------- | ------------------------------------------------------------------------------------- |
| bap_identities_pkey | CREATE UNIQUE INDEX bap_identities_pkey ON public.bap_identities USING btree (bap_id) |

### Relations

```mermaid
erDiagram

"public.transactions" }o--|| "public.bap_identities" : "FOREIGN KEY (bap_id) REFERENCES bap_identities(bap_id)"

"public.bap_identities" {
  text bap_id ""
  text bap_uri ""
  text__ network_ids ""
  boolean is_active ""
  timestamp_with_time_zone created_at ""
  timestamp_with_time_zone updated_at ""
}
"public.transactions" {
  uuid transaction_id ""
  text bap_id FK ""
  text network_id ""
  text bpp_id ""
  text bpp_uri ""
  transaction_status status ""
  timestamp_with_time_zone created_at ""
  timestamp_with_time_zone updated_at ""
  text buyer_subject ""
}
```

---

## public.transactions

### Description

One row per Beckn transactionId initiated by this BAP. Status advances as select → init → confirm actions progress.

### Columns

| Name           | Type                     | Default                           | Nullable | Children                                                  | Parents                                           | Comment |
| -------------- | ------------------------ | --------------------------------- | -------- | --------------------------------------------------------- | ------------------------------------------------- | ------- |
| transaction_id | uuid                     |                                   | false    | [public.contract_snapshots](#publiccontract_snapshots) |                                                   |         |
| bap_id         | text                     |                                   | false    |                                                           | [public.bap_identities](#publicbap_identities) |         |
| network_id     | text                     |                                   | true     |                                                           |                                                   |         |
| bpp_id         | text                     |                                   | true     |                                                           |                                                   |         |
| bpp_uri        | text                     |                                   | true     |                                                           |                                                   |         |
| status         | transaction_status       | 'SELECT_SENT'::transaction_status | false    |                                                           |                                                   |         |
| created_at     | timestamp with time zone | now()                             | false    |                                                           |                                                   |         |
| updated_at     | timestamp with time zone | now()                             | false    |                                                           |                                                   |         |
| buyer_subject  | text                     |                                   | true     |                                                           |                                                   |         |

### Constraints

| Name                     | Type        | Definition                                             |
| ------------------------ | ----------- | ------------------------------------------------------ |
| transactions_bap_id_fkey | FOREIGN KEY | FOREIGN KEY (bap_id) REFERENCES bap_identities(bap_id) |
| transactions_pkey        | PRIMARY KEY | PRIMARY KEY (transaction_id)                           |

### Indexes

| Name                           | Definition                                                                                                                       |
| ------------------------------ | -------------------------------------------------------------------------------------------------------------------------------- |
| transactions_pkey              | CREATE UNIQUE INDEX transactions_pkey ON public.transactions USING btree (transaction_id)                                        |
| idx_transactions_buyer_subject | CREATE INDEX idx_transactions_buyer_subject ON public.transactions USING btree (buyer_subject) WHERE (buyer_subject IS NOT NULL) |

### Relations

```mermaid
erDiagram

"public.contract_snapshots" }o--|| "public.transactions" : "FOREIGN KEY (transaction_id) REFERENCES transactions(transaction_id)"
"public.transactions" }o--|| "public.bap_identities" : "FOREIGN KEY (bap_id) REFERENCES bap_identities(bap_id)"

"public.transactions" {
  uuid transaction_id ""
  text bap_id FK ""
  text network_id ""
  text bpp_id ""
  text bpp_uri ""
  transaction_status status ""
  timestamp_with_time_zone created_at ""
  timestamp_with_time_zone updated_at ""
  text buyer_subject ""
}
"public.contract_snapshots" {
  uuid id ""
  uuid transaction_id FK ""
  beckn_action action ""
  text contract_id "The UUID the BPP assigned to the contract (populated from on_init onwards)."
  jsonb contract ""
  timestamp_with_time_zone created_at ""
  timestamp_with_time_zone updated_at ""
}
"public.bap_identities" {
  text bap_id ""
  text bap_uri ""
  text__ network_ids ""
  boolean is_active ""
  timestamp_with_time_zone created_at ""
  timestamp_with_time_zone updated_at ""
}
```

---

## public.contract_snapshots

### Description

Stores the full contract JSON received from the BPP at each on_* callback. The BAP uses this to track the evolving contract state through the Beckn lifecycle.

### Columns

| Name           | Type                     | Default           | Nullable | Children | Parents                                       | Comment                                                                     |
| -------------- | ------------------------ | ----------------- | -------- | -------- | --------------------------------------------- | --------------------------------------------------------------------------- |
| id             | uuid                     | gen_random_uuid() | false    |          |                                               |                                                                             |
| transaction_id | uuid                     |                   | false    |          | [public.transactions](#publictransactions) |                                                                             |
| action         | beckn_action             |                   | false    |          |                                               |                                                                             |
| contract_id    | text                     |                   | true     |          |                                               | The UUID the BPP assigned to the contract (populated from on_init onwards). |
| contract       | jsonb                    |                   | false    |          |                                               |                                                                             |
| created_at     | timestamp with time zone | now()             | false    |          |                                               |                                                                             |
| updated_at     | timestamp with time zone | now()             | false    |          |                                               |                                                                             |

### Constraints

| Name                                   | Type        | Definition                                                           |
| -------------------------------------- | ----------- | -------------------------------------------------------------------- |
| contract_snapshots_transaction_id_fkey | FOREIGN KEY | FOREIGN KEY (transaction_id) REFERENCES transactions(transaction_id) |
| contract_snapshots_pkey                | PRIMARY KEY | PRIMARY KEY (id)                                                     |

### Indexes

| Name                    | Definition                                                                                |
| ----------------------- | ----------------------------------------------------------------------------------------- |
| contract_snapshots_pkey | CREATE UNIQUE INDEX contract_snapshots_pkey ON public.contract_snapshots USING btree (id) |

### Relations

```mermaid
erDiagram

"public.contract_snapshots" }o--|| "public.transactions" : "FOREIGN KEY (transaction_id) REFERENCES transactions(transaction_id)"

"public.contract_snapshots" {
  uuid id ""
  uuid transaction_id FK ""
  beckn_action action ""
  text contract_id "The UUID the BPP assigned to the contract (populated from on_init onwards)."
  jsonb contract ""
  timestamp_with_time_zone created_at ""
  timestamp_with_time_zone updated_at ""
}
"public.transactions" {
  uuid transaction_id ""
  text bap_id FK ""
  text network_id ""
  text bpp_id ""
  text bpp_uri ""
  transaction_status status ""
  timestamp_with_time_zone created_at ""
  timestamp_with_time_zone updated_at ""
  text buyer_subject ""
}
```

---

## public.beckn_message_log

### Description

Immutable audit log of all Beckn protocol messages received or sent by this BAP.

### Columns

| Name                   | Type                     | Default           | Nullable | Children | Parents | Comment |
| ---------------------- | ------------------------ | ----------------- | -------- | -------- | ------- | ------- |
| id                     | uuid                     | gen_random_uuid() | false    |          |         |         |
| transaction_id         | uuid                     |                   | true     |          |         |         |
| message_id             | uuid                     |                   | false    |          |         |         |
| action                 | beckn_action             |                   | false    |          |         |         |
| direction              | message_direction        |                   | false    |          |         |         |
| bap_id                 | text                     |                   | true     |          |         |         |
| bap_uri                | text                     |                   | true     |          |         |         |
| bpp_id                 | text                     |                   | true     |          |         |         |
| bpp_uri                | text                     |                   | true     |          |         |         |
| network_id             | text                     |                   | true     |          |         |         |
| url                    | text                     |                   | true     |          |         |         |
| ack_status             | ack_status               |                   | true     |          |         |         |
| request_payload        | jsonb                    |                   | true     |          |         |         |
| response_payload       | jsonb                    |                   | true     |          |         |         |
| error_message          | text                     |                   | true     |          |         |         |
| processing_duration_ms | integer                  |                   | true     |          |         |         |
| created_at             | timestamp with time zone | now()             | false    |          |         |         |

### Constraints

| Name                   | Type        | Definition                   |
| ---------------------- | ----------- | ---------------------------- |
| beckn_message_log_pkey | PRIMARY KEY | PRIMARY KEY (id, created_at) |

### Indexes

| Name                   | Definition                                                                                               |
| ---------------------- | -------------------------------------------------------------------------------------------------------- |
| beckn_message_log_pkey | CREATE UNIQUE INDEX beckn_message_log_pkey ON ONLY public.beckn_message_log USING btree (id, created_at) |

### Relations

```mermaid
erDiagram

"public.beckn_message_log" {
  uuid id ""
  uuid transaction_id ""
  uuid message_id ""
  beckn_action action ""
  message_direction direction ""
  text bap_id ""
  text bap_uri ""
  text bpp_id ""
  text bpp_uri ""
  text network_id ""
  text url ""
  ack_status ack_status ""
  jsonb request_payload ""
  jsonb response_payload ""
  text error_message ""
  integer processing_duration_ms ""
  timestamp_with_time_zone created_at ""
}
```

---

## public.reconciliations

### Columns

| Name               | Type                     | Default         | Nullable | Children | Parents | Comment |
| ------------------ | ------------------------ | --------------- | -------- | -------- | ------- | ------- |
| id                 | text                     |                 | false    |          |         |         |
| bpp_id             | text                     |                 | false    |          |         |         |
| settlement_date    | date                     |                 | false    |          |         |         |
| status             | text                     |                 | false    |          |         |         |
| claimed_summary    | jsonb                    |                 | false    |          |         |         |
| computed_summary   | jsonb                    |                 | true     |          |         |         |
| contracts          | jsonb                    |                 | true     |          |         |         |
| payout             | jsonb                    |                 | true     |          |         |         |
| request_payload    | jsonb                    |                 | false    |          |         |         |
| response_payload   | jsonb                    |                 | true     |          |         |         |
| error_message      | text                     |                 | true     |          |         |         |
| created_at         | timestamp with time zone | now()           | false    |          |         |         |
| updated_at         | timestamp with time zone | now()           | false    |          |         |         |
| direction          | text                     | 'INBOUND'::text | false    |          |         |         |
| request_message_id | uuid                     |                 | true     |          |         |         |
| transaction_id     | text                     |                 | true     |          |         |         |

### Constraints

| Name                 | Type        | Definition       |
| -------------------- | ----------- | ---------------- |
| reconciliations_pkey | PRIMARY KEY | PRIMARY KEY (id) |

### Indexes

| Name                                | Definition                                                                                                     |
| ----------------------------------- | -------------------------------------------------------------------------------------------------------------- |
| reconciliations_pkey                | CREATE UNIQUE INDEX reconciliations_pkey ON public.reconciliations USING btree (id)                            |
| idx_reconciliations_transaction     | CREATE INDEX idx_reconciliations_transaction ON public.reconciliations USING btree (direction, transaction_id) |
| idx_reconciliations_request_message | CREATE INDEX idx_reconciliations_request_message ON public.reconciliations USING btree (request_message_id)    |

### Relations

```mermaid
erDiagram

"public.reconciliations" {
  text id ""
  text bpp_id ""
  date settlement_date ""
  text status ""
  jsonb claimed_summary ""
  jsonb computed_summary ""
  jsonb contracts ""
  jsonb payout ""
  jsonb request_payload ""
  jsonb response_payload ""
  text error_message ""
  timestamp_with_time_zone created_at ""
  timestamp_with_time_zone updated_at ""
  text direction ""
  uuid request_message_id ""
  text transaction_id ""
}
```

---

## public.discover_requests

### Columns

| Name           | Type                     | Default | Nullable | Children | Parents | Comment |
| -------------- | ------------------------ | ------- | -------- | -------- | ------- | ------- |
| transaction_id | uuid                     |         | false    |          |         |         |
| buyer_subject  | text                     |         | false    |          |         |         |
| created_at     | timestamp with time zone | now()   | false    |          |         |         |

### Constraints

| Name                   | Type        | Definition                   |
| ---------------------- | ----------- | ---------------------------- |
| discover_requests_pkey | PRIMARY KEY | PRIMARY KEY (transaction_id) |

### Indexes

| Name                        | Definition                                                                                                        |
| --------------------------- | ----------------------------------------------------------------------------------------------------------------- |
| discover_requests_pkey      | CREATE UNIQUE INDEX discover_requests_pkey ON public.discover_requests USING btree (transaction_id)               |
| discover_requests_buyer_idx | CREATE INDEX discover_requests_buyer_idx ON public.discover_requests USING btree (buyer_subject, created_at DESC) |

### Relations

```mermaid
erDiagram

"public.discover_requests" {
  uuid transaction_id ""
  text buyer_subject ""
  timestamp_with_time_zone created_at ""
}
```

---

## public.grievances

### Description

Order-level grievances (ION Grievance v1, /support and /on_support): current state, rebuilt from grievance_events.

### Columns

| Name               | Type                     | Default     | Nullable | Children                                                                                                                                                                            | Parents | Comment |
| ------------------ | ------------------------ | ----------- | -------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | ------- | ------- |
| grievance_id       | text                     |             | false    | [public.grievance_events](#publicgrievance_events) [public.grievance_resolutions](#publicgrievance_resolutions) [public.grievance_executions](#publicgrievance_executions) |         |         |
| transaction_id     | uuid                     |             | false    |                                                                                                                                                                                     |         |         |
| order_id           | text                     |             | false    |                                                                                                                                                                                     |         |         |
| bap_id             | text                     |             | false    |                                                                                                                                                                                     |         |         |
| bap_uri            | text                     |             | false    |                                                                                                                                                                                     |         |         |
| bpp_id             | text                     |             | false    |                                                                                                                                                                                     |         |         |
| bpp_uri            | text                     |             | false    |                                                                                                                                                                                     |         |         |
| network_id         | text                     |             | true     |                                                                                                                                                                                     |         |         |
| originating_np_id  | text                     |             | false    |                                                                                                                                                                                     |         |         |
| originating_side   | text                     |             | false    |                                                                                                                                                                                     |         |         |
| category           | text                     |             | false    |                                                                                                                                                                                     |         |         |
| description        | text                     |             | false    |                                                                                                                                                                                     |         |         |
| affected           | jsonb                    | '[]'::jsonb | false    |                                                                                                                                                                                     |         |         |
| related_references | jsonb                    | '[]'::jsonb | false    |                                                                                                                                                                                     |         |         |
| policy_ref         | text                     |             | false    |                                                                                                                                                                                     |         |         |
| case_status        | text                     |             | false    |                                                                                                                                                                                     |         |         |
| last_event_type    | text                     |             | false    |                                                                                                                                                                                     |         |         |
| last_event_at      | timestamp with time zone |             | false    |                                                                                                                                                                                     |         |         |
| opened_at          | timestamp with time zone |             | false    |                                                                                                                                                                                     |         |         |
| acknowledged_at    | timestamp with time zone |             | true     |                                                                                                                                                                                     |         |         |
| resolved_at        | timestamp with time zone |             | true     |                                                                                                                                                                                     |         |         |
| closed_at          | timestamp with time zone |             | true     |                                                                                                                                                                                     |         |         |
| channels           | jsonb                    |             | true     |                                                                                                                                                                                     |         |         |
| seen_at            | timestamp with time zone |             | true     |                                                                                                                                                                                     |         |         |
| buyer_subject      | text                     |             | true     |                                                                                                                                                                                     |         |         |
| created_at         | timestamp with time zone | now()       | false    |                                                                                                                                                                                     |         |         |
| updated_at         | timestamp with time zone | now()       | false    |                                                                                                                                                                                     |         |         |

### Constraints

| Name            | Type        | Definition                 |
| --------------- | ----------- | -------------------------- |
| grievances_pkey | PRIMARY KEY | PRIMARY KEY (grievance_id) |

### Indexes

| Name                       | Definition                                                                                             |
| -------------------------- | ------------------------------------------------------------------------------------------------------ |
| grievances_pkey            | CREATE UNIQUE INDEX grievances_pkey ON public.grievances USING btree (grievance_id)                    |
| idx_grievances_transaction | CREATE INDEX idx_grievances_transaction ON public.grievances USING btree (transaction_id)              |
| idx_grievances_buyer       | CREATE INDEX idx_grievances_buyer ON public.grievances USING btree (buyer_subject, last_event_at DESC) |

### Relations

```mermaid
erDiagram

"public.grievance_events" }o--|| "public.grievances" : "FOREIGN KEY (grievance_id) REFERENCES grievances(grievance_id) ON DELETE CASCADE"
"public.grievance_resolutions" }o--|| "public.grievances" : "FOREIGN KEY (grievance_id) REFERENCES grievances(grievance_id) ON DELETE CASCADE"
"public.grievance_executions" }o--|| "public.grievances" : "FOREIGN KEY (grievance_id) REFERENCES grievances(grievance_id) ON DELETE CASCADE"

"public.grievances" {
  text grievance_id ""
  uuid transaction_id ""
  text order_id ""
  text bap_id ""
  text bap_uri ""
  text bpp_id ""
  text bpp_uri ""
  text network_id ""
  text originating_np_id ""
  text originating_side ""
  text category ""
  text description ""
  jsonb affected ""
  jsonb related_references ""
  text policy_ref ""
  text case_status ""
  text last_event_type ""
  timestamp_with_time_zone last_event_at ""
  timestamp_with_time_zone opened_at ""
  timestamp_with_time_zone acknowledged_at ""
  timestamp_with_time_zone resolved_at ""
  timestamp_with_time_zone closed_at ""
  jsonb channels ""
  timestamp_with_time_zone seen_at ""
  text buyer_subject ""
  timestamp_with_time_zone created_at ""
  timestamp_with_time_zone updated_at ""
}
"public.grievance_events" {
  uuid id ""
  bigint seq ""
  text grievance_id FK ""
  uuid message_id ""
  text action ""
  text direction ""
  text event_type ""
  text delivery ""
  jsonb attributes ""
  text error ""
  timestamp_with_time_zone occurred_at ""
  timestamp_with_time_zone created_at ""
}
"public.grievance_resolutions" {
  text grievance_id FK ""
  text proposal_id ""
  integer version ""
  text proposed_by_np_id ""
  text authorization_type ""
  jsonb resolution ""
  text decision_result ""
  text decided_by_np_id ""
  timestamp_with_time_zone decided_at ""
  text decision_reason ""
}
"public.grievance_executions" {
  text grievance_id FK ""
  text proposal_id ""
  integer version ""
  text action_id ""
  text executed_by_np_id ""
  text execution_reference ""
  timestamp_with_time_zone completed_at ""
}
```

---

## public.grievance_events

### Description

Every grievance event (IONGrievance) received or sent; the record grievances, grievance_resolutions and grievance_executions are rebuilt from.

### Columns

| Name         | Type                     | Default | Nullable | Children | Parents                                   | Comment |
| ------------ | ------------------------ | ------- | -------- | -------- | ----------------------------------------- | ------- |
| id           | uuid                     |         | false    |          |                                           |         |
| seq          | bigint                   |         | false    |          |                                           |         |
| grievance_id | text                     |         | false    |          | [public.grievances](#publicgrievances) |         |
| message_id   | uuid                     |         | false    |          |                                           |         |
| action       | text                     |         | false    |          |                                           |         |
| direction    | text                     |         | false    |          |                                           |         |
| event_type   | text                     |         | false    |          |                                           |         |
| delivery     | text                     |         | false    |          |                                           |         |
| attributes   | jsonb                    |         | false    |          |                                           |         |
| error        | text                     |         | true     |          |                                           |         |
| occurred_at  | timestamp with time zone |         | false    |          |                                           |         |
| created_at   | timestamp with time zone | now()   | false    |          |                                           |         |

### Constraints

| Name                               | Type        | Definition                                                                                   |
| ---------------------------------- | ----------- | -------------------------------------------------------------------------------------------- |
| grievance_events_action_check      | CHECK       | CHECK ((action = ANY (ARRAY['support'::text, 'on_support'::text])))                          |
| grievance_events_delivery_check    | CHECK       | CHECK ((delivery = ANY (ARRAY['SENDING'::text, 'ACK'::text, 'NACK'::text, 'FAILED'::text]))) |
| grievance_events_direction_check   | CHECK       | CHECK ((direction = ANY (ARRAY['INBOUND'::text, 'OUTBOUND'::text])))                         |
| grievance_events_grievance_id_fkey | FOREIGN KEY | FOREIGN KEY (grievance_id) REFERENCES grievances(grievance_id) ON DELETE CASCADE             |
| grievance_events_pkey              | PRIMARY KEY | PRIMARY KEY (id)                                                                             |

### Indexes

| Name                           | Definition                                                                                             |
| ------------------------------ | ------------------------------------------------------------------------------------------------------ |
| grievance_events_pkey          | CREATE UNIQUE INDEX grievance_events_pkey ON public.grievance_events USING btree (id)                  |
| idx_grievance_events_grievance | CREATE INDEX idx_grievance_events_grievance ON public.grievance_events USING btree (grievance_id, seq) |

### Relations

```mermaid
erDiagram

"public.grievance_events" }o--|| "public.grievances" : "FOREIGN KEY (grievance_id) REFERENCES grievances(grievance_id) ON DELETE CASCADE"

"public.grievance_events" {
  uuid id ""
  bigint seq ""
  text grievance_id FK ""
  uuid message_id ""
  text action ""
  text direction ""
  text event_type ""
  text delivery ""
  jsonb attributes ""
  text error ""
  timestamp_with_time_zone occurred_at ""
  timestamp_with_time_zone created_at ""
}
"public.grievances" {
  text grievance_id ""
  uuid transaction_id ""
  text order_id ""
  text bap_id ""
  text bap_uri ""
  text bpp_id ""
  text bpp_uri ""
  text network_id ""
  text originating_np_id ""
  text originating_side ""
  text category ""
  text description ""
  jsonb affected ""
  jsonb related_references ""
  text policy_ref ""
  text case_status ""
  text last_event_type ""
  timestamp_with_time_zone last_event_at ""
  timestamp_with_time_zone opened_at ""
  timestamp_with_time_zone acknowledged_at ""
  timestamp_with_time_zone resolved_at ""
  timestamp_with_time_zone closed_at ""
  jsonb channels ""
  timestamp_with_time_zone seen_at ""
  text buyer_subject ""
  timestamp_with_time_zone created_at ""
  timestamp_with_time_zone updated_at ""
}
```

---

## public.grievance_resolutions

### Description

Resolution versions proposed for a grievance, each with the decision on it.

### Columns

| Name               | Type                     | Default | Nullable | Children | Parents                                   | Comment |
| ------------------ | ------------------------ | ------- | -------- | -------- | ----------------------------------------- | ------- |
| grievance_id       | text                     |         | false    |          | [public.grievances](#publicgrievances) |         |
| proposal_id        | text                     |         | false    |          |                                           |         |
| version            | integer                  |         | false    |          |                                           |         |
| proposed_by_np_id  | text                     |         | false    |          |                                           |         |
| authorization_type | text                     |         | false    |          |                                           |         |
| resolution         | jsonb                    |         | false    |          |                                           |         |
| decision_result    | text                     |         | true     |          |                                           |         |
| decided_by_np_id   | text                     |         | true     |          |                                           |         |
| decided_at         | timestamp with time zone |         | true     |          |                                           |         |
| decision_reason    | text                     |         | true     |          |                                           |         |

### Constraints

| Name                                    | Type        | Definition                                                                       |
| --------------------------------------- | ----------- | -------------------------------------------------------------------------------- |
| grievance_resolutions_grievance_id_fkey | FOREIGN KEY | FOREIGN KEY (grievance_id) REFERENCES grievances(grievance_id) ON DELETE CASCADE |
| grievance_resolutions_pkey              | PRIMARY KEY | PRIMARY KEY (grievance_id, proposal_id, version)                                 |

### Indexes

| Name                       | Definition                                                                                                                      |
| -------------------------- | ------------------------------------------------------------------------------------------------------------------------------- |
| grievance_resolutions_pkey | CREATE UNIQUE INDEX grievance_resolutions_pkey ON public.grievance_resolutions USING btree (grievance_id, proposal_id, version) |

### Relations

```mermaid
erDiagram

"public.grievance_resolutions" }o--|| "public.grievances" : "FOREIGN KEY (grievance_id) REFERENCES grievances(grievance_id) ON DELETE CASCADE"

"public.grievance_resolutions" {
  text grievance_id FK ""
  text proposal_id ""
  integer version ""
  text proposed_by_np_id ""
  text authorization_type ""
  jsonb resolution ""
  text decision_result ""
  text decided_by_np_id ""
  timestamp_with_time_zone decided_at ""
  text decision_reason ""
}
"public.grievances" {
  text grievance_id ""
  uuid transaction_id ""
  text order_id ""
  text bap_id ""
  text bap_uri ""
  text bpp_id ""
  text bpp_uri ""
  text network_id ""
  text originating_np_id ""
  text originating_side ""
  text category ""
  text description ""
  jsonb affected ""
  jsonb related_references ""
  text policy_ref ""
  text case_status ""
  text last_event_type ""
  timestamp_with_time_zone last_event_at ""
  timestamp_with_time_zone opened_at ""
  timestamp_with_time_zone acknowledged_at ""
  timestamp_with_time_zone resolved_at ""
  timestamp_with_time_zone closed_at ""
  jsonb channels ""
  timestamp_with_time_zone seen_at ""
  text buyer_subject ""
  timestamp_with_time_zone created_at ""
  timestamp_with_time_zone updated_at ""
}
```

---

## public.grievance_executions

### Description

Executed resolution actions, with their execution references.

### Columns

| Name                | Type                     | Default | Nullable | Children | Parents                                   | Comment |
| ------------------- | ------------------------ | ------- | -------- | -------- | ----------------------------------------- | ------- |
| grievance_id        | text                     |         | false    |          | [public.grievances](#publicgrievances) |         |
| proposal_id         | text                     |         | false    |          |                                           |         |
| version             | integer                  |         | false    |          |                                           |         |
| action_id           | text                     |         | false    |          |                                           |         |
| executed_by_np_id   | text                     |         | false    |          |                                           |         |
| execution_reference | text                     |         | true     |          |                                           |         |
| completed_at        | timestamp with time zone |         | false    |          |                                           |         |

### Constraints

| Name                                   | Type        | Definition                                                                       |
| -------------------------------------- | ----------- | -------------------------------------------------------------------------------- |
| grievance_executions_grievance_id_fkey | FOREIGN KEY | FOREIGN KEY (grievance_id) REFERENCES grievances(grievance_id) ON DELETE CASCADE |
| grievance_executions_pkey              | PRIMARY KEY | PRIMARY KEY (grievance_id, proposal_id, version, action_id)                      |

### Indexes

| Name                      | Definition                                                                                                                               |
| ------------------------- | ---------------------------------------------------------------------------------------------------------------------------------------- |
| grievance_executions_pkey | CREATE UNIQUE INDEX grievance_executions_pkey ON public.grievance_executions USING btree (grievance_id, proposal_id, version, action_id) |

### Relations

```mermaid
erDiagram

"public.grievance_executions" }o--|| "public.grievances" : "FOREIGN KEY (grievance_id) REFERENCES grievances(grievance_id) ON DELETE CASCADE"

"public.grievance_executions" {
  text grievance_id FK ""
  text proposal_id ""
  integer version ""
  text action_id ""
  text executed_by_np_id ""
  text execution_reference ""
  timestamp_with_time_zone completed_at ""
}
"public.grievances" {
  text grievance_id ""
  uuid transaction_id ""
  text order_id ""
  text bap_id ""
  text bap_uri ""
  text bpp_id ""
  text bpp_uri ""
  text network_id ""
  text originating_np_id ""
  text originating_side ""
  text category ""
  text description ""
  jsonb affected ""
  jsonb related_references ""
  text policy_ref ""
  text case_status ""
  text last_event_type ""
  timestamp_with_time_zone last_event_at ""
  timestamp_with_time_zone opened_at ""
  timestamp_with_time_zone acknowledged_at ""
  timestamp_with_time_zone resolved_at ""
  timestamp_with_time_zone closed_at ""
  jsonb channels ""
  timestamp_with_time_zone seen_at ""
  text buyer_subject ""
  timestamp_with_time_zone created_at ""
  timestamp_with_time_zone updated_at ""
}
```

---

---

> Generated by [tbls](https://github.com/k1LoW/tbls)
