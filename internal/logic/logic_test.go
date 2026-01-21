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

func TestSelectNotes_MultiOutput_AvoidsChangeZeroWhenOutputsDriveFee(t *testing.T) {
	notes := []UnspentNote{
		{TxID: "a", ActionIndex: 0, ValueZat: 75_000},
		{TxID: "b", ActionIndex: 0, ValueZat: 1_000},
	}

	// Two outputs means 3 actions if we have change (2 outputs + change).
	// If the first note exactly matches amount+feeWithChange, we must select
	// another note so change>0 and the fee assumption remains valid.
	selected, fee, err := SelectNotes(notes, 60_000, 2)
	if err != nil {
		t.Fatalf("SelectNotes: %v", err)
	}
	if fee != 15_000 {
		t.Fatalf("fee=%d want %d", fee, 15_000)
	}
	if len(selected) != 2 {
		t.Fatalf("selected=%d want %d", len(selected), 2)
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
