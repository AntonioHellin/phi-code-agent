package tool

import (
	"fmt"
	"strings"
)

type diffKind int

const (
	diffEqual diffKind = iota
	diffDelete
	diffInsert
)

type diffLine struct {
	kind diffKind
	text string
}

const defaultDiffContextLines = 3

// splitLines normalizes CRLF to LF and splits raw text into logical lines.
func splitLines(rawText string) []string {
	if rawText == "" {
		return nil
	}
	normalized := strings.ReplaceAll(rawText, "\r\n", "\n")
	// Remove a single trailing newline so "A\nB\n" yields ["A", "B"] rather than an extra phantom empty line.
	normalized = strings.TrimSuffix(normalized, "\n")
	if normalized == "" {
		return []string{""}
	}
	return strings.Split(normalized, "\n")
}

// computeDiffLines computes line-level edit operations using prefix/suffix trimming plus LCS.
func computeDiffLines(oldLines, newLines []string) []diffLine {
	// Trim identical leading and trailing lines in O(old + new) so large files
	// with small local edits only run quadratic DP on the changed middle slice.
	prefixLen := 0
	for prefixLen < len(oldLines) && prefixLen < len(newLines) && oldLines[prefixLen] == newLines[prefixLen] {
		prefixLen++
	}

	suffixLen := 0
	for suffixLen < len(oldLines)-prefixLen && suffixLen < len(newLines)-prefixLen &&
		oldLines[len(oldLines)-1-suffixLen] == newLines[len(newLines)-1-suffixLen] {
		suffixLen++
	}

	oldChanged := oldLines[prefixLen : len(oldLines)-suffixLen]
	newChanged := newLines[prefixLen : len(newLines)-suffixLen]

	oldChangedLen := len(oldChanged)
	newChangedLen := len(newChanged)

	// Build the Longest Common Subsequence (LCS) table bottom-up.
	// lcs[oldIdx][newIdx] stores how many matching lines exist in oldChanged[oldIdx:] and newChanged[newIdx:].
	// Filling backward lets the forward reconstruction pass decide each step in O(1) without recursion.
	lcs := make([][]int, oldChangedLen+1)
	for row := range lcs {
		lcs[row] = make([]int, newChangedLen+1)
	}
	for oldIdx := oldChangedLen - 1; oldIdx >= 0; oldIdx-- {
		for newIdx := newChangedLen - 1; newIdx >= 0; newIdx-- {
			if oldChanged[oldIdx] == newChanged[newIdx] {
				lcs[oldIdx][newIdx] = lcs[oldIdx+1][newIdx+1] + 1
			} else if lcs[oldIdx+1][newIdx] >= lcs[oldIdx][newIdx+1] {
				lcs[oldIdx][newIdx] = lcs[oldIdx+1][newIdx]
			} else {
				lcs[oldIdx][newIdx] = lcs[oldIdx][newIdx+1]
			}
		}
	}

	// Reconstruct the edit script: prefix equals -> DP middle walk -> trailing suffix equals.
	operations := make([]diffLine, 0, len(oldLines)+len(newLines))
	for prefixIdx := 0; prefixIdx < prefixLen; prefixIdx++ {
		operations = append(operations, diffLine{kind: diffEqual, text: oldLines[prefixIdx]})
	}

	oldIdx, newIdx := 0, 0
	for oldIdx < oldChangedLen && newIdx < newChangedLen {
		switch {
		case oldChanged[oldIdx] == newChanged[newIdx]:
			operations = append(operations, diffLine{kind: diffEqual, text: oldChanged[oldIdx]})
			oldIdx++
			newIdx++
		// Prefer deletions before insertions (>=) so hunks group "-" lines above "+" lines like git diff.
		case lcs[oldIdx+1][newIdx] >= lcs[oldIdx][newIdx+1]:
			operations = append(operations, diffLine{kind: diffDelete, text: oldChanged[oldIdx]})
			oldIdx++
		default:
			operations = append(operations, diffLine{kind: diffInsert, text: newChanged[newIdx]})
			newIdx++
		}
	}
	for ; oldIdx < oldChangedLen; oldIdx++ {
		operations = append(operations, diffLine{kind: diffDelete, text: oldChanged[oldIdx]})
	}
	for ; newIdx < newChangedLen; newIdx++ {
		operations = append(operations, diffLine{kind: diffInsert, text: newChanged[newIdx]})
	}

	suffixStart := len(oldLines) - suffixLen
	for suffixIdx := suffixStart; suffixIdx < len(oldLines); suffixIdx++ {
		operations = append(operations, diffLine{kind: diffEqual, text: oldLines[suffixIdx]})
	}
	return operations
}

// UnifiedDiff formats a Git-style unified diff between oldText and newText.
// Returns an empty string when both texts have identical logical lines.
func UnifiedDiff(oldLabel, newLabel, oldText, newText string) string {
	oldLines := splitLines(oldText)
	newLines := splitLines(newText)
	operations := computeDiffLines(oldLines, newLines)

	// Index 1-based file coordinates at every operation boundary and locate all changes.
	// Tracking coordinates ahead of time avoids rescanning from line 1 when formatting each hunk.
	oldLineAt := make([]int, len(operations)+1)
	newLineAt := make([]int, len(operations)+1)
	var changePositions []int
	currentOldLine := 1
	currentNewLine := 1
	for opIdx, operation := range operations {
		oldLineAt[opIdx] = currentOldLine
		newLineAt[opIdx] = currentNewLine
		switch operation.kind {
		case diffEqual:
			currentOldLine++
			currentNewLine++
		case diffDelete:
			changePositions = append(changePositions, opIdx)
			currentOldLine++
		case diffInsert:
			changePositions = append(changePositions, opIdx)
			currentNewLine++
		}
	}
	oldLineAt[len(operations)] = currentOldLine
	newLineAt[len(operations)] = currentNewLine
	if len(changePositions) == 0 {
		return ""
	}

	var diffBuilder strings.Builder
	diffBuilder.WriteString(fmt.Sprintf("--- %s\n", oldLabel))
	diffBuilder.WriteString(fmt.Sprintf("+++ %s\n", newLabel))

	// Partition changed indexes into unified diff hunks with ±defaultDiffContextLines context.
	changeCursor := 0
	for changeCursor < len(changePositions) {
		hunkStartIdx := max(0, changePositions[changeCursor]-defaultDiffContextLines)
		hunkEndIdx := min(len(operations), changePositions[changeCursor]+defaultDiffContextLines+1)
		// Merge consecutive changes whose ±context windows overlap or touch.
		for changeCursor+1 < len(changePositions) {
			nextStartIdx := max(0, changePositions[changeCursor+1]-defaultDiffContextLines)
			if nextStartIdx > hunkEndIdx {
				break
			}
			changeCursor++
			hunkEndIdx = min(len(operations), changePositions[changeCursor]+defaultDiffContextLines+1)
		}
		writeHunk(&diffBuilder, operations, oldLineAt, newLineAt, hunkStartIdx, hunkEndIdx)
		changeCursor++
	}
	return strings.TrimSuffix(diffBuilder.String(), "\n")
}

// writeHunk renders one @@ -oldStart,oldCount +newStart,newCount @@ section.
func writeHunk(diffBuilder *strings.Builder, operations []diffLine, oldLineAt, newLineAt []int, hunkStartIdx, hunkEndIdx int) {
	oldCount, newCount := 0, 0
	for opIdx := hunkStartIdx; opIdx < hunkEndIdx; opIdx++ {
		switch operations[opIdx].kind {
		case diffEqual:
			oldCount++
			newCount++
		case diffDelete:
			oldCount++
		case diffInsert:
			newCount++
		}
	}
	// Unified diff convention: when a side has 0 lines (new or deleted file), start line is 0.
	oldStart := oldLineAt[hunkStartIdx]
	if oldCount == 0 {
		oldStart--
	}
	newStart := newLineAt[hunkStartIdx]
	if newCount == 0 {
		newStart--
	}
	diffBuilder.WriteString(fmt.Sprintf("@@ -%d,%d +%d,%d @@\n", oldStart, oldCount, newStart, newCount))
	for opIdx := hunkStartIdx; opIdx < hunkEndIdx; opIdx++ {
		operation := operations[opIdx]
		switch operation.kind {
		case diffEqual:
			diffBuilder.WriteString(" " + operation.text + "\n")
		case diffDelete:
			diffBuilder.WriteString("-" + operation.text + "\n")
		case diffInsert:
			diffBuilder.WriteString("+" + operation.text + "\n")
		}
	}
}

// ColorizeDiff applies ANSI terminal colors to unified diff headers, hunk markers, additions, and deletions.
func ColorizeDiff(unifiedDiff string) string {
	if unifiedDiff == "" {
		return ""
	}
	const (
		ansiReset = "\033[0m"
		ansiBold  = "\033[1m"
		ansiRed   = "\033[31m"
		ansiGreen = "\033[32m"
		ansiCyan  = "\033[36m"
	)

	lines := strings.Split(unifiedDiff, "\n")
	coloredLines := make([]string, 0, len(lines))
	for _, rawLine := range lines {
		switch {
		// Check "--- " / "+++ " file headers before "-" / "+" so headers are bolded rather than colored as edits.
		case strings.HasPrefix(rawLine, "--- "), strings.HasPrefix(rawLine, "+++ "):
			coloredLines = append(coloredLines, ansiBold+rawLine+ansiReset)
		case strings.HasPrefix(rawLine, "@@ "):
			coloredLines = append(coloredLines, ansiCyan+rawLine+ansiReset)
		case strings.HasPrefix(rawLine, "+"):
			coloredLines = append(coloredLines, ansiGreen+rawLine+ansiReset)
		case strings.HasPrefix(rawLine, "-"):
			coloredLines = append(coloredLines, ansiRed+rawLine+ansiReset)
		default:
			coloredLines = append(coloredLines, rawLine)
		}
	}
	return strings.Join(coloredLines, "\n")
}
