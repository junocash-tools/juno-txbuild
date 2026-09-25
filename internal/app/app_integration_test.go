//go:build integration

package app

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"testing"
	"time"

	"github.com/Abdullah1738/juno-sdk-go/junoscan"
	"github.com/Abdullah1738/juno-sdk-go/types"
	"github.com/Abdullah1738/juno-txbuild/pkg/txbuild"
)

func TestIntegration_PlanSend(t *testing.T) {
	jd, _ := startJunocashd(t)

	changeAddr := unifiedAddress(t, jd, 0)
	mineAndShieldOnce(t, jd, changeAddr)
	toAddr := unifiedAddress(t, jd, 0)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	plan, err := txbuild.PlanSend(ctx, txbuild.SendConfig{
		RPCURL:  jd.RPCURL,
		RPCUser: jd.RPCUser,
		RPCPass: jd.RPCPassword,

		WalletID: "test-wallet",
		CoinType: 0,
		Account:  0,

		ToAddress:     toAddr,
		AmountZat:     "1000000",
		ChangeAddress: changeAddr,

		MinConfirmations: 1,
		ExpiryOffset:     40,
	})
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if err := validatePlanBasics(plan); err != nil {
		t.Fatalf("invalid plan: %v", err)
	}
	// Default fee policy: 2 conventional ZIP-317 actions x 5000 zat x multiplier 20.
	if plan.FeeZat != "200000" {
		t.Fatalf("fee_zat=%q want %q", plan.FeeZat, "200000")
	}

	shieldCoinbase(t, jd, changeAddr, 2)
	notes := waitSpendableOrchardNoteCount(t, jd, 0, 2)
	if len(notes) != 2 {
		t.Fatalf("notes=%d want %d", len(notes), 2)
	}

	var minNote, maxNote uint64
	for i, n := range notes {
		if i == 0 || n.ValueZat < minNote {
			minNote = n.ValueZat
		}
		if n.ValueZat > maxNote {
			maxNote = n.ValueZat
		}
	}
	if minNote == 0 || maxNote == 0 || minNote == maxNote {
		t.Fatalf("unexpected note values (min=%d max=%d)", minNote, maxNote)
	}
	exercisePlanSendExclusions(t, ctx, txbuild.SendConfig{
		RPCURL:           jd.RPCURL,
		RPCUser:          jd.RPCUser,
		RPCPass:          jd.RPCPassword,
		WalletID:         "test-wallet",
		ToAddress:        changeAddr,
		AmountZat:        "1000000",
		ChangeAddress:    changeAddr,
		MinConfirmations: 1,
		ExpiryOffset:     40,
	}, spendableNoteIDs(notes))

	plan, err = txbuild.PlanSend(ctx, txbuild.SendConfig{
		RPCURL:  jd.RPCURL,
		RPCUser: jd.RPCUser,
		RPCPass: jd.RPCPassword,

		WalletID: "test-wallet",
		CoinType: 0,
		Account:  0,

		ToAddress:     changeAddr,
		AmountZat:     strconv.FormatUint(maxNote, 10),
		ChangeAddress: changeAddr,

		MinConfirmations: 1,
		ExpiryOffset:     40,
		MinNoteZat:       0,
	})
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if len(plan.Notes) < 2 {
		t.Fatalf("notes=%d want >=2", len(plan.Notes))
	}

	_, err = txbuild.PlanSend(ctx, txbuild.SendConfig{
		RPCURL:  jd.RPCURL,
		RPCUser: jd.RPCUser,
		RPCPass: jd.RPCPassword,

		WalletID: "test-wallet",
		CoinType: 0,
		Account:  0,

		ToAddress:     changeAddr,
		AmountZat:     strconv.FormatUint(maxNote, 10),
		ChangeAddress: changeAddr,

		MinConfirmations: 1,
		ExpiryOffset:     40,
		MinNoteZat:       minNote + 1,
	})
	if err == nil {
		t.Fatalf("expected error")
	}
	var ce types.CodedError
	if !errors.As(err, &ce) || ce.Code != types.ErrCodeInsufficientBalance {
		t.Fatalf("expected insufficient_balance error, got %v", err)
	}
}

func TestIntegration_PlanSend_WithScanURL(t *testing.T) {
	jd, rpc := startJunocashd(t)

	changeAddr := unifiedAddress(t, jd, 0)
	mineAndShieldOnce(t, jd, changeAddr)
	toAddr := unifiedAddress(t, jd, 0)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	shieldCoinbase(t, jd, changeAddr, 2)
	notes := waitSpendableOrchardNoteCount(t, jd, 0, 2)
	if len(notes) != 2 {
		t.Fatalf("notes=%d want %d", len(notes), 2)
	}

	var minNote, maxNote uint64
	for i, n := range notes {
		if i == 0 || n.ValueZat < minNote {
			minNote = n.ValueZat
		}
		if n.ValueZat > maxNote {
			maxNote = n.ValueZat
		}
	}
	if minNote == 0 || maxNote == 0 || minNote == maxNote {
		t.Fatalf("unexpected note values (min=%d max=%d)", minNote, maxNote)
	}

	scanSrv := startScanStub(t, ctx, rpc, "")
	exercisePlanSendExclusions(t, ctx, txbuild.SendConfig{
		RPCURL:           jd.RPCURL,
		RPCUser:          jd.RPCUser,
		RPCPass:          jd.RPCPassword,
		ScanURL:          scanSrv.URL,
		WalletID:         "test-wallet",
		ToAddress:        changeAddr,
		AmountZat:        "1000000",
		ChangeAddress:    changeAddr,
		MinConfirmations: 1,
		ExpiryOffset:     40,
	}, spendableNoteIDs(notes))

	plan, err := txbuild.PlanSend(ctx, txbuild.SendConfig{
		RPCURL:  jd.RPCURL,
		RPCUser: jd.RPCUser,
		RPCPass: jd.RPCPassword,

		ScanURL: scanSrv.URL,

		WalletID: "test-wallet",
		CoinType: 0,
		Account:  0,

		ToAddress:     toAddr,
		AmountZat:     "1000000",
		ChangeAddress: changeAddr,

		MinConfirmations: 1,
		ExpiryOffset:     40,
		MinNoteZat:       0,
	})
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if err := validatePlanBasics(plan); err != nil {
		t.Fatalf("invalid plan: %v", err)
	}

	plan, err = txbuild.PlanSend(ctx, txbuild.SendConfig{
		RPCURL:  jd.RPCURL,
		RPCUser: jd.RPCUser,
		RPCPass: jd.RPCPassword,

		ScanURL: scanSrv.URL,

		WalletID: "test-wallet",
		CoinType: 0,
		Account:  0,

		ToAddress:     changeAddr,
		AmountZat:     strconv.FormatUint(maxNote, 10),
		ChangeAddress: changeAddr,

		MinConfirmations: 1,
		ExpiryOffset:     40,
		MinNoteZat:       0,
	})
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if len(plan.Notes) < 2 {
		t.Fatalf("notes=%d want >=2", len(plan.Notes))
	}

	_, err = txbuild.PlanSend(ctx, txbuild.SendConfig{
		RPCURL:  jd.RPCURL,
		RPCUser: jd.RPCUser,
		RPCPass: jd.RPCPassword,

		ScanURL: scanSrv.URL,

		WalletID: "test-wallet",
		CoinType: 0,
		Account:  0,

		ToAddress:     changeAddr,
		AmountZat:     strconv.FormatUint(maxNote, 10),
		ChangeAddress: changeAddr,

		MinConfirmations: 1,
		ExpiryOffset:     40,
		MinNoteZat:       minNote + 1,
	})
	if err == nil {
		t.Fatalf("expected error")
	}
	var ce types.CodedError
	if !errors.As(err, &ce) || ce.Code != types.ErrCodeInsufficientBalance {
		t.Fatalf("expected insufficient_balance error, got %v", err)
	}
}

func TestIntegration_PlanSend_WithScanURL_WithBearerToken(t *testing.T) {
	jd, rpc := startJunocashd(t)

	changeAddr := unifiedAddress(t, jd, 0)
	mineAndShieldOnce(t, jd, changeAddr)
	toAddr := unifiedAddress(t, jd, 0)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	scanSrv := startScanStub(t, ctx, rpc, "secret")

	plan, err := txbuild.PlanSend(ctx, txbuild.SendConfig{
		RPCURL:  jd.RPCURL,
		RPCUser: jd.RPCUser,
		RPCPass: jd.RPCPassword,

		ScanURL:         scanSrv.URL,
		ScanBearerToken: "secret",

		WalletID: "test-wallet",
		CoinType: 0,
		Account:  0,

		ToAddress:     toAddr,
		AmountZat:     "1000000",
		ChangeAddress: changeAddr,

		MinConfirmations: 1,
		ExpiryOffset:     40,
	})
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if err := validatePlanBasics(plan); err != nil {
		t.Fatalf("invalid plan: %v", err)
	}

	_, err = txbuild.PlanSend(ctx, txbuild.SendConfig{
		RPCURL:  jd.RPCURL,
		RPCUser: jd.RPCUser,
		RPCPass: jd.RPCPassword,

		ScanURL:         scanSrv.URL,
		ScanBearerToken: "wrong",

		WalletID: "test-wallet",
		CoinType: 0,
		Account:  0,

		ToAddress:     toAddr,
		AmountZat:     "1000000",
		ChangeAddress: changeAddr,

		MinConfirmations: 1,
		ExpiryOffset:     40,
	})
	if err == nil {
		t.Fatalf("expected error")
	}
	var he *junoscan.HTTPError
	if !errors.As(err, &he) || he.StatusCode != 401 {
		t.Fatalf("expected http 401 error, got %v", err)
	}
}

func TestIntegration_PlanSweep(t *testing.T) {
	jd, rpc := startJunocashd(t)

	orchardAddr := unifiedAddress(t, jd, 0)
	mineAndShieldOnce(t, jd, orchardAddr)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	plan, err := txbuild.PlanSweep(ctx, txbuild.SweepConfig{
		RPCURL:  jd.RPCURL,
		RPCUser: jd.RPCUser,
		RPCPass: jd.RPCPassword,

		WalletID: "test-wallet",
		CoinType: 0,
		Account:  0,

		ToAddress:     orchardAddr,
		ChangeAddress: orchardAddr,

		MinConfirmations: 1,
		ExpiryOffset:     40,
	})
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if plan.Kind != types.TxPlanKindSweep {
		t.Fatalf("kind=%q want %q", plan.Kind, types.TxPlanKindSweep)
	}
	if err := validatePlanBasics(plan); err != nil {
		t.Fatalf("invalid plan: %v", err)
	}
	if len(plan.Outputs) != 1 {
		t.Fatalf("outputs=%d want %d", len(plan.Outputs), 1)
	}
	amt, err := strconv.ParseUint(plan.Outputs[0].AmountZat, 10, 64)
	if err != nil || amt == 0 {
		t.Fatalf("amount invalid")
	}

	excluded := make([]string, 0, len(plan.Notes))
	for _, note := range plan.Notes {
		excluded = append(excluded, note.NoteID)
	}
	_, err = txbuild.PlanSweep(ctx, txbuild.SweepConfig{
		RPCURL:           jd.RPCURL,
		RPCUser:          jd.RPCUser,
		RPCPass:          jd.RPCPassword,
		WalletID:         "test-wallet",
		ToAddress:        orchardAddr,
		ChangeAddress:    orchardAddr,
		ExcludedNoteIDs:  excluded,
		MinConfirmations: 1,
		ExpiryOffset:     40,
	})
	assertInsufficientBalance(t, err)

	scanSrv := startScanStub(t, ctx, rpc, "")
	_, err = txbuild.PlanSweep(ctx, txbuild.SweepConfig{
		RPCURL:           jd.RPCURL,
		RPCUser:          jd.RPCUser,
		RPCPass:          jd.RPCPassword,
		ScanURL:          scanSrv.URL,
		WalletID:         "test-wallet",
		ToAddress:        orchardAddr,
		ChangeAddress:    orchardAddr,
		ExcludedNoteIDs:  excluded,
		MinConfirmations: 1,
		ExpiryOffset:     40,
	})
	assertInsufficientBalance(t, err)
}

func TestIntegration_PlanSendMany(t *testing.T) {
	jd, _ := startJunocashd(t)

	changeAddr := unifiedAddress(t, jd, 0)
	mineAndShieldOnce(t, jd, changeAddr)
	toAddr := unifiedAddress(t, jd, 0)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	plan, err := txbuild.Plan(ctx, txbuild.PlanConfig{
		RPCURL:  jd.RPCURL,
		RPCUser: jd.RPCUser,
		RPCPass: jd.RPCPassword,

		WalletID: "test-wallet",
		CoinType: 0,
		Account:  0,

		Kind: types.TxPlanKindWithdrawal,
		Outputs: []types.TxOutput{
			{ToAddress: toAddr, AmountZat: "1000000"},
			{ToAddress: toAddr, AmountZat: "2000000"},
		},
		ChangeAddress: changeAddr,

		MinConfirmations: 1,
		ExpiryOffset:     40,
	})
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if plan.Kind != types.TxPlanKindWithdrawal {
		t.Fatalf("kind=%q want %q", plan.Kind, types.TxPlanKindWithdrawal)
	}
	if err := validatePlanBasics(plan); err != nil {
		t.Fatalf("invalid plan: %v", err)
	}
	if len(plan.Outputs) != 2 {
		t.Fatalf("outputs=%d want %d", len(plan.Outputs), 2)
	}
}

func TestIntegration_PlanConsolidate(t *testing.T) {
	jd, _ := startJunocashd(t)

	orchardAddr := unifiedAddress(t, jd, 0)
	mineAndShieldOnce(t, jd, orchardAddr)
	mineAndShieldOnce(t, jd, orchardAddr)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	plan, err := txbuild.PlanConsolidate(ctx, txbuild.ConsolidateConfig{
		RPCURL:  jd.RPCURL,
		RPCUser: jd.RPCUser,
		RPCPass: jd.RPCPassword,

		WalletID: "test-wallet",
		CoinType: 0,
		Account:  0,

		ToAddress:     orchardAddr,
		ChangeAddress: orchardAddr,
		MaxSpends:     50,

		MinConfirmations: 1,
		ExpiryOffset:     40,
	})
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if plan.Kind != types.TxPlanKindRebalance {
		t.Fatalf("kind=%q want %q", plan.Kind, types.TxPlanKindRebalance)
	}
	if err := validatePlanBasics(plan); err != nil {
		t.Fatalf("invalid plan: %v", err)
	}
	if len(plan.Outputs) != 1 {
		t.Fatalf("outputs=%d want %d", len(plan.Outputs), 1)
	}
	if len(plan.Notes) < 2 {
		t.Fatalf("notes=%d want >=2", len(plan.Notes))
	}
}

func TestIntegration_PlanConsolidate_WithScanURL(t *testing.T) {
	jd, rpc := startJunocashd(t)

	orchardAddr := unifiedAddress(t, jd, 0)
	mineAndShieldOnce(t, jd, orchardAddr)
	mineAndShieldOnce(t, jd, orchardAddr)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	scanSrv := startScanStub(t, ctx, rpc, "secret")

	plan, err := txbuild.PlanConsolidate(ctx, txbuild.ConsolidateConfig{
		RPCURL:  jd.RPCURL,
		RPCUser: jd.RPCUser,
		RPCPass: jd.RPCPassword,

		ScanURL:         scanSrv.URL,
		ScanBearerToken: "secret",

		WalletID: "test-wallet",
		CoinType: 0,
		Account:  0,

		ToAddress:     orchardAddr,
		ChangeAddress: orchardAddr,
		MaxSpends:     50,

		MinConfirmations: 1,
		ExpiryOffset:     40,
	})
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if plan.Kind != types.TxPlanKindRebalance {
		t.Fatalf("kind=%q want %q", plan.Kind, types.TxPlanKindRebalance)
	}
	if err := validatePlanBasics(plan); err != nil {
		t.Fatalf("invalid plan: %v", err)
	}
	if len(plan.Outputs) != 1 {
		t.Fatalf("outputs=%d want %d", len(plan.Outputs), 1)
	}
	if len(plan.Notes) < 2 {
		t.Fatalf("notes=%d want >=2", len(plan.Notes))
	}
}

func spendableNoteIDs(notes []spendableOrchardNote) []string {
	ids := make([]string, 0, len(notes))
	for _, note := range notes {
		ids = append(ids, note.TxID+":"+strconv.FormatUint(uint64(note.OutIndex), 10))
	}
	return ids
}

func exercisePlanSendExclusions(t *testing.T, ctx context.Context, cfg txbuild.SendConfig, allNoteIDs []string) {
	t.Helper()
	if len(allNoteIDs) < 2 {
		t.Fatalf("notes=%d want at least 2", len(allNoteIDs))
	}

	baseline, err := txbuild.PlanSend(ctx, cfg)
	if err != nil {
		t.Fatalf("baseline exclusion plan: %v", err)
	}
	if len(baseline.Notes) != 1 {
		t.Fatalf("baseline selected notes=%d want 1", len(baseline.Notes))
	}

	cfg.ExcludedNoteIDs = []string{baseline.Notes[0].NoteID}
	alternate, err := txbuild.PlanSend(ctx, cfg)
	if err != nil {
		t.Fatalf("alternate exclusion plan: %v", err)
	}
	for _, note := range alternate.Notes {
		if note.NoteID == baseline.Notes[0].NoteID {
			t.Fatalf("excluded note %q was selected", note.NoteID)
		}
	}

	cfg.ExcludedNoteIDs = allNoteIDs
	_, err = txbuild.PlanSend(ctx, cfg)
	assertInsufficientBalance(t, err)
}

func assertInsufficientBalance(t *testing.T, err error) {
	t.Helper()
	var coded types.CodedError
	if !errors.As(err, &coded) || coded.Code != types.ErrCodeInsufficientBalance {
		t.Fatalf("expected insufficient_balance, got %v", err)
	}
}

func TestIntegration_PlanSendManyExtraSpends(t *testing.T) {
	jd, _ := startJunocashd(t)

	changeAddr := unifiedAddress(t, jd, 0)
	mineAndShieldOnce(t, jd, changeAddr)
	before := len(listSpendableOrchardNotes(t, jd, 0))
	fundSmallNotes(t, jd, 0, changeAddr, 6, "0.005", before+6)
	_, toAddr := newAccountAddress(t, jd)

	smallIDs := make(map[string]struct{})
	for _, n := range listSpendableOrchardNotes(t, jd, 0) {
		if n.ValueZat == 500_000 {
			smallIDs[planNoteID(n.TxID, n.OutIndex)] = struct{}{}
		}
	}
	if len(smallIDs) != 6 {
		t.Fatalf("small notes=%d want 6", len(smallIDs))
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	cfg := txbuild.PlanConfig{
		RPCURL:  jd.RPCURL,
		RPCUser: jd.RPCUser,
		RPCPass: jd.RPCPassword,

		WalletID: "test-wallet",
		Account:  0,

		Kind: types.TxPlanKindWithdrawal,
		Outputs: []types.TxOutput{
			{ToAddress: toAddr, AmountZat: "3000000"},
		},
		ChangeAddress: changeAddr,

		MinConfirmations: 1,
		ExpiryOffset:     40,
	}

	legacy, err := txbuild.Plan(ctx, cfg)
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	base, baseReport, err := txbuild.PlanWithReport(ctx, cfg)
	if err != nil {
		t.Fatalf("plan with report: %v", err)
	}
	legacyJSON, _ := json.Marshal(legacy)
	baseJSON, _ := json.Marshal(base)
	if string(legacyJSON) != string(baseJSON) {
		t.Fatalf("extra_spends=0 changed the plan")
	}
	if baseReport != (txbuild.PlanReport{}) {
		t.Fatalf("base report=%+v want zero", baseReport)
	}
	if len(base.Notes) != 1 {
		t.Fatalf("base notes=%d want 1", len(base.Notes))
	}
	if _, ok := smallIDs[base.Notes[0].NoteID]; ok {
		t.Fatalf("base selection unexpectedly picked a small note")
	}

	cfg.ExtraSpends = 4
	cfg.ExtraSpendMaxZat = 1_000_000
	plan, report, err := txbuild.PlanWithReport(ctx, cfg)
	if err != nil {
		t.Fatalf("plan with extra spends: %v", err)
	}
	if err := validatePlanBasics(plan); err != nil {
		t.Fatalf("invalid plan: %v", err)
	}
	if report.ExtraSpends != 4 || report.ExtraSpendZat != 2_000_000 {
		t.Fatalf("report=%+v want 4 extras totalling 2000000", report)
	}
	if len(plan.Notes) != 5 {
		t.Fatalf("notes=%d want 5", len(plan.Notes))
	}
	if plan.Notes[0].NoteID != base.Notes[0].NoteID {
		t.Fatalf("base note moved: got %q want %q", plan.Notes[0].NoteID, base.Notes[0].NoteID)
	}
	seen := make(map[string]struct{})
	for _, n := range plan.Notes[1:] {
		if _, ok := smallIDs[n.NoteID]; !ok {
			t.Fatalf("extra note %q is not one of the small notes", n.NoteID)
		}
		if _, dup := seen[n.NoteID]; dup {
			t.Fatalf("duplicate note %q", n.NoteID)
		}
		seen[n.NoteID] = struct{}{}
	}
	if plan.FeeZat != "500000" {
		t.Fatalf("fee=%s want 500000", plan.FeeZat)
	}
	outJSON, _ := json.Marshal(plan.Outputs)
	baseOutJSON, _ := json.Marshal(base.Outputs)
	if string(outJSON) != string(baseOutJSON) || plan.ChangeAddress != base.ChangeAddress {
		t.Fatalf("outputs or change address changed")
	}

	cfg.ExcludedNoteIDs = nil
	for id := range smallIDs {
		cfg.ExcludedNoteIDs = append(cfg.ExcludedNoteIDs, id)
	}
	excluded, report, err := txbuild.PlanWithReport(ctx, cfg)
	if err != nil {
		t.Fatalf("plan with excluded small notes: %v", err)
	}
	if report.ExtraSpends != 0 || len(excluded.Notes) != 1 {
		t.Fatalf("excluded small notes were topped up: report=%+v notes=%d", report, len(excluded.Notes))
	}
}
