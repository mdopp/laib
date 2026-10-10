# werkstatt

The one dev container (docs/DESIGN.md "Werkstatt"). v0 carries what
ServiceBay's `claude-dev` SSH path carried: a persistent `/workspace` with the
checkouts, Claude Code, pi, gh, git, tmux and sshd on port 2222 for the
Claude Code mobile app. Every session gets Laib as an MCP server as the
`claude` hand.

v1 (issue #8): the autoloop runs as a second container of the same image
(`werkstatt-autoloop`): pi works tickets labelled `pi:ready` in
`AUTOLOOP_REPOS`, opens a PR, and a ticket it cannot finish gets the label
`needs-claude` with the protocol attached, so the claude hand picks it up;
pi never retries. `laib` is a shell command in the container (laibctl: the
Laib MCP with the token from `/workspace/.laib/token`), `podlet` turns a
compose file into Quadlet units, and the pi extension `laib-llama` keeps
pi's model list in step with the lease. Still open: the `dev.<domain>` web
surface (pi-web's UI), which arrives with Haus; LDAP logins; per-project
tokens.

Migration from `claude-dev`: point `${DATA_DIR}/werkstatt/workspace` at the
old `/workspace` volume and the checkouts and `~/.claude` history carry over.
