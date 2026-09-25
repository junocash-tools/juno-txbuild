//go:build integration || e2e

package app

import (
	"context"
	"encoding/json"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Abdullah1738/juno-txbuild/internal/testutil/containers"
)

// fundSmallNotes sends count notes of amount (in JUNO) from fromAddr to fresh
// addresses of the same account, mines them in and waits until the account
// holds at least wantTotal spendable Orchard notes.
func fundSmallNotes(t *testing.T, jd *containers.Junocashd, account uint32, fromAddr string, count int, amount string, wantTotal int) {
	t.Helper()
	ctx := context.Background()

	type recipient struct {
		Address string      `json:"address"`
		Amount  json.Number `json:"amount"`
	}
	recipients := make([]recipient, 0, count)
	for range count {
		recipients = append(recipients, recipient{
			Address: unifiedAddress(t, jd, account),
			Amount:  json.Number(amount),
		})
	}
	b, err := json.Marshal(recipients)
	if err != nil {
		t.Fatalf("marshal recipients: %v", err)
	}

	raw, err := jd.ExecCLI(ctx, "z_sendmany", fromAddr, string(b), "1")
	if err != nil {
		t.Fatalf("z_sendmany: %v", err)
	}
	opid := strings.Trim(strings.TrimSpace(string(raw)), "\"")
	if opid == "" {
		t.Fatalf("z_sendmany: missing opid")
	}
	txid := waitOpSuccess(t, jd, opid)
	waitWalletTx(t, jd, txid)

	if _, err := jd.ExecCLI(ctx, "generate", "2"); err != nil {
		t.Fatalf("confirm blocks: %v", err)
	}
	waitSpendableOrchardNoteCount(t, jd, account, wantTotal)
}

// newAccountAddress creates a new wallet account and returns its unified address.
func newAccountAddress(t *testing.T, jd *containers.Junocashd) (uint32, string) {
	t.Helper()
	raw, err := jd.ExecCLI(context.Background(), "z_getnewaccount")
	if err != nil {
		t.Fatalf("z_getnewaccount: %v", err)
	}
	var resp struct {
		Account uint32 `json:"account"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		t.Fatalf("z_getnewaccount: invalid json")
	}
	return resp.Account, unifiedAddress(t, jd, resp.Account)
}

func noteIDSet(ids []string) map[string]struct{} {
	out := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		out[strings.ToLower(id)] = struct{}{}
	}
	return out
}

func planNoteID(txid string, actionIndex uint32) string {
	return strings.ToLower(strings.TrimSpace(txid)) + ":" + strconv.FormatUint(uint64(actionIndex), 10)
}

func sortNoteIDsByActionIndex(t *testing.T, ids []string) {
	t.Helper()
	index := func(id string) uint64 {
		i := strings.LastIndexByte(id, ':')
		if i < 0 {
			t.Fatalf("malformed note id %q", id)
		}
		n, err := strconv.ParseUint(id[i+1:], 10, 32)
		if err != nil {
			t.Fatalf("malformed note id %q", id)
		}
		return n
	}
	sort.Slice(ids, func(a, b int) bool {
		if ta, tb := ids[a][:strings.LastIndexByte(ids[a], ':')], ids[b][:strings.LastIndexByte(ids[b], ':')]; ta != tb {
			return ta < tb
		}
		return index(ids[a]) < index(ids[b])
	})
}

// waitSpendableOrchardNoteCountExact waits until the account holds exactly want
// spendable Orchard notes.
func waitSpendableOrchardNoteCountExact(t *testing.T, jd *containers.Junocashd, account uint32, want int) []spendableOrchardNote {
	t.Helper()
	deadline := time.Now().Add(2 * time.Minute)
	for {
		notes := listSpendableOrchardNotes(t, jd, account)
		if len(notes) == want {
			return notes
		}
		if time.Now().After(deadline) {
			t.Fatalf("spendable orchard notes=%d want %d", len(notes), want)
		}
		time.Sleep(200 * time.Millisecond)
	}
}
