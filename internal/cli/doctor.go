package cli

import (
	"bytes"
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/hoanghero125/pfwd/internal/detect"
	"github.com/hoanghero125/pfwd/internal/engine"
	"github.com/hoanghero125/pfwd/internal/forward"
	"github.com/hoanghero125/pfwd/internal/safetext"
	"github.com/hoanghero125/pfwd/internal/sshx"
)

// Check statuses.
const (
	checkOK   = "ok"
	checkWarn = "warn"
	checkFail = "fail"
)

// check is one line of pfwd doctor.
type check struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Detail string `json:"detail"`
	Fix    string `json:"fix"`
}

// doctorReport is what pfwd doctor found. code is the exit code its first
// failure maps to.
type doctorReport struct {
	Host   string  `json:"host"`
	Checks []check `json:"checks"`
	code   int
}

func (r *doctorReport) add(name, status, detail, fix string) {
	r.Checks = append(r.Checks, check{Name: name, Status: status, Detail: detail, Fix: fix})
}

// fail adds a failed check; the first one decides the exit code.
func (r *doctorReport) fail(name, detail, fix string, code int) {
	r.add(name, checkFail, detail, fix)
	if r.code == exitOK {
		r.code = code
	}
}

// silentExit ends a command with code after it printed its own report.
type silentExit struct{ code int }

func (e *silentExit) Error() string { return "exit status " + strconv.Itoa(e.code) }

type doctorFlags struct {
	bundle bool
	output string
}

func newDoctorCmd(f *rootFlags) *cobra.Command {
	var df doctorFlags
	cmd := &cobra.Command{
		Use:   "doctor [host]",
		Short: "Diagnose config, auth, detection and port binding for a host",
		Long: `pfwd doctor <host> checks, in order: how the ssh config resolves the host,
whether it answers, its host key, the login, port detection, -L and -R
forwarding, and listening here. Each line says what it found and, when
something is wrong, how to fix it. It never writes known_hosts.

--bundle writes a zip for a bug report: the version, your config, a summary
of remembered forwards, the end of the log and, with a host, this report.
Secrets and your home directory are taken out; check it before sharing.`,
		Example:           "  pfwd doctor devbox\n  pfwd doctor devbox --json\n  pfwd doctor --bundle devbox",
		Args:              usageArgs(cobra.MaximumNArgs(1)),
		ValidArgsFunction: completeHosts(f, true),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 && !df.bundle {
				return &usageError{errors.New("name a host, like `pfwd doctor devbox`, or pass --bundle")}
			}
			c, err := loadCommon(cmd, f)
			if err != nil {
				return err
			}
			var report *doctorReport
			var text bytes.Buffer
			if len(args) == 1 {
				ctx, stop := interruptible(cmd.Context())
				defer stop()
				r := runDoctor(ctx, cmd, f, c, args[0])
				report = &r
				printDoctor(io.MultiWriter(cmd.OutOrStdout(), &text), r, wantJSON(cmd) && !df.bundle)
			}
			if df.bundle {
				path, err := writeBundle(df.output, c, text.String())
				if err != nil {
					return err
				}
				fmt.Fprintf(cmd.OutOrStdout(), "Wrote %s. Look inside before you share it.\n", path)
			}
			if report != nil && report.code != exitOK {
				return &silentExit{report.code}
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&df.bundle, "bundle", false, "write a zip of diagnostics for a bug report")
	cmd.Flags().StringVar(&df.output, "output", "", "where --bundle writes the zip (default pfwd-doctor-<time>.zip here)")
	return cmd
}

// printDoctor prints the report as lines with a mark each, or as JSON.
func printDoctor(w io.Writer, r doctorReport, asJSON bool) {
	if asJSON {
		_ = writeJSON(w, r)
		return
	}
	marks := map[string]string{checkOK: "✓", checkWarn: "!", checkFail: "✗"}
	if legacyConsole(os.Getenv, runtime.GOOS) {
		marks = map[string]string{checkOK: "+", checkWarn: "!", checkFail: "x"}
	}
	fmt.Fprintf(w, "pfwd doctor %s\n", safetext.Clean(r.Host))
	for _, c := range r.Checks {
		fmt.Fprintf(w, "%s %-12s %s\n", marks[c.Status], c.Name, safetext.Clean(c.Detail))
		if c.Fix != "" {
			fmt.Fprintf(w, "  %-12s fix: %s\n", "", safetext.Clean(c.Fix))
		}
	}
}

// doctorPrompts answers what a doctor login needs: secrets on the
// terminal, and never trust in an unknown host key, so known_hosts stays
// as it is.
type doctorPrompts struct {
	t     *textPrompter
	asked bool
}

func (p *doctorPrompts) ConfirmHostKey(context.Context, sshx.HostKeyQuestion) (bool, error) {
	p.asked = true
	return false, nil
}

func (p *doctorPrompts) AskCredential(ctx context.Context, q sshx.CredentialQuestion) ([]byte, error) {
	reply := make(chan engine.CredentialAnswer, 1)
	p.t.credential(engine.NewCredentialPrompt(engine.HostID(q.Host), q, reply))
	select {
	case a := <-reply:
		if a.Canceled {
			return nil, sshx.ErrCanceled
		}
		return a.Secret, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// runDoctor runs the checks in order, stopping after a failure that leaves
// nothing more to check.
func runDoctor(ctx context.Context, cmd *cobra.Command, f *rootFlags, c common, target string) doctorReport {
	r := doctorReport{Host: target}
	logger, closeLog := headlessLog(c.level, cmd.ErrOrStderr())
	defer closeLog()
	d := newHostDialer(c.cfg, f, logger)
	defer d.close()

	host, err := d.add(ctx, target, true)
	if err != nil {
		r.fail("ssh config", err.Error(), "check the Host entry, or name another config with -F", exitCode(err))
		return r
	}
	r.Host = string(host)
	hc := d.hostConfig(host)
	r.add("ssh config", checkOK, configDetail(hc, c), "")

	if hc.ProxyJump == "" && hc.ProxyCommand == "" {
		addr := net.JoinHostPort(hc.HostName, strconv.Itoa(hc.Port))
		start := time.Now()
		nc, err := net.DialTimeout("tcp", addr, hc.ConnectTimeout)
		if err != nil {
			r.fail("reach", fmt.Sprintf("%s doesn't answer: %v", addr, err), "check the host name and port, your network or VPN, and that sshd runs there", exitConnect)
			return r
		}
		_ = nc.Close()
		r.add("reach", checkOK, fmt.Sprintf("%s answers (%d ms)", addr, time.Since(start).Milliseconds()), "")
	} else {
		r.add("reach", checkOK, "through the jump host or ProxyCommand; the login below tests it", "")
	}

	prompts := &doctorPrompts{t: newTextPrompter(cmd.InOrStdin(), cmd.ErrOrStderr())}
	start := time.Now()
	conn, err := d.Dial(ctx, host, prompts)
	switch {
	case err == nil:
		defer func() { _ = conn.Close() }()
		r.add("host key", checkOK, "matches known_hosts", "")
		r.add("login", checkOK, fmt.Sprintf("logged in as %s (%d ms)", hc.User, time.Since(start).Milliseconds()), "")
	case errors.Is(err, sshx.ErrHostKey) && prompts.asked:
		r.fail("host key", "this host's key is not in known_hosts yet",
			fmt.Sprintf("connect once with `pfwd %s` or `ssh %s` and check the fingerprint, or pass --accept-new", host, host), exitHostKey)
		return r
	case errors.Is(err, sshx.ErrHostKey):
		r.fail("host key", err.Error(), fmt.Sprintf("if the server was reinstalled, remove its old key with `ssh-keygen -R %s` and connect again", hc.HostName), exitHostKey)
		return r
	case errors.Is(err, sshx.ErrAuth):
		r.add("host key", checkOK, "matches known_hosts", "")
		r.fail("login", err.Error(), "", exitAuth)
		return r
	default:
		r.fail("login", err.Error(), "", exitCode(err))
		return r
	}

	ports := doctorDetect(ctx, conn, &r)
	doctorForwards(ctx, conn, ports, &r)
	doctorBind(c, &r)
	return r
}

// configDetail says where the host resolves to and with what.
func configDetail(hc *sshx.HostConfig, c common) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s is %s@%s:%d", hc.Alias, hc.User, hc.HostName, hc.Port)
	if hc.ProxyJump != "" {
		fmt.Fprintf(&b, " through %s", hc.ProxyJump)
	}
	if hc.ProxyCommand != "" {
		b.WriteString(" through a ProxyCommand")
	}
	var keys []string
	for _, k := range hc.IdentityFiles {
		if _, err := os.Stat(k); err == nil {
			keys = append(keys, k)
		}
	}
	switch {
	case len(keys) > 0:
		fmt.Fprintf(&b, "; keys: %s", strings.Join(keys, ", "))
	case hc.IdentityAgent == "none":
		b.WriteString("; no key file found and the agent is off")
	default:
		b.WriteString("; keys from ssh-agent")
	}
	if c.cfg.SSH.UseOpenSSHConfig {
		if _, err := sshEnv(c.cfg).LookSSH(); err == nil {
			b.WriteString(" (from ssh -G)")
		}
	}
	return b.String()
}

// doctorDetect probes the server and takes one snapshot of its ports.
func doctorDetect(ctx context.Context, conn engine.Conn, r *doctorReport) []detect.RemotePort {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	var first *detect.Update
	err := detect.Watch(ctx, conn, detect.WatchOptions{Interval: time.Second}, func(u detect.Update) {
		if first == nil {
			first = &u
			cancel()
		}
	})
	switch {
	case err != nil:
		r.add("detection", checkWarn, err.Error(), "forwards you name still work; run `pfwd doctor` again with --log-level debug for details")
	case first == nil:
		r.add("detection", checkWarn, "no port list within 15 s", "forwards you name still work")
	case first.Strategy == detect.None:
		r.add("detection", checkWarn, fmt.Sprintf("%s, with no ss, netstat, lsof or /proc", cmp.Or(first.OS, "an unknown OS")),
			"install iproute2 (ss) or net-tools there; forwards you name still work")
	default:
		listening := fmt.Sprintf("%d ports listening", len(first.Ports))
		if len(first.Ports) == 1 {
			listening = "1 port listening"
		}
		r.add("detection", checkOK, fmt.Sprintf("%s, %s: %s", first.OS, first.Strategy, listening), "")
		return first.Ports
	}
	return nil
}

// doctorForwards opens a direct-tcpip channel (-L) to a listening port, and
// a remote listener (-R) that it closes at once.
func doctorForwards(ctx context.Context, conn engine.Conn, ports []detect.RemotePort, r *doctorReport) {
	target := "127.0.0.1:22"
	if len(ports) > 0 {
		target = net.JoinHostPort(detect.Target(ports[0].Addrs), strconv.Itoa(int(ports[0].Port)))
	}
	dctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	nc, err := conn.DialContext(dctx, "tcp", target)
	cancel()
	switch {
	case err == nil:
		_ = nc.Close()
		r.add("-L forwards", checkOK, "the server opens connections for pfwd (to "+target+")", "")
	case errors.Is(err, sshx.ErrForwardingRefused):
		r.fail("-L forwards", "the server refuses TCP forwarding", "set AllowTcpForwarding yes (or local) in the server's sshd_config", exitError)
	default:
		r.add("-L forwards", checkWarn, "could not test: "+err.Error(), "")
	}

	ln, err := conn.Listen("127.0.0.1", 0)
	switch {
	case err == nil:
		_ = ln.Close()
		r.add("-R forwards", checkOK, "the server listens for pfwd", "")
	case errors.Is(err, sshx.ErrForwardingRefused):
		r.add("-R forwards", checkWarn, "the server refuses to listen for pfwd, so `r` and -R won't work", "set AllowTcpForwarding yes (or remote) in the server's sshd_config")
	default:
		r.add("-R forwards", checkWarn, "could not test: "+err.Error(), "")
	}
}

// doctorBind checks that forwards can listen here, and what happens to
// ports below 1024.
func doctorBind(c common, r *doctorReport) {
	bind := c.cfg.Forward.Bind
	ln, err := forward.Listen(bind, 0)
	if err != nil {
		r.fail("local bind", fmt.Sprintf("can't listen on %s: %v", bind, err), "set forward.bind (or --bind) to an address of this machine, like 127.0.0.1", exitError)
		return
	}
	_ = ln.Close()
	low := "ports below 1024 work here"
	if ln, err := forward.Listen(bind, 1023); err == nil {
		_ = ln.Close()
	} else if errors.Is(err, os.ErrPermission) {
		low = fmt.Sprintf("ports below 1024 need root here, so they move up by %d (forward.privileged_offset)", c.cfg.Forward.PrivilegedOffset)
		if c.cfg.Forward.PrivilegedOffset == 0 {
			low = "ports below 1024 need root here, and forward.privileged_offset = 0 keeps them from moving"
		}
	}
	r.add("local bind", checkOK, fmt.Sprintf("forwards can listen on %s; %s", bind, low), "")
}
