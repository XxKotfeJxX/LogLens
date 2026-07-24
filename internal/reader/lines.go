package reader

import (
	"bufio"
	"context"
	"errors"
	"io"
)

type Line struct {
	Number     int64
	ByteOffset int64
	ByteLength int64
	Raw        []byte
	Oversized  bool
}

// StreamLines keeps only MaxEntrySize bytes per entry in memory while preserving
// the full byte length and offset for later seek-based retrieval.
func StreamLines(ctx context.Context, source io.Reader, maxEntrySize int, handle func(Line) error) error {
	if maxEntrySize <= 0 {
		maxEntrySize = 4 << 20
	}
	buffered := bufio.NewReaderSize(source, 128<<10)
	var (
		number    int64 = 1
		offset    int64
		raw       = make([]byte, 0, min(maxEntrySize, 128<<10))
		length    int64
		oversized bool
	)

	flush := func() error {
		if length == 0 {
			return nil
		}
		line := Line{
			Number: number, ByteOffset: offset, ByteLength: length,
			Raw: append([]byte(nil), raw...), Oversized: oversized,
		}
		if err := handle(line); err != nil {
			return err
		}
		offset += length
		number++
		raw = raw[:0]
		length = 0
		oversized = false
		return nil
	}

	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		part, err := buffered.ReadSlice('\n')
		length += int64(len(part))
		if remaining := maxEntrySize - len(raw); remaining > 0 {
			take := len(part)
			if take > remaining {
				take = remaining
			}
			raw = append(raw, part[:take]...)
		}
		if length > int64(maxEntrySize) {
			oversized = true
		}

		switch {
		case err == nil:
			if err := flush(); err != nil {
				return err
			}
		case errors.Is(err, bufio.ErrBufferFull):
			continue
		case errors.Is(err, io.EOF):
			return flush()
		default:
			return err
		}
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
