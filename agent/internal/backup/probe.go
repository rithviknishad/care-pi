package backup

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	"golang.org/x/crypto/ssh"

	"github.com/rithviknishad/care-pi/agent/internal/config"
	"github.com/rithviknishad/care-pi/agent/internal/host"
	"github.com/rithviknishad/care-pi/agent/internal/hostcheck"
	"github.com/rithviknishad/care-pi/agent/internal/paths"
	"github.com/rithviknishad/care-pi/agent/internal/problem"
)

// Next actions of a destination test (schema DestinationTestResult.next_action).
const (
	NextNone          = "none"
	NextAcceptHostKey = "accept-host-key"
	NextInstallSSHKey = "install-ssh-key"
	NextCreateBucket  = "create-bucket"
	NextFix           = "fix"
)

// RepoPresence tells which care-pi repositories are at a location.
type RepoPresence struct {
	Exists      bool     `json:"exists"`
	OtherKeyIDs []string `json:"other_key_ids"`
}

// TestResult is the result of a destination test (schema DestinationTestResult).
type TestResult struct {
	OK                 bool              `json:"ok"`
	Checks             []hostcheck.Check `json:"checks"`
	HostKeyFingerprint *string           `json:"host_key_fingerprint"`
	PublicKey          *string           `json:"public_key"`
	HostKey            *problem.HostKey  `json:"host_key"`
	NextAction         string            `json:"next_action"`
	Repository         *RepoPresence     `json:"repository"`
	FreeBytes          *int64            `json:"free_bytes"`
	Location           *string           `json:"location"`
	TestedAt           time.Time         `json:"tested_at"`
	TotalBytes         *int64            `json:"-"`
}

// TestRequest is one destination test.
type TestRequest struct {
	Dest              config.BackupDestination
	Creds             *Credentials
	AcceptFingerprint string
	CreateBucket      bool
	// Key is the recovery key. nil: the result has no location; an sftp test needs it.
	Key      *Key
	Hostname string
}

// SSHProber talks SSH to an SFTP server. Tests replace it.
type SSHProber interface {
	// HostKey returns the host key that the server presents. algos limits the key types ("" for each).
	HostKey(ctx context.Context, addr string, algos []string) (ssh.PublicKey, error)
	// Auth logs in with the signer and the pinned host key. ErrAuthRefused: the server refused the key.
	Auth(ctx context.Context, addr, user string, hostKey ssh.PublicKey, signer ssh.Signer) error
}

// S3Client is the part of an S3 API that the test needs. Tests replace it.
type S3Client interface {
	BucketExists(ctx context.Context, bucket string) (bool, error)
	MakeBucket(ctx context.Context, bucket, region string) error
	Put(ctx context.Context, bucket, key string, data []byte) error
	Get(ctx context.Context, bucket, key string) ([]byte, error)
	Remove(ctx context.Context, bucket, key string) error
	ObjectLock(ctx context.Context, bucket string) (bool, error)
	Prefixes(ctx context.Context, bucket, prefix string) ([]string, error)
}

// ErrAuthRefused tells that the server refused the SSH key.
var ErrAuthRefused = errors.New("the server refused the SSH key of the agent")

// Tester runs the destination tests (docs/api/README.md section 13.5).
type Tester struct {
	Paths   paths.Paths
	Runner  host.Runner
	Systemd host.Systemd
	Now     func() time.Time
	SSH     SSHProber
	// NewS3 returns the client of an S3 destination. nil uses minio-go.
	NewS3 func(d config.BackupDestination, c *Credentials) (S3Client, error)
	// Binary is the care-pi binary for the posix probe (paths.Binary when empty).
	Binary string
}

func (t *Tester) now() time.Time {
	if t.Now != nil {
		return t.Now().UTC().Truncate(time.Second)
	}
	return time.Now().UTC().Truncate(time.Second)
}

type checks struct {
	list []hostcheck.Check
	ok   bool
}

func (c *checks) add(name string, lvl hostcheck.Level, ok bool, detail, fix string) bool {
	c.list = append(c.list, hostcheck.Check{Name: name, Level: lvl, OK: ok, Detail: detail, Fix: fix})
	if !ok && lvl == hostcheck.Required {
		c.ok = false
	}
	return ok
}

// Test runs the checks of the type. It stops at the first failed required check.
func (t *Tester) Test(ctx context.Context, req TestRequest) (*TestResult, error) {
	res := &TestResult{Checks: []hostcheck.Check{}, NextAction: NextNone, TestedAt: t.now()}
	if req.Key != nil {
		loc := Location(req.Dest, req.Key.ID())
		res.Location = &loc
	}
	c := &checks{ok: true}
	var err error
	switch req.Dest.Type {
	case TypePOSIX:
		err = t.testPOSIX(ctx, req, res, c)
	case TypeSFTP:
		if req.Key == nil {
			return nil, problem.New(problem.NoRecoveryKey, "An sftp test needs the recovery key: the SSH key of the agent comes from it. Make it with POST /backups/recovery-key.")
		}
		err = t.testSFTP(ctx, req, res, c)
	case TypeS3:
		err = t.testS3(ctx, req, res, c)
	default:
		return nil, problem.Invalid("/destination/type", "use posix, sftp, or s3", "enum")
	}
	if err != nil {
		return nil, err
	}
	res.Checks = c.list
	res.OK = c.ok
	if !res.OK && res.NextAction == NextNone {
		res.NextAction = NextFix
	}
	return res, nil
}

// presence returns the care-pi repositories of a list of names.
func presence(names []string, keyID string) *RepoPresence {
	rp := &RepoPresence{OtherKeyIDs: []string{}}
	re := regexp.MustCompile(`^care-pi-([0-9a-f]{8})/?$`)
	for _, n := range names {
		m := re.FindStringSubmatch(strings.TrimSpace(filepath.Base(strings.TrimSuffix(n, "/"))))
		if m == nil {
			continue
		}
		if m[1] == keyID {
			rp.Exists = true
		} else {
			rp.OtherKeyIDs = append(rp.OtherKeyIDs, m[1])
		}
	}
	sort.Strings(rp.OtherKeyIDs)
	return rp
}

func keyID(k *Key) string {
	if k == nil {
		return ""
	}
	return k.ID()
}

func testName() string {
	var b [6]byte
	_, _ = rand.Read(b[:])
	return ".care-pi-test-" + hex.EncodeToString(b[:])
}

// ---- posix ----

// PathProbe is the output of "care-pi backup destination probe PATH" (a hidden command). It runs
// in the environment of restic (a transient unit of the agent), so its result is the result of restic.
type PathProbe struct {
	WriteOK  bool     `json:"write_ok"`
	Error    string   `json:"error,omitempty"`
	Free     int64    `json:"free_bytes"`
	Total    int64    `json:"total_bytes"`
	RootDisk bool     `json:"root_disk"`
	Names    []string `json:"names"`
	Mounted  *bool    `json:"mounted,omitempty"`
}

// ProbePath writes, reads, and deletes 1 MiB in dir, and reads its free space and its entries.
func ProbePath(dir string) PathProbe {
	pp := PathProbe{Names: []string{}}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		pp.Error = err.Error()
		return pp
	}
	data := make([]byte, 1<<20)
	_, _ = rand.Read(data)
	f := filepath.Join(dir, testName())
	err := os.WriteFile(f, data, 0o600)
	if err == nil {
		var back []byte
		back, err = os.ReadFile(f)
		if err == nil && !bytes.Equal(back, data) {
			err = errors.New("the data read back differs")
		}
		if rerr := os.Remove(f); err == nil {
			err = rerr
		}
	}
	if err != nil {
		pp.Error = err.Error()
	} else {
		pp.WriteOK = true
	}
	var st syscall.Statfs_t
	if syscall.Statfs(dir, &st) == nil {
		pp.Free = int64(st.Bavail) * int64(st.Bsize)
		pp.Total = int64(st.Blocks) * int64(st.Bsize)
	}
	var a, r syscall.Stat_t
	if syscall.Stat(dir, &a) == nil && syscall.Stat("/", &r) == nil {
		pp.RootDisk = a.Dev == r.Dev
	}
	if ents, err := os.ReadDir(dir); err == nil {
		for _, e := range ents {
			if e.IsDir() {
				pp.Names = append(pp.Names, e.Name())
			}
		}
	}
	return pp
}

func (t *Tester) binary() string {
	if t.Binary != "" {
		return t.Binary
	}
	return paths.Binary
}

// Probe runs the path probe through the runner.
func (t *Tester) Probe(ctx context.Context, dir string) (*PathProbe, error) {
	out, err := t.Runner.Run(ctx, host.Cmd{Name: t.binary(), Args: []string{"backup", "destination", "probe", dir}})
	if err != nil {
		return nil, err
	}
	var pp PathProbe
	if err := json.Unmarshal(bytes.TrimSpace(out), &pp); err != nil {
		return nil, fmt.Errorf("unexpected probe output: %w", err)
	}
	return &pp, nil
}

func (t *Tester) testPOSIX(ctx context.Context, req TestRequest, res *TestResult, c *checks) error {
	d := req.Dest
	base := Base(d)
	if d.DiskLabel != "" {
		if t.Systemd == nil {
			return errors.New("no systemd client")
		}
		mount := MountUnit(DiskMount(d.DiskLabel))
		err := DiskPresent(t.Paths, d)
		if err == nil {
			err = t.Systemd.Start(ctx, mount)
		}
		if !c.add("mount", hostcheck.Required, err == nil, mountDetail(d.DiskLabel, err), "connect the disk, or prepare it with POST /backups/disks/{disk_id}/prepare") {
			return nil
		}
	}
	pp, err := t.Probe(ctx, base)
	if err != nil {
		c.add("write", hostcheck.Required, false, err.Error(), "check the path")
		return nil
	}
	if !c.add("write", hostcheck.Required, pp.WriteOK, writeDetail(pp), "use a directory that root can write") {
		return nil
	}
	c.add("read", hostcheck.Required, true, "read 1 MiB back", "")
	c.add("delete", hostcheck.Required, true, "deleted the test file", "")
	free, total := pp.Free, pp.Total
	res.FreeBytes, res.TotalBytes = &free, &total
	c.add("free-space", hostcheck.Advised, free >= LowSpace, fmt.Sprintf("%d MiB free", free>>20), "free space on the disk")
	detail := "the path is not on the root disk"
	if pp.RootDisk {
		detail = "the path is on the root disk: a disk failure loses the data and the backup"
	}
	c.add("root-disk", hostcheck.Advised, !pp.RootDisk, detail, "use a USB disk or another computer")
	res.Repository = presence(pp.Names, keyID(req.Key))
	c.add("repository", hostcheck.Advised, true, repoDetail(res.Repository, keyID(req.Key)), "")
	return nil
}

func mountDetail(label string, err error) string {
	if err != nil {
		return "LABEL=" + label + " is not available: " + err.Error()
	}
	return "LABEL=" + label + " at " + DiskMount(label)
}

func writeDetail(pp *PathProbe) string {
	if pp.WriteOK {
		return "wrote and read 1 MiB"
	}
	return pp.Error
}

func repoDetail(rp *RepoPresence, keyID string) string {
	if rp == nil {
		return "the agent cannot list the location"
	}
	parts := []string{}
	if keyID != "" {
		if rp.Exists {
			parts = append(parts, RepoDir(keyID)+" exists; the agent uses it again")
		} else {
			parts = append(parts, RepoDir(keyID)+" does not exist yet")
		}
	}
	if len(rp.OtherKeyIDs) > 0 {
		parts = append(parts, "other care-pi repositories: "+strings.Join(rp.OtherKeyIDs, ", "))
	}
	if len(parts) == 0 {
		return "no care-pi repository"
	}
	return strings.Join(parts, "; ")
}

// ---- sftp ----

// DefaultHostKeyAlgorithms is the order of the key types that the agent asks for at the first contact.
var DefaultHostKeyAlgorithms = []string{ssh.KeyAlgoED25519, ssh.KeyAlgoECDSA256, ssh.KeyAlgoECDSA384,
	ssh.KeyAlgoECDSA521, ssh.KeyAlgoRSASHA512, ssh.KeyAlgoRSASHA256}

// HostKeyOf returns the HostKey of a public key.
func HostKeyOf(k ssh.PublicKey, trusted bool) *problem.HostKey {
	line := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(k)))
	return &problem.HostKey{Type: k.Type(), FingerprintSHA256: ssh.FingerprintSHA256(k), PublicKey: line, Trusted: trusted}
}

// ParseHostKey reads a host_key value.
func ParseHostKey(s string) (ssh.PublicKey, error) {
	k, _, _, _, err := ssh.ParseAuthorizedKey([]byte(strings.TrimSpace(s)))
	return k, err
}

func algosOf(keyType string) []string {
	if keyType == ssh.KeyAlgoRSA {
		return []string{ssh.KeyAlgoRSASHA512, ssh.KeyAlgoRSASHA256, ssh.KeyAlgoRSA}
	}
	return []string{keyType}
}

func (t *Tester) testSFTP(ctx context.Context, req TestRequest, res *TestResult, c *checks) error {
	d := req.Dest
	port := d.Port
	if port == 0 {
		port = DefaultSFTPPort
	}
	addr := net.JoinHostPort(d.Host, strconv.Itoa(port))
	var pinned ssh.PublicKey
	if d.HostKey != "" {
		k, err := ParseHostKey(d.HostKey)
		if err != nil {
			return problem.Invalid("/destination/host_key", "the host key is not valid: "+err.Error(), "format")
		}
		pinned = k
	}
	var algos []string
	if pinned != nil && req.AcceptFingerprint == "" {
		algos = algosOf(pinned.Type())
	}
	presented, err := t.SSH.HostKey(ctx, addr, algos)
	if !c.add("connect", hostcheck.Required, err == nil, connectDetail(addr, err), "check the host and the port, and that the server runs SSH") {
		return nil
	}
	fp := ssh.FingerprintSHA256(presented)
	res.HostKeyFingerprint = &fp
	switch {
	case req.AcceptFingerprint != "":
		if fp != req.AcceptFingerprint {
			p := problem.Newf(problem.DestinationUntrusted, "%s presents a host key with another fingerprint (%s). Compare the fingerprint with the server.", d.Host, fp)
			p.HostKey = HostKeyOf(presented, false)
			if d.ID != "" {
				p.DestinationID = d.ID
			}
			return p
		}
		res.HostKey = HostKeyOf(presented, true)
	case pinned != nil:
		if !bytes.Equal(pinned.Marshal(), presented.Marshal()) {
			res.HostKey = HostKeyOf(presented, false)
			res.NextAction = NextAcceptHostKey
			c.add("host-key", hostcheck.Required, false, "the server presents another host key than the pinned key", "accept the new key only when you know why it changed")
			return nil
		}
		res.HostKey = HostKeyOf(presented, true)
	default:
		res.HostKey = HostKeyOf(presented, false)
		res.NextAction = NextAcceptHostKey
		c.add("host-key", hostcheck.Required, false, "first contact: compare the fingerprint with the server", "accept the fingerprint")
		return nil
	}
	c.add("host-key", hostcheck.Required, true, presented.Type()+" "+fp, "")
	signer, err := ssh.NewSignerFromKey(req.Key.SSHKey())
	if err != nil {
		return err
	}
	line, _ := req.Key.SSHPublic(SSHComment(req.Hostname, req.Key.ID()))
	err = t.SSH.Auth(ctx, addr, d.User, presented, signer)
	if errors.Is(err, ErrAuthRefused) {
		auth := "restrict " + line
		res.PublicKey = &auth
		res.NextAction = NextInstallSSHKey
		c.add("authenticate", hostcheck.Required, false, d.User+"@"+d.Host+" refused the SSH key of the agent", "add the key line to ~/.ssh/authorized_keys of "+d.User)
		return nil
	}
	if !c.add("authenticate", hostcheck.Required, err == nil, authDetail(d, err), "check the user") {
		return nil
	}
	// The rest uses the OpenSSH client with the pinned key, as restic does.
	dd := d
	dd.HostKey = res.HostKey.PublicKey
	if dd.ID == "" {
		dd.ID = "candidate"
	}
	r := Repo{Dest: dd, Key: *req.Key, Hostname: req.Hostname}
	s, err := Open(t.Paths, r)
	if err != nil {
		return err
	}
	defer s.Close()
	return t.sftpBatch(ctx, s, req, res, c)
}

func connectDetail(addr string, err error) string {
	if err != nil {
		return addr + ": " + err.Error()
	}
	return addr + " answers"
}

func authDetail(d config.BackupDestination, err error) string {
	if err != nil {
		return err.Error()
	}
	return "logged in as " + d.User
}

var dfLine = regexp.MustCompile(`^\s*([0-9]+)\s+([0-9]+)\s+([0-9]+)\s+([0-9]+)\s+([0-9]+)%`)

// sftpBatch writes, reads, and deletes a test file, then reads the free space and the entries.
func (t *Tester) sftpBatch(ctx context.Context, s *Session, req TestRequest, res *TestResult, c *checks) error {
	d := req.Dest
	base := strings.TrimRight(d.Path, "/")
	local := filepath.Join(s.Dir(), "probe")
	data := make([]byte, 1<<20)
	_, _ = rand.Read(data)
	if err := os.WriteFile(local, data, 0o600); err != nil {
		return err
	}
	remote := base + "/" + testName()
	hostLocal := t.Paths.Host(local)
	batch := fmt.Sprintf("-mkdir %s\nput %s %s\nget %s %s.back\nrm %s\n-df %s\n-ls -1 %s\n", base, hostLocal, remote, remote, hostLocal, remote, base, base)
	cfg := t.Paths.Host(filepath.Join(s.Dir(), "ssh_config"))
	key := t.Paths.Host(filepath.Join(s.Dir(), "id_ed25519"))
	out, err := t.Runner.Run(ctx, host.Cmd{Name: "sftp", Args: []string{"-F", cfg, "-i", key, "-b", "-", SSHAlias(d.ID)},
		Stdin: strings.NewReader(batch)})
	if err != nil {
		c.add("write", hostcheck.Required, false, sftpError(err), "check that "+d.User+" can write "+base)
		return nil
	}
	back, rerr := os.ReadFile(local + ".back")
	same := rerr == nil && bytes.Equal(back, data)
	c.add("write", hostcheck.Required, true, "wrote 1 MiB to "+base, "")
	if !c.add("read", hostcheck.Required, same, "read 1 MiB back", "check the server") {
		return nil
	}
	c.add("delete", hostcheck.Required, true, "deleted the test file", "")
	names, free, total := parseBatch(string(out))
	if free != nil {
		res.FreeBytes, res.TotalBytes = free, total
		c.add("free-space", hostcheck.Advised, *free >= LowSpace, fmt.Sprintf("%d MiB free", *free>>20), "free space on the server")
	}
	res.Repository = presence(names, keyID(req.Key))
	c.add("repository", hostcheck.Advised, true, repoDetail(res.Repository, keyID(req.Key)), "")
	return nil
}

func sftpError(err error) string {
	var xe *host.ExitError
	if errors.As(err, &xe) && strings.TrimSpace(xe.Stderr) != "" {
		return strings.TrimSpace(errorText(xe.Stderr))
	}
	return err.Error()
}

// parseBatch reads the output of the df and ls commands of the sftp batch.
func parseBatch(out string) (names []string, free, total *int64) {
	section := ""
	for _, l := range strings.Split(out, "\n") {
		if rest, ok := strings.CutPrefix(l, "sftp> "); ok {
			f := strings.Fields(strings.TrimPrefix(rest, "-"))
			if len(f) > 0 {
				section = f[0]
			}
			continue
		}
		switch section {
		case "df":
			if m := dfLine.FindStringSubmatch(l); m != nil {
				size, _ := strconv.ParseInt(m[1], 10, 64)
				avail, _ := strconv.ParseInt(m[3], 10, 64)
				a, s := avail*1024, size*1024
				free, total = &a, &s
			}
		case "ls":
			if n := strings.TrimSpace(l); n != "" {
				names = append(names, n)
			}
		}
	}
	return names, free, total
}

// NetSSH is the SSHProber of x/crypto/ssh.
type NetSSH struct{ Timeout time.Duration }

var errStop = errors.New("stop after the host key")

func (n NetSSH) timeout() time.Duration {
	if n.Timeout > 0 {
		return n.Timeout
	}
	return 20 * time.Second
}

func (n NetSSH) dial(ctx context.Context, addr string, cfg *ssh.ClientConfig) (*ssh.Client, error) {
	d := net.Dialer{Timeout: n.timeout()}
	conn, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, err
	}
	_ = conn.SetDeadline(time.Now().Add(n.timeout()))
	sc, chans, reqs, err := ssh.NewClientConn(conn, addr, cfg)
	if err != nil {
		_ = conn.Close()
		return nil, err
	}
	return ssh.NewClient(sc, chans, reqs), nil
}

// HostKey implements SSHProber.
func (n NetSSH) HostKey(ctx context.Context, addr string, algos []string) (ssh.PublicKey, error) {
	if len(algos) == 0 {
		algos = DefaultHostKeyAlgorithms
	}
	var got ssh.PublicKey
	cfg := &ssh.ClientConfig{User: "care-pi-probe", HostKeyAlgorithms: algos, Timeout: n.timeout(),
		HostKeyCallback: func(_ string, _ net.Addr, k ssh.PublicKey) error {
			got = k
			return errStop
		}}
	c, err := n.dial(ctx, addr, cfg)
	if c != nil {
		_ = c.Close()
	}
	if got != nil {
		return got, nil
	}
	if err == nil {
		err = errors.New("the server sent no host key")
	}
	return nil, err
}

// Auth implements SSHProber.
func (n NetSSH) Auth(ctx context.Context, addr, user string, hostKey ssh.PublicKey, signer ssh.Signer) error {
	cfg := &ssh.ClientConfig{User: user, Auth: []ssh.AuthMethod{ssh.PublicKeys(signer)}, Timeout: n.timeout(),
		HostKeyAlgorithms: algosOf(hostKey.Type()), HostKeyCallback: ssh.FixedHostKey(hostKey)}
	c, err := n.dial(ctx, addr, cfg)
	if err != nil {
		if strings.Contains(err.Error(), "unable to authenticate") {
			return ErrAuthRefused
		}
		return err
	}
	return c.Close()
}

// ---- s3 ----

func (t *Tester) s3(d config.BackupDestination, c *Credentials) (S3Client, error) {
	if t.NewS3 != nil {
		return t.NewS3(d, c)
	}
	return NewMinioS3(d, c)
}

func (t *Tester) testS3(ctx context.Context, req TestRequest, res *TestResult, c *checks) error {
	d := req.Dest
	if req.Creds == nil {
		c.add("authenticate", hostcheck.Required, false, "no S3 keys for "+d.ID, "send the credentials, or set them with PUT /backups/destinations/{id}/secrets")
		return nil
	}
	cl, err := t.s3(d, req.Creds)
	if !c.add("connect", hostcheck.Required, err == nil, s3Detail(d, err), "check the endpoint") {
		return nil
	}
	exists, err := cl.BucketExists(ctx, d.Bucket)
	if err != nil {
		auth := isAuthError(err)
		name := "connect"
		if auth {
			name = "authenticate"
		}
		c.add(name, hostcheck.Required, false, err.Error(), "check the endpoint and the access keys")
		return nil
	}
	c.add("authenticate", hostcheck.Required, true, "the keys are accepted", "")
	if !exists && req.CreateBucket {
		err := cl.MakeBucket(ctx, d.Bucket, d.Region)
		if !c.add("bucket", hostcheck.Required, err == nil, bucketDetail(d.Bucket, true, err), "check the permissions of the keys") {
			return nil
		}
	} else if !exists {
		res.NextAction = NextCreateBucket
		c.add("bucket", hostcheck.Required, false, "the bucket "+d.Bucket+" does not exist", "create the bucket (create_bucket: true)")
		return nil
	} else {
		c.add("bucket", hostcheck.Required, true, "the bucket "+d.Bucket+" exists", "")
	}
	prefix := ""
	if d.Prefix != "" {
		prefix = d.Prefix + "/"
	}
	key := prefix + testName()
	data := make([]byte, 1<<20)
	_, _ = rand.Read(data)
	err = cl.Put(ctx, d.Bucket, key, data)
	if !c.add("write", hostcheck.Required, err == nil, errOr(err, "wrote 1 MiB"), "check the permissions of the keys") {
		return nil
	}
	back, err := cl.Get(ctx, d.Bucket, key)
	if err == nil && !bytes.Equal(back, data) {
		err = errors.New("the data read back differs")
	}
	if !c.add("read", hostcheck.Required, err == nil, errOr(err, "read 1 MiB back"), "check the permissions of the keys") {
		_ = cl.Remove(ctx, d.Bucket, key)
		return nil
	}
	err = cl.Remove(ctx, d.Bucket, key)
	if !c.add("delete", hostcheck.Required, err == nil, errOr(err, "deleted the test object"), "restic needs the permission to delete") {
		return nil
	}
	locked, err := cl.ObjectLock(ctx, d.Bucket)
	detail := "no object lock"
	if err == nil && locked {
		detail = "the bucket has object lock"
	}
	c.add("object-lock", hostcheck.Advised, true, detail, "")
	names, err := cl.Prefixes(ctx, d.Bucket, prefix)
	if err == nil {
		res.Repository = presence(names, keyID(req.Key))
	}
	c.add("repository", hostcheck.Advised, true, repoDetail(res.Repository, keyID(req.Key)), "")
	return nil
}

func s3Detail(d config.BackupDestination, err error) string {
	if err != nil {
		return err.Error()
	}
	return d.Endpoint
}

func bucketDetail(b string, created bool, err error) string {
	if err != nil {
		return "cannot create the bucket " + b + ": " + err.Error()
	}
	if created {
		return "created the bucket " + b
	}
	return "the bucket " + b + " exists"
}

func errOr(err error, ok string) string {
	if err != nil {
		return err.Error()
	}
	return ok
}

func isAuthError(err error) bool {
	r := minio.ToErrorResponse(err)
	switch r.Code {
	case "AccessDenied", "InvalidAccessKeyId", "SignatureDoesNotMatch":
		return true
	}
	return r.StatusCode == 403
}

// MinioS3 is the S3Client of minio-go.
type MinioS3 struct{ c *minio.Client }

// NewMinioS3 returns the client of an s3 destination (path-style bucket lookup).
func NewMinioS3(d config.BackupDestination, cr *Credentials) (*MinioS3, error) {
	ep := strings.TrimRight(d.Endpoint, "/")
	secure := strings.HasPrefix(ep, "https://")
	ep = strings.TrimPrefix(strings.TrimPrefix(ep, "https://"), "http://")
	c, err := minio.New(ep, &minio.Options{Creds: credentials.NewStaticV4(cr.AccessKeyID, cr.SecretAccessKey, ""),
		Secure: secure, Region: d.Region, BucketLookup: minio.BucketLookupPath})
	if err != nil {
		return nil, err
	}
	return &MinioS3{c: c}, nil
}

// BucketExists implements S3Client.
func (m *MinioS3) BucketExists(ctx context.Context, b string) (bool, error) {
	return m.c.BucketExists(ctx, b)
}

// MakeBucket implements S3Client.
func (m *MinioS3) MakeBucket(ctx context.Context, b, region string) error {
	return m.c.MakeBucket(ctx, b, minio.MakeBucketOptions{Region: region})
}

// Put implements S3Client.
func (m *MinioS3) Put(ctx context.Context, b, k string, data []byte) error {
	_, err := m.c.PutObject(ctx, b, k, bytes.NewReader(data), int64(len(data)), minio.PutObjectOptions{})
	return err
}

// Get implements S3Client.
func (m *MinioS3) Get(ctx context.Context, b, k string) ([]byte, error) {
	o, err := m.c.GetObject(ctx, b, k, minio.GetObjectOptions{})
	if err != nil {
		return nil, err
	}
	defer func() { _ = o.Close() }()
	return io.ReadAll(o)
}

// Remove implements S3Client.
func (m *MinioS3) Remove(ctx context.Context, b, k string) error {
	return m.c.RemoveObject(ctx, b, k, minio.RemoveObjectOptions{})
}

// ObjectLock implements S3Client.
func (m *MinioS3) ObjectLock(ctx context.Context, b string) (bool, error) {
	_, mode, _, _, err := m.c.GetObjectLockConfig(ctx, b)
	return err == nil && mode != nil && *mode != "", err
}

// Prefixes implements S3Client.
func (m *MinioS3) Prefixes(ctx context.Context, b, prefix string) ([]string, error) {
	var out []string
	for o := range m.c.ListObjects(ctx, b, minio.ListObjectsOptions{Prefix: prefix}) {
		if o.Err != nil {
			return out, o.Err
		}
		out = append(out, strings.TrimPrefix(o.Key, prefix))
	}
	return out, nil
}

// RemovePrefix deletes each object below the prefix.
func (m *MinioS3) RemovePrefix(ctx context.Context, b, prefix string) error {
	objs := make(chan minio.ObjectInfo)
	go func() {
		defer close(objs)
		for o := range m.c.ListObjects(ctx, b, minio.ListObjectsOptions{Prefix: prefix, Recursive: true}) {
			if o.Err == nil {
				objs <- o
			}
		}
	}()
	for e := range m.c.RemoveObjects(ctx, b, objs, minio.RemoveObjectsOptions{}) {
		if e.Err != nil {
			return e.Err
		}
	}
	return nil
}

// MountUnit returns the systemd mount unit of a path (systemd-escape --path --suffix=mount).
func MountUnit(path string) string { return EscapePath(path) + ".mount" }

// AutomountUnit returns the systemd automount unit of a path.
func AutomountUnit(path string) string { return EscapePath(path) + ".automount" }

// EscapePath escapes a path as "systemd-escape --path" does.
func EscapePath(p string) string {
	p = strings.Trim(p, "/")
	if p == "" {
		return "-"
	}
	var b strings.Builder
	for i := 0; i < len(p); i++ {
		ch := p[i]
		switch {
		case ch == '/':
			b.WriteByte('-')
		case (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || (ch >= '0' && ch <= '9') || ch == '_' || (ch == '.' && i > 0):
			b.WriteByte(ch)
		default:
			fmt.Fprintf(&b, `\x%02x`, ch)
		}
	}
	return b.String()
}
