package txbuild

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/Abdullah1738/juno-sdk-go/types"
	"github.com/Abdullah1738/juno-txbuild/internal/logic"
)

const (
	// MaxOrchardSpendNotes is the maximum number of Orchard notes accepted by
	// juno-txsign for one transaction.
	MaxOrchardSpendNotes = 200
	// MaxOrchardOutputs is the maximum number of Orchard outputs accepted by
	// juno-txsign for one transaction.
	MaxOrchardOutputs = 200

	// MaxExtraSpends is the largest accepted extra spend request. A plan needs at
	// least one base note, so extras can never exceed the signer limit minus one.
	MaxExtraSpends = MaxOrchardSpendNotes - 1
	// MaxTransactionFeeZat mirrors junocashd's DEFAULT_TRANSACTION_MAXFEE
	// (0.1 coin). sendrawtransaction rejects larger fees as absurd unless
	// allowhighfees is set, so extra spends never push the fee above it.
	MaxTransactionFeeZat uint64 = 10_000_000

	DefaultMinConfirmations int64  = 100
	DefaultFeeMultiplier    uint64 = 20
	// txExpiringSoonThreshold mirrors junocashd's mempool admission policy.
	txExpiringSoonThreshold uint64 = 3

	ErrCodeTooManyInputs types.ErrorCode = "too_many_inputs"

	canonicalNoteIDPattern = `^[0-9a-f]{64}:(0|[1-9][0-9]*)$`
)

var canonicalNoteIDRE = regexp.MustCompile(canonicalNoteIDPattern)

func normalizedMinConfirmations(value int64) int64 {
	if value <= 0 {
		return DefaultMinConfirmations
	}
	return value
}

func normalizedFeeMultiplier(value uint64) uint64 {
	if value == 0 {
		return DefaultFeeMultiplier
	}
	return value
}

func validateAccount(account uint32) error {
	if account >= 1<<31 {
		return types.CodedError{Code: types.ErrCodeInvalidRequest, Message: "account must be below 2147483648"}
	}
	return nil
}

func ensureOrchardSpendLimit(count int) error {
	if count <= MaxOrchardSpendNotes {
		return nil
	}
	return types.CodedError{
		Code: ErrCodeTooManyInputs,
		Message: fmt.Sprintf(
			"transaction requires %d Orchard inputs; maximum is %d; consolidate notes and retry",
			count,
			MaxOrchardSpendNotes,
		),
	}
}

func signerCompatiblePlan(plan types.TxPlan, hasChange bool) (types.TxPlan, error) {
	if err := ensureOrchardSpendLimit(len(plan.Notes)); err != nil {
		return types.TxPlan{}, err
	}
	if err := validatePlanNoteIDs(plan.Notes); err != nil {
		return types.TxPlan{}, err
	}
	outputCount := len(plan.Outputs)
	if hasChange {
		outputCount++
	}
	if outputCount > MaxOrchardOutputs {
		return types.TxPlan{}, types.CodedError{
			Code:    types.ErrCodeInvalidRequest,
			Message: fmt.Sprintf("transaction has %d Orchard outputs including change; maximum is %d", outputCount, MaxOrchardOutputs),
		}
	}
	return plan, nil
}

func validatePlanNoteIDs(notes []types.OrchardSpendNote) error {
	seen := make(map[string]struct{}, len(notes))
	for i, note := range notes {
		if note.NoteID == "" {
			return types.CodedError{
				Code:    types.ErrCodeInvalidRequest,
				Message: fmt.Sprintf("notes[%d].note_id required", i),
			}
		}
		parts := canonicalNoteIDRE.FindStringSubmatch(note.NoteID)
		if parts == nil {
			return types.CodedError{
				Code:    types.ErrCodeInvalidRequest,
				Message: fmt.Sprintf("notes[%d].note_id must match %s", i, canonicalNoteIDPattern),
			}
		}
		if _, err := strconv.ParseUint(parts[1], 10, 32); err != nil {
			return types.CodedError{
				Code:    types.ErrCodeInvalidRequest,
				Message: fmt.Sprintf("notes[%d].note_id action index must fit uint32", i),
			}
		}
		if _, exists := seen[note.NoteID]; exists {
			return types.CodedError{
				Code:    types.ErrCodeInvalidRequest,
				Message: fmt.Sprintf("notes[%d].note_id duplicates an earlier selected note", i),
			}
		}
		seen[note.NoteID] = struct{}{}
	}
	return nil
}

func validateExcludedNoteIDs(noteIDs []string) (map[string]struct{}, error) {
	seen := make(map[string]struct{}, len(noteIDs))
	for i, noteID := range noteIDs {
		if noteID == "" {
			return nil, types.CodedError{
				Code:    types.ErrCodeInvalidRequest,
				Message: fmt.Sprintf("excluded_note_ids[%d] required", i),
			}
		}
		parts := canonicalNoteIDRE.FindStringSubmatch(noteID)
		if parts == nil {
			return nil, types.CodedError{
				Code:    types.ErrCodeInvalidRequest,
				Message: fmt.Sprintf("excluded_note_ids[%d] must match %s", i, canonicalNoteIDPattern),
			}
		}
		if _, err := strconv.ParseUint(parts[1], 10, 32); err != nil {
			return nil, types.CodedError{
				Code:    types.ErrCodeInvalidRequest,
				Message: fmt.Sprintf("excluded_note_ids[%d] action index must fit uint32", i),
			}
		}
		if _, exists := seen[noteID]; exists {
			return nil, types.CodedError{
				Code:    types.ErrCodeInvalidRequest,
				Message: fmt.Sprintf("excluded_note_ids[%d] duplicates an earlier excluded note", i),
			}
		}
		seen[noteID] = struct{}{}
	}
	return seen, nil
}

func filterExcludedUnspentNotes(notes []logic.UnspentNote, excluded map[string]struct{}) []logic.UnspentNote {
	if len(excluded) == 0 {
		return notes
	}
	out := make([]logic.UnspentNote, 0, len(notes))
	for _, note := range notes {
		noteID := fmt.Sprintf("%s:%d", strings.ToLower(strings.TrimSpace(note.TxID)), note.ActionIndex)
		if _, skip := excluded[noteID]; skip {
			continue
		}
		out = append(out, note)
	}
	return out
}

func orchardChangeRequired(totalIn, totalOut, feeZat uint64) (bool, error) {
	if totalIn < totalOut {
		return false, errors.New("txbuild: invalid transaction totals")
	}
	remaining := totalIn - totalOut
	if remaining < feeZat {
		return false, errors.New("txbuild: invalid transaction totals")
	}
	return remaining > feeZat, nil
}

func selectNotesForPlan(notes []logic.UnspentNote, amountZat uint64, outputCount int, feePolicy logic.FeePolicy) ([]logic.UnspentNote, uint64, error) {
	selected, feeZat, err := logic.SelectNotesWithFeePolicy(notes, amountZat, outputCount, feePolicy)
	if err != nil {
		if errors.Is(err, logic.ErrInsufficientFunds) {
			return nil, 0, types.CodedError{Code: types.ErrCodeInsufficientBalance, Message: "insufficient funds"}
		}
		return nil, 0, fmt.Errorf("txbuild: select notes: %w", err)
	}
	if err := ensureOrchardSpendLimit(len(selected)); err != nil {
		return nil, 0, err
	}
	return selected, feeZat, nil
}

func validateExtraSpends(extraSpends int) error {
	if extraSpends < 0 || extraSpends > MaxExtraSpends {
		return types.CodedError{
			Code:    types.ErrCodeInvalidRequest,
			Message: fmt.Sprintf("extra_spends must be between 0 and %d", MaxExtraSpends),
		}
	}
	return nil
}

// addExtraSpendsForPlan applies the optional note top-up on top of the base
// selection. With extraSpends == 0 it returns the base selection untouched.
func addExtraSpendsForPlan(eligible, selected []logic.UnspentNote, amountZat uint64, outputCount int, feeZat uint64, feePolicy logic.FeePolicy, extraSpends int, maxNoteZat, minChangeZat uint64) ([]logic.UnspentNote, uint64, PlanReport, error) {
	if extraSpends <= 0 || outputCount+1 > MaxOrchardOutputs {
		return selected, feeZat, PlanReport{}, nil
	}
	res, err := logic.AddExtraSpends(eligible, selected, amountZat, outputCount, feeZat, feePolicy, logic.ExtraSpendPolicy{
		MaxExtra:        extraSpends,
		MaxNoteValueZat: maxNoteZat,
		MaxSpends:       MaxOrchardSpendNotes,
		MaxFeeZat:       MaxTransactionFeeZat,
		MinChangeZat:    minChangeZat,
	})
	if err != nil {
		return nil, 0, PlanReport{}, fmt.Errorf("txbuild: extra spends: %w", err)
	}
	if err := ensureOrchardSpendLimit(len(res.Selected)); err != nil {
		return nil, 0, PlanReport{}, err
	}
	return res.Selected, res.FeeZat, PlanReport{ExtraSpends: res.ExtraSpends, ExtraSpendZat: res.ExtraSpendZat}, nil
}
