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
- `JUNO_SCAN_BEARER_TOKEN` (optional; bearer token for `juno-scan` HTTP API requests)

- `send`: single-output withdrawal plan
- `send-many`: multi-output withdrawal plan (JSON outputs file)
- `sweep`: sweep all spendable notes into 1 output
- `consolidate`: consolidate 2 to 200 notes into 1 output (`--max-spends` defaults to `50`)
- `rebalance`: multi-output rebalance plan (JSON outputs file)

Run `juno-txbuild --help` (or `juno-txbuild <command> -h`) for the complete flag reference.

Every command accepts repeatable `--exclude-note-id <txid:index>` flags. Use them to keep notes already reserved by other transaction attempts out of the candidate set. The Go API exposes the same input as `ExcludedNoteIDs []string` on `SendConfig`, `PlanConfig`, `SweepConfig`, and `ConsolidateConfig`.

All commands default to `--minconf 100`. The signer accepts at most 200 Orchard inputs and 200 total Orchard outputs, including implicit change. A plan may have 200 explicit outputs only when it creates no change. If a withdrawal needs more inputs, txbuild returns `too_many_inputs`; consolidate notes and retry.

`--coin-type 0` infers the ZIP-32 coin type from the node (`8133` mainnet, `8134` testnet, `8135` regtest). An explicit value must match the connected network.

## Fees

The base fee calculation is:

- `base_fee_zat = 5000 * max(2, max(spends, outputs))`
- where `outputs` includes the change output when `change > 0`

The shipped default is `--fee-multiplier 20`, matching the pinned `junocashd` 0.9.13 policy. Therefore the default fee is `base_fee_zat * 20` before `--fee-add-zat`.

To pay a higher fee (e.g. during congestion, or to reduce time-to-mine), use:

- `--fee-multiplier <n>` (multiplies the base fee; default `20`)
- `--fee-add-zat <zat>` (adds an absolute zatoshi amount on top)

To avoid creating very small change notes, use:

- `--min-change-zat <zat>`: if computed change is in `(0, min-change-zat)`, `juno-txbuild` adds it to the fee and omits the change output.

To avoid spending very small notes (dust-like inputs), use:

- `--min-note-zat <zat>`: skips spendable notes with value `< min-note-zat` when selecting inputs.

The base fee counts the change action and the default multiplier gives `100000 * max(2, spends, outputs + change)`, which is exactly the ZIP-317 conventional fee `junocashd` 0.9.13 enforces. A plan built with the defaults is accepted even with `-txunpaidactionlimit=0`.

Note: `junocashd` currently rejects conflicting transactions in the mempool (no replacement/RBF), and Orchard spends cannot be fee-bumped via CPFP. Set the fee you want before broadcasting.

## Extra spends (note top-up)

Wallets that receive many deposits and pay out through `send-many` only ever grow their note count: each withdrawal spends one note and creates one change note. `send-many` and `rebalance` can spend a few extra small notes on each transaction so the count comes back down without a separate consolidation flow:

- `--extra-spends <n>`: add up to `n` notes beyond what the payment needs (default `0`, max `199`).
- `--extra-spend-max-zat <zat>`: only add notes with value `<= zat` (default `0` = no cap).

Normal selection runs first, unchanged. Then txbuild adds notes from the remaining eligible set, smallest value first. Eligible means the same minconf, `--min-note-zat` and exclusion filters as normal selection, plus the note source's own checks: `z_listunspent` and the anchor-height commitment index over RPC, or the pending-spend and position checks with `--scan-url`. Ties are broken by height, then txid, then action index, so the same inputs always pick the same notes. A note is skipped when its value does not exceed the fee increase it causes, or when it would leave change in `(0, min-change-zat)`. The fee is recomputed over the final spend count and all added value goes to change. Outputs and the change address never change. Top-up stops before exceeding 200 spends or a 10,000,000 zat fee (the `junocashd` absurd-fee limit for `sendrawtransaction`). The fee cap only limits the top-up; it does not change how the base fee is computed.

With `--json`, the envelope reports what was added next to `data`:

```json
{"version":"v1","status":"ok","data":{...},"selection":{"extra_spends":4,"extra_spend_zat":"2000000"}}
```

`extra_spend_zat` is a decimal string, like the other zat amounts. Without `--json`, the same numbers are written to stderr. With the default `--extra-spends 0`, selection, the plan and the envelope are unchanged. Go callers can set `PlanConfig.ExtraSpends` and `PlanConfig.ExtraSpendMaxZat` and use `PlanWithReport` to read the counts.

## Transaction expiry

All `TxPlan`s include `expiry_height` (Overwinter `nExpiryHeight`) so transactions that are not mined will eventually become invalid.

`juno-txbuild` computes:

- `expiry_height = (chain_tip_height + 1) + expiry_offset`

where `expiry_offset` is controlled by `--expiry-offset` (default: `40`, min: `4`).

For exchange/custody use, pick an `expiry_offset` that is long enough to tolerate short-lived partitions, but short enough to deterministically release notes if a tx gets stuck.

## Optional `juno-scan` integration

By default, `juno-txbuild` uses `junocashd` RPC to enumerate spendable Orchard notes and build witnesses.

If you provide `--scan-url` (or set `JUNO_SCAN_URL`), `juno-txbuild` will source unspent notes + witness paths from `juno-scan` instead, avoiding a full chain rescan per invocation. In this mode, `--wallet-id` is used as the `wallet_id` for `juno-scan`.

Before reading notes, txbuild requires scanner health status `ok` and an exact scanner/node height and hash match at the captured planning anchor. Witnesses are requested at that explicit height. Before returning a plan, txbuild re-reads every selected note, verifies the same node and scanner anchor, and verifies that the node network, next-block consensus branch, and remaining expiry window are still compatible. A reorg, scanner-tip change, selected-note spend, consensus upgrade, or near-expiry plan during planning therefore fails closed. The node may advance beyond the anchor only while that anchor remains canonical, the next-block branch is unchanged, and the plan remains acceptable to the node's expiring-soon policy.

txbuild only reads `status`, `scanned_height` and `scanned_hash` from `/v1/health`, so it works with scanners that do not report `event_epoch` (v1.4.x, including v1.4.7-mainnet) as well as newer ones.

If `juno-scan` is configured with `-api-bearer-token`, pass `--scan-bearer-token` (or set `JUNO_SCAN_BEARER_TOKEN`) so `juno-txbuild` will include `Authorization: Bearer <token>` on all `juno-scan` requests.

## Concurrency and note reservations

The final selected-note recheck is not a reservation. `juno-txbuild` does not coordinate concurrent planners, and scanner pending-spend state is observational rather than a lock.

Every returned note has a required, unique canonical `note_id` in the form `<64-lowercase-hex-source-txid>:<base-10-uint32-action-index>`. Pass every active reservation for the source wallet back through `ExcludedNoteIDs` or repeated `--exclude-note-id` flags when building another plan. Exclusions are applied before deterministic selection in both node-backed and scanner-backed modes. Unknown canonical IDs are harmless and ignored. Missing, malformed, non-canonical, or duplicate exclusion inputs fail with `invalid_request`; if the remaining candidates cannot fund the transaction, planning fails with `insufficient_balance`.

Exclusion is still not a database lock. A concurrent coordinator should:

1. Read the wallet's active note reservations and build with those note IDs excluded.
2. Atomically reserve every returned `notes[].note_id`, with a unique constraint on the note ID (scoped by network if the database mixes networks).
3. If any reservation conflicts, reserve none, discard the plan, refresh the exclusion set, and rebuild.
4. Sign only after the complete selected-note set is durably reserved.

Serializing this loop per wallet is simplest, but the atomic all-or-nothing reservation and conflict retry remain required for multiple coordinator replicas. Never sign two plans with overlapping note IDs. Keep reservations until the transaction is confirmed or is conclusively rejected or expired and scanner state has reconciled; only then release them.

Go callers can provide exclusions directly:

```go
plan, err := txbuild.PlanSend(ctx, txbuild.SendConfig{
    // RPC, scanner, wallet, destination, amount, and change settings omitted.
    ExcludedNoteIDs: activeReservationNoteIDs,
})
```

## File formats

### `TxOutput` (`--outputs-file`)

`send-many` and `rebalance` accept `--outputs-file <path|->` containing a JSON array of `TxOutput` items:

```json
[
  { "to_address": "j*1...", "amount_zat": "100000" },
  { "to_address": "j*1...", "amount_zat": "250000", "memo_hex": "..." }
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
- `too_many_inputs`
- `no_liquidity_in_hot`
- `not_found`

## Testing

`make test` runs unit + integration + e2e suites (Dockerized `junocashd` regtest).

The e2e suite signs and broadcasts top-up transactions with `juno-txsign`. It uses `JUNO_TXSIGN_BIN`, or `../juno-txsign/bin/juno-txsign` by default, and fails if neither exists. Set `JUNO_TXSIGN_SKIP=1` to skip those tests explicitly. Scanner compatibility tests run the pinned `juno-scan` v1.4.7-mainnet release in Docker (see `docker/juno-scan`).
