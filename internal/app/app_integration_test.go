//go:build integration

package app

import (
	"context"
	"testing"
	"time"

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
