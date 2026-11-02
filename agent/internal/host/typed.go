package host

import (
	"fmt"
	"strings"

	"github.com/rithviknishad/care-pi/agent/internal/paths"
)

// WriteAptConf is a typed request (Phase 7, M2 of the review): "write-apt-conf on|off" writes
// /etc/apt/apt.conf.d/52care-pi.conf with the fixed text of AptSecurityConf. Exec (also in the privileged helper)
// builds the command and its input itself, so the caller chooses only on or off, never the text or the file.
const WriteAptConf = "write-apt-conf"

// WriteAptConfCmd returns the typed request for updates.os_security.
func WriteAptConfCmd(on bool) Cmd {
	v := "off"
	if on {
		v = "on"
	}
	return Cmd{Name: WriteAptConf, Args: []string{v}}
}

// AptSecurityConf returns the text of /etc/apt/apt.conf.d/52care-pi.conf for updates.os_security.
func AptSecurityConf(on bool) string {
	v := "0"
	if on {
		v = "1"
	}
	return "// Managed by care-pi (updates.os_security in /etc/care-pi/config.yaml).\n" +
		"// Install security updates automatically. Never reboot by itself: Care reboots\n" +
		"// inside the update window that the operator sets.\n" +
		"Unattended-Upgrade::Automatic-Reboot \"false\";\n" +
		"APT::Periodic::Update-Package-Lists \"1\";\n" +
		"APT::Periodic::Unattended-Upgrade \"" + v + "\";\n"
}

// expand returns the command of a typed request, or c. The file is outside the sandbox of care-pi.service and of the
// helper, so a transient unit writes it.
func expand(c Cmd) (Cmd, error) {
	if c.Name != WriteAptConf {
		return c, nil
	}
	if len(c.Args) != 1 || (c.Args[0] != "on" && c.Args[0] != "off") {
		return c, fmt.Errorf("%s: use on or off, not %q", WriteAptConf, c.Args)
	}
	return Cmd{Name: "systemd-run", Args: []string{"--wait", "--collect", "--pipe", "--quiet", "--unit=care-pi-apt-conf",
		"--", "tee", paths.AptConfCarePi}, Stdin: strings.NewReader(AptSecurityConf(c.Args[0] == "on"))}, nil
}
