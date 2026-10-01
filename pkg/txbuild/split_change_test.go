package txbuild

import (
	"strconv"
	"testing"

	"github.com/Abdullah1738/juno-sdk-go/types"
	"github.com/Abdullah1738/juno-txbuild/internal/logic"
)

func TestValidateSplitChange(t *testing.T) {
	for _, n := range []int{0, 1, 2, MaxSplitChange} {
		if err := validateSplitChange(n, "change"); err != nil {
			t.Fatalf("validateSplitChange(%d): %v", n, err)
		}
	}
	for _, n := range []int{-1, MaxSplitChange + 1} {
		assertCodedError(t, validateSplitChange(n, "change"), types.ErrCodeInvalidRequest, "between 0 and 199")
	}
	assertCodedError(t, validateSplitChange(2, " "), types.ErrCodeInvalidRequest, "requires change_address")
}

func TestApplySplitChangeKeepsSingleChangeWhenOff(t *testing.T) {
	outputs := []types.TxOutput{{ToAddress: "dest", AmountZat: "50000000"}}
	policy := logic.FeePolicy{Multiplier: 20}
	for _, n := range []int{0, 1} {
		got, fee, notes, err := applySplitChange(outputs, "change", 1, 119_780_000, 50_000_000, 200_000, true, policy, n, 0, 0)
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 1 || fee != 200_000 || notes != 1 {
			t.Fatalf("n=%d outputs=%d fee=%d notes=%d", n, len(got), fee, notes)
		}
	}
	got, fee, notes, err := applySplitChange(outputs, "change", 1, 50_200_000, 50_000_000, 200_000, false, policy, 4, 0, 0)
	if err != nil || len(got) != 1 || fee != 200_000 || notes != 0 {
		t.Fatalf("no change: outputs=%d fee=%d notes=%d err=%v", len(got), fee, notes, err)
	}
}

func TestApplySplitChangeSplitsEvenlyAndChargesFee(t *testing.T) {
	outputs := []types.TxOutput{{ToAddress: "dest", AmountZat: "50000000"}}
	policy := logic.FeePolicy{Multiplier: 20}
	totalIn, totalOut := uint64(119_780_000), uint64(50_000_000)
	got, fee, notes, err := applySplitChange(outputs, "change", 1, totalIn, totalOut, 200_000, true, policy, 4, 1_000_000, 0)
	if err != nil {
		t.Fatal(err)
	}
	// 1 spend, 1 payment + 4 change notes = 5 actions at 20x ZIP-317.
	if fee != 5*5_000*20 || notes != 4 || len(got) != 4 {
		t.Fatalf("fee=%d notes=%d outputs=%d", fee, notes, len(got))
	}
	if got[0] != outputs[0] {
		t.Fatalf("payment output changed: %+v", got[0])
	}
	change := totalIn - totalOut - fee
	piece := change / 4
	var split uint64
	for _, out := range got[1:] {
		if out.ToAddress != "change" || out.MemoHex != "" || out.AmountZat != strconv.FormatUint(piece, 10) {
			t.Fatalf("bad split output: %+v", out)
		}
		split += piece
	}
	if signerChange := change - split; signerChange < piece {
		t.Fatalf("signer change %d smaller than piece %d", signerChange, piece)
	}
	if len(outputs) != 1 {
		t.Fatal("input outputs slice was modified")
	}
}

func TestApplySplitChangeLowersCountForMinimumAndFee(t *testing.T) {
	outputs := []types.TxOutput{{ToAddress: "dest", AmountZat: "1000"}}
	policy := logic.FeePolicy{Multiplier: 1}
	// change after a 2-note split: 31_000 - 1000 - 15_000 = 15_000 -> 7_500 each.
	got, fee, notes, err := applySplitChange(outputs, "change", 1, 31_000, 1_000, 10_000, true, policy, 10, 7_000, 0)
	if err != nil {
		t.Fatal(err)
	}
	if notes != 2 || len(got) != 2 || fee != 15_000 || got[1].AmountZat != "7500" {
		t.Fatalf("notes=%d outputs=%+v fee=%d", notes, got, fee)
	}
	// MinChangeZat also bounds the piece size.
	got, fee, notes, err = applySplitChange(outputs, "change", 1, 31_000, 1_000, 10_000, true, policy, 10, 0, 8_000)
	if err != nil || notes != 1 || len(got) != 1 || fee != 10_000 {
		t.Fatalf("min change: notes=%d outputs=%d fee=%d err=%v", notes, len(got), fee, err)
	}
	// A split that would exceed the node fee limit is not used.
	got, fee, notes, err = applySplitChange(outputs, "change", 1, MaxTransactionFeeZat*10, 1_000, 5_000*1_000, true, logic.FeePolicy{Multiplier: 1_000}, 3, 0, 0)
	if err != nil || notes != 1 || len(got) != 1 || fee != 5_000*1_000 {
		t.Fatalf("fee cap: notes=%d outputs=%d fee=%d err=%v", notes, len(got), fee, err)
	}
}

func TestApplySplitChangeRespectsOutputLimit(t *testing.T) {
	outputs := make([]types.TxOutput, MaxOrchardOutputs-2)
	for i := range outputs {
		outputs[i] = types.TxOutput{ToAddress: "dest", AmountZat: "1"}
	}
	got, _, notes, err := applySplitChange(outputs, "change", 1, 1_000_000_000, uint64(len(outputs)), 0, true, logic.FeePolicy{Multiplier: 1}, 10, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if notes != 2 || len(got)+1 != MaxOrchardOutputs {
		t.Fatalf("notes=%d outputs=%d", notes, len(got))
	}
	if _, err := signerCompatiblePlan(types.TxPlan{Outputs: got}, true); err != nil {
		t.Fatalf("split plan is not signer compatible: %v", err)
	}
}
