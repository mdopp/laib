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
through it (#242). Piper speaks with Thorsten (`de_DE-thorsten-high`, fixed by
ADR 0030). Martin, the Kokoro GPU voice of the old stack, is not in the
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

**The wake word** is „Hey Mycroft“ (ADR 0030), a pretrained model: the HA
Voice PE carries it in its firmware (microWakeWord) and offers it in its
„Aktivierungswort“ select, openWakeWord here carries it built in for streaming
satellites. Nothing is trained or flashed. `stimme-names` (every 15 minutes,
with `STIMME_HA_TOKEN`) reads the satellites' wake-word selects: a device that
offers „Hey Mycroft“ for the first time gets it selected, once (a later choice
by hand stays; home-assistant's post-deploy selects it on its next run too), and
the status says „auf dem Gerät“. A device that does not offer it is named in a
gelb finding with what to do (ESPHome: `micro_wake_word` with `hey_mycroft`).
No satellite, or one Home Assistant does not reach, is no measurement.

**The wakes** (#825). On its first day the Voice PE woke twice in ten minutes on
room talk. `stimme-wakes` (127.0.0.1:10301, Home Assistant's Whisper entry)
forwards every byte to Whisper on 10300 and back and reads the transcripts on
the side. A transcript that starts with neither the wake word's name nor a
command (`COMMANDS` in `stimme-wakes.py`) is a wake without a command;
„Fehlalarm“ right after a wake (within ten minutes) marks that wake too; a
transcription within 30 s of the last one is a follow-up, no wake. It counts
per day in `stimme/micro/laib-wakes.json`; stimme-names writes „heute 2×
geweckt, 2 ohne Befehl“ into the status line and the last days into
`einzelheiten`. Nothing it hears is kept or printed, only counts.
Frage judges the sentences that reach it as well (frage/gate.go): one nobody
meant gets a tone instead of an answer, and Frage sends „ohne“ to
stimme-wakes' second port, 10302 (0.0.0.0 for the frage pod, `lan: false`).
The wake that transcript was, if it was the last one and less than 30 s ago,
then counts as one without a command, once. Nothing of the text crosses.

Until ADR 0030 Stimme trained the persona's own phrase („Hey Joshua“) and kept
the household's recordings and false wakes for it. The trainer is gone; what it
left on the box (`stimme/trainer`, `stimme/recordings` with household audio,
`stimme/wakeword`, the models in `stimme/micro`) stays on disk until the owner
deletes it.

A box without a GPU: `WHISPER_IMAGE: docker.io/rhasspy/wyoming-whisper`,
`WHISPER_MODEL: small-int8`, and `VOICE_ENV` pointing at a file that says
`WHISPER_DEVICE=cpu` (it must exist: Whisper requires it, a Quadlet
`EnvironmentFile=-` is no optional file but a broken path, #557; on the GPU
box llama's pre-start writes it).
