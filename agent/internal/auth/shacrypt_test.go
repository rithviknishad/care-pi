package auth

import "testing"

// The vectors come from glibc crypt(3) (python:3.12-slim, crypt.crypt).
func TestShaCryptMatchesGlibc(t *testing.T) {
	cases := []struct{ pw, hash string }{
		{"Hello world!", "$5$saltstring$5B8vYYiY.CVt1RlTTf8KbXBH3hsxY/GNooZaBBGWEc5"},
		{"correct horse 2026 Δ", "$5$saltstring$UVQFhSl6pJIe.838edQv4q3.C5AwvFcsdox2/Qdg/OD"},
		{"", "$5$saltstring$FdNfA4gXqvCeO6iZs7G/.wwwoywYZqo0l1pwmfWaBA7"},
		{"Hello world!", "$6$saltstring$svn8UoSVapNtMuq1ukKS4tPQd8iKwSMHWjl/O817G3uBnIFNjnQJuesI68u4OTLiBFdcbYEdFCoEOfaS35inz1"},
		{"correct horse 2026 Δ", "$6$saltstring$jXsX588qvhnRM4vQrtUJ8X0ukom8VQLfNrPoRxq8fCp6ao2t02r4P6WZKSCPzcPPuHaih09EGvvisnkEZOm7x1"},
		{"Hello world!", "$5$rounds=10000$saltstringsaltst$3xv.VbSHBb41AL9AvLeujZkZRBAwqFMz2.opqey6IcA"},
		{"Hello world!", "$6$rounds=1400$anotherlongsalts$5FGyu8c4BZDX4wJgs0Un26YOw2XibT5eTkHF1I1aP3QqStoJI9BHD2YPJYsAjEePVGUyBjdZxcNqMWlrrbIOC."},
		{"correct horse 2026 Δ", "$5$Ab3dEf9h$CaZWUZ1jloj1AWi8cpvs9Drp1muQ6jUs7sY/EYdjL87"},
		{"", "$6$x$QSmr1Bx2g4O6BzKvdkgOcyU6H91X6I/XBv5pSalMhSPkwdH6Beo3F455xZJg0v//bxVK5F4OE5k1.0xuR26MK0"},
	}
	for _, c := range cases {
		ok, err := verifyShaCrypt(c.pw, c.hash)
		if err != nil || !ok {
			got, _ := shaCrypt(c.pw, c.hash)
			t.Errorf("%q: ok=%v err=%v\n got  %s\n want %s", c.pw, ok, err, got, c.hash)
		}
		if ok, _ := verifyShaCrypt(c.pw+"x", c.hash); ok {
			t.Errorf("%q: a different password matched", c.pw)
		}
	}
	if _, err := verifyShaCrypt("x", "$y$j9T$abc$def"); err == nil {
		t.Error("yescrypt must give an error")
	}
}
