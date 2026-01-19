# juno-txbuild

Online `TxPlan` (v0) builder for offline signing.

`juno-txbuild` talks to `junocashd` over RPC to gather chain state, select spend candidates, and produce a `TxPlan` JSON package for offline signing with `juno-txsign`.

## API stability

- The `TxPlan` file format is versioned via `txplan.version` (currently `"v0"`). Breaking changes must be introduced as a new version value.
- For automation/integrations, treat JSON as the stable API surface (`--out` or `--json`). Human-oriented output may change.
- Schemas:
  - `api/txplan.v0.schema.json`
  - `api/txoutputs.schema.json` (for `--outputs-file`)

## CLI

Environment variables (optional; avoid passing secrets on the command line):

- `JUNO_RPC_URL`
- `JUNO_RPC_USER`
- `JUNO_RPC_PASS`
- `JUNO_SCAN_URL` (optional; use `juno-scan` for notes + witnesses)

- `send`: single-output withdrawal plan
- `send-many`: multi-output withdrawal plan (JSON outputs file)
- `sweep`: sweep all spendable notes into 1 output
- `rebalance`: multi-output rebalance plan (JSON outputs file)

Run `juno-txbuild --help` (or `juno-txbuild <command> -h`) for the complete flag reference.

## Optional `juno-scan` integration

By default, `juno-txbuild` uses `junocashd` RPC to enumerate spendable Orchard notes and build witnesses.

If you provide `--scan-url` (or set `JUNO_SCAN_URL`), `juno-txbuild` will source unspent notes + witness paths from `juno-scan` instead, avoiding a full chain rescan per invocation. In this mode, `--wallet-id` is used as the `wallet_id` for `juno-scan`.

## File formats

### `TxOutput` (`--outputs-file`)

`send-many` and `rebalance` accept `--outputs-file <path|->` containing a JSON array of `TxOutput` items:

```json
[
  { "to_address": "j1...", "amount_zat": "100000" },
  { "to_address": "j1...", "amount_zat": "250000", "memo_hex": "..." }
]
```

See `api/txoutputs.schema.json`.

### `TxPlan` (stdout / `--out`)

All commands produce a `TxPlan` JSON object (pretty-printed to stdout by default). Use `--out <path>` to write the plan to a file (mode `0600`).

The `TxPlan` schema is documented in `api/txplan.v0.schema.json`.

### `--json` envelope

When `--json` is set, output is wrapped:

- success: `{"version":"v1","status":"ok","data":<TxPlan>}`
- error: `{"version":"v1","status":"err","error":{"code":"...","message":"..."}}`

## Errors

Error codes are designed to be machine-readable:

- `invalid_request`
- `insufficient_balance`
- `no_liquidity_in_hot`
- `not_found`

## Testing

`make test` runs unit + integration + e2e suites (Dockerized `junocashd` regtest).
