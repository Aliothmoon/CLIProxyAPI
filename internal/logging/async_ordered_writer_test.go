package logging

import (
	"bytes"
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestAsyncOrderedWriterPreservesOrder(t *testing.T) {
	var sink bytes.Buffer

	writer, err := NewAsyncOrderedWriter(&sink, 8)
	if err != nil {
		t.Fatalf("new writer: %v", err)
	}

	const total = 200
	for i := 0; i < total; i++ {
		if _, errWrite := writer.Write([]byte(fmt.Sprintf("line-%03d\n", i))); errWrite != nil {
			t.Fatalf("write %d failed: %v", i, errWrite)
		}
	}

	if errClose := writer.Close(); errClose != nil {
		t.Fatalf("close: %v", errClose)
	}

	lines := strings.Split(strings.TrimSpace(sink.String()), "\n")
	if len(lines) != total {
		t.Fatalf("expected %d lines, got %d", total, len(lines))
	}
	for i := 0; i < total; i++ {
		want := fmt.Sprintf("line-%03d", i)
		if lines[i] != want {
			t.Fatalf("line %d mismatch: got %q want %q", i, lines[i], want)
		}
	}
}

func TestAsyncOrderedWriterCloseDrainsQueue(t *testing.T) {
	var sink bytes.Buffer

	writer, err := NewAsyncOrderedWriter(&sink, 4)
	if err != nil {
		t.Fatalf("new writer: %v", err)
	}

	for i := 0; i < 50; i++ {
		if _, errWrite := writer.Write([]byte("x")); errWrite != nil {
			t.Fatalf("write %d failed: %v", i, errWrite)
		}
	}

	if errClose := writer.Close(); errClose != nil {
		t.Fatalf("close: %v", errClose)
	}
	if got := sink.Len(); got != 50 {
		t.Fatalf("expected 50 bytes flushed, got %d", got)
	}
}

func TestAsyncOrderedWriterWriteAfterClose(t *testing.T) {
	var sink bytes.Buffer

	writer, err := NewAsyncOrderedWriter(&sink, 2)
	if err != nil {
		t.Fatalf("new writer: %v", err)
	}
	if errClose := writer.Close(); errClose != nil {
		t.Fatalf("close: %v", errClose)
	}
	if _, errWrite := writer.Write([]byte("after-close")); errWrite == nil {
		t.Fatal("expected write after close to fail")
	}
}

func TestAsyncOrderedWriterBlocksWhenQueueFull(t *testing.T) {
	sink := newBlockingSink()
	writer, err := NewAsyncOrderedWriter(sink, 1)
	if err != nil {
		t.Fatalf("new writer: %v", err)
	}

	if _, errWrite := writer.Write([]byte("A")); errWrite != nil {
		t.Fatalf("write A: %v", errWrite)
	}
	if _, errWrite := writer.Write([]byte("B")); errWrite != nil {
		t.Fatalf("write B: %v", errWrite)
	}

	doneThird := make(chan error, 1)
	go func() {
		_, errWrite := writer.Write([]byte("C"))
		doneThird <- errWrite
	}()

	select {
	case errWrite := <-doneThird:
		t.Fatalf("third write should block, returned early with err=%v", errWrite)
	case <-time.After(80 * time.Millisecond):
	}

	sink.releaseOne() // finish A, consumer can dequeue B, third write can enqueue C

	select {
	case errWrite := <-doneThird:
		if errWrite != nil {
			t.Fatalf("third write failed: %v", errWrite)
		}
	case <-time.After(300 * time.Millisecond):
		t.Fatal("third write did not unblock after sink progressed")
	}

	sink.releaseOne() // finish B
	sink.releaseOne() // finish C

	if errClose := writer.Close(); errClose != nil {
		t.Fatalf("close: %v", errClose)
	}

	writes := sink.snapshot()
	got := strings.Join(writes, "")
	if got != "ABC" {
		t.Fatalf("unexpected sink writes order/content: got %q want %q", got, "ABC")
	}
}

type blockingSink struct {
	allow chan struct{}

	mu     sync.Mutex
	writes []string
}

func newBlockingSink() *blockingSink {
	return &blockingSink{
		allow: make(chan struct{}, 16),
	}
}

func (s *blockingSink) Write(p []byte) (int, error) {
	<-s.allow
	s.mu.Lock()
	s.writes = append(s.writes, string(p))
	s.mu.Unlock()
	return len(p), nil
}

func (s *blockingSink) releaseOne() {
	s.allow <- struct{}{}
}

func (s *blockingSink) snapshot() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, len(s.writes))
	copy(out, s.writes)
	return out
}

var _ io.Writer = (*blockingSink)(nil)
