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

func FilterNotesMinValue(notes []UnspentNote, minNoteZat uint64) []UnspentNote {
	if minNoteZat == 0 {
		return notes
	}
	out := make([]UnspentNote, 0, len(notes))
	for _, n := range notes {
		if n.ValueZat >= minNoteZat {
			out = append(out, n)
		}
	}
	return out
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

// SuppressDustChange converts small change into fee.
//
// If minChangeZat is > 0 and the computed change is in (0, minChangeZat),
// the returned fee is increased by the change amount so that change becomes 0.
//
// This is useful for avoiding very small change notes without changing the
// requested outputs.
func SuppressDustChange(totalIn, totalOut, feeZat, minChangeZat uint64) (uint64, bool, error) {
	if minChangeZat == 0 {
		return feeZat, false, nil
	}
	if totalIn < totalOut {
		return 0, false, errors.New("invalid totals")
	}
	rem := totalIn - totalOut
	if rem < feeZat {
		return 0, false, errors.New("invalid totals")
	}
	change := rem - feeZat
	if change == 0 || change >= minChangeZat {
		return feeZat, false, nil
	}
	newFee, ok := addUint64(feeZat, change)
	if !ok {
		return 0, false, errors.New("overflow")
	}
	return newFee, true, nil
}

func SelectNotes(notes []UnspentNote, amountZat uint64, outputCount int) ([]UnspentNote, uint64, error) {
	return SelectNotesWithFeePolicy(notes, amountZat, outputCount, FeePolicy{})
}

func SelectNotesWithFeePolicy(notes []UnspentNote, amountZat uint64, outputCount int, feePolicy FeePolicy) ([]UnspentNote, uint64, error) {
	if len(notes) == 0 {
		return nil, 0, errors.New("insufficient funds")
	}

	sortByAsc := func(ns []UnspentNote) {
		sort.Slice(ns, func(i, j int) bool {
			if ns[i].ValueZat != ns[j].ValueZat {
				return ns[i].ValueZat < ns[j].ValueZat
			}
			if ns[i].TxID != ns[j].TxID {
				return ns[i].TxID < ns[j].TxID
			}
			return ns[i].ActionIndex < ns[j].ActionIndex
		})
	}
	sortByDesc := func(ns []UnspentNote) {
		sort.Slice(ns, func(i, j int) bool {
			if ns[i].ValueZat != ns[j].ValueZat {
				return ns[i].ValueZat > ns[j].ValueZat
			}
			if ns[i].TxID != ns[j].TxID {
				return ns[i].TxID < ns[j].TxID
			}
			return ns[i].ActionIndex < ns[j].ActionIndex
		})
	}

	notesAsc := append([]UnspentNote(nil), notes...)
	sortByAsc(notesAsc)

	neededTotal := func(spendCount, outputs int) (uint64, uint64, error) {
		feeMin := RequiredFeeSend(spendCount, outputs)
		fee, err := feePolicy.Apply(feeMin)
		if err != nil {
			return 0, 0, err
		}
		need, ok := addUint64(amountZat, fee)
		if !ok {
			return 0, 0, errors.New("overflow")
		}
		return need, fee, nil
	}

	// 1-note exact match with no change output.
	if need, fee, err := neededTotal(1, outputCount); err != nil {
		return nil, 0, err
	} else {
		for _, n := range notesAsc {
			if n.ValueZat == need {
				return []UnspentNote{n}, fee, nil
			}
		}
	}

	// 1-note best fit (assume change output exists; if not, this is an overpayment but valid).
	if need, fee, err := neededTotal(1, outputCount+1); err != nil {
		return nil, 0, err
	} else {
		for _, n := range notesAsc {
			if n.ValueZat >= need {
				return []UnspentNote{n}, fee, nil
			}
		}
	}

	// 2-note exact match with no change output.
	if need, fee, err := neededTotal(2, outputCount); err != nil {
		return nil, 0, err
	} else if len(notesAsc) >= 2 {
		i, j := 0, len(notesAsc)-1
		for i < j {
			sum, ok := addUint64(notesAsc[i].ValueZat, notesAsc[j].ValueZat)
			if !ok {
				return nil, 0, errors.New("overflow")
			}
			switch {
			case sum == need:
				return []UnspentNote{notesAsc[i], notesAsc[j]}, fee, nil
			case sum < need:
				i++
			default:
				j--
			}
		}
	}

	// 2-note best fit (assume change output exists; if not, this is an overpayment but valid).
	if need, fee, err := neededTotal(2, outputCount+1); err != nil {
		return nil, 0, err
	} else if len(notesAsc) >= 2 {
		var bestI, bestJ int
		var bestSum uint64
		found := false
		for i := 0; i < len(notesAsc)-1; i++ {
			a := notesAsc[i].ValueZat
			if a >= need {
				break
			}
			bNeed := need - a
			j := i + 1 + sort.Search(len(notesAsc)-(i+1), func(k int) bool {
				return notesAsc[i+1+k].ValueZat >= bNeed
			})
			if j <= i || j >= len(notesAsc) {
				continue
			}
			sum, ok := addUint64(a, notesAsc[j].ValueZat)
			if !ok {
				return nil, 0, errors.New("overflow")
			}
			if !found || sum < bestSum || (sum == bestSum && (i < bestI || (i == bestI && j < bestJ))) {
				bestSum, bestI, bestJ = sum, i, j
				found = true
			}
		}
		if found {
			return []UnspentNote{notesAsc[bestI], notesAsc[bestJ]}, fee, nil
		}
	}

	// Greedy fallback (largest-first), with fee computed on each step.
	notesDesc := append([]UnspentNote(nil), notes...)
	sortByDesc(notesDesc)

	var selected []UnspentNote
	var total uint64
	for _, n := range notesDesc {
		selected = append(selected, n)
		var ok bool
		total, ok = addUint64(total, n.ValueZat)
		if !ok {
			return nil, 0, errors.New("overflow")
		}

		// Exact match with no change output.
		needNoChange, feeNoChange, err := neededTotal(len(selected), outputCount)
		if err != nil {
			return nil, 0, err
		}
		if total == needNoChange {
			return selected, feeNoChange, nil
		}

		needWithChange, feeWithChange, err := neededTotal(len(selected), outputCount+1)
		if err != nil {
			return nil, 0, err
		}
		if total >= needWithChange {
			return selected, feeWithChange, nil
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
