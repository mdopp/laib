# stimme — the Wyoming set for Home Assistant Assist (ADR 0015)

Whisper on 10300, Piper on 10200, openWakeWord on 10400, all on the host
network so Home Assistant adds them as Wyoming integrations at
`127.0.0.1`. The wake word is the trainer's output under
`stimme/wakeword/<name>.tflite`; Voice PE devices run it on-device once HA
pushes it. Martin, the Kokoro GPU voice Solaris used through an OpenAI-API
server, is not in the Assist path: Piper's `de_DE-thorsten-high` is. If
Martin should speak the house, it needs a Wyoming bridge — a later issue.

A box without a GPU: `WHISPER_IMAGE: docker.io/rhasspy/wyoming-whisper`,
`WHISPER_MODEL: small-int8`, and `VOICE_ENV` pointing at a file that says
`WHISPER_DEVICE=cpu`; the trainer needs the GPU and stays off there
(`laib ignore` its unit, or remove the file from the service folder).
