// Package sshx connects to SSH hosts the way OpenSSH would. It resolves a
// host alias through `ssh -G` (or the ssh_config parser when OpenSSH is not
// installed), authenticates with ssh-agent and key files, and verifies host
// keys against known_hosts, asking before it trusts an unknown host.
package sshx
