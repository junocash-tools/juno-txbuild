//go:build e2e

package app

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Abdullah1738/juno-sdk-go/types"
)

func TestE2E_CLI_SendBuildsTxPlan(t *testing.T) {
	jd, _ := startJunocashd(t)

	changeAddr := unifiedAddress(t, jd, 0)
	mineAndShieldOnce(t, jd, changeAddr)
	toAddr := unifiedAddress(t, jd, 0)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	bin := filepath.Join(repoRoot(), "bin", "juno-txbuild")
	cmd := exec.CommandContext(
		ctx,
		bin,
		"send",
		"--rpc-url", jd.RPCURL,
		"--rpc-user", jd.RPCUser,
		"--rpc-pass", jd.RPCPassword,
		"--wallet-id", "test-wallet",
		"--account", "0",
		"--to", toAddr,
		"--amount-zat", "1000000",
		"--change-address", changeAddr,
		"--json",
	)

	out, err := cmd.Output()
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			t.Fatalf("juno-txbuild: %s", strings.TrimSpace(string(ee.Stderr)))
		}
		t.Fatalf("juno-txbuild: %v", err)
	}

	var resp struct {
		Status string       `json:"status"`
		Data   types.TxPlan `json:"data"`
	}
	if err := json.Unmarshal(out, &resp); err != nil {
		t.Fatalf("decode json: %v", err)
	}
	if resp.Status != "ok" {
		t.Fatalf("unexpected status")
	}
	if err := validatePlanBasics(resp.Data); err != nil {
		t.Fatalf("invalid plan: %v", err)
	}
}

func TestE2E_CLI_SendBuildsTxPlan_WithScanURL(t *testing.T) {
	jd, rpc := startJunocashd(t)

	changeAddr := unifiedAddress(t, jd, 0)
	mineAndShieldOnce(t, jd, changeAddr)
	toAddr := unifiedAddress(t, jd, 0)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	scanSrv := startScanStub(t, ctx, rpc)

	bin := filepath.Join(repoRoot(), "bin", "juno-txbuild")
	cmd := exec.CommandContext(
		ctx,
		bin,
		"send",
		"--rpc-url", jd.RPCURL,
		"--rpc-user", jd.RPCUser,
		"--rpc-pass", jd.RPCPassword,
		"--scan-url", scanSrv.URL,
		"--wallet-id", "test-wallet",
		"--account", "0",
		"--to", toAddr,
		"--amount-zat", "1000000",
		"--change-address", changeAddr,
		"--json",
	)

	out, err := cmd.Output()
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			t.Fatalf("juno-txbuild: %s", strings.TrimSpace(string(ee.Stderr)))
		}
		t.Fatalf("juno-txbuild: %v", err)
	}

	var resp struct {
		Status string       `json:"status"`
		Data   types.TxPlan `json:"data"`
	}
	if err := json.Unmarshal(out, &resp); err != nil {
		t.Fatalf("decode json: %v", err)
	}
	if resp.Status != "ok" {
		t.Fatalf("unexpected status")
	}
	if err := validatePlanBasics(resp.Data); err != nil {
		t.Fatalf("invalid plan: %v", err)
	}
}

func TestE2E_CLI_SweepBuildsTxPlan(t *testing.T) {
	jd, _ := startJunocashd(t)

	addr := unifiedAddress(t, jd, 0)
	mineAndShieldOnce(t, jd, addr)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	bin := filepath.Join(repoRoot(), "bin", "juno-txbuild")
	cmd := exec.CommandContext(
		ctx,
		bin,
		"sweep",
		"--rpc-url", jd.RPCURL,
		"--rpc-user", jd.RPCUser,
		"--rpc-pass", jd.RPCPassword,
		"--wallet-id", "test-wallet",
		"--account", "0",
		"--to", addr,
		"--change-address", addr,
		"--json",
	)

	out, err := cmd.Output()
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			t.Fatalf("juno-txbuild: %s", strings.TrimSpace(string(ee.Stderr)))
		}
		t.Fatalf("juno-txbuild: %v", err)
	}

	var resp struct {
		Status string       `json:"status"`
		Data   types.TxPlan `json:"data"`
	}
	if err := json.Unmarshal(out, &resp); err != nil {
		t.Fatalf("decode json: %v", err)
	}
	if resp.Status != "ok" {
		t.Fatalf("unexpected status")
	}
	if resp.Data.Kind != types.TxPlanKindSweep {
		t.Fatalf("unexpected kind")
	}
	if err := validatePlanBasics(resp.Data); err != nil {
		t.Fatalf("invalid plan: %v", err)
	}
}

func TestE2E_CLI_SendManyBuildsTxPlan(t *testing.T) {
	jd, _ := startJunocashd(t)

	changeAddr := unifiedAddress(t, jd, 0)
	mineAndShieldOnce(t, jd, changeAddr)
	toAddr := unifiedAddress(t, jd, 0)

	tmp := t.TempDir()
	outsPath := filepath.Join(tmp, "outputs.json")
	outs := []types.TxOutput{
		{ToAddress: toAddr, AmountZat: "1000000"},
		{ToAddress: toAddr, AmountZat: "2000000"},
	}
	b, err := json.Marshal(outs)
	if err != nil {
		t.Fatalf("marshal outputs: %v", err)
	}
	if err := os.WriteFile(outsPath, append(b, '\n'), 0o600); err != nil {
		t.Fatalf("write outputs: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	bin := filepath.Join(repoRoot(), "bin", "juno-txbuild")
	cmd := exec.CommandContext(
		ctx,
		bin,
		"send-many",
		"--rpc-url", jd.RPCURL,
		"--rpc-user", jd.RPCUser,
		"--rpc-pass", jd.RPCPassword,
		"--wallet-id", "test-wallet",
		"--account", "0",
		"--outputs-file", outsPath,
		"--change-address", changeAddr,
		"--json",
	)

	out, err := cmd.Output()
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			t.Fatalf("juno-txbuild: %s", strings.TrimSpace(string(ee.Stderr)))
		}
		t.Fatalf("juno-txbuild: %v", err)
	}

	var resp struct {
		Status string       `json:"status"`
		Data   types.TxPlan `json:"data"`
	}
	if err := json.Unmarshal(out, &resp); err != nil {
		t.Fatalf("decode json: %v", err)
	}
	if resp.Status != "ok" {
		t.Fatalf("unexpected status")
	}
	if resp.Data.Kind != types.TxPlanKindWithdrawal {
		t.Fatalf("unexpected kind")
	}
	if len(resp.Data.Outputs) != 2 {
		t.Fatalf("unexpected outputs length")
	}
	if err := validatePlanBasics(resp.Data); err != nil {
		t.Fatalf("invalid plan: %v", err)
	}
}
