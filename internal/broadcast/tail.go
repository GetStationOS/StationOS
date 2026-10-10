package broadcast

import (
	"encoding/binary"
	"errors"
	"io"
)

// Hold only a near-silent tail, flushing it unchanged if music resumes.
// At EOF discard up to five seconds; internal pauses and audible fades survive.
// PCM is stereo signed 16-bit at 44100 Hz, before station gain is applied.
type musicTail struct {
	io.ReadCloser
	tail, pending []byte
	ended         bool
}

const tailLimit = 5 * 44100 * 4

func (r *musicTail) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	for len(r.pending) == 0 && !r.ended {
		block := make([]byte, 1764) // ten milliseconds, aligned to stereo frames
		n, err := io.ReadFull(r.ReadCloser, block)
		block = block[:n]
		quiet := n > 0
		for i := 0; i+1 < n; i += 2 {
			v := int16(binary.LittleEndian.Uint16(block[i:]))
			if v > 32 || v < -32 {
				quiet = false
				break
			}
		}
		if quiet {
			r.tail = append(r.tail, block...)
			if len(r.tail) > tailLimit {
				excess := len(r.tail) - tailLimit
				r.pending = append(r.pending, r.tail[:excess]...)
				copy(r.tail, r.tail[excess:])
				r.tail = r.tail[:tailLimit]
			}
		} else if n > 0 {
			r.pending = append(r.pending, r.tail...)
			r.pending = append(r.pending, block...)
			r.tail = r.tail[:0]
		}
		if err != nil {
			if !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
				return 0, err
			}
			r.ended = true
			r.tail = nil
		}
	}
	n := copy(p, r.pending)
	r.pending = r.pending[n:]
	if n == 0 && r.ended {
		return 0, io.EOF
	}
	return n, nil
}
