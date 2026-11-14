package zipw

import (
	"bytes"
	"compress/flate"
	"hash/crc32"
	"io"
	"math/rand"
	"runtime"
	"testing"
)

func TestDeflate(t *testing.T) {
	rnd := rand.New(rand.NewSource(1))
	// Half random, half repetitive: matches cross block boundaries.
	data := make([]byte, 3*blockSize+5)
	rnd.Read(data[:len(data)/2])
	for i := len(data) / 2; i < len(data); i++ {
		data[i] = "repetitive text "[i%16]
	}
	sizes := []int{0, 1, dictSize, blockSize - 1, blockSize, blockSize + 1, 2 * blockSize, len(data)}
	for _, size := range sizes {
		in := data[len(data)-size:]
		var outs [][]byte
		for _, procs := range []int{1, 4, 32} {
			old := runtime.GOMAXPROCS(procs)
			var out bytes.Buffer
			h := crc32.NewIEEE()
			n, err := deflate(&out, bytes.NewReader(in), h)
			runtime.GOMAXPROCS(old)
			if err != nil || n != int64(size) || h.Sum32() != crc32.ChecksumIEEE(in) {
				t.Fatalf("size %d, %d procs: n=%d err=%v crc ok=%v", size, procs, n, err, h.Sum32() == crc32.ChecksumIEEE(in))
			}
			got, err := io.ReadAll(flate.NewReader(&out))
			if err != nil || !bytes.Equal(got, in) {
				t.Fatalf("size %d, %d procs: round trip failed: %v", size, procs, err)
			}
			outs = append(outs, out.Bytes())
		}
		for _, o := range outs[1:] {
			if !bytes.Equal(o, outs[0]) {
				t.Errorf("size %d: output depends on GOMAXPROCS", size)
			}
		}
	}
}

func TestDeflateReadError(t *testing.T) {
	r := io.MultiReader(bytes.NewReader(make([]byte, blockSize+10)), errReader{})
	if _, err := deflate(io.Discard, r, io.Discard); err == nil {
		t.Fatal("read error not reported")
	}
}

type errReader struct{}

func (errReader) Read([]byte) (int, error) { return 0, io.ErrClosedPipe }
