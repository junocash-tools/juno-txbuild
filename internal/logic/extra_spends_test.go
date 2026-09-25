package logic

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func extraNote(id string, idx uint32, height int64, value uint64) UnspentNote {
	return UnspentNote{TxID: strings.Repeat(id, 64), ActionIndex: idx, Height: height, ValueZat: value}
}

func noteIDs(notes []UnspentNote) []string {
	out := make([]string, len(notes))
	for i, n := range notes {
		out[i] = fmt.Sprintf("%s:%d", n.TxID[:1], n.ActionIndex)
	}
	return out
}

// legacyPolicy is the planner default: 5000 * 20 = 100,000 zat per action.
var legacyPolicy = FeePolicy{Multiplier: 20}

func baseSelection(t *testing.T, notes []UnspentNote, amount uint64, outputs int) ([]UnspentNote, uint64) {
	t.Helper()
	selected, fee, err := SelectNotesWithFeePolicy(notes, amount, outputs, legacyPolicy)
	if err != nil {
		t.Fatalf("SelectNotesWithFeePolicy: %v", err)
	}
	return selected, fee
}

func TestAddExtraSpends_ZeroIsNoop(t *testing.T) {
	notes := []UnspentNote{
		extraNote("a", 0, 10, 50_000_000),
		extraNote("b", 0, 10, 500_000),
		extraNote("c", 0, 10, 600_000),
	}
	selected, fee := baseSelection(t, notes, 10_000_000, 1)

	for _, max := range []int{0, -1} {
		res, err := AddExtraSpends(notes, selected, 10_000_000, 1, fee, legacyPolicy, ExtraSpendPolicy{MaxExtra: max})
		if err != nil {
			t.Fatalf("AddExtraSpends: %v", err)
		}
		if !reflect.DeepEqual(res.Selected, selected) || res.FeeZat != fee || res.ExtraSpends != 0 || res.ExtraSpendZat != 0 {
			t.Fatalf("max=%d changed selection: %+v", max, res)
		}
	}
}

func TestAddExtraSpends_SmallestFirstAndFeeRecomputed(t *testing.T) {
	notes := []UnspentNote{
		extraNote("a", 0, 10, 50_000_000),
		extraNote("d", 0, 10, 900_000),
		extraNote("b", 0, 10, 300_000),
		extraNote("c", 0, 10, 400_000),
		extraNote("e", 0, 10, 700_000),
	}
	const amount = 10_000_000
	selected, fee := baseSelection(t, notes, amount, 1)
	if len(selected) != 1 || selected[0].TxID[:1] != "a" || fee != 200_000 {
		t.Fatalf("unexpected base selection %v fee=%d", noteIDs(selected), fee)
	}

	res, err := AddExtraSpends(notes, selected, amount, 1, fee, legacyPolicy, ExtraSpendPolicy{MaxExtra: 3})
	if err != nil {
		t.Fatalf("AddExtraSpends: %v", err)
	}
	if got, want := noteIDs(res.Selected), []string{"a:0", "b:0", "c:0", "e:0"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("selected=%v want %v", got, want)
	}
	// 4 spends, 1 output + change: 100,000 * max(2, 4, 2).
	if res.FeeZat != 400_000 {
		t.Fatalf("fee=%d want 400000", res.FeeZat)
	}
	want, err := legacyPolicy.Apply(RequiredFeeSend(len(res.Selected), 2))
	if err != nil || res.FeeZat != want {
		t.Fatalf("fee=%d not recomputed over final spend count (want %d, err %v)", res.FeeZat, want, err)
	}
	if res.ExtraSpends != 3 || res.ExtraSpendZat != 1_400_000 {
		t.Fatalf("report extra=%d zat=%d", res.ExtraSpends, res.ExtraSpendZat)
	}
	var totalIn uint64
	for _, n := range res.Selected {
		totalIn += n.ValueZat
	}
	change := totalIn - amount - res.FeeZat
	if change != 50_000_000+1_400_000-amount-400_000 {
		t.Fatalf("change=%d", change)
	}
}

func TestAddExtraSpends_SkipsNotesNotWorthTheFee(t *testing.T) {
	// Base already spends two notes, so every extra spend adds one action
	// (100,000 zat) to the fee.
	notes := []UnspentNote{
		extraNote("a", 0, 10, 6_000_000),
		extraNote("f", 0, 10, 5_000_000),
		extraNote("b", 0, 10, 50_000),  // below marginal fee
		extraNote("c", 0, 10, 100_000), // equal to marginal fee, still not worth it
		extraNote("d", 0, 10, 100_001),
		extraNote("e", 0, 10, 150_000),
	}
	selected := []UnspentNote{notes[0], notes[1]}
	const fee = 200_000

	res, err := AddExtraSpends(notes, selected, 10_000_000, 1, fee, legacyPolicy, ExtraSpendPolicy{MaxExtra: 4})
	if err != nil {
		t.Fatalf("AddExtraSpends: %v", err)
	}
	if got, want := noteIDs(res.Selected), []string{"a:0", "f:0", "d:0", "e:0"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("selected=%v want %v", got, want)
	}
	if res.FeeZat != 400_000 {
		t.Fatalf("fee=%d want 400000", res.FeeZat)
	}
}

func TestAddExtraSpends_FreeWhileUnderGraceActions(t *testing.T) {
	// One spend with one output + change costs max(2, 1, 2) = 2 actions, so a
	// second spend adds no fee and any positive-value note is worth taking.
	notes := []UnspentNote{
		extraNote("a", 0, 10, 50_000_000),
		extraNote("b", 0, 10, 1),
	}
	selected, fee := baseSelection(t, notes, 10_000_000, 1)
	res, err := AddExtraSpends(notes, selected, 10_000_000, 1, fee, legacyPolicy, ExtraSpendPolicy{MaxExtra: 1})
	if err != nil {
		t.Fatalf("AddExtraSpends: %v", err)
	}
	if res.ExtraSpends != 1 || res.FeeZat != fee {
		t.Fatalf("extra=%d fee=%d base fee=%d", res.ExtraSpends, res.FeeZat, fee)
	}
}

func TestAddExtraSpends_ExactMatchBaseGainsChangeFee(t *testing.T) {
	// Base selection is an exact match without change (fee 2 actions). Adding
	// a note creates change, so the fee is recomputed with the change action.
	notes := []UnspentNote{
		extraNote("a", 0, 10, 1_200_000),
		extraNote("b", 0, 10, 500_000),
	}
	selected, fee := baseSelection(t, notes, 1_000_000, 2)
	if len(selected) != 1 || fee != 200_000 {
		t.Fatalf("unexpected base %v fee=%d", noteIDs(selected), fee)
	}
	res, err := AddExtraSpends(notes, selected, 1_000_000, 2, fee, legacyPolicy, ExtraSpendPolicy{MaxExtra: 1})
	if err != nil {
		t.Fatalf("AddExtraSpends: %v", err)
	}
	// 2 spends, 2 outputs + change = 3 actions.
	if res.ExtraSpends != 1 || res.FeeZat != 300_000 {
		t.Fatalf("extra=%d fee=%d", res.ExtraSpends, res.FeeZat)
	}
}

func TestAddExtraSpends_NeverAddsNotesOutsideEligibleSet(t *testing.T) {
	// The caller filters excluded, reserved, pending-spent, unconfirmed and
	// below-minimum notes before selection. Only what remains is eligible, so
	// a filtered note can never come back as an extra.
	all := []UnspentNote{
		extraNote("a", 0, 10, 50_000_000),
		extraNote("b", 0, 10, 300_000), // excluded/reserved
		extraNote("c", 0, 10, 400_000), // pending spent
		extraNote("d", 0, 10, 500_000), // unconfirmed
		extraNote("e", 0, 10, 600_000),
	}
	eligible := []UnspentNote{all[0], all[4]}
	selected, fee := baseSelection(t, eligible, 10_000_000, 1)

	res, err := AddExtraSpends(eligible, selected, 10_000_000, 1, fee, legacyPolicy, ExtraSpendPolicy{MaxExtra: 4})
	if err != nil {
		t.Fatalf("AddExtraSpends: %v", err)
	}
	if got, want := noteIDs(res.Selected), []string{"a:0", "e:0"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("selected=%v want %v", got, want)
	}
}

func TestAddExtraSpends_DoesNotDuplicateSelectedNotes(t *testing.T) {
	notes := []UnspentNote{
		extraNote("a", 0, 10, 50_000_000),
		extraNote("b", 0, 10, 300_000),
		extraNote("b", 0, 10, 300_000), // duplicate entry
		{TxID: strings.ToUpper(strings.Repeat("a", 64)), ActionIndex: 0, Height: 10, ValueZat: 50_000_000},
	}
	selected := []UnspentNote{notes[0]}
	res, err := AddExtraSpends(notes, selected, 10_000_000, 1, 200_000, legacyPolicy, ExtraSpendPolicy{MaxExtra: 4})
	if err != nil {
		t.Fatalf("AddExtraSpends: %v", err)
	}
	if got, want := noteIDs(res.Selected), []string{"a:0", "b:0"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("selected=%v want %v", got, want)
	}
}

func TestAddExtraSpends_RespectsMaxNoteValue(t *testing.T) {
	notes := []UnspentNote{
		extraNote("a", 0, 10, 50_000_000),
		extraNote("b", 0, 10, 300_000),
		extraNote("c", 0, 10, 1_000_000),
		extraNote("d", 0, 10, 1_000_001),
		extraNote("e", 0, 10, 20_000_000),
	}
	selected, fee := baseSelection(t, notes, 10_000_000, 1)
	res, err := AddExtraSpends(notes, selected, 10_000_000, 1, fee, legacyPolicy, ExtraSpendPolicy{MaxExtra: 4, MaxNoteValueZat: 1_000_000})
	if err != nil {
		t.Fatalf("AddExtraSpends: %v", err)
	}
	// Base picks "e" as best fit; "a" and "d" are above the cap.
	if got, want := noteIDs(res.Selected), []string{"e:0", "b:0", "c:0"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("selected=%v want %v", got, want)
	}
}

func TestAddExtraSpends_CapsAtMaxSpends(t *testing.T) {
	notes := []UnspentNote{extraNote("a", 0, 10, 1_000_000_000)}
	for i := 0; i < 300; i++ {
		notes = append(notes, UnspentNote{TxID: fmt.Sprintf("%064x", i+1), ActionIndex: 0, Height: 10, ValueZat: 1_000_000})
	}
	selected, fee := baseSelection(t, notes, 10_000_000, 1)
	res, err := AddExtraSpends(notes, selected, 10_000_000, 1, fee, FeePolicy{Multiplier: 1}, ExtraSpendPolicy{MaxExtra: 250, MaxSpends: 200})
	if err != nil {
		t.Fatalf("AddExtraSpends: %v", err)
	}
	if len(res.Selected) != 200 || res.ExtraSpends != 199 {
		t.Fatalf("selected=%d extra=%d want 200/199", len(res.Selected), res.ExtraSpends)
	}
}

func TestAddExtraSpends_CapsAtMaxFee(t *testing.T) {
	notes := []UnspentNote{extraNote("a", 0, 10, 1_000_000_000)}
	for i := 0; i < 150; i++ {
		notes = append(notes, UnspentNote{TxID: fmt.Sprintf("%064x", i+1), ActionIndex: 0, Height: 10, ValueZat: 1_000_000})
	}
	// Planner defaults with a large flat add: fee = 100,000*actions + 1,000,000.
	policy := FeePolicy{Multiplier: 20, AddZat: 1_000_000}
	selected, fee, err := SelectNotesWithFeePolicy(notes, 10_000_000, 1, policy)
	if err != nil {
		t.Fatalf("SelectNotesWithFeePolicy: %v", err)
	}
	res, err := AddExtraSpends(notes, selected, 10_000_000, 1, fee, policy, ExtraSpendPolicy{MaxExtra: 150, MaxSpends: 200, MaxFeeZat: 10_000_000})
	if err != nil {
		t.Fatalf("AddExtraSpends: %v", err)
	}
	// 90 spends * 100,000 + 1,000,000 = 10,000,000 exactly; 91 would exceed.
	if len(res.Selected) != 90 || res.FeeZat != 10_000_000 {
		t.Fatalf("selected=%d fee=%d want 90/10000000", len(res.Selected), res.FeeZat)
	}
}

func TestAddExtraSpends_BaseFeeAboveCapAddsNothing(t *testing.T) {
	notes := []UnspentNote{
		extraNote("a", 0, 10, 100_000_000),
		extraNote("b", 0, 10, 500_000),
		extraNote("c", 0, 10, 600_000),
	}
	// A flat add already puts the base fee over the 10M cap. The top-up is
	// limited by the cap, the base plan is not.
	policy := FeePolicy{Multiplier: 20, AddZat: 12_000_000}
	selected, fee, err := SelectNotesWithFeePolicy(notes, 10_000_000, 1, policy)
	if err != nil {
		t.Fatalf("SelectNotesWithFeePolicy: %v", err)
	}
	res, err := AddExtraSpends(notes, selected, 10_000_000, 1, fee, policy, ExtraSpendPolicy{MaxExtra: 4, MaxSpends: 200, MaxFeeZat: 10_000_000})
	if err != nil {
		t.Fatalf("AddExtraSpends: %v", err)
	}
	if res.ExtraSpends != 0 || res.FeeZat != fee || !reflect.DeepEqual(res.Selected, selected) {
		t.Fatalf("res=%+v want base selection fee=%d", res, fee)
	}
}

func TestAddExtraSpends_RejectsDustChange(t *testing.T) {
	// Base: exact 2-note match leaves no change. Adding "c" would leave change
	// below MinChangeZat, so it is skipped; "d" leaves enough change.
	notes := []UnspentNote{
		extraNote("a", 0, 10, 1_200_000),
		extraNote("c", 0, 10, 100_500),
		extraNote("d", 0, 10, 700_000),
	}
	selected := []UnspentNote{notes[0]}
	// amount 1,000,000 with 2 outputs: no-change fee 200,000 -> exact.
	res, err := AddExtraSpends(notes, selected, 1_000_000, 2, 200_000, legacyPolicy, ExtraSpendPolicy{MaxExtra: 1, MinChangeZat: 10_000})
	if err != nil {
		t.Fatalf("AddExtraSpends: %v", err)
	}
	if got, want := noteIDs(res.Selected), []string{"a:0", "d:0"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("selected=%v want %v", got, want)
	}
}

func TestAddExtraSpends_DeterministicTieBreak(t *testing.T) {
	big := extraNote("a", 0, 1, 50_000_000)
	notes := []UnspentNote{
		big,
		extraNote("f", 1, 30, 500_000),
		extraNote("e", 0, 20, 500_000),
		extraNote("c", 2, 20, 500_000),
		extraNote("c", 1, 20, 500_000),
		extraNote("b", 0, 25, 500_000),
	}
	want := []string{"a:0", "c:1", "c:2", "e:0", "b:0"}

	for i := 0; i < 5; i++ {
		shuffled := append([]UnspentNote(nil), notes...)
		// rotate the candidate order each round
		shuffled = append(shuffled[i:], shuffled[:i]...)
		res, err := AddExtraSpends(shuffled, []UnspentNote{big}, 10_000_000, 1, 200_000, legacyPolicy, ExtraSpendPolicy{MaxExtra: 4})
		if err != nil {
			t.Fatalf("AddExtraSpends: %v", err)
		}
		if got := noteIDs(res.Selected); !reflect.DeepEqual(got, want) {
			t.Fatalf("round %d selected=%v want %v", i, got, want)
		}
	}
}

func TestAddExtraSpends_RejectsInconsistentBase(t *testing.T) {
	notes := []UnspentNote{extraNote("a", 0, 10, 1_000)}
	if _, err := AddExtraSpends(notes, notes, 10_000, 1, 200_000, legacyPolicy, ExtraSpendPolicy{MaxExtra: 1}); err == nil {
		t.Fatal("expected error for base selection that does not cover amount + fee")
	}
}

func TestRequiredFeeSend_LegacyDefaultMatchesZIP317(t *testing.T) {
	// junocashd v0.9.13 uses ZIP-317 with a 100,000 zat marginal fee and two
	// grace actions. The planner default multiplier of 20 on the 5,000 zat base
	// gives the same per-action fee, and selection counts the change output as
	// an action whenever change exists.
	const marginal = 100_000
	for _, tc := range []struct{ spends, outputs int }{
		{1, 1}, {1, 2}, {2, 2}, {5, 2}, {3, 10}, {200, 201},
	} {
		got, err := FeePolicy{Multiplier: 20}.Apply(RequiredFeeSend(tc.spends, tc.outputs))
		if err != nil {
			t.Fatal(err)
		}
		actions := max(2, tc.spends, tc.outputs)
		if got != uint64(marginal*actions) {
			t.Fatalf("spends=%d outputs=%d fee=%d want %d", tc.spends, tc.outputs, got, marginal*actions)
		}
	}

	notes := []UnspentNote{extraNote("a", 0, 1, 10_000_000)}
	_, fee, err := SelectNotesWithFeePolicy(notes, 1_000_000, 2, FeePolicy{Multiplier: 20})
	if err != nil {
		t.Fatal(err)
	}
	// 2 outputs + change = 3 actions.
	if fee != 3*marginal {
		t.Fatalf("fee=%d want %d (change action counted)", fee, 3*marginal)
	}
}
