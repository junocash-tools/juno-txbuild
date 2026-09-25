package txbuild

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Abdullah1738/juno-sdk-go/junocashd"
	"github.com/Abdullah1738/juno-sdk-go/junoscan"
	"github.com/Abdullah1738/juno-sdk-go/types"
	"github.com/Abdullah1738/juno-txbuild/internal/chain"
	"github.com/Abdullah1738/juno-txbuild/internal/logic"
)

func TestScanBackedPlannersReverifyAnchorAfterWitness(t *testing.T) {
	tests := []struct {
		name string
		plan func(context.Context, *junocashd.Client, chain.ChainInfo, string) error
	}{
		{
			name: "send and send-many",
			plan: func(ctx context.Context, rpc *junocashd.Client, info chain.ChainInfo, scanURL string) error {
				_, err := planWithScan(ctx, rpc, info, regtestCoinType, PlanConfig{
					ScanURL:          scanURL,
					WalletID:         "hot",
					Kind:             types.TxPlanKindWithdrawal,
					Outputs:          []types.TxOutput{{ToAddress: "destination", AmountZat: "1000000"}},
					ChangeAddress:    "change",
					MinConfirmations: 1,
					ExpiryOffset:     40,
					FeeMultiplier:    1,
				}, 1_000_000, nil, &PlanReport{})
				return err
			},
		},
		{
			name: "sweep",
			plan: func(ctx context.Context, rpc *junocashd.Client, info chain.ChainInfo, scanURL string) error {
				_, err := planSweepWithScan(ctx, rpc, info, regtestCoinType, SweepConfig{
					ScanURL:          scanURL,
					WalletID:         "hot",
					ToAddress:        "destination",
					ChangeAddress:    "change",
					MinConfirmations: 1,
					ExpiryOffset:     40,
					FeeMultiplier:    1,
				}, nil)
				return err
			},
		},
		{
			name: "consolidate",
			plan: func(ctx context.Context, rpc *junocashd.Client, info chain.ChainInfo, scanURL string) error {
				_, err := planConsolidateWithScan(ctx, rpc, info, regtestCoinType, ConsolidateConfig{
					ScanURL:          scanURL,
					WalletID:         "hot",
					ToAddress:        "destination",
					ChangeAddress:    "change",
					MaxSpends:        2,
					MinConfirmations: 1,
					ExpiryOffset:     40,
					FeeMultiplier:    1,
				}, nil)
				return err
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			const (
				anchorHeight = int64(100)
				noteHeight   = int64(90)
				anchorHash   = "anchor-hash"
				blockHash    = "note-block-hash"
				txid         = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
			)

			var anchorHashCalls atomic.Int32
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
					if len(request.Params) != 1 || json.Unmarshal(request.Params[0], &height) != nil {
						http.Error(w, "invalid height", http.StatusBadRequest)
						return
					}
					switch height {
					case anchorHeight:
						anchorHashCalls.Add(1)
						result = anchorHash
					case noteHeight:
						result = blockHash
					default:
						http.Error(w, "unexpected height", http.StatusBadRequest)
						return
					}
				case "getblock":
					result = testOrchardBlock(txid)
				case "getblockchaininfo":
					result = map[string]any{
						"chain":  "regtest",
						"blocks": anchorHeight,
						"consensus": map[string]any{
							"nextblock": "c8e71055",
						},
					}
				default:
					http.Error(w, "unexpected method", http.StatusBadRequest)
					return
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"result": result, "error": nil, "id": request.ID})
			}))
			defer node.Close()

			var healthCalls atomic.Int32
			var notesCalls atomic.Int32
			var witnessCalls atomic.Int32
			scanMux := http.NewServeMux()
			scanMux.HandleFunc("/v1/health", func(w http.ResponseWriter, r *http.Request) {
				call := healthCalls.Add(1)
				if call == 2 && (witnessCalls.Load() != 1 || notesCalls.Load() != 2) {
					t.Errorf("closing health check occurred before witness and selected-note recheck")
				}
				hash := anchorHash
				if call > 1 {
					hash = "changed-hash"
				}
				_ = json.NewEncoder(w).Encode(map[string]any{
					"status":         "ok",
					"event_epoch":    strings.Repeat("e", 64),
					"scanned_height": anchorHeight,
					"scanned_hash":   hash,
				})
			})
			scanMux.HandleFunc("/v1/wallets/hot/notes", func(w http.ResponseWriter, r *http.Request) {
				if healthCalls.Load() != 1 {
					t.Errorf("notes were not read between anchor checks")
				}
				notesCalls.Add(1)
				_ = json.NewEncoder(w).Encode(map[string]any{
					"notes": []map[string]any{
						testScanNote(txid, 0, noteHeight, 1),
						testScanNote(txid, 1, noteHeight, 2),
					},
				})
			})
			scanMux.HandleFunc("/v1/orchard/witness", func(w http.ResponseWriter, r *http.Request) {
				if notesCalls.Load() == 0 || healthCalls.Load() != 1 {
					t.Errorf("witness was not read inside the anchor bracket")
				}
				witnessCalls.Add(1)
				var request junoscan.WitnessRequest
				if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
					http.Error(w, "invalid json", http.StatusBadRequest)
					return
				}
				if request.AnchorHeight == nil || *request.AnchorHeight != anchorHeight {
					http.Error(w, "wrong anchor", http.StatusBadRequest)
					return
				}
				paths := make([]map[string]any, 0, len(request.Positions))
				for _, position := range request.Positions {
					paths = append(paths, map[string]any{
						"position":  position,
						"auth_path": testAuthPath(),
					})
				}
				_ = json.NewEncoder(w).Encode(map[string]any{
					"status":        "ok",
					"anchor_height": anchorHeight,
					"root":          "root",
					"paths":         paths,
				})
			})
			scanner := httptest.NewServer(scanMux)
			defer scanner.Close()

			rpc := junocashd.New(node.URL, "", "")
			err := tt.plan(context.Background(), rpc, chain.ChainInfo{
				Chain:    "regtest",
				Height:   anchorHeight,
				BranchID: 0xc8e71055,
			}, scanner.URL)
			if err == nil || !strings.Contains(err.Error(), "scanner hash mismatch") {
				t.Fatalf("error=%v want scanner hash mismatch", err)
			}
			if healthCalls.Load() != 2 || notesCalls.Load() != 2 || witnessCalls.Load() != 1 || anchorHashCalls.Load() != 2 {
				t.Fatalf("calls health=%d notes=%d witness=%d anchor_hash=%d want 2,2,1,2", healthCalls.Load(), notesCalls.Load(), witnessCalls.Load(), anchorHashCalls.Load())
			}
		})
	}
}

func TestSelectedNoteRecheckDetectsPendingSpendAtSameTip(t *testing.T) {
	const txid = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/wallets/hot/notes", func(w http.ResponseWriter, r *http.Request) {
		note := testScanNote(txid, 0, 90, 1)
		note["pending_spent_txid"] = strings.Repeat("b", 64)
		_ = json.NewEncoder(w).Encode(map[string]any{"notes": []map[string]any{note}})
	})
	server := httptest.NewServer(mux)
	defer server.Close()
	scanner, err := junoscan.New(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	err = verifySelectedNotesStillSpendable(context.Background(), scanner, "hot", 100, 1, 0, []logic.UnspentNote{{TxID: txid, ActionIndex: 0, ValueZat: 2_000_000}})
	if err == nil || !strings.Contains(err.Error(), "spendability changed") {
		t.Fatalf("error=%v want spendability changed", err)
	}
}

func testScanNote(txid string, actionIndex int, height int64, position int64) map[string]any {
	return map[string]any{
		"direction":         "incoming",
		"txid":              txid,
		"action_index":      actionIndex,
		"height":            height,
		"position":          position,
		"recipient_address": "receiver",
		"value_zat":         int64(2_000_000),
		"note_nullifier":    nil,
		"created_at":        "2026-07-22T00:00:00Z",
	}
}

func testOrchardBlock(txid string) map[string]any {
	action := func(seed string) map[string]any {
		return map[string]any{
			"nullifier":     strings.Repeat(seed, 32),
			"cmx":           strings.Repeat("22", 32),
			"ephemeralKey":  strings.Repeat("33", 32),
			"encCiphertext": strings.Repeat("44", 52),
		}
	}
	return map[string]any{
		"tx": []map[string]any{
			{
				"txid": txid,
				"orchard": map[string]any{
					"actions": []map[string]any{action("11"), action("55")},
				},
			},
		},
	}
}

func testAuthPath() []string {
	path := make([]string, 32)
	for i := range path {
		path[i] = fmt.Sprintf("node-%d", i)
	}
	return path
}
