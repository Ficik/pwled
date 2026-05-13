package mqtt

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"reflect"
	"sync"
	"sync/atomic"
	"time"

	paho "github.com/eclipse/paho.mqtt.golang"

	"pwled/internal/config"
)

// Client publishes pwled state to MQTT and exposes presets + an activity
// binary_sensor to Home Assistant via its discovery protocol.
//
// All entities live under `<DiscoveryPrefix>/<component>/<NodeID>/<object>/`,
// while runtime state and the command topic live under
// `pwled/<NodeID>/...`. A retained LWT on
// `pwled/<NodeID>/availability` flips HA to "unavailable" if pwled crashes.
type Client struct {
	opts Options
	cfg  *config.Store
	mq   paho.Client

	mu           sync.Mutex
	lastActivity string // last activity payload pushed; avoids needless republish churn
	closed       atomic.Bool
}

// topic prefixes derived from Options.NodeID.
func (c *Client) base() string         { return "pwled/" + c.opts.NodeID }
func (c *Client) availTopic() string   { return c.base() + "/availability" }
func (c *Client) presetSet() string    { return c.base() + "/preset/set" }
func (c *Client) presetState() string  { return c.base() + "/preset/state" }
func (c *Client) activityState() string { return c.base() + "/activity/state" }

func (c *Client) discoveryTopic(component, object string) string {
	return fmt.Sprintf("%s/%s/%s/%s/config", c.opts.DiscoveryPrefix, component, c.opts.NodeID, object)
}

// New connects to the broker and publishes initial Home Assistant discovery
// payloads. Returns ErrDisabled when opts.Broker is empty so callers can fall
// through with a nil-safe sentinel.
//
// The returned client subscribes to the preset command topic and republishes
// discovery when the preset list changes (e.g. via the web UI).
func New(opts Options, cfg *config.Store) (*Client, error) {
	if opts.Broker == "" {
		return nil, ErrDisabled
	}
	opts = opts.withDefaults()

	c := &Client{opts: opts, cfg: cfg}

	o := paho.NewClientOptions().
		AddBroker(opts.Broker).
		SetClientID(opts.ClientID).
		SetCleanSession(true).
		SetAutoReconnect(true).
		SetConnectRetry(true).
		SetConnectRetryInterval(5 * time.Second).
		SetKeepAlive(30 * time.Second).
		SetWill(c.availTopic(), "offline", 1, true).
		SetOnConnectHandler(c.onConnect).
		SetConnectionLostHandler(func(_ paho.Client, err error) {
			slog.Warn("mqtt", "event", "disconnected", "err", err)
		})
	if opts.User != "" {
		o.SetUsername(opts.User)
		o.SetPassword(opts.Pass)
	}

	c.mq = paho.NewClient(o)
	tok := c.mq.Connect()
	// First connect happens in the background (AutoReconnect handles retries).
	// We wait briefly so a typo in the broker URL is visible at startup.
	if !tok.WaitTimeout(5 * time.Second) {
		slog.Warn("mqtt", "event", "initial connect pending", "broker", opts.Broker)
	} else if err := tok.Error(); err != nil {
		slog.Warn("mqtt", "event", "initial connect failed", "broker", opts.Broker, "err", err)
	}

	return c, nil
}

// onConnect publishes availability + discovery + current state every time the
// connection (re-)opens, so HA picks the entities back up after broker
// restarts.
func (c *Client) onConnect(mq paho.Client) {
	slog.Info("mqtt", "event", "connected", "broker", c.opts.Broker)
	mq.Publish(c.availTopic(), 1, true, "online")
	c.publishDiscovery()
	c.publishPresetState()
	// Activity state is re-sent by the next frame; nothing to do here.

	if tok := mq.Subscribe(c.presetSet(), 1, c.handlePresetSet); tok.Wait() && tok.Error() != nil {
		slog.Warn("mqtt", "event", "subscribe failed", "topic", c.presetSet(), "err", tok.Error())
	}

	// Watch for preset-list changes so HA's `select` options stay in sync.
	go c.watchConfig()
}

// watchConfig is started once per (re)connect; it tears down when the config
// store closes or the client is closed.
func (c *Client) watchConfig() {
	sub := c.cfg.Subscribe()
	prevNames := presetNames(c.cfg.Load())
	prevActive := activePreset(c.cfg.Load())
	for cfg := range sub {
		if c.closed.Load() {
			return
		}
		curNames := presetNames(cfg)
		if !reflect.DeepEqual(curNames, prevNames) {
			c.publishDiscovery()
			prevNames = curNames
		}
		curActive := activePreset(cfg)
		if curActive != prevActive {
			c.publishPresetState()
			prevActive = curActive
		}
	}
}

// publishDiscovery sends both entity discovery payloads (retained).
func (c *Client) publishDiscovery() {
	cfg := c.cfg.Load()

	host, _ := os.Hostname()
	device := map[string]any{
		"identifiers":  []string{"pwled-" + c.opts.NodeID},
		"name":         "pwled " + c.opts.NodeID,
		"model":        "pwled",
		"manufacturer": "pwled",
		"sw_version":   "",
	}
	if host != "" {
		device["configuration_url"] = "" // optional; populate if/when HTTP UI is reachable from HA
	}
	availability := []map[string]string{
		{"topic": c.availTopic(), "payload_available": "online", "payload_not_available": "offline"},
	}

	presetCfg := map[string]any{
		"name":          "Preset",
		"unique_id":     c.opts.NodeID + "_preset",
		"object_id":     c.opts.NodeID + "_preset",
		"state_topic":   c.presetState(),
		"command_topic": c.presetSet(),
		"options":       presetNames(cfg),
		"availability":  availability,
		"device":        device,
		"icon":          "mdi:tune-vertical",
	}
	c.publishJSON(c.discoveryTopic("select", "preset"), presetCfg, true)

	activityCfg := map[string]any{
		"name":         "Activity",
		"unique_id":    c.opts.NodeID + "_activity",
		"object_id":    c.opts.NodeID + "_activity",
		"state_topic":  c.activityState(),
		"payload_on":   "active",
		"payload_off":  "idle",
		"device_class": "sound",
		"availability": availability,
		"device":       device,
	}
	c.publishJSON(c.discoveryTopic("binary_sensor", "activity"), activityCfg, true)
}

func (c *Client) publishPresetState() {
	name := activePreset(c.cfg.Load())
	c.publish(c.presetState(), name, true)
}

// SetActive publishes the current activity-monitor state. Cheap to call every
// frame — the underlying client coalesces / batches at the transport layer,
// but we still guard with a tiny cache to avoid topic churn.
func (c *Client) SetActive(active bool) {
	payload := "idle"
	if active {
		payload = "active"
	}
	c.mu.Lock()
	if c.lastActivity == payload {
		c.mu.Unlock()
		return
	}
	c.lastActivity = payload
	c.mu.Unlock()
	c.publish(c.activityState(), payload, true)
}

// handlePresetSet applies a preset by name. Unknown names are logged and
// ignored.
func (c *Client) handlePresetSet(_ paho.Client, msg paho.Message) {
	name := string(msg.Payload())
	cfg := *c.cfg.Load()
	for _, p := range cfg.Presets {
		if p.Name == name {
			cfg.DSPSettings = p.Settings
			if err := c.cfg.Apply(cfg); err != nil {
				slog.Warn("mqtt", "event", "apply preset failed", "preset", name, "err", err)
			}
			return
		}
	}
	slog.Warn("mqtt", "event", "unknown preset", "name", name)
}

// Close marks the client offline and disconnects from the broker.
func (c *Client) Close() {
	if !c.closed.CompareAndSwap(false, true) {
		return
	}
	tok := c.mq.Publish(c.availTopic(), 1, true, "offline")
	tok.WaitTimeout(500 * time.Millisecond)
	c.mq.Disconnect(500)
}

// publishJSON marshals v as JSON and publishes it; logs and swallows errors so
// MQTT failures never affect the audio path.
func (c *Client) publishJSON(topic string, v any, retain bool) {
	b, err := json.Marshal(v)
	if err != nil {
		slog.Warn("mqtt", "event", "marshal failed", "topic", topic, "err", err)
		return
	}
	c.publish(topic, string(b), retain)
}

func (c *Client) publish(topic, payload string, retain bool) {
	if !c.mq.IsConnectionOpen() {
		return
	}
	c.mq.Publish(topic, 1, retain, payload)
}

// presetNames returns just the names from cfg.Presets in their stored order.
func presetNames(cfg *config.Config) []string {
	out := make([]string, 0, len(cfg.Presets))
	for _, p := range cfg.Presets {
		out = append(out, p.Name)
	}
	return out
}

// activePreset returns the name of the preset whose Settings equal cfg's live
// DSPSettings, or "" if no preset matches.
func activePreset(cfg *config.Config) string {
	for _, p := range cfg.Presets {
		if reflect.DeepEqual(p.Settings, cfg.DSPSettings) {
			return p.Name
		}
	}
	return ""
}
