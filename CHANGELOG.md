# Changelog

## Unreleased

- Add opt-in note top-up to `send-many` and `rebalance` through `--extra-spends` and `--extra-spend-max-zat` (Go: `PlanConfig.ExtraSpends`, `PlanConfig.ExtraSpendMaxZat`, `PlanWithReport`). Extra notes are picked smallest first, skipped when not worth their fee, and capped at 200 spends and a 10,000,000 zat fee. The JSON envelope reports `selection.extra_spends` and `selection.extra_spend_zat` when enabled.
- Add exclusion-aware note selection to every planner mode through public Go configuration and repeatable `--exclude-note-id` CLI flags.
- Reject malformed and duplicate exclusion IDs before accessing the node or scanner.

## v1.6.0 (2026-02-10)

- Compute `expiry_height` as `(chain_tip_height + 1) + expiry_offset` (previously computed from `chain_tip_height`).
- Require `--expiry-offset >= 4` to avoid expiring-soon rejection.
- Update CLI/help text and documentation for transaction expiry semantics.
