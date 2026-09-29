-- Queries for the /beckn/select handler.
-- Used to look up offers and resources from the published catalog inventory.

-- name: GetOffer :one
-- Finds an active offer by ID within this BPP.
-- Returns the first match (offer IDs should be unique within a BPP).
SELECT * FROM offers
WHERE id = $1 AND bpp_id = $2 AND deleted_at IS NULL
LIMIT 1;

-- name: GetResource :one
-- Finds an active resource (product/service) by ID within this BPP.
-- Columns are listed explicitly: search_vector (tsvector) can't be scanned
-- into the generated model, which made every lookup fail.
SELECT id, catalog_id, bpp_id, provider_id,
       descriptor_name, descriptor_code, descriptor_short_desc, descriptor_long_desc,
       descriptor_thumbnail_image, descriptor_docs, descriptor_media_files,
       resource_attributes, resource_attributes_context, resource_attributes_type,
       master_resource_id, created_at, updated_at, deleted_at
FROM resources
WHERE id = $1 AND bpp_id = $2 AND deleted_at IS NULL
LIMIT 1;

-- name: GetOfferConsiderations :many
-- Returns all active pricing records for an offer.
SELECT * FROM offer_considerations
WHERE offer_id = $1 AND bpp_id = $2;
