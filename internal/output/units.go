package output

import "fmt"

// Units for the text table.
//
// A byte count is the clearest case for why this exists: 730365952 is a true
// answer to "how much memory" and a useless one, while "697 MiB" is the same
// number in a form somebody can act on. The conversion belongs here rather than
// in the command, because it must not reach --json: a consumer wants the number
// it can compare, not a string it has to parse back.
type Unit int

const (
	UnitNone Unit = iota
	UnitBytes
	UnitBytesPerSecond
	UnitMillicores
	UnitPercent
)

// render formats one value in this unit, or reports that it cannot.
func (u Unit) render(v any) (string, bool) {
	n, ok := asFloat(v)
	if !ok || u == UnitNone {
		return "", false
	}

	switch u {
	case UnitBytes:
		return scale(n, 1024, []string{"B", "KiB", "MiB", "GiB", "TiB"}, ""), true
	case UnitBytesPerSecond:
		return scale(n, 1000, []string{"B", "kB", "MB", "GB", "TB"}, "/s"), true
	case UnitMillicores:
		return fmt.Sprintf("%dm", int64(n)), true
	case UnitPercent:
		return fmt.Sprintf("%d%%", int64(n)), true
	}
	return "", false
}

// scale steps a number up through its units until it is small enough to read.
//
// Bytes step by 1024 and rates by 1000, which looks inconsistent and is not: a
// stored byte is counted in powers of two and a transfer rate is quoted in
// powers of ten, by everything from a disk label to a network bill.
func scale(v float64, step float64, names []string, suffix string) string {
	i := 0
	for v >= step && i < len(names)-1 {
		v /= step
		i++
	}
	if i == 0 {
		return fmt.Sprintf("%d %s%s", int64(v), names[i], suffix)
	}
	return fmt.Sprintf("%.1f %s%s", v, names[i], suffix)
}

func asFloat(v any) (float64, bool) {
	switch n := v.(type) {
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	case float64:
		return n, true
	}
	return 0, false
}
