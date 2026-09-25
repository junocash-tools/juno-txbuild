//go:build e2e

package app

import (
	"context"
	"crypto/pbkdf2"
	"crypto/sha512"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Abdullah1738/juno-sdk-go/types"
	"github.com/Abdullah1738/juno-txbuild/internal/testutil/containers"
)

type signedTx struct {
	TxID     string
	RawTxHex string
}

// txsignBinary returns the juno-txsign binary used to sign plans in e2e tests.
// Set JUNO_TXSIGN_BIN, or build ../juno-txsign/bin/juno-txsign. A missing
// signer fails the test unless JUNO_TXSIGN_SKIP=1 is set explicitly.
func txsignBinary(t *testing.T) string {
	t.Helper()
	bin := strings.TrimSpace(os.Getenv("JUNO_TXSIGN_BIN"))
	if bin == "" {
		bin = filepath.Join(repoRoot(), "..", "juno-txsign", "bin", "juno-txsign")
	}
	if _, err := os.Stat(bin); err != nil {
		if os.Getenv("JUNO_TXSIGN_SKIP") == "1" {
			t.Skipf("juno-txsign binary not found at %s (JUNO_TXSIGN_SKIP=1): %v", bin, err)
		}
		t.Fatalf("juno-txsign binary not found at %s: set JUNO_TXSIGN_BIN, or JUNO_TXSIGN_SKIP=1 to skip signing tests: %v", bin, err)
	}
	return bin
}

// nodeSeedBase64 derives the wallet's BIP39 seed from z_getseedphrase.
func nodeSeedBase64(t *testing.T, ctx context.Context, jd *containers.Junocashd) string {
	t.Helper()
	raw, err := jd.ExecCLI(ctx, "z_getseedphrase")
	if err != nil {
		t.Fatalf("z_getseedphrase: %v", err)
	}
	var mnemonic string
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "===") {
			continue
		}
		if words := strings.Fields(strings.Trim(line, "\"")); len(words) == 24 {
			mnemonic = strings.Join(words, " ")
			break
		}
	}
	if mnemonic == "" {
		t.Fatalf("z_getseedphrase: mnemonic not found")
	}
	seed, err := pbkdf2.Key(sha512.New, mnemonic, []byte("mnemonic"), 2048, 64)
	if err != nil {
		t.Fatalf("derive seed: %v", err)
	}
	return base64.StdEncoding.EncodeToString(seed)
}

func signPlanWithNodeSeed(t *testing.T, ctx context.Context, jd *containers.Junocashd, signer string, plan types.TxPlan) signedTx {
	t.Helper()
	tmp := t.TempDir()
	planPath := filepath.Join(tmp, "txplan.json")
	seedPath := filepath.Join(tmp, "seed.base64")

	planJSON, err := json.Marshal(plan)
	if err != nil {
		t.Fatalf("marshal txplan: %v", err)
	}
	if err := os.WriteFile(planPath, append(planJSON, '\n'), 0o600); err != nil {
		t.Fatalf("write txplan: %v", err)
	}
	if err := os.WriteFile(seedPath, []byte(nodeSeedBase64(t, ctx, jd)+"\n"), 0o600); err != nil {
		t.Fatalf("write seed: %v", err)
	}

	out, err := exec.CommandContext(ctx, signer, "sign", "--txplan", planPath, "--seed-file", seedPath, "--json").Output()
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			t.Fatalf("juno-txsign: %s %s", strings.TrimSpace(string(out)), strings.TrimSpace(string(ee.Stderr)))
		}
		t.Fatalf("juno-txsign: %v", err)
	}
	var resp struct {
		Status string `json:"status"`
		Data   struct {
			TxID     string `json:"txid"`
			RawTxHex string `json:"raw_tx_hex"`
		} `json:"data"`
	}
	if err := json.Unmarshal(out, &resp); err != nil {
		t.Fatalf("decode juno-txsign output: %v", err)
	}
	if resp.Status != "ok" || resp.Data.TxID == "" || resp.Data.RawTxHex == "" {
		t.Fatalf("unexpected juno-txsign output: %s", out)
	}
	return signedTx{TxID: resp.Data.TxID, RawTxHex: resp.Data.RawTxHex}
}
