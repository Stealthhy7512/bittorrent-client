package peerwire

import "io"

// writeFull retries successful short writes until all data is written. It
// preserves partial progress on errors and returns io.ErrNoProgress if a writer
// returns zero bytes with no error, avoiding an infinite retry loop.
func writeFull(w io.Writer, data []byte) (int64, error) {
	written := 0

	for written < len(data) {
		n, err := w.Write(data[written:])
		written += n

		if err != nil {
			return int64(written), err
		}
		if n == 0 {
			return int64(written), io.ErrNoProgress
		}
	}
	return int64(written), nil
}
