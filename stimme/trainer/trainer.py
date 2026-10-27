#!/usr/bin/env python3
"""Stimme's wake-word trainer (laib #128, ADR 0020 §5): it trains the house
persona's phrase into the on-device microWakeWord model the Voice PE runs.

The trigger is the state, not a queue. Laib renders WAKE_PHRASE, WAKE_ID,
WAKE_LANGUAGES and PERSONA_REV into this unit; a persona change changes the
unit, which restarts it. On start the trainer looks at what it left in /out:

- <id>.tflite and <id>.json there: done, nothing to do.
- <id>.status.json says failed for this phrase and this persona revision:
  wait. A run that failed once is not retried in a loop; a persona change
  (a new revision), a new trainer build (TRAINER_BUILD) or deleting the status file retries it.
- anything else, including a run cut by a restart: train.

New false wakes under /recordings/<id>/negativ (stimme-wakes, #825) and new recordings
of the phrase under /recordings/<id>/haus (Haus, #828) start no run; the next one trains
on them. The owner starts one: stimme-names offers „neu trainieren“, which moves the
model aside (`laib retrain stimme`, stimme-retrain.service), and this decides to train. WAKE_CUTOFF is written into a done model's
manifest on start, without training (retune).

It writes <id>.status.json (training, done, failed with a reason) so `laib`
and a person can see where it stands, and the model atomically when done.
A failed run says why in two registers (#588, #627): a German sentence for
the family (the step and the kind of failure), and in `einzelheiten` the
error line and the output's tail for the admin.
Flashing the model onto the Voice PE is esphome's flash unit, after the owner approves
(`laib flash esphome`, #759); stimme-names measures whether the device has it.

Stdlib only; TensorFlow lives in the image and is reached through train.py.
"""

from __future__ import annotations

import collections
import io
import json
import logging
import os
import pathlib
import re
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


def write_status(out: pathlib.Path, wake_id: str, state: str, phrase: str, rev: str, reason: str,
                 einzelheiten: str = "", begonnen: str = "") -> None:
    """begonnen: when a done run began (#828); stimme-names counts the recordings after it as new"""
    out.mkdir(parents=True, exist_ok=True)
    at = time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime())
    tmp = status_path(out, wake_id).with_suffix(".tmp")
    s = {"state": state, "phrase": phrase, "rev": rev, "reason": reason, "einzelheiten": einzelheiten, "at": at}
    if begonnen:
        s["begonnen"] = begonnen
    tmp.write_text(json.dumps(s, ensure_ascii=False) + "\n")
    tmp.replace(status_path(out, wake_id))
    write_laib_status(out, state, phrase, reason, at, einzelheiten)


LAIB_STATUS = "laib-status.json"
RETRY = "erneut versucht wird bei einem neuen Satz, einer neuen Revision der persona (box.yml persona) oder einem neuen Trainer"


def write_laib_status(out: pathlib.Path, state: str, phrase: str, reason: str, at: str, einzelheiten: str = "") -> None:
    """The status laib shows on the Box page and in diag (#169, #309): written
    when the state changes, so laib reads it fresh and nothing polls. detail
    is for the family; the raw words go only into einzelheiten (#588)."""
    detail = {"done": f"Wake-Word „{phrase}“ trainiert, noch nicht auf dem Gerät",
              "training": f"Wake-Word „{phrase}“ wird trainiert (seit {at})",
              "failed": f"Wake-Word „{phrase}“: Training fehlgeschlagen {reason}"}.get(state, f"Wake-Word „{phrase}“ noch nicht trainiert")
    findings = [{"severity": "gelb", "detail": detail, "fix": RETRY, "einzelheiten": einzelheiten}] if state == "failed" else []
    if state == "done":  # #759: whether the device runs it, stimme-names measures through Home Assistant and writes here
        findings = [{"severity": "info", "detail": f"Wake-Word „{phrase}“: ob das Gerät es kennt, ist noch nicht gemessen",
                     "fix": "stimme-names sieht alle 15 Minuten in Home Assistant nach", "topic": "wakeword", "unmeasured": True}]
    out.mkdir(parents=True, exist_ok=True)
    tmp = out / (LAIB_STATUS + ".tmp")
    tmp.write_text(json.dumps({"at": at, "detail": detail, "einzelheiten": einzelheiten, "findings": findings, "phrase": phrase},
                              ensure_ascii=False) + "\n")
    tmp.replace(out / LAIB_STATUS)


def show_done(out: pathlib.Path, phrase: str) -> None:
    """A model from an earlier run: what stimme-names measured on the device for this phrase stays (#759)."""
    try:
        s = json.loads((out / LAIB_STATUS).read_text())
        if s.get("phrase") == phrase and "geraet" in s:
            return
    except (OSError, ValueError):
        pass
    write_laib_status(out, "done", phrase, "", time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime()))


def show_waiting(out: pathlib.Path, wake_id: str, phrase: str) -> None:
    """A run that failed waits; laib keeps showing why it failed."""
    s = json.loads(status_path(out, wake_id).read_text())
    write_laib_status(out, "failed", phrase, s.get("reason", ""), s.get("at", ""), s.get("einzelheiten", ""))


def revision() -> str:
    """what a failed run is remembered for: the persona's revision and the trainer's build, so a
    fixed trainer image tries again by itself (2026-10-24)"""
    rev, build = os.environ.get("PERSONA_REV", ""), os.environ.get("TRAINER_BUILD", "")
    return f"{rev}@{build}" if build else rev


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


# The steps, as train.py announces them ("== phase: <name>") and as the
# family reads them; provisioning is everything before train.py.
STEPS = {"provision": "Vorbereiten", "generate": "Beispiele erzeugen", "real": "eigene Aufnahmen",
         "household": "Fehlalarme aus dem Haus",
         "features": "Merkmale berechnen", "negatives": "Gegenbeispiele laden",
         "train": "Modell trainieren", "export": "Modell schreiben"}
TAIL_LINES = 40
EXCEPTION_LINE = re.compile(r"^(?:[A-Za-z_][\w.]*\.)?([A-Za-z_]\w*(?:Error|Exception|Exit|Interrupt))(?::|$)")


class StepFailed(Exception):
    """A child that exited non-zero: grund is the family's sentence (from
    „Training fehlgeschlagen“ on), einzelheiten the error line and the tail."""

    def __init__(self, cmd: list[str], rc: int, tail: list[str]):
        step = "provision"
        for line in tail:
            if line.startswith("== phase: "):
                step = line[len("== phase: "):].strip()
        if os.path.basename(cmd[0]) == "wget":
            step = "provision"
        error = cause(tail, rc)
        rest = "\n".join(line[:500] for line in tail)
        self.einzelheiten = error[:500] + "\n\n" + rest[-3000:]
        self.grund = f"beim Schritt „{STEPS.get(step, step)}“: {kind(cmd, rc, error)}."
        super().__init__(self.grund)


def cause(tail: list[str], rc: int) -> str:
    """The real error: the last exception line that is not a child's
    CalledProcessError about the real one further up."""
    for line in reversed(tail):
        m = EXCEPTION_LINE.match(line.strip())
        if m and m.group(1) != "CalledProcessError":
            return line.strip()
    if rc < 0:
        return f"killed by signal {-rc}"
    return next((l.strip() for l in reversed(tail) if l.strip()), f"exit status {rc}")


def kind(cmd: list[str], rc: int, error: str) -> str:
    """What went wrong, in words the family understands."""
    low = error.lower()
    if rc in (-9, 137) or "memoryerror" in low or "resourceexhausted" in low or "out of memory" in low:
        return "der Speicher hat nicht gereicht"
    if os.path.basename(cmd[0]) == "wget" or "connection" in low or "http error" in low:
        return "ein Download ist fehlgeschlagen; ist das Internet erreichbar?"
    if "cuda" in low or "gpu" in low:
        return "die Grafikkarte war nicht nutzbar"
    if re.match(r"(ModuleNotFoundError|ImportError|AttributeError: module)", error):
        return "Programmteile im Trainer passen nicht zusammen, ein Fehler im Trainer selbst"
    return "ein unerwarteter Fehler"


_stopping = False
_child: subprocess.Popen | None = None


def _on_stop(signum, frame) -> None:
    # PID 1 ignores SIGTERM without a handler; podman would SIGKILL after 10 s
    # and systemd report every planned stop by the GPU lease as failed.
    global _stopping
    _stopping = True
    if _child is not None and _child.poll() is None:
        _child.terminate()


def _run(cmd: list[str], cwd: pathlib.Path | None = None, env: dict[str, str] | None = None) -> None:
    global _child
    if _stopping:
        raise _Stopped()
    log.info("+ %s", " ".join(cmd))
    # the child's output goes to the journal as before, and its tail stays
    # here so a failure can say why (#627)
    tail: collections.deque[str] = collections.deque(maxlen=TAIL_LINES)
    try:
        _child = subprocess.Popen(cmd, cwd=cwd, stdout=subprocess.PIPE, stderr=subprocess.STDOUT, env=env)
    except OSError as err:
        raise StepFailed(cmd, 127, [f"{type(err).__name__}: {err}"]) from err
    # lines end at \n only: a progress bar's \r redraws stay one line
    for line in io.TextIOWrapper(_child.stdout, encoding="utf-8", errors="replace", newline="\n"):
        sys.stdout.write(line)
        sys.stdout.flush()
        tail.append(line.rstrip("\n").rsplit("\r")[-1])
    rc = _child.wait()
    _child = None
    if _stopping:
        raise _Stopped()
    if rc != 0:
        raise StepFailed(cmd, rc, list(tail))


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
    # piper-sample-generator pins audiomentations==0.33.0, for its augment.py
    # we never call; with its deps it downgraded the image's audiomentations
    # and microWakeWord's augmentation lost AddColorNoise (#627). So: no deps,
    # and its sample generator's own ones by name.
    pip = [sys.executable, "-m", "pip", "install", "--cache-dir", str(work / "pip-cache")]
    _run(pip + ["--no-deps", "-e", str(work / "piper-sample-generator")])
    # datasets stays below 4: microWakeWord decodes its clips with it, and 4 needs torchcodec (2026-10-24)
    _run(pip + ["piper-tts==1.3.0", "torch>=2,<3", "torchaudio", "datasets>=2.18,<4"])
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
    if any((recordings / NEGATIVES).glob("*.wav")):   # #825: the false wakes stimme-wakes kept
        cmd += ["--negatives", str(recordings / NEGATIVES)]
    cmd += ["--cutoff", str(cutoff())]
    # the CPU by default (2026-10-24): the card holds the household model and
    # Whisper, and the run died validating with no memory left on it. The
    # model is small; the CPU takes hours longer and nobody loses the GPU.
    env = dict(os.environ)
    if os.environ.get("WAKEWORD_DEVICE", "cpu") != "gpu":
        env["CUDA_VISIBLE_DEVICES"] = "-1"
    _run(cmd, env=env)
    return out / f"{wake_id}.tflite"


NEGATIVES = "negativ"   # under recordings/<id>/: stimme-wakes' false wakes, negatives, never positives (#825)
CUTOFF = 0.97          # #825: 0.95 woke on room talk twice in ten minutes


def cutoff() -> float:
    """WAKE_CUTOFF (values.yml): how sure the device must be; one it cannot use is the default"""
    try:
        c = float(os.environ.get("WAKE_CUTOFF") or CUTOFF)
    except ValueError:
        c = -1
    if not 0.5 <= c < 1:
        log.warning("WAKE_CUTOFF %r is no probability between 0.5 and 1: %s", os.environ.get("WAKE_CUTOFF"), CUTOFF)
        return CUTOFF
    return c


def retune(out: pathlib.Path, wake_id: str, c: float) -> bool:
    """#825: a trained model's manifest at the cutoff asked for, without training again; the device
    takes it with the next flash, which stimme-names offers once the flashed one differs"""
    path = out / f"{wake_id}.json"
    try:
        manifest = json.loads(path.read_text())
        micro = manifest["micro"]
    except (OSError, ValueError, KeyError, TypeError):
        log.warning("%s is not readable: its cutoff stays", path)
        return False
    if micro.get("probability_cutoff") == round(c, 3):
        return False
    log.info("probability_cutoff %s -> %s in %s", micro.get("probability_cutoff"), round(c, 3), path.name)
    micro["probability_cutoff"] = round(c, 3)
    tmp = path.with_name(path.name + ".tmp")
    tmp.write_text(json.dumps(manifest, indent=2) + "\n")
    tmp.replace(path)
    return True


def main() -> int:
    logging.basicConfig(level=logging.INFO, format="%(asctime)s %(levelname)s %(name)s %(message)s")
    signal.signal(signal.SIGTERM, _on_stop)
    signal.signal(signal.SIGINT, _on_stop)
    phrase = os.environ.get("WAKE_PHRASE", "").strip()
    wake_id = os.environ.get("WAKE_ID", "").strip()
    rev = revision()
    languages = [l for l in os.environ.get("WAKE_LANGUAGES", "de").split(",") if l]
    work = pathlib.Path(os.environ.get("WAKEWORD_WORK_DIR", "/work"))
    out = pathlib.Path(os.environ.get("WAKEWORD_OUT_DIR", "/out"))
    if not phrase or not wake_id:
        log.error("WAKE_PHRASE and WAKE_ID are required (laib renders them from the house persona)")
        return 2
    what = decide(out, phrase, wake_id, rev)
    log.info("wake word %r (%s), persona rev %s: %s", phrase, wake_id, rev, what)
    if what == "done":  # a model from an earlier run, or a phrase that came back
        retune(out, wake_id, cutoff())
        show_done(out, phrase)
    elif what == "wait":  # the failed run's reason stays what laib shows
        show_waiting(out, wake_id, phrase)
    if what == "train":
        sets = positive_sets(phrase, languages)
        begonnen = time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime())
        write_status(out, wake_id, "training", phrase, rev, "")
        try:
            provision(work, sets)
            model = train(work, out, phrase, wake_id, sets)
            write_status(out, wake_id, "done", phrase, rev, f"Modell gebaut: {model.name}", begonnen=begonnen)
            log.info("done: %s", model)
        except _Stopped:
            # left as "training": the next start trains again
            log.info("stopped during training")
            return 0
        except StepFailed as err:  # one failed run is a status, never a dead container
            write_status(out, wake_id, "failed", phrase, rev, err.grund, err.einzelheiten)
            log.error("training failed %s\n%s", err.grund, err.einzelheiten.split("\n", 1)[0])
        except Exception as err:
            write_status(out, wake_id, "failed", phrase, rev, "mit einem unerwarteten Fehler im Trainer.",
                         f"{type(err).__name__}: {err}"[:4000])
            log.exception("training failed")
    while not _stopping:  # idle until the next persona change restarts the unit
        time.sleep(1)
    return 0


if __name__ == "__main__":
    sys.exit(main())
