# stimme — the Wyoming set for Home Assistant Assist (ADR 0015)

Whisper on 10300 (Home Assistant reaches it through stimme-wakes on 10301,
below), Piper on 10200, openWakeWord on 10400, all on the host
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
In front of the names go the verbs (#961): „Schalte das Licht an, Schalte es
aus“ („Schalte Bambi an“ came back as „Schade van Bion.“ with Bambi in the
prompt, but no „Schalte“), then the house's stations („Spiele Einslive, …,
Radio Bob, Spiele Musik von“), one spoken form each, taken from the list
home-assistant's pre-start renders for Assist's LaibRadio
(`custom_sentences/de/laib_medien.yaml`, #933), which the unit reads on the
host and hands in as `LAIB_SENDER`. The verbs are filled after the device
names: a full budget cuts them and the rooms, never a device. No Home
Assistant or no stations on the share: no „Spiele“, the switching words stay.

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

**False wakes** (#825). On its first day the Voice PE woke twice in ten
minutes on room talk („Auf Wiedersehen.“): the model had only heard Piper.
Three things answer that:

- *The cutoff.* `WAKE_CUTOFF` (values.yml, default 0.97; the first model was
  exported at 0.95) is how sure the device must be. The trainer writes it into a
  trained model's manifest `<id>.json` on start, without training. The device runs
  the manifest it was flashed with: esphome's flash unit leaves a copy,
  `<id>.geflasht.json`, after every flash that went through, and while the two
  differ (or a device was flashed before that copy existed) Stimme's status offers
  the same flash again, through the same repair and approval. The Voice PE's own
  „Wake word sensitivity“ select is no knob for this: its firmware sets the
  cutoffs of okay_nabu, hey_jarvis and hey_mycroft by their ids, never a model
  flashed beside them (home-assistant-voice-pe's `wake_word_sensitivity`).
- *The negatives.* `stimme-wakes` (127.0.0.1:10301, Home Assistant's Whisper
  entry) forwards every byte to Whisper on 10300 and back and reads the events on
  the side. A transcript that starts with neither the persona's name nor a command
  (`COMMANDS` in `stimme-wakes.py`) is a wake without a command: its audio goes to
  `stimme/recordings/<id>/negativ/` (0600, the newest `WAKE_NEGATIVES_MAX`, the
  folder's `CACHEDIR.TAG` keeps it out of the nightly backup: household audio stays
  on the box). „Fehlalarm“ right after a wake (within ten minutes) marks that wake
  too. A transcription within 30 s of the last one is a follow-up, no wake. The
  trainer folds the negatives in as their own negative feature set on its next run
  (never as positives); nothing starts a run for them. What the device streams is
  what follows the trigger, so a negative is the room around a false wake, not
  necessarily the sound that woke it.
- *The numbers.* stimme-wakes counts the wakes per day in
  `stimme/micro/laib-wakes.json`; stimme-names writes „heute 2× geweckt, 2 ohne
  Befehl“ into the status line and the last days into `einzelheiten`. Any
  transcription through Home Assistant's Whisper entry counts, also a press of the
  device's button. Nothing it hears is printed; Whisper's own log still has the
  text it heard.

**The household's own recordings** (#828). Haus records the family saying the
phrase (its Stimme card) into `stimme/recordings/<id>/haus/` as
`<person>-<YYYYMMDD-HHMMSS>.wav`: 16 kHz, mono, 16-bit PCM, 0.25–4 s, 0600,
written through a `.<name>.part` dotfile. The haus template mounts that one
folder read-write at `/stimme-aufnahmen` (`HAUS_WAKE_RECORDINGS`); it creates it
0700 with a `CACHEDIR.TAG` before Haus starts, so restic leaves it out like
`negativ/` (household audio stays on the box). Every container involved runs as
its root, the box's user `core` on the host. The trainer folds every `*.wav`
under `<id>/` in as positives except `negativ/` and dotfiles.
No run starts by itself. stimme-names (in Whisper's container, which mounts the
recordings read-only) counts them in the status line: per person (from the file
name), the false wakes, and what is new since the model's run began (the
trainer's `begonnen` in `<id>.status.json`, else the model file's time); a clip
the trainer would not take counts as `unbrauchbar`, never as new. With at least
5 new recordings or 50 new false wakes the status offers „neu trainieren“
first: `laib repair status stimme` puts `laib retrain stimme` into the Inbox (a
destroy, only a human approves it). Approved, `stimme-retrain.service` moves
`<id>.tflite`, `<id>.json` and `<id>.geflasht.json` into `stimme/micro/vorher/`
(the way back: copy them back while no new model is there) and try-restarts the
trainer, which trains. The device keeps the old model; once the new one is
there, the flash above is offered (no `geflasht.json`: „ein älterer Stand“).
Only counts reach the journal, never a name or a sound.

A box without a GPU: `WHISPER_IMAGE: docker.io/rhasspy/wyoming-whisper`,
`WHISPER_MODEL: small-int8`, and `VOICE_ENV` pointing at a file that says
`WHISPER_DEVICE=cpu` (it must exist: Whisper requires it, a Quadlet
`EnvironmentFile=-` is no optional file but a broken path, #557; on the GPU
box llama's pre-start writes it); the trainer needs the GPU and stays off there
(`laib ignore` its unit, or remove the file from the service folder).
