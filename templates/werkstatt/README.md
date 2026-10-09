# werkstatt

The one dev container (docs/DESIGN.md "Werkstatt"). v0 carries what
ServiceBay's `claude-dev` SSH path carried: a persistent `/workspace` with the
checkouts, Claude Code, pi, gh, git, tmux and sshd on port 2222 for the
Claude Code mobile app. Every session gets Laib as an MCP server as the
`claude` hand.

Not yet (v1, issue #8): the `dev.<domain>` web surface, the autoloop with the
pi-first / `needs-claude` router, LDAP logins, per-project tokens.

Migration from `claude-dev`: point `${DATA_DIR}/werkstatt/workspace` at the
old `/workspace` volume and the checkouts and `~/.claude` history carry over.
