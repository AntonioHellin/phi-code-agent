package tool

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestUnifiedDiff verifies line-level LCS diffs, CRLF normalization, new-file headers,
// nearby hunk merging, distant hunk splitting, and empty output for identical inputs.
func TestUnifiedDiff(t *testing.T) {
	t.Run("groups deletions before insertions and preserves context", func(t *testing.T) {
		oldText := "A\nB\nC\nE\n"
		newText := "A\nX\nB\nY\nE\n"

		actualDiff := UnifiedDiff("sample.txt (existing)", "sample.txt (proposed)", oldText, newText)
		expectedDiff := strings.Join([]string{
			"--- sample.txt (existing)",
			"+++ sample.txt (proposed)",
			"@@ -1,4 +1,5 @@",
			" A",
			"+X",
			" B",
			"-C",
			"+Y",
			" E",
		}, "\n")

		if actualDiff != expectedDiff {
			t.Fatalf("UnifiedDiff mismatch:\ngot:\n%s\n\nwant:\n%s", actualDiff, expectedDiff)
		}
	})

	t.Run("returns empty string for identical texts and CRLF-only differences", func(t *testing.T) {
		if noChangeDiff := UnifiedDiff("a", "b", "line1\nline2\n", "line1\nline2\n"); noChangeDiff != "" {
			t.Errorf("expected empty diff for identical texts, got:\n%s", noChangeDiff)
		}
		if crlfOnlyDiff := UnifiedDiff("a", "b", "line1\r\nline2\r\n", "line1\nline2\n"); crlfOnlyDiff != "" {
			t.Errorf("expected empty diff when only CRLF vs LF differs, got:\n%s", crlfOnlyDiff)
		}
	})

	t.Run("uses 0,0 old range for brand-new files", func(t *testing.T) {
		newFileDiff := UnifiedDiff("/dev/null", "notes.txt (new file)", "", "first\nsecond\n")
		for _, expectedLine := range []string{
			"--- /dev/null",
			"+++ notes.txt (new file)",
			"@@ -0,0 +1,2 @@",
			"+first",
			"+second",
		} {
			if !strings.Contains(newFileDiff, expectedLine) {
				t.Errorf("expected new-file diff to contain %q, got:\n%s", expectedLine, newFileDiff)
			}
		}
	})

	t.Run("splits distant edits into separate hunks while merging nearby edits", func(t *testing.T) {
		var originalLines []string
		for lineNumber := 1; lineNumber <= 16; lineNumber++ {
			originalLines = append(originalLines, fmt.Sprintf("line %02d", lineNumber))
		}

		// Two distant edits (line 2 and line 15) have >6 untouched lines between them -> 2 hunks.
		distantEditedLines := append([]string(nil), originalLines...)
		distantEditedLines[1] = "line 02 edited"
		distantEditedLines[14] = "line 15 edited"

		distantDiff := UnifiedDiff("old", "new", strings.Join(originalLines, "\n"), strings.Join(distantEditedLines, "\n"))
		if hunkCount := strings.Count(distantDiff, "@@ "); hunkCount != 2 {
			t.Fatalf("expected 2 separate hunks for distant edits, got %d:\n%s", hunkCount, distantDiff)
		}
		if strings.Contains(distantDiff, " line 08") {
			t.Errorf("expected middle untouched lines to be omitted between hunks, got:\n%s", distantDiff)
		}

		// Two nearby edits (line 2 and line 6) overlap within ±3 context -> merged into 1 hunk.
		nearbyEditedLines := append([]string(nil), originalLines...)
		nearbyEditedLines[1] = "line 02 edited"
		nearbyEditedLines[5] = "line 06 edited"

		nearbyDiff := UnifiedDiff("old", "new", strings.Join(originalLines, "\n"), strings.Join(nearbyEditedLines, "\n"))
		if hunkCount := strings.Count(nearbyDiff, "@@ "); hunkCount != 1 {
			t.Fatalf("expected nearby edits to merge into 1 hunk, got %d:\n%s", hunkCount, nearbyDiff)
		}
	})
}

// TestColorizeDiff verifies ANSI color assignment and confirms file headers
// (--- / +++) are bolded rather than miscolored as deletions or additions.
func TestColorizeDiff(t *testing.T) {
	if emptyColored := ColorizeDiff(""); emptyColored != "" {
		t.Fatalf("expected empty output for empty diff, got %q", emptyColored)
	}

	plainDiff := strings.Join([]string{
		"--- file.go (existing)",
		"+++ file.go (proposed)",
		"@@ -1,2 +1,2 @@",
		" context",
		"-old line",
		"+new line",
	}, "\n")

	coloredDiff := ColorizeDiff(plainDiff)
	for _, expectedSegment := range []string{
		"\033[1m--- file.go (existing)\033[0m",
		"\033[1m+++ file.go (proposed)\033[0m",
		"\033[36m@@ -1,2 +1,2 @@\033[0m",
		"\033[31m-old line\033[0m",
		"\033[32m+new line\033[0m",
	} {
		if !strings.Contains(coloredDiff, expectedSegment) {
			t.Errorf("expected colored diff to contain %q, got:\n%s", expectedSegment, coloredDiff)
		}
	}
	if strings.Contains(coloredDiff, "\033[31m--- ") || strings.Contains(coloredDiff, "\033[32m+++ ") {
		t.Errorf("headers must not be miscolored as deletions or additions:\n%s", coloredDiff)
	}
}

// TestWriteFilePreview_AndDecoratorForwarding verifies WriteFileTool.Preview for new,
// modified, unchanged, and invalid inputs, as well as forwarding through RetryDecorator.
func TestWriteFilePreview_AndDecoratorForwarding(t *testing.T) {
	wrappedWriteTool := &RetryDecorator{Tool: &WriteFileTool{}, MaxRetries: 2}
	var previewerTool Previewer = wrappedWriteTool

	tempDir := t.TempDir()
	newFilePath := filepath.Join(tempDir, "fresh.txt")
	escapedNewPath := strings.ReplaceAll(newFilePath, `\`, `\\`)

	// New file produces a new-file diff header and a Create prompt.
	newPreview, newConfirmPrompt, hasNewPreview := previewerTool.Preview(`{"path":"` + escapedNewPath + `","content":"hello\nworld"}`)
	if !hasNewPreview {
		t.Fatalf("expected preview for new file")
	}
	if newConfirmPrompt != "Create "+newFilePath+"?" {
		t.Errorf("expected Create prompt, got %q", newConfirmPrompt)
	}
	if !strings.Contains(newPreview, "(new file)") || !strings.Contains(newPreview, "+hello") {
		t.Errorf("expected colored new-file diff preview, got:\n%s", newPreview)
	}

	// Existing file produces a unified diff and an Apply changes prompt.
	existingFilePath := filepath.Join(tempDir, "existing.txt")
	if writeErr := os.WriteFile(existingFilePath, []byte("alpha\nbeta\n"), 0644); writeErr != nil {
		t.Fatalf("failed seeding test file: %v", writeErr)
	}
	escapedExistingPath := strings.ReplaceAll(existingFilePath, `\`, `\\`)

	editPreview, editConfirmPrompt, hasEditPreview := previewerTool.Preview(`{"path":"` + escapedExistingPath + `","content":"alpha\ngamma\n"}`)
	if !hasEditPreview {
		t.Fatalf("expected preview for existing file")
	}
	if editConfirmPrompt != "Apply changes to "+existingFilePath+"?" {
		t.Errorf("expected Apply changes prompt, got %q", editConfirmPrompt)
	}
	if !strings.Contains(editPreview, "-beta") || !strings.Contains(editPreview, "+gamma") {
		t.Errorf("expected -beta / +gamma diff lines, got:\n%s", editPreview)
	}

	// Unchanged file omits ANSI diff lines and reports (no changes).
	samePreview, _, hasSamePreview := previewerTool.Preview(`{"path":"` + escapedExistingPath + `","content":"alpha\nbeta\n"}`)
	if !hasSamePreview || !strings.Contains(samePreview, "(no changes)") {
		t.Errorf("expected (no changes) preview, got hasPreview=%v text=%q", hasSamePreview, samePreview)
	}

	// Invalid JSON or empty path falls back cleanly without preview.
	if _, _, hasInvalid := previewerTool.Preview(`{"path":`); hasInvalid {
		t.Errorf("expected hasPreview=false for invalid JSON")
	}
	if _, _, hasEmptyPath := previewerTool.Preview(`{"path":"   ","content":"x"}`); hasEmptyPath {
		t.Errorf("expected hasPreview=false for blank path")
	}
}
