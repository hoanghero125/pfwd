<!--
The PR title becomes the squash-merge commit message, so it must follow
Conventional Commits 1.0.0:  <type>(<optional scope>)<optional !>: <description>
Types: feat, fix, docs, refactor, perf, test, build, ci, chore, revert
Examples: "feat(tui): add port filter", "fix(sshx)!: drop agent fallback"
-->

## Summary

<!-- What does this PR change, and why? Link the issue it resolves. -->

Closes #

## How was this tested?

<!-- Commands you ran, the OS(es) you ran them on, and the remote host type if relevant. -->

## Breaking changes

<!-- Describe any breaking change and how users migrate, or write "None". A breaking change also needs "!" in the PR title. -->

## Checklist

- [ ] PR title follows [Conventional Commits](https://www.conventionalcommits.org/en/v1.0.0/)
- [ ] `make lint test` passes locally
- [ ] Tests added or updated (or this PR explains why none are needed)
- [ ] Docs updated (`README.md`, `docs/`, command `--help` text) where behavior changed
- [ ] Related items in the spec checklist (`docs/SPEC.md`) are ticked
- [ ] No secrets, private hostnames or personal data in code, tests, fixtures or screenshots
