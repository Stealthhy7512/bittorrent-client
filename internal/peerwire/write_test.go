package peerwire

import "bytes"

type shortWriter struct {
	bytes.Buffer
	limit int
}
