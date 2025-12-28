package logic

import (
	"errors"
	"sort"
	"strconv"
	"strings"
)

type UnspentNote struct {
	TxID        string
	ActionIndex uint32
	ValueZat    uint64
}

func RequiredFeeSend(spendCount, outputCount int) uint64 {
	actions := spendCount
	if outputCount > actions {
		actions = outputCount
	}
	if actions < 2 {
		actions = 2
	}
	return 5_000 * uint64(actions)
}

func SelectNotes(notes []UnspentNote, amountZat uint64, outputCount int) ([]UnspentNote, uint64, error) {
	sort.Slice(notes, func(i, j int) bool {
		if notes[i].ValueZat != notes[j].ValueZat {
			return notes[i].ValueZat > notes[j].ValueZat
		}
		if notes[i].TxID != notes[j].TxID {
			return notes[i].TxID < notes[j].TxID
		}
		return notes[i].ActionIndex < notes[j].ActionIndex
	})

	var selected []UnspentNote
	var total uint64
	for _, n := range notes {
		selected = append(selected, n)
		total += n.ValueZat
		// Fee assumes we will produce a change output, unless the selected notes exactly
		// match the required amount and fee is unchanged without change (e.g. spend-count
		// dominates).
		feeWithChange := RequiredFeeSend(len(selected), outputCount+1)
		need, ok := addUint64(amountZat, feeWithChange)
		if !ok {
			return nil, 0, errors.New("overflow")
		}
		if total > need {
			return selected, feeWithChange, nil
		}
		if total == need {
			feeNoChange := RequiredFeeSend(len(selected), outputCount)
			if feeNoChange == feeWithChange {
				return selected, feeWithChange, nil
			}
		}
	}
	return nil, 0, errors.New("insufficient funds")
}

func ParseUint64Decimal(s string) (uint64, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, errors.New("empty")
	}
	return strconv.ParseUint(s, 10, 64)
}

func ParseZECToZat(s string) (uint64, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, errors.New("empty")
	}

	neg := false
	if strings.HasPrefix(s, "-") {
		neg = true
		s = strings.TrimPrefix(s, "-")
	}
	if neg {
		return 0, errors.New("negative")
	}

	whole, frac, _ := strings.Cut(s, ".")
	if whole == "" {
		whole = "0"
	}
	if len(frac) > 8 {
		return 0, errors.New("too many decimals")
	}
	frac = frac + strings.Repeat("0", 8-len(frac))

	w, err := strconv.ParseUint(whole, 10, 64)
	if err != nil {
		return 0, err
	}
	f, err := strconv.ParseUint(frac, 10, 64)
	if err != nil {
		return 0, err
	}

	if w > (^uint64(0))/100_000_000 {
		return 0, errors.New("overflow")
	}
	return w*100_000_000 + f, nil
}

func addUint64(a, b uint64) (uint64, bool) {
	sum := a + b
	if sum < a {
		return 0, false
	}
	return sum, true
}
