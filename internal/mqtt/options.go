// Package mqtt publishes pwled state to an MQTT broker and exposes presets
// to Home Assistant via its MQTT discovery protocol.
//
// Currently exposes one entity: a `select` whose options are the preset
// names from the live config. Selecting an option applies that preset; the
// state reflects the active preset name (empty if the live DSP settings
// don't match any preset).
package mqtt

import (
	"errors"
	"os"
	"strings"
)

// Options is the CLI-supplied connection + naming config. An empty Broker
// disables MQTT entirely (the constructor returns ErrDisabled).
type Options struct {
	Broker          string // e.g. "tcp://192.168.1.10:1883". Empty = disabled.
	User, Pass      string // optional credentials
	DiscoveryPrefix string // HA discovery topic prefix; defaults to "homeassistant"
	NodeID          string // identifier used in topics + unique_id; defaults to "pwled"
	ClientID        string // MQTT client ID; defaults to "pwled-<NodeID>-<hostname>"
}

// ErrDisabled signals that MQTT is not configured (empty Broker).
var ErrDisabled = errors.New("mqtt: disabled (no broker)")

// withDefaults returns a copy of o with empty fields filled from sensible
// defaults — DiscoveryPrefix=homeassistant, NodeID=pwled, ClientID derived
// from hostname.
func (o Options) withDefaults() Options {
	if o.DiscoveryPrefix == "" {
		o.DiscoveryPrefix = "homeassistant"
	}
	if o.NodeID == "" {
		o.NodeID = "pwled"
	}
	o.NodeID = sanitizeID(o.NodeID)
	if o.ClientID == "" {
		host, _ := os.Hostname()
		host = sanitizeID(host)
		if host == "" {
			host = "node"
		}
		o.ClientID = "pwled-" + o.NodeID + "-" + host
	}
	return o
}

// sanitizeID strips characters that aren't valid in MQTT topic segments or
// HA unique_ids: keep [a-z0-9_-], lowercase, collapse other runs to '_'.
func sanitizeID(s string) string {
	s = strings.ToLower(s)
	var b strings.Builder
	prevUnderscore := false
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-', r == '_':
			b.WriteRune(r)
			prevUnderscore = r == '_'
		default:
			if !prevUnderscore && b.Len() > 0 {
				b.WriteByte('_')
				prevUnderscore = true
			}
		}
	}
	return strings.Trim(b.String(), "_-")
}
