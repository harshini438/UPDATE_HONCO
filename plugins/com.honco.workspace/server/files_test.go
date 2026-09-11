package main

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"
)

// The ceiling must bite while streaming, on the byte AFTER the limit --
// exactly at the limit is allowed, one over is not. (Filename sanitising
// is covered in recording_test.go; this file is about size.)
func TestCeilingReaderExactlyAtLimitPasses(t *testing.T) {
	data := bytes.Repeat([]byte("x"), 1000)
	got, err := io.ReadAll(&ceilingReader{r: bytes.NewReader(data), limit: 1000})
	if err != nil {
		t.Fatalf("a file exactly at the limit was refused: %v", err)
	}
	if len(got) != 1000 {
		t.Fatalf("read %d bytes, want 1000", len(got))
	}
}

func TestCeilingReaderOneOverFails(t *testing.T) {
	data := bytes.Repeat([]byte("x"), 1001)
	_, err := io.ReadAll(&ceilingReader{r: bytes.NewReader(data), limit: 1000})
	if !errors.Is(err, errRecordingTooLarge) {
		t.Fatalf("one byte over the limit was not refused: %v", err)
	}
}

// It must fail early, not after buffering everything. With a small read
// buffer the failure has to arrive well before the whole input is consumed
// -- that is the entire point of putting the ceiling inside the reader.
func TestCeilingReaderFailsBeforeConsumingEverything(t *testing.T) {
	src := &countingReader{r: strings.NewReader(strings.Repeat("y", 1<<20))}
	cr := &ceilingReader{r: src, limit: 4096}
	buf := make([]byte, 512)
	var err error
	for err == nil {
		_, err = cr.Read(buf)
	}
	if !errors.Is(err, errRecordingTooLarge) {
		t.Fatalf("expected the size refusal, got %v", err)
	}
	if src.n > 8192 {
		t.Fatalf("read %d bytes from the source before refusing; should have stopped near the 4096 limit", src.n)
	}
}

type countingReader struct {
	r io.Reader
	n int64
}

func (c *countingReader) Read(b []byte) (int, error) {
	n, err := c.r.Read(b)
	c.n += int64(n)
	return n, err
}

// The hard ceiling is a ceiling: a configured value above it is clamped,
// and zero means "Mattermost's limit", which this test cannot see -- but it
// can see that the constant itself is what recordingLimit will never exceed.
func TestMaxRecordingBytesIsTwoGiB(t *testing.T) {
	if MaxRecordingBytes != 2<<30 {
		t.Fatalf("MaxRecordingBytes = %d, want %d", MaxRecordingBytes, 2<<30)
	}
}
