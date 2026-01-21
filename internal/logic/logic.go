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

type FeePolicy struct {
	Multiplier uint64
	AddZat     uint64
}

func (p FeePolicy) Apply(base uint64) (uint64, error) {
	mult := p.Multiplier
	if mult == 0 {
		mult = 1
	}
	v, ok := mulUint64(base, mult)
	if !ok {
		return 0, errors.New("overflow")
	}
	v, ok = addUint64(v, p.AddZat)
	if !ok {
		return 0, errors.New("overflow")
	}
	return v, nil
}

// RequiredFeeSend returns the minimum ZIP-317 conventional fee for an Orchard
// send with the given spend and output counts.
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
	return SelectNotesWithFeePolicy(notes, amountZat, outputCount, FeePolicy{})
}

func SelectNotesWithFeePolicy(notes []UnspentNote, amountZat uint64, outputCount int, feePolicy FeePolicy) ([]UnspentNote, uint64, error) {
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
		var ok bool
		total, ok = addUint64(total, n.ValueZat)
		if !ok {
			return nil, 0, errors.New("overflow")
		}
		// Fee assumes we will produce a change output, unless the selected notes exactly
		// match the required amount and fee is unchanged without change (e.g. spend-count
		// dominates).
		feeWithChangeMin := RequiredFeeSend(len(selected), outputCount+1)
		feeWithChange, err := feePolicy.Apply(feeWithChangeMin)
		if err != nil {
			return nil, 0, err
		}
		need, ok := addUint64(amountZat, feeWithChange)
		if !ok {
			return nil, 0, errors.New("overflow")
		}
		if total > need {
			return selected, feeWithChange, nil
		}
		if total == need {
			feeNoChangeMin := RequiredFeeSend(len(selected), outputCount)
			feeNoChange, err := feePolicy.Apply(feeNoChangeMin)
			if err != nil {
				return nil, 0, err
			}
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

func mulUint64(a, b uint64) (uint64, bool) {
	if a == 0 || b == 0 {
		return 0, true
	}
	if a > (^uint64(0))/b {
		return 0, false
	}
	return a * b, true
}
