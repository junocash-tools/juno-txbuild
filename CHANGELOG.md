# Changelog

## v1.7.0 (2026-09-25)

- Add opt-in note top-up to `send-many` and `rebalance` through `--extra-spends` and `--extra-spend-max-zat` (Go: `PlanConfig.ExtraSpends`, `PlanConfig.ExtraSpendMaxZat`, `PlanWithReport`). Extra notes are picked smallest first, skipped when not worth their fee, and capped at 200 spends and a 10,000,000 zat fee. The JSON envelope reports `selection.extra_spends` and `selection.extra_spend_zat` when enabled.
- Add exclusion-aware note selection to every planner mode through public Go configuration and repeatable `--exclude-note-id` CLI flags.
- Reject malformed and duplicate exclusion IDs before accessing the node or scanner.
- Require canonical, unique `notes[].note_id` values (`<lowercase txid>:<action index>`) in plans; the schema also bounds coin type, account, chain and note/output counts.
- Change CLI defaults to `--fee-multiplier 20` and `--minconf 100` (Go zero values normalize the same way), require `--coin-type` to match the node network, and cap plans at 200 input notes (`too_many_inputs`) and 200 outputs including change.
- Take `branch_id` from the next block's consensus branch.
- Scanner-backed planning now fails closed unless the scanner and node agree on the anchor, and re-checks selected notes and chain context before returning.
- Pin the Docker test node to `junocashd` 0.9.13.

## v1.6.0 (2026-02-10)

- Compute `expiry_height` as `(chain_tip_height + 1) + expiry_offset` (previously computed from `chain_tip_height`).
- Require `--expiry-offset >= 4` to avoid expiring-soon rejection.
- Update CLI/help text and documentation for transaction expiry semantics.
