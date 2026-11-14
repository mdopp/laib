"""The print server's rule (owner 2026-11-14): every printer found in the home network or at the
box's USB gets a queue with the driver CUPS matches, driverless first; it is shared on the LAN only
when it does not already reach every device by itself; the box's own shared queues are never found
again as printers. The fixtures are what lpinfo and avahi-browse print."""
import importlib.util
import unittest
from pathlib import Path

spec = importlib.util.spec_from_file_location("finden", Path(__file__).resolve().parent.parent / "druck-finden.py")
finden = importlib.util.module_from_spec(spec)
spec.loader.exec_module(finden)

# lpinfo -l -v --include-schemes dnssd,usb: the Epson over Wi-Fi (two services), an HP that speaks
# IPP Everywhere, a LaserJet with only port 9100, a Brother at the USB, and the box's own shared queue
LPINFO_V = """Device: uri = dnssd://EPSON%20WF-3540%20Series._ipp._tcp.local/?uuid=cfe92100-67c4-11d4-a45f-ac18260a36a8
        class = network
        info = EPSON WF-3540 Series
        make-and-model = EPSON WF-3540 Series
        device-id = MFG:EPSON;MDL:WF-3540 Series;CMD:ESCPL2,BDC,D4,D4PX,ESCPR7,END4,GENEP,URF;
        location =
Device: uri = dnssd://EPSON%20WF-3540%20Series._printer._tcp.local/
        class = network
        info = EPSON WF-3540 Series
        make-and-model = EPSON WF-3540 Series
        device-id = MFG:EPSON;MDL:WF-3540 Series;CMD:ESCPL2,BDC,D4,D4PX,ESCPR7,END4,GENEP,URF;
        location =
Device: uri = dnssd://HP%20OfficeJet%20Pro%209010%20series%20%5B4B2A1C%5D._ipp._tcp.local/?uuid=1c852a4d-b800-1f08-abcd-a0481c4b2a1c
        class = network
        info = HP OfficeJet Pro 9010 series [4B2A1C]
        make-and-model = HP OfficeJet Pro 9010 series
        device-id = MFG:HP;MDL:OfficeJet Pro 9010 series;CMD:PCL,PCLXL,PostScript,PDF,PWG,URF;
        location =
Device: uri = dnssd://HP%20LaserJet%201320._pdl-datastream._tcp.local/
        class = network
        info = HP LaserJet 1320
        make-and-model = HP LaserJet 1320
        device-id = MFG:Hewlett-Packard;MDL:HP LaserJet 1320 series;CMD:PJL,PCL,POSTSCRIPT;
        location =
Device: uri = usb://Brother/HL-1110%20series?serial=E71798J6N123456
        class = direct
        info = Brother HL-1110 series
        make-and-model = Brother HL-1110 series
        device-id = MFG:Brother;CMD:GDI;MDL:HL-1110 series;CLS:PRINTER;
        location =
Device: uri = usb://Canon/LBP2900?serial=0000A1B2C3D4
        class = direct
        info = Canon LBP2900
        make-and-model = Canon LBP2900
        device-id = MFG:Canon;MDL:LBP2900;CMD:CAPT;
        location =
Device: uri = dnssd://Epson%20WF-3540%20(%C3%BCber%20die%20Box)%20%40%20drucker._ipp._tcp.local/?uuid=0f3c6a59-0000-3000-8000-000000000001
        class = network
        info = Epson WF-3540 (über die Box) @ drucker
        make-and-model = Local Raw Printer
        device-id = MFG:Epson;MDL:WF-3540;
        location =
"""

# avahi-browse -rtpk _ipp._tcp: the Epson takes URF and JPEG only; the OfficeJet PDF, PWG and URF
AVAHI = r"""+;wlp2s0;IPv4;EPSON\032WF-3540\032Series;_ipp._tcp;local
=;wlp2s0;IPv4;EPSON\032WF-3540\032Series;_ipp._tcp;local;EPSON0A36A8.local;192.168.178.87;631;"txtvers=1" "ty=EPSON WF-3540 Series" "rp=Epson_IPP_Printer" "pdl=application/octet-stream,image/urf,image/jpeg" "URF=CP1,MT1-3-8-10-11-12,PQ4-5,OB9,OFU0,RS360,SRGB24,W8,IS1,V1.4" "UUID=cfe92100-67c4-11d4-a45f-ac18260a36a8"
=;wlp2s0;IPv6;EPSON\032WF-3540\032Series;_ipp._tcp;local;EPSON0A36A8.local;fe80::a6ee:57ff:fe0a:36a8;631;"txtvers=1" "rp=Epson_IPP_Printer"
=;wlp2s0;IPv4;HP\032OfficeJet\032Pro\0329010\032series\032\0914B2A1C\093;_ipp._tcp;local;HP4B2A1C.local;192.168.178.40;631;"txtvers=1" "ty=HP OfficeJet Pro 9010 series" "rp=ipp/print" "pdl=application/pdf,image/pwg-raster,image/urf,image/jpeg" "URF=W8,CP1,IS4-1,MT1-2-8-9-10-11,OB10,PQ4,RS300-600,V1.4,DM1" "UUID=1c852a4d-b800-1f08-abcd-a0481c4b2a1c"
"""

MODELS = {
    "MFG:EPSON;MDL:WF-3540 Series;": finden.parse_models(
        "gutenprint.5.3://escp2-wf3540/expert Epson WF-3540 - CUPS+Gutenprint v5.3.5\n"
        "escpr:0/cups/model/epson-inkjet-printer-escpr/Epson-WF-3540_Series-epson-escpr-en.ppd Epson WF-3540 Series, Epson Inkjet Printer Driver (ESC/P-R) for Linux\n"),
    "MFG:HP;MDL:OfficeJet Pro 9010 series;": finden.parse_models(
        "drv:///hpcups.drv/hp-officejet_pro_9010_series.ppd HP OfficeJet Pro 9010 Series, hpcups 3.25.2\n"
        "driverless:ipp://HP%20OfficeJet%20Pro%209010%20series%20%5B4B2A1C%5D._ipp._tcp.local/ HP OfficeJet Pro 9010 series, driverless, cups-filters 1.28.17\n"),
    "MFG:Brother;MDL:HL-1110 series;": finden.parse_models(
        "gutenprint.5.3://brother-hl-1110/expert Brother HL-1110 - CUPS+Gutenprint v5.3.5\n"),
    "MFG:Hewlett-Packard;MDL:HP LaserJet 1320 series;": finden.parse_models(
        "drv:///sample.drv/generic.ppd Generic PostScript Printer\n"
        "drv:///hpcups.drv/hp-laserjet_1320.ppd HP LaserJet 1320 Series, hpcups 3.25.2\n"),
}


def found(**kw):
    return {p["key"]: p for p in finden.plan(finden.parse_devices(LPINFO_V), finden.parse_avahi(AVAHI), MODELS, **kw)}


class Rule(unittest.TestCase):
    # Ablauf L-46
    def test_the_epson_is_offered_with_its_vendor_driver_because_it_takes_no_pdf(self):
        p = found()["netz:EPSON WF-3540 Series"]
        self.assertTrue(p["model"].startswith("escpr:"), "escpr ranks before Gutenprint: Epson's own driver")
        self.assertEqual(p["kind"], "hersteller")
        self.assertTrue(p["shared"], "URF and JPEG only: Android's own print service cannot print to it")
        self.assertEqual(p["name"], "Epson WF-3540")
        self.assertIn("._ipp._tcp.local", p["uri"], "the IPP service wins over LPD, and a DNS-SD name, not an address")
        self.assertNotIn("192.168", p["uri"])

    # Ablauf L-46
    def test_a_printer_that_reaches_every_device_itself_is_not_offered_twice(self):
        p = found()["netz:HP OfficeJet Pro 9010 series [4B2A1C]"]
        self.assertEqual(p["kind"], "driverless")
        self.assertTrue(p["model"].startswith("driverless:"), "driverless before hpcups when it takes PDF or PWG")
        self.assertFalse(p["shared"], "URF and PDF: iPhone, Android and Windows find it without the box")

    def test_a_network_printer_without_ipp_gets_its_vendor_driver_and_is_shared(self):
        p = found()["netz:HP LaserJet 1320"]
        self.assertIn("hpcups", p["model"], "the generic PPD never wins")
        self.assertTrue(p["shared"])
        self.assertNotIn("ha", p, "no IPP of its own: Home Assistant reaches it only through the box")

    # Ablauf L-46
    def test_a_usb_printer_is_always_shared(self):
        p = found()["usb:usb://Brother/HL-1110%20series?serial=E71798J6N123456"]
        self.assertIn("gutenprint", p["model"])
        self.assertTrue(p["shared"], "nothing else in the home network reaches a printer at the box's USB")
        self.assertEqual(p["name"], "Brother HL-1110")

    def test_a_printer_without_a_driver_gets_no_queue_and_a_reason(self):
        p = found()["usb:usb://Canon/LBP2900?serial=0000A1B2C3D4"]
        self.assertNotIn("model", p, "CAPT: no bundled driver, and over USB no driverless way")
        self.assertIn("Treiber", p["why"])
        self.assertEqual(finden.ha_list([p], {}), [])

    def test_the_box_never_finds_its_own_queue_again(self):
        self.assertFalse([k for k in found() if "Box" in k])

    def test_a_queue_keeps_its_name_and_names_do_not_collide(self):
        ps = finden.queue_names(list(found().values()), {"netz:EPSON WF-3540 Series": {"queue": "Epson_WF-3540"}})
        names = [p["queue"] for p in ps if "queue" in p]
        self.assertIn("Epson_WF-3540", names)
        self.assertEqual(len(names), len(set(names)))
        self.assertEqual(finden.info(next(p for p in ps if p.get("queue") == "Epson_WF-3540")), "Epson WF-3540 (über die Box)")

    def test_zero_printers_is_a_sentence_not_a_failure(self):
        self.assertEqual(finden.plan([], {}, {}), [])
        self.assertIn("Noch kein Drucker", finden.detail([]))

    def test_a_printer_away_for_a_while_stays_and_is_forgotten_after_the_days(self):
        known = {"a": {"gesehen": 0}, "b": {"gesehen": 0}}
        self.assertEqual(finden.forget(known, {"a"}, 29 * 86400), [])
        self.assertEqual(finden.forget(known, {"a"}, 31 * 86400), ["b"])


# What the box's CUPS said on 2026-11-14 (lpinfo -l -v, abridged): CUPS' bare backends, the
# virtual BRF printer, the Epson under its DNS-SD name „EPSON“ from three backends, and snmp's address
BOX_V = """Device: uri = beh
        class = network
        info = Backend Error Handler
        make-and-model = Unknown
        device-id = 
        location = 
Device: uri = cups-brf:/
        class = file
        info = CUPS-BRF
        make-and-model = Virtual Braille BRF Printer
        device-id = MFG:Generic;MDL:CUPS-BRF Printer;DES:Generic CUPS-BRF Printer;CLS:PRINTER;CMD:BRF;
        location = 
Device: uri = ipp
        class = network
        info = Internet Printing Protocol (ipp)
        make-and-model = Unknown
        device-id = 
        location = 
Device: uri = dnssd://EPSON._pdl-datastream._tcp.local/
        class = network
        info = EPSON
        make-and-model = EPSON WF-3540 Series
        device-id = MFG:EPSON;MDL:WF-3540 Series;
        location = 
Device: uri = ipp://EPSON._ipp._tcp.local/
        class = network
        info = EPSON WF-3540 Series (driverless)
        make-and-model = EPSON WF-3540 Series
        device-id = MFG:EPSON;MDL:WF-3540 Series;CMD:AppleRaster,URF,JPEG;
        location = 
Device: uri = lpd://192.168.178.87:515/PASSTHRU
        class = network
        info = EPSON WF-3540 Series
        make-and-model = EPSON WF-3540 Series
        device-id = MFG:EPSON;CMD:ESCPL2,BDC,D4,D4PX,ESCPR1;MDL:WF-3540 Series;CLS:PRINTER;DES:EPSON WF-3540 Series;
        location = 
Device: uri = dnssd://EPSON._printer._tcp.local/
        class = network
        info = EPSON
        make-and-model = EPSON WF-3540 Series
        device-id = MFG:EPSON;MDL:WF-3540 Series;
        location = 
"""
BOX_AVAHI = r"""=;eno1;IPv4;EPSON;_ipp._tcp;local;EPSON0A36A8.local;192.168.178.87;631;"UUID=7c47f900-67ff-11d4-9a7f-b0e8920a36a8" "URF=CP1,MT1-3-8-10-11-12,PQ4-5,OB9,OFU0,RS360,SRGB24,W8,DM3,IS1-2-18" "rp=Epson_IPP_Printer" "pdl=application/octet-stream,image/urf,image/jpeg" "product=(EPSON WF-3540 Series)" "usb_MDL=WF-3540 Series" "usb_MFG=EPSON" "ty=EPSON WF-3540 Series" "txtvers=1"
"""
BOX_M = ("escpr:0/cups/model/epson-inkjet-printer-escpr/Epson-WF-3540_Series-epson-escpr-en.ppd Epson WF-3540 Series, Epson Inkjet Printer Driver (ESC/P-R) for Linux\n"
         "driverless:ipp://EPSON._ipp._tcp.local/ EPSON WF-3540 Series, driverless, cups-filters 1.28.17\n")


class TheBox(unittest.TestCase):
    """the first day on the box: no queue, because the round asked CUPS for the BRF printer's PPDs,
    got „not found“ and gave up, and the Epson's IPP service came as ipp://EPSON._ipp._tcp.local/"""

    def test_only_the_printers_are_looked_up_by_mfg_and_mdl(self):
        devs = finden.parse_devices(BOX_V)
        ids = sorted({finden.match_id(d) for d, _ in finden.chosen(devs).values()})
        self.assertEqual(ids, ["MFG:EPSON;MDL:WF-3540 Series;"], "no lookup for cups-brf, beh or snmp's address")

    # Ablauf L-46
    def test_the_epson_gets_escpr_over_its_ipp_name(self):
        devs = finden.parse_devices(BOX_V)
        ps = finden.plan(devs, finden.parse_avahi(BOX_AVAHI), {"MFG:EPSON;MDL:WF-3540 Series;": finden.parse_models(BOX_M)})
        self.assertEqual([p["key"] for p in ps], ["netz:EPSON"])
        p = ps[0]
        self.assertEqual(p["uri"], "ipp://EPSON._ipp._tcp.local/", "IPP by its DNS-SD name, never 192.168.178.87")
        self.assertTrue(p["model"].startswith("escpr:"))
        self.assertTrue(p["shared"])
        self.assertEqual(p["name"], "Epson WF-3540")
        self.assertEqual((p["ha"]["host"], p["ha"]["pfad"]), ("EPSON0A36A8.local", "/Epson_IPP_Printer"))

    def test_no_ppd_found_is_an_empty_list_not_a_failed_round(self):
        self.assertEqual(finden.run("sh", "-c", "echo 'lpinfo: client-error-not-found' >&2; exit 1", none="not-found"), "")
        with self.assertRaises(RuntimeError):
            finden.run("sh", "-c", "echo 'lpinfo: server-error-internal-error' >&2; exit 1", none="not-found")


class HomeAssistant(unittest.TestCase):
    def test_a_network_printer_goes_in_by_its_own_name_a_box_only_one_by_the_queue(self):
        ps = finden.queue_names(list(found().values()), {})
        ha = {h["name"]: h for h in finden.ha_list(ps, {"HP_LaserJet_1320": "aaaa-1320"})}
        epson = ha["Epson WF-3540"]
        self.assertEqual((epson["host"], epson["port"], epson["pfad"], epson["tls"]), ("EPSON0A36A8.local", 631, "/Epson_IPP_Printer", False))
        self.assertEqual(epson["uuid"], "cfe92100-67c4-11d4-a45f-ac18260a36a8")
        self.assertIn("HP OfficeJet Pro 9010", ha, "unshared, still the household's printer: its ink in Home Assistant")
        lj = ha["HP LaserJet 1320 (über die Box)"]
        self.assertEqual((lj["host"], lj["port"], lj["pfad"], lj["uuid"]), ("127.0.0.1", 631, "/printers/HP_LaserJet_1320", "aaaa-1320"))
        self.assertFalse([h for h in ha if h.startswith("Epson") and "Box" in h], "the Epson's re-share is never a second entry")
        self.assertIn("Brother HL-1110 (über die Box)", ha, "a USB printer goes in through the box's queue")


class Parsing(unittest.TestCase):
    def test_avahi_escapes_and_ipv4_first(self):
        a = finden.parse_avahi(AVAHI)
        e = a[("EPSON WF-3540 Series", "_ipp._tcp")]
        self.assertEqual(e["address"], "192.168.178.87")
        self.assertEqual(e["txt"]["pdl"], "application/octet-stream,image/urf,image/jpeg")
        self.assertIn(("HP OfficeJet Pro 9010 series [4B2A1C]", "_ipp._tcp"), a)

    def test_dnssd_names(self):
        self.assertEqual(finden.dnssd_name("dnssd://EPSON%20WF-3540%20Series._ipp._tcp.local/?uuid=x"), ("EPSON WF-3540 Series", "_ipp._tcp"))
        self.assertEqual(finden.dnssd_name("socket://10.0.0.1"), (None, None))


if __name__ == "__main__":
    unittest.main()
