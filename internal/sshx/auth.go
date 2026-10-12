package sshx

import (
	"errors"
	"fmt"
	"io/fs"
	"os"

	"golang.org/x/crypto/ssh"
)

// keyFile is one IdentityFile entry that exists on disk.
type keyFile struct {
	path   string
	signer ssh.Signer    // nil when encrypted
	pub    ssh.PublicKey // nil when unknown (an encrypted PEM key without a .pub file)
	// encrypted keys need a passphrase.
	encrypted bool
}

// loadKeyFiles reads the identity files that exist, and the certificates
// stored next to them as <path>-cert.pub (OpenSSH loads those on its own).
// Missing files are skipped silently, like OpenSSH does for its default
// list; other problems become notes for the auth error message. Key
// material is never logged.
func loadKeyFiles(paths []string) (files []keyFile, certs []*ssh.Certificate, notes []string) {
	for _, p := range paths {
		data, err := os.ReadFile(p)
		if err != nil {
			if !errors.Is(err, fs.ErrNotExist) {
				notes = append(notes, fmt.Sprintf("could not read %s: %v", p, err))
			}
			continue
		}
		signer, err := ssh.ParsePrivateKey(data)
		var missing *ssh.PassphraseMissingError
		switch {
		case errors.As(err, &missing):
			pub := missing.PublicKey
			if pub == nil {
				pub = readPublicKey(p + ".pub")
			}
			files = append(files, keyFile{path: p, pub: pub, encrypted: true})
		case err != nil:
			notes = append(notes, fmt.Sprintf("could not use %s: %v", p, err))
			continue
		default:
			files = append(files, keyFile{path: p, signer: signer, pub: signer.PublicKey()})
		}
		if cert, ok := readPublicKey(p + "-cert.pub").(*ssh.Certificate); ok {
			certs = append(certs, cert)
		}
	}
	return files, certs, notes
}

// loadCertFiles reads CertificateFile entries. Unreadable or non-certificate
// files become notes.
func loadCertFiles(paths []string) (certs []*ssh.Certificate, notes []string) {
	for _, p := range paths {
		if _, err := os.Stat(p); err != nil {
			if !errors.Is(err, fs.ErrNotExist) {
				notes = append(notes, fmt.Sprintf("could not read %s: %v", p, err))
			}
			continue
		}
		cert, ok := readPublicKey(p).(*ssh.Certificate)
		if !ok {
			notes = append(notes, fmt.Sprintf("%s is not an SSH certificate", p))
			continue
		}
		certs = append(certs, cert)
	}
	return certs, notes
}

func readPublicKey(path string) ssh.PublicKey {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	pub, _, _, _, err := ssh.ParseAuthorizedKey(data)
	if err != nil {
		return nil
	}
	return pub
}

// asCert returns pub as a certificate, if it is one. Keys from ssh-agent
// arrive as *agent.Key whatever their type, so the wire form is parsed.
func asCert(pub ssh.PublicKey) (*ssh.Certificate, bool) {
	if cert, ok := pub.(*ssh.Certificate); ok {
		return cert, true
	}
	parsed, err := ssh.ParsePublicKey(pub.Marshal())
	if err != nil {
		return nil, false
	}
	cert, ok := parsed.(*ssh.Certificate)
	return cert, ok
}

// keyID identifies the key pair behind a public key: a certificate counts
// as the key it certifies.
func keyID(pub ssh.PublicKey) string {
	if cert, ok := asCert(pub); ok {
		return string(cert.Key.Marshal())
	}
	return string(pub.Marshal())
}

// orderSigners decides which keys are offered, in order: agent keys that
// match an identity file or certificate, then the other agent keys (unless
// IdentitiesOnly), then identity files the agent does not hold. Each key is
// offered with its certificates first, as OpenSSH does. All of them go into
// one publickey method, because x/crypto stops trying a method name once it
// has failed. unlock turns an encrypted key with a known public key into a
// signer that asks for its passphrase; nil skips encrypted keys with a note.
func orderSigners(hc *HostConfig, agentKeys []ssh.Signer, files []keyFile, certs []*ssh.Certificate, unlock func(keyFile) ssh.Signer) (signers []ssh.Signer, notes []string) {
	configured := make(map[string]bool)
	for _, f := range files {
		if f.pub != nil {
			configured[keyID(f.pub)] = true
		}
	}
	certsFor := make(map[string][]*ssh.Certificate)
	for _, c := range certs {
		id := keyID(c)
		certsFor[id] = append(certsFor[id], c)
		configured[id] = true
	}
	inAgent := make(map[string]bool)
	for _, s := range agentKeys {
		inAgent[keyID(s.PublicKey())] = true
	}

	offered := make(map[string]bool)
	offer := func(s ssh.Signer) {
		if _, isCert := asCert(s.PublicKey()); !isCert {
			for _, c := range certsFor[keyID(s.PublicKey())] {
				if cs, err := ssh.NewCertSigner(c, s); err == nil && !offered[string(cs.PublicKey().Marshal())] {
					offered[string(cs.PublicKey().Marshal())] = true
					signers = append(signers, cs)
				}
			}
		}
		if k := string(s.PublicKey().Marshal()); !offered[k] {
			offered[k] = true
			signers = append(signers, s)
		}
	}

	for _, s := range agentKeys {
		if configured[keyID(s.PublicKey())] {
			offer(s)
		}
	}
	if !hc.IdentitiesOnly {
		for _, s := range agentKeys {
			if !configured[keyID(s.PublicKey())] {
				offer(s)
			}
		}
	}
	for _, f := range files {
		if f.pub != nil && inAgent[keyID(f.pub)] {
			continue
		}
		if f.encrypted {
			if unlock != nil && f.pub != nil {
				offer(unlock(f))
				continue
			}
			notes = append(notes, fmt.Sprintf(
				"%s is passphrase-protected; run `ssh-add %s` to unlock it in ssh-agent", f.path, f.path))
			continue
		}
		offer(f.signer)
	}
	return signers, notes
}
