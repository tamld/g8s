package diffintel

import (
	"bufio"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

var hunkHeaderRegex = regexp.MustCompile(`^@@\s+-(\d+)(?:,(\d+))?\s+\+(\d+)(?:,(\d+))?\s+@@(?:\s*(.*))?$`)

// ParseUnifiedDiff parses unified git diff output into a slice of FileDiff structs.
func ParseUnifiedDiff(raw string) ([]FileDiff, error) {
	scanner := bufio.NewScanner(strings.NewReader(raw))
	// Allow larger buffer for big single lines
	buf := make([]byte, 64*1024)
	scanner.Buffer(buf, 1024*1024)

	var files []FileDiff
	var currentFile *FileDiff
	var currentHunk *Hunk
	var currOldLine, currNewLine int

	flushHunk := func() {
		if currentFile != nil && currentHunk != nil {
			currentFile.Hunks = append(currentFile.Hunks, *currentHunk)
			currentHunk = nil
		}
	}

	flushFile := func() {
		flushHunk()
		if currentFile != nil {
			files = append(files, *currentFile)
			currentFile = nil
		}
	}

	for scanner.Scan() {
		line := scanner.Text()

		// New file diff header
		if strings.HasPrefix(line, "diff --git ") {
			flushFile()
			parts := strings.Fields(line)
			oldP, newP := "", ""
			if len(parts) >= 4 {
				oldP = strings.TrimPrefix(parts[2], "a/")
				newP = strings.TrimPrefix(parts[3], "b/")
			}
			currentFile = &FileDiff{
				OldPath: oldP,
				NewPath: newP,
			}
			continue
		}

		if currentFile == nil {
			// Lines before first 'diff --git' are ignored
			continue
		}

		if strings.HasPrefix(line, "new file mode ") {
			currentFile.IsNew = true
			continue
		}
		if strings.HasPrefix(line, "deleted file mode ") {
			currentFile.IsDeleted = true
			continue
		}
		if strings.HasPrefix(line, "Binary files ") && strings.HasSuffix(line, " differ") {
			currentFile.IsBinary = true
			continue
		}
		if strings.HasPrefix(line, "--- ") {
			p := strings.TrimPrefix(line, "--- ")
			p = strings.TrimPrefix(p, "a/")
			if p != "/dev/null" && currentFile.OldPath == "" {
				currentFile.OldPath = p
			}
			continue
		}
		if strings.HasPrefix(line, "+++ ") {
			p := strings.TrimPrefix(line, "+++ ")
			p = strings.TrimPrefix(p, "b/")
			if p != "/dev/null" && currentFile.NewPath == "" {
				currentFile.NewPath = p
			}
			continue
		}

		// Hunk header: @@ -oldStart,oldLines +newStart,newLines @@
		if strings.HasPrefix(line, "@@ ") {
			flushHunk()
			matches := hunkHeaderRegex.FindStringSubmatch(line)
			if len(matches) < 4 {
				return nil, fmt.Errorf("malformed hunk header: %s", line)
			}

			oldStart, _ := strconv.Atoi(matches[1])
			oldLines := 1
			if matches[2] != "" {
				oldLines, _ = strconv.Atoi(matches[2])
			}

			newStart, _ := strconv.Atoi(matches[3])
			newLines := 1
			if matches[4] != "" {
				newLines, _ = strconv.Atoi(matches[4])
			}

			header := ""
			if len(matches) >= 6 {
				header = strings.TrimSpace(matches[5])
			}

			currentHunk = &Hunk{
				OldStart: oldStart,
				OldLines: oldLines,
				NewStart: newStart,
				NewLines: newLines,
				Header:   header,
				Lines:    make([]DiffLine, 0, newLines+oldLines),
			}
			currOldLine = oldStart
			currNewLine = newStart
			continue
		}

		// Synthesize fallback hunk if @@ header was omitted in simplified or synthetic diffs
		if currentFile != nil && currentHunk == nil && len(line) > 0 && (line[0] == '+' || line[0] == '-' || line[0] == ' ') {
			currentHunk = &Hunk{
				OldStart: 1,
				OldLines: 1,
				NewStart: 1,
				NewLines: 1,
				Lines:    make([]DiffLine, 0, 10),
			}
			currOldLine = 1
			currNewLine = 1
		}

		// Inside a hunk
		if currentHunk != nil {
			if len(line) == 0 {
				// Empty context line
				currentHunk.Lines = append(currentHunk.Lines, DiffLine{
					Type:    LineContext,
					Content: "",
					OldLine: currOldLine,
					NewLine: currNewLine,
				})
				currOldLine++
				currNewLine++
				continue
			}

			switch line[0] {
			case '+':
				currentHunk.Lines = append(currentHunk.Lines, DiffLine{
					Type:    LineAdd,
					Content: line[1:],
					NewLine: currNewLine,
				})
				currNewLine++
			case '-':
				currentHunk.Lines = append(currentHunk.Lines, DiffLine{
					Type:    LineDelete,
					Content: line[1:],
					OldLine: currOldLine,
				})
				currOldLine++
			case ' ':
				currentHunk.Lines = append(currentHunk.Lines, DiffLine{
					Type:    LineContext,
					Content: line[1:],
					OldLine: currOldLine,
					NewLine: currNewLine,
				})
				currOldLine++
				currNewLine++
			case '\\':
				// e.g. "\ No newline at end of file" -> skip
				continue
			}
		}
	}

	flushFile()

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scan unified diff: %w", err)
	}

	return files, nil
}
