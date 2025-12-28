package txbuild

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/Abdullah1738/juno-sdk-go/junocashd"
	"github.com/Abdullah1738/juno-sdk-go/types"
	"github.com/Abdullah1738/juno-txbuild/internal/chain"
	"github.com/Abdullah1738/juno-txbuild/internal/logic"
	"github.com/Abdullah1738/juno-txbuild/internal/witness"
)

type SendConfig struct {
	RPCURL  string
	RPCUser string
	RPCPass string

	WalletID string
	CoinType uint32
	Account  uint32

	ToAddress     string
	AmountZat     string
	MemoHex       string
	ChangeAddress string

	MinConfirmations int64
	ExpiryOffset     uint32
}

func PlanSend(ctx context.Context, cfg SendConfig) (types.TxPlan, error) {
	cfg.RPCURL = strings.TrimSpace(cfg.RPCURL)
	cfg.RPCUser = strings.TrimSpace(cfg.RPCUser)
	cfg.RPCPass = strings.TrimSpace(cfg.RPCPass)
	cfg.WalletID = strings.TrimSpace(cfg.WalletID)
	cfg.ToAddress = strings.TrimSpace(cfg.ToAddress)
	cfg.AmountZat = strings.TrimSpace(cfg.AmountZat)
	cfg.MemoHex = strings.TrimSpace(cfg.MemoHex)
	cfg.ChangeAddress = strings.TrimSpace(cfg.ChangeAddress)

	if cfg.RPCURL == "" {
		return types.TxPlan{}, types.CodedError{Code: types.ErrCodeInvalidRequest, Message: "rpc url required"}
	}
	if cfg.WalletID == "" {
		return types.TxPlan{}, types.CodedError{Code: types.ErrCodeInvalidRequest, Message: "wallet_id required"}
	}
	if cfg.ToAddress == "" || cfg.ChangeAddress == "" {
		return types.TxPlan{}, types.CodedError{Code: types.ErrCodeInvalidRequest, Message: "to and change_address required"}
	}
	if cfg.AmountZat == "" {
		return types.TxPlan{}, types.CodedError{Code: types.ErrCodeInvalidRequest, Message: "amount_zat required"}
	}
	if cfg.MinConfirmations <= 0 {
		cfg.MinConfirmations = 1
	}
	if cfg.ExpiryOffset == 0 {
		cfg.ExpiryOffset = 40
	}

	amountZat, err := parseUint64Decimal(cfg.AmountZat)
	if err != nil {
		return types.TxPlan{}, types.CodedError{Code: types.ErrCodeInvalidRequest, Message: "amount_zat invalid"}
	}

	rpc := junocashd.New(cfg.RPCURL, cfg.RPCUser, cfg.RPCPass)

	chainInfo, err := chain.GetChainInfo(ctx, rpc)
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

	selected, feeZat, err := logic.SelectNotes(notes, amountZat)
	if err != nil {
		return types.TxPlan{}, types.CodedError{Code: types.ErrCodeInsufficientBalance, Message: "insufficient funds"}
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
		Version:      types.V0,
		Kind:         types.TxPlanKindWithdrawal,
		WalletID:     cfg.WalletID,
		CoinType:     cfg.CoinType,
		Account:      cfg.Account,
		Chain:        chainInfo.Chain,
		BranchID:     chainInfo.BranchID,
		AnchorHeight: anchorHeight,
		Anchor:       wit.Root,
		ExpiryHeight: expiryHeight,
		Outputs: []types.TxOutput{
			{ToAddress: cfg.ToAddress, AmountZat: cfg.AmountZat, MemoHex: cfg.MemoHex},
		},
		ChangeAddress: cfg.ChangeAddress,
		FeeZat:        strconv.FormatUint(feeZat, 10),
		Notes:         planNotes,
	}
	return plan, nil
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
		if !n.Spendable {
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
