package peerwire

import (
	"bytes"
	"errors"
	"io"
	"testing"
)

type shortWriter struct {
	bytes.Buffer
	limit int
}

type recordingErrorWriter struct {
	written int
	err     error
	calls   int
}

func (w *recordingErrorWriter) Write(p []byte) (int, error) {
	w.calls++
	return min(w.written, len(p)), w.err
}

func TestMessageWriteToReportsPartialWriteError(t *testing.T) {
	wantErr := errors.New("write failed")
	w := &recordingErrorWriter{written: 3, err: wantErr}
	n, err := (Message{ID: MessageInterested}).WriteTo(w)
	if n != 3 || !errors.Is(err, wantErr) || w.calls != 1 {
		t.Fatalf("WriteTo() = (%d, %v), calls = %d; want 3, write error, one call", n, err, w.calls)
	}
}

func TestMessageWriteToRejectsNoProgress(t *testing.T) {
	w := &recordingErrorWriter{}
	n, err := (Message{ID: MessageInterested}).WriteTo(w)
	if n != 0 || !errors.Is(err, io.ErrNoProgress) || w.calls != 1 {
		t.Fatalf("WriteTo() = (%d, %v), calls = %d; want 0, io.ErrNoProgress, one call", n, err, w.calls)
	}
}
