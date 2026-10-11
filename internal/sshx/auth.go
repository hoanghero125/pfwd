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
	// encrypted keys need a passphrase, which pfwd does not ask for yet.
	encrypted bool
}

// loadKeyFiles reads the identity files that exist. Missing files are
// skipped silently, like OpenSSH does for its default list; other problems
// become notes for the auth error message. Key material is never logged.
func loadKeyFiles(paths []string) (files []keyFile, notes []string) {
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
		default:
			files = append(files, keyFile{path: p, signer: signer, pub: signer.PublicKey()})
		}
	}
	return files, notes
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

// orderSigners decides which keys are offered, in order: agent keys that
// match an identity file, then the other agent keys (unless
// IdentitiesOnly), then identity files the agent does not hold. All of
// them go into one publickey method, because x/crypto stops trying a method
// name once it has failed.
func orderSigners(hc *HostConfig, agentKeys []ssh.Signer, files []keyFile) (signers []ssh.Signer, notes []string) {
	isFileKey := make(map[string]bool)
	for _, f := range files {
		if f.pub != nil {
			isFileKey[string(f.pub.Marshal())] = true
		}
	}
	inAgent := make(map[string]bool)
	for _, s := range agentKeys {
		inAgent[string(s.PublicKey().Marshal())] = true
	}

	for _, s := range agentKeys {
		if isFileKey[string(s.PublicKey().Marshal())] {
			signers = append(signers, s)
		}
	}
	if !hc.IdentitiesOnly {
		for _, s := range agentKeys {
			if !isFileKey[string(s.PublicKey().Marshal())] {
				signers = append(signers, s)
			}
		}
	}
	for _, f := range files {
		if f.pub != nil && inAgent[string(f.pub.Marshal())] {
			continue
		}
		if f.encrypted {
			notes = append(notes, fmt.Sprintf(
				"%s is passphrase-protected; run `ssh-add %s` to unlock it in ssh-agent (pfwd asks for passphrases from v0.3.0)", f.path, f.path))
			continue
		}
		signers = append(signers, f.signer)
	}
	return signers, notes
}
