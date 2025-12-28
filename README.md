# juno-txbuild

Online `TxPlan` (v0) builder for offline signing.

Requires a running `junocashd` (RPC) to source chain state and spend candidates.

## Commands

- `send`: single-output withdrawal plan
- `send-many`: multi-output withdrawal plan (JSON outputs file)
- `sweep`: sweep all spendable notes into 1 output
- `rebalance`: multi-output rebalance plan (JSON outputs file)

## Testing

`make test` runs unit + integration + e2e suites (Dockerized `junocashd` regtest).
