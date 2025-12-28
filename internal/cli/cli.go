package cli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Abdullah1738/juno-sdk-go/types"
	"github.com/Abdullah1738/juno-txbuild/pkg/txbuild"
)

func Run(args []string) int {
	return RunWithIO(args, os.Stdout, os.Stderr)
}

func RunWithIO(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		writeUsage(stdout)
		return 2
	}

	switch args[0] {
	case "-h", "--help", "help":
		writeUsage(stdout)
		return 0
	case "send":
		return runSend(args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "unknown command: %s\n\n", args[0])
		writeUsage(stderr)
		return 2
	}
}

func writeUsage(w io.Writer) {
	fmt.Fprintln(w, "juno-txbuild")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "Online TxPlan v0 builder for offline signing.")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "Usage:")
	fmt.Fprintln(w, "  juno-txbuild send --rpc-url <url> --rpc-user <user> --rpc-pass <pass> --wallet-id <id> --coin-type <n> --account <n> --to <j1..> --amount-zat <zat> --change-address <j1..> [--memo-hex <hex>] [--minconf <n>] [--expiry-offset <n>] [--out <path>] [--json]")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "Env:")
	fmt.Fprintln(w, "  JUNO_RPC_URL, JUNO_RPC_USER, JUNO_RPC_PASS")
}

func runSend(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("send", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	var rpcURL string
	var rpcUser string
	var rpcPass string

	var walletID string
	var coinType uint
	var account uint
	var to string
	var amountZat string
	var memoHex string
	var changeAddr string
	var minconf int64
	var expiryOffset uint

	var outPath string
	var jsonOut bool

	fs.StringVar(&rpcURL, "rpc-url", "", "junocashd RPC URL")
	fs.StringVar(&rpcUser, "rpc-user", "", "junocashd RPC username")
	fs.StringVar(&rpcPass, "rpc-pass", "", "junocashd RPC password")

	fs.StringVar(&walletID, "wallet-id", "", "wallet id")
	fs.UintVar(&coinType, "coin-type", 8133, "ZIP-32 coin type")
	fs.UintVar(&account, "account", 0, "unified account id")
	fs.StringVar(&to, "to", "", "destination unified address (j1...)")
	fs.StringVar(&amountZat, "amount-zat", "", "amount to send in zatoshis")
	fs.StringVar(&memoHex, "memo-hex", "", "optional memo bytes (hex, <=512 bytes)")
	fs.StringVar(&changeAddr, "change-address", "", "change unified address (j1...)")
	fs.Int64Var(&minconf, "minconf", 1, "minimum confirmations for spendable notes")
	fs.UintVar(&expiryOffset, "expiry-offset", 40, "expiry height offset from chain tip")

	fs.StringVar(&outPath, "out", "", "optional path to write TxPlan JSON")
	fs.BoolVar(&jsonOut, "json", false, "JSON output")

	if err := fs.Parse(args); err != nil {
		fmt.Fprintln(stderr, err.Error())
		return 2
	}

	rpcURL, rpcUser, rpcPass, err := rpcConfigFromFlags(rpcURL, rpcUser, rpcPass)
	if err != nil {
		return writeErr(stdout, stderr, jsonOut, types.ErrCodeInvalidRequest, err.Error())
	}

	cfg := txbuild.SendConfig{
		RPCURL:  rpcURL,
		RPCUser: rpcUser,
		RPCPass: rpcPass,

		WalletID: walletID,
		CoinType: uint32(coinType),
		Account:  uint32(account),

		ToAddress:     to,
		AmountZat:     amountZat,
		MemoHex:       memoHex,
		ChangeAddress: changeAddr,

		MinConfirmations: minconf,
		ExpiryOffset:     uint32(expiryOffset),
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	plan, err := txbuild.PlanSend(ctx, cfg)
	if err != nil {
		var ce types.CodedError
		if errors.As(err, &ce) {
			return writeErr(stdout, stderr, jsonOut, ce.Code, ce.Message)
		}
		return writeErr(stdout, stderr, jsonOut, types.ErrCodeInvalidRequest, err.Error())
	}

	b, err := json.MarshalIndent(plan, "", "  ")
	if err != nil {
		return writeErr(stdout, stderr, jsonOut, types.ErrCodeInvalidRequest, "marshal txplan")
	}
	b = append(b, '\n')

	if outPath != "" {
		if err := os.WriteFile(outPath, b, 0o600); err != nil {
			return writeErr(stdout, stderr, jsonOut, types.ErrCodeInvalidRequest, fmt.Sprintf("write %s: %v", filepath.Base(outPath), err))
		}
	}

	if jsonOut {
		_ = json.NewEncoder(stdout).Encode(map[string]any{
			"status": "ok",
			"data":   plan,
		})
		return 0
	}

	_, _ = stdout.Write(b)
	return 0
}

func rpcConfigFromFlags(url, user, pass string) (string, string, string, error) {
	if strings.TrimSpace(url) == "" {
		url = os.Getenv("JUNO_RPC_URL")
	}
	if strings.TrimSpace(user) == "" {
		user = os.Getenv("JUNO_RPC_USER")
	}
	if strings.TrimSpace(pass) == "" {
		pass = os.Getenv("JUNO_RPC_PASS")
	}

	url = strings.TrimSpace(url)
	if url == "" {
		return "", "", "", fmt.Errorf("rpc-url is required (or set JUNO_RPC_URL)")
	}
	return url, strings.TrimSpace(user), strings.TrimSpace(pass), nil
}

func writeErr(stdout, stderr io.Writer, jsonOut bool, code types.ErrorCode, msg string) int {
	if jsonOut {
		_ = json.NewEncoder(stdout).Encode(map[string]any{
			"status": "err",
			"error": map[string]any{
				"code":    code,
				"message": msg,
			},
		})
		return 1
	}
	if msg == "" {
		msg = string(code)
	}
	fmt.Fprintln(stderr, msg)
	return 1
}
