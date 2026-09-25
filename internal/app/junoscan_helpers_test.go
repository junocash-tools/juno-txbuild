//go:build integration || e2e

package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/Abdullah1738/juno-sdk-go/junocashd"
	"github.com/Abdullah1738/juno-txbuild/internal/testutil/containers"
)

// startReleasedJunoScan runs the pinned juno-scan release (v1.4.7-mainnet)
// against jd. Its /v1/health has no event_epoch.
func startReleasedJunoScan(t *testing.T, jd *containers.Junocashd, bearerToken string) *containers.JunoScan {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	sc, err := containers.StartJunoScan(ctx, jd, containers.JunoScanConfig{BearerToken: bearerToken})
	if err != nil {
		t.Fatalf("start juno-scan: %v", err)
	}
	t.Cleanup(func() {
		termCtx, termCancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer termCancel()
		_ = sc.Terminate(termCtx)
	})
	return sc
}

func exportViewingKey(t *testing.T, jd *containers.Junocashd, addr string) string {
	t.Helper()
	raw, err := jd.ExecCLI(context.Background(), "z_exportviewingkey", addr)
	if err != nil {
		t.Fatalf("z_exportviewingkey: %v", err)
	}
	ufvk := strings.TrimSpace(string(raw))
	if ufvk == "" {
		t.Fatalf("z_exportviewingkey: empty ufvk")
	}
	return ufvk
}

func scanRequest(t *testing.T, ctx context.Context, method, rawURL, bearerToken string, body any) (int, []byte) {
	t.Helper()
	var r io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal scanner request: %v", err)
		}
		r = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, rawURL, r)
	if err != nil {
		t.Fatalf("build scanner request: %v", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if bearerToken != "" {
		req.Header.Set("Authorization", "Bearer "+bearerToken)
	}
	resp, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
	if err != nil {
		return 0, nil
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	return resp.StatusCode, raw
}

func registerScanWallet(t *testing.T, sc *containers.JunoScan, bearerToken, walletID, ufvk string) {
	t.Helper()
	status, raw := scanRequest(t, context.Background(), http.MethodPost, sc.URL+"/v1/wallets", bearerToken, map[string]string{
		"wallet_id": walletID,
		"ufvk":      ufvk,
	})
	if status != http.StatusOK {
		t.Fatalf("POST /v1/wallets status=%d body=%s", status, strings.TrimSpace(string(raw)))
	}
}

// waitScanCaughtUp waits until the scanner is at the node tip and lists at
// least wantNotes unspent notes for walletID.
func waitScanCaughtUp(t *testing.T, sc *containers.JunoScan, rpc *junocashd.Client, bearerToken, walletID string, wantNotes int) []byte {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	var lastHealth, lastNotes []byte
	for {
		tip, err := rpc.GetBlockCount(ctx)
		if err == nil {
			hash, hashErr := rpc.GetBlockHash(ctx, tip)
			status, health := scanRequest(t, ctx, http.MethodGet, sc.URL+"/v1/health", bearerToken, nil)
			lastHealth = health
			var h struct {
				ScannedHeight *int64  `json:"scanned_height"`
				ScannedHash   *string `json:"scanned_hash"`
			}
			if hashErr == nil && status == http.StatusOK && json.Unmarshal(health, &h) == nil &&
				h.ScannedHeight != nil && *h.ScannedHeight == tip && h.ScannedHash != nil && *h.ScannedHash == hash {
				notesURL := fmt.Sprintf("%s/v1/wallets/%s/notes?spent=false&limit=1000", sc.URL, url.PathEscape(walletID))
				status, notes := scanRequest(t, ctx, http.MethodGet, notesURL, bearerToken, nil)
				lastNotes = notes
				var page struct {
					Notes []json.RawMessage `json:"notes"`
				}
				if status == http.StatusOK && json.Unmarshal(notes, &page) == nil && len(page.Notes) >= wantNotes {
					return health
				}
			}
		}
		select {
		case <-ctx.Done():
			t.Fatalf("juno-scan did not catch up: health=%s notes=%s", lastHealth, lastNotes)
		case <-time.After(250 * time.Millisecond):
		}
	}
}

type releasedScanFixture struct {
	jd          *containers.Junocashd
	rpc         *junocashd.Client
	scan        *containers.JunoScan
	bearerToken string
	walletID    string
	changeAddr  string
	toAddr      string
	// before is the account 0 spendable note count before the small notes.
	before   int
	smallIDs []string
}

// setupReleasedScanWallet funds account 0 with one shielded note plus six
// 0.005 JUNO notes and waits for the released juno-scan to index them.
func setupReleasedScanWallet(t *testing.T, bearerToken string, nodeArgs ...string) releasedScanFixture {
	t.Helper()
	// juno-scan reads mempool transactions and recommends a tx index.
	jd, rpc := startJunocashd(t, append([]string{"-txindex=1"}, nodeArgs...)...)

	changeAddr := unifiedAddress(t, jd, 0)
	scan := startReleasedJunoScan(t, jd, bearerToken)
	const walletID = "test-wallet"
	registerScanWallet(t, scan, bearerToken, walletID, exportViewingKey(t, jd, changeAddr))

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
	sortNoteIDsByActionIndex(t, smallIDs)

	health := waitScanCaughtUp(t, scan, rpc, bearerToken, walletID, before+6)
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(health, &fields); err != nil {
		t.Fatalf("decode scanner health: %v", err)
	}
	if _, ok := fields["event_epoch"]; ok {
		t.Fatalf("juno-scan %s unexpectedly reports event_epoch: %s", scan.Version, health)
	}

	return releasedScanFixture{
		jd:          jd,
		rpc:         rpc,
		scan:        scan,
		bearerToken: bearerToken,
		walletID:    walletID,
		changeAddr:  changeAddr,
		toAddr:      toAddr,
		before:      before,
		smallIDs:    smallIDs,
	}
}
