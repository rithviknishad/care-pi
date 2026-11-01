package auth

import (
	"bufio"
	"bytes"
	"crypto/subtle"
	"os"
	"strings"

	"github.com/openwall/yescrypt-go"
	"go.yaml.in/yaml/v3"

	"github.com/rithviknishad/care-pi/agent/internal/paths"
)

// OSPassword checks the claim secret of the method os-password: the password of the user that
// Raspberry Pi Imager created (plans/07-first-contact-trust.md, section 4.2).
type OSPassword interface {
	// Available reports whether the method can work on this host now.
	Available() bool
	// Verify reports whether secret is the password of the Imager user.
	Verify(secret string) bool
}

// UserDataFiles are the cloud-init files of Raspberry Pi Imager, in order.
var UserDataFiles = []string{"/boot/firmware/user-data", "/boot/user-data"}

// Shadow finds the Imager user in the cloud-init user-data, then checks the password against
// its hash in /etc/shadow. It supports the SHA-crypt hashes ($5$, $6$) and yescrypt ($y$), the
// hashes that Imager writes.
// Raspberry Pi Imager 2.x writes the singular key "user:" (a mapping with name and passwd), so that
// cloud-init renames the default user of the image. Older user-data has the list "users:".
// cloud-init writes the hash into /etc/shadow in its init stage, before cloud-final.service
// and before care-firstboot.service (spike SP10, plans/08-spike-log.md).
type Shadow struct {
	Paths paths.Paths
}

func (s Shadow) user() string {
	for _, f := range UserDataFiles {
		data, err := os.ReadFile(s.Paths.Abs(f))
		if err != nil {
			continue
		}
		var doc struct {
			User  any   `yaml:"user"`
			Users []any `yaml:"users"`
		}
		if yaml.Unmarshal(data, &doc) != nil {
			continue
		}
		if name := userName(doc.User); name != "" {
			return name
		}
		for _, u := range doc.Users {
			if name := userName(u); name != "" {
				return name
			}
		}
	}
	return ""
}

// userName returns the name of a cloud-init user entry: a mapping with "name", or a plain name.
// The entry "default" and root give "".
func userName(u any) string {
	name := ""
	switch v := u.(type) {
	case map[string]any:
		name, _ = v["name"].(string)
	case string:
		name = v
	}
	if name == "default" || name == "root" {
		return ""
	}
	return name
}

func (s Shadow) hash() string {
	name := s.user()
	if name == "" {
		return ""
	}
	data, err := os.ReadFile(s.Paths.Abs("/etc/shadow"))
	if err != nil {
		return ""
	}
	sc := bufio.NewScanner(bytes.NewReader(data))
	for sc.Scan() {
		f := strings.Split(sc.Text(), ":")
		if len(f) >= 2 && f[0] == name {
			return f[1]
		}
	}
	return ""
}

// Available implements OSPassword.
func (s Shadow) Available() bool {
	h := s.hash()
	return strings.HasPrefix(h, "$5$") || strings.HasPrefix(h, "$6$") || strings.HasPrefix(h, "$y$")
}

// Verify implements OSPassword.
func (s Shadow) Verify(secret string) bool {
	h := s.hash()
	if strings.HasPrefix(h, "$y$") {
		return verifyYescrypt(secret, h)
	}
	ok, err := verifyShaCrypt(secret, h)
	return err == nil && ok
}

// verifyYescrypt checks a yescrypt hash ($y$). Imager 2.x writes yescrypt for each OS with a
// release date of 2023 or later (CustomisationGenerator::osUsesYescrypt), so for each trixie image.
// The default cost of libxcrypt (j9T) uses 16 MiB for one check.
func verifyYescrypt(password, hashed string) bool {
	got, err := yescrypt.Hash([]byte(password), []byte(hashed))
	return err == nil && subtle.ConstantTimeCompare(got, []byte(hashed)) == 1
}

// NoOSPassword is a host without the Imager user.
type NoOSPassword struct{}

// Available implements OSPassword.
func (NoOSPassword) Available() bool { return false }

// Verify implements OSPassword.
func (NoOSPassword) Verify(string) bool { return false }
