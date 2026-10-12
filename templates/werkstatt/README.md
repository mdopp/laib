# werkstatt

The one dev container (docs/DESIGN.md "Werkstatt"). v0 carries what
ServiceBay's `claude-dev` SSH path carried: a persistent `/workspace` with the
checkouts, Claude Code, pi, gh, git, tmux and sshd on port 2222 for the
Claude Code mobile app. Every session gets Laib as an MCP server as the
`claude` hand.

v1 (issue #8): the autoloop runs as a second container of the same image
(`werkstatt-autoloop`): pi works tickets labelled `pi:ready` in
`AUTOLOOP_REPOS`, opens a PR, and a ticket it cannot finish gets the label
`needs-claude` with the protocol attached, so the claude hand picks it up.
A transient failure (no clone, the preset refused, pi not startable) keeps
`pi:ready` and is tried again on later passes, three times, then it is
handed over with "Versuch 3 von 3" (#44). `needs-claude` is consumed by a
person or a Claude Code session in the dev container
(`gh issue list -l needs-claude`); nothing automatic reads it. `laib` is a
shell command in the container (laibctl: the Laib MCP with the token from
`/run/laib/token`), `podlet` turns a compose file into Quadlet units, and
the pi extension `laib-llama` keeps pi's model list in step with the
lease: `extensions/catalog.mjs` turns the lease's `/v1/models` (id,
`allowed_in_mode`, no `pi` object) into pi models and defines the provider
id once; the autoloop pins `laib-llama/<preset>` and a test checks both
agree and lists the lease's real catalogue through the extension.

**Tokens per container (#44).** Each container writes its own tokens to
its own tmpfs `/run/laib` (`Tmpfs=` in both units): the dev container
holds `LAIB_CLAUDE_TOKEN` (hand `claude`, the TTL `policy.yml` declares —
`laib token mint claude --note werkstatt` again and `laib secret set
LAIB_CLAUDE_TOKEN` when it runs out) and `WERKSTATT_GIT_TOKEN`; the
autoloop holds `LAIB_PI_TOKEN` (hand `pi`) and `WERKSTATT_PI_GIT_TOKEN`, a
fine-grained PAT limited to `AUTOLOOP_REPOS` (Contents rw for the branch
push and the claim ref, Issues r, Pull requests rw). Nothing with a token
touches the shared `/workspace` volume, so restarting the two in either
order cannot swap hands; `laib journal` shows `pi` for the autoloop's notes
and `claude` for the dev container's. Still open: the `dev.<domain>` web
surface (pi-web's UI), which arrives with Haus; LDAP logins; per-project
tokens.

Migration from `claude-dev`: point `${DATA_DIR}/werkstatt/workspace` at the
old `/workspace` volume and the checkouts and `~/.claude` history carry over.
