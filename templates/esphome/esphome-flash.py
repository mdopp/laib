"""The house persona's wake word onto the ESPHome devices (#759).

Stimme's trainer builds the persona's microWakeWord model (stimme/micro/<id>.tflite
and its manifest <id>.json); the Voice PE runs it only once its firmware carries it.
That was a step by hand in ESPHome, and the owner does not work on a console, so it
never happened. esphome-flash.service does it, and only `laib flash esphome` starts
it, after a human approved the repair Stimme's status names (laib repair status
stimme). The unit copies the model into /config/models on the host; this script runs
inside the ESPHome container (`podman exec -i … python3 -`, the stdlib only):

- the devices are the YAML files in /config that use micro_wake_word or import the
  Voice PE's package, found by their files, never by a name laib made up;
- each one's micro_wake_word models point at /config/models/<id>.json: a model of
  its own from /config/models (Solaris's, a persona's from before) goes, the
  upstream ones (okay_nabu …) and every other setting stay. A shape this edit does
  not know is left to a person, with the file named. The YAML from before stays
  beside it as <file>.laib-vorher;
- then `esphome run --no-logs --device OTA <file>` compiles it and uploads it over
  the air: the device restarts and is silent for about two minutes.

It exits 1 when a device failed or none was found, so the unit and laib diag say so.
Whether the device offers the phrase afterwards, Stimme measures through Home
Assistant (stimme-names), which then selects it.
"""
import os
import pathlib
import re
import subprocess
import sys

TOP = re.compile(r"^\S")   # a top-level line; a comment at column 0 ends no block


def item(wid: str, indent: str) -> list:
    return [f"{indent}- model: /config/models/{wid}.json\n", f"{indent}  id: {wid}\n"]


def with_model(text: str, wid: str) -> str:
    """the device YAML with micro_wake_word on the persona's model, every other line as it was"""
    lines = text.splitlines(keepends=True)
    start = next((i for i, l in enumerate(lines) if re.match(r"^micro_wake_word:\s*(#.*)?$", l)), None)
    if start is None:
        return text + ("" if text.endswith("\n") or not text else "\n") + "micro_wake_word:\n  models:\n" + "".join(item(wid, "    "))
    end = next((i for i in range(start + 1, len(lines)) if TOP.match(lines[i]) and not lines[i].startswith("#")), len(lines))
    block = lines[start + 1:end]
    m = next((i for i, l in enumerate(block) if l.strip().startswith("models:")), None)
    if m is None:
        while block and not block[-1].strip():
            block.pop()
        return "".join(lines[:start + 1] + block + ["  models:\n"] + item(wid, "    ") + lines[end:])
    if block[m].strip() != "models:":
        raise ValueError(f"micro_wake_word models in a form laib does not edit: {block[m].strip()}")
    deep = len(block[m]) - len(block[m].lstrip())
    stop = next((i for i in range(m + 1, len(block)) if block[i].strip() and len(block[i]) - len(block[i].lstrip()) <= deep), len(block))
    items, cur = [], None
    for line in block[m + 1:stop]:
        if line.lstrip().startswith("- "):
            cur = [line]
            items.append(cur)
        elif cur is not None:
            cur.append(line)
    indent = items[0][0][:len(items[0][0]) - len(items[0][0].lstrip())] if items else " " * (deep + 2)
    kept = [i for i in items if "/config/models/" not in "".join(i) and f"id: {wid}" not in "".join(i)]
    models = [line for i in kept for line in i] + item(wid, indent)
    return "".join(lines[:start + 1] + block[:m + 1] + models + block[stop:] + lines[end:])


def devices(config: pathlib.Path) -> list:
    """the wake-word devices, by their files"""
    out = []
    for p in sorted(config.glob("*.yaml")):
        if p.name == "secrets.yaml":
            continue
        text = p.read_text(encoding="utf-8")
        if re.search(r"^micro_wake_word:", text, re.M) or "home-assistant-voice-pe" in text:
            out.append(p)
    return out


def main(config: pathlib.Path, wid: str, run) -> int:
    missing = [f for f in (f"{wid}.json", f"{wid}.tflite") if not (config / "models" / f).is_file()]
    if not wid or missing:
        print(f"esphome-flash: no model {wid or '(no wake word)'} in /config/models ({', '.join(missing)}): is Stimme's trainer done?")
        return 1
    found = devices(config)
    if not found:
        print("esphome-flash: no device in /config uses micro_wake_word or the Voice PE package: nothing to flash")
        return 1
    failed = []
    for p in found:
        text = p.read_text(encoding="utf-8")
        try:
            new = with_model(text, wid)
        except ValueError as err:
            print(f"esphome-flash: {p.name}: {err}; edit it by hand in ESPHome")
            failed.append(p.stem)
            continue
        if new != text:
            (p.parent / (p.name + ".laib-vorher")).write_text(text, encoding="utf-8")
            p.write_text(new, encoding="utf-8")
            print(f"esphome-flash: {p.name}: micro_wake_word on {wid} (the file from before: {p.name}.laib-vorher)")
        print(f"esphome-flash: {p.stem}: compiling and uploading over the air")
        if run(["esphome", "run", "--no-logs", "--device", "OTA", str(p)]) != 0:
            print(f"esphome-flash: {p.stem}: NOT uploaded (esphome's output above): is the device on and in the network?")
            failed.append(p.stem)
        else:
            print(f"esphome-flash: {p.stem}: uploaded, the device restarts")
    return 1 if failed else 0


if __name__ == "__main__":
    sys.exit(main(pathlib.Path(os.environ.get("ESPHOME_CONFIG", "/config")), os.environ.get("WAKE_ID", "").strip(),
                  lambda cmd: subprocess.run(cmd, stdin=subprocess.DEVNULL).returncode))
