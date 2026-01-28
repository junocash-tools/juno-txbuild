package app

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Abdullah1738/juno-sdk-go/junocashd"
	"github.com/Abdullah1738/juno-txbuild/internal/logic"
	"github.com/Abdullah1738/juno-txbuild/internal/witness"
)

type scanFixture struct {
	chainHeight int64
	cmxHex      []string
	byOutpoint  map[string]struct {
		height   int64
		position int64
	}
}

func startScanStub(t *testing.T, ctx context.Context, rpc *junocashd.Client, bearerToken string) *httptest.Server {
	t.Helper()

	if ctx == nil {
		ctx = context.Background()
	}
	if rpc == nil {
		t.Fatalf("scan stub: rpc is nil")
	}

	fx, err := buildScanFixture(ctx, rpc)
	if err != nil {
		t.Fatalf("scan stub: build fixture: %v", err)
	}

	bearerToken = strings.TrimSpace(bearerToken)

	mux := http.NewServeMux()
	mux.HandleFunc("/v1/wallets/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if bearerToken != "" && !validBearerToken(r, bearerToken) {
			w.Header().Set("WWW-Authenticate", "Bearer")
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}

		path := strings.TrimPrefix(r.URL.Path, "/v1/wallets/")
		parts := strings.Split(path, "/")
		if len(parts) < 2 || parts[1] != "notes" {
			http.NotFound(w, r)
			return
		}

		var raw []struct {
			TxID      string      `json:"txid"`
			Pool      string      `json:"pool"`
			OutIndex  uint32      `json:"outindex"`
			Spendable bool        `json:"spendable"`
			Amount    json.Number `json:"amount"`
		}
		if err := rpc.Call(ctx, "z_listunspent", []any{int64(1), int64(9999999), true}, &raw); err != nil {
			http.Error(w, "rpc error", http.StatusInternalServerError)
			return
		}

		type note struct {
			TxID          string `json:"txid"`
			ActionIndex   uint32 `json:"action_index"`
			Height        int64  `json:"height"`
			Position      int64  `json:"position"`
			RecipientAddr string `json:"recipient_address"`
			ValueZat      int64  `json:"value_zat"`
			NoteNullifier string `json:"note_nullifier"`
			CreatedAt     string `json:"created_at"`
		}

		out := make([]note, 0, len(raw))
		now := time.Now().UTC().Format(time.RFC3339Nano)
		for _, n := range raw {
			if strings.ToLower(strings.TrimSpace(n.Pool)) != "orchard" || !n.Spendable {
				continue
			}
			txid := strings.ToLower(strings.TrimSpace(n.TxID))
			key := txid + ":" + strconv.FormatUint(uint64(n.OutIndex), 10)
			meta, ok := fx.byOutpoint[key]
			if !ok {
				continue
			}
			v, err := logic.ParseZECToZat(n.Amount.String())
			if err != nil || v == 0 || v > uint64(^uint64(0)>>1) {
				continue
			}
			out = append(out, note{
				TxID:          txid,
				ActionIndex:   n.OutIndex,
				Height:        meta.height,
				Position:      meta.position,
				RecipientAddr: "",
				ValueZat:      int64(v),
				NoteNullifier: strings.Repeat("0", 64),
				CreatedAt:     now,
			})
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"notes": out})
	})

	mux.HandleFunc("/v1/orchard/witness", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if bearerToken != "" && !validBearerToken(r, bearerToken) {
			w.Header().Set("WWW-Authenticate", "Bearer")
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}

		var req struct {
			Positions []uint32 `json:"positions"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid json", http.StatusBadRequest)
			return
		}
		if len(req.Positions) == 0 {
			http.Error(w, "positions required", http.StatusBadRequest)
			return
		}

		wit, err := witness.OrchardWitness(fx.cmxHex, req.Positions)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		type path struct {
			Position uint32   `json:"position"`
			AuthPath []string `json:"auth_path"`
		}
		paths := make([]path, 0, len(wit.Paths))
		for _, p := range wit.Paths {
			paths = append(paths, path{Position: p.Position, AuthPath: p.AuthPath})
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status":        "ok",
			"anchor_height": fx.chainHeight,
			"root":          wit.Root,
			"paths":         paths,
		})
	})

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func validBearerToken(r *http.Request, expected string) bool {
	expected = strings.TrimSpace(expected)
	if expected == "" {
		return true
	}
	h := strings.TrimSpace(r.Header.Get("Authorization"))
	parts := strings.Fields(h)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "bearer") {
		return false
	}
	return parts[1] == expected
}

func buildScanFixture(ctx context.Context, rpc *junocashd.Client) (scanFixture, error) {
	if rpc == nil {
		return scanFixture{}, errors.New("rpc is nil")
	}

	chainHeight, err := rpc.GetBlockCount(ctx)
	if err != nil {
		return scanFixture{}, err
	}

	type blockV2 struct {
		Tx []struct {
			TxID    string `json:"txid"`
			Orchard struct {
				Actions []struct {
					CMX string `json:"cmx"`
				} `json:"actions"`
			} `json:"orchard"`
		} `json:"tx"`
	}

	fx := scanFixture{
		chainHeight: chainHeight,
		cmxHex:      nil,
		byOutpoint:  make(map[string]struct{ height, position int64 }),
	}

	var pos int64
	for height := int64(0); height <= chainHeight; height++ {
		hash, err := rpc.GetBlockHash(ctx, height)
		if err != nil {
			return scanFixture{}, err
		}
		var blk blockV2
		if err := rpc.Call(ctx, "getblock", []any{hash, 2}, &blk); err != nil {
			return scanFixture{}, err
		}
		for _, t := range blk.Tx {
			txid := strings.ToLower(strings.TrimSpace(t.TxID))
			for i, a := range t.Orchard.Actions {
				cmx := strings.ToLower(strings.TrimSpace(a.CMX))
				if len(cmx) != 64 {
					continue
				}
				fx.cmxHex = append(fx.cmxHex, cmx)
				key := txid + ":" + strconv.Itoa(i)
				fx.byOutpoint[key] = struct {
					height   int64
					position int64
				}{height: height, position: pos}
				pos++
			}
		}
	}

	if len(fx.cmxHex) == 0 {
		return scanFixture{}, errors.New("no orchard commitments")
	}
	return fx, nil
}
