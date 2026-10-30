#!/usr/bin/env python3
"""Train one on-device microWakeWord model: the house persona's phrase (laib #128).

The household's HA Voice PE (ESP32-S3) detects its wake word on the device with
microWakeWord, a different framework and model format from the server-side
openWakeWord. This produces the quantized streaming `<id>.tflite` plus the
ESPHome v2 manifest `<id>.json` that the ESPHome `micro_wake_word:` component
consumes; flashing stays a separate ESPHome step.

Ported from solarisbay's train-micro-wake-word.py, which trained the fixed word
"Solaris" from a queue in Solaris's database. Here the phrase, its id and the
languages are arguments (trainer.py passes the house persona's), each wake word
has its own run dir (samples, features, checkpoints), and household recordings
are a plain directory of WAVs.

Phases (each idempotent on its output dir):
  1. generate  — synthesize the phrase with Piper voices of every language the
     persona speaks (German first; English adds the English pronunciation).
  2. real      — fold the household's own recordings in, if any, through the
     same augmentation path; silent, clipped or misencoded clips stay out.
  3. features  — augment (RIR + background) and compute the spectrogram mmaps.
     household — the false wakes the box kept (stimme-wakes, #825) the same way, as
     a negative set of this household's rooms and voices, if there are any.
  4. negatives — microWakeWord's pre-generated negative datasets (HF).
  5. train     — mixednet, convert and quantize to the streaming INT8 tflite.
  6. export    — the tflite and the manifest at the chosen probability cutoff.

Needs TensorFlow-GPU, microWakeWord and piper-sample-generator (the image has
them); run with TF_FORCE_GPU_ALLOW_GROWTH=true, the GPU is shared.
"""

from __future__ import annotations

import argparse
import array
import json
import pathlib
import shutil
import subprocess
import sys
import wave

import yaml

# Household recordings are copied into the synthetic positives dir under this
# prefix — it can never collide with generate_positives' "%06d.wav" sequence.
REAL_PREFIX = "real_"

# What a usable recording looks like. Same contract the browser recorder and the
# gatekeeper write (#1081): 16 kHz mono 16-bit, one spoken word.
SAMPLE_RATE = 16000
MIN_FRAMES = SAMPLE_RATE // 4  # 0.25 s
MAX_FRAMES = SAMPLE_RATE * 4  # 4 s
# int16 peak below this is a muted mic or room tone, not a spoken wake word.
SILENCE_PEAK = 500
CLIPPED_LEVEL = 32700
MAX_CLIPPED_SHARE = 0.01

# The false wakes stimme-wakes keeps under recordings/<id>/ (#825): negatives.
NEGATIVES = "negativ"
HOUSEHOLD_FEATURES = "household_negative_features"
MIN_SPLIT_CLIPS = 10   # fewer and Clips' train/test split leaves an empty side (#825)
# How the household's negatives weigh against microWakeWord's corpora (speech and
# dinner_party at 10). UNVERIFIED like DEFAULT_REAL_OVERSAMPLE below: a few dozen
# clips at 3.0 are drawn often, each through a fresh augmentation; the penalty 2.0
# makes a false accept on this household's rooms cost twice a corpus one. Judge it
# by the counts in `laib status stimme` after the next run.
HOUSEHOLD_SAMPLING_WEIGHT = 3.0
HOUSEHOLD_PENALTY_WEIGHT = 2.0

# How many copies of each resident recording join the positive set.
#
# UNVERIFIED — nobody has yet run a full training with real samples, and this
# ratio can only be judged by how the resulting model behaves on the household's
# voices. It is a starting point, not a tuned constant. The reasoning: ten
# recordings against --positive-samples 2000 is 0.5% of the positive set, a
# rounding error the model never learns from; x20 makes them ~9%. Copies are not
# dead weight — the augmenter draws RIR/background/EQ/gain per clip, so each
# copy becomes a different training example.
DEFAULT_REAL_OVERSAMPLE = 20


def _run(cmd: list[str], cwd: pathlib.Path | None = None) -> None:
    sys.stdout.write("+ " + " ".join(cmd) + "\n")
    sys.stdout.flush()
    subprocess.run(cmd, cwd=cwd, check=True)


def phase(name: str) -> None:
    """Announce a phase; trainer.py names the step a failure happened in by
    the last of these lines (#627)."""
    sys.stdout.write(f"== phase: {name}\n")
    sys.stdout.flush()


def generate_positives(work: pathlib.Path, run: pathlib.Path, phrase: str, voice_sets: list[list[str]], n: int) -> pathlib.Path:
    """Synthesize the phrase with each language's voices into <run>/generated_samples."""
    psg = work / "piper-sample-generator"
    out = run / "generated_samples"
    out.mkdir(parents=True, exist_ok=True)
    for stale in out.glob("[0-9]*.wav"):  # a cut run's partial set
        stale.unlink()
    sets = []
    for voices in voice_sets:
        args = [a for v in voices if (psg / "voices" / v).exists() for a in ("--model", str(psg / "voices" / v))]
        if args:
            sets.append(args)
    if not sets:
        raise SystemExit(f"No Piper voices found in {psg / 'voices'}; trainer.py downloads them.")
    per_set = max(1, n // len(sets))
    idx = 0
    for voice_args in sets:
        phrase_dir = out / f"p{idx}"
        phrase_dir.mkdir(exist_ok=True)
        _run(
            [
                sys.executable,
                "-m",
                "piper_sample_generator",
                phrase,
                "--max-samples",
                str(per_set),
                "--batch-size",
                "10",
                *voice_args,
                "--length-scales",
                "0.85",
                "1.0",
                "1.15",
                "1.3",
                "--noise-scales",
                "0.667",
                "0.85",
                "1.0",
                "--output-dir",
                str(phrase_dir),
            ],
            cwd=psg,
        )
        # flatten into the single samples dir with unique names
        for wav in sorted(phrase_dir.glob("*.wav")):
            wav.rename(out / f"{idx:06d}.wav")
            idx += 1
        shutil.rmtree(phrase_dir, ignore_errors=True)
    sys.stdout.write(f"Generated {idx} positive clips -> {out}\n")
    return out


def reject_reason(wav_path: pathlib.Path) -> str | None:
    """Why this recording must stay out of the positive set, or None if it may
    join it. A silent, clipped or misencoded clip teaches the model that the
    wake word sounds like nothing at all, so it is worse than one clip fewer."""
    try:
        with wave.open(str(wav_path), "rb") as wav:
            fmt = (wav.getnchannels(), wav.getsampwidth(), wav.getframerate())
            if fmt != (1, 2, SAMPLE_RATE):
                return (
                    f"{fmt[2]} Hz/{fmt[0]}ch/{fmt[1] * 8}-bit, want 16 kHz mono 16-bit"
                )
            frames = wav.getnframes()
            pcm = wav.readframes(frames)
    except (wave.Error, EOFError, OSError) as err:
        return f"unreadable: {type(err).__name__}: {err}"
    if frames < MIN_FRAMES:
        return f"too short: {frames / SAMPLE_RATE:.2f}s"
    if frames > MAX_FRAMES:
        return f"too long: {frames / SAMPLE_RATE:.2f}s"
    samples = array.array("h")
    samples.frombytes(pcm[: len(pcm) - len(pcm) % samples.itemsize])
    if sys.byteorder == "big":
        samples.byteswap()  # WAV PCM is little-endian
    if not samples:
        return "no audio data"
    peak = max(max(samples), -min(samples))
    if peak < SILENCE_PEAK:
        return f"silent: peak {peak}"
    clipped = sum(1 for s in samples if s >= CLIPPED_LEVEL or s <= -CLIPPED_LEVEL)
    if clipped > len(samples) * MAX_CLIPPED_SHARE:
        return f"clipped: {clipped / len(samples):.1%} of frames at full scale"
    return None


def fold_real_positives(run: pathlib.Path, recordings: pathlib.Path | None, oversample: int) -> int:
    """Copy the household's own recordings of the phrase into the positives.

    One model wakes the house for everyone, so every recording counts. The
    feature phase splits positives at random, so copies of one recording land
    on both sides: the ROC recall is optimistic for the household's voices.
    Judge the model in the room."""
    out = run / "generated_samples"
    out.mkdir(parents=True, exist_ok=True)
    for stale in out.glob(f"{REAL_PREFIX}*.wav"):
        stale.unlink()
    if recordings is None or oversample < 1 or not recordings.is_dir():
        sys.stdout.write("No household recordings — training on synthetic positives only\n")
        return 0
    used = 0
    for wav in sorted(recordings.rglob("*.wav")):
        if NEGATIVES in wav.relative_to(recordings).parts:   # the false wakes: never a positive (#825)
            continue
        if wav.name.startswith("."):   # #828: Haus writes through .<name>.part, a dotfile is never a whole recording
            continue
        reason = reject_reason(wav)
        if reason is not None:
            sys.stdout.write(f"  skip {wav.name}: {reason}\n")
            continue
        for copy in range(oversample):
            shutil.copyfile(wav, out / f"{REAL_PREFIX}{used:04d}_{copy:03d}.wav")
        used += 1
    sys.stdout.write(f"Folded {used} recording(s) in as {used * oversample} positives (x{oversample})\n")
    return used


def build_features(work: pathlib.Path, run: pathlib.Path, source: str = "generated_samples",
                   target: str = "generated_augmented_features") -> None:
    """Augment the clips in <run>/<source> and write the streaming spectrogram feature mmaps to <run>/<target>."""
    script = run / f"_build_{target}.py"
    script.write_text(_FEATURES_SCRIPT.replace("@WORK@", str(work)).replace("@SOURCE@", source).replace("@TARGET@", target))
    _run([sys.executable, str(script)], cwd=run)


def household_negatives(work: pathlib.Path, run: pathlib.Path, negatives: pathlib.Path | None) -> int:
    """#825: the false wakes the box kept, as a negative feature set; none leaves no set behind.
    16 kHz mono 16-bit and a quarter second at least; longer is fine, a negative has no length."""
    clips, features = run / "household_negatives", run / HOUSEHOLD_FEATURES
    shutil.rmtree(clips, ignore_errors=True)
    shutil.rmtree(features, ignore_errors=True)
    used = 0
    for wav in sorted(negatives.glob("*.wav")) if negatives is not None and negatives.is_dir() else []:
        try:
            with wave.open(str(wav), "rb") as w:
                ok = (w.getnchannels(), w.getsampwidth(), w.getframerate()) == (1, 2, SAMPLE_RATE) and w.getnframes() >= MIN_FRAMES
        except (wave.Error, EOFError, OSError):
            ok = False
        if not ok:
            sys.stdout.write(f"  skip negative {wav.name}\n")
            continue
        clips.mkdir(parents=True, exist_ok=True)
        shutil.copyfile(wav, clips / f"{used:05d}.wav")
        used += 1
    sys.stdout.write(f"{used} false wake(s) from the household as negatives\n")
    # #825: microWakeWord's Clips splits off a fifth for test and validation, which
    # leaves no training set below a handful (1 false wake: ValueError, the run
    # of 2026-10-28). Few clips are repeated up to MIN_SPLIT_CLIPS; a negative seen
    # twice weighs a little more, which a false wake of this house may.
    for i in range(used, MIN_SPLIT_CLIPS if 0 < used < MIN_SPLIT_CLIPS else used):
        shutil.copyfile(clips / f"{i % used:05d}.wav", clips / f"{i:05d}.wav")
    if used:
        build_features(work, run, clips.name, features.name)
    return used


def fetch_negatives(work: pathlib.Path) -> None:
    """Download microWakeWord's pre-generated negative spectrogram datasets."""
    out = work / "negative_datasets"
    if (out / "speech").exists():
        sys.stdout.write("negatives already present, skipping download\n")
        return
    out.mkdir(parents=True, exist_ok=True)
    root = "https://huggingface.co/datasets/kahrendt/microwakeword/resolve/main/"
    for fname in (
        "dinner_party.zip",
        "dinner_party_eval.zip",
        "no_speech.zip",
        "speech.zip",
    ):
        zp = out / fname
        _run(["wget", "-q", "-O", str(zp), root + fname])
        _run(["unzip", "-q", "-o", str(zp), "-d", str(out)])
        zp.unlink()


def write_training_config(work: pathlib.Path, run: pathlib.Path, steps: int) -> pathlib.Path:
    household = [{
        "features_dir": str(run / HOUSEHOLD_FEATURES),
        "sampling_weight": HOUSEHOLD_SAMPLING_WEIGHT,
        "penalty_weight": HOUSEHOLD_PENALTY_WEIGHT,
        "truth": False,
        "truncation_strategy": "random",
        "type": "mmap",
    }] if (run / HOUSEHOLD_FEATURES).is_dir() else []
    cfg = {
        "window_step_ms": 10,
        "train_dir": str(run / "trained_models" / "wakeword"),
        "features": [
            {
                "features_dir": str(run / "generated_augmented_features"),
                "sampling_weight": 2.0,
                "penalty_weight": 1.0,
                "truth": True,
                "truncation_strategy": "truncate_start",
                "type": "mmap",
            },
            {
                "features_dir": str(work / "negative_datasets" / "speech"),
                "sampling_weight": 10.0,
                "penalty_weight": 1.0,
                "truth": False,
                "truncation_strategy": "random",
                "type": "mmap",
            },
            {
                "features_dir": str(work / "negative_datasets" / "dinner_party"),
                "sampling_weight": 10.0,
                "penalty_weight": 1.0,
                "truth": False,
                "truncation_strategy": "random",
                "type": "mmap",
            },
            {
                "features_dir": str(work / "negative_datasets" / "no_speech"),
                "sampling_weight": 5.0,
                "penalty_weight": 1.0,
                "truth": False,
                "truncation_strategy": "random",
                "type": "mmap",
            },
            {
                "features_dir": str(work / "negative_datasets" / "dinner_party_eval"),
                "sampling_weight": 0.0,
                "penalty_weight": 1.0,
                "truth": False,
                "truncation_strategy": "split",
                "type": "mmap",
            },
        ] + household,
        "training_steps": [steps],
        "positive_class_weight": [1],
        "negative_class_weight": [20],
        "learning_rates": [0.001],
        "batch_size": 128,
        "time_mask_max_size": [0],
        "time_mask_count": [0],
        "freq_mask_max_size": [0],
        "freq_mask_count": [0],
        "eval_step_interval": 500,
        "clip_duration_ms": 1500,
        "target_minimization": 0.9,
        "minimization_metric": None,
        "maximization_metric": "average_viable_recall",
    }
    path = run / "training_parameters.yaml"
    path.write_text(yaml.safe_dump(cfg, sort_keys=False))
    return path


def train(work: pathlib.Path, mww: pathlib.Path, cfg: pathlib.Path) -> None:
    _run(
        [
            sys.executable,
            "-m",
            "microwakeword.model_train_eval",
            f"--training_config={cfg}",
            "--train",
            "1",
            "--restore_checkpoint",
            "1",
            "--test_tflite_streaming_quantized",
            "1",
            "--use_weights",
            "best_weights",
            "mixednet",
            "--pointwise_filters",
            "64,64,64,64",
            "--repeat_in_block",
            "1, 1, 1, 1",
            "--mixconv_kernel_sizes",
            "[5], [7,11], [9,15], [23]",
            "--residual_connection",
            "0,0,0,0",
            "--first_conv_filters",
            "32",
            "--first_conv_kernel_size",
            "5",
            "--stride",
            "3",
        ],
        cwd=mww,
    )


def export(run: pathlib.Path, out_tflite: pathlib.Path, cutoff: float, phrase: str, languages: list[str]) -> None:
    """The quantized streaming tflite and the ESPHome v2 manifest, each written
    to a temp name and renamed, so /out never holds half a model."""
    src = (
        run
        / "trained_models"
        / "wakeword"
        / "tflite_stream_state_internal_quant"
        / "stream_state_internal_quant.tflite"
    )
    if not src.exists():
        raise SystemExit(f"trained tflite not found at {src}")
    out_tflite.parent.mkdir(parents=True, exist_ok=True)
    tmp = out_tflite.with_name(out_tflite.name + ".tmp")
    shutil.copyfile(src, tmp)

    arena = _estimate_arena()
    manifest = {
        "type": "micro",
        "wake_word": phrase,
        "author": "Laib",
        "website": "https://github.com/mdopp/laib",
        "model": out_tflite.name,
        "trained_languages": languages,
        "version": 2,
        "micro": {
            "probability_cutoff": round(cutoff, 3),
            "sliding_window_size": 5,
            "feature_step_size": 10,
            "tensor_arena_size": arena,
            "minimum_esphome_version": "2024.7.0",
        },
    }
    manifest_path = out_tflite.with_suffix(".json")
    mtmp = manifest_path.with_name(manifest_path.name + ".tmp")
    mtmp.write_text(json.dumps(manifest, indent=2) + "\n")
    tmp.replace(out_tflite)
    mtmp.replace(manifest_path)
    sys.stdout.write(f"Wrote {out_tflite} ({out_tflite.stat().st_size} B)\n")
    sys.stdout.write(f"Wrote {manifest_path}\n{json.dumps(manifest, indent=2)}\n")


def _estimate_arena() -> int:
    """Tensor-arena hint; ESPHome computes/bumps the real value at flash time.

    There is no public TFLite API for the micro arena requirement, so we ship a
    safe okay_nabu-class default. If ESPHome rejects it at flash (Phase 2) it
    reports the exact bytes needed; bump this field then.
    """
    return 30000


def main(argv: list[str] | None = None) -> int:
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("--work", default="/work", help="shared work dir: sources, voices, negatives")
    ap.add_argument("--run", type=pathlib.Path, required=True, help="this wake word's dir: samples, features, checkpoints")
    ap.add_argument("--phrase", required=True, help='the wake phrase, e.g. "Hey Joshua"')
    ap.add_argument("--id", required=True, help="the model name, e.g. hey_joshua")
    ap.add_argument("--languages", default="de", help="comma list for the manifest, e.g. de,en")
    ap.add_argument("--voice", action="append", required=True, help="Piper voice .onnx (repeatable); grouped by language prefix")
    ap.add_argument("--out", type=pathlib.Path, required=True, help="the produced streaming tflite")
    ap.add_argument("--recordings", type=pathlib.Path, default=None, help="the household's own WAVs of the phrase")
    ap.add_argument("--negatives", type=pathlib.Path, default=None, help="the household's false wakes, WAVs (#825)")
    ap.add_argument("--positive-samples", type=int, default=2000)
    ap.add_argument("--real-oversample", type=int, default=DEFAULT_REAL_OVERSAMPLE)
    ap.add_argument("--steps", type=int, default=12000)
    ap.add_argument("--cutoff", type=float, default=0.97)  # #825: 0.95 woke on room talk; trainer.py passes WAKE_CUTOFF
    ap.add_argument("--phase", choices=["all", "generate", "real", "features", "household", "negatives", "train", "export"], default="all")
    args = ap.parse_args(argv)

    work = pathlib.Path(args.work)
    run = args.run
    run.mkdir(parents=True, exist_ok=True)
    mww = work / "microWakeWord"
    groups: dict[str, list[str]] = {}
    for v in args.voice:
        groups.setdefault(v.split("_")[0], []).append(v)
    languages = [l for l in args.languages.split(",") if l]

    if args.phase in ("all", "generate"):
        phase("generate")
        generate_positives(work, run, args.phrase, list(groups.values()), args.positive_samples)
    if args.phase in ("all", "real"):
        phase("real")
        fold_real_positives(run, args.recordings, args.real_oversample)
    if args.phase in ("all", "features"):
        phase("features")
        build_features(work, run)
    if args.phase in ("all", "household"):
        phase("household")
        household_negatives(work, run, args.negatives)
    if args.phase in ("all", "negatives"):
        phase("negatives")
        fetch_negatives(work)
    if args.phase in ("all", "train"):
        phase("train")
        cfg = write_training_config(work, run, args.steps)
        train(work, mww, cfg)
    if args.phase in ("all", "export"):
        phase("export")
        export(run, args.out, args.cutoff, args.phrase, languages)
    return 0


# Runs inside the run dir; relies on microWakeWord being importable. The
# background and RIR corpora, when present, sit in the shared work dir. Kept as a string
# so the producer is a single committed file (the box has no editor).
_FEATURES_SCRIPT = """\
import glob
import os
from mmap_ninja.ragged import RaggedMmap
from microwakeword.audio.augmentation import Augmentation
from microwakeword.audio.clips import Clips
from microwakeword.audio.spectrograms import SpectrogramGeneration

def _nonempty(dirs):
    return [d for d in dirs if glob.glob(os.path.join(d, "**", "*.wav"), recursive=True)]

bg = _nonempty([os.path.join("@WORK@", d) for d in ("fma_16k", "audioset_16k")])
rir = _nonempty([os.path.join("@WORK@", "mit_rirs")])

clips = Clips(
    input_directory="@SOURCE@",
    file_pattern="*.wav",
    max_clip_duration_s=None,
    remove_silence=False,
    random_split_seed=10,
    split_count=0.1,
)
augmenter = Augmentation(
    augmentation_duration_s=3.2,
    augmentation_probabilities={
        "SevenBandParametricEQ": 0.1,
        "TanhDistortion": 0.1,
        "PitchShift": 0.1,
        "BandStopFilter": 0.1,
        "AddColorNoise": 0.1,
        "AddBackgroundNoise": 0.75 if bg else 0.0,
        "Gain": 1.0,
        "RIR": 0.5 if rir else 0.0,
    },
    impulse_paths=rir,
    background_paths=bg,
    background_min_snr_db=-5,
    background_max_snr_db=10,
    min_jitter_s=0.195,
    max_jitter_s=0.205,
)

out_root = "@TARGET@"
os.makedirs(out_root, exist_ok=True)
for split, split_name, repetition, slide in (
    ("training", "train", 2, 10),
    ("validation", "validation", 1, 10),
    ("testing", "test", 1, 1),
):
    out_dir = os.path.join(out_root, split)
    os.makedirs(out_dir, exist_ok=True)
    spectrograms = SpectrogramGeneration(
        clips=clips, augmenter=augmenter, slide_frames=slide, step_ms=10
    )
    RaggedMmap.from_generator(
        out_dir=os.path.join(out_dir, "wakeword_mmap"),
        sample_generator=spectrograms.spectrogram_generator(
            split=split_name, repeat=repetition
        ),
        batch_size=100,
        verbose=True,
    )
"""


if __name__ == "__main__":
    sys.exit(main())
