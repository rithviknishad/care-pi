package auth

import (
	"bufio"
	"bytes"
	"os"
	"strings"

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
// its hash in /etc/shadow. It supports the SHA-crypt hashes ($5$, $6$) that Imager writes.
// It needs a check on a Pi that was flashed with Imager (spike SP10).
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
			Users []any `yaml:"users"`
		}
		if yaml.Unmarshal(data, &doc) != nil {
			continue
		}
		for _, u := range doc.Users {
			if m, ok := u.(map[string]any); ok {
				if name, ok := m["name"].(string); ok && name != "" && name != "root" {
					return name
				}
			}
		}
	}
	return ""
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
	return strings.HasPrefix(h, "$5$") || strings.HasPrefix(h, "$6$")
}

// Verify implements OSPassword.
func (s Shadow) Verify(secret string) bool {
	ok, err := verifyShaCrypt(secret, s.hash())
	return err == nil && ok
}

// NoOSPassword is a host without the Imager user.
type NoOSPassword struct{}

// Available implements OSPassword.
func (NoOSPassword) Available() bool { return false }

// Verify implements OSPassword.
func (NoOSPassword) Verify(string) bool { return false }
