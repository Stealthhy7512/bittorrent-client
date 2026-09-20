package peerwire

import "io"

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
