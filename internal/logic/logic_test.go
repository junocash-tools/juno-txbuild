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
	selected, fee, err := SelectNotes(notes, 60_000)
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
