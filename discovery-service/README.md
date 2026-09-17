# discovery-service

A Go implementation of a Beckn **Discovery Service (DS)**: it serves
`POST /discover` requests and answers them asynchronously via
`POST /on_discover` to the caller's callback URL, per Beckn Protocol v2.0.0.

See [CONTEXT.md](CONTEXT.md) for the full architecture, decision log, and
protocol details.

## How it works

A Consumer Node (BAP) asks for products/services; this service matches its
in-memory catalog data against the request's `message.intent`
(`textSearch`/`filters`/`spatial` — see CONTEXT.md D18) and answers either
synchronously (`GET /discover`) or asynchronously via a callback
(`POST /discover` → `POST {bapUri}/on_discover`).

```mermaid
sequenceDiagram
    participant CN as Consumer Node (BAP)
    participant DS as discovery-service
    participant Store as CatalogStore (in-memory)

    rect rgb(235, 245, 255)
    note over CN,DS: Async path — POST /discover (primary)
    CN->>DS: POST /discover<br/>{context, message.intent}
    DS-->>CN: 200 Ack (signature)
    Note right of DS: Ack is NOT the result —<br/>just "received & queued"
    DS->>Store: Catalogs()
    Store-->>DS: last-known-good []Catalog
    DS->>DS: matchCatalogs(catalogs, intent)
    DS->>CN: POST {bapUri}/on_discover<br/>{context, message.catalogs}
    CN-->>DS: 200 Ack
    end

    rect rgb(240, 255, 240)
    note over CN,DS: Sync path — GET /discover (shortcut)
    CN->>DS: GET /discover<br/>{context, message.intent}
    DS->>Store: Catalogs()
    Store-->>DS: last-known-good []Catalog
    DS->>DS: matchCatalogs(catalogs, intent)
    DS-->>CN: 200 {context, message.catalogs}<br/>(result inline, no callback)
    end
```

A malformed `filters` expression is rejected with `400` at the `Ack`/response
stage itself. `CatalogStore` is fed by `internal/catalogsource` (polling a
BPP's dashboard API, or the embedded demo catalog if unconfigured); a
catalog survives a query only if it passes every spatial constraint, then
its resources are narrowed by `textSearch`/`filters`, and offers referencing
a now-excluded resource are dropped.

## Quickstart

```bash
cp .env.example .env
docker compose up --build
```

The service listens on `HOST_PORT` (see `.env`), forwarded to `PORT` inside
the container.

## Development

```bash
go build ./...
go vet ./...
go test ./...
```

Run locally without Docker:

```bash
go run ./cmd/discovery
```
