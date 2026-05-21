package patch

import "strings"

// UnifiedDiff returns a minimal unified diff between two texts, computed with
// a line-level longest-common-subsequence.
func UnifiedDiff(oldText, newText, label string) string {
	if oldText == newText {
		return ""
	}
	oldLines := splitLines(oldText)
	newLines := splitLines(newText)
	lcs := lcsTable(oldLines, newLines)

	var b strings.Builder
	b.WriteString("--- " + label + " (before)\n")
	b.WriteString("+++ " + label + " (after)\n")

	i, j := 0, 0
	for i < len(oldLines) && j < len(newLines) {
		if oldLines[i] == newLines[j] {
			b.WriteString("  " + oldLines[i] + "\n")
			i++
			j++
			continue
		}
		if lcs[i+1][j] >= lcs[i][j+1] {
			b.WriteString("- " + oldLines[i] + "\n")
			i++
		} else {
			b.WriteString("+ " + newLines[j] + "\n")
			j++
		}
	}
	for ; i < len(oldLines); i++ {
		b.WriteString("- " + oldLines[i] + "\n")
	}
	for ; j < len(newLines); j++ {
		b.WriteString("+ " + newLines[j] + "\n")
	}
	return b.String()
}

func splitLines(s string) []string {
	s = strings.TrimSuffix(s, "\n")
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}

// lcsTable builds the LCS length table for two line slices.
func lcsTable(a, b []string) [][]int {
	t := make([][]int, len(a)+1)
	for i := range t {
		t[i] = make([]int, len(b)+1)
	}
	for i := len(a) - 1; i >= 0; i-- {
		for j := len(b) - 1; j >= 0; j-- {
			if a[i] == b[j] {
				t[i][j] = t[i+1][j+1] + 1
			} else if t[i+1][j] >= t[i][j+1] {
				t[i][j] = t[i+1][j]
			} else {
				t[i][j] = t[i][j+1]
			}
		}
	}
	return t
}
