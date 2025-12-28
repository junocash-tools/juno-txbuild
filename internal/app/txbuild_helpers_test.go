//go:build integration || e2e

package app

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Abdullah1738/juno-sdk-go/junocashd"
	"github.com/Abdullah1738/juno-sdk-go/types"
	"github.com/Abdullah1738/juno-txbuild/internal/testutil/containers"
)

const testCoinType = uint32(8133)

func startJunocashd(t *testing.T) (*containers.Junocashd, *junocashd.Client) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	t.Cleanup(cancel)

	jd, err := containers.StartJunocashd(ctx)
	if err != nil {
		t.Fatalf("start junocashd: %v", err)
	}
	t.Cleanup(func() {
		termCtx, termCancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer termCancel()
		_ = jd.Terminate(termCtx)
	})

	rpc := junocashd.New(jd.RPCURL, jd.RPCUser, jd.RPCPassword)
	return jd, rpc
}

func unifiedAddress(t *testing.T, jd *containers.Junocashd, account uint32) string {
	t.Helper()
	raw, err := jd.ExecCLI(context.Background(), "z_getaddressforaccount", strconv.FormatUint(uint64(account), 10))
	if err != nil {
		t.Fatalf("z_getaddressforaccount: %v", err)
	}
	var resp struct {
		Address string `json:"address"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		t.Fatalf("z_getaddressforaccount: invalid json")
	}
	addr := strings.TrimSpace(resp.Address)
	if addr == "" {
		t.Fatalf("z_getaddressforaccount: missing address")
	}
	return addr
}

func mineAndShieldOnce(t *testing.T, jd *containers.Junocashd, orchardAddr string) {
	t.Helper()
	ctx := context.Background()

	if _, err := jd.ExecCLI(ctx, "generate", "101"); err != nil {
		t.Fatalf("generate: %v", err)
	}

	raw, err := jd.ExecCLI(ctx, "z_shieldcoinbase", "*", orchardAddr)
	if err != nil {
		t.Fatalf("z_shieldcoinbase: %v", err)
	}
	var shieldResp struct {
		OpID string `json:"opid"`
	}
	if err := json.Unmarshal(raw, &shieldResp); err != nil {
		t.Fatalf("z_shieldcoinbase: invalid json")
	}
	opid := strings.TrimSpace(shieldResp.OpID)
	if opid == "" {
		t.Fatalf("z_shieldcoinbase: missing opid")
	}

	waitOpSuccess(t, jd, opid)

	if _, err := jd.ExecCLI(ctx, "generate", "1"); err != nil {
		t.Fatalf("confirm: %v", err)
	}
}

func waitOpSuccess(t *testing.T, jd *containers.Junocashd, opid string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	type op struct {
		Status string `json:"status"`
		Error  struct {
			Message string `json:"message"`
		} `json:"error,omitempty"`
	}

	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()

	for {
		raw, err := jd.ExecCLI(ctx, "z_getoperationstatus", fmt.Sprintf("[\"%s\"]", opid))
		if err != nil {
			t.Fatalf("z_getoperationstatus: %v", err)
		}
		var ops []op
		if err := json.Unmarshal(raw, &ops); err != nil {
			t.Fatalf("z_getoperationstatus: invalid json")
		}
		if len(ops) != 1 {
			t.Fatalf("z_getoperationstatus: unexpected response")
		}
		switch strings.ToLower(strings.TrimSpace(ops[0].Status)) {
		case "success":
			return
		case "failed":
			msg := strings.TrimSpace(ops[0].Error.Message)
			if msg == "" {
				msg = "operation failed"
			}
			t.Fatalf("%s", msg)
		default:
		}

		select {
		case <-ctx.Done():
			t.Fatalf("operation timeout")
		case <-ticker.C:
		}
	}
}

func validatePlanBasics(plan types.TxPlan) error {
	if plan.Version != types.V0 {
		return errors.New("version")
	}
	if strings.TrimSpace(string(plan.Kind)) == "" {
		return errors.New("kind")
	}
	if strings.TrimSpace(plan.WalletID) == "" {
		return errors.New("wallet_id")
	}
	if plan.CoinType != testCoinType {
		return errors.New("coin_type")
	}
	if strings.TrimSpace(plan.Chain) == "" {
		return errors.New("chain")
	}
	if plan.BranchID == 0 {
		return errors.New("branch_id")
	}
	if plan.AnchorHeight == 0 {
		return errors.New("anchor_height")
	}
	if len(plan.Anchor) != 64 {
		return errors.New("anchor")
	}
	if _, err := hex.DecodeString(plan.Anchor); err != nil {
		return errors.New("anchor_hex")
	}
	if plan.ExpiryHeight == 0 {
		return errors.New("expiry_height")
	}
	if len(plan.Outputs) != 1 {
		return errors.New("outputs")
	}
	if strings.TrimSpace(plan.Outputs[0].ToAddress) == "" {
		return errors.New("to_address")
	}
	if strings.TrimSpace(plan.Outputs[0].AmountZat) == "" {
		return errors.New("amount_zat")
	}
	if strings.TrimSpace(plan.ChangeAddress) == "" {
		return errors.New("change_address")
	}
	if strings.TrimSpace(plan.FeeZat) == "" {
		return errors.New("fee_zat")
	}
	if len(plan.Notes) == 0 {
		return errors.New("notes")
	}
	for _, n := range plan.Notes {
		if len(n.Path) != 32 {
			return errors.New("witness_path")
		}
	}
	return nil
}

func repoRoot() string {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		return "."
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
}

