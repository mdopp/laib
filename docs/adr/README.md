# Laib ADRs

Status · Context · Decision · Consequences. One file per decision, next free
number: **0030**. Each file's first paragraph after the title says *in which
situation* it applies, so an agent can self-select.

| # | Decision |
|---|---|
| [0001](0001-agent-first-state-repo-one-verb.md) | Agent-first: a Git state repo is the truth, `plan`/`apply` is the one verb, no LLM code in the kernel |
| [0002](0002-quadlet-native-single-binary-go.md) | systemd runs the containers via Quadlet; Laib is one static Go binary on the host |
| [0003](0003-no-ssh-agent-laib-runs-on-the-host.md) | No SSH remote agent: Laib runs on the host and reads podman/journald directly; one box |
| [0004](0004-templates-are-contracts.md) | Templates are Quadlet files plus a `laib.yml` contract, not forms and generators |
| [0005](0005-test-pyramid-verify-in-apply.md) | Golden renderers, one booting integration job, verify inside apply; no gates until recurrence (amended: the cap is 16k; #309: the kernel structured, programs out with their own budgets, the cap 15k) |
| [0006](0006-identities-hands-services-age-key.md) | Three kinds of identity: humans via SSO, four hand tokens, service secrets; one `age` key |
| [0007](0007-caddy-replaces-npm.md) | Caddy replaces Nginx Proxy Manager; the Caddyfile is rendered from state |
| [0008](0008-unmanaged-is-drift.md) | An unmanaged unit is drift: adopt or ignore, no bundle builder |
| [0009](0009-hermes-threshold.md) | Hermes is not adopted for voice; superseded for text by 0016 |
| [0010](0010-one-migration-no-compat.md) | One user, one migration, no compatibility layer (amended: carried out 2026-10-19, its tooling removed 2026-10-22) |
| [0011](0011-model-server-is-a-laib-role.md) | The model server (llama-server, router, policy proxy, lease) is a Laib platform role, Solaris is a client |
| [0012](0012-one-approval-surface-and-the-trust-boundary.md) | One approval surface (the box page); the host shell is the human; pages trust only the proxy's secret header |
| [0013](0013-the-agent-is-the-operator.md) | The agent is the operator: observe unbounded, correct inside an automatic envelope, ask only for the irreversible |
| [0014](0014-operator-solaris-haus-are-three-programs.md) | The Operator (box agent), Solaris (household agent) and Haus (pages, cards, companion API) are three programs; the Solaris part superseded by 0015/0016 |
| [0015](0015-voice-is-ha-assist-solaris-retired.md) | Proposed: voice is HA Assist + Stimme + Frage; Solaris the agent is retired, its memory becomes a Haus library; Haus's Sprechen field goes to the Bote (amended) |
| [0016](0016-the-bote-hermes-as-stock.md) | The Bote: the agent you talk to on Signal, Matrix, Telegram or Discord, Hermes as stock, approval by verified Signal number or Matrix ID (amended) |
| [0017](0017-data-lives-in-standard-backends.md) | Data lives in standard backends (Radicale, Markdown files, Paperless, HA); agents are clients through one Haus MCP source; the Bote remembers about you, never for you (amended #561: the household's address is box.yml `zuhause`, changed by a class-human proposal, never geocoded by Laib) |
| [0018](0018-two-boxes-are-a-role-federation-not-a-cluster.md) | Parked: two boxes would be a role federation, not a cluster; in use from it: a remote model URL for a box without a GPU |
| [0019](0019-calm-before-autonomy-and-the-trust-kernel.md) | Calm before autonomy: the trial proves five enforced invariants, three visible guarantees, updates as proposals, an outside watchdog and plain-language decisions before the Operator gets mutating verbs |
| [0020](0020-personas.md) | Personas: the house's character (name, wake word, style, voice, gimmicks, icon) is a package; one house persona for voice and Haus, chat personas per person; a persona is style, never rules; Joshua is the first |
| [0021](0021-names.md) | Names: one name, one thing; the family sees the persona; services are German nouns, mechanisms English, Bote tools named for the system they reach; legacy names only in a named place with an end condition |
| [0022](0022-the-app-is-a-widget-client-messages-go-through-chat.md) | The app is a widget client, built new; widgets stay current through a lean live stream while the screen is on; messages go to a person through their chat channel via the Bote; nothing of the Solaris app is carried |
| [0023](0023-the-operator-has-its-own-budget-and-its-work-comes-in-skills.md) | The Operator has its own line budget (3,000, outside the kernel's cap, 15k since #309); its work comes in skills — declared files, active or paused in `policy.yml`, rights checked by the kernel |
| [0024](0024-the-host-os-is-a-prebuilt-image-proven-at-boot.md) | Proposed: the host OS is a prebuilt image (the NVIDIA module built in CI, not on the box); the CDI spec at every boot; every new deployment proven or rolled back once |
| [0025](0025-the-operator-reads-the-web-and-never-acts-on-it.md) | The Operator reads the web through the box's own search (Frage's SearXNG), entry points first, only in sessions that can only propose; a proposal resting on it is a human's |
| [0026](0026-household-verbs-live-outside-the-kernel.md) | Household verbs (scan, zuhause, person_*, calendar, net geraete) live outside the kernel: the kernel offers proposals scoped to box.yml/values paths and a verb door to programs that can only read and propose |
| [0027](0027-only-what-touches-the-host-runs-on-the-host.md) | Only what touches the host runs on the host (laib, laib-disk, self-update, backup); Operator, notify, haushalt and lease run as containers with their own token and folders, never the age key or the state repo |
| [0028](0028-documents-are-their-own-program.md) | Documents are their own program, Dokumente (scan, split, contracts and invoices, reminders, retry, one status), in its own repo; laib only runs it (the scan template) and keeps the box's part |
| [0029](0029-one-message-archive-per-person.md) | One message archive per person: msgvault installed once per person, one LAN name `archiv.<domain>` routed by the signed-in person, each archive in a vault only its person opens, phone backups through Haus into `archiv-eingang/<person>/`; amended: a Briefkasten imports while the vault is closed, the latest backup stays for restoring a phone |
