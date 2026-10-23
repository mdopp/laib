# scan — paper stacks into Paperless (#568)

A stack of paper goes in; the documents in it come out in Paperless, each
on its own, blank pages gone, titled `JJJJ-MM-TT Absender - Titel`. One
stack at a time, and exactly one closing message per stack.

## Two ways in

**A — the inbox folder (default, no device control).** A scanner that scans
to a network folder writes to `\\<box>\data\scan-eingang` (Samba, the
file-share). One file is one stack: PDF, a multi-page TIFF, JPG or PNG. It
is taken once its size held still for 15 s and it carries no temporary
ending (`.part`, `.tmp`, `~`, a leading dot). A subfolder names the person
the message goes to: `scan-eingang/mama/…`. The file moves into the
stack's archive at once; nothing stays in the inbox. A PDF's own text layer
is used as it is, and its documents are cut from the original's pages.

**B — a SANE network scanner (optional, `SCANNER` set).** `laib scan start`
(Haus's „Stapel scannen“, the Bote, the CLI) scans the feeder, duplex as
`SCAN_DUPLEX` says, over the fixed `SCAN_AREA`. The WF-3540 needs
`210x297`; SANE's default 215.9×297.18 jams it. Only "out of documents" in
scanimage's whole output is a clean end. Any other stop is a pause: the
pages scanned stay, the state is `wartet-auf-rest`, and the person hears
which sheet to start the rest from. `weiter` scans on from that sheet (a
half-scanned sheet is dropped and scanned again), `abschliessen` files what
is there, `verwerfen` drops it. A reminder after 30 minutes; dropped after
a day, with a message. The device's own scan button is not used: the
WF-3540's never reaches SANE.

## One way through

1. Each page: Tesseract's orientation (turned at confidence ≥ 2), OCR `deu`,
   brightness. Blank is bright with fewer than 4 words, or a back page whose
   front shows through: mirrored, the OCR reads clearly more.
2. The household model (`MODEL_NAME`, through the lease's proxy, JSON,
   temperature 0.1) marks for each page whether a new document starts, and
   names each document's kind, sender, date and title. Documents are the runs
   from one start to the next, never page groups the model makes up. A page
   the model calls blank goes only when it is bright. While the card is lent
   to another mode the service waits up to 30 minutes. A model that fails or
   answers nonsense gives one document "Stapel unsortiert": nothing is lost.
   Above 40 pages the stack goes in overlapping windows of 30.
3. One PDF per document into Paperless' consume folder,
   `<consume>/<SCAN_TAG>/` (the folder is the tag), staged as `._scan-*.part`
   and renamed whole. A name taken gets ` (2)`.
4. The closing message through bote-notify, to the order's person, else the
   inbox subfolder's, else `SCAN_NOTIFY`, else box.yml `notify.person`:
   **FERTIG** („Scan fertig: 20 Blatt, 38 Seiten, 9 leer entfernt, 7 Dokumente
   in Paperless (Tag Stapel): …“), **WARTET**, **ABGEBROCHEN** („nichts
   abgelegt“) or **FEHLER**, also after a restart mid-stack, saying what is in
   Paperless already.

## The interface

**Orders** — `laib scan <action>` (MCP `laib_scan`, tier propose: the haus
hand by its verbs, the Bote by its ceiling) writes one JSON file into
`${DATA_DIR}/scan/auftrag/`: `{"aktion", "stapel", "person", "von", "at"}`.
Actions: `start`, `weiter`, `abschliessen`, `verwerfen`, `redo` (with
`stapel`), and for the setup `suchen`, `pruefen`, `probe` (#571). The
service takes them within seconds; one that does not fit the state (weiter
with nothing waiting) is logged and dropped.

`laib scan einrichten [--geraet <sane-gerät>]` writes no order: it is a
proposal. Without the scan service it adds the template with that scanner
(class human: the owner approves); with it, it sets `SCANNER` in its
values, a value change the box's dial decides like any other (`values:
auto` applies it and rolls back on red). A device name with a quote, a
newline, `%` or `$` is refused.

**Status** — `${DATA_DIR}/scan/laib-status.json`, written on every change,
read by `laib status`, `laib diag`, `laib scan status` and Haus:

```json
{
  "at": "2026-10-22T10:15:00Z",
  "detail": "Der Scanner hat bei Blatt 6 abgebrochen (…)",
  "zustand": "bereit | scannt | wartet-auf-rest | trennt | legt-ab | fertig | fehler",
  "seite": 12,
  "blatt": 6,
  "grund": "sane_start: Error during device I/O",
  "scanner": true,
  "eingang": "scan-eingang",
  "stapel": "2026-10-22T10-12-40",
  "person": "mama",
  "findings": [{"severity": "gelb", "detail": "…", "fix": "laib scan redo --stapel …"}],
  "letzte": [{"stapel": "…", "ende": "fertig | abgebrochen | fehler", "quelle": "scanner | ordner",
              "datei": "Scan_0001.pdf", "at": "…", "seiten": 38, "leer": 9, "meldung": "…",
              "dokumente": [{"titel": "2023-05-01 Mosel - Rechnung", "datei": "2023-05-01 Mosel - Rechnung.pdf"}]}]
}
```

`suche` is the last search, `{"zustand": "läuft | fertig | fehler", "at",
"detail", "gefragt", "einzelheiten"?, "funde": [{"geraet":
"epson2:net:192.168.178.87", "name": "Epson PID 0899 flatbed scanner"}]}`;
a `geraet` goes into `einrichten --geraet` as it is. `gefragt` is how many
LAN neighbours the search asked directly (#589). `pruefung` is the last
check, `{"geraet", "ok", "at", "detail", "einzelheiten"?, "probe"?}`: on
the first start with a new `SCANNER`, on `pruefen` (the device opened,
nothing scanned) and on `probe` (one sheet from the feeder, read, never
filed). Both stay in every status until the next search or check.

`detail` is always a German sentence for the family („Der Scanner unter
192.168.178.87 antwortet nicht – ist er an?“); the raw words of SANE, of a
socket or of a tool go only into `einzelheiten` (#589, #588), also on a
stack in `letzte` that failed before its first sheet.

`seite` is set while `scannt` (the page being scanned), `blatt` and `grund`
while `wartet-auf-rest` (the sheet to put the rest in from, the scanner's
reason). `fertig` and `fehler` are the last stack's end; a new stack may
start from them as from `bereit`. `scanner: false` means no way B: Haus
shows no „Stapel scannen“. `letzte` holds the last ten stacks, newest first;
a document's `datei` is its original file name in Paperless.

**Archive** — `${DATA_DIR}/scan/<stapel>/`: `original.<ext>` or `roh/`
(the scanner's pages in scan order), `seiten/` (turned), `plan.json` (each
page's verdict, the documents), `log.txt`, `stapel.json`. Not backed up;
gone after `SCAN_KEEP_DAYS`. `laib scan redo --stapel <stapel>` splits and
files it again (Paperless refuses a document it holds already).

## Values

| Value | Default | |
|---|---|---|
| `SCANNER` | empty | a SANE device, `epson2:net:192.168.178.87`; `<backend>:net:<host>` is added to the backend's config at start. Empty: only the inbox |
| `SCAN_DPI` | 300 | |
| `SCAN_MODE` | Gray | or Color |
| `SCAN_AREA` | 210x297 | mm |
| `SCAN_DUPLEX` | true | |
| `SCAN_BACK_FIRST` | true | duplex: the WF-3540 delivers each sheet's back first |
| `SCAN_TAG` | Stapel | the Paperless tag |
| `SCAN_NOTIFY` | empty | a person from box.yml `people`; empty: `notify.person` |
| `SCAN_KEEP_DAYS` | 14 | |
| `MODEL_NAME` | qwen3.6-35b-a3b | the household model |

No secret of its own: bote-notify's bearer (`BOTE_NOTIFY_TOKEN`, generated by
`laib add bote`) and the model key (`MODEL_KEY`) arrive in the environment,
never on argv.

## Setting it up

From Haus (haus#77): „Scannen einrichten“ proposes the service; „Scanner
suchen“ lists what the network answers, „Diesen einrichten“ (or a device
typed by hand, `epson2:net:<ip>`) sets it, the check follows by itself,
„Probeseite“ scans one sheet. The search finds epson2 devices (with `net
autodiscovery`, which the service adds) and eSCL/AirScan devices
(sane-airscan), and then asks the LAN's neighbours directly (#589): the
host's neighbour table (`/proc/net/arp`, the host's own, as `laib net
geraete` reads it; complete IPv4 entries on the default route's
interface), after one UDP knock on the discard port of each address of
their /24, as `laib net geraete --wecken` does. Each neighbour is asked on
two ports only, TCP 1865 (epson2: a find `epson2:net:<ip>`) and eSCL
(`/eSCL/ScannerCapabilities` on 80, then 443: `escl:http://<ip>:80`, the
SANE escl backend's name, or sane-airscan's device when it lists that
address), in parallel, 1.5 s per knock and 3 s all told after a 1.5 s
wake. A device asleep through the knock, or outside that /24, is not found:
type it by hand.

The check knocks on the device's port first (1865 for `epson2:net:`, the
URL's for `escl:`; an `airscan:` name carries no address and goes straight
to SANE): no answer is „antwortet nicht – ist er an?“, and SANE is not
asked.
A scanner that scans to a folder is set up in its own web menu: the share
`\\<box>\data`, the folder `scan-eingang`, a family member's Samba account.

By hand: `laib add scan SCANNER=<gerät>`.
The service runs on the host network (the scanner on the LAN, the lease's
proxy and bote-notify on loopback), so box.yml `hostNetwork` must list
`scan`; `laib add scan` proposes it, and the owner approves. The SANE
scanner needs a fixed address on the LAN. The image is
`ghcr.io/mdopp/laib-scan` from `scan/` in this repo; laib-images pulls it
nightly.

## Accepted when

1. A multi-page PDF in `\\<box>\data\scan-eingang` → its documents in
   Paperless and the FERTIG message.
2. A stack of ~20 sheets on the WF-3540, one stop rescued with `weiter` →
   FERTIG. At least 90 % of the documents split right, no blank page in
   Paperless.
