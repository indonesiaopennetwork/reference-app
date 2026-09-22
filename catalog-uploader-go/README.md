# Catalog Uploader (Go)

A command-line tool written in Go to upload file-based catalogs (CSV format) directly to the Beckn Provider Platform (BPP).

## Features

- **CSV Parsing:** Parses pipe-delimited (`|`) CSV files.
- **Beckn Compliance:** Automatically maps rows (products) into the proper Beckn catalog format (resources, offers, etc.).
- **Smart Name Handling:** Intelligently combines `name_bahasa` and `name_english` without duplicating names if they are already present.

## Installation

You need Go installed on your system.

```bash
cd catalog-uploader-go
go build -o catalog-uploader
```

## Usage

```bash
./catalog-uploader \
  --file <path_to_csv> \
  --catalog-id <unique_catalog_id> \
  --catalog-name <human_readable_name> \
  --provider-id <unique_provider_id> \
  --provider-name <human_readable_provider_name>
```

### Flags

| Flag | Required? | Description | Example |
|------|-----------|-------------|---------|
| `--file` | Yes | Path to the pipe-delimited CSV file. | `products.csv` |
| `--catalog-id` | Yes | Unique identifier for the catalog. | `cat-001` |
| `--catalog-name` | Yes | Human-readable name for the catalog. | `My Catalog` |
| `--provider-id` | Yes | Unique identifier for the provider. | `prov-001` |
| `--provider-name` | Yes | Human-readable provider name. | `My Store` |
| `--catalog-short-desc` | No | Short description of the catalog. | `Weekly specials` |
| `--catalog-long-desc` | No | Detailed description of the catalog. | `Our best weekly specials...` |
| `--bpp-url` | No | Base URL of the BPP. Defaults to `http://localhost:8080`. | `http://localhost:8080` |

## Example

```bash
./catalog-uploader \
  --file "indonesian-snacks.csv" \
  --catalog-id "cat-go-001" \
  --catalog-name "Go Test Catalog" \
  --provider-id "provider-go-001" \
  --provider-name "Go Test Provider"
```

This will read `indonesian-snacks.csv`, transform it, and push it to `http://localhost:8080/api/v1/catalog/publish`.
