# stimme — the Wyoming set for Home Assistant Assist (ADR 0015)

Whisper on 10300, Piper on 10200, openWakeWord on 10400, all on the host
network so Home Assistant reaches them at `127.0.0.1`. Nobody adds them in
HA by hand: home-assistant's post-deploy adds a Wyoming entry for each
server that answers, once, found by host and port, and points an Assist
pipeline part whose engine is gone at Stimme (#540). After `laib add
stimme`, `laib apply home-assistant` runs that hook (`laib diag` names a
missing Wyoming row until then). On the box that ran the old voice
stack, its Whisper and openWakeWord hold 10300 and 10400: Phase 5's script
takes it off and does both steps (#540). Piper also answers pods on the box at
`host.containers.internal:10200` (it binds `0.0.0.0`, the port is `lan:
false`): foundry-chronicle's `chronik` speaks its consent announcement
through it (#242). Piper speaks with the house persona's voice (`PERSONA_VOICE`,
ADR 0020). Martin, the Kokoro GPU voice of the old stack, is not in the
Assist path; it would need a Wyoming bridge.

**The household's names.** Whisper has never heard „Bambi“ as a name: „Schalte
Bambi an“ came back as „Schaut er beim Jahn.“ `stimme-names.service` (every 15
minutes) reads what the household exposed to Assist in Home Assistant, the
entities' names and aliases and the areas and floors they are in, with
`STIMME_HA_TOKEN` (home-assistant's post-deploy mints it for the non-admin HA
user „Stimme“), and writes it comma-joined to `stimme/whisper/laib-names.txt`.
Whisper reads that file at start as its initial prompt
(`WYO_WHISPER_INITIAL_PROMPT_FILE`); the order and the 200-token budget are
wyoming-faster-whisper's own (its `--hass-token` biasing, which needs an
administrator's token and the aiohttp extra this image lacks). The unit
rewrites the file only when the names changed and only then restarts Whisper,
with `try-restart`: a Whisper the GPU lease stopped stays stopped and reads the
new names when it comes back. A name not exposed to Assist (Home Assistant →
Einstellungen → Sprachassistenten → Freigeben) is not in the prompt. Spellings
of one name („Sofa-Licht“, „Sofa Licht“, the aliases home-assistant adds for
„Sofalicht“, #760) count once: Whisper writes those anyway. Home
Assistant away keeps the last names; a refused token fails the unit (`laib
apply home-assistant` mints a new one). `laib look stimme --container
systemd-stimme-whisper -- cat /config/laib-names.txt` shows what Whisper hears.

**The wake word** is the house persona's phrase (Joshua: "Hey Joshua"). The
HA Voice PE wakes on the device with microWakeWord; openWakeWord here serves
streaming satellites only. The trainer (`stimme/trainer` in this repo, image
`laib-wakeword-trainer`) gets the phrase, its id and the persona's languages
from the unit. When `stimme/micro/<id>.tflite` is missing it trains: Piper
voices for every language (German first, English for an English name),
plus the household's own WAVs under `stimme/recordings/<id>/` if any.
`stimme/micro/<id>.status.json` says training, done or failed with the
reason: a German sentence for the family (the step and the kind of
failure), and in `einzelheiten` the error line and the output's last lines
for the admin (`laib look stimme --container systemd-stimme-wakeword-trainer
-- cat /out/<id>.status.json`, #627). A failed run is not retried until the persona changes or that file
is deleted. A persona change restarts the unit (`PERSONA_REV`) and trains
the new phrase. It trains on the CPU (the card holds the household model and
Whisper; WAKEWORD_DEVICE=gpu to use it), some hours; the GPU lease still
stops the trainer while a mode needs the whole box.

**On the Voice PE** (#759): Laib flashes it, once the owner approves. Until a
satellite offers the phrase, Stimme's status says „Wake-Word „Hey Joshua“
trainiert, noch nicht auf dem Gerät“, with a repair: `laib repair status
stimme` puts `laib flash esphome` into the Inbox (a destroy, only a human
approves it; the card says the speaker restarts and is silent for about two
minutes). Approved, esphome's `esphome-flash.service` copies `<id>.tflite` and
`<id>.json` from `stimme/micro` into ESPHome's `models/`, points each device
YAML's `micro_wake_word` at `<id>.json` (a model of its own from `models/`, a
persona's from before, goes; upstream's stay; the YAML from before stays beside it as
`….yaml.laib-vorher`) and compiles and uploads it over the air
(templates/esphome). Whether the device runs it is measured, not assumed:
`stimme-names` (every 15 minutes, with `STIMME_HA_TOKEN`) reads the
satellites' „Aktivierungswort“ selects; once one offers the phrase, it selects
it there, once, and the status says „auf dem Gerät“. The trainer's own „done“
says „noch nicht auf dem Gerät“ and marks it unmeasured, so a Box-Aufgabe with
the befund `status wakeword` closes only on that measurement. home-assistant's
post-deploy selects the phrase on its next run too; the persona's Assist
pipeline uses `<id>` as its server wake word only once openWakeWord here
offers it (a model in `stimme/wakeword`), and none rather than a dead one
until then (templates/home-assistant, #634).

A box without a GPU: `WHISPER_IMAGE: docker.io/rhasspy/wyoming-whisper`,
`WHISPER_MODEL: small-int8`, and `VOICE_ENV` pointing at a file that says
`WHISPER_DEVICE=cpu` (it must exist: Whisper requires it, a Quadlet
`EnvironmentFile=-` is no optional file but a broken path, #557; on the GPU
box llama's pre-start writes it); the trainer needs the GPU and stays off there
(`laib ignore` its unit, or remove the file from the service folder).
