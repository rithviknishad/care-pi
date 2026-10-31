package units

import "testing"

// The same cases as console/tests/format.test.ts.
func TestBytes(t *testing.T) {
	for _, c := range []struct {
		n    int64
		want string
	}{
		{0, "0 B"},
		{1000, "1000 B"},
		{1536, "1.5 KiB"},
		{16384, "16 KiB"},
		{512 << 20, "512 MiB"},
		{2122317824, "2 GiB"},
		{2006 << 20, "2 GiB"},
		{971296768, "926.3 MiB"},    // 926.3 MiB
		{111067645050, "103.4 GiB"}, // 103.44 GiB
		{128_035_676_160, "119.2 GiB"},
		{1048545, "1 MiB"}, // 1023.97 KiB
	} {
		if got := Bytes(c.n); got != c.want {
			t.Errorf("Bytes(%d) = %q, want %q", c.n, got, c.want)
		}
	}
}
