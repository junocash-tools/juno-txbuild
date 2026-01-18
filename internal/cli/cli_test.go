package cli

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/Abdullah1738/juno-sdk-go/types"
)

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
