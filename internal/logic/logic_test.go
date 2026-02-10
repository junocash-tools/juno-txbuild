package logic

import "testing"

func TestParseZECToZat(t *testing.T) {
	got, err := ParseZECToZat("0.24985000")
	if err != nil {
		t.Fatalf("ParseZECToZat: %v", err)
	}
	if got != 24_985_000 {
		t.Fatalf("got %d want %d", got, 24_985_000)
	}
}

func TestSelectNotes_AccountsForFeeStepFunction(t *testing.T) {
	notes := []UnspentNote{
		{TxID: "b", ActionIndex: 0, ValueZat: 60_000},
		{TxID: "a", ActionIndex: 0, ValueZat: 60_000},
	}

	// For 1 spend, fee is still 10000. Need 70000, so requires both notes.
	selected, fee, err := SelectNotes(notes, 60_000, 1)
	if err != nil {
		t.Fatalf("SelectNotes: %v", err)
	}
	if fee != 10_000 {
		t.Fatalf("fee=%d want %d", fee, 10_000)
	}
	if len(selected) != 2 {
		t.Fatalf("selected=%d want %d", len(selected), 2)
	}
}

func TestSelectNotes_MultiOutput_AllowsChangeZeroWhenOutputsDriveFee(t *testing.T) {
	notes := []UnspentNote{
		{TxID: "a", ActionIndex: 0, ValueZat: 75_000},
		{TxID: "b", ActionIndex: 0, ValueZat: 1_000},
	}

	// Two outputs means 3 actions if we have change (2 outputs + change).
	// If the first note exactly matches amount+feeWithChange, we may select
	// it and overpay (no change output).
	selected, fee, err := SelectNotes(notes, 60_000, 2)
	if err != nil {
		t.Fatalf("SelectNotes: %v", err)
	}
	if fee != 15_000 {
		t.Fatalf("fee=%d want %d", fee, 15_000)
	}
	if len(selected) != 1 {
		t.Fatalf("selected=%d want %d", len(selected), 1)
	}
}

func TestSelectNotes_MultiOutput_UsesExactMatchWithoutChangeWhenFeeDrops(t *testing.T) {
	notes := []UnspentNote{
		{TxID: "a", ActionIndex: 0, ValueZat: 70_000},
		{TxID: "b", ActionIndex: 0, ValueZat: 1_000},
	}

	// With two outputs and no change output, fee is 10000 (2 actions).
	// If we can match amount+fee exactly, we don't need to assume a change output.
	selected, fee, err := SelectNotes(notes, 60_000, 2)
	if err != nil {
		t.Fatalf("SelectNotes: %v", err)
	}
	if fee != 10_000 {
		t.Fatalf("fee=%d want %d", fee, 10_000)
	}
	if len(selected) != 1 {
		t.Fatalf("selected=%d want %d", len(selected), 1)
	}
}

func TestSelectNotes_MultiOutput_AllowsChangeZeroWhenSpendCountDrivesFee(t *testing.T) {
	notes := []UnspentNote{
		{TxID: "a", ActionIndex: 0, ValueZat: 25_000},
		{TxID: "b", ActionIndex: 0, ValueZat: 25_000},
		{TxID: "c", ActionIndex: 0, ValueZat: 25_000},
	}

	selected, fee, err := SelectNotes(notes, 60_000, 2)
	if err != nil {
		t.Fatalf("SelectNotes: %v", err)
	}
	if fee != 15_000 {
		t.Fatalf("fee=%d want %d", fee, 15_000)
	}
	if len(selected) != 3 {
		t.Fatalf("selected=%d want %d", len(selected), 3)
	}
}

func TestSelectNotesWithFeePolicy_FeeMultiplier_SelectsMoreNotes(t *testing.T) {
	notes := []UnspentNote{
		{TxID: "a", ActionIndex: 0, ValueZat: 75_000},
		{TxID: "b", ActionIndex: 0, ValueZat: 10_000},
	}

	selected, fee, err := SelectNotesWithFeePolicy(notes, 60_000, 1, FeePolicy{Multiplier: 2})
	if err != nil {
		t.Fatalf("SelectNotesWithFeePolicy: %v", err)
	}
	if fee != 20_000 {
		t.Fatalf("fee=%d want %d", fee, 20_000)
	}
	if len(selected) != 2 {
		t.Fatalf("selected=%d want %d", len(selected), 2)
	}
}

func TestSuppressDustChange_AddsToFee(t *testing.T) {
	newFee, suppressed, err := SuppressDustChange(100_001, 90_000, 10_000, 5_000)
	if err != nil {
		t.Fatalf("SuppressDustChange: %v", err)
	}
	if !suppressed {
		t.Fatalf("suppressed=false want true")
	}
	if newFee != 10_001 {
		t.Fatalf("newFee=%d want %d", newFee, 10_001)
	}
}

func TestSuppressDustChange_NoOpWhenAboveThreshold(t *testing.T) {
	newFee, suppressed, err := SuppressDustChange(105_000, 90_000, 10_000, 5_000)
	if err != nil {
		t.Fatalf("SuppressDustChange: %v", err)
	}
	if suppressed {
		t.Fatalf("suppressed=true want false")
	}
	if newFee != 10_000 {
		t.Fatalf("newFee=%d want %d", newFee, 10_000)
	}
}

func TestFilterNotesMinValue(t *testing.T) {
	notes := []UnspentNote{
		{TxID: "a", ActionIndex: 0, ValueZat: 1},
		{TxID: "b", ActionIndex: 0, ValueZat: 10},
		{TxID: "c", ActionIndex: 0, ValueZat: 20},
	}

	got := FilterNotesMinValue(notes, 10)
	if len(got) != 2 {
		t.Fatalf("len=%d want %d", len(got), 2)
	}
	if got[0].TxID != "b" || got[0].ValueZat != 10 {
		t.Fatalf("got[0]=%+v", got[0])
	}
	if got[1].TxID != "c" || got[1].ValueZat != 20 {
		t.Fatalf("got[1]=%+v", got[1])
	}

	got = FilterNotesMinValue(notes, 0)
	if len(got) != 3 {
		t.Fatalf("len=%d want %d", len(got), 3)
	}

	got = FilterNotesMinValue(notes, 21)
	if len(got) != 0 {
		t.Fatalf("len=%d want %d", len(got), 0)
	}
}

func TestExpiryHeightFromTip(t *testing.T) {
	got, err := ExpiryHeightFromTip(100, 40)
	if err != nil {
		t.Fatalf("ExpiryHeightFromTip: %v", err)
	}
	// tip=100 means the next block height is 101.
	if got != 141 {
		t.Fatalf("got %d want %d", got, 141)
	}
}

func TestExpiryHeightFromTip_Overflow(t *testing.T) {
	if _, err := ExpiryHeightFromTip(^uint32(0), 40); err == nil {
		t.Fatalf("expected error")
	}
	if _, err := ExpiryHeightFromTip(^uint32(0)-1, 2); err == nil {
		t.Fatalf("expected error")
	}
}
