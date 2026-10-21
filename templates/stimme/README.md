# stimme — the Wyoming set for Home Assistant Assist (ADR 0015)

Whisper on 10300, Piper on 10200, openWakeWord on 10400, all on the host
network so Home Assistant reaches them at `127.0.0.1`. Nobody adds them in
HA by hand: home-assistant's post-deploy adds a Wyoming entry for each
server that answers, once, found by host and port, and points an Assist
pipeline part whose engine is gone at Stimme (#540). After `laib add
stimme`, `laib apply home-assistant` runs that hook (`laib diag` names a
missing Wyoming row until then). On the box that ran the old voice
stack, its Whisper and openWakeWord hold 10300 and 10400: Phase 5's script
takes it off and does both steps (docs/MIGRATION.md, Phase 5, #540). Piper also answers pods on the box at
`host.containers.internal:10200` (it binds `0.0.0.0`, the port is `lan:
false`): foundry-chronicle's `chronik` speaks its consent announcement
through it (#242). Piper speaks with the house persona's voice (`PERSONA_VOICE`,
ADR 0020). Martin, the Kokoro GPU voice of the old stack, is not in the
Assist path; it would need a Wyoming bridge.

**The wake word** is the house persona's phrase (Joshua: "Hey Joshua"). The
HA Voice PE wakes on the device with microWakeWord; openWakeWord here serves
streaming satellites only. The trainer (`stimme/trainer` in this repo, image
`laib-wakeword-trainer`) gets the phrase, its id and the persona's languages
from the unit. When `stimme/micro/<id>.tflite` is missing it trains: Piper
voices for every language (German first, English for an English name),
plus the household's own WAVs under `stimme/recordings/<id>/` if any.
`stimme/micro/<id>.status.json` says training, done or failed with the
reason. A failed run is not retried until the persona changes or that file
is deleted. A persona change restarts the unit (`PERSONA_REV`) and trains
the new phrase. Hours of GPU time; the GPU lease stops the trainer while a
mode needs the card.

**On the Voice PE** (once, by hand, in ESPHome): copy `<id>.tflite` and
`<id>.json` from `stimme/micro` next to the device's YAML, set
`micro_wake_word: models: - model: <id>.json`, then Install.

A box without a GPU: `WHISPER_IMAGE: docker.io/rhasspy/wyoming-whisper`,
`WHISPER_MODEL: small-int8`, and `VOICE_ENV` pointing at a file that says
`WHISPER_DEVICE=cpu`; the trainer needs the GPU and stays off there
(`laib ignore` its unit, or remove the file from the service folder).
