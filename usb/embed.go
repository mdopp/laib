// Package usb carries the Butane template and the live-install scripts that
// laib-stick puts on an install stick.
package usb

import _ "embed"

// Butane is the box's configuration template (${VARS} substituted).
//
//go:embed fedora-coreos.bu
var Butane string

// PreInstall picks the OS disk in the live system and writes it to installer.d.
//
//go:embed pre-install.sh
var PreInstall string

// PostInstall promotes the installed disk in the EFI boot order.
//
//go:embed post-install.sh
var PostInstall string
