package reader

import (
	"context"
	"strings"
	"testing"
)

func TestStreamLinesOffsetsForCRLFAndFinalLine(t *testing.T) {
	input := "first\r\nsecond\nfinal"
	var lines []Line
	err := StreamLines(context.Background(), strings.NewReader(input), 1024, func(line Line) error {
		lines = append(lines, line)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) != 3 {
		t.Fatalf("got %d lines, want 3", len(lines))
	}
	wantOffsets := []int64{0, 7, 14}
	wantLengths := []int64{7, 7, 5}
	for i := range lines {
		if lines[i].ByteOffset != wantOffsets[i] || lines[i].ByteLength != wantLengths[i] {
			t.Errorf("line %d offset/length = %d/%d, want %d/%d", i, lines[i].ByteOffset, lines[i].ByteLength, wantOffsets[i], wantLengths[i])
		}
	}
}

func TestStreamLinesBoundsOversizedContent(t *testing.T) {
	var got Line
	err := StreamLines(context.Background(), strings.NewReader("1234567890\n"), 5, func(line Line) error {
		got = line
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if !got.Oversized || len(got.Raw) != 5 || got.ByteLength != 11 {
		t.Fatalf("unexpected oversized line: %+v", got)
	}
}
