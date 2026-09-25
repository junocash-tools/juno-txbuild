//go:build e2e

package app

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
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
	if err == nil {
		t.Fatal("default 100-confirmation policy accepted a two-confirmation note")
	}
	var immature struct {
		Status string `json:"status"`
		Error  struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if decodeErr := json.Unmarshal(out, &immature); decodeErr != nil || immature.Status != "err" || immature.Error.Code != string(types.ErrCodeInsufficientBalance) {
		t.Fatalf("unexpected immature default response: %s (decode=%v)", out, decodeErr)
	}
	if _, err := jd.ExecCLI(ctx, "generate", "98"); err != nil {
		t.Fatalf("mature default-confirmation note: %v", err)
	}
	cmd = exec.CommandContext(
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
	out, err = cmd.Output()
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

	shieldCoinbase(t, jd, changeAddr, 2)
	notes := waitSpendableOrchardNoteCount(t, jd, 0, 2)

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

	runWithExclusions := func(excludedNoteIDs []string) ([]byte, error) {
		args := []string{
			"send",
			"--rpc-url", jd.RPCURL,
			"--rpc-user", jd.RPCUser,
			"--rpc-pass", jd.RPCPassword,
			"--wallet-id", "test-wallet",
			"--account", "0",
			"--to", changeAddr,
			"--amount-zat", "1000000",
			"--change-address", changeAddr,
			"--minconf", "1",
			"--json",
		}
		for _, noteID := range excludedNoteIDs {
			args = append(args, "--exclude-note-id", noteID)
		}
		return exec.CommandContext(ctx, bin, args...).Output()
	}

	out, err = runWithExclusions(nil)
	if err != nil {
		t.Fatalf("baseline exclusion CLI plan: %v", err)
	}
	if err := json.Unmarshal(out, &resp); err != nil {
		t.Fatalf("decode baseline exclusion plan: %v", err)
	}
	if resp.Status != "ok" || len(resp.Data.Notes) != 1 {
		t.Fatalf("unexpected baseline exclusion response: %s", out)
	}
	excludedNoteID := resp.Data.Notes[0].NoteID

	out, err = runWithExclusions([]string{excludedNoteID})
	if err != nil {
		t.Fatalf("alternate exclusion CLI plan: %v", err)
	}
	if err := json.Unmarshal(out, &resp); err != nil {
		t.Fatalf("decode alternate exclusion plan: %v", err)
	}
	if resp.Status != "ok" || len(resp.Data.Notes) == 0 {
		t.Fatalf("unexpected alternate exclusion response: %s", out)
	}
	for _, note := range resp.Data.Notes {
		if note.NoteID == excludedNoteID {
			t.Fatalf("excluded note %q was selected", note.NoteID)
		}
	}

	allNoteIDs := make([]string, 0, len(notes))
	for _, note := range notes {
		allNoteIDs = append(allNoteIDs, note.TxID+":"+strconv.FormatUint(uint64(note.OutIndex), 10))
	}
	out, err = runWithExclusions(allNoteIDs)
	if err == nil {
		t.Fatal("all-excluded CLI plan unexpectedly succeeded")
	}
	var excludedErrResp struct {
		Status string `json:"status"`
		Error  struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if decodeErr := json.Unmarshal(out, &excludedErrResp); decodeErr != nil || excludedErrResp.Status != "err" || excludedErrResp.Error.Code != string(types.ErrCodeInsufficientBalance) {
		t.Fatalf("unexpected all-excluded response: %s (decode=%v)", out, decodeErr)
	}

	cmd = exec.CommandContext(
		ctx,
		bin,
		"send",
		"--rpc-url", jd.RPCURL,
		"--rpc-user", jd.RPCUser,
		"--rpc-pass", jd.RPCPassword,
		"--wallet-id", "test-wallet",
		"--account", "0",
		"--to", changeAddr,
		"--amount-zat", strconv.FormatUint(maxNote, 10),
		"--change-address", changeAddr,
		"--minconf", "1",
		"--json",
	)

	out, err = cmd.Output()
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			t.Fatalf("juno-txbuild: %s", strings.TrimSpace(string(ee.Stderr)))
		}
		t.Fatalf("juno-txbuild: %v", err)
	}
	if err := json.Unmarshal(out, &resp); err != nil {
		t.Fatalf("decode json: %v", err)
	}
	if resp.Status != "ok" {
		t.Fatalf("unexpected status")
	}
	if len(resp.Data.Notes) < 2 {
		t.Fatalf("notes=%d want >=2", len(resp.Data.Notes))
	}

	cmd = exec.CommandContext(
		ctx,
		bin,
		"send",
		"--rpc-url", jd.RPCURL,
		"--rpc-user", jd.RPCUser,
		"--rpc-pass", jd.RPCPassword,
		"--wallet-id", "test-wallet",
		"--account", "0",
		"--to", changeAddr,
		"--amount-zat", strconv.FormatUint(maxNote, 10),
		"--change-address", changeAddr,
		"--min-note-zat", strconv.FormatUint(minNote+1, 10),
		"--minconf", "1",
		"--json",
	)

	out, err = cmd.Output()
	if err == nil {
		t.Fatalf("expected error")
	}

	var errResp struct {
		Status string `json:"status"`
		Error  struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(out, &errResp); err != nil {
		t.Fatalf("decode json: %v", err)
	}
	if errResp.Status != "err" {
		t.Fatalf("unexpected status")
	}
	if errResp.Error.Code != string(types.ErrCodeInsufficientBalance) {
		t.Fatalf("unexpected error code: %q (%s)", errResp.Error.Code, errResp.Error.Message)
	}
}

func TestE2E_CLI_SendBuildsTxPlan_WithScanURL(t *testing.T) {
	jd, rpc := startJunocashd(t)

	changeAddr := unifiedAddress(t, jd, 0)
	mineAndShieldOnce(t, jd, changeAddr)
	toAddr := unifiedAddress(t, jd, 0)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	scanSrv := startScanStub(t, ctx, rpc, "")

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
		"--minconf", "1",
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

func TestE2E_CLI_SendBuildsTxPlan_WithScanURL_WithBearerToken(t *testing.T) {
	jd, rpc := startJunocashd(t)

	changeAddr := unifiedAddress(t, jd, 0)
	mineAndShieldOnce(t, jd, changeAddr)
	toAddr := unifiedAddress(t, jd, 0)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	scanSrv := startScanStub(t, ctx, rpc, "secret")

	bin := filepath.Join(repoRoot(), "bin", "juno-txbuild")
	cmd := exec.CommandContext(
		ctx,
		bin,
		"send",
		"--rpc-url", jd.RPCURL,
		"--rpc-user", jd.RPCUser,
		"--rpc-pass", jd.RPCPassword,
		"--scan-url", scanSrv.URL,
		"--scan-bearer-token", "secret",
		"--wallet-id", "test-wallet",
		"--account", "0",
		"--to", toAddr,
		"--amount-zat", "1000000",
		"--change-address", changeAddr,
		"--minconf", "1",
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
		"--minconf", "1",
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
		"--minconf", "1",
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

func TestE2E_CLI_ConsolidateBuildsTxPlan(t *testing.T) {
	jd, _ := startJunocashd(t)

	addr := unifiedAddress(t, jd, 0)
	mineAndShieldOnce(t, jd, addr)
	mineAndShieldOnce(t, jd, addr)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	bin := filepath.Join(repoRoot(), "bin", "juno-txbuild")
	cmd := exec.CommandContext(
		ctx,
		bin,
		"consolidate",
		"--rpc-url", jd.RPCURL,
		"--rpc-user", jd.RPCUser,
		"--rpc-pass", jd.RPCPassword,
		"--wallet-id", "test-wallet",
		"--account", "0",
		"--to", addr,
		"--change-address", addr,
		"--max-spends", "50",
		"--minconf", "1",
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
	if resp.Data.Kind != types.TxPlanKindRebalance {
		t.Fatalf("unexpected kind")
	}
	if err := validatePlanBasics(resp.Data); err != nil {
		t.Fatalf("invalid plan: %v", err)
	}
	if len(resp.Data.Outputs) != 1 {
		t.Fatalf("unexpected outputs length")
	}
	if len(resp.Data.Notes) < 2 {
		t.Fatalf("unexpected notes length")
	}
}

func TestE2E_CLI_SendManyExtraSpendsMinesUnderStrictFeePolicy(t *testing.T) {
	signer := txsignBinary(t)

	jd, rpc := startJunocashd(t, "-txunpaidactionlimit=0", "-blockunpaidactionlimit=0")

	changeAddr := unifiedAddress(t, jd, 0)
	mineAndShieldOnce(t, jd, changeAddr)
	before := len(listSpendableOrchardNotes(t, jd, 0))
	fundSmallNotes(t, jd, 0, changeAddr, 6, "0.005", before+6)
	_, toAddr := newAccountAddress(t, jd)

	var smallIDs []string
	for _, n := range listSpendableOrchardNotes(t, jd, 0) {
		if n.ValueZat == 500_000 {
			smallIDs = append(smallIDs, planNoteID(n.TxID, n.OutIndex))
		}
	}
	if len(smallIDs) != 6 {
		t.Fatalf("small notes=%d want 6", len(smallIDs))
	}
	// Equal value and height: the tie-break is txid then action index, and
	// all six share a txid, so the four lowest action indices get picked.
	sortNoteIDsByActionIndex(t, smallIDs)
	wantExtras := noteIDSet(smallIDs[:4])

	tmp := t.TempDir()
	outsPath := filepath.Join(tmp, "outputs.json")
	outs := []types.TxOutput{{ToAddress: toAddr, AmountZat: "3000000"}}
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
		"--minconf", "1",
		"--extra-spends", "4",
		"--extra-spend-max-zat", "1000000",
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
		Version   string       `json:"version"`
		Status    string       `json:"status"`
		Data      types.TxPlan `json:"data"`
		Selection struct {
			ExtraSpends   int    `json:"extra_spends"`
			ExtraSpendZat string `json:"extra_spend_zat"`
		} `json:"selection"`
	}
	if err := json.Unmarshal(out, &resp); err != nil {
		t.Fatalf("decode json: %v", err)
	}
	if resp.Version != "v1" || resp.Status != "ok" {
		t.Fatalf("unexpected envelope: %s", out)
	}
	if resp.Selection.ExtraSpends != 4 || resp.Selection.ExtraSpendZat != "2000000" {
		t.Fatalf("selection=%+v want 4 extras totalling 2000000", resp.Selection)
	}
	plan := resp.Data
	if err := validatePlanBasics(plan); err != nil {
		t.Fatalf("invalid plan: %v", err)
	}
	if len(plan.Outputs) != 1 || plan.Outputs[0] != outs[0] {
		t.Fatalf("outputs changed: %+v", plan.Outputs)
	}
	if len(plan.Notes) != 5 {
		t.Fatalf("notes=%d want 5", len(plan.Notes))
	}
	for _, n := range plan.Notes[1:] {
		if _, ok := wantExtras[strings.ToLower(n.NoteID)]; !ok {
			t.Fatalf("unexpected extra note %q (want %v)", n.NoteID, smallIDs[:4])
		}
	}
	if plan.FeeZat != "500000" {
		t.Fatalf("fee=%s want 500000", plan.FeeZat)
	}

	// Control: the same top-up at the pre-ZIP-317 marginal rate (5000/action)
	// must be refused by this node, so the acceptance below proves the fee.
	underpaidOut, err := exec.CommandContext(
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
		"--minconf", "1",
		"--fee-multiplier", "1",
		"--extra-spends", "4",
		"--extra-spend-max-zat", "1000000",
		"--json",
	).Output()
	if err != nil {
		t.Fatalf("underpaid control plan: %v", err)
	}
	var underpaid struct {
		Data types.TxPlan `json:"data"`
	}
	if err := json.Unmarshal(underpaidOut, &underpaid); err != nil {
		t.Fatalf("decode underpaid control plan: %v", err)
	}
	if underpaid.Data.FeeZat != "25000" || len(underpaid.Data.Notes) != 5 {
		t.Fatalf("underpaid control: fee=%s notes=%d want 25000/5", underpaid.Data.FeeZat, len(underpaid.Data.Notes))
	}
	underpaidSigned := signPlanWithNodeSeed(t, ctx, jd, signer, underpaid.Data)
	err = rpc.Call(ctx, "sendrawtransaction", []any{underpaidSigned.RawTxHex}, nil)
	if err == nil {
		t.Fatalf("strict node accepted an underpaid top-up")
	}
	if !strings.Contains(err.Error(), "unpaid action limit exceeded") {
		t.Fatalf("underpaid top-up rejected for the wrong reason: %v", err)
	}

	signed := signPlanWithNodeSeed(t, ctx, jd, signer, plan)

	var acceptedTxID string
	if err := rpc.Call(ctx, "sendrawtransaction", []any{signed.RawTxHex}, &acceptedTxID); err != nil {
		t.Fatalf("sendrawtransaction under strict fee policy: %v", err)
	}
	if !strings.EqualFold(acceptedTxID, signed.TxID) {
		t.Fatalf("txid mismatch: got %s want %s", acceptedTxID, signed.TxID)
	}
	var hashes []string
	if err := rpc.Call(ctx, "generate", []any{1}, &hashes); err != nil || len(hashes) != 1 {
		t.Fatalf("generate: %v", err)
	}
	var blk struct {
		Tx []string `json:"tx"`
	}
	if err := rpc.Call(ctx, "getblock", []any{hashes[0], 1}, &blk); err != nil {
		t.Fatalf("getblock: %v", err)
	}
	mined := false
	for _, txid := range blk.Tx {
		if strings.EqualFold(txid, signed.TxID) {
			mined = true
		}
	}
	if !mined {
		t.Fatalf("tx %s not mined in block %s", signed.TxID, hashes[0])
	}

	// Five notes spent, one change note back: the account drops by four.
	after := waitSpendableOrchardNoteCountExact(t, jd, 0, before+6-4)
	for _, n := range after {
		id := planNoteID(n.TxID, n.OutIndex)
		for _, spent := range plan.Notes {
			if strings.EqualFold(id, spent.NoteID) {
				t.Fatalf("spent note %s still unspent", id)
			}
		}
	}
}

// TestE2E_CLI_SendManyWithReleasedJunoScan runs send-many against the pinned
// juno-scan v1.4.7-mainnet release (no event_epoch in /v1/health), then signs
// and mines the topped-up plan on a strict-fee node.
func TestE2E_CLI_SendManyWithReleasedJunoScan(t *testing.T) {
	signer := txsignBinary(t)
	fx := setupReleasedScanWallet(t, "secret", "-txunpaidactionlimit=0", "-blockunpaidactionlimit=0")

	tmp := t.TempDir()
	outsPath := filepath.Join(tmp, "outputs.json")
	outs := []types.TxOutput{{ToAddress: fx.toAddr, AmountZat: "3000000"}}
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
	sendMany := func(extra ...string) ([]byte, error) {
		args := []string{
			"send-many",
			"--rpc-url", fx.jd.RPCURL,
			"--rpc-user", fx.jd.RPCUser,
			"--rpc-pass", fx.jd.RPCPassword,
			"--scan-url", fx.scan.URL,
			"--scan-bearer-token", fx.bearerToken,
			"--wallet-id", fx.walletID,
			"--account", "0",
			"--outputs-file", outsPath,
			"--change-address", fx.changeAddr,
			"--minconf", "1",
			"--json",
		}
		return exec.CommandContext(ctx, bin, append(args, extra...)...).Output()
	}

	type envelope struct {
		Version   string       `json:"version"`
		Status    string       `json:"status"`
		Data      types.TxPlan `json:"data"`
		Selection *struct {
			ExtraSpends   int    `json:"extra_spends"`
			ExtraSpendZat string `json:"extra_spend_zat"`
		} `json:"selection"`
	}
	run := func(extra ...string) envelope {
		t.Helper()
		out, err := sendMany(extra...)
		if err != nil {
			var ee *exec.ExitError
			if errors.As(err, &ee) {
				t.Fatalf("juno-txbuild %v: %s %s", extra, strings.TrimSpace(string(out)), strings.TrimSpace(string(ee.Stderr)))
			}
			t.Fatalf("juno-txbuild %v: %v", extra, err)
		}
		var resp envelope
		if err := json.Unmarshal(out, &resp); err != nil {
			t.Fatalf("decode json: %v", err)
		}
		if resp.Version != "v1" || resp.Status != "ok" {
			t.Fatalf("unexpected envelope: %s", out)
		}
		if err := validatePlanBasics(resp.Data); err != nil {
			t.Fatalf("invalid plan: %v", err)
		}
		return resp
	}

	base := run()
	if base.Selection != nil || len(base.Data.Notes) != 1 || base.Data.FeeZat != "200000" {
		t.Fatalf("base plan: selection=%v notes=%d fee=%s", base.Selection, len(base.Data.Notes), base.Data.FeeZat)
	}

	resp := run("--extra-spends", "4", "--extra-spend-max-zat", "1000000")
	if resp.Selection == nil || resp.Selection.ExtraSpends != 4 || resp.Selection.ExtraSpendZat != "2000000" {
		t.Fatalf("selection=%+v want 4 extras totalling 2000000", resp.Selection)
	}
	plan := resp.Data
	if len(plan.Outputs) != 1 || plan.Outputs[0] != outs[0] {
		t.Fatalf("outputs changed: %+v", plan.Outputs)
	}
	if len(plan.Notes) != 5 || plan.FeeZat != "500000" {
		t.Fatalf("plan: notes=%d fee=%s want 5/500000", len(plan.Notes), plan.FeeZat)
	}
	wantExtras := noteIDSet(fx.smallIDs[:4])
	for _, n := range plan.Notes[1:] {
		if _, ok := wantExtras[n.NoteID]; !ok {
			t.Fatalf("unexpected extra note %q (want %v)", n.NoteID, fx.smallIDs[:4])
		}
	}

	signed := signPlanWithNodeSeed(t, ctx, fx.jd, signer, plan)
	var acceptedTxID string
	if err := fx.rpc.Call(ctx, "sendrawtransaction", []any{signed.RawTxHex}, &acceptedTxID); err != nil {
		t.Fatalf("sendrawtransaction under strict fee policy: %v", err)
	}
	if !strings.EqualFold(acceptedTxID, signed.TxID) {
		t.Fatalf("txid mismatch: got %s want %s", acceptedTxID, signed.TxID)
	}
	if _, err := fx.jd.ExecCLI(ctx, "generate", "1"); err != nil {
		t.Fatalf("generate: %v", err)
	}
	after := waitSpendableOrchardNoteCountExact(t, fx.jd, 0, fx.before+6-4)
	for _, n := range after {
		id := planNoteID(n.TxID, n.OutIndex)
		for _, spent := range plan.Notes {
			if id == spent.NoteID {
				t.Fatalf("spent note %s still unspent", id)
			}
		}
	}
}
