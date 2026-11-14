package zipw

import (
	"bytes"
	"compress/flate"
	"io"
	"runtime"
)

// Deflate input is split into blocks of blockSize, compressed in parallel
// and concatenated, the way pigz does: each block is primed with the
// previous dictSize bytes as its dictionary, and every block but the last
// ends with a sync flush, so the pieces join into one valid Deflate stream.
// The output depends only on the input and blockSize, never on timing or
// the number of CPUs, so it stays deterministic (§8.2).
const (
	blockSize = 1 << 20
	dictSize  = 32 << 10 // Deflate's window
)

// blockSem bounds the blocks being compressed at once, across every
// concurrent deflate call: one per CPU.
var blockSem = make(chan struct{}, runtime.GOMAXPROCS(0))

type block struct {
	data, dict []byte
	last       bool
	out        chan []byte // the compressed block, once ready
}

// deflate compresses everything from r to w at flate.BestCompression,
// passing the uncompressed bytes through sum as well. It returns how many
// bytes it read.
func deflate(w io.Writer, r io.Reader, sum io.Writer) (int64, error) {
	queue := make(chan *block, cap(blockSem)) // in input order, bounding memory
	readErr := make(chan error, 1)
	var n int64

	go func() {
		defer close(queue)
		var prev []byte
		cur, err := readBlock(r)
		for {
			if err != nil {
				readErr <- err
				return
			}
			n += int64(len(cur))
			sum.Write(cur)
			var next []byte
			last := len(cur) < blockSize
			if !last {
				next, err = readBlock(r)
				last = err == nil && len(next) == 0
			}
			b := &block{data: cur, dict: prev, last: last, out: make(chan []byte, 1)}
			blockSem <- struct{}{}
			go func() {
				b.out <- compressBlock(b)
				<-blockSem
			}()
			queue <- b
			if last {
				readErr <- nil
				return
			}
			prev = cur[len(cur)-dictSize:]
			cur = next
		}
	}()

	var werr error
	for b := range queue {
		out := <-b.out
		if werr == nil {
			_, werr = w.Write(out)
		}
	}
	if err := <-readErr; err != nil {
		return n, err
	}
	return n, werr
}

// readBlock reads up to blockSize bytes; a short block means r is done.
// Its buffer grows as data arrives, so a small file costs a small buffer.
func readBlock(r io.Reader) ([]byte, error) {
	buf := make([]byte, 0, 64<<10)
	for len(buf) < blockSize {
		if len(buf) == cap(buf) {
			// Exactly double (to at most blockSize): append's growth would
			// round up to an allocation size class, letting a block run
			// past blockSize by however much the Go runtime chose.
			grown := make([]byte, len(buf), min(2*cap(buf), blockSize))
			copy(grown, buf)
			buf = grown
		}
		k, err := r.Read(buf[len(buf):min(cap(buf), blockSize)])
		buf = buf[:len(buf)+k]
		if err == io.EOF {
			return buf, nil
		}
		if err != nil {
			return buf, err
		}
	}
	return buf, nil
}

func compressBlock(b *block) []byte {
	var out bytes.Buffer
	// Neither call can fail writing to a bytes.Buffer, and the level is
	// valid.
	fw, _ := flate.NewWriterDict(&out, flate.BestCompression, b.dict)
	fw.Write(b.data)
	if b.last {
		fw.Close()
	} else {
		fw.Flush()
	}
	return out.Bytes()
}
