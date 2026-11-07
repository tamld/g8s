package telemetry

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
)

// LedgerRow binds a decoded S1 observation to its zero-based byte offset in the
// source JSONL file. Offsets include blank lines and CRLF bytes.
type LedgerRow[T any] struct {
	Offset int64
	Record T
}

// ReadLedger reads a bounded-line JSONL ledger. Missing ledgers are empty;
// malformed/truncated observations and I/O errors fail closed rather than
// silently shifting evidence references. Each line is limited to 1 MiB.
func ReadLedger[T any](ctx context.Context, filename string) ([]LedgerRow[T], error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	f, err := os.Open(filename)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	var next, offset int64
	scanner.Split(func(data []byte, atEOF bool) (int, []byte, error) {
		advance, token, err := bufio.ScanLines(data, atEOF)
		if token != nil {
			offset = next
		}
		next += int64(advance)
		return advance, token, err
	})
	var rows []LedgerRow[T]
	for scanner.Scan() {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		var row T
		if err := json.Unmarshal(line, &row); err != nil {
			return nil, fmt.Errorf("ledger byte %d: %w", offset, err)
		}
		rows = append(rows, LedgerRow[T]{Offset: offset, Record: row})
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return rows, ctx.Err()
}
