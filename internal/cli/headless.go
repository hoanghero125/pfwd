package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"golang.org/x/term"

	"github.com/hoanghero125/pfwd/internal/engine"
	"github.com/hoanghero125/pfwd/internal/safetext"
	"github.com/hoanghero125/pfwd/internal/sshx"
)

// errInterrupted ends a command the user interrupted (exit 130).
var errInterrupted = errors.New("interrupted")

// textPrompter answers the engine's prompts on the terminal: y/N for an
// unknown host key, hidden input for secrets. Without a terminal it
// declines and keeps the reason, with its fix, as the command's error.
type textPrompter struct {
	interactive bool
	out         io.Writer
	err         error
	// readLine and readSecret read the answer from the terminal; tests
	// replace them.
	readLine   func() (string, error)
	readSecret func() ([]byte, error)
	// restore puts the terminal back as it was before a read, after an
	// interrupt left ReadPassword with echo off.
	restore func()
}

// newTextPrompter asks on in and out when both are terminals.
func newTextPrompter(in io.Reader, out io.Writer) *textPrompter {
	p := &textPrompter{out: out}
	fin, inOK := in.(*os.File)
	fout, outOK := out.(*os.File)
	if !inOK || !outOK || !term.IsTerminal(int(fin.Fd())) || !term.IsTerminal(int(fout.Fd())) {
		return p
	}
	fd := int(fin.Fd())
	p.interactive = true
	p.readLine = func() (string, error) { return readTerminalLine(fin) }
	p.readSecret = func() ([]byte, error) { return term.ReadPassword(fd) }
	if state, err := term.GetState(fd); err == nil {
		p.restore = func() { _ = term.Restore(fd, state) }
	}
	return p
}

// ask runs read, unless ctx ends first. The read then goes on in the
// background until the process exits, which it does at once: a terminal
// read can't be canceled. The terminal is restored, since an interrupted
// ReadPassword leaves echo off.
func ask[T any](ctx context.Context, t *textPrompter, read func() (T, error)) (T, error) {
	type result struct {
		v   T
		err error
	}
	got := make(chan result, 1)
	go func() {
		v, err := read()
		got <- result{v, err}
	}()
	select {
	case r := <-got:
		return r.v, r.err
	case <-ctx.Done():
		if t.restore != nil {
			t.restore()
		}
		fmt.Fprintln(t.out)
		var zero T
		return zero, errInterrupted
	}
}

// hostKey asks whether to trust an unknown host key.
func (t *textPrompter) hostKey(ctx context.Context, p engine.HostKeyPrompt) {
	q := p.Question
	host := safetext.Clean(q.Host)
	if !t.interactive {
		t.err = fmt.Errorf("%w: %s's host key (%s %s) is not in known_hosts, and there is no terminal to ask; run `pfwd %s` or `ssh %s` once in a terminal, or pass --accept-new",
			sshx.ErrHostKey, host, q.KeyType, q.Fingerprint, host, host)
		p.Answer(false)
		return
	}
	fmt.Fprintf(t.out, "The authenticity of host %s can't be established.\n%s key fingerprint is %s.\n", host, q.KeyType, q.Fingerprint)
	if q.Saves {
		fmt.Fprint(t.out, "Trust it, save it to known_hosts and connect? [y/N] ")
	} else {
		fmt.Fprint(t.out, "Trust it for this connection? [y/N] ")
	}
	answer, err := ask(ctx, t, t.readLine)
	if errors.Is(err, errInterrupted) {
		t.err = errInterrupted
	}
	answer = strings.ToLower(answer)
	p.Answer(err == nil && (answer == "y" || answer == "yes"))
}

// credential asks for a passphrase, a password or a one-time code.
func (t *textPrompter) credential(ctx context.Context, p engine.CredentialPrompt) {
	q := p.Question
	host := safetext.Clean(q.Host)
	var prompt string
	switch q.Kind {
	case sshx.Passphrase:
		prompt = fmt.Sprintf("Passphrase for %s: ", safetext.Clean(q.Prompt))
	case sshx.Password:
		prompt = fmt.Sprintf("Password for %s@%s: ", safetext.Clean(q.Prompt), host)
	default:
		prompt = safetext.Clean(q.Prompt)
	}
	if !t.interactive {
		t.err = fmt.Errorf("%w: %s asks %q, and there is no terminal to answer; add the key to ssh-agent, or run the command in a terminal",
			sshx.ErrAuth, host, strings.TrimSpace(prompt))
		p.Cancel()
		return
	}
	if q.Retry {
		fmt.Fprintln(t.out, "That was not accepted; try again.")
	}
	if q.Instruction != "" {
		fmt.Fprintln(t.out, safetext.Clean(q.Instruction))
	}
	fmt.Fprint(t.out, prompt)
	var secret []byte
	var err error
	if q.Echo {
		var line string
		line, err = ask(ctx, t, t.readLine)
		secret = []byte(line)
	} else {
		secret, err = ask(ctx, t, t.readSecret)
		if err == nil {
			fmt.Fprintln(t.out)
		}
	}
	if err != nil {
		if errors.Is(err, errInterrupted) {
			t.err = errInterrupted
		}
		p.Cancel()
		return
	}
	p.Answer(secret)
}

// readTerminalLine reads one line from the terminal, a byte at a time so
// nothing is buffered past it.
func readTerminalLine(in *os.File) (string, error) {
	var b strings.Builder
	buf := make([]byte, 1)
	for {
		n, err := in.Read(buf)
		if n == 1 && buf[0] != '\n' {
			b.WriteByte(buf[0])
		}
		if n == 1 && buf[0] == '\n' {
			return strings.TrimSpace(b.String()), nil
		}
		if err != nil {
			return strings.TrimSpace(b.String()), err
		}
	}
}

// runEvents feeds eng's events to handle until it reports done or an
// error, or ctx ends (errInterrupted). Prompts are answered first, on the
// terminal; an interrupt ends one at once.
func runEvents(ctx context.Context, eng *engine.Engine, p *textPrompter, handle func(engine.Event) (bool, error)) error {
	for {
		select {
		case <-ctx.Done():
			return errInterrupted
		case ev := <-eng.Events():
			switch ev := ev.(type) {
			case engine.HostKeyPrompt:
				p.hostKey(ctx, ev)
				continue
			case engine.CredentialPrompt:
				p.credential(ctx, ev)
				continue
			}
			done, err := handle(ev)
			if err != nil || done {
				return err
			}
		}
	}
}

// waitConnected waits until host is connected. A failed connection
// returns the prompter's reason when it declined a prompt, else the
// session's error.
func waitConnected(ctx context.Context, eng *engine.Engine, p *textPrompter, host engine.HostID) error {
	return runEvents(ctx, eng, p, func(ev engine.Event) (bool, error) {
		s, ok := ev.(engine.SessionChanged)
		if !ok || s.Host != host {
			return false, nil
		}
		switch s.State {
		case engine.Connected:
			return true, nil
		case engine.SessionFailed, engine.Closed:
			if p.err != nil {
				return true, p.err
			}
			if s.Err != nil {
				return true, s.Err
			}
			return true, fmt.Errorf("the connection to %s ended", host)
		}
		return false, nil
	})
}

// noForwards drops the config's always-on forwards: ls and fwd create only
// what they are asked for.
func noForwards(base func(engine.HostID) engine.HostSettings) func(engine.HostID) engine.HostSettings {
	return func(host engine.HostID) engine.HostSettings {
		s := base(host)
		s.Forwards = nil
		return s
	}
}
