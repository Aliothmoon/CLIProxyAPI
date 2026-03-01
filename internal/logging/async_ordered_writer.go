package logging

import (
	"errors"
	"fmt"
	"io"
	"os"
	"sync"
)

const defaultAsyncLogQueueCapacity = 32768

// AsyncOrderedWriter offloads writes to a single background worker while preserving enqueue order.
// It uses a bounded queue and blocks producers when the queue is full.
type AsyncOrderedWriter struct {
	sink io.Writer

	mu       sync.Mutex
	cond     *sync.Cond
	queue    [][]byte
	capacity int
	head     int
	size     int
	closed   bool

	workerWG sync.WaitGroup

	writeErr     error
	writeErrOnce sync.Once
}

// NewAsyncOrderedWriter creates an async writer with bounded queue capacity.
func NewAsyncOrderedWriter(sink io.Writer, capacity int) (*AsyncOrderedWriter, error) {
	if sink == nil {
		return nil, errors.New("logging: async ordered writer requires non-nil sink")
	}
	if capacity <= 0 {
		capacity = defaultAsyncLogQueueCapacity
	}

	w := &AsyncOrderedWriter{
		sink:     sink,
		capacity: capacity,
		queue:    make([][]byte, capacity),
	}
	w.cond = sync.NewCond(&w.mu)
	w.workerWG.Add(1)
	go w.run()
	return w, nil
}

// Write enqueues bytes for ordered async delivery.
func (w *AsyncOrderedWriter) Write(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}

	buf := make([]byte, len(p))
	copy(buf, p)

	w.mu.Lock()
	defer w.mu.Unlock()

	for w.size == w.capacity && !w.closed {
		w.cond.Wait()
	}
	if w.closed {
		return 0, io.ErrClosedPipe
	}

	idx := (w.head + w.size) % w.capacity
	w.queue[idx] = buf
	w.size++
	w.cond.Signal()
	return len(p), nil
}

// Close stops accepting new writes and drains queued data.
func (w *AsyncOrderedWriter) Close() error {
	w.mu.Lock()
	if w.closed {
		err := w.writeErr
		w.mu.Unlock()
		return err
	}
	w.closed = true
	w.cond.Broadcast()
	w.mu.Unlock()

	w.workerWG.Wait()
	return w.writeErr
}

func (w *AsyncOrderedWriter) run() {
	defer w.workerWG.Done()

	for {
		w.mu.Lock()
		for w.size == 0 && !w.closed {
			w.cond.Wait()
		}
		if w.size == 0 && w.closed {
			w.mu.Unlock()
			return
		}

		msg := w.queue[w.head]
		w.queue[w.head] = nil
		w.head = (w.head + 1) % w.capacity
		w.size--
		w.cond.Signal()
		w.mu.Unlock()

		if err := writeAll(w.sink, msg); err != nil {
			w.setWriteErr(err)
		}
	}
}

func (w *AsyncOrderedWriter) setWriteErr(err error) {
	if err == nil {
		return
	}
	w.writeErrOnce.Do(func() {
		w.mu.Lock()
		w.writeErr = err
		w.mu.Unlock()
		_, _ = fmt.Fprintf(os.Stderr, "logging: async writer sink error: %v\n", err)
	})
}

func writeAll(sink io.Writer, p []byte) error {
	for len(p) > 0 {
		n, err := sink.Write(p)
		if err != nil {
			return err
		}
		if n <= 0 {
			return io.ErrShortWrite
		}
		p = p[n:]
	}
	return nil
}
