package sshx

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/skeema/knownhosts"
	"golang.org/x/crypto/ssh"
	xknownhosts "golang.org/x/crypto/ssh/knownhosts"
)

// HostKeyQuestion describes an unknown host key the user is asked to trust.
type HostKeyQuestion struct {
	// Host is the alias the user typed.
	Host string
	// KeyType is the key algorithm, like ssh-ed25519.
	KeyType string
	// Fingerprint is the SHA256 fingerprint, as ssh prints it.
	Fingerprint string
}

// HostKeyPrompter asks the user whether to trust an unknown host key.
type HostKeyPrompter interface {
	ConfirmHostKey(ctx context.Context, q HostKeyQuestion) (bool, error)
}

// hostKeyChecker verifies host keys against known_hosts and applies the
// StrictHostKeyChecking policy to unknown hosts.
type hostKeyChecker struct {
	hc        *HostConfig
	prompter  HostKeyPrompter
	acceptNew bool
	db        *knownhosts.HostKeyDB
	// names maps a temporary copy of a known_hosts file, read in place of
	// the file because it had lines that can't be parsed, back to the file.
	names map[string]string
	// lookup is the known_hosts name for this host: HostKeyAlias, or
	// host:port.
	lookup string
	// beforePrompt and afterPrompt pause the handshake deadline while the
	// user decides.
	beforePrompt, afterPrompt func()
}

func newHostKeyChecker(hc *HostConfig, prompter HostKeyPrompter, acceptNew bool, log *slog.Logger) (*hostKeyChecker, error) {
	var files []string
	for _, f := range hc.KnownHostsFiles {
		if _, err := os.Stat(f); err == nil {
			files = append(files, f)
		}
	}
	db, names, err := readKnownHosts(files, log)
	if err != nil {
		return nil, fmt.Errorf("read known_hosts: %w; check that the file is readable", err)
	}
	lookup := net.JoinHostPort(hc.HostName, strconv.Itoa(hc.Port))
	if hc.HostKeyAlias != "" {
		// Like OpenSSH, an alias replaces the host name and ignores the port.
		lookup = net.JoinHostPort(hc.HostKeyAlias, "22")
	}
	return &hostKeyChecker{
		hc: hc, prompter: prompter, acceptNew: acceptNew, db: db, names: names, lookup: lookup,
		beforePrompt: func() {}, afterPrompt: func() {},
	}, nil
}

// readKnownHosts reads the known_hosts files. Like OpenSSH, it skips a line
// it can't parse, with a warning in the log, instead of refusing to connect.
// A file with such lines is read from a temporary copy that has them
// blanked, so line numbers still match the file; names maps each copy back
// to its file. The copies are removed before it returns.
func readKnownHosts(files []string, log *slog.Logger) (*knownhosts.HostKeyDB, map[string]string, error) {
	names := map[string]string{}
	defer func() {
		for tmp := range names {
			_ = os.Remove(tmp)
		}
	}()
	paths := make([]string, 0, len(files))
	for _, f := range files {
		p, err := parsableKnownHosts(f, log)
		if err != nil {
			return nil, nil, err
		}
		if p != f {
			names[p] = f
		}
		paths = append(paths, p)
	}
	db, err := knownhosts.NewDB(paths...)
	if err != nil {
		return nil, nil, err
	}
	return db, names, nil
}

// parsableKnownHosts returns f when x/crypto parses all of it, else the
// path of a temporary copy with each line it rejects blanked and logged.
func parsableKnownHosts(f string, log *slog.Logger) (string, error) {
	_, err := xknownhosts.New(f)
	if err == nil {
		return f, nil
	}
	data, rerr := os.ReadFile(f)
	if rerr != nil {
		return "", rerr
	}
	tmp, cerr := os.CreateTemp("", "pfwd-known_hosts-*")
	if cerr != nil {
		return "", cerr
	}
	name := tmp.Name()
	_ = tmp.Close()
	lines := bytes.Split(data, []byte("\n"))
	for src := f; err != nil; src = name {
		n, why, ok := rejectedLine(err, src)
		if !ok || n > len(lines) || len(lines[n-1]) == 0 {
			_ = os.Remove(name)
			return "", err
		}
		log.Warn("skipping a known_hosts line that can't be parsed", "at", fmt.Sprintf("%s:%d", f, n), "err", why)
		lines[n-1] = nil
		if werr := os.WriteFile(name, bytes.Join(lines, []byte("\n")), 0o600); werr != nil { //nolint:gosec // name comes from os.CreateTemp
			_ = os.Remove(name)
			return "", werr
		}
		_, err = xknownhosts.New(name)
	}
	return name, nil
}

// rejectedLine reads the line number and reason from x/crypto's
// "knownhosts: <file>:<line>: <reason>" parse error.
func rejectedLine(err error, file string) (int, string, bool) {
	rest, ok := strings.CutPrefix(err.Error(), "knownhosts: "+file+":")
	if !ok {
		return 0, "", false
	}
	num, why, ok := strings.Cut(rest, ": ")
	n, cerr := strconv.Atoi(num)
	if !ok || cerr != nil || n < 1 {
		return 0, "", false
	}
	return n, why, true
}

// algorithms returns the host key algorithms known for this host, so the
// server presents a key we can verify. Empty means no preference.
func (c *hostKeyChecker) algorithms() []string {
	return c.db.HostKeyAlgorithms(c.lookup)
}

func (c *hostKeyChecker) callback(ctx context.Context) ssh.HostKeyCallback {
	return func(_ string, remote net.Addr, key ssh.PublicKey) error {
		if _, ok := remote.(*net.TCPAddr); !ok {
			// A ProxyCommand has no remote address. x/crypto checks only the
			// host name when it has one, but still parses the address.
			remote = &net.TCPAddr{IP: net.IPv4zero}
		}
		err := c.db.HostKeyCallback()(c.lookup, remote, key)
		switch {
		case err == nil:
			return nil
		case knownhosts.IsHostKeyChanged(err):
			return c.changedError(err, key)
		case !knownhosts.IsHostUnknown(err):
			return &Error{Kind: ErrHostKey, Err: err, Msg: fmt.Sprintf(
				"host key for %s was refused by known_hosts: %v", c.hc.Alias, err)}
		}

		fingerprint := ssh.FingerprintSHA256(key)
		switch {
		case c.hc.StrictHostKeyChecking == "yes":
			return &Error{Kind: ErrHostKey, Msg: fmt.Sprintf(
				"host key for %s is unknown and StrictHostKeyChecking is yes (%s %s); connect once with `ssh %s` to add it",
				c.hc.Alias, key.Type(), fingerprint, c.hc.Alias)}
		case c.acceptNew || c.hc.StrictHostKeyChecking == "accept-new" || c.hc.StrictHostKeyChecking == "no":
			return c.add(key)
		case c.prompter == nil:
			return &Error{Kind: ErrHostKey, Msg: fmt.Sprintf(
				"host key for %s is unknown (%s %s); run pfwd in a terminal to confirm it, pass --accept-new, or connect once with `ssh %s`",
				c.hc.Alias, key.Type(), fingerprint, c.hc.Alias)}
		}

		c.beforePrompt()
		ok, err := c.prompter.ConfirmHostKey(ctx, HostKeyQuestion{Host: c.hc.Alias, KeyType: key.Type(), Fingerprint: fingerprint})
		c.afterPrompt()
		if err != nil {
			return &Error{Kind: ErrHostKey, Err: err, Msg: fmt.Sprintf("host key for %s was not confirmed: %v", c.hc.Alias, err)}
		}
		if !ok {
			return &Error{Kind: ErrHostKey, Msg: fmt.Sprintf(
				"host key for %s was not trusted, so pfwd did not connect; known_hosts is unchanged", c.hc.Alias)}
		}
		return c.add(key)
	}
}

func (c *hostKeyChecker) changedError(err error, key ssh.PublicKey) error {
	where := "known_hosts"
	var keyErr *xknownhosts.KeyError
	if errors.As(err, &keyErr) && len(keyErr.Want) > 0 {
		file := keyErr.Want[0].Filename
		if orig, ok := c.names[file]; ok {
			file = orig
		}
		where = fmt.Sprintf("%s:%d", file, keyErr.Want[0].Line)
	}
	name := knownhosts.Normalize(c.lookup)
	return &Error{Kind: ErrHostKey, Err: err, Msg: fmt.Sprintf(
		"host key for %s has changed: the server sent %s %s, which does not match %s. Someone may be intercepting the connection. If the server was reinstalled, remove the old key with `ssh-keygen -R %q` and connect again",
		c.hc.Alias, key.Type(), ssh.FingerprintSHA256(key), where, name)}
}

// add appends the key to the user's known_hosts file (the first one listed),
// creating the file and its directory when needed. With no user file
// (UserKnownHostsFile none) the key is trusted for this connection only.
func (c *hostKeyChecker) add(key ssh.PublicKey) error {
	if c.hc.NoUserKnownHosts {
		return nil
	}
	if len(c.hc.KnownHostsFiles) == 0 {
		return &Error{Kind: ErrHostKey, Msg: fmt.Sprintf(
			"host key for %s is unknown and no known_hosts file is configured; set UserKnownHostsFile in ~/.ssh/config", c.hc.Alias)}
	}
	path := c.hc.KnownHostsFiles[0]
	name := knownhosts.Normalize(c.lookup)
	if c.hc.HashKnownHosts {
		name = xknownhosts.HashHostname(name)
	}
	if err := appendKnownHost(path, knownhosts.Line([]string{name}, key)); err != nil {
		return &Error{Kind: ErrHostKey, Err: err, Msg: fmt.Sprintf(
			"could not save the host key for %s to %s: %v; check that the file is writable", c.hc.Alias, path, err)}
	}
	return nil
}

func appendKnownHost(path, line string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	// Start on a new line if the file does not end with one.
	if info, err := f.Stat(); err == nil && info.Size() > 0 {
		last := make([]byte, 1)
		if _, err := f.ReadAt(last, info.Size()-1); err != nil && !errors.Is(err, io.EOF) {
			return err
		}
		if last[0] != '\n' {
			line = "\n" + line
		}
	} else if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	_, err = f.WriteString(line + "\n")
	return err
}
