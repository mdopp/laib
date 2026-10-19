---
name: a-deployment-is-not-done-until-you-looked
description: "A deployment is not done until you looked. You are about to report a service as deployed, live, or working — or you just ran install/deploy/update and want to know what still has to be true before you say so. Read this before writing the summary, not after someone asks."
---

# A deployment is not done until you looked

**Answer first:** "the tool returned success" is not a deployment. Six things
have to be true, each one checkable with a command, and each one has been false
on this box while a session reported success.

## The six

**1. The container's own healthcheck is green — and you read its log.**

```sh
podman inspect <container> --format '{{.State.Health.Status}} fails={{.State.Health.FailingStreak}}'
podman inspect <container> --format '{{range .State.Health.Log}}[{{.ExitCode}}] {{.Output}}{{end}}'
```

`unhealthy` with a page that answers 200 is the normal shape of this failure —
see ServiceBay's `/workspace/servicebay/assists/footgun-healthcheck-runs-a-binary-the-image-lacks.md`.
Laib's own verdict (`laib status`) being green is a different question and does
not settle this one.

**2. The container is not restarting.** Read `RestartCount` twice, minutes
apart. A number that moves while you are deploying nothing is a loop, and a loop
costs the whole box — one of them made every service on this machine sluggish
for eleven hours.

```sh
podman inspect <container> --format '{{.RestartCount}}'
```

**3. The page works in a browser, not just in `curl`.** A 200 says bytes were
served. It does not say the app started. Load it with the browser this container
has and read the console:

```sh
NODE_PATH=/usr/local/lib/node_modules node -e '
const {chromium} = require("playwright");
chromium.launch({args:["--no-sandbox"]}).then(async b => {
  const p = await b.newPage(); const errs = [];
  p.on("pageerror", e => errs.push(String(e).split("\n")[0]));
  p.on("console", m => m.type() === "error" && errs.push(m.text()));
  const r = await p.goto(process.argv[1], {waitUntil: "networkidle"});
  console.log(r.status(), JSON.stringify(await p.title()), "errors:", errs.length);
  errs.slice(0,3).forEach(e => console.log("  !", e));
  await b.close();
})' "http://host.containers.internal:<port>/"
```

A page that returns 200 and throws `SyntaxError` in the bundle is a broken
deployment that every HTTP-level test calls green. That exact case happened here.

**4. The public URL answers from where its users are.** Your pod cannot reach
`https://<sub>.<domain>/` — it is isolated, and a refusal from inside
proves nothing. Ask the box, or ask the operator to load it. Do not report a
public URL you only tested from the inside.

**5. The proxy route names the service that actually serves it.** A route
forwarding to the right *port* while naming a service that no longer exists works
until the next rename and is wrong in the state today: read the route in
`services/<name>/laib.yml` and measure it:

```sh
laib-app check <name>.<domain>
```

**6. The running image is the one CI published.** If the app arrived inlined
into the pod spec — base64 in an `args` blob, a tarball in an env var — the
deployment is a workaround for a registry problem that is still there, and the
next change means rewriting a 100 KB YAML. Check what is actually running
(on the box, or ask the owner to):

```sh
podman inspect <container> --format '{{.ImageName}} {{.Image}}'
```

"published, pulled, current" is the answer you want. Anything else is the real
task, still open.

## And then report what you measured

Name the numbers you read, not the outcome you hoped for. "Health check: ok" is
worth nothing if it refers to a different check than the one that is failing.
If one of the six is not true, say which — a known gap is a finished piece of
work; a summary with a green tick over an unchecked box is not.

---
Carried from ServiceBay's assist catalog (`assists/checklist-a-deployment-is-not-done-until-you-looked.md` in mdopp/servicebay) when ServiceBay went off (laib #394). It is Laib's now: change it here.
