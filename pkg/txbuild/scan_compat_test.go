package txbuild

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Abdullah1738/juno-sdk-go/junocashd"
	"github.com/Abdullah1738/juno-sdk-go/junoscan"
	"github.com/Abdullah1738/juno-sdk-go/types"
	"github.com/Abdullah1738/juno-txbuild/internal/chain"
)

// The fixtures below are copied from juno-scan v1.4.7-mainnet responses. That
// release does not report event_epoch or ready in /v1/health, serializes
// memo_hex and note_nullifier explicitly and answers auth failures with a plain
// text 401.
const (
	v147AnchorHeight = int64(200)
	v147AnchorHash   = "0b5c3a0c1f0f7f0a8f4d8f7d0a0a8e4b7a6c2e9b1d3f5a7c9e1b3d5f7a9c1e3b"
)

var v147HealthBody = `{"scanned_hash":"` + v147AnchorHash + `","scanned_height":200,"status":"ok"}` + "\n"

func v147NotesBody(t *testing.T) string {
	t.Helper()
	note := func(txid string, actionIndex int32, height, position, value int64, extra string) string {
		b, err := json.Marshal(map[string]any{
			"direction":         "incoming",
			"txid":              txid,
			"action_index":      actionIndex,
			"height":            height,
			"position":          position,
			"recipient_address": "jregtest1recipient",
			"value_zat":         value,
			"memo_hex":          nil,
			"note_nullifier":    strings.Repeat("ab", 32),
			"recipient_scope":   "external",
			"created_at":        "2026-07-31T10:11:12.123456Z",
		})
		if err != nil {
			t.Fatalf("marshal note: %v", err)
		}
		if extra == "" {
			return string(b)
		}
		return strings.TrimSuffix(string(b), "}") + "," + extra + "}"
	}
	notes := []string{
		note(strings.Repeat("a", 64), 0, 100, 1, 20_000_000, ""),
		note(strings.Repeat("b", 64), 1, 110, 2, 300_000, ""),
		note(strings.Repeat("c", 64), 0, 120, 3, 400_000, ""),
		note(strings.Repeat("d", 64), 0, 130, 4, 500_000, `"pending_spent_txid":"`+strings.Repeat("9", 64)+`","pending_spent_at":"2026-07-31T10:12:00Z","pending_spent_expiry_height":240`),
	}
	return `{"notes":[` + strings.Join(notes, ",") + "]}\n"
}

type v147Scanner struct {
	url         string
	healthCalls int
}

func startV147Scanner(t *testing.T, bearerToken string) *v147Scanner {
	t.Helper()
	s := &v147Scanner{}
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/health", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		s.healthCalls++
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_, _ = w.Write([]byte(v147HealthBody))
	})
	mux.HandleFunc("/v1/wallets/hot/notes", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("spent") != "false" {
			http.Error(w, "unexpected spent filter", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_, _ = w.Write([]byte(v147NotesBody(t)))
	})
	mux.HandleFunc("/v1/orchard/witness", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			AnchorHeight *int64  `json:"anchor_height,omitempty"`
			Positions    []int64 `json:"positions"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid json", http.StatusBadRequest)
			return
		}
		if req.AnchorHeight == nil || *req.AnchorHeight != v147AnchorHeight {
			http.Error(w, "unexpected anchor", http.StatusBadRequest)
			return
		}
		paths := make([]map[string]any, 0, len(req.Positions))
		for _, p := range req.Positions {
			paths = append(paths, map[string]any{"position": p, "auth_path": testAuthPath()})
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status":        "ok",
			"anchor_height": v147AnchorHeight,
			"root":          strings.Repeat("cd", 32),
			"paths":         paths,
		})
	})

	var handler http.Handler = mux
	if bearerToken != "" {
		handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("Authorization") != "Bearer "+bearerToken {
				w.Header().Set("WWW-Authenticate", "Bearer")
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			mux.ServeHTTP(w, r)
		})
	}
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	s.url = srv.URL
	return s
}

func startV147Node(t *testing.T) *junocashd.Client {
	t.Helper()
	heights := map[string]int64{}
	for i, id := range []string{"a", "b", "c", "d"} {
		heights[strings.Repeat(id, 64)] = int64(100 + 10*i)
	}
	node := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			ID     any               `json:"id"`
			Method string            `json:"method"`
			Params []json.RawMessage `json:"params"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			http.Error(w, "invalid json", http.StatusBadRequest)
			return
		}
		var result any
		switch request.Method {
		case "getblockhash":
			var height int64
			_ = json.Unmarshal(request.Params[0], &height)
			if height == v147AnchorHeight {
				result = v147AnchorHash
			} else {
				result = "block-" + jsonInt(height)
			}
		case "getblock":
			var hash string
			_ = json.Unmarshal(request.Params[0], &hash)
			var txs []map[string]any
			for txid, height := range heights {
				if "block-"+jsonInt(height) == hash {
					txs = append(txs, testOrchardBlock(txid)["tx"].([]map[string]any)...)
				}
			}
			result = map[string]any{"tx": txs}
		case "getblockchaininfo":
			result = map[string]any{
				"chain":     "regtest",
				"blocks":    v147AnchorHeight,
				"consensus": map[string]any{"nextblock": "c8e71055"},
			}
		default:
			http.Error(w, "unexpected method", http.StatusBadRequest)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"result": result, "error": nil, "id": request.ID})
	}))
	t.Cleanup(node.Close)
	return junocashd.New(node.URL, "", "")
}

var v147ChainInfo = chain.ChainInfo{Chain: "regtest", Height: v147AnchorHeight, BranchID: 0xc8e71055}

func TestScannerHealthAcceptsV147Response(t *testing.T) {
	scanner := startV147Scanner(t, "")
	sc, health, err := newScanClient(scanner.url, "")
	if err != nil {
		t.Fatalf("newScanClient: %v", err)
	}

	// The SDK client alone rejects this scanner, which is what broke v1.7.0.
	if _, err := sc.Health(context.Background()); err == nil || !strings.Contains(err.Error(), "event_epoch") {
		t.Fatalf("sdk health error=%v, want event_epoch rejection", err)
	}

	got, err := health.Health(context.Background())
	if err != nil {
		t.Fatalf("Health: %v", err)
	}
	if got.Status != "ok" || got.ScannedHeight == nil || *got.ScannedHeight != v147AnchorHeight || got.ScannedHash == nil || *got.ScannedHash != v147AnchorHash {
		t.Fatalf("health=%+v", got)
	}
}

func TestScannerHealthIgnoresEventEpoch(t *testing.T) {
	for _, epoch := range []string{`"` + strings.Repeat("e", 64) + `"`, `""`, `"NOT-HEX"`, `null`} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(`{"status":"ok","ready":true,"event_epoch":` + epoch + `,"scanned_height":7,"scanned_hash":"h"}`))
		}))
		health, err := newScannerHealthClient(srv.URL+"/", nil)
		if err != nil {
			srv.Close()
			t.Fatalf("newScannerHealthClient: %v", err)
		}
		got, err := health.Health(context.Background())
		srv.Close()
		if err != nil {
			t.Fatalf("epoch %s: %v", epoch, err)
		}
		if got.Status != "ok" || *got.ScannedHeight != 7 || *got.ScannedHash != "h" {
			t.Fatalf("epoch %s: health=%+v", epoch, got)
		}
	}
}

func TestScannerHealthErrors(t *testing.T) {
	tests := []struct {
		name       string
		status     int
		body       string
		wantHTTP   bool
		wantCode   string
		wantMsg    string
		wantSubstr string
	}{
		{name: "plain text 401", status: http.StatusUnauthorized, body: "unauthorized\n", wantHTTP: true, wantSubstr: "junoscan: http 401: unauthorized"},
		{name: "db error", status: http.StatusInternalServerError, body: "db error\n", wantHTTP: true, wantSubstr: "junoscan: http 500: db error"},
		{name: "error envelope", status: http.StatusServiceUnavailable, body: `{"error":{"code":"not_ready","message":"catching up","retryable":true}}`, wantHTTP: true, wantCode: "not_ready", wantMsg: "catching up"},
		{name: "flat error", status: http.StatusBadRequest, body: `{"code":"bad","message":"nope"}`, wantHTTP: true, wantCode: "bad", wantMsg: "nope"},
		{name: "invalid json", status: http.StatusOK, body: "not json", wantSubstr: "junoscan: invalid json response"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Accept") != "application/json" {
					t.Errorf("accept=%q", r.Header.Get("Accept"))
				}
				w.WriteHeader(tt.status)
				_, _ = w.Write([]byte(tt.body))
			}))
			defer srv.Close()
			health, err := newScannerHealthClient(srv.URL, nil)
			if err != nil {
				t.Fatalf("newScannerHealthClient: %v", err)
			}
			_, err = health.Health(context.Background())
			if err == nil {
				t.Fatal("expected error")
			}
			var httpErr *junoscan.HTTPError
			if errors.As(err, &httpErr) != tt.wantHTTP {
				t.Fatalf("HTTPError=%v want %v (err=%v)", httpErr != nil, tt.wantHTTP, err)
			}
			if tt.wantHTTP {
				if httpErr.StatusCode != tt.status || httpErr.Code != tt.wantCode || httpErr.Message != tt.wantMsg {
					t.Fatalf("http error=%+v", httpErr)
				}
				if tt.status == http.StatusServiceUnavailable && (!httpErr.Retryable || !httpErr.Temporary()) {
					t.Fatalf("retryable lost: %+v", httpErr)
				}
			}
			if tt.wantSubstr != "" && !strings.Contains(err.Error(), tt.wantSubstr) {
				t.Fatalf("error=%q want %q", err, tt.wantSubstr)
			}
		})
	}

	if _, err := newScannerHealthClient("not a url", nil); err == nil {
		t.Fatal("expected invalid base url error")
	}
}

func TestPlanWithScanAgainstV147Scanner(t *testing.T) {
	rpc := startV147Node(t)
	outputs := []types.TxOutput{{ToAddress: "dest", AmountZat: "5000000"}}
	for _, token := range []string{"", "secret"} {
		scanner := startV147Scanner(t, token)
		cfg := PlanConfig{
			ScanURL:          scanner.url,
			ScanBearerToken:  token,
			WalletID:         "hot",
			Kind:             types.TxPlanKindWithdrawal,
			Outputs:          outputs,
			ChangeAddress:    "change",
			MinConfirmations: 1,
			ExpiryOffset:     40,
			FeeMultiplier:    DefaultFeeMultiplier,
		}

		var report PlanReport
		plan, err := planWithScan(context.Background(), rpc, v147ChainInfo, regtestCoinType, cfg, 5_000_000, nil, &report)
		if err != nil {
			t.Fatalf("token=%q planWithScan: %v", token, err)
		}
		if len(plan.Notes) != 1 || plan.Notes[0].NoteID != strings.Repeat("a", 64)+":0" || plan.FeeZat != "200000" {
			t.Fatalf("token=%q base plan notes=%+v fee=%s", token, plan.Notes, plan.FeeZat)
		}
		if scanner.healthCalls != 2 {
			t.Fatalf("token=%q health calls=%d want 2", token, scanner.healthCalls)
		}

		cfg.ExtraSpends = 4
		plan, err = planWithScan(context.Background(), rpc, v147ChainInfo, regtestCoinType, cfg, 5_000_000, nil, &report)
		if err != nil {
			t.Fatalf("token=%q planWithScan extra spends: %v", token, err)
		}
		var ids []string
		for _, n := range plan.Notes {
			ids = append(ids, n.NoteID)
		}
		want := []string{strings.Repeat("a", 64) + ":0", strings.Repeat("b", 64) + ":1", strings.Repeat("c", 64) + ":0"}
		if strings.Join(ids, ",") != strings.Join(want, ",") {
			t.Fatalf("token=%q notes=%v want %v (pending note must stay out)", token, ids, want)
		}
		if report.ExtraSpends != 2 || report.ExtraSpendZat != 700_000 || plan.FeeZat != "300000" {
			t.Fatalf("token=%q report=%+v fee=%s", token, report, plan.FeeZat)
		}
	}
}

func TestPlanWithScanV147WrongBearerTokenIsHTTPError(t *testing.T) {
	rpc := startV147Node(t)
	scanner := startV147Scanner(t, "secret")
	cfg := PlanConfig{
		ScanURL:          scanner.url,
		ScanBearerToken:  "wrong",
		WalletID:         "hot",
		Kind:             types.TxPlanKindWithdrawal,
		Outputs:          []types.TxOutput{{ToAddress: "dest", AmountZat: "5000000"}},
		ChangeAddress:    "change",
		MinConfirmations: 1,
		ExpiryOffset:     40,
	}
	_, err := planWithScan(context.Background(), rpc, v147ChainInfo, regtestCoinType, cfg, 5_000_000, nil, &PlanReport{})
	var httpErr *junoscan.HTTPError
	if !errors.As(err, &httpErr) || httpErr.StatusCode != http.StatusUnauthorized {
		t.Fatalf("error=%v want junoscan 401", err)
	}
}

func TestSweepAndConsolidateWithScanAgainstV147Scanner(t *testing.T) {
	rpc := startV147Node(t)
	scanner := startV147Scanner(t, "")

	sweep, err := planSweepWithScan(context.Background(), rpc, v147ChainInfo, regtestCoinType, SweepConfig{
		ScanURL:          scanner.url,
		WalletID:         "hot",
		ToAddress:        "dest",
		ChangeAddress:    "change",
		MinConfirmations: 1,
		ExpiryOffset:     40,
		FeeMultiplier:    DefaultFeeMultiplier,
	}, nil)
	if err != nil {
		t.Fatalf("planSweepWithScan: %v", err)
	}
	if len(sweep.Notes) != 3 {
		t.Fatalf("sweep notes=%d want 3", len(sweep.Notes))
	}

	consolidate, err := planConsolidateWithScan(context.Background(), rpc, v147ChainInfo, regtestCoinType, ConsolidateConfig{
		ScanURL:          scanner.url,
		WalletID:         "hot",
		ToAddress:        "dest",
		ChangeAddress:    "change",
		MaxSpends:        2,
		MinConfirmations: 1,
		ExpiryOffset:     40,
		FeeMultiplier:    DefaultFeeMultiplier,
	}, nil)
	if err != nil {
		t.Fatalf("planConsolidateWithScan: %v", err)
	}
	if len(consolidate.Notes) != 2 {
		t.Fatalf("consolidate notes=%d want 2", len(consolidate.Notes))
	}
}
