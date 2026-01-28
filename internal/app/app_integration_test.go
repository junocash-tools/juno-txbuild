//go:build integration

package app

import (
	"context"
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
}

func TestIntegration_PlanSend_WithScanURL(t *testing.T) {
	jd, rpc := startJunocashd(t)

	changeAddr := unifiedAddress(t, jd, 0)
	mineAndShieldOnce(t, jd, changeAddr)
	toAddr := unifiedAddress(t, jd, 0)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	scanSrv := startScanStub(t, ctx, rpc, "")

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
	})
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if err := validatePlanBasics(plan); err != nil {
		t.Fatalf("invalid plan: %v", err)
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
	jd, _ := startJunocashd(t)

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
