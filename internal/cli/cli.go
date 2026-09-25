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
	"strconv"
	"strings"
	"time"

	"github.com/Abdullah1738/juno-sdk-go/types"
	"github.com/Abdullah1738/juno-txbuild/pkg/txbuild"
)

const jsonVersionV1 = "v1"

type repeatedStringFlag []string

func (f *repeatedStringFlag) String() string {
	return strings.Join(*f, ",")
}

func (f *repeatedStringFlag) Set(value string) error {
	*f = append(*f, value)
	return nil
}

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
	case "send-many":
		return runPlanOutputs(args[1:], types.TxPlanKindWithdrawal, stdout, stderr)
	case "sweep":
		return runSweep(args[1:], stdout, stderr)
	case "consolidate":
		return runConsolidate(args[1:], stdout, stderr)
	case "rebalance":
		return runPlanOutputs(args[1:], types.TxPlanKindRebalance, stdout, stderr)
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
	fmt.Fprintln(w, "  juno-txbuild send --rpc-url <url> --rpc-user <user> --rpc-pass <pass> [--scan-url <url>] [--scan-bearer-token <token>] --wallet-id <id> --coin-type <n> --account <n> --to <j*1..> --amount-zat <zat> --change-address <j*1..> [--exclude-note-id <txid:index>]... [--memo-hex <hex>] [--fee-multiplier <n>] [--fee-add-zat <zat>] [--min-change-zat <zat>] [--min-note-zat <zat>] [--minconf <n>] [--expiry-offset <n>] [--out <path>] [--json]")
	fmt.Fprintln(w, "  juno-txbuild send-many --rpc-url <url> --rpc-user <user> --rpc-pass <pass> [--scan-url <url>] [--scan-bearer-token <token>] --wallet-id <id> --coin-type <n> --account <n> --outputs-file <path|-> --change-address <j*1..> [--exclude-note-id <txid:index>]... [--fee-multiplier <n>] [--fee-add-zat <zat>] [--min-change-zat <zat>] [--min-note-zat <zat>] [--extra-spends <n>] [--extra-spend-max-zat <zat>] [--minconf <n>] [--expiry-offset <n>] [--out <path>] [--json]")
	fmt.Fprintln(w, "  juno-txbuild sweep --rpc-url <url> --rpc-user <user> --rpc-pass <pass> [--scan-url <url>] [--scan-bearer-token <token>] --wallet-id <id> --coin-type <n> --account <n> --to <j*1..> [--change-address <j*1..>] [--exclude-note-id <txid:index>]... [--memo-hex <hex>] [--fee-multiplier <n>] [--fee-add-zat <zat>] [--min-note-zat <zat>] [--minconf <n>] [--expiry-offset <n>] [--out <path>] [--json]")
	fmt.Fprintln(w, "  juno-txbuild consolidate --rpc-url <url> --rpc-user <user> --rpc-pass <pass> [--scan-url <url>] [--scan-bearer-token <token>] --wallet-id <id> --coin-type <n> --account <n> --to <j*1..> [--change-address <j*1..>] [--exclude-note-id <txid:index>]... [--memo-hex <hex>] [--max-spends <2..200>] [--fee-multiplier <n>] [--fee-add-zat <zat>] [--min-note-zat <zat>] [--minconf <n>] [--expiry-offset <n>] [--out <path>] [--json]")
	fmt.Fprintln(w, "  juno-txbuild rebalance --rpc-url <url> --rpc-user <user> --rpc-pass <pass> [--scan-url <url>] [--scan-bearer-token <token>] --wallet-id <id> --coin-type <n> --account <n> --outputs-file <path|-> --change-address <j*1..> [--exclude-note-id <txid:index>]... [--fee-multiplier <n>] [--fee-add-zat <zat>] [--min-change-zat <zat>] [--min-note-zat <zat>] [--extra-spends <n>] [--extra-spend-max-zat <zat>] [--minconf <n>] [--expiry-offset <n>] [--out <path>] [--json]")
	fmt.Fprintln(w, "")
	fmt.Fprintf(w, "Defaults: --minconf %d, --fee-multiplier %d; signer limit: %d inputs and %d total outputs including change.\n", txbuild.DefaultMinConfirmations, txbuild.DefaultFeeMultiplier, txbuild.MaxOrchardSpendNotes, txbuild.MaxOrchardOutputs)
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "Env:")
	fmt.Fprintln(w, "  JUNO_RPC_URL, JUNO_RPC_USER, JUNO_RPC_PASS, JUNO_SCAN_URL, JUNO_SCAN_BEARER_TOKEN")
}

func runSend(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("send", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	var rpcURL string
	var rpcUser string
	var rpcPass string
	var scanURL string
	var scanBearerToken string

	var walletID string
	var coinType uint64
	var account uint64
	var excludedNoteIDs repeatedStringFlag
	var to string
	var amountZat string
	var memoHex string
	var changeAddr string
	var minconf int64
	var expiryOffset uint64
	var feeMultiplier uint64
	var feeAddZat uint64
	var minChangeZat uint64
	var minNoteZat uint64

	var outPath string
	var jsonOut bool

	fs.StringVar(&rpcURL, "rpc-url", "", "junocashd RPC URL")
	fs.StringVar(&rpcUser, "rpc-user", "", "junocashd RPC username")
	fs.StringVar(&rpcPass, "rpc-pass", "", "junocashd RPC password")
	fs.StringVar(&scanURL, "scan-url", "", "optional juno-scan base URL (http://host:port)")
	fs.StringVar(&scanBearerToken, "scan-bearer-token", "", "optional bearer token for juno-scan HTTP API (Authorization: Bearer ...)")

	fs.StringVar(&walletID, "wallet-id", "", "wallet id")
	fs.Uint64Var(&coinType, "coin-type", 0, "ZIP-32 coin type (0 = auto)")
	fs.Uint64Var(&account, "account", 0, "unified account id")
	fs.Var(&excludedNoteIDs, "exclude-note-id", "canonical note ID to exclude from selection (repeatable)")
	fs.StringVar(&to, "to", "", "destination unified address (j*1...)")
	fs.StringVar(&amountZat, "amount-zat", "", "amount to send in zatoshis")
	fs.StringVar(&memoHex, "memo-hex", "", "optional memo bytes (hex, <=512 bytes)")
	fs.StringVar(&changeAddr, "change-address", "", "change unified address (j*1...)")
	fs.Uint64Var(&feeMultiplier, "fee-multiplier", txbuild.DefaultFeeMultiplier, "multiplies the ZIP-317 base fee (>=1; default 20)")
	fs.Uint64Var(&feeAddZat, "fee-add-zat", 0, "adds zatoshis on top of the conventional fee")
	fs.Uint64Var(&minChangeZat, "min-change-zat", 0, "if change is in (0, min-change-zat), add it to fee and omit change output")
	fs.Uint64Var(&minNoteZat, "min-note-zat", 0, "skip spendable notes with value < min-note-zat")
	fs.Int64Var(&minconf, "minconf", txbuild.DefaultMinConfirmations, "minimum confirmations for spendable notes (default 100)")
	fs.Uint64Var(&expiryOffset, "expiry-offset", 40, "expiry height offset from next block height (chain tip + 1, min: 4)")

	fs.StringVar(&outPath, "out", "", "optional path to write TxPlan JSON")
	fs.BoolVar(&jsonOut, "json", false, "JSON output")

	if err := fs.Parse(args); err != nil {
		fmt.Fprintln(stderr, err.Error())
		return 2
	}
	coinTypeValue, accountValue, expiryOffsetValue, flagErr := plannerUint32Flags(coinType, account, expiryOffset)
	if flagErr != nil {
		return writeErr(stdout, stderr, jsonOut, types.ErrCodeInvalidRequest, flagErr.Error())
	}

	rpcURL, rpcUser, rpcPass, err := rpcConfigFromFlags(rpcURL, rpcUser, rpcPass)
	if err != nil {
		return writeErr(stdout, stderr, jsonOut, types.ErrCodeInvalidRequest, err.Error())
	}
	if strings.TrimSpace(scanURL) == "" {
		scanURL = os.Getenv("JUNO_SCAN_URL")
	}
	scanURL = strings.TrimSpace(scanURL)
	if strings.TrimSpace(scanBearerToken) == "" {
		scanBearerToken = os.Getenv("JUNO_SCAN_BEARER_TOKEN")
	}
	if strings.TrimSpace(scanBearerToken) == "" {
		scanBearerToken = os.Getenv("JUNO_SCAN_API_BEARER_TOKEN")
	}
	scanBearerToken = strings.TrimSpace(scanBearerToken)

	cfg := txbuild.SendConfig{
		RPCURL:  rpcURL,
		RPCUser: rpcUser,
		RPCPass: rpcPass,

		ScanURL:         scanURL,
		ScanBearerToken: scanBearerToken,

		WalletID: walletID,
		CoinType: coinTypeValue,
		Account:  accountValue,

		ExcludedNoteIDs: excludedNoteIDs,

		ToAddress:     to,
		AmountZat:     amountZat,
		MemoHex:       memoHex,
		ChangeAddress: changeAddr,

		MinConfirmations: minconf,
		ExpiryOffset:     expiryOffsetValue,
		MinNoteZat:       minNoteZat,

		FeeMultiplier: feeMultiplier,
		FeeAddZat:     feeAddZat,
		MinChangeZat:  minChangeZat,
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

	return writePlan(stdout, stderr, jsonOut, outPath, plan)
}

func runSweep(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("sweep", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	var rpcURL string
	var rpcUser string
	var rpcPass string
	var scanURL string
	var scanBearerToken string

	var walletID string
	var coinType uint64
	var account uint64
	var excludedNoteIDs repeatedStringFlag
	var to string
	var memoHex string
	var changeAddr string
	var minconf int64
	var expiryOffset uint64
	var feeMultiplier uint64
	var feeAddZat uint64
	var minNoteZat uint64

	var outPath string
	var jsonOut bool

	fs.StringVar(&rpcURL, "rpc-url", "", "junocashd RPC URL")
	fs.StringVar(&rpcUser, "rpc-user", "", "junocashd RPC username")
	fs.StringVar(&rpcPass, "rpc-pass", "", "junocashd RPC password")
	fs.StringVar(&scanURL, "scan-url", "", "optional juno-scan base URL (http://host:port)")
	fs.StringVar(&scanBearerToken, "scan-bearer-token", "", "optional bearer token for juno-scan HTTP API (Authorization: Bearer ...)")

	fs.StringVar(&walletID, "wallet-id", "", "wallet id")
	fs.Uint64Var(&coinType, "coin-type", 0, "ZIP-32 coin type (0 = auto)")
	fs.Uint64Var(&account, "account", 0, "unified account id")
	fs.Var(&excludedNoteIDs, "exclude-note-id", "canonical note ID to exclude from selection (repeatable)")
	fs.StringVar(&to, "to", "", "destination unified address (j*1...)")
	fs.StringVar(&memoHex, "memo-hex", "", "optional memo bytes (hex, <=512 bytes)")
	fs.StringVar(&changeAddr, "change-address", "", "change unified address (j*1...) (defaults to --to)")
	fs.Uint64Var(&feeMultiplier, "fee-multiplier", txbuild.DefaultFeeMultiplier, "multiplies the ZIP-317 base fee (>=1; default 20)")
	fs.Uint64Var(&feeAddZat, "fee-add-zat", 0, "adds zatoshis on top of the conventional fee")
	fs.Uint64Var(&minNoteZat, "min-note-zat", 0, "skip spendable notes with value < min-note-zat")
	fs.Int64Var(&minconf, "minconf", txbuild.DefaultMinConfirmations, "minimum confirmations for spendable notes (default 100)")
	fs.Uint64Var(&expiryOffset, "expiry-offset", 40, "expiry height offset from next block height (chain tip + 1, min: 4)")

	fs.StringVar(&outPath, "out", "", "optional path to write TxPlan JSON")
	fs.BoolVar(&jsonOut, "json", false, "JSON output")

	if err := fs.Parse(args); err != nil {
		fmt.Fprintln(stderr, err.Error())
		return 2
	}
	coinTypeValue, accountValue, expiryOffsetValue, flagErr := plannerUint32Flags(coinType, account, expiryOffset)
	if flagErr != nil {
		return writeErr(stdout, stderr, jsonOut, types.ErrCodeInvalidRequest, flagErr.Error())
	}

	rpcURL, rpcUser, rpcPass, err := rpcConfigFromFlags(rpcURL, rpcUser, rpcPass)
	if err != nil {
		return writeErr(stdout, stderr, jsonOut, types.ErrCodeInvalidRequest, err.Error())
	}
	if strings.TrimSpace(scanURL) == "" {
		scanURL = os.Getenv("JUNO_SCAN_URL")
	}
	scanURL = strings.TrimSpace(scanURL)
	if strings.TrimSpace(scanBearerToken) == "" {
		scanBearerToken = os.Getenv("JUNO_SCAN_BEARER_TOKEN")
	}
	if strings.TrimSpace(scanBearerToken) == "" {
		scanBearerToken = os.Getenv("JUNO_SCAN_API_BEARER_TOKEN")
	}
	scanBearerToken = strings.TrimSpace(scanBearerToken)

	cfg := txbuild.SweepConfig{
		RPCURL:  rpcURL,
		RPCUser: rpcUser,
		RPCPass: rpcPass,

		ScanURL:         scanURL,
		ScanBearerToken: scanBearerToken,

		WalletID: walletID,
		CoinType: coinTypeValue,
		Account:  accountValue,

		ExcludedNoteIDs: excludedNoteIDs,

		ToAddress:     to,
		MemoHex:       memoHex,
		ChangeAddress: changeAddr,

		MinConfirmations: minconf,
		ExpiryOffset:     expiryOffsetValue,
		MinNoteZat:       minNoteZat,

		FeeMultiplier: feeMultiplier,
		FeeAddZat:     feeAddZat,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	plan, err := txbuild.PlanSweep(ctx, cfg)
	if err != nil {
		var ce types.CodedError
		if errors.As(err, &ce) {
			return writeErr(stdout, stderr, jsonOut, ce.Code, ce.Message)
		}
		return writeErr(stdout, stderr, jsonOut, types.ErrCodeInvalidRequest, err.Error())
	}

	return writePlan(stdout, stderr, jsonOut, outPath, plan)
}

func runConsolidate(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("consolidate", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	var rpcURL string
	var rpcUser string
	var rpcPass string
	var scanURL string
	var scanBearerToken string

	var walletID string
	var coinType uint64
	var account uint64
	var excludedNoteIDs repeatedStringFlag
	var to string
	var memoHex string
	var changeAddr string
	var maxSpends int
	var minconf int64
	var expiryOffset uint64
	var feeMultiplier uint64
	var feeAddZat uint64
	var minNoteZat uint64

	var outPath string
	var jsonOut bool

	fs.StringVar(&rpcURL, "rpc-url", "", "junocashd RPC URL")
	fs.StringVar(&rpcUser, "rpc-user", "", "junocashd RPC username")
	fs.StringVar(&rpcPass, "rpc-pass", "", "junocashd RPC password")
	fs.StringVar(&scanURL, "scan-url", "", "optional juno-scan base URL (http://host:port)")
	fs.StringVar(&scanBearerToken, "scan-bearer-token", "", "optional bearer token for juno-scan HTTP API (Authorization: Bearer ...)")

	fs.StringVar(&walletID, "wallet-id", "", "wallet id")
	fs.Uint64Var(&coinType, "coin-type", 0, "ZIP-32 coin type (0 = auto)")
	fs.Uint64Var(&account, "account", 0, "unified account id")
	fs.Var(&excludedNoteIDs, "exclude-note-id", "canonical note ID to exclude from selection (repeatable)")
	fs.StringVar(&to, "to", "", "destination unified address (j*1...)")
	fs.StringVar(&memoHex, "memo-hex", "", "optional memo bytes (hex, <=512 bytes)")
	fs.StringVar(&changeAddr, "change-address", "", "change unified address (j*1...) (defaults to --to)")
	fs.IntVar(&maxSpends, "max-spends", 50, "max notes to consolidate into 1 output (2..200; default 50)")
	fs.Uint64Var(&feeMultiplier, "fee-multiplier", txbuild.DefaultFeeMultiplier, "multiplies the ZIP-317 base fee (>=1; default 20)")
	fs.Uint64Var(&feeAddZat, "fee-add-zat", 0, "adds zatoshis on top of the conventional fee")
	fs.Uint64Var(&minNoteZat, "min-note-zat", 0, "skip spendable notes with value < min-note-zat")
	fs.Int64Var(&minconf, "minconf", txbuild.DefaultMinConfirmations, "minimum confirmations for spendable notes (default 100)")
	fs.Uint64Var(&expiryOffset, "expiry-offset", 40, "expiry height offset from next block height (chain tip + 1, min: 4)")

	fs.StringVar(&outPath, "out", "", "optional path to write TxPlan JSON")
	fs.BoolVar(&jsonOut, "json", false, "JSON output")

	if err := fs.Parse(args); err != nil {
		fmt.Fprintln(stderr, err.Error())
		return 2
	}
	coinTypeValue, accountValue, expiryOffsetValue, flagErr := plannerUint32Flags(coinType, account, expiryOffset)
	if flagErr != nil {
		return writeErr(stdout, stderr, jsonOut, types.ErrCodeInvalidRequest, flagErr.Error())
	}
	if maxSpends < 2 || maxSpends > txbuild.MaxOrchardSpendNotes {
		return writeErr(stdout, stderr, jsonOut, types.ErrCodeInvalidRequest, fmt.Sprintf("max-spends must be between 2 and %d", txbuild.MaxOrchardSpendNotes))
	}

	rpcURL, rpcUser, rpcPass, err := rpcConfigFromFlags(rpcURL, rpcUser, rpcPass)
	if err != nil {
		return writeErr(stdout, stderr, jsonOut, types.ErrCodeInvalidRequest, err.Error())
	}
	if strings.TrimSpace(scanURL) == "" {
		scanURL = os.Getenv("JUNO_SCAN_URL")
	}
	scanURL = strings.TrimSpace(scanURL)
	if strings.TrimSpace(scanBearerToken) == "" {
		scanBearerToken = os.Getenv("JUNO_SCAN_BEARER_TOKEN")
	}
	if strings.TrimSpace(scanBearerToken) == "" {
		scanBearerToken = os.Getenv("JUNO_SCAN_API_BEARER_TOKEN")
	}
	scanBearerToken = strings.TrimSpace(scanBearerToken)

	cfg := txbuild.ConsolidateConfig{
		RPCURL:  rpcURL,
		RPCUser: rpcUser,
		RPCPass: rpcPass,

		ScanURL:         scanURL,
		ScanBearerToken: scanBearerToken,

		WalletID: walletID,
		CoinType: coinTypeValue,
		Account:  accountValue,

		ExcludedNoteIDs: excludedNoteIDs,

		ToAddress:     to,
		MemoHex:       memoHex,
		ChangeAddress: changeAddr,

		MaxSpends: maxSpends,

		MinConfirmations: minconf,
		ExpiryOffset:     expiryOffsetValue,
		MinNoteZat:       minNoteZat,

		FeeMultiplier: feeMultiplier,
		FeeAddZat:     feeAddZat,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	plan, err := txbuild.PlanConsolidate(ctx, cfg)
	if err != nil {
		var ce types.CodedError
		if errors.As(err, &ce) {
			return writeErr(stdout, stderr, jsonOut, ce.Code, ce.Message)
		}
		return writeErr(stdout, stderr, jsonOut, types.ErrCodeInvalidRequest, err.Error())
	}

	return writePlan(stdout, stderr, jsonOut, outPath, plan)
}

func runPlanOutputs(args []string, kind types.TxPlanKind, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet(string(kind), flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	var rpcURL string
	var rpcUser string
	var rpcPass string
	var scanURL string
	var scanBearerToken string

	var walletID string
	var coinType uint64
	var account uint64
	var excludedNoteIDs repeatedStringFlag
	var outputsFile string
	var changeAddr string
	var minconf int64
	var expiryOffset uint64
	var feeMultiplier uint64
	var feeAddZat uint64
	var minChangeZat uint64
	var minNoteZat uint64
	var extraSpends int
	var extraSpendMaxZat uint64

	var outPath string
	var jsonOut bool

	fs.StringVar(&rpcURL, "rpc-url", "", "junocashd RPC URL")
	fs.StringVar(&rpcUser, "rpc-user", "", "junocashd RPC username")
	fs.StringVar(&rpcPass, "rpc-pass", "", "junocashd RPC password")
	fs.StringVar(&scanURL, "scan-url", "", "optional juno-scan base URL (http://host:port)")
	fs.StringVar(&scanBearerToken, "scan-bearer-token", "", "optional bearer token for juno-scan HTTP API (Authorization: Bearer ...)")

	fs.StringVar(&walletID, "wallet-id", "", "wallet id")
	fs.Uint64Var(&coinType, "coin-type", 0, "ZIP-32 coin type (0 = auto)")
	fs.Uint64Var(&account, "account", 0, "unified account id")
	fs.Var(&excludedNoteIDs, "exclude-note-id", "canonical note ID to exclude from selection (repeatable)")
	fs.StringVar(&outputsFile, "outputs-file", "", "path to JSON array of TxOutputs (or - for stdin)")
	fs.IntVar(&extraSpends, "extra-spends", 0, "add up to n extra small notes on top of the selection (0 = off)")
	fs.Uint64Var(&extraSpendMaxZat, "extra-spend-max-zat", 0, "only add extra notes with value <= this (0 = no cap)")
	fs.StringVar(&changeAddr, "change-address", "", "change unified address (j*1...)")
	fs.Uint64Var(&feeMultiplier, "fee-multiplier", txbuild.DefaultFeeMultiplier, "multiplies the ZIP-317 base fee (>=1; default 20)")
	fs.Uint64Var(&feeAddZat, "fee-add-zat", 0, "adds zatoshis on top of the conventional fee")
	fs.Uint64Var(&minChangeZat, "min-change-zat", 0, "if change is in (0, min-change-zat), add it to fee and omit change output")
	fs.Uint64Var(&minNoteZat, "min-note-zat", 0, "skip spendable notes with value < min-note-zat")
	fs.Int64Var(&minconf, "minconf", txbuild.DefaultMinConfirmations, "minimum confirmations for spendable notes (default 100)")
	fs.Uint64Var(&expiryOffset, "expiry-offset", 40, "expiry height offset from next block height (chain tip + 1, min: 4)")

	fs.StringVar(&outPath, "out", "", "optional path to write TxPlan JSON")
	fs.BoolVar(&jsonOut, "json", false, "JSON output")

	if err := fs.Parse(args); err != nil {
		fmt.Fprintln(stderr, err.Error())
		return 2
	}
	coinTypeValue, accountValue, expiryOffsetValue, flagErr := plannerUint32Flags(coinType, account, expiryOffset)
	if flagErr != nil {
		return writeErr(stdout, stderr, jsonOut, types.ErrCodeInvalidRequest, flagErr.Error())
	}
	if extraSpends < 0 || extraSpends > txbuild.MaxExtraSpends {
		return writeErr(stdout, stderr, jsonOut, types.ErrCodeInvalidRequest, fmt.Sprintf("extra-spends must be between 0 and %d", txbuild.MaxExtraSpends))
	}

	outputsFile = strings.TrimSpace(outputsFile)
	if outputsFile == "" {
		return writeErr(stdout, stderr, jsonOut, types.ErrCodeInvalidRequest, "outputs-file is required")
	}

	outs, err := loadOutputs(outputsFile)
	if err != nil {
		return writeErr(stdout, stderr, jsonOut, types.ErrCodeInvalidRequest, err.Error())
	}
	if len(outs) > txbuild.MaxOrchardOutputs {
		return writeErr(stdout, stderr, jsonOut, types.ErrCodeInvalidRequest, fmt.Sprintf("outputs file must contain at most %d entries", txbuild.MaxOrchardOutputs))
	}

	rpcURL, rpcUser, rpcPass, err = rpcConfigFromFlags(rpcURL, rpcUser, rpcPass)
	if err != nil {
		return writeErr(stdout, stderr, jsonOut, types.ErrCodeInvalidRequest, err.Error())
	}
	if strings.TrimSpace(scanURL) == "" {
		scanURL = os.Getenv("JUNO_SCAN_URL")
	}
	scanURL = strings.TrimSpace(scanURL)
	if strings.TrimSpace(scanBearerToken) == "" {
		scanBearerToken = os.Getenv("JUNO_SCAN_BEARER_TOKEN")
	}
	if strings.TrimSpace(scanBearerToken) == "" {
		scanBearerToken = os.Getenv("JUNO_SCAN_API_BEARER_TOKEN")
	}
	scanBearerToken = strings.TrimSpace(scanBearerToken)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	plan, report, err := txbuild.PlanWithReport(ctx, txbuild.PlanConfig{
		RPCURL:  rpcURL,
		RPCUser: rpcUser,
		RPCPass: rpcPass,

		ScanURL:         scanURL,
		ScanBearerToken: scanBearerToken,

		WalletID: walletID,
		CoinType: coinTypeValue,
		Account:  accountValue,

		ExcludedNoteIDs: excludedNoteIDs,

		Kind:          kind,
		Outputs:       outs,
		ChangeAddress: changeAddr,

		MinConfirmations: minconf,
		ExpiryOffset:     expiryOffsetValue,
		MinNoteZat:       minNoteZat,

		FeeMultiplier: feeMultiplier,
		FeeAddZat:     feeAddZat,
		MinChangeZat:  minChangeZat,

		ExtraSpends:      extraSpends,
		ExtraSpendMaxZat: extraSpendMaxZat,
	})
	if err != nil {
		var ce types.CodedError
		if errors.As(err, &ce) {
			return writeErr(stdout, stderr, jsonOut, ce.Code, ce.Message)
		}
		return writeErr(stdout, stderr, jsonOut, types.ErrCodeInvalidRequest, err.Error())
	}

	var selection *selectionReport
	if extraSpends > 0 {
		selection = &selectionReport{
			ExtraSpends:   report.ExtraSpends,
			ExtraSpendZat: strconv.FormatUint(report.ExtraSpendZat, 10),
		}
	}
	return writePlanWithSelection(stdout, stderr, jsonOut, outPath, plan, selection)
}

// selectionReport is added to the --json envelope when extra spends were
// requested. It sits next to "data" so the TxPlan itself stays unchanged.
type selectionReport struct {
	ExtraSpends   int    `json:"extra_spends"`
	ExtraSpendZat string `json:"extra_spend_zat"`
}

func loadOutputs(path string) ([]types.TxOutput, error) {
	var r io.Reader
	if path == "-" {
		r = os.Stdin
	} else {
		f, err := os.Open(path)
		if err != nil {
			return nil, fmt.Errorf("open outputs file: %w", err)
		}
		defer f.Close()
		r = f
	}

	var outs []types.TxOutput
	dec := json.NewDecoder(r)
	if err := dec.Decode(&outs); err != nil {
		return nil, errors.New("invalid outputs json")
	}
	return outs, nil
}

func writePlan(stdout, stderr io.Writer, jsonOut bool, outPath string, plan types.TxPlan) int {
	return writePlanWithSelection(stdout, stderr, jsonOut, outPath, plan, nil)
}

func writePlanWithSelection(stdout, stderr io.Writer, jsonOut bool, outPath string, plan types.TxPlan, selection *selectionReport) int {
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
		env := map[string]any{
			"version": jsonVersionV1,
			"status":  "ok",
			"data":    plan,
		}
		if selection != nil {
			env["selection"] = selection
		}
		_ = json.NewEncoder(stdout).Encode(env)
		return 0
	}
	if selection != nil {
		fmt.Fprintf(stderr, "extra_spends=%d extra_spend_zat=%s\n", selection.ExtraSpends, selection.ExtraSpendZat)
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

func plannerUint32Flags(coinType, account, expiryOffset uint64) (uint32, uint32, uint32, error) {
	const maxUint32 = uint64(^uint32(0))
	if coinType > maxUint32 {
		return 0, 0, 0, errors.New("coin-type must fit uint32")
	}
	if account >= 1<<31 {
		return 0, 0, 0, errors.New("account must be below 2147483648")
	}
	if expiryOffset > maxUint32 {
		return 0, 0, 0, errors.New("expiry-offset must fit uint32")
	}
	return uint32(coinType), uint32(account), uint32(expiryOffset), nil
}

func writeErr(stdout, stderr io.Writer, jsonOut bool, code types.ErrorCode, msg string) int {
	if jsonOut {
		_ = json.NewEncoder(stdout).Encode(map[string]any{
			"version": jsonVersionV1,
			"status":  "err",
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
