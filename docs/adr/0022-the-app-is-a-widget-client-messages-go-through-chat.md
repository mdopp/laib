# ADR 0022 — The app is a widget client; messages go through the person's chat; nothing old is carried

Applies when you are about to add a feature to the Android app, a path to
Haus's `/napi`, a notification of any kind ("Waschmaschine fertig", an
approval, a reminder), or a compatibility shim for the Solaris app.

- **Status:** Accepted, 2026-10-14
- **Deciders:** the owner (mdopp):
  - "Muss die Solaris app das überhaupt noch machen? … Sondiere das Mal!"
  - "Alles neu! Keine rückwärts Kompatibilität!"
  - "Ich mag meine Widgets." (after the comparison with the Home Assistant app)
  - Option 1 for keeping widgets current: "Ich will das Telefon nicht zumüllen."

## Context

The Solaris app (`solaris-android`, about 14,000 lines of Kotlin) wraps
Solaris's PWA as a TWA. It carries native widgets, a foreground realtime
service with notices, catch-up, a 4-minute poll and approvals, plus
diagnostics. Most of its jobs now have a better home:

- **Conversation:** the Bote talks with people on Signal, Matrix, Telegram,
  Discord and more (Hermes' gateway, ADR 0016).
- **Approvals:** they reach the admin through the relay (ADR 0012, #48).
- **Pages:** Haus's pages run in any browser (haus#22, haus#24).

What only the app does well is the widgets. The Home Assistant app's
widgets are plainer:

- one entity, a button, a media player, or a picture refreshed hourly;
- a template as text;
- a to-do list;
- no graphs, no lists of entities;
- a 30-minute refresh unless given notification access.

Hermes delivers to any of its platforms with `send_message`. That is
deliberately not a model tool. It is reachable through `hermes send` and
its stdio MCP server, both of which use Hermes' own per-platform senders.

## Decision

1. **The app is a widget client, built new.** It is a new app (new
   package id, new scheme, new token prefix, no Solaris names). It holds
   the home-screen widgets (devices, energy, camera, active devices, the
   household's tools) and pairing. It has no TWA or PWA shell, no
   approvals, no notices and no diagnostics. The widget code comes over
   from the old app; everything else stays behind.

2. **Widgets stay current through one lean live stream, only while the
   screen is on.**
   - Haus sends state changes (`card_state`) for the entities the device
     watches.
   - There are no notices, no catch-up and no polling with the screen off.
     On screen-on the app reconnects and resyncs.
   - No other app is needed on the phone.
   - UnifiedPush/ntfy was weighed and set aside: it would need an extra
     app on every phone.

3. **Messages to people go through the person's chat channel, not the
   app.**
   - A message is addressed to a **person**. It goes out on that person's
     channel (Signal, Matrix, Telegram, …), from the Bote, so the person
     can answer it.
   - A small service in the Bote's pod, built on the Hermes image,
     accepts a message for a person over HTTP with a token. It resolves
     the person's target from the state and delivers it with Hermes' own
     sender (`hermes send`).
   - Home Assistant automations call it.
   - People create those automations by voice or chat with the Bote's
     `automat` tool (draft, plain-words summary, a confirmation code, then
     Home Assistant).
   - People and their channels are declared once, in `box.yml`.

4. **Nothing old is carried (ADR 0010, sharpened).** These go, with no
   end condition and no compatibility period:
   - Haus's Solaris bridge;
   - the old app's names (`cloud.dopp.solaris`, `sol_device_`,
     `/napi/servicebay`);
   - the old app itself.

   Phones pair again with the new app. ADR 0021's legacy rows for Solaris
   and the app end here.

## Consequences

- Haus loses `internal/legacy`, the bridge and `SOLARIS_URL`. Its `/napi`
  becomes the new app's API: the Box keys, the house paths, the tool rows,
  camera, upload, the events stream and persona.
- The Solaris notification slice of haus#10 is not built. Neither is the
  name switch of haus#17.
- `solaris-android` is superseded; the new app is its successor. Whether
  it lives in a renamed repo or a new one is the owner's call (#30).
- `box.yml` gets the household's people and their channels. The Bote's
  `chatPersonas` keep mapping a number to a persona.
- The first real proof is Phase 2 on the NUC:
  - a sentence to the Bote becomes an automation;
  - the automation fires;
  - the message arrives on the person's channel.

  Until then the delivery path is proven in tests only.
