# Changelog

## [0.4.3](https://github.com/mdopp/laib/compare/v0.4.2...v0.4.3) (2026-10-13)


### Bug Fixes

* **stick:** the data-disk record survives a power cut, and an empty one adopts again ([#107](https://github.com/mdopp/laib/issues/107)) ([1750ef6](https://github.com/mdopp/laib/commit/1750ef6f57238c0c79e2e7c92c4caf3ccde8fbcb))
* **stick:** the data-disk record survives a power cut, and an empty one adopts again ([#107](https://github.com/mdopp/laib/issues/107)) ([0275082](https://github.com/mdopp/laib/commit/027508229e6278d4aa9f0648441fecd03c3af0bc))

## [0.4.2](https://github.com/mdopp/laib/compare/v0.4.1...v0.4.2) (2026-10-13)


### Bug Fixes

* **firewall:** reloading the ruleset replaces the table; the last dropped port going removes it ([#102](https://github.com/mdopp/laib/issues/102)) ([ab5ed48](https://github.com/mdopp/laib/commit/ab5ed48922e9ca26d556690459ee4b40ec29c722))
* **firewall:** reloading the ruleset replaces the table; the last dropped port going removes it ([#102](https://github.com/mdopp/laib/issues/102)) ([1fd154f](https://github.com/mdopp/laib/commit/1fd154f199cfc77a8acd94500b6a6a2e037c51a5))
* **stick:** first boot requires its data disk ([#107](https://github.com/mdopp/laib/issues/107)) ([c0d701d](https://github.com/mdopp/laib/commit/c0d701d4bfe901f8c613cedfa1f97b63766afc42))
* **stick:** first boot requires its data disk; the VM log keeps the data-disk journal ([#107](https://github.com/mdopp/laib/issues/107)) ([d97e1a0](https://github.com/mdopp/laib/commit/d97e1a0ef2a80d48f3fdac57aec94b939d9dab8c))
* **stick:** the end of the first boot reaches the disk GRUB reads ([#81](https://github.com/mdopp/laib/issues/81)) ([caeae9f](https://github.com/mdopp/laib/commit/caeae9f9ec7097e7abb262fee840a36daa630acf))
* **stick:** the end of the first boot reaches the disk GRUB reads ([#81](https://github.com/mdopp/laib/issues/81)) ([6b5df85](https://github.com/mdopp/laib/commit/6b5df85dd173887fc0e0b8287af19249e92f953f))

## [0.4.1](https://github.com/mdopp/laib/compare/v0.4.0...v0.4.1) (2026-10-13)


### Bug Fixes

* **release:** latest means the newest release with binaries; the gate dispatches vm-install again ([#99](https://github.com/mdopp/laib/issues/99)) ([622599f](https://github.com/mdopp/laib/commit/622599f95f643f47351db6aff226d145aa481927))
* **release:** latest means the newest release with binaries; the gate dispatches vm-install again ([#99](https://github.com/mdopp/laib/issues/99)) ([e00b840](https://github.com/mdopp/laib/commit/e00b840500507a8dea6ecb96efb8dacc5b10e50b))

## [0.4.0](https://github.com/mdopp/laib/compare/v0.3.5...v0.4.0) (2026-10-12)


### Features

* **cards:** every decision shows a plain-German card — consequence, reversibility, who and why, a safe default ([#58](https://github.com/mdopp/laib/issues/58)) ([d0e2af1](https://github.com/mdopp/laib/commit/d0e2af1d8dff9e3301fa40a14f5e8edfd1a58215))
* **cards:** every decision shows a plain-German card — consequence, reversibility, who and why, a safe default ([#58](https://github.com/mdopp/laib/issues/58)) ([72b0227](https://github.com/mdopp/laib/commit/72b0227c01af6a6d3176ffeb1f749e809510c37a))
* **diag:** disk health by SMART for the array's members and the OS disk, the box's temperature ([#59](https://github.com/mdopp/laib/issues/59)) ([7b16e16](https://github.com/mdopp/laib/commit/7b16e166c7d698b136f824f911a719c715bebffb))
* **diag:** SMART for the array's members and the OS disk, the box's temperature ([#59](https://github.com/mdopp/laib/issues/59)) ([58fe227](https://github.com/mdopp/laib/commit/58fe227540550ce48e7f6736e92216236001e5aa))
* **hooks:** hooks run in ghcr.io/mdopp/laib-hooks, pre-start before the first start ([#36](https://github.com/mdopp/laib/issues/36)) ([f6e0ac8](https://github.com/mdopp/laib/commit/f6e0ac88c71f2e3a918004e2aabc5811528f9779))
* **intake:** laib wish files a GitHub issue with the wunsch label, escalations become issues, the autoloop points at a repo that exists ([#56](https://github.com/mdopp/laib/issues/56)) ([245d6ca](https://github.com/mdopp/laib/commit/245d6ca3d3e1a601b581ea975740a2af18860b7d))
* **intake:** laib wish files a GitHub issue, escalations become issues, the autoloop's repo exists ([#56](https://github.com/mdopp/laib/issues/56)) ([09dafe3](https://github.com/mdopp/laib/commit/09dafe34140db1f65cebdbed31749d2fb18ffc36))
* **operator:** a typed, task-shaped surface for small models — a menu per trigger, CLI arguments built in code, invalid calls refused, compact results, the typed eval ([#53](https://github.com/mdopp/laib/issues/53)) ([fe65733](https://github.com/mdopp/laib/commit/fe6573336a329f6b332cc69bd251fd328149cce0))
* **operator:** a typed, task-shaped tool surface with a menu per trigger for small models ([#53](https://github.com/mdopp/laib/issues/53)) ([71239c7](https://github.com/mdopp/laib/commit/71239c7c8581f0cdc848617fd48e2b117e7a3b42))
* **recovery:** the recovery sheet, restore --box, replacing restores, quiesced databases, a monthly restore test ([#40](https://github.com/mdopp/laib/issues/40)) ([ebf85be](https://github.com/mdopp/laib/commit/ebf85bee7d414c2489518c2b66ccd16712dd61a9))
* **recovery:** the recovery sheet, restore --box, replacing restores, quiesced databases, a monthly restore test ([#40](https://github.com/mdopp/laib/issues/40)) ([691e409](https://github.com/mdopp/laib/commit/691e4090021ffbf008d5663ca8dbd2fa5a80fa21))
* **relay:** approval by verified number is code — the relay decides through an approver hand that can only decide; Hermes holds no token that can approve; Automat confirms server-side ([e022725](https://github.com/mdopp/laib/commit/e022725435714805f4e3aa9e9de458034aa6e122))
* **relay:** approval from Signal is decided by code that sees the number — Hermes holds no token that can approve ([66c83e2](https://github.com/mdopp/laib/commit/66c83e25ffae619cdfc35078d14896c313e9aa1f))
* **review:** a proposal is judged by its content, not its shape — what only a human may allow is named, and an agent cannot approve it ([13e8f4c](https://github.com/mdopp/laib/commit/13e8f4cc6af3f2f41918563de2a8aa80ea7b1225))
* **review:** proposals judged by content — what only a human may allow is named, and no agent can approve it ([f18ddd5](https://github.com/mdopp/laib/commit/f18ddd5fb87d4a36c389e8c3d0389845116dd944))
* **routes:** proxySecret — Caddy sends a secret header to the authenticated handler; the haus template holds no human token and a least-privilege hand ([a8b18c5](https://github.com/mdopp/laib/commit/a8b18c58d07bc26ab2511340022c44f3cb7e4888))
* **routes:** proxySecret for pages behind the proxy; the haus template with a least-privilege hand and no human token ([901b6fa](https://github.com/mdopp/laib/commit/901b6fa8dd1e4ee3288fbaaf864fc68e95532f00))
* **status:** readiness carries lastBackup and lastChange — the family's three guarantees (haus[#2](https://github.com/mdopp/laib/issues/2)) ([edf06eb](https://github.com/mdopp/laib/commit/edf06eb8ca83c4d1d2dc1487dec63894fb42c498))
* **status:** readiness carries lastBackup and lastChange — the family's three guarantees (haus[#2](https://github.com/mdopp/laib/issues/2)) ([3f9c315](https://github.com/mdopp/laib/commit/3f9c3153fff06628937140622c5081edeabb07d2))
* **status:** the Operator's heartbeat in readiness, status, diag and the page — enabled but down, silent or unanswering is red or yellow, never invisible ([aaa9cce](https://github.com/mdopp/laib/commit/aaa9cce985fdd7e876634d41ccb483400c896b59))
* **stick:** --source — the first boot installs one build served over HTTP ([b3d05f0](https://github.com/mdopp/laib/commit/b3d05f0915fa996633bade8282db72241c182c51))
* **trial:** real certificates for the trial — stick carries the DNS provider, init writes dns.provider and --tls ([#45](https://github.com/mdopp/laib/issues/45)) ([ecc0dcc](https://github.com/mdopp/laib/commit/ecc0dcc979ab569baee9b8164ac390f3bef118eb))
* **trial:** real certificates for the trial — the stick carries the DNS provider, init writes dns.provider and --tls ([#45](https://github.com/mdopp/laib/issues/45)) ([7664662](https://github.com/mdopp/laib/commit/766466246d34a1f1d8a7e8ba8ddfd76a9c044be3))
* **watchdog:** serve pings an outside watchdog while not red, diag wants one, laib family-card prints the paper card ([#57](https://github.com/mdopp/laib/issues/57)) ([fad4e0f](https://github.com/mdopp/laib/commit/fad4e0f1d25a5716a45eaea41a476f82a73ce1c3))
* **watchdog:** serve pings an outside watchdog while not red, diag wants one, laib family-card prints the paper card ([#57](https://github.com/mdopp/laib/issues/57)) ([77bc49c](https://github.com/mdopp/laib/commit/77bc49c8cdb8cb639b641a938cb5d311993a19c4))


### Bug Fixes

* **apply:** a pod service restarts through its pod unit only; Authelia waits 3 s between tries ([#36](https://github.com/mdopp/laib/issues/36)) ([c5c4ced](https://github.com/mdopp/laib/commit/c5c4ced55fa980b17caca585075eb4f76b8ee688))
* **auth:** post-deploy defines DOMAIN again for the radicale account's mail ([#36](https://github.com/mdopp/laib/issues/36)) ([2627a7e](https://github.com/mdopp/laib/commit/2627a7e301c4d35b56c940fd782b4ff3188f137e))
* **backup:** restic in podman's user namespace — databases were missing from every backup ([#40](https://github.com/mdopp/laib/issues/40)) ([5bf6768](https://github.com/mdopp/laib/commit/5bf6768edcbfa15aab03862cea943d32274c7537))
* **backup:** restic runs as podman unshare restic, so the databases are in every backup and a restore gives their files their owners back ([#40](https://github.com/mdopp/laib/issues/40)) ([9d01578](https://github.com/mdopp/laib/commit/9d01578486b6806a75524f0d204375e1890f1a03))
* **backup:** the console says which service was stopped for the snapshot, not only the journal ([#40](https://github.com/mdopp/laib/issues/40)) ([6e7d76b](https://github.com/mdopp/laib/commit/6e7d76b0da356f5405424467ee8d7113defb7459))
* **boot:** firewall converges at boot, login and first boot survive a reboot, updates restart and roll back ([#37](https://github.com/mdopp/laib/issues/37)) ([6f35f97](https://github.com/mdopp/laib/commit/6f35f97eddfca67837d9f6aafb31131197ed637d))
* **boot:** the boot test's scripted model speaks the typed surface the Operator now defaults to ([#53](https://github.com/mdopp/laib/issues/53)) ([2e25634](https://github.com/mdopp/laib/commit/2e256347cf9255c7219007cfb2628355ead9b5ad))
* **boot:** the firewall converges at boot, the login and the first boot survive a reboot, updates restart and can roll back ([#37](https://github.com/mdopp/laib/issues/37)) ([00eb57e](https://github.com/mdopp/laib/commit/00eb57e4f23075aa1faf0810ccb633b853c5f317))
* **bote:** only MCP servers that answer, the SOUL names only wired tools, Frage as MCP, link QR ([#54](https://github.com/mdopp/laib/issues/54)) ([a0900aa](https://github.com/mdopp/laib/commit/a0900aae9b9e48bb76721bd79c163b8344992a05))
* **bote:** render only MCP servers that answer, the SOUL names only wired tools, Frage as an MCP tool, link QR on the Box page ([#54](https://github.com/mdopp/laib/issues/54)) ([6ec911d](https://github.com/mdopp/laib/commit/6ec911d1f1ae64763ead06ec733a63b1cb195ea6))
* **bote:** the human token's value line leaves values.example.yml too (template lint) ([d97e0e1](https://github.com/mdopp/laib/commit/d97e0e1026fa74eb322a7186c6b7098f4f5b8a4f))
* **disk:** ask lsblk for the member's parent only ([b0fe9b3](https://github.com/mdopp/laib/commit/b0fe9b34e044180f44a88d06a2c00e796d39e907))
* **disk:** laib disk add inspects the device and refuses what it would destroy ([f0a94c9](https://github.com/mdopp/laib/commit/f0a94c9b858606c2db3ad7cdb1b8f6859f581617))
* **envelope:** fail closed — os.Root jail, exec only in the service's own containers, no mutation without a snapshot, a readable journal, a look verb without budget ([5d6b7fe](https://github.com/mdopp/laib/commit/5d6b7fe1dbc3d0dbca2c6abfef65f1290b00d2a7))
* **envelope:** fail closed — os.Root jail, own containers only, no mutation without a snapshot, a look verb without budget ([ccd51f6](https://github.com/mdopp/laib/commit/ccd51f6adde2ee277d0a817275c3462171b9aac2))
* **family:** apps with their own login route auth: none, the route policy is written down, diag checks Home Assistant's integrations ([#55](https://github.com/mdopp/laib/issues/55)) ([f44592c](https://github.com/mdopp/laib/commit/f44592c5a1cc497db76901c77146b7c4d182ad1a))
* **family:** routes for apps with their own logins, radicale with a read-only LDAP bind, HA integration check ([#55](https://github.com/mdopp/laib/issues/55), with [#70](https://github.com/mdopp/laib/issues/70) and the kernel chain) ([e4f1a4f](https://github.com/mdopp/laib/commit/e4f1a4f15b10f18c972574fb29d3dcc6624c10de))
* **hooks:** Authelia's OIDC block only once a client exists; hooks of role holders run when their rendered files change ([#36](https://github.com/mdopp/laib/issues/36)) ([0382541](https://github.com/mdopp/laib/commit/03825411747fce3f21fa1b0abbec5b9fa556e6be))
* **hooks:** pre-start lands executable, a non-executable hook is a red check, the pod unit restarts first ([#36](https://github.com/mdopp/laib/issues/36)) ([3bfec2a](https://github.com/mdopp/laib/commit/3bfec2ae9dbd2b2dfbbef86c039bc7d77e7ea844))
* **install:** install-box.sh stays executable ([fc95e36](https://github.com/mdopp/laib/commit/fc95e36837804617720a76b8148a86239b101bd8))
* **kernel:** restarted roles are verified, the Caddyfile is validated before writing, remove checks dependants, the box approves its own updates once ([#50](https://github.com/mdopp/laib/issues/50)) ([ec61fd6](https://github.com/mdopp/laib/commit/ec61fd6c209a3bf4124f8d29ac3641b61624970e))
* **kernel:** restarted roles verified, Caddyfile validated before writing, remove checks dependants, the box approves its own updates once ([#50](https://github.com/mdopp/laib/issues/50)) ([3726ee0](https://github.com/mdopp/laib/commit/3726ee0c4401caa4dd470f3863cecd623111df1d))
* **lease:** modes name roles, the API wants the model key, narrowest mode, renew or lose the card, visible starvation, one port ([#42](https://github.com/mdopp/laib/issues/42)) ([8921505](https://github.com/mdopp/laib/commit/892150542582ec9de480ced55204e1b3839f778d))
* **lease:** modes name roles, the API wants the model key, renew or lose the card, one port ([#42](https://github.com/mdopp/laib/issues/42)) ([3f99967](https://github.com/mdopp/laib/commit/3f99967c7936441592cbfc52909e9c74bbeec721))
* **lock:** one flock held by every mutating verb, converge skips a foreign lock, pause/resume, pending activation ([#39](https://github.com/mdopp/laib/issues/39)) ([42c6cb6](https://github.com/mdopp/laib/commit/42c6cb678c031ac3dd37197cdcc9cd6822bd964f))
* **lock:** one flock held by every mutating verb, converge skips a foreign lock, pause/resume, restart-only runs count, pending activation ([#39](https://github.com/mdopp/laib/issues/39)) ([0341b51](https://github.com/mdopp/laib/commit/0341b5160bf3ebfc0796f77b74fb57841a44e1a2))
* **merge:** the lint box keeps the real data dir and the e2e podman shim runs only hooks (fix/36 + kernel chain) ([de95c9c](https://github.com/mdopp/laib/commit/de95c9c6797552fe52b5d9778fac098b9892dfa5))
* **operator:** no stdout swap, text tool calls end red, invalid arguments never run, prompt from the tool list, budgeted context, an eval harness ([#43](https://github.com/mdopp/laib/issues/43)) ([9f76ddc](https://github.com/mdopp/laib/commit/9f76ddcb2468712170bb2af413ff8b4a2aaeec24))
* **operator:** verbs write to the env writer (no stdout swap), text tool calls end red, invalid arguments never run, prompt from the tool list, budgeted context, reds wake the operator, silence turns red, an eval harness ([#43](https://github.com/mdopp/laib/issues/43)) ([1418ff8](https://github.com/mdopp/laib/commit/1418ff8aef7c5fcfc3dd42e09ece5a2db60a63cd))
* **radicale:** bind LDAP with a read-only account the auth template seeds, not as the LLDAP admin ([#36](https://github.com/mdopp/laib/issues/36), [#55](https://github.com/mdopp/laib/issues/55)) ([31b8a06](https://github.com/mdopp/laib/commit/31b8a0649f326f9eaa2092eb835dfb0654c011d8))
* **radicale:** the box's user maps onto the image's uid 2999 so /data is writable ([#36](https://github.com/mdopp/laib/issues/36)) ([2b2fd82](https://github.com/mdopp/laib/commit/2b2fd82bd1df0904b64f431c8073de71aacbbb77))
* **release:** gate the release on a green vm-install of its commit with provenance; manifest at 0.3.5; one doc-drift pass; test artifacts untracked ([#47](https://github.com/mdopp/laib/issues/47)) ([7324dad](https://github.com/mdopp/laib/commit/7324dad3aa1875fb080535592e8b570b8642924f))
* **release:** releases gated on a green vm-install with provenance, manifest synced, doc drift checked in ci ([#47](https://github.com/mdopp/laib/issues/47)) ([f41ae0f](https://github.com/mdopp/laib/commit/f41ae0f14a763eb9e35ef118f3c6093d3c5ce8f9))
* **render:** no identity_providers block in laib-clients.yml until a service declares oidc ([#36](https://github.com/mdopp/laib/issues/36)) ([5858d04](https://github.com/mdopp/laib/commit/5858d04125ea205a460918a8d60af1a270b3312b))
* **restore:** start what the restore stopped — apply restarts only what its plan changes ([#36](https://github.com/mdopp/laib/issues/36)) ([57f4bb0](https://github.com/mdopp/laib/commit/57f4bb07362d92be2c8ec8e5fd4e771210a4edbc))
* **review:** compare the executable bit only (a checkout's 0600 is not a change); the dial stays laib's own delegation — approvals bind hands, not auto ([29d1985](https://github.com/mdopp/laib/commit/29d1985f761ab835f4086556511f651f6ef7e301))
* **rollback:** rollback by proposal, approve in a worktree with validation, a manifest of managed files ([#41](https://github.com/mdopp/laib/issues/41)) ([a6bdcb0](https://github.com/mdopp/laib/commit/a6bdcb095bfabfbfb205a2ba4a8b668b88b0eb7c))
* **rollback:** rollback by proposal, approve in a worktree with validation, a manifest of managed files, operator disable revokes ([#41](https://github.com/mdopp/laib/issues/41)) ([7a208cb](https://github.com/mdopp/laib/commit/7a208cb24a8aae6aa5bbae13c6b9d3ac14c29a59))
* **secrets:** write secrets.age with yaml.Marshal, so every value comes back unchanged ([17a46d0](https://github.com/mdopp/laib/commit/17a46d092dd16a5045c9189c8efdb28e44ac7fa3))
* **stick:** a failed write never yields a verified ISO ([7b97ae4](https://github.com/mdopp/laib/commit/7b97ae4bd92fe39527cb537eb7a67904917f5d87))
* **stick:** a stick left in an installed box does not reinstall it ([80fe3e7](https://github.com/mdopp/laib/commit/80fe3e77c9cf7c84c31b4a1c589c655907eb9369))
* **stick:** a stick left in an installed box does not reinstall it ([ab35191](https://github.com/mdopp/laib/commit/ab35191e51929001b057c918bd509feb51da4b84))
* **stick:** data-disk unit waits for /var/usrlocal, no ordering cycle with its own mount ([#32](https://github.com/mdopp/laib/issues/32)) ([28bc8d8](https://github.com/mdopp/laib/commit/28bc8d87be5844079aca700a926c6207c55c102c))
* **stick:** look at the device before writing, and accept only an ISO that reached the disk ([f34292c](https://github.com/mdopp/laib/commit/f34292c607ec2f5a9b75027caa9e60957033ebb9))
* **stick:** look at the device before writing; never the system disk, a non-stick only with --force-device ([cae255a](https://github.com/mdopp/laib/commit/cae255af12a5b48a9e192a50d0681194035e2d18))
* **stick:** zero the old CoreOS root before a reinstall writes the image ([#81](https://github.com/mdopp/laib/issues/81)) ([2bd0814](https://github.com/mdopp/laib/commit/2bd081457366afb4789ef9080fac966a93db2753))
* **stick:** zero the old CoreOS root before a reinstall, so a power cut cannot leave an unmountable root ([#81](https://github.com/mdopp/laib/issues/81)) ([3d699bf](https://github.com/mdopp/laib/commit/3d699bf041f0a5ad05bcab3076ff519207115387))
* **templates:** unique host ports, health required, needs: mcp, images pulled before the start, snapshot before auto-update, no auto-update for databases ([#46](https://github.com/mdopp/laib/issues/46)) ([c9d4ae8](https://github.com/mdopp/laib/commit/c9d4ae8f964c9984f41300005d527c96c02bdf9a))
* **templates:** unique host ports, health required, needs: mcp, images pulled before the start, snapshot before auto-update, no auto-update for databases ([#46](https://github.com/mdopp/laib/issues/46)) ([ebe32bd](https://github.com/mdopp/laib/commit/ebe32bd2369b71516cb3361d2a3e9299dcb62398))
* **templates:** unique host ports, health required, needs: mcp, images pulled first, snapshot before auto-update, no auto-update for databases ([#46](https://github.com/mdopp/laib/issues/46)) ([199ec30](https://github.com/mdopp/laib/commit/199ec30c4ccda411713c534fcd27cb2a06f7e3a9))
* **usb:** a recorded array that udev left inactive is started degraded before the mount ([751478c](https://github.com/mdopp/laib/commit/751478cf077465c643e6f356d481d7be039b3f7d))
* **usb:** an array udev holds inactive is started from /proc/mdstat before assembling (the members are busy, --assemble cannot) ([0acc0e9](https://github.com/mdopp/laib/commit/0acc0e91d073618bb9820cd3396b2f0848ed6b7d))
* **usb:** the data disk is chosen once, by identity, and never wiped on a later boot ([6260998](https://github.com/mdopp/laib/commit/6260998dfb4fddbfe1fbfada035780b8f3a7d9ee))
* **usb:** the data disk is chosen once, by identity, and never wiped on a later boot ([0b8f6a8](https://github.com/mdopp/laib/commit/0b8f6a8c7dfe709b70d1211dd25b7c3f21c1f624))
* **usb:** the EFI boot entry is the installed disk's, and USB entries are switched off ([e673f2a](https://github.com/mdopp/laib/commit/e673f2a4cb565da0045f51223fd10710f1f60a7d))
* **vm-install:** no apostrophe in a comment inside $SSH '…' (the Caddy step did not parse) ([593c5d7](https://github.com/mdopp/laib/commit/593c5d70170794fda5847f6ed168d505e9c9e516))
* **vm-install:** the Caddy step parses again; ci checks every workflow run block with bash -n ([1fab12f](https://github.com/mdopp/laib/commit/1fab12f6d003c4405fa1601c06ddaf047124afe8))
* **werkstatt:** provider id defined once, the lease's catalog listed, tokens per container, retries before escalation ([#44](https://github.com/mdopp/laib/issues/44)) ([84d1653](https://github.com/mdopp/laib/commit/84d1653723270004c5269ddb37d963785f74de7b))
* **werkstatt:** provider id once, the lease's catalog, tokens per container, retries before escalation ([#44](https://github.com/mdopp/laib/issues/44)) ([b3bf74e](https://github.com/mdopp/laib/commit/b3bf74eab5e371f9e9afdfd4694cd47d92f30ceb))
