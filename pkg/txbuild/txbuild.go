package txbuild

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"

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

	WalletID string
	CoinType uint32
	Account  uint32

	ToAddress string
	AmountZat string
	MemoHex   string

	ChangeAddress string

	MinConfirmations int64
	ExpiryOffset     uint32

	FeeMultiplier uint64
	FeeAddZat     uint64
	MinChangeZat  uint64
}

func PlanSend(ctx context.Context, cfg SendConfig) (types.TxPlan, error) {
	return Plan(ctx, PlanConfig{
		RPCURL:  cfg.RPCURL,
		RPCUser: cfg.RPCUser,
		RPCPass: cfg.RPCPass,

		ScanURL: cfg.ScanURL,

		WalletID: cfg.WalletID,
		CoinType: cfg.CoinType,
		Account:  cfg.Account,

		Kind: types.TxPlanKindWithdrawal,
		Outputs: []types.TxOutput{
			{ToAddress: cfg.ToAddress, AmountZat: cfg.AmountZat, MemoHex: cfg.MemoHex},
		},
		ChangeAddress: cfg.ChangeAddress,

		MinConfirmations: cfg.MinConfirmations,
		ExpiryOffset:     cfg.ExpiryOffset,

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

	WalletID string
	CoinType uint32
	Account  uint32

	Kind          types.TxPlanKind
	Outputs       []types.TxOutput
	ChangeAddress string

	MinConfirmations int64
	ExpiryOffset     uint32

	FeeMultiplier uint64
	FeeAddZat     uint64
	MinChangeZat  uint64
}

func Plan(ctx context.Context, cfg PlanConfig) (types.TxPlan, error) {
	cfg.RPCURL = strings.TrimSpace(cfg.RPCURL)
	cfg.RPCUser = strings.TrimSpace(cfg.RPCUser)
	cfg.RPCPass = strings.TrimSpace(cfg.RPCPass)
	cfg.ScanURL = strings.TrimSpace(cfg.ScanURL)
	cfg.WalletID = strings.TrimSpace(cfg.WalletID)
	cfg.ChangeAddress = strings.TrimSpace(cfg.ChangeAddress)

	if cfg.RPCURL == "" {
		return types.TxPlan{}, types.CodedError{Code: types.ErrCodeInvalidRequest, Message: "rpc url required"}
	}
	if cfg.WalletID == "" {
		return types.TxPlan{}, types.CodedError{Code: types.ErrCodeInvalidRequest, Message: "wallet_id required"}
	}
	switch cfg.Kind {
	case types.TxPlanKindWithdrawal, types.TxPlanKindSweep, types.TxPlanKindRebalance:
	default:
		return types.TxPlan{}, types.CodedError{Code: types.ErrCodeInvalidRequest, Message: "unsupported kind"}
	}
	if len(cfg.Outputs) == 0 {
		return types.TxPlan{}, types.CodedError{Code: types.ErrCodeInvalidRequest, Message: "outputs required"}
	}
	if cfg.ChangeAddress == "" {
		return types.TxPlan{}, types.CodedError{Code: types.ErrCodeInvalidRequest, Message: "change_address required"}
	}
	if cfg.MinConfirmations <= 0 {
		cfg.MinConfirmations = 1
	}
	if cfg.ExpiryOffset == 0 {
		cfg.ExpiryOffset = 40
	}
	if cfg.FeeMultiplier == 0 {
		cfg.FeeMultiplier = 1
	}

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

	coinType := cfg.CoinType
	if coinType == 0 {
		switch strings.ToLower(strings.TrimSpace(chainInfo.Chain)) {
		case "main":
			coinType = 8133
		case "test":
			coinType = 8134
		case "regtest":
			coinType = 8135
		default:
			return types.TxPlan{}, types.CodedError{Code: types.ErrCodeInvalidRequest, Message: "unknown chain"}
		}
	}
	if chainInfo.Height < 0 {
		return types.TxPlan{}, errors.New("txbuild: invalid chain height")
	}
	if chainInfo.Height > int64(^uint32(0)) {
		return types.TxPlan{}, errors.New("txbuild: chain height too large")
	}
	anchorHeight := uint32(chainInfo.Height)

	if cfg.ScanURL != "" {
		return planWithScan(ctx, rpc, chainInfo, coinType, cfg, totalOut)
	}

	orchard, err := chain.BuildOrchardIndex(ctx, rpc, int64(anchorHeight))
	if err != nil {
		return types.TxPlan{}, err
	}
	if len(orchard.CMXHex) == 0 {
		return types.TxPlan{}, errors.New("txbuild: no orchard commitments")
	}

	notes, err := listUnspentOrchardNotes(ctx, rpc, cfg.MinConfirmations, cfg.Account)
	if err != nil {
		return types.TxPlan{}, err
	}
	if len(notes) == 0 {
		return types.TxPlan{}, types.CodedError{Code: types.ErrCodeInsufficientBalance, Message: "no spendable notes"}
	}

	feePolicy := logic.FeePolicy{
		Multiplier: cfg.FeeMultiplier,
		AddZat:     cfg.FeeAddZat,
	}

	selected, feeZat, err := logic.SelectNotesWithFeePolicy(notes, totalOut, len(cfg.Outputs), feePolicy)
	if err != nil {
		return types.TxPlan{}, types.CodedError{Code: types.ErrCodeInsufficientBalance, Message: "insufficient funds"}
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

	expiryHeight := anchorHeight + cfg.ExpiryOffset
	if expiryHeight < anchorHeight {
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
	return plan, nil
}

type SweepConfig struct {
	RPCURL  string
	RPCUser string
	RPCPass string

	ScanURL string

	WalletID string
	CoinType uint32
	Account  uint32

	ToAddress     string
	MemoHex       string
	ChangeAddress string

	MinConfirmations int64
	ExpiryOffset     uint32

	FeeMultiplier uint64
	FeeAddZat     uint64
}

func PlanSweep(ctx context.Context, cfg SweepConfig) (types.TxPlan, error) {
	cfg.RPCURL = strings.TrimSpace(cfg.RPCURL)
	cfg.RPCUser = strings.TrimSpace(cfg.RPCUser)
	cfg.RPCPass = strings.TrimSpace(cfg.RPCPass)
	cfg.ScanURL = strings.TrimSpace(cfg.ScanURL)
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
	if cfg.ToAddress == "" {
		return types.TxPlan{}, types.CodedError{Code: types.ErrCodeInvalidRequest, Message: "to required"}
	}
	if cfg.ChangeAddress == "" {
		cfg.ChangeAddress = cfg.ToAddress
	}
	if cfg.MinConfirmations <= 0 {
		cfg.MinConfirmations = 1
	}
	if cfg.ExpiryOffset == 0 {
		cfg.ExpiryOffset = 40
	}
	if cfg.FeeMultiplier == 0 {
		cfg.FeeMultiplier = 1
	}

	rpc := junocashd.New(cfg.RPCURL, cfg.RPCUser, cfg.RPCPass)

	chainInfo, err := chain.GetChainInfo(ctx, rpc)
	if err != nil {
		return types.TxPlan{}, err
	}

	coinType := cfg.CoinType
	if coinType == 0 {
		switch strings.ToLower(strings.TrimSpace(chainInfo.Chain)) {
		case "main":
			coinType = 8133
		case "test":
			coinType = 8134
		case "regtest":
			coinType = 8135
		default:
			return types.TxPlan{}, types.CodedError{Code: types.ErrCodeInvalidRequest, Message: "unknown chain"}
		}
	}
	if chainInfo.Height < 0 {
		return types.TxPlan{}, errors.New("txbuild: invalid chain height")
	}
	if chainInfo.Height > int64(^uint32(0)) {
		return types.TxPlan{}, errors.New("txbuild: chain height too large")
	}
	anchorHeight := uint32(chainInfo.Height)

	if cfg.ScanURL != "" {
		return planSweepWithScan(ctx, rpc, chainInfo, coinType, cfg)
	}

	orchard, err := chain.BuildOrchardIndex(ctx, rpc, int64(anchorHeight))
	if err != nil {
		return types.TxPlan{}, err
	}
	if len(orchard.CMXHex) == 0 {
		return types.TxPlan{}, errors.New("txbuild: no orchard commitments")
	}

	notes, err := listUnspentOrchardNotes(ctx, rpc, cfg.MinConfirmations, cfg.Account)
	if err != nil {
		return types.TxPlan{}, err
	}
	if len(notes) == 0 {
		return types.TxPlan{}, types.CodedError{Code: types.ErrCodeInsufficientBalance, Message: "no spendable notes"}
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

	expiryHeight := anchorHeight + cfg.ExpiryOffset
	if expiryHeight < anchorHeight {
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
	return plan, nil
}

type ConsolidateConfig struct {
	RPCURL  string
	RPCUser string
	RPCPass string

	ScanURL string

	WalletID string
	CoinType uint32
	Account  uint32

	ToAddress     string
	MemoHex       string
	ChangeAddress string

	MaxSpends int

	MinConfirmations int64
	ExpiryOffset     uint32

	FeeMultiplier uint64
	FeeAddZat     uint64
}

func PlanConsolidate(ctx context.Context, cfg ConsolidateConfig) (types.TxPlan, error) {
	cfg.RPCURL = strings.TrimSpace(cfg.RPCURL)
	cfg.RPCUser = strings.TrimSpace(cfg.RPCUser)
	cfg.RPCPass = strings.TrimSpace(cfg.RPCPass)
	cfg.ScanURL = strings.TrimSpace(cfg.ScanURL)
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
	if cfg.ToAddress == "" {
		return types.TxPlan{}, types.CodedError{Code: types.ErrCodeInvalidRequest, Message: "to required"}
	}
	if cfg.ChangeAddress == "" {
		cfg.ChangeAddress = cfg.ToAddress
	}
	if cfg.MaxSpends <= 0 {
		cfg.MaxSpends = 50
	}
	if cfg.MinConfirmations <= 0 {
		cfg.MinConfirmations = 1
	}
	if cfg.ExpiryOffset == 0 {
		cfg.ExpiryOffset = 40
	}
	if cfg.FeeMultiplier == 0 {
		cfg.FeeMultiplier = 1
	}

	rpc := junocashd.New(cfg.RPCURL, cfg.RPCUser, cfg.RPCPass)

	chainInfo, err := chain.GetChainInfo(ctx, rpc)
	if err != nil {
		return types.TxPlan{}, err
	}

	coinType := cfg.CoinType
	if coinType == 0 {
		switch strings.ToLower(strings.TrimSpace(chainInfo.Chain)) {
		case "main":
			coinType = 8133
		case "test":
			coinType = 8134
		case "regtest":
			coinType = 8135
		default:
			return types.TxPlan{}, types.CodedError{Code: types.ErrCodeInvalidRequest, Message: "unknown chain"}
		}
	}
	if chainInfo.Height < 0 {
		return types.TxPlan{}, errors.New("txbuild: invalid chain height")
	}
	if chainInfo.Height > int64(^uint32(0)) {
		return types.TxPlan{}, errors.New("txbuild: chain height too large")
	}
	anchorHeight := uint32(chainInfo.Height)

	if cfg.ScanURL != "" {
		return planConsolidateWithScan(ctx, rpc, chainInfo, coinType, cfg)
	}

	orchard, err := chain.BuildOrchardIndex(ctx, rpc, int64(anchorHeight))
	if err != nil {
		return types.TxPlan{}, err
	}
	if len(orchard.CMXHex) == 0 {
		return types.TxPlan{}, errors.New("txbuild: no orchard commitments")
	}

	notes, err := listUnspentOrchardNotes(ctx, rpc, cfg.MinConfirmations, cfg.Account)
	if err != nil {
		return types.TxPlan{}, err
	}
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

	expiryHeight := anchorHeight + cfg.ExpiryOffset
	if expiryHeight < anchorHeight {
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
	return plan, nil
}

type spendableNote struct {
	TxID        string
	ActionIndex uint32
	Height      int64
	Position    uint32
	ValueZat    uint64
}

func planWithScan(ctx context.Context, rpc *junocashd.Client, chainInfo chain.ChainInfo, coinType uint32, cfg PlanConfig, totalOut uint64) (types.TxPlan, error) {
	sc, err := junoscan.New(cfg.ScanURL)
	if err != nil {
		return types.TxPlan{}, err
	}

	notes, err := listSpendableNotesFromScan(ctx, sc, cfg.WalletID, chainInfo.Height, cfg.MinConfirmations)
	if err != nil {
		return types.TxPlan{}, err
	}
	if len(notes) == 0 {
		return types.TxPlan{}, types.CodedError{Code: types.ErrCodeInsufficientBalance, Message: "no spendable notes"}
	}

	feePolicy := logic.FeePolicy{
		Multiplier: cfg.FeeMultiplier,
		AddZat:     cfg.FeeAddZat,
	}
	selected, feeZat, err := logic.SelectNotesWithFeePolicy(notesToUnspent(notes), totalOut, len(cfg.Outputs), feePolicy)
	if err != nil {
		return types.TxPlan{}, types.CodedError{Code: types.ErrCodeInsufficientBalance, Message: "insufficient funds"}
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

	wit, err := sc.OrchardWitness(ctx, nil, positions)
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

	expiryHeight := uint32(chainInfo.Height) + cfg.ExpiryOffset
	if expiryHeight < uint32(chainInfo.Height) {
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
	return plan, nil
}

func planConsolidateWithScan(ctx context.Context, rpc *junocashd.Client, chainInfo chain.ChainInfo, coinType uint32, cfg ConsolidateConfig) (types.TxPlan, error) {
	sc, err := junoscan.New(cfg.ScanURL)
	if err != nil {
		return types.TxPlan{}, err
	}

	notes, err := listSpendableNotesFromScan(ctx, sc, cfg.WalletID, chainInfo.Height, cfg.MinConfirmations)
	if err != nil {
		return types.TxPlan{}, err
	}
	if len(notes) < 2 {
		return types.TxPlan{}, types.CodedError{Code: types.ErrCodeInvalidRequest, Message: "not enough spendable notes to consolidate"}
	}

	feePolicy := logic.FeePolicy{
		Multiplier: cfg.FeeMultiplier,
		AddZat:     cfg.FeeAddZat,
	}
	selected, feeZat, err := selectNotesForConsolidation(notesToUnspent(notes), cfg.MaxSpends, feePolicy)
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

	wit, err := sc.OrchardWitness(ctx, nil, positions)
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

	expiryHeight := uint32(chainInfo.Height) + cfg.ExpiryOffset
	if expiryHeight < uint32(chainInfo.Height) {
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
	return plan, nil
}

func planSweepWithScan(ctx context.Context, rpc *junocashd.Client, chainInfo chain.ChainInfo, coinType uint32, cfg SweepConfig) (types.TxPlan, error) {
	sc, err := junoscan.New(cfg.ScanURL)
	if err != nil {
		return types.TxPlan{}, err
	}

	notes, err := listSpendableNotesFromScan(ctx, sc, cfg.WalletID, chainInfo.Height, cfg.MinConfirmations)
	if err != nil {
		return types.TxPlan{}, err
	}
	if len(notes) == 0 {
		return types.TxPlan{}, types.CodedError{Code: types.ErrCodeInsufficientBalance, Message: "no spendable notes"}
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

	wit, err := sc.OrchardWitness(ctx, nil, positions)
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

	expiryHeight := uint32(chainInfo.Height) + cfg.ExpiryOffset
	if expiryHeight < uint32(chainInfo.Height) {
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
	return plan, nil
}

func selectNotesForConsolidation(notes []logic.UnspentNote, maxSpends int, feePolicy logic.FeePolicy) ([]logic.UnspentNote, uint64, error) {
	if maxSpends <= 0 {
		maxSpends = 50
	}
	if maxSpends > len(notes) {
		maxSpends = len(notes)
	}
	if maxSpends < 2 {
		return nil, 0, types.CodedError{Code: types.ErrCodeInvalidRequest, Message: "max_spends must be >= 2"}
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

func listSpendableNotesFromScan(ctx context.Context, sc *junoscan.Client, walletID string, tipHeight int64, minConf int64) ([]spendableNote, error) {
	raw, err := sc.ListWalletNotes(ctx, walletID, true)
	if err != nil {
		return nil, err
	}
	out := make([]spendableNote, 0, len(raw))
	for _, n := range raw {
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
		if n.ValueZat > int64(^uint64(0)>>1) {
			return nil, errors.New("txbuild: note value too large")
		}
		if *n.Position > int64(^uint32(0)) {
			return nil, errors.New("txbuild: note position too large")
		}
		out = append(out, spendableNote{
			TxID:        strings.ToLower(strings.TrimSpace(n.TxID)),
			ActionIndex: uint32(n.ActionIndex),
			Height:      n.Height,
			Position:    uint32(*n.Position),
			ValueZat:    uint64(n.ValueZat),
		})
	}
	return out, nil
}

func notesToUnspent(ns []spendableNote) []logic.UnspentNote {
	out := make([]logic.UnspentNote, 0, len(ns))
	for _, n := range ns {
		out = append(out, logic.UnspentNote{TxID: n.TxID, ActionIndex: n.ActionIndex, ValueZat: n.ValueZat})
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

func listUnspentOrchardNotes(ctx context.Context, rpc *junocashd.Client, minConf int64, account uint32) ([]logic.UnspentNote, error) {
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
		out = append(out, logic.UnspentNote{
			TxID:        txid,
			ActionIndex: n.OutIndex,
			ValueZat:    v,
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
