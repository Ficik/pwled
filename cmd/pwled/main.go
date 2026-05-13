package main

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"text/tabwriter"

	"github.com/spf13/pflag"

	"pwled/internal/audio"
	"pwled/internal/config"
	"pwled/internal/dsp"
	"pwled/internal/httpui"
	"pwled/internal/mqtt"
	"pwled/internal/wled"
)

func main() {
	list      := pflag.BoolP("list", "l", false, "list available PipeWire audio nodes and exit")
	target    := pflag.StringP("target", "t", "", "PipeWire node name or ID to capture")
	dest      := pflag.StringP("dest", "d", "255.255.255.255", "UDP destination address")
	port      := pflag.IntP("port", "p", 11988, "UDP destination port")
	rate      := pflag.IntP("rate", "r", 44100, "audio sample rate (44100 or 48000)")
	http      := pflag.StringP("http", "H", ":8080", "HTTP monitor address (empty to disable)")
	cfgPath   := pflag.String("config", "", "path to TOML config (default: $XDG_CONFIG_HOME/pwled/config.toml)")

	mqttBroker  := pflag.String("mqtt-broker", "", "MQTT broker URL (e.g. tcp://192.168.1.10:1883); empty disables MQTT")
	mqttUser    := pflag.String("mqtt-user", "", "MQTT username (optional)")
	mqttPass    := pflag.String("mqtt-pass", "", "MQTT password (optional)")
	mqttPrefix  := pflag.String("mqtt-discovery-prefix", "homeassistant", "Home Assistant discovery topic prefix")
	mqttNodeID  := pflag.String("mqtt-node-id", "pwled", "node identifier used in MQTT topics and unique_id")

	pflag.Parse()

	if *list {
		nodes, err := audio.ListNodes()
		if err != nil {
			slog.Error("listing nodes failed", "err", err)
			os.Exit(1)
		}
		tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(tw, "NAME\tCLASS\tDESCRIPTION")
		for _, n := range nodes {
			fmt.Fprintf(tw, "%s\t%s\t%s\n", n.Name, n.Class, n.Description)
		}
		tw.Flush()
		return
	}

	resolvedCfgPath := *cfgPath
	if resolvedCfgPath == "" {
		p, err := config.DefaultPath()
		if err != nil {
			slog.Error("resolve default config path", "err", err)
			os.Exit(1)
		}
		resolvedCfgPath = p
	}
	initialCfg, err := config.LoadOrCreate(resolvedCfgPath)
	if err != nil {
		slog.Error("load config", "path", resolvedCfgPath, "err", err)
		os.Exit(1)
	}
	slog.Info("config", "path", resolvedCfgPath)
	store := config.NewStore(initialCfg, func(c config.Config) error {
		return config.Save(resolvedCfgPath, c)
	})

	// 1024-sample hop ≈ 23 ms at 44100 Hz — one WLED packet per hop.
	const hopSize = 1024

	var ui *httpui.Server
	if *http != "" {
		ui = httpui.New(store)
		if _, err := ui.Listen(*http); err != nil {
			slog.Error("http server failed", "err", err)
			os.Exit(1)
		}
	}

	src, err := audio.NewPwCat(*target, *rate, hopSize)
	if err != nil {
		slog.Error("audio source failed", "err", err)
		os.Exit(1)
	}
	defer src.Close()

	proc := dsp.New(*rate, hopSize, store)

	sender, err := wled.NewSender(*dest, *port, store)
	if err != nil {
		slog.Error("sender failed", "err", err)
		os.Exit(1)
	}
	defer sender.Close()

	var mqClient *mqtt.Client
	mqClient, err = mqtt.New(mqtt.Options{
		Broker:          *mqttBroker,
		User:            *mqttUser,
		Pass:            *mqttPass,
		DiscoveryPrefix: *mqttPrefix,
		NodeID:          *mqttNodeID,
	}, store)
	switch {
	case err == nil:
		defer mqClient.Close()
	case errors.Is(err, mqtt.ErrDisabled):
		// MQTT not configured — silent.
	default:
		slog.Warn("mqtt disabled", "err", err)
		mqClient = nil
	}

	slog.Info("running",
		"target", *target,
		"dest", *dest,
		"port", *port,
		"rate", *rate,
	)

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)

	for {
		select {
		case frame, ok := <-src.Frames():
			if !ok {
				if err := src.Err(); err != nil {
					slog.Error("audio source closed", "err", err)
				}
				return
			}
			a := proc.Process(frame)
			sender.SetActive(a.Active)
			if mqClient != nil {
				mqClient.SetActive(a.Active)
			}
			if err := sender.Send(analysisToPacket(a)); err != nil {
				slog.Warn("send error", "err", err)
			}
			if ui != nil {
				ui.Publish(httpui.Frame{
					Raw:       a.SampleRaw,
					Smooth:    a.SampleSmth,
					Peak:      a.SamplePeak,
					Bands:     a.FFTResult,
					Magnitude: a.Magnitude,
					MajorPeak: a.MajorPeak,
					Active:    a.Active,
				})
			}
		case <-sig:
			slog.Info("shutting down")
			return
		}
	}
}

func analysisToPacket(a dsp.Analysis) wled.Packet {
	p := wled.Packet{
		SampleRaw:    a.SampleRaw,
		SampleSmth:   a.SampleSmth,
		FFTResult:    a.FFTResult,
		FFTMagnitude: a.Magnitude,
		FFTMajorPeak: a.MajorPeak,
	}
	if a.SamplePeak {
		p.SamplePeak = 1
	}
	return p
}
