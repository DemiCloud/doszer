package build

import (
	"testing"
	"time"
)

func TestPVDDate(t *testing.T) {
	pvd := func(date string, tz int8) []byte {
		b := make([]byte, 2048)
		b[0] = 1
		copy(b[1:], "CD001")
		copy(b[813:], date)
		b[829] = byte(tz)
		return b
	}
	tests := []struct {
		name string
		pvd  []byte
		want time.Time
	}{
		{"UTC", pvd("1996110912120000", 0), time.Date(1996, 11, 9, 12, 12, 0, 0, time.UTC)},
		{"UTC-8", pvd("1996110904120050", -32), time.Date(1996, 11, 9, 12, 12, 0, 500_000_000, time.UTC)},
		{"not specified", pvd("0000000000000000", 0), Epoch},
		{"garbage", pvd("19961109XX120000", 0), Epoch},
		{"not a PVD", make([]byte, 2048), Epoch},
	}
	for _, tt := range tests {
		if got := pvdDate(tt.pvd); !got.Equal(tt.want) {
			t.Errorf("%s: pvdDate = %v, want %v", tt.name, got, tt.want)
		}
	}
}
