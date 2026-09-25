package txbuild

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Abdullah1738/juno-sdk-go/junocashd"
	"github.com/Abdullah1738/juno-sdk-go/junoscan"
	"github.com/Abdullah1738/juno-sdk-go/types"
	"github.com/Abdullah1738/juno-txbuild/internal/chain"
	"github.com/Abdullah1738/juno-txbuild/internal/logic"
	"github.com/Abdullah1738/juno-txbuild/internal/witness"
)

type SendConfig struct {
	RPCURL  string
	RPCUser string
	RPCPass string

	ScanURL string
	// Optional bearer token for HTTP requests to juno-scan.
	// Sent as: Authorization: Bearer <token>
	ScanBearerToken string

	WalletID string
	CoinType uint32
	Account  uint32
	// ExcludedNoteIDs contains canonical note IDs that must not be selected.
	// Coordinators use this to exclude notes reserved by other transaction attempts.
	ExcludedNoteIDs []string

	ToAddress string
	AmountZat string
	MemoHex   string

	ChangeAddress string

	MinConfirmations int64
	ExpiryOffset     uint32
	MinNoteZat       uint64

	FeeMultiplier uint64
	FeeAddZat     uint64
	MinChangeZat  uint64
}

func PlanSend(ctx context.Context, cfg SendConfig) (types.TxPlan, error) {
	return Plan(ctx, PlanConfig{
		RPCURL:  cfg.RPCURL,
		RPCUser: cfg.RPCUser,
		RPCPass: cfg.RPCPass,

		ScanURL:         cfg.ScanURL,
		ScanBearerToken: cfg.ScanBearerToken,

		WalletID: cfg.WalletID,
		CoinType: cfg.CoinType,
		Account:  cfg.Account,

		ExcludedNoteIDs: cfg.ExcludedNoteIDs,

		Kind: types.TxPlanKindWithdrawal,
		Outputs: []types.TxOutput{
			{ToAddress: cfg.ToAddress, AmountZat: cfg.AmountZat, MemoHex: cfg.MemoHex},
		},
		ChangeAddress: cfg.ChangeAddress,

		MinConfirmations: cfg.MinConfirmations,
		ExpiryOffset:     cfg.ExpiryOffset,
		MinNoteZat:       cfg.MinNoteZat,

		FeeMultiplier: cfg.FeeMultiplier,
		FeeAddZat:     cfg.FeeAddZat,
		MinChangeZat:  cfg.MinChangeZat,
	})
}

type PlanConfig struct {
	RPCURL  string
	RPCUser string
	RPCPass string

	ScanURL string
	// Optional bearer token for HTTP requests to juno-scan.
	// Sent as: Authorization: Bearer <token>
	ScanBearerToken string

	WalletID string
	CoinType uint32
	Account  uint32
	// ExcludedNoteIDs contains canonical note IDs that must not be selected.
	// Unknown canonical IDs are ignored; malformed or duplicate IDs are rejected.
	ExcludedNoteIDs []string

	Kind          types.TxPlanKind
	Outputs       []types.TxOutput
	ChangeAddress string

	MinConfirmations int64
	ExpiryOffset     uint32
	MinNoteZat       uint64

	FeeMultiplier uint64
	FeeAddZat     uint64
	MinChangeZat  uint64

	// ExtraSpends adds up to this many extra notes on top of the base
	// selection, smallest first, to shrink the wallet's note count. Extras only
	// change the spend set, fee and change; outputs are never touched. 0 keeps
	// the base selection as is.
	ExtraSpends int
	// ExtraSpendMaxZat only allows extra notes with value <= this. 0 means no cap.
	ExtraSpendMaxZat uint64
}

// PlanReport describes planner decisions that are not part of the TxPlan.
type PlanReport struct {
	// ExtraSpends is the number of notes added by the extra spend top-up.
	ExtraSpends int
	// ExtraSpendZat is the total value of those notes.
	ExtraSpendZat uint64
}

func Plan(ctx context.Context, cfg PlanConfig) (types.TxPlan, error) {
	plan, _, err := PlanWithReport(ctx, cfg)
	return plan, err
}

// PlanWithReport is Plan plus a report of the extra spend top-up.
func PlanWithReport(ctx context.Context, cfg PlanConfig) (types.TxPlan, PlanReport, error) {
	var report PlanReport
	plan, err := planOutputs(ctx, cfg, &report)
	if err != nil {
		return types.TxPlan{}, PlanReport{}, err
	}
	return plan, report, nil
}

func planOutputs(ctx context.Context, cfg PlanConfig, report *PlanReport) (types.TxPlan, error) {
	cfg.RPCURL = strings.TrimSpace(cfg.RPCURL)
	cfg.RPCUser = strings.TrimSpace(cfg.RPCUser)
	cfg.RPCPass = strings.TrimSpace(cfg.RPCPass)
	cfg.ScanURL = strings.TrimSpace(cfg.ScanURL)
	cfg.ScanBearerToken = strings.TrimSpace(cfg.ScanBearerToken)
	cfg.WalletID = strings.TrimSpace(cfg.WalletID)
	cfg.ChangeAddress = strings.TrimSpace(cfg.ChangeAddress)

	if cfg.RPCURL == "" {
		return types.TxPlan{}, types.CodedError{Code: types.ErrCodeInvalidRequest, Message: "rpc url required"}
	}
	if cfg.WalletID == "" {
		return types.TxPlan{}, types.CodedError{Code: types.ErrCodeInvalidRequest, Message: "wallet_id required"}
	}
	if err := validateAccount(cfg.Account); err != nil {
		return types.TxPlan{}, err
	}
	excludedNoteIDs, err := validateExcludedNoteIDs(cfg.ExcludedNoteIDs)
	if err != nil {
		return types.TxPlan{}, err
	}
	if err := validateExtraSpends(cfg.ExtraSpends); err != nil {
		return types.TxPlan{}, err
	}
	switch cfg.Kind {
	case types.TxPlanKindWithdrawal, types.TxPlanKindSweep, types.TxPlanKindRebalance:
	default:
		return types.TxPlan{}, types.CodedError{Code: types.ErrCodeInvalidRequest, Message: "unsupported kind"}
	}
	if len(cfg.Outputs) == 0 {
		return types.TxPlan{}, types.CodedError{Code: types.ErrCodeInvalidRequest, Message: "outputs required"}
	}
	if len(cfg.Outputs) > MaxOrchardOutputs {
		return types.TxPlan{}, types.CodedError{Code: types.ErrCodeInvalidRequest, Message: fmt.Sprintf("outputs must contain at most %d entries", MaxOrchardOutputs)}
	}
	if cfg.ChangeAddress == "" {
		return types.TxPlan{}, types.CodedError{Code: types.ErrCodeInvalidRequest, Message: "change_address required"}
	}
	cfg.MinConfirmations = normalizedMinConfirmations(cfg.MinConfirmations)
	if cfg.ExpiryOffset == 0 {
		cfg.ExpiryOffset = 40
	}
	if cfg.ExpiryOffset < 4 {
		return types.TxPlan{}, types.CodedError{Code: types.ErrCodeInvalidRequest, Message: "expiry_offset must be >= 4"}
	}
	cfg.FeeMultiplier = normalizedFeeMultiplier(cfg.FeeMultiplier)

	var totalOut uint64
	for i := range cfg.Outputs {
		cfg.Outputs[i].ToAddress = strings.TrimSpace(cfg.Outputs[i].ToAddress)
		cfg.Outputs[i].AmountZat = strings.TrimSpace(cfg.Outputs[i].AmountZat)
		cfg.Outputs[i].MemoHex = strings.TrimSpace(cfg.Outputs[i].MemoHex)
		if cfg.Outputs[i].ToAddress == "" {
			return types.TxPlan{}, types.CodedError{Code: types.ErrCodeInvalidRequest, Message: fmt.Sprintf("outputs[%d].to_address required", i)}
		}
		if cfg.Outputs[i].AmountZat == "" {
			return types.TxPlan{}, types.CodedError{Code: types.ErrCodeInvalidRequest, Message: fmt.Sprintf("outputs[%d].amount_zat required", i)}
		}
		amt, err := parseUint64Decimal(cfg.Outputs[i].AmountZat)
		if err != nil || amt == 0 {
			return types.TxPlan{}, types.CodedError{Code: types.ErrCodeInvalidRequest, Message: fmt.Sprintf("outputs[%d].amount_zat invalid", i)}
		}
		var ok bool
		totalOut, ok = addUint64(totalOut, amt)
		if !ok {
			return types.TxPlan{}, types.CodedError{Code: types.ErrCodeInvalidRequest, Message: "outputs sum overflow"}
		}
	}

	rpc := junocashd.New(cfg.RPCURL, cfg.RPCUser, cfg.RPCPass)

	chainInfo, err := chain.GetChainInfo(ctx, rpc)
	if err != nil {
		return types.TxPlan{}, err
	}

	coinType, err := resolveCoinType(chainInfo.Chain, cfg.CoinType)
	if err != nil {
		return types.TxPlan{}, err
	}
	if chainInfo.Height < 0 {
		return types.TxPlan{}, errors.New("txbuild: invalid chain height")
	}
	if chainInfo.Height > int64(^uint32(0)) {
		return types.TxPlan{}, errors.New("txbuild: chain height too large")
	}
	anchorHeight := uint32(chainInfo.Height)

	if cfg.ScanURL != "" {
		return planWithScan(ctx, rpc, chainInfo, coinType, cfg, totalOut, excludedNoteIDs, report)
	}
	nodeSnapshot, err := captureNodeAnchor(ctx, rpc, chainInfo.Height)
	if err != nil {
		return types.TxPlan{}, err
	}

	orchard, err := chain.BuildOrchardIndex(ctx, rpc, int64(anchorHeight))
	if err != nil {
		return types.TxPlan{}, err
	}
	if len(orchard.CMXHex) == 0 {
		return types.TxPlan{}, errors.New("txbuild: no orchard commitments")
	}

	notes, err := listUnspentOrchardNotes(ctx, rpc, int64(anchorHeight), cfg.MinConfirmations, cfg.Account)
	if err != nil {
		return types.TxPlan{}, err
	}
	notes = logic.FilterNotesMinValue(notes, cfg.MinNoteZat)
	notes = filterExcludedUnspentNotes(notes, excludedNoteIDs)
	if len(notes) == 0 {
		return types.TxPlan{}, types.CodedError{Code: types.ErrCodeInsufficientBalance, Message: "no spendable notes"}
	}

	feePolicy := logic.FeePolicy{
		Multiplier: cfg.FeeMultiplier,
		AddZat:     cfg.FeeAddZat,
	}

	selected, feeZat, err := selectNotesForPlan(notes, totalOut, len(cfg.Outputs), feePolicy)
	if err != nil {
		return types.TxPlan{}, err
	}
	if cfg.ExtraSpends > 0 {
		// Only top up from notes the anchor-height index already covers, so a
		// note mined after the anchor can't make an optional extra fail the plan.
		indexed := make([]logic.UnspentNote, 0, len(notes))
		for _, n := range notes {
			if _, ok := orchard.ByOutpoint[fmt.Sprintf("%s:%d", n.TxID, n.ActionIndex)]; ok {
				indexed = append(indexed, n)
			}
		}
		selected, feeZat, *report, err = addExtraSpendsForPlan(indexed, selected, totalOut, len(cfg.Outputs), feeZat, feePolicy, cfg.ExtraSpends, cfg.ExtraSpendMaxZat, cfg.MinChangeZat)
		if err != nil {
			return types.TxPlan{}, err
		}
	}

	var totalIn uint64
	for _, n := range selected {
		var ok bool
		totalIn, ok = addUint64(totalIn, n.ValueZat)
		if !ok {
			return types.TxPlan{}, errors.New("txbuild: selected notes sum overflow")
		}
	}
	feeZat, _, err = logic.SuppressDustChange(totalIn, totalOut, feeZat, cfg.MinChangeZat)
	if err != nil {
		return types.TxPlan{}, err
	}
	hasChange, err := orchardChangeRequired(totalIn, totalOut, feeZat)
	if err != nil {
		return types.TxPlan{}, err
	}

	positions := make([]uint32, 0, len(selected))
	planNotes := make([]types.OrchardSpendNote, 0, len(selected))
	for _, n := range selected {
		key := fmt.Sprintf("%s:%d", n.TxID, n.ActionIndex)
		act, ok := orchard.ByOutpoint[key]
		if !ok {
			return types.TxPlan{}, errors.New("txbuild: missing orchard action for selected note")
		}
		planNotes = append(planNotes, types.OrchardSpendNote{
			NoteID:          key,
			ActionNullifier: act.Nullifier,
			CMX:             act.CMX,
			Position:        act.Position,
			Path:            nil,
			EphemeralKey:    act.EphemeralKey,
			EncCiphertext:   act.EncCiphertext,
		})
		positions = append(positions, act.Position)
	}

	wit, err := witness.OrchardWitness(orchard.CMXHex, positions)
	if err != nil {
		return types.TxPlan{}, err
	}
	if len(wit.Paths) != len(planNotes) {
		return types.TxPlan{}, errors.New("txbuild: witness response mismatch")
	}

	for i := range planNotes {
		if wit.Paths[i].Position != planNotes[i].Position {
			return types.TxPlan{}, errors.New("txbuild: witness response mismatch")
		}
		planNotes[i].Path = wit.Paths[i].AuthPath
	}

	expiryHeight, err := logic.ExpiryHeightFromTip(anchorHeight, cfg.ExpiryOffset)
	if err != nil {
		return types.TxPlan{}, errors.New("txbuild: expiry height overflow")
	}

	plan := types.TxPlan{
		Version:       types.V0,
		Kind:          cfg.Kind,
		WalletID:      cfg.WalletID,
		CoinType:      coinType,
		Account:       cfg.Account,
		Chain:         chainInfo.Chain,
		BranchID:      chainInfo.BranchID,
		AnchorHeight:  anchorHeight,
		Anchor:        wit.Root,
		ExpiryHeight:  expiryHeight,
		Outputs:       cfg.Outputs,
		ChangeAddress: cfg.ChangeAddress,
		FeeZat:        strconv.FormatUint(feeZat, 10),
		Notes:         planNotes,
	}
	plan, err = signerCompatiblePlan(plan, hasChange)
	if err != nil {
		return types.TxPlan{}, err
	}
	if err := verifyNodeAnchor(ctx, rpc, nodeSnapshot); err != nil {
		return types.TxPlan{}, err
	}
	if err := verifyChainContext(ctx, rpc, chainInfo, nodeSnapshot, expiryHeight); err != nil {
		return types.TxPlan{}, err
	}
	return plan, nil
}

type SweepConfig struct {
	RPCURL  string
	RPCUser string
	RPCPass string

	ScanURL string
	// Optional bearer token for HTTP requests to juno-scan.
	// Sent as: Authorization: Bearer <token>
	ScanBearerToken string

	WalletID string
	CoinType uint32
	Account  uint32
	// ExcludedNoteIDs contains canonical note IDs that must not be selected.
	// Unknown canonical IDs are ignored; malformed or duplicate IDs are rejected.
	ExcludedNoteIDs []string

	ToAddress     string
	MemoHex       string
	ChangeAddress string

	MinConfirmations int64
	ExpiryOffset     uint32
	MinNoteZat       uint64

	FeeMultiplier uint64
	FeeAddZat     uint64
}

func PlanSweep(ctx context.Context, cfg SweepConfig) (types.TxPlan, error) {
	cfg.RPCURL = strings.TrimSpace(cfg.RPCURL)
	cfg.RPCUser = strings.TrimSpace(cfg.RPCUser)
	cfg.RPCPass = strings.TrimSpace(cfg.RPCPass)
	cfg.ScanURL = strings.TrimSpace(cfg.ScanURL)
	cfg.ScanBearerToken = strings.TrimSpace(cfg.ScanBearerToken)
	cfg.WalletID = strings.TrimSpace(cfg.WalletID)
	cfg.ToAddress = strings.TrimSpace(cfg.ToAddress)
	cfg.MemoHex = strings.TrimSpace(cfg.MemoHex)
	cfg.ChangeAddress = strings.TrimSpace(cfg.ChangeAddress)

	if cfg.RPCURL == "" {
		return types.TxPlan{}, types.CodedError{Code: types.ErrCodeInvalidRequest, Message: "rpc url required"}
	}
	if cfg.WalletID == "" {
		return types.TxPlan{}, types.CodedError{Code: types.ErrCodeInvalidRequest, Message: "wallet_id required"}
	}
	if err := validateAccount(cfg.Account); err != nil {
		return types.TxPlan{}, err
	}
	excludedNoteIDs, err := validateExcludedNoteIDs(cfg.ExcludedNoteIDs)
	if err != nil {
		return types.TxPlan{}, err
	}
	if cfg.ToAddress == "" {
		return types.TxPlan{}, types.CodedError{Code: types.ErrCodeInvalidRequest, Message: "to required"}
	}
	if cfg.ChangeAddress == "" {
		cfg.ChangeAddress = cfg.ToAddress
	}
	cfg.MinConfirmations = normalizedMinConfirmations(cfg.MinConfirmations)
	if cfg.ExpiryOffset == 0 {
		cfg.ExpiryOffset = 40
	}
	if cfg.ExpiryOffset < 4 {
		return types.TxPlan{}, types.CodedError{Code: types.ErrCodeInvalidRequest, Message: "expiry_offset must be >= 4"}
	}
	cfg.FeeMultiplier = normalizedFeeMultiplier(cfg.FeeMultiplier)

	rpc := junocashd.New(cfg.RPCURL, cfg.RPCUser, cfg.RPCPass)

	chainInfo, err := chain.GetChainInfo(ctx, rpc)
	if err != nil {
		return types.TxPlan{}, err
	}

	coinType, err := resolveCoinType(chainInfo.Chain, cfg.CoinType)
	if err != nil {
		return types.TxPlan{}, err
	}
	if chainInfo.Height < 0 {
		return types.TxPlan{}, errors.New("txbuild: invalid chain height")
	}
	if chainInfo.Height > int64(^uint32(0)) {
		return types.TxPlan{}, errors.New("txbuild: chain height too large")
	}
	anchorHeight := uint32(chainInfo.Height)

	if cfg.ScanURL != "" {
		return planSweepWithScan(ctx, rpc, chainInfo, coinType, cfg, excludedNoteIDs)
	}
	nodeSnapshot, err := captureNodeAnchor(ctx, rpc, chainInfo.Height)
	if err != nil {
		return types.TxPlan{}, err
	}

	orchard, err := chain.BuildOrchardIndex(ctx, rpc, int64(anchorHeight))
	if err != nil {
		return types.TxPlan{}, err
	}
	if len(orchard.CMXHex) == 0 {
		return types.TxPlan{}, errors.New("txbuild: no orchard commitments")
	}

	notes, err := listUnspentOrchardNotes(ctx, rpc, int64(anchorHeight), cfg.MinConfirmations, cfg.Account)
	if err != nil {
		return types.TxPlan{}, err
	}
	notes = logic.FilterNotesMinValue(notes, cfg.MinNoteZat)
	notes = filterExcludedUnspentNotes(notes, excludedNoteIDs)
	if len(notes) == 0 {
		return types.TxPlan{}, types.CodedError{Code: types.ErrCodeInsufficientBalance, Message: "no spendable notes"}
	}
	if err := ensureOrchardSpendLimit(len(notes)); err != nil {
		return types.TxPlan{}, err
	}

	var totalIn uint64
	for _, n := range notes {
		var ok bool
		totalIn, ok = addUint64(totalIn, n.ValueZat)
		if !ok {
			return types.TxPlan{}, errors.New("txbuild: notes sum overflow")
		}
	}
	feePolicy := logic.FeePolicy{
		Multiplier: cfg.FeeMultiplier,
		AddZat:     cfg.FeeAddZat,
	}
	feeMin := logic.RequiredFeeSend(len(notes), 1)
	feeZat, err := feePolicy.Apply(feeMin)
	if err != nil {
		return types.TxPlan{}, err
	}
	if totalIn <= feeZat {
		return types.TxPlan{}, types.CodedError{Code: types.ErrCodeInsufficientBalance, Message: "insufficient funds"}
	}
	amount := totalIn - feeZat

	positions := make([]uint32, 0, len(notes))
	planNotes := make([]types.OrchardSpendNote, 0, len(notes))
	for _, n := range notes {
		key := fmt.Sprintf("%s:%d", n.TxID, n.ActionIndex)
		act, ok := orchard.ByOutpoint[key]
		if !ok {
			return types.TxPlan{}, errors.New("txbuild: missing orchard action for selected note")
		}
		planNotes = append(planNotes, types.OrchardSpendNote{
			NoteID:          key,
			ActionNullifier: act.Nullifier,
			CMX:             act.CMX,
			Position:        act.Position,
			Path:            nil,
			EphemeralKey:    act.EphemeralKey,
			EncCiphertext:   act.EncCiphertext,
		})
		positions = append(positions, act.Position)
	}

	wit, err := witness.OrchardWitness(orchard.CMXHex, positions)
	if err != nil {
		return types.TxPlan{}, err
	}
	if len(wit.Paths) != len(planNotes) {
		return types.TxPlan{}, errors.New("txbuild: witness response mismatch")
	}

	for i := range planNotes {
		if wit.Paths[i].Position != planNotes[i].Position {
			return types.TxPlan{}, errors.New("txbuild: witness response mismatch")
		}
		planNotes[i].Path = wit.Paths[i].AuthPath
	}

	expiryHeight, err := logic.ExpiryHeightFromTip(anchorHeight, cfg.ExpiryOffset)
	if err != nil {
		return types.TxPlan{}, errors.New("txbuild: expiry height overflow")
	}

	plan := types.TxPlan{
		Version:      types.V0,
		Kind:         types.TxPlanKindSweep,
		WalletID:     cfg.WalletID,
		CoinType:     coinType,
		Account:      cfg.Account,
		Chain:        chainInfo.Chain,
		BranchID:     chainInfo.BranchID,
		AnchorHeight: anchorHeight,
		Anchor:       wit.Root,
		ExpiryHeight: expiryHeight,
		Outputs: []types.TxOutput{
			{ToAddress: cfg.ToAddress, AmountZat: strconv.FormatUint(amount, 10), MemoHex: cfg.MemoHex},
		},
		ChangeAddress: cfg.ChangeAddress,
		FeeZat:        strconv.FormatUint(feeZat, 10),
		Notes:         planNotes,
	}
	plan, err = signerCompatiblePlan(plan, false)
	if err != nil {
		return types.TxPlan{}, err
	}
	if err := verifyNodeAnchor(ctx, rpc, nodeSnapshot); err != nil {
		return types.TxPlan{}, err
	}
	if err := verifyChainContext(ctx, rpc, chainInfo, nodeSnapshot, expiryHeight); err != nil {
		return types.TxPlan{}, err
	}
	return plan, nil
}

type ConsolidateConfig struct {
	RPCURL  string
	RPCUser string
	RPCPass string

	ScanURL string
	// Optional bearer token for HTTP requests to juno-scan.
	// Sent as: Authorization: Bearer <token>
	ScanBearerToken string

	WalletID string
	CoinType uint32
	Account  uint32
	// ExcludedNoteIDs contains canonical note IDs that must not be selected.
	// Unknown canonical IDs are ignored; malformed or duplicate IDs are rejected.
	ExcludedNoteIDs []string

	ToAddress     string
	MemoHex       string
	ChangeAddress string

	MaxSpends int

	MinConfirmations int64
	ExpiryOffset     uint32
	MinNoteZat       uint64

	FeeMultiplier uint64
	FeeAddZat     uint64
}

func PlanConsolidate(ctx context.Context, cfg ConsolidateConfig) (types.TxPlan, error) {
	cfg.RPCURL = strings.TrimSpace(cfg.RPCURL)
	cfg.RPCUser = strings.TrimSpace(cfg.RPCUser)
	cfg.RPCPass = strings.TrimSpace(cfg.RPCPass)
	cfg.ScanURL = strings.TrimSpace(cfg.ScanURL)
	cfg.ScanBearerToken = strings.TrimSpace(cfg.ScanBearerToken)
	cfg.WalletID = strings.TrimSpace(cfg.WalletID)
	cfg.ToAddress = strings.TrimSpace(cfg.ToAddress)
	cfg.MemoHex = strings.TrimSpace(cfg.MemoHex)
	cfg.ChangeAddress = strings.TrimSpace(cfg.ChangeAddress)

	if cfg.RPCURL == "" {
		return types.TxPlan{}, types.CodedError{Code: types.ErrCodeInvalidRequest, Message: "rpc url required"}
	}
	if cfg.WalletID == "" {
		return types.TxPlan{}, types.CodedError{Code: types.ErrCodeInvalidRequest, Message: "wallet_id required"}
	}
	if err := validateAccount(cfg.Account); err != nil {
		return types.TxPlan{}, err
	}
	excludedNoteIDs, err := validateExcludedNoteIDs(cfg.ExcludedNoteIDs)
	if err != nil {
		return types.TxPlan{}, err
	}
	if cfg.ToAddress == "" {
		return types.TxPlan{}, types.CodedError{Code: types.ErrCodeInvalidRequest, Message: "to required"}
	}
	if cfg.ChangeAddress == "" {
		cfg.ChangeAddress = cfg.ToAddress
	}
	if cfg.MaxSpends == 0 {
		cfg.MaxSpends = 50
	}
	if cfg.MaxSpends < 2 || cfg.MaxSpends > MaxOrchardSpendNotes {
		return types.TxPlan{}, types.CodedError{Code: types.ErrCodeInvalidRequest, Message: fmt.Sprintf("max_spends must be between 2 and %d", MaxOrchardSpendNotes)}
	}
	cfg.MinConfirmations = normalizedMinConfirmations(cfg.MinConfirmations)
	if cfg.ExpiryOffset == 0 {
		cfg.ExpiryOffset = 40
	}
	if cfg.ExpiryOffset < 4 {
		return types.TxPlan{}, types.CodedError{Code: types.ErrCodeInvalidRequest, Message: "expiry_offset must be >= 4"}
	}
	cfg.FeeMultiplier = normalizedFeeMultiplier(cfg.FeeMultiplier)

	rpc := junocashd.New(cfg.RPCURL, cfg.RPCUser, cfg.RPCPass)

	chainInfo, err := chain.GetChainInfo(ctx, rpc)
	if err != nil {
		return types.TxPlan{}, err
	}

	coinType, err := resolveCoinType(chainInfo.Chain, cfg.CoinType)
	if err != nil {
		return types.TxPlan{}, err
	}
	if chainInfo.Height < 0 {
		return types.TxPlan{}, errors.New("txbuild: invalid chain height")
	}
	if chainInfo.Height > int64(^uint32(0)) {
		return types.TxPlan{}, errors.New("txbuild: chain height too large")
	}
	anchorHeight := uint32(chainInfo.Height)

	if cfg.ScanURL != "" {
		return planConsolidateWithScan(ctx, rpc, chainInfo, coinType, cfg, excludedNoteIDs)
	}
	nodeSnapshot, err := captureNodeAnchor(ctx, rpc, chainInfo.Height)
	if err != nil {
		return types.TxPlan{}, err
	}

	orchard, err := chain.BuildOrchardIndex(ctx, rpc, int64(anchorHeight))
	if err != nil {
		return types.TxPlan{}, err
	}
	if len(orchard.CMXHex) == 0 {
		return types.TxPlan{}, errors.New("txbuild: no orchard commitments")
	}

	notes, err := listUnspentOrchardNotes(ctx, rpc, int64(anchorHeight), cfg.MinConfirmations, cfg.Account)
	if err != nil {
		return types.TxPlan{}, err
	}
	notes = logic.FilterNotesMinValue(notes, cfg.MinNoteZat)
	notes = filterExcludedUnspentNotes(notes, excludedNoteIDs)
	if len(notes) < 2 {
		return types.TxPlan{}, types.CodedError{Code: types.ErrCodeInvalidRequest, Message: "not enough spendable notes to consolidate"}
	}

	feePolicy := logic.FeePolicy{
		Multiplier: cfg.FeeMultiplier,
		AddZat:     cfg.FeeAddZat,
	}
	selected, feeZat, err := selectNotesForConsolidation(notes, cfg.MaxSpends, feePolicy)
	if err != nil {
		return types.TxPlan{}, err
	}

	var totalIn uint64
	for _, n := range selected {
		var ok bool
		totalIn, ok = addUint64(totalIn, n.ValueZat)
		if !ok {
			return types.TxPlan{}, errors.New("txbuild: selected notes sum overflow")
		}
	}
	if totalIn <= feeZat {
		return types.TxPlan{}, types.CodedError{Code: types.ErrCodeInsufficientBalance, Message: "insufficient funds"}
	}
	amount := totalIn - feeZat

	positions := make([]uint32, 0, len(selected))
	planNotes := make([]types.OrchardSpendNote, 0, len(selected))
	for _, n := range selected {
		key := fmt.Sprintf("%s:%d", n.TxID, n.ActionIndex)
		act, ok := orchard.ByOutpoint[key]
		if !ok {
			return types.TxPlan{}, errors.New("txbuild: missing orchard action for selected note")
		}
		planNotes = append(planNotes, types.OrchardSpendNote{
			NoteID:          key,
			ActionNullifier: act.Nullifier,
			CMX:             act.CMX,
			Position:        act.Position,
			Path:            nil,
			EphemeralKey:    act.EphemeralKey,
			EncCiphertext:   act.EncCiphertext,
		})
		positions = append(positions, act.Position)
	}

	wit, err := witness.OrchardWitness(orchard.CMXHex, positions)
	if err != nil {
		return types.TxPlan{}, err
	}
	if len(wit.Paths) != len(planNotes) {
		return types.TxPlan{}, errors.New("txbuild: witness response mismatch")
	}

	for i := range planNotes {
		if wit.Paths[i].Position != planNotes[i].Position {
			return types.TxPlan{}, errors.New("txbuild: witness response mismatch")
		}
		planNotes[i].Path = wit.Paths[i].AuthPath
	}

	expiryHeight, err := logic.ExpiryHeightFromTip(anchorHeight, cfg.ExpiryOffset)
	if err != nil {
		return types.TxPlan{}, errors.New("txbuild: expiry height overflow")
	}

	plan := types.TxPlan{
		Version:      types.V0,
		Kind:         types.TxPlanKindRebalance,
		WalletID:     cfg.WalletID,
		CoinType:     coinType,
		Account:      cfg.Account,
		Chain:        chainInfo.Chain,
		BranchID:     chainInfo.BranchID,
		AnchorHeight: anchorHeight,
		Anchor:       wit.Root,
		ExpiryHeight: expiryHeight,
		Outputs: []types.TxOutput{
			{ToAddress: cfg.ToAddress, AmountZat: strconv.FormatUint(amount, 10), MemoHex: cfg.MemoHex},
		},
		ChangeAddress: cfg.ChangeAddress,
		FeeZat:        strconv.FormatUint(feeZat, 10),
		Notes:         planNotes,
	}
	plan, err = signerCompatiblePlan(plan, false)
	if err != nil {
		return types.TxPlan{}, err
	}
	if err := verifyNodeAnchor(ctx, rpc, nodeSnapshot); err != nil {
		return types.TxPlan{}, err
	}
	if err := verifyChainContext(ctx, rpc, chainInfo, nodeSnapshot, expiryHeight); err != nil {
		return types.TxPlan{}, err
	}
	return plan, nil
}

type spendableNote struct {
	TxID        string
	ActionIndex uint32
	Height      int64
	Position    uint32
	ValueZat    uint64
}

func planWithScan(ctx context.Context, rpc *junocashd.Client, chainInfo chain.ChainInfo, coinType uint32, cfg PlanConfig, totalOut uint64, excludedNoteIDs map[string]struct{}, report *PlanReport) (types.TxPlan, error) {
	sc, err := newScanClient(cfg.ScanURL, cfg.ScanBearerToken)
	if err != nil {
		return types.TxPlan{}, err
	}
	snapshot, err := captureScannerAnchor(ctx, rpc, sc, chainInfo.Height)
	if err != nil {
		return types.TxPlan{}, err
	}

	notes, err := listSpendableNotesFromScan(ctx, sc, cfg.WalletID, snapshot.height, cfg.MinConfirmations, cfg.MinNoteZat)
	if err != nil {
		return types.TxPlan{}, err
	}

	unspent := logic.FilterNotesMinValue(notesToUnspent(notes), cfg.MinNoteZat)
	unspent = filterExcludedUnspentNotes(unspent, excludedNoteIDs)
	if len(unspent) == 0 {
		return types.TxPlan{}, types.CodedError{Code: types.ErrCodeInsufficientBalance, Message: "no spendable notes"}
	}

	feePolicy := logic.FeePolicy{
		Multiplier: cfg.FeeMultiplier,
		AddZat:     cfg.FeeAddZat,
	}
	selected, feeZat, err := selectNotesForPlan(unspent, totalOut, len(cfg.Outputs), feePolicy)
	if err != nil {
		return types.TxPlan{}, err
	}
	selected, feeZat, *report, err = addExtraSpendsForPlan(unspent, selected, totalOut, len(cfg.Outputs), feeZat, feePolicy, cfg.ExtraSpends, cfg.ExtraSpendMaxZat, cfg.MinChangeZat)
	if err != nil {
		return types.TxPlan{}, err
	}

	var totalIn uint64
	for _, n := range selected {
		var ok bool
		totalIn, ok = addUint64(totalIn, n.ValueZat)
		if !ok {
			return types.TxPlan{}, errors.New("txbuild: selected notes sum overflow")
		}
	}
	feeZat, _, err = logic.SuppressDustChange(totalIn, totalOut, feeZat, cfg.MinChangeZat)
	if err != nil {
		return types.TxPlan{}, err
	}
	hasChange, err := orchardChangeRequired(totalIn, totalOut, feeZat)
	if err != nil {
		return types.TxPlan{}, err
	}

	noteByOutpoint := make(map[string]spendableNote, len(notes))
	for _, n := range notes {
		key := fmt.Sprintf("%s:%d", n.TxID, n.ActionIndex)
		noteByOutpoint[key] = n
	}

	positions := make([]uint32, 0, len(selected))
	planNotes := make([]types.OrchardSpendNote, 0, len(selected))
	blockCache := make(map[int64]blockV2)
	for _, n := range selected {
		key := fmt.Sprintf("%s:%d", n.TxID, n.ActionIndex)
		meta, ok := noteByOutpoint[key]
		if !ok {
			return types.TxPlan{}, errors.New("txbuild: missing note metadata from scan")
		}

		act, err := orchardActionForNote(ctx, rpc, blockCache, meta.Height, meta.TxID, meta.ActionIndex)
		if err != nil {
			return types.TxPlan{}, err
		}

		positions = append(positions, meta.Position)
		planNotes = append(planNotes, types.OrchardSpendNote{
			NoteID:          key,
			ActionNullifier: act.Nullifier,
			CMX:             act.CMX,
			Position:        meta.Position,
			Path:            nil,
			EphemeralKey:    act.EphemeralKey,
			EncCiphertext:   act.EncCiphertext,
		})
	}

	wit, err := orchardWitnessAtAnchor(ctx, sc, snapshot.height, positions)
	if err != nil {
		return types.TxPlan{}, err
	}
	if strings.TrimSpace(wit.Root) == "" || len(wit.Paths) != len(positions) {
		return types.TxPlan{}, errors.New("txbuild: invalid witness response")
	}
	if wit.AnchorHeight < 0 || wit.AnchorHeight > int64(^uint32(0)) {
		return types.TxPlan{}, errors.New("txbuild: invalid witness anchor_height")
	}

	pathByPos := make(map[uint32][]string, len(wit.Paths))
	for _, p := range wit.Paths {
		pathByPos[p.Position] = p.AuthPath
	}
	for i := range planNotes {
		p, ok := pathByPos[planNotes[i].Position]
		if !ok || len(p) != 32 {
			return types.TxPlan{}, errors.New("txbuild: witness path missing")
		}
		planNotes[i].Path = p
	}

	expiryHeight, err := logic.ExpiryHeightFromTip(uint32(snapshot.height), cfg.ExpiryOffset)
	if err != nil {
		return types.TxPlan{}, errors.New("txbuild: expiry height overflow")
	}

	plan := types.TxPlan{
		Version:       types.V0,
		Kind:          cfg.Kind,
		WalletID:      cfg.WalletID,
		CoinType:      coinType,
		Account:       cfg.Account,
		Chain:         chainInfo.Chain,
		BranchID:      chainInfo.BranchID,
		AnchorHeight:  uint32(wit.AnchorHeight),
		Anchor:        wit.Root,
		ExpiryHeight:  expiryHeight,
		Outputs:       cfg.Outputs,
		ChangeAddress: cfg.ChangeAddress,
		FeeZat:        strconv.FormatUint(feeZat, 10),
		Notes:         planNotes,
	}
	plan, err = signerCompatiblePlan(plan, hasChange)
	if err != nil {
		return types.TxPlan{}, err
	}
	if err := verifySelectedNotesStillSpendable(ctx, sc, cfg.WalletID, snapshot.height, cfg.MinConfirmations, cfg.MinNoteZat, selected); err != nil {
		return types.TxPlan{}, err
	}
	if err := verifyScannerAnchor(ctx, rpc, sc, snapshot); err != nil {
		return types.TxPlan{}, err
	}
	if err := verifyChainContext(ctx, rpc, chainInfo, snapshot, expiryHeight); err != nil {
		return types.TxPlan{}, err
	}
	return plan, nil
}

func planConsolidateWithScan(ctx context.Context, rpc *junocashd.Client, chainInfo chain.ChainInfo, coinType uint32, cfg ConsolidateConfig, excludedNoteIDs map[string]struct{}) (types.TxPlan, error) {
	sc, err := newScanClient(cfg.ScanURL, cfg.ScanBearerToken)
	if err != nil {
		return types.TxPlan{}, err
	}
	snapshot, err := captureScannerAnchor(ctx, rpc, sc, chainInfo.Height)
	if err != nil {
		return types.TxPlan{}, err
	}

	notes, err := listSpendableNotesFromScan(ctx, sc, cfg.WalletID, snapshot.height, cfg.MinConfirmations, cfg.MinNoteZat)
	if err != nil {
		return types.TxPlan{}, err
	}

	unspent := logic.FilterNotesMinValue(notesToUnspent(notes), cfg.MinNoteZat)
	unspent = filterExcludedUnspentNotes(unspent, excludedNoteIDs)
	if len(unspent) < 2 {
		return types.TxPlan{}, types.CodedError{Code: types.ErrCodeInvalidRequest, Message: "not enough spendable notes to consolidate"}
	}

	feePolicy := logic.FeePolicy{
		Multiplier: cfg.FeeMultiplier,
		AddZat:     cfg.FeeAddZat,
	}
	selected, feeZat, err := selectNotesForConsolidation(unspent, cfg.MaxSpends, feePolicy)
	if err != nil {
		return types.TxPlan{}, err
	}

	var totalIn uint64
	for _, n := range selected {
		var ok bool
		totalIn, ok = addUint64(totalIn, n.ValueZat)
		if !ok {
			return types.TxPlan{}, errors.New("txbuild: selected notes sum overflow")
		}
	}
	if totalIn <= feeZat {
		return types.TxPlan{}, types.CodedError{Code: types.ErrCodeInsufficientBalance, Message: "insufficient funds"}
	}
	amount := totalIn - feeZat

	noteByOutpoint := make(map[string]spendableNote, len(notes))
	for _, n := range notes {
		key := fmt.Sprintf("%s:%d", n.TxID, n.ActionIndex)
		noteByOutpoint[key] = n
	}

	positions := make([]uint32, 0, len(selected))
	planNotes := make([]types.OrchardSpendNote, 0, len(selected))
	blockCache := make(map[int64]blockV2)
	for _, n := range selected {
		key := fmt.Sprintf("%s:%d", n.TxID, n.ActionIndex)
		meta, ok := noteByOutpoint[key]
		if !ok {
			return types.TxPlan{}, errors.New("txbuild: missing note metadata from scan")
		}

		act, err := orchardActionForNote(ctx, rpc, blockCache, meta.Height, meta.TxID, meta.ActionIndex)
		if err != nil {
			return types.TxPlan{}, err
		}

		positions = append(positions, meta.Position)
		planNotes = append(planNotes, types.OrchardSpendNote{
			NoteID:          key,
			ActionNullifier: act.Nullifier,
			CMX:             act.CMX,
			Position:        meta.Position,
			Path:            nil,
			EphemeralKey:    act.EphemeralKey,
			EncCiphertext:   act.EncCiphertext,
		})
	}

	wit, err := orchardWitnessAtAnchor(ctx, sc, snapshot.height, positions)
	if err != nil {
		return types.TxPlan{}, err
	}
	if strings.TrimSpace(wit.Root) == "" || len(wit.Paths) != len(positions) {
		return types.TxPlan{}, errors.New("txbuild: invalid witness response")
	}
	if wit.AnchorHeight < 0 || wit.AnchorHeight > int64(^uint32(0)) {
		return types.TxPlan{}, errors.New("txbuild: invalid witness anchor_height")
	}

	pathByPos := make(map[uint32][]string, len(wit.Paths))
	for _, p := range wit.Paths {
		pathByPos[p.Position] = p.AuthPath
	}
	for i := range planNotes {
		p, ok := pathByPos[planNotes[i].Position]
		if !ok || len(p) != 32 {
			return types.TxPlan{}, errors.New("txbuild: witness path missing")
		}
		planNotes[i].Path = p
	}

	expiryHeight, err := logic.ExpiryHeightFromTip(uint32(snapshot.height), cfg.ExpiryOffset)
	if err != nil {
		return types.TxPlan{}, errors.New("txbuild: expiry height overflow")
	}

	plan := types.TxPlan{
		Version:      types.V0,
		Kind:         types.TxPlanKindRebalance,
		WalletID:     cfg.WalletID,
		CoinType:     coinType,
		Account:      cfg.Account,
		Chain:        chainInfo.Chain,
		BranchID:     chainInfo.BranchID,
		AnchorHeight: uint32(wit.AnchorHeight),
		Anchor:       wit.Root,
		ExpiryHeight: expiryHeight,
		Outputs: []types.TxOutput{
			{ToAddress: cfg.ToAddress, AmountZat: strconv.FormatUint(amount, 10), MemoHex: cfg.MemoHex},
		},
		ChangeAddress: cfg.ChangeAddress,
		FeeZat:        strconv.FormatUint(feeZat, 10),
		Notes:         planNotes,
	}
	plan, err = signerCompatiblePlan(plan, false)
	if err != nil {
		return types.TxPlan{}, err
	}
	if err := verifySelectedNotesStillSpendable(ctx, sc, cfg.WalletID, snapshot.height, cfg.MinConfirmations, cfg.MinNoteZat, selected); err != nil {
		return types.TxPlan{}, err
	}
	if err := verifyScannerAnchor(ctx, rpc, sc, snapshot); err != nil {
		return types.TxPlan{}, err
	}
	if err := verifyChainContext(ctx, rpc, chainInfo, snapshot, expiryHeight); err != nil {
		return types.TxPlan{}, err
	}
	return plan, nil
}

func planSweepWithScan(ctx context.Context, rpc *junocashd.Client, chainInfo chain.ChainInfo, coinType uint32, cfg SweepConfig, excludedNoteIDs map[string]struct{}) (types.TxPlan, error) {
	sc, err := newScanClient(cfg.ScanURL, cfg.ScanBearerToken)
	if err != nil {
		return types.TxPlan{}, err
	}
	snapshot, err := captureScannerAnchor(ctx, rpc, sc, chainInfo.Height)
	if err != nil {
		return types.TxPlan{}, err
	}

	notes, err := listSpendableNotesFromScan(ctx, sc, cfg.WalletID, snapshot.height, cfg.MinConfirmations, cfg.MinNoteZat)
	if err != nil {
		return types.TxPlan{}, err
	}
	notes = filterExcludedSpendableNotes(notes, excludedNoteIDs)
	if len(notes) == 0 {
		return types.TxPlan{}, types.CodedError{Code: types.ErrCodeInsufficientBalance, Message: "no spendable notes"}
	}
	if err := ensureOrchardSpendLimit(len(notes)); err != nil {
		return types.TxPlan{}, err
	}

	var totalIn uint64
	for _, n := range notes {
		var ok bool
		totalIn, ok = addUint64(totalIn, n.ValueZat)
		if !ok {
			return types.TxPlan{}, errors.New("txbuild: notes sum overflow")
		}
	}
	feePolicy := logic.FeePolicy{
		Multiplier: cfg.FeeMultiplier,
		AddZat:     cfg.FeeAddZat,
	}
	feeMin := logic.RequiredFeeSend(len(notes), 1)
	feeZat, err := feePolicy.Apply(feeMin)
	if err != nil {
		return types.TxPlan{}, err
	}
	if totalIn <= feeZat {
		return types.TxPlan{}, types.CodedError{Code: types.ErrCodeInsufficientBalance, Message: "insufficient funds"}
	}
	amount := totalIn - feeZat

	positions := make([]uint32, 0, len(notes))
	planNotes := make([]types.OrchardSpendNote, 0, len(notes))
	blockCache := make(map[int64]blockV2)
	for _, n := range notes {
		act, err := orchardActionForNote(ctx, rpc, blockCache, n.Height, n.TxID, n.ActionIndex)
		if err != nil {
			return types.TxPlan{}, err
		}
		positions = append(positions, n.Position)
		planNotes = append(planNotes, types.OrchardSpendNote{
			NoteID:          fmt.Sprintf("%s:%d", n.TxID, n.ActionIndex),
			ActionNullifier: act.Nullifier,
			CMX:             act.CMX,
			Position:        n.Position,
			Path:            nil,
			EphemeralKey:    act.EphemeralKey,
			EncCiphertext:   act.EncCiphertext,
		})
	}

	wit, err := orchardWitnessAtAnchor(ctx, sc, snapshot.height, positions)
	if err != nil {
		return types.TxPlan{}, err
	}
	if strings.TrimSpace(wit.Root) == "" || len(wit.Paths) != len(positions) {
		return types.TxPlan{}, errors.New("txbuild: invalid witness response")
	}
	if wit.AnchorHeight < 0 || wit.AnchorHeight > int64(^uint32(0)) {
		return types.TxPlan{}, errors.New("txbuild: invalid witness anchor_height")
	}
	pathByPos := make(map[uint32][]string, len(wit.Paths))
	for _, p := range wit.Paths {
		pathByPos[p.Position] = p.AuthPath
	}
	for i := range planNotes {
		p, ok := pathByPos[planNotes[i].Position]
		if !ok || len(p) != 32 {
			return types.TxPlan{}, errors.New("txbuild: witness path missing")
		}
		planNotes[i].Path = p
	}

	expiryHeight, err := logic.ExpiryHeightFromTip(uint32(snapshot.height), cfg.ExpiryOffset)
	if err != nil {
		return types.TxPlan{}, errors.New("txbuild: expiry height overflow")
	}

	plan := types.TxPlan{
		Version:      types.V0,
		Kind:         types.TxPlanKindSweep,
		WalletID:     cfg.WalletID,
		CoinType:     coinType,
		Account:      cfg.Account,
		Chain:        chainInfo.Chain,
		BranchID:     chainInfo.BranchID,
		AnchorHeight: uint32(wit.AnchorHeight),
		Anchor:       wit.Root,
		ExpiryHeight: expiryHeight,
		Outputs: []types.TxOutput{
			{ToAddress: cfg.ToAddress, AmountZat: strconv.FormatUint(amount, 10), MemoHex: cfg.MemoHex},
		},
		ChangeAddress: cfg.ChangeAddress,
		FeeZat:        strconv.FormatUint(feeZat, 10),
		Notes:         planNotes,
	}
	plan, err = signerCompatiblePlan(plan, false)
	if err != nil {
		return types.TxPlan{}, err
	}
	if err := verifySelectedNotesStillSpendable(ctx, sc, cfg.WalletID, snapshot.height, cfg.MinConfirmations, cfg.MinNoteZat, notesToUnspent(notes)); err != nil {
		return types.TxPlan{}, err
	}
	if err := verifyScannerAnchor(ctx, rpc, sc, snapshot); err != nil {
		return types.TxPlan{}, err
	}
	if err := verifyChainContext(ctx, rpc, chainInfo, snapshot, expiryHeight); err != nil {
		return types.TxPlan{}, err
	}
	return plan, nil
}

func verifyChainContext(ctx context.Context, rpc chain.RPC, expected chain.ChainInfo, snapshot scannerAnchorSnapshot, expiryHeight uint32) error {
	current, err := chain.GetChainInfo(ctx, rpc)
	if err != nil {
		return err
	}
	if !strings.EqualFold(strings.TrimSpace(current.Chain), strings.TrimSpace(expected.Chain)) {
		return errors.New("txbuild: node network changed during planning")
	}
	if current.Height < snapshot.height {
		return errors.New("txbuild: node tip moved behind the planning anchor")
	}
	if current.BranchID != expected.BranchID {
		return errors.New("txbuild: next-block consensus branch changed during planning; retry")
	}
	nextBlockHeight := uint64(current.Height) + 1
	if nextBlockHeight+txExpiringSoonThreshold > uint64(expiryHeight) {
		return errors.New("txbuild: transaction expiry became too close during planning; retry")
	}
	return nil
}

func selectNotesForConsolidation(notes []logic.UnspentNote, maxSpends int, feePolicy logic.FeePolicy) ([]logic.UnspentNote, uint64, error) {
	if maxSpends == 0 {
		maxSpends = 50
	}
	if maxSpends < 2 || maxSpends > MaxOrchardSpendNotes {
		return nil, 0, types.CodedError{Code: types.ErrCodeInvalidRequest, Message: fmt.Sprintf("max_spends must be between 2 and %d", MaxOrchardSpendNotes)}
	}
	if maxSpends > len(notes) {
		maxSpends = len(notes)
	}

	notesAsc := append([]logic.UnspentNote(nil), notes...)
	sort.Slice(notesAsc, func(i, j int) bool {
		if notesAsc[i].ValueZat != notesAsc[j].ValueZat {
			return notesAsc[i].ValueZat < notesAsc[j].ValueZat
		}
		if notesAsc[i].TxID != notesAsc[j].TxID {
			return notesAsc[i].TxID < notesAsc[j].TxID
		}
		return notesAsc[i].ActionIndex < notesAsc[j].ActionIndex
	})

	prefix := make([]uint64, len(notesAsc)+1)
	for i := 0; i < len(notesAsc); i++ {
		v, ok := addUint64(prefix[i], notesAsc[i].ValueZat)
		if !ok {
			return nil, 0, errors.New("txbuild: notes sum overflow")
		}
		prefix[i+1] = v
	}
	suffix := make([]uint64, len(notesAsc)+1)
	for i := 0; i < len(notesAsc); i++ {
		v, ok := addUint64(suffix[i], notesAsc[len(notesAsc)-1-i].ValueZat)
		if !ok {
			return nil, 0, errors.New("txbuild: notes sum overflow")
		}
		suffix[i+1] = v
	}

	for k := maxSpends; k >= 2; k-- {
		feeMin := logic.RequiredFeeSend(k, 1)
		feeZat, err := feePolicy.Apply(feeMin)
		if err != nil {
			return nil, 0, err
		}

		found := false
		var bestT int
		for t := k; t >= 0; t-- {
			small := prefix[t]
			large := suffix[k-t]
			total, ok := addUint64(small, large)
			if !ok {
				return nil, 0, errors.New("txbuild: notes sum overflow")
			}
			if total > feeZat {
				found = true
				bestT = t
				break
			}
		}
		if !found {
			continue
		}

		selected := make([]logic.UnspentNote, 0, k)
		selected = append(selected, notesAsc[:bestT]...)
		if k-bestT > 0 {
			selected = append(selected, notesAsc[len(notesAsc)-(k-bestT):]...)
		}
		return selected, feeZat, nil
	}

	return nil, 0, types.CodedError{Code: types.ErrCodeInsufficientBalance, Message: "insufficient funds"}
}

type bearerAuthRoundTripper struct {
	token string
	next  http.RoundTripper
}

func (t bearerAuthRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	next := t.next
	if next == nil {
		next = http.DefaultTransport
	}
	if t.token == "" {
		return next.RoundTrip(req)
	}
	r2 := req.Clone(req.Context())
	r2.Header = req.Header.Clone()
	r2.Header.Set("Authorization", "Bearer "+t.token)
	return next.RoundTrip(r2)
}

func newScanClient(baseURL, bearerToken string) (*junoscan.Client, error) {
	bearerToken = strings.TrimSpace(bearerToken)
	if bearerToken == "" {
		return junoscan.New(baseURL)
	}

	hc := &http.Client{
		Timeout: 15 * time.Second,
		Transport: bearerAuthRoundTripper{
			token: bearerToken,
			next:  http.DefaultTransport,
		},
	}
	return junoscan.New(baseURL, junoscan.WithHTTPClient(hc))
}

func listSpendableNotesFromScan(ctx context.Context, sc *junoscan.Client, walletID string, tipHeight int64, minConf int64, minNoteZat uint64) ([]spendableNote, error) {
	maxSignedZat := uint64(^uint64(0) >> 1)
	if minNoteZat > maxSignedZat {
		return []spendableNote{}, nil
	}

	opts := junoscan.ListWalletNotesOptions{
		OnlyUnspent: true,
		Direction:   "incoming",
		Limit:       1000,
	}
	if minNoteZat > 0 {
		opts.MinValueZat = int64(minNoteZat)
	}

	seenCursor := map[string]struct{}{}
	seenNotes := map[string]struct{}{}
	out := make([]spendableNote, 0, 1024)
	for {
		page, err := sc.ListWalletNotesPage(ctx, walletID, opts)
		if err != nil {
			return nil, err
		}
		for _, n := range page.Notes {
			direction := strings.ToLower(strings.TrimSpace(n.Direction))
			switch direction {
			case "incoming":
			case "outgoing":
				continue
			case "":
				return nil, errors.New("txbuild: scan note missing direction")
			default:
				return nil, fmt.Errorf("txbuild: scan note has invalid direction %q", direction)
			}
			if n.PendingSpentTxID != nil && strings.TrimSpace(*n.PendingSpentTxID) != "" {
				continue
			}
			if n.Position == nil || *n.Position < 0 {
				continue
			}
			if n.Height < 0 {
				continue
			}
			if tipHeight < n.Height {
				continue
			}
			conf := tipHeight - n.Height + 1
			if conf < minConf {
				continue
			}
			if n.ActionIndex < 0 {
				continue
			}
			if n.ValueZat <= 0 {
				continue
			}
			if minNoteZat > 0 && uint64(n.ValueZat) < minNoteZat {
				continue
			}
			if n.ValueZat > int64(^uint64(0)>>1) {
				return nil, errors.New("txbuild: note value too large")
			}
			if *n.Position > int64(^uint32(0)) {
				return nil, errors.New("txbuild: note position too large")
			}
			key := fmt.Sprintf("%s:%d", strings.ToLower(strings.TrimSpace(n.TxID)), n.ActionIndex)
			if _, exists := seenNotes[key]; exists {
				return nil, errors.New("txbuild: scan returned a duplicate note")
			}
			seenNotes[key] = struct{}{}
			out = append(out, spendableNote{
				TxID:        strings.ToLower(strings.TrimSpace(n.TxID)),
				ActionIndex: uint32(n.ActionIndex),
				Height:      n.Height,
				Position:    uint32(*n.Position),
				ValueZat:    uint64(n.ValueZat),
			})
		}

		next := strings.TrimSpace(page.NextCursor)
		if next == "" {
			break
		}
		if _, ok := seenCursor[next]; ok {
			return nil, errors.New("txbuild: scan notes cursor did not advance")
		}
		seenCursor[next] = struct{}{}
		opts.Cursor = next
	}
	return out, nil
}

func verifySelectedNotesStillSpendable(ctx context.Context, sc *junoscan.Client, walletID string, tipHeight, minConf int64, minNoteZat uint64, selected []logic.UnspentNote) error {
	current, err := listSpendableNotesFromScan(ctx, sc, walletID, tipHeight, minConf, minNoteZat)
	if err != nil {
		return err
	}
	available := make(map[string]uint64, len(current))
	for _, note := range current {
		available[fmt.Sprintf("%s:%d", note.TxID, note.ActionIndex)] = note.ValueZat
	}
	for _, note := range selected {
		key := fmt.Sprintf("%s:%d", strings.ToLower(strings.TrimSpace(note.TxID)), note.ActionIndex)
		if value, ok := available[key]; !ok || value != note.ValueZat {
			return errors.New("txbuild: selected note spendability changed during planning; retry")
		}
	}
	return nil
}

func notesToUnspent(ns []spendableNote) []logic.UnspentNote {
	out := make([]logic.UnspentNote, 0, len(ns))
	for _, n := range ns {
		out = append(out, logic.UnspentNote{TxID: n.TxID, ActionIndex: n.ActionIndex, ValueZat: n.ValueZat, Height: n.Height})
	}
	return out
}

func filterExcludedSpendableNotes(notes []spendableNote, excluded map[string]struct{}) []spendableNote {
	if len(excluded) == 0 {
		return notes
	}
	out := make([]spendableNote, 0, len(notes))
	for _, note := range notes {
		noteID := fmt.Sprintf("%s:%d", strings.ToLower(strings.TrimSpace(note.TxID)), note.ActionIndex)
		if _, skip := excluded[noteID]; skip {
			continue
		}
		out = append(out, note)
	}
	return out
}

type orchardAction struct {
	Nullifier     string
	CMX           string
	EphemeralKey  string
	EncCiphertext string
}

type blockV2 struct {
	Tx []struct {
		TxID    string `json:"txid"`
		Orchard struct {
			Actions []struct {
				Nullifier     string `json:"nullifier"`
				CMX           string `json:"cmx"`
				EphemeralKey  string `json:"ephemeralKey"`
				EncCiphertext string `json:"encCiphertext"`
			} `json:"actions"`
		} `json:"orchard"`
	} `json:"tx"`
}

func orchardActionForNote(ctx context.Context, rpc *junocashd.Client, cache map[int64]blockV2, height int64, txid string, actionIndex uint32) (orchardAction, error) {
	blk, ok := cache[height]
	if !ok {
		hash, err := rpc.GetBlockHash(ctx, height)
		if err != nil {
			return orchardAction{}, err
		}
		if err := rpc.Call(ctx, "getblock", []any{hash, 2}, &blk); err != nil {
			return orchardAction{}, err
		}
		cache[height] = blk
	}

	txid = strings.ToLower(strings.TrimSpace(txid))
	for _, t := range blk.Tx {
		if strings.ToLower(strings.TrimSpace(t.TxID)) != txid {
			continue
		}
		if int(actionIndex) < 0 || int(actionIndex) >= len(t.Orchard.Actions) {
			return orchardAction{}, errors.New("txbuild: action_index out of range")
		}
		a := t.Orchard.Actions[actionIndex]
		act := orchardAction{
			Nullifier:     strings.ToLower(strings.TrimSpace(a.Nullifier)),
			CMX:           strings.ToLower(strings.TrimSpace(a.CMX)),
			EphemeralKey:  strings.ToLower(strings.TrimSpace(a.EphemeralKey)),
			EncCiphertext: strings.ToLower(strings.TrimSpace(a.EncCiphertext)),
		}
		if len(act.EncCiphertext) >= 104 {
			act.EncCiphertext = act.EncCiphertext[:104]
		}
		if !is32ByteHex(act.Nullifier) || !is32ByteHex(act.CMX) || !is32ByteHex(act.EphemeralKey) {
			return orchardAction{}, errors.New("txbuild: invalid orchard action encoding")
		}
		if len(act.EncCiphertext) != 104 {
			return orchardAction{}, errors.New("txbuild: invalid orchard action encoding")
		}
		if _, err := hex.DecodeString(act.EncCiphertext); err != nil {
			return orchardAction{}, errors.New("txbuild: invalid orchard action encoding")
		}
		return act, nil
	}
	return orchardAction{}, errors.New("txbuild: tx not found in block")
}

func is32ByteHex(s string) bool {
	if len(s) != 64 {
		return false
	}
	_, err := hex.DecodeString(s)
	return err == nil
}

func listUnspentOrchardNotes(ctx context.Context, rpc *junocashd.Client, tipHeight, minConf int64, account uint32) ([]logic.UnspentNote, error) {
	var raw []struct {
		TxID          string      `json:"txid"`
		Pool          string      `json:"pool"`
		OutIndex      uint32      `json:"outindex"`
		Confirmations int64       `json:"confirmations"`
		Spendable     bool        `json:"spendable"`
		Account       *uint32     `json:"account,omitempty"`
		Amount        json.Number `json:"amount"`
	}
	if err := rpc.Call(ctx, "z_listunspent", []any{minConf, 9999999, true}, &raw); err != nil {
		return nil, err
	}

	out := make([]logic.UnspentNote, 0, len(raw))
	for _, n := range raw {
		if strings.ToLower(strings.TrimSpace(n.Pool)) != "orchard" {
			continue
		}
		if n.Account != nil && *n.Account != account {
			continue
		}
		txid := strings.ToLower(strings.TrimSpace(n.TxID))
		if txid == "" {
			continue
		}
		v, err := parseZECToZat(n.Amount.String())
		if err != nil {
			return nil, err
		}
		var height int64
		if n.Confirmations > 0 && tipHeight >= n.Confirmations-1 {
			height = tipHeight - n.Confirmations + 1
		}
		out = append(out, logic.UnspentNote{
			TxID:        txid,
			ActionIndex: n.OutIndex,
			ValueZat:    v,
			Height:      height,
		})
	}

	return out, nil
}

func parseUint64Decimal(s string) (uint64, error) {
	return logic.ParseUint64Decimal(s)
}

func parseZECToZat(s string) (uint64, error) {
	return logic.ParseZECToZat(s)
}

func addUint64(a, b uint64) (uint64, bool) {
	sum := a + b
	if sum < a {
		return 0, false
	}
	return sum, true
}
