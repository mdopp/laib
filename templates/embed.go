// Package templates ships the built-in templates inside the binary, so a
// fresh box can add caddy, authelia, whoami or werkstatt offline.
package templates

import "embed"

// FS holds every built-in template folder (ADR 0004).
//
//go:embed adguard auth caddy whoami werkstatt bote frage media audiobookshelf music-assistant radicale vaultwarden mosquitto esphome beets home-assistant immich file-share stimme paperless haus llama usage-metrics kalender
var FS embed.FS
