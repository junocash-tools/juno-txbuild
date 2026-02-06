package txbuild

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Abdullah1738/juno-sdk-go/junoscan"
)

func TestListSpendableNotesFromScan_PaginatesAndFilters(t *testing.T) {
	t.Parallel()

	pageCalls := 0
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/wallets/hot/notes", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		q := r.URL.Query()
		if q.Get("spent") != "false" {
			http.Error(w, "bad spent", http.StatusBadRequest)
			return
		}
		if q.Get("limit") != "1000" {
			http.Error(w, "bad limit", http.StatusBadRequest)
			return
		}
		if q.Get("min_value_zat") != "10" {
			http.Error(w, "bad min_value_zat", http.StatusBadRequest)
			return
		}

		cursor := q.Get("cursor")
		now := time.Now().UTC()
		switch cursor {
		case "":
			pageCalls++
			_ = json.NewEncoder(w).Encode(map[string]any{
				"notes": []map[string]any{
					{
						"txid":              "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
						"action_index":      0,
						"height":            100,
						"position":          1,
						"recipient_address": "jtest1qqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqp4f3t7",
						"value_zat":         5,
						"note_nullifier":    "1111111111111111111111111111111111111111111111111111111111111111",
						"created_at":        now,
					},
					{
						"txid":              "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
						"action_index":      1,
						"height":            101,
						"position":          2,
						"recipient_address": "jtest1qqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqp4f3t7",
						"value_zat":         20,
						"note_nullifier":    "2222222222222222222222222222222222222222222222222222222222222222",
						"created_at":        now,
					},
				},
				"next_cursor": "cursor-1",
			})
		case "cursor-1":
			pageCalls++
			_ = json.NewEncoder(w).Encode(map[string]any{
				"notes": []map[string]any{
					{
						"txid":              "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc",
						"action_index":      2,
						"height":            102,
						"position":          3,
						"recipient_address": "jtest1qqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqp4f3t7",
						"value_zat":         30,
						"note_nullifier":    "3333333333333333333333333333333333333333333333333333333333333333",
						"created_at":        now,
					},
				},
			})
		default:
			http.Error(w, "bad cursor", http.StatusBadRequest)
		}
	})

	srv := httptest.NewServer(mux)
	defer srv.Close()

	sc, err := junoscan.New(srv.URL)
	if err != nil {
		t.Fatalf("junoscan.New: %v", err)
	}

	out, err := listSpendableNotesFromScan(context.Background(), sc, "hot", 200, 1, 10)
	if err != nil {
		t.Fatalf("listSpendableNotesFromScan: %v", err)
	}
	if pageCalls != 2 {
		t.Fatalf("page calls=%d", pageCalls)
	}
	if len(out) != 2 {
		t.Fatalf("notes=%d", len(out))
	}
	if out[0].TxID != "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb" {
		t.Fatalf("first txid=%q", out[0].TxID)
	}
	if out[1].TxID != "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc" {
		t.Fatalf("second txid=%q", out[1].TxID)
	}
}

func TestListSpendableNotesFromScan_CursorLoop(t *testing.T) {
	t.Parallel()

	mux := http.NewServeMux()
	mux.HandleFunc("/v1/wallets/hot/notes", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"notes": []map[string]any{
				{
					"txid":              "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
					"action_index":      1,
					"height":            101,
					"position":          2,
					"recipient_address": "jtest1qqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqp4f3t7",
					"value_zat":         20,
					"note_nullifier":    "2222222222222222222222222222222222222222222222222222222222222222",
					"created_at":        time.Now().UTC(),
				},
			},
			"next_cursor": "stuck",
		})
	})

	srv := httptest.NewServer(mux)
	defer srv.Close()

	sc, err := junoscan.New(srv.URL)
	if err != nil {
		t.Fatalf("junoscan.New: %v", err)
	}

	_, err = listSpendableNotesFromScan(context.Background(), sc, "hot", 200, 1, 0)
	if err == nil {
		t.Fatalf("expected error")
	}
}
