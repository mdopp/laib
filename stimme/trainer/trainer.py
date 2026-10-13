#!/usr/bin/env python3
"""Stimme's wake-word trainer (laib #128, ADR 0020 §5): it trains the house
persona's phrase into the on-device microWakeWord model the Voice PE runs.

The trigger is the state, not a queue. Laib renders WAKE_PHRASE, WAKE_ID,
WAKE_LANGUAGES and PERSONA_REV into this unit; a persona change changes the
unit, which restarts it. On start the trainer looks at what it left in /out:

- <id>.tflite and <id>.json there: done, nothing to do.
- <id>.status.json says failed for this phrase and this persona revision:
  wait. A run that failed once is not retried in a loop; a persona change
  (a new revision) or deleting the status file retries it.
- anything else, including a run cut by a restart: train.

It writes <id>.status.json (training, done, failed with a reason) so `laib`
and a person can see where it stands, and the model atomically when done.
Flashing the model onto the Voice PE stays a separate ESPHome step (README).

Stdlib only; TensorFlow lives in the image and is reached through train.py.
"""

from __future__ import annotations

import json
import logging
import os
import pathlib
import shutil
import signal
import subprocess
import sys
import time
from dataclasses import dataclass, field

log = logging.getLogger("wakeword-trainer")

IMAGE_SOURCES = pathlib.Path(os.environ.get("WAKEWORD_IMAGE_SOURCES", "/opt"))
SOURCE_REPOS = ("microWakeWord", "piper-sample-generator")
TRAIN_SCRIPT = IMAGE_SOURCES / "train.py"
PIPER_VOICES = "https://huggingface.co/rhasspy/piper-voices/resolve/main"

# Piper voices per language the persona speaks. German is load-bearing for a
# German household; English adds the English pronunciation of a name like
# "Joshua" as a second positive set (#128).
VOICES = {
    "de": (
        "de_DE-thorsten-high.onnx",
        "de_DE-thorsten_emotional-medium.onnx",
        "de_DE-eva_k-x_low.onnx",
        "de_DE-kerstin-low.onnx",
        "de_DE-karlsson-low.onnx",
        "de_DE-ramona-low.onnx",
        "de_DE-pavoque-low.onnx",
    ),
    "en": (
        "en_US-lessac-medium.onnx",
        "en_US-libritts_r-medium.onnx",
        "en_GB-alan-medium.onnx",
    ),
}


@dataclass
class PositiveSet:
    lang: str
    phrase: str
    voices: tuple[str, ...] = field(default_factory=tuple)


def positive_sets(phrase: str, languages: list[str]) -> list[PositiveSet]:
    """One synthetic positive set per language the persona speaks; German when
    none of its languages has voices here."""
    sets = [PositiveSet(lang, phrase, VOICES[lang]) for lang in languages if lang in VOICES]
    return sets or [PositiveSet("de", phrase, VOICES["de"])]


def run_dir(work: pathlib.Path, wake_id: str) -> pathlib.Path:
    """Samples, features and checkpoints of one wake word: a new phrase never
    trains on the old one's positives or resumes its checkpoint."""
    return work / "runs" / wake_id


def status_path(out: pathlib.Path, wake_id: str) -> pathlib.Path:
    return out / f"{wake_id}.status.json"


def write_status(out: pathlib.Path, wake_id: str, state: str, phrase: str, rev: str, reason: str) -> None:
    out.mkdir(parents=True, exist_ok=True)
    tmp = status_path(out, wake_id).with_suffix(".tmp")
    tmp.write_text(json.dumps({"state": state, "phrase": phrase, "rev": rev, "reason": reason,
                               "at": time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime())}, ensure_ascii=False) + "\n")
    tmp.replace(status_path(out, wake_id))


def decide(out: pathlib.Path, phrase: str, wake_id: str, rev: str) -> str:
    """train, done or wait — from the files in /out alone."""
    if (out / f"{wake_id}.tflite").exists() and (out / f"{wake_id}.json").exists():
        return "done"
    try:
        s = json.loads(status_path(out, wake_id).read_text())
    except (OSError, ValueError):
        return "train"
    if s.get("state") == "failed" and s.get("phrase") == phrase and s.get("rev") == rev:
        return "wait"
    return "train"


class _Stopped(Exception):
    pass


_stopping = False
_child: subprocess.Popen | None = None


def _on_stop(signum, frame) -> None:
    # PID 1 ignores SIGTERM without a handler; podman would SIGKILL after 10 s
    # and systemd report every planned stop by the GPU lease as failed.
    global _stopping
    _stopping = True
    if _child is not None and _child.poll() is None:
        _child.terminate()


def _run(cmd: list[str], cwd: pathlib.Path | None = None) -> None:
    global _child
    if _stopping:
        raise _Stopped()
    log.info("+ %s", " ".join(cmd))
    _child = subprocess.Popen(cmd, cwd=cwd)
    rc = _child.wait()
    _child = None
    if _stopping:
        raise _Stopped()
    if rc != 0:
        raise subprocess.CalledProcessError(rc, cmd)


def provision(work: pathlib.Path, sets: list[PositiveSet]) -> None:
    """Lay out the work dir train.py expects. Idempotent. The sources go onto
    the work volume (they accumulate gigabytes of artefacts there);
    piper-sample-generator's torch/piper-tts stack (~3 GB) is installed here,
    not baked into the TensorFlow image, with the pip cache on the volume so a
    restart does not download it again; the voices once."""
    work.mkdir(parents=True, exist_ok=True)
    for repo in SOURCE_REPOS:
        if not (work / repo).exists():
            shutil.copytree(IMAGE_SOURCES / repo, work / repo, symlinks=True)
    _run([sys.executable, "-m", "pip", "install", "--cache-dir", str(work / "pip-cache"),
          "-e", str(work / "piper-sample-generator")])
    voices = work / "piper-sample-generator" / "voices"
    voices.mkdir(parents=True, exist_ok=True)
    for s in sets:
        for v in s.voices:
            lang, name, quality = v[: -len(".onnx")].split("-")
            for suffix in ("", ".json"):
                target = voices / (v + suffix)
                if target.exists():
                    continue
                part = target.with_name(target.name + ".part")
                _run(["wget", "-q", "-O", str(part),
                      f"{PIPER_VOICES}/{lang.split('_')[0]}/{lang}/{name}/{quality}/{v}{suffix}"])
                part.replace(target)


def train(work: pathlib.Path, out: pathlib.Path, phrase: str, wake_id: str, sets: list[PositiveSet]) -> pathlib.Path:
    cmd = [sys.executable, str(TRAIN_SCRIPT), "--work", str(work), "--run", str(run_dir(work, wake_id)),
           "--phrase", phrase, "--id", wake_id, "--out", str(out / f"{wake_id}.tflite"),
           "--steps", os.environ.get("WAKEWORD_TRAINING_STEPS", "12000"),
           "--languages", ",".join(s.lang for s in sets)]
    for s in sets:
        for v in s.voices:
            cmd += ["--voice", v]
    recordings = pathlib.Path(os.environ.get("WAKEWORD_RECORDINGS", "/recordings")) / wake_id
    if recordings.is_dir():
        cmd += ["--recordings", str(recordings)]
    _run(cmd)
    return out / f"{wake_id}.tflite"


def main() -> int:
    logging.basicConfig(level=logging.INFO, format="%(asctime)s %(levelname)s %(name)s %(message)s")
    signal.signal(signal.SIGTERM, _on_stop)
    signal.signal(signal.SIGINT, _on_stop)
    phrase = os.environ.get("WAKE_PHRASE", "").strip()
    wake_id = os.environ.get("WAKE_ID", "").strip()
    rev = os.environ.get("PERSONA_REV", "")
    languages = [l for l in os.environ.get("WAKE_LANGUAGES", "de").split(",") if l]
    work = pathlib.Path(os.environ.get("WAKEWORD_WORK_DIR", "/work"))
    out = pathlib.Path(os.environ.get("WAKEWORD_OUT_DIR", "/out"))
    if not phrase or not wake_id:
        log.error("WAKE_PHRASE and WAKE_ID are required (laib renders them from the house persona)")
        return 2
    what = decide(out, phrase, wake_id, rev)
    log.info("wake word %r (%s), persona rev %s: %s", phrase, wake_id, rev, what)
    if what == "train":
        sets = positive_sets(phrase, languages)
        write_status(out, wake_id, "training", phrase, rev, "")
        try:
            provision(work, sets)
            model = train(work, out, phrase, wake_id, sets)
            write_status(out, wake_id, "done", phrase, rev, f"Modell gebaut: {model.name}")
            log.info("done: %s", model)
        except _Stopped:
            # left as "training": the next start trains again
            log.info("stopped during training")
            return 0
        except Exception as err:  # one failed run is a status, never a dead container
            write_status(out, wake_id, "failed", phrase, rev, f"{type(err).__name__}: {err}"[:500])
            log.error("training failed: %s", err)
    while not _stopping:  # idle until the next persona change restarts the unit
        time.sleep(1)
    return 0


if __name__ == "__main__":
    sys.exit(main())
