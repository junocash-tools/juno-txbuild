# Changelog

## v1.6.0 (2026-02-10)

- Compute `expiry_height` as `(chain_tip_height + 1) + expiry_offset` (previously computed from `chain_tip_height`).
- Require `--expiry-offset >= 4` to avoid expiring-soon rejection.
- Update CLI/help text and documentation for transaction expiry semantics.

