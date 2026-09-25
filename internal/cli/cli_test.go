package cli

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/Abdullah1738/juno-sdk-go/types"
)

func TestUsageDocumentsSafeDefaultsAndLimits(t *testing.T) {
	var out, errBuf bytes.Buffer

	code := RunWithIO([]string{"--help"}, &out, &errBuf)
	if code != 0 {
		t.Fatalf("exit code=%d stderr=%q", code, errBuf.String())
	}
	for _, want := range []string{
		"--max-spends <2..200>",
		"--exclude-note-id <txid:index>",
		"Defaults: --minconf 100, --fee-multiplier 20",
		"signer limit: 200 inputs and 200 total outputs including change",
	} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("usage missing %q:\n%s", want, out.String())
		}
	}
}

func TestSendRejectsMalformedAndDuplicateExcludedNoteIDsBeforeRPC(t *testing.T) {
	canonical := strings.Repeat("a", 64) + ":0"
	tests := []struct {
		name       string
		exclusions []string
		want       string
	}{
		{name: "malformed", exclusions: []string{"not-a-note-id"}, want: "must match"},
		{name: "duplicate", exclusions: []string{canonical, canonical}, want: "duplicates"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			args := []string{
				"send",
				"--rpc-url", "http://127.0.0.1:1",
				"--wallet-id", "hot",
				"--to", "destination",
				"--amount-zat", "1",
				"--change-address", "change",
				"--json",
			}
			for _, noteID := range tt.exclusions {
				args = append(args, "--exclude-note-id", noteID)
			}

			var out, errBuf bytes.Buffer
			code := RunWithIO(args, &out, &errBuf)
			if code != 1 {
				t.Fatalf("exit code=%d stderr=%q", code, errBuf.String())
			}
			var envelope struct {
				Error struct {
					Code    string `json:"code"`
					Message string `json:"message"`
				} `json:"error"`
			}
			if err := json.Unmarshal(out.Bytes(), &envelope); err != nil {
				t.Fatalf("decode error envelope: %v", err)
			}
			if envelope.Error.Code != string(types.ErrCodeInvalidRequest) || !strings.Contains(envelope.Error.Message, tt.want) {
				t.Fatalf("unexpected error: %+v", envelope.Error)
			}
		})
	}
}

func TestConsolidateRejectsMaxSpendsOutsideRangeBeforeRPC(t *testing.T) {
	for _, value := range []string{"-1", "0", "1", "201"} {
		t.Run(value, func(t *testing.T) {
			var out, errBuf bytes.Buffer
			code := RunWithIO([]string{"consolidate", "--max-spends", value, "--json"}, &out, &errBuf)
			if code != 1 {
				t.Fatalf("exit code=%d stderr=%q", code, errBuf.String())
			}
			var envelope struct {
				Error struct {
					Code    string `json:"code"`
					Message string `json:"message"`
				} `json:"error"`
			}
			if err := json.Unmarshal(out.Bytes(), &envelope); err != nil {
				t.Fatalf("decode error envelope: %v", err)
			}
			if envelope.Error.Code != string(types.ErrCodeInvalidRequest) || !strings.Contains(envelope.Error.Message, "between 2 and 200") {
				t.Fatalf("unexpected error: %+v", envelope.Error)
			}
		})
	}
}

func TestPlannerUint32FlagsRejectOverflowAndHardenedAccount(t *testing.T) {
	maxUint32 := uint64(^uint32(0))
	if _, _, _, err := plannerUint32Flags(maxUint32, (1<<31)-1, maxUint32); err != nil {
		t.Fatalf("valid boundaries rejected: %v", err)
	}
	for name, values := range map[string][3]uint64{
		"coin type overflow": {maxUint32 + 1, 0, 40},
		"hardened account":   {8135, 1 << 31, 40},
		"expiry overflow":    {8135, 0, maxUint32 + 1},
	} {
		t.Run(name, func(t *testing.T) {
			if _, _, _, err := plannerUint32Flags(values[0], values[1], values[2]); err == nil {
				t.Fatal("invalid values accepted")
			}
		})
	}
}

func TestWriteErr_JSON_IncludesVersion(t *testing.T) {
	var out, errBuf bytes.Buffer

	code := writeErr(&out, &errBuf, true, types.ErrCodeInvalidRequest, "bad request")
	if code != 1 {
		t.Fatalf("unexpected exit code: %d", code)
	}

	var v map[string]any
	if err := json.Unmarshal(out.Bytes(), &v); err != nil {
		t.Fatalf("invalid json: %v (%q)", err, out.String())
	}
	if v["version"] != "v1" || v["status"] != "err" {
		t.Fatalf("unexpected json: %v", v)
	}
}

func TestWritePlan_JSON_IncludesVersion(t *testing.T) {
	var out, errBuf bytes.Buffer

	plan := types.TxPlan{
		Version: types.V0,
		Kind:    types.TxPlanKindWithdrawal,
	}

	code := writePlan(&out, &errBuf, true, "", plan)
	if code != 0 {
		t.Fatalf("unexpected exit code: %d (stderr=%q)", code, errBuf.String())
	}

	var v map[string]any
	if err := json.Unmarshal(out.Bytes(), &v); err != nil {
		t.Fatalf("invalid json: %v (%q)", err, out.String())
	}
	if v["version"] != "v1" || v["status"] != "ok" {
		t.Fatalf("unexpected json: %v", v)
	}
}

func TestSendManyRejectsExtraSpendsOutsideRangeBeforeRPC(t *testing.T) {
	for _, cmd := range []string{"send-many", "rebalance"} {
		for _, value := range []string{"-1", "200"} {
			t.Run(cmd+"/"+value, func(t *testing.T) {
				var out, errBuf bytes.Buffer
				code := RunWithIO([]string{
					cmd,
					"--rpc-url", "http://127.0.0.1:1",
					"--wallet-id", "hot",
					"--outputs-file", "unused.json",
					"--change-address", "change",
					"--extra-spends", value,
					"--json",
				}, &out, &errBuf)
				if code != 1 {
					t.Fatalf("exit code=%d stderr=%q", code, errBuf.String())
				}
				var envelope struct {
					Error struct {
						Code    string `json:"code"`
						Message string `json:"message"`
					} `json:"error"`
				}
				if err := json.Unmarshal(out.Bytes(), &envelope); err != nil {
					t.Fatalf("decode error envelope: %v", err)
				}
				if envelope.Error.Code != string(types.ErrCodeInvalidRequest) || !strings.Contains(envelope.Error.Message, "between 0 and 199") {
					t.Fatalf("unexpected error: %+v", envelope.Error)
				}
			})
		}
	}
}

func TestUsageDocumentsExtraSpendFlags(t *testing.T) {
	var out, errBuf bytes.Buffer
	if code := RunWithIO([]string{"--help"}, &out, &errBuf); code != 0 {
		t.Fatalf("exit code=%d", code)
	}
	if strings.Count(out.String(), "[--extra-spends <n>] [--extra-spend-max-zat <zat>]") != 2 {
		t.Fatalf("usage missing extra spend flags for send-many and rebalance:\n%s", out.String())
	}
}

func TestWritePlanWithSelection_JSONEnvelope(t *testing.T) {
	plan := types.TxPlan{Version: types.V0, Kind: types.TxPlanKindWithdrawal, FeeZat: "500000"}

	var plain, errBuf bytes.Buffer
	if code := writePlan(&plain, &errBuf, true, "", plan); code != 0 {
		t.Fatalf("writePlan exit=%d", code)
	}
	var v map[string]json.RawMessage
	if err := json.Unmarshal(plain.Bytes(), &v); err != nil {
		t.Fatal(err)
	}
	if _, ok := v["selection"]; ok {
		t.Fatalf("default envelope must not include selection: %s", plain.String())
	}

	var out bytes.Buffer
	sel := &selectionReport{ExtraSpends: 4, ExtraSpendZat: "1550000"}
	if code := writePlanWithSelection(&out, &errBuf, true, "", plan, sel); code != 0 {
		t.Fatalf("writePlanWithSelection exit=%d", code)
	}
	var envelope struct {
		Version   string       `json:"version"`
		Status    string       `json:"status"`
		Data      types.TxPlan `json:"data"`
		Selection struct {
			ExtraSpends   int    `json:"extra_spends"`
			ExtraSpendZat string `json:"extra_spend_zat"`
		} `json:"selection"`
	}
	if err := json.Unmarshal(out.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.Version != "v1" || envelope.Status != "ok" || envelope.Data.FeeZat != "500000" {
		t.Fatalf("unexpected envelope: %s", out.String())
	}
	if envelope.Selection.ExtraSpends != 4 || envelope.Selection.ExtraSpendZat != "1550000" {
		t.Fatalf("unexpected selection: %+v", envelope.Selection)
	}

	// The plan body is the same either way.
	var a, b struct {
		Data json.RawMessage `json:"data"`
	}
	_ = json.Unmarshal(plain.Bytes(), &a)
	_ = json.Unmarshal(out.Bytes(), &b)
	if !bytes.Equal(a.Data, b.Data) {
		t.Fatalf("plan body differs:\n%s\n%s", a.Data, b.Data)
	}

	var raw, stderr bytes.Buffer
	if code := writePlanWithSelection(&raw, &stderr, false, "", plan, sel); code != 0 {
		t.Fatalf("raw exit=%d", code)
	}
	if !strings.Contains(stderr.String(), "extra_spends=4 extra_spend_zat=1550000") {
		t.Fatalf("stderr=%q", stderr.String())
	}
	var rawPlan types.TxPlan
	if err := json.Unmarshal(raw.Bytes(), &rawPlan); err != nil || rawPlan.FeeZat != "500000" {
		t.Fatalf("raw plan output invalid: %v %q", err, raw.String())
	}
}
