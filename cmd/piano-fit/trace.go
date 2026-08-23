package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync/atomic"
)

// traceRecord is one line of a --trace file: what the search evaluated, when,
// and what the incumbent was at that moment.
//
// The pair (Aggregate, Best) is the whole point. Aggregate alone says what a
// sampler proposed; Best alone says how the run progressed. Together they
// separate "the search is exploring" from "the search is stuck", which is the
// question a convergence curve has to answer.
type traceRecord struct {
	Eval      int64   `json:"eval"`
	TimeSec   float64 `json:"t_sec"`
	Worker    int     `json:"worker"`
	Aggregate float64 `json:"aggregate"`
	Best      float64 `json:"best"`
}

// traceWriter serialises trace records onto one background goroutine.
//
// It is deliberately not a mutex around the file: the objective function runs
// on every worker and a shared lock there would serialise the search itself,
// which would change the very timings the trace is supposed to measure. The
// buffered channel absorbs bursts instead, and a full channel drops the record
// rather than blocking a worker — a trace with a gap is a diagnostic loss, a
// stalled worker is a corrupted measurement.
type traceWriter struct {
	ch       chan traceRecord
	done     chan struct{}
	file     *os.File
	buf      *bufio.Writer
	dropped  atomic.Int64
	writeErr error
}

// traceChannelDepth is sized so a burst of records from every worker fits
// without touching the drop path at any realistic worker count.
const traceChannelDepth = 4096

// newTraceWriter opens path and starts the writer goroutine. A caller that
// passes an empty path gets a nil writer, which every method accepts.
func newTraceWriter(path string) (*traceWriter, error) {
	if path == "" {
		return nil, nil
	}
	if dir := filepath.Dir(path); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, fmt.Errorf("trace dir: %w", err)
		}
	}
	file, err := os.Create(path)
	if err != nil {
		return nil, fmt.Errorf("trace file: %w", err)
	}

	w := &traceWriter{
		ch:   make(chan traceRecord, traceChannelDepth),
		done: make(chan struct{}),
		file: file,
		buf:  bufio.NewWriter(file),
	}
	go w.run()

	return w, nil
}

func (w *traceWriter) run() {
	defer close(w.done)

	enc := json.NewEncoder(w.buf)
	for rec := range w.ch {
		if err := enc.Encode(rec); err != nil && w.writeErr == nil {
			w.writeErr = err
		}
	}
}

// record queues one trace line. It never blocks and it is safe on a nil
// writer, so the call site stays unconditional.
func (w *traceWriter) record(rec traceRecord) {
	if w == nil {
		return
	}
	select {
	case w.ch <- rec:
	default:
		w.dropped.Add(1)
	}
}

// close drains the queue and flushes the file. It reports a dropped-record
// count rather than hiding it: a trace that silently lost samples would read
// as a search that evaluated fewer candidates than it did.
func (w *traceWriter) close() error {
	if w == nil {
		return nil
	}
	close(w.ch)
	<-w.done

	if err := w.buf.Flush(); err != nil {
		return fmt.Errorf("trace flush: %w", err)
	}
	if err := w.file.Close(); err != nil {
		return fmt.Errorf("trace close: %w", err)
	}
	if w.writeErr != nil {
		return fmt.Errorf("trace write: %w", w.writeErr)
	}
	if dropped := w.dropped.Load(); dropped > 0 {
		return fmt.Errorf("trace dropped %d records: the queue could not keep up", dropped)
	}

	return nil
}
