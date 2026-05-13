package audio

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
)

// Node is a PipeWire audio node that can be used as a pw-cat target.
type Node struct {
	Name        string
	Class       string
	Description string
}

// ListNodes returns all PipeWire nodes that have a media.class containing "Audio".
func ListNodes() ([]Node, error) {
	out, err := exec.Command("pw-dump").Output()
	if err != nil {
		return nil, fmt.Errorf("pw-dump: %w", err)
	}

	var objects []struct {
		Type string `json:"type"`
		Info struct {
			Props map[string]any `json:"props"`
		} `json:"info"`
	}
	if err := json.Unmarshal(out, &objects); err != nil {
		return nil, fmt.Errorf("parse pw-dump output: %w", err)
	}

	var nodes []Node
	for _, obj := range objects {
		if obj.Type != "PipeWire:Interface:Node" {
			continue
		}
		cls, _ := obj.Info.Props["media.class"].(string)
		if !strings.HasPrefix(cls, "Audio/") && !strings.HasPrefix(cls, "Stream/") {
			continue
		}
		name := strProp(obj.Info.Props, "node.name")
		desc := strProp(obj.Info.Props, "node.description")
		nodes = append(nodes, Node{Name: name, Class: cls, Description: desc})

		// Sinks have a virtual monitor source accessible as <name>.monitor
		if cls == "Audio/Sink" {
			nodes = append(nodes, Node{
				Name:        name + ".monitor",
				Class:       "Audio/Source (monitor)",
				Description: desc + " (loopback)",
			})
		}
	}
	return nodes, nil
}

func strProp(props map[string]any, key string) string {
	v, _ := props[key].(string)
	return v
}
