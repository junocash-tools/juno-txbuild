package txbuild

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/Abdullah1738/juno-sdk-go/junocashd"
	"github.com/Abdullah1738/juno-sdk-go/junoscan"
	"github.com/Abdullah1738/juno-sdk-go/types"
	"github.com/Abdullah1738/juno-txbuild/internal/chain"
	"github.com/Abdullah1738/juno-txbuild/internal/logic"
)

func TestValidateExtraSpendsRange(t *testing.T) {
	for _, n := range []int{0, 1, MaxExtraSpends} {
		if err := validateExtraSpends(n); err != nil {
			t.Fatalf("validateExtraSpends(%d): %v", n, err)
		}
	}
	for _, n := range []int{-1, MaxExtraSpends + 1} {
		assertCodedError(t, validateExtraSpends(n), types.ErrCodeInvalidRequest, "between 0 and 199")
	}

	_, err := Plan(context.Background(), PlanConfig{
		RPCURL:        "http://unused.invalid",
		WalletID:      "hot",
		Kind:          types.TxPlanKindWithdrawal,
		Outputs:       []types.TxOutput{{ToAddress: "j1example", AmountZat: "1"}},
		ChangeAddress: "j1change",
		ExtraSpends:   MaxExtraSpends + 1,
	})
	assertCodedError(t, err, types.ErrCodeInvalidRequest, "between 0 and 199")
}

func TestAddExtraSpendsForPlanDisabledKeepsBaseSelection(t *testing.T) {
	notes := makeUnspentNotes(5, 1_000_000)
	notes[0].ValueZat = 50_000_000
	policy := logic.FeePolicy{Multiplier: DefaultFeeMultiplier}
	selected, fee, err := selectNotesForPlan(notes, 10_000_000, 1, policy)
	if err != nil {
		t.Fatal(err)
	}

	got, gotFee, report, err := addExtraSpendsForPlan(notes, selected, 10_000_000, 1, fee, policy, 0, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, selected) || gotFee != fee || report != (PlanReport{}) {
		t.Fatalf("disabled top-up changed selection: %+v fee=%d report=%+v", got, gotFee, report)
	}

	// No room for a change output: top-up is skipped rather than failing.
	got, gotFee, report, err = addExtraSpendsForPlan(notes, selected, 10_000_000, MaxOrchardOutputs, fee, policy, 4, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, selected) || gotFee != fee || report != (PlanReport{}) {
		t.Fatalf("top-up at output limit changed selection: %+v", got)
	}
}

func TestAddExtraSpendsForPlanCapsAtSignerAndFeeLimits(t *testing.T) {
	notes := makeUnspentNotes(MaxOrchardSpendNotes+50, 1_000_000)
	notes[0].ValueZat = 1_000_000_000

	// Signer input limit with a tiny fee.
	cheap := logic.FeePolicy{Multiplier: 1}
	selected, fee, err := selectNotesForPlan(notes, 10_000_000, 1, cheap)
	if err != nil {
		t.Fatal(err)
	}
	got, _, report, err := addExtraSpendsForPlan(notes, selected, 10_000_000, 1, fee, cheap, MaxExtraSpends, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != MaxOrchardSpendNotes || report.ExtraSpends != MaxOrchardSpendNotes-len(selected) {
		t.Fatalf("selected=%d extra=%d", len(got), report.ExtraSpends)
	}

	// junocashd absurd-fee cap with planner default fees.
	def := logic.FeePolicy{Multiplier: DefaultFeeMultiplier}
	selected, fee, err = selectNotesForPlan(notes, 10_000_000, 1, def)
	if err != nil {
		t.Fatal(err)
	}
	got, gotFee, report, err := addExtraSpendsForPlan(notes, selected, 10_000_000, 1, fee, def, MaxExtraSpends, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if gotFee > MaxTransactionFeeZat || len(got) != 100 || gotFee != MaxTransactionFeeZat {
		t.Fatalf("selected=%d fee=%d extra=%d", len(got), gotFee, report.ExtraSpends)
	}
}

// TestPlanWithScanExtraSpends runs the scan-backed planner end to end against
// stub node and scanner servers and checks that the top-up only changes the
// spend side of the plan.
func TestPlanWithScanExtraSpends(t *testing.T) {
	const (
		anchorHeight = int64(200)
		anchorHash   = "anchor-hash"
	)
	type stubNote struct {
		txid       string
		action     int
		height     int64
		pos        int64
		value      int64
		pending    bool
		noPosition bool
	}
	stubNotes := []stubNote{
		{txid: strings.Repeat("a", 64), action: 0, height: 100, pos: 1, value: 50_000_000},
		{txid: strings.Repeat("b", 64), action: 0, height: 110, pos: 2, value: 900_000},
		{txid: strings.Repeat("c", 64), action: 1, height: 120, pos: 3, value: 300_000},
		{txid: strings.Repeat("d", 64), action: 0, height: 105, pos: 4, value: 300_000},
		{txid: strings.Repeat("e", 64), action: 0, height: 130, pos: 5, value: 50_000},
		{txid: strings.Repeat("f", 64), action: 0, height: 140, pos: 6, value: 20_000_000},
		// Notes that would win the top-up if they were eligible. None of them
		// may ever be selected.
		{txid: strings.Repeat("1", 64), action: 0, height: 150, pos: 7, value: 200_000, pending: true},
		{txid: strings.Repeat("2", 64), action: 0, height: anchorHeight + 1, pos: 8, value: 200_000},
		{txid: strings.Repeat("3", 64), action: 0, height: 150, pos: 9, value: 200_000, noPosition: true},
		{txid: strings.Repeat("4", 64), action: 0, height: 150, pos: 10, value: 30_000},
		{txid: strings.Repeat("5", 64), action: 0, height: 150, pos: 11, value: 200_000},
	}
	excluded, err := validateExcludedNoteIDs([]string{strings.Repeat("5", 64) + ":0"})
	if err != nil {
		t.Fatalf("validateExcludedNoteIDs: %v", err)
	}

	node := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			ID     any               `json:"id"`
			Method string            `json:"method"`
			Params []json.RawMessage `json:"params"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			http.Error(w, "invalid json", http.StatusBadRequest)
			return
		}
		var result any
		switch request.Method {
		case "getblockhash":
			var height int64
			_ = json.Unmarshal(request.Params[0], &height)
			if height == anchorHeight {
				result = anchorHash
			} else {
				result = "block-" + jsonInt(height)
			}
		case "getblock":
			var hash string
			_ = json.Unmarshal(request.Params[0], &hash)
			var txs []map[string]any
			for _, n := range stubNotes {
				if "block-"+jsonInt(n.height) == hash {
					txs = append(txs, testOrchardBlock(n.txid)["tx"].([]map[string]any)...)
				}
			}
			result = map[string]any{"tx": txs}
		case "getblockchaininfo":
			result = map[string]any{
				"chain":     "regtest",
				"blocks":    anchorHeight,
				"consensus": map[string]any{"nextblock": "c8e71055"},
			}
		default:
			http.Error(w, "unexpected method", http.StatusBadRequest)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"result": result, "error": nil, "id": request.ID})
	}))
	defer node.Close()

	var mu sync.Mutex
	var witnessPositions [][]uint32
	scanMux := http.NewServeMux()
	scanMux.HandleFunc("/v1/health", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status":         "ok",
			"event_epoch":    strings.Repeat("e", 64),
			"scanned_height": anchorHeight,
			"scanned_hash":   anchorHash,
		})
	})
	scanMux.HandleFunc("/v1/wallets/hot/notes", func(w http.ResponseWriter, r *http.Request) {
		notes := make([]map[string]any, 0, len(stubNotes))
		for _, n := range stubNotes {
			note := testScanNote(n.txid, n.action, n.height, n.pos)
			note["value_zat"] = n.value
			if n.pending {
				note["pending_spent_txid"] = strings.Repeat("9", 64)
			}
			if n.noPosition {
				note["position"] = nil
			}
			notes = append(notes, note)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"notes": notes})
	})
	scanMux.HandleFunc("/v1/orchard/witness", func(w http.ResponseWriter, r *http.Request) {
		var request junoscan.WitnessRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			http.Error(w, "invalid json", http.StatusBadRequest)
			return
		}
		mu.Lock()
		witnessPositions = append(witnessPositions, append([]uint32(nil), request.Positions...))
		mu.Unlock()
		paths := make([]map[string]any, 0, len(request.Positions))
		for _, position := range request.Positions {
			paths = append(paths, map[string]any{"position": position, "auth_path": testAuthPath()})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status":        "ok",
			"anchor_height": anchorHeight,
			"root":          "root",
			"paths":         paths,
		})
	})
	scanner := httptest.NewServer(scanMux)
	defer scanner.Close()

	rpc := junocashd.New(node.URL, "", "")
	info := chain.ChainInfo{Chain: "regtest", Height: anchorHeight, BranchID: 0xc8e71055}
	outputs := []types.TxOutput{
		{ToAddress: "dest-1", AmountZat: "6000000", MemoHex: "abcd"},
		{ToAddress: "dest-2", AmountZat: "4000000"},
	}
	cfg := PlanConfig{
		ScanURL:          scanner.URL,
		WalletID:         "hot",
		Kind:             types.TxPlanKindWithdrawal,
		Outputs:          outputs,
		ChangeAddress:    "change",
		MinConfirmations: 1,
		ExpiryOffset:     40,
		MinNoteZat:       40_000,
		FeeMultiplier:    DefaultFeeMultiplier,
	}

	run := func(cfg PlanConfig) (types.TxPlan, PlanReport, []uint32) {
		t.Helper()
		mu.Lock()
		witnessPositions = nil
		mu.Unlock()
		var report PlanReport
		plan, err := planWithScan(context.Background(), rpc, info, regtestCoinType, cfg, 10_000_000, excluded, &report)
		if err != nil {
			t.Fatalf("planWithScan: %v", err)
		}
		mu.Lock()
		defer mu.Unlock()
		if len(witnessPositions) != 1 {
			t.Fatalf("witness calls=%d", len(witnessPositions))
		}
		return plan, report, witnessPositions[0]
	}

	basePlan, baseReport, basePositions := run(cfg)
	if baseReport != (PlanReport{}) || len(basePlan.Notes) != 1 || basePlan.FeeZat != "300000" {
		t.Fatalf("base plan notes=%d fee=%s report=%+v", len(basePlan.Notes), basePlan.FeeZat, baseReport)
	}
	if !reflect.DeepEqual(basePositions, []uint32{6}) {
		t.Fatalf("base witness positions=%v", basePositions)
	}

	cfg.ExtraSpends = 4
	cfg.ExtraSpendMaxZat = 1_000_000
	plan, report, positions := run(cfg)

	if !reflect.DeepEqual(plan.Outputs, outputs) || plan.ChangeAddress != basePlan.ChangeAddress {
		t.Fatalf("outputs changed: %+v", plan.Outputs)
	}
	var gotIDs []string
	for _, n := range plan.Notes {
		gotIDs = append(gotIDs, n.NoteID)
	}
	// Base picks f (best fit); then smallest first with height tie-break:
	// e, d (h105) before c (h120), then b. e and d fit inside the existing
	// 3-action fee, c and b each pay one more action. a is over the value cap.
	// The pending, unconfirmed, position-less, below-min and excluded notes
	// never show up.
	wantIDs := []string{
		strings.Repeat("f", 64) + ":0",
		strings.Repeat("e", 64) + ":0",
		strings.Repeat("d", 64) + ":0",
		strings.Repeat("c", 64) + ":1",
		strings.Repeat("b", 64) + ":0",
	}
	if !reflect.DeepEqual(gotIDs, wantIDs) {
		t.Fatalf("notes=%v want %v", gotIDs, wantIDs)
	}
	if !reflect.DeepEqual(positions, []uint32{6, 5, 4, 3, 2}) {
		t.Fatalf("witness positions=%v", positions)
	}
	// 5 spends, 2 outputs + change: 100,000 * 5.
	if plan.FeeZat != "500000" {
		t.Fatalf("fee=%s want 500000", plan.FeeZat)
	}
	if report.ExtraSpends != 4 || report.ExtraSpendZat != 1_550_000 {
		t.Fatalf("report=%+v", report)
	}

	// Change split on the same base selection: f (20,000,000) pays 10,000,000.
	// 2 payments + 3 change notes = 5 actions -> fee 500,000; the change of
	// 9,500,000 becomes two outputs of 3,166,666 plus the signer change.
	cfg.ExtraSpends = 0
	cfg.ExtraSpendMaxZat = 0
	cfg.SplitChange = 3
	split, splitReport, _ := run(cfg)
	if len(split.Notes) != 1 || split.FeeZat != "500000" || splitReport.ChangeNotes != 3 {
		t.Fatalf("split notes=%d fee=%s report=%+v", len(split.Notes), split.FeeZat, splitReport)
	}
	wantOutputs := append(append([]types.TxOutput(nil), outputs...),
		types.TxOutput{ToAddress: "change", AmountZat: "3166666"},
		types.TxOutput{ToAddress: "change", AmountZat: "3166666"},
	)
	if !reflect.DeepEqual(split.Outputs, wantOutputs) || split.ChangeAddress != "change" {
		t.Fatalf("split outputs=%+v", split.Outputs)
	}
}

func jsonInt(v int64) string {
	b, _ := json.Marshal(v)
	return string(b)
}
