package audio

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"math"
	"os/exec"
	"strconv"
	"sync"
	"time"
)

// PwCat captures audio from a PipeWire node by spawning pw-cat.
// It implements Source.
type PwCat struct {
	cmd        *exec.Cmd
	frames     chan Frame
	mu         sync.Mutex
	err        error
	hopSize    int
	sampleRate int
}

type portEntry struct {
	id      int
	channel string
}

// NewPwCat starts pw-cat in record mode using node.autoconnect=false and a
// unique node name, then manually links the target's output ports to our input
// port via pw-link after polling pw-dump for both nodes.
// target is a PipeWire node name or numeric ID (empty = PipeWire default).
func NewPwCat(target string, sampleRate, hopSize int) (*PwCat, error) {
	uid := fmt.Sprintf("pwled-mon-%d", time.Now().UnixNano())
	uidJSON, _ := json.Marshal(uid)

	args := []string{
		"--record",
		"--format", "f32",
		"--rate", fmt.Sprintf("%d", sampleRate),
		"--channels", "1",
		"--properties", fmt.Sprintf(`{"node.name":%s,"node.autoconnect":false}`, uidJSON),
	}
	if target != "" {
		args = append(args, "--target", target)
	}
	args = append(args, "-")

	cmd := exec.Command("pw-cat", args...)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("pw-cat stdout pipe: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, fmt.Errorf("pw-cat stderr pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("pw-cat start: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	outIDs, inIDs, linkErr := waitAndMatchPorts(ctx, target, uid)
	if linkErr != nil {
		cmd.Process.Kill()
		cmd.Wait() //nolint:errcheck
		return nil, fmt.Errorf("port linking: %w", linkErr)
	}

	for i := range outIDs {
		lc := exec.Command("pw-link", strconv.Itoa(outIDs[i]), strconv.Itoa(inIDs[i]))
		if out, lerr := lc.CombinedOutput(); lerr != nil {
			slog.Warn("pw-link", "out", outIDs[i], "in", inIDs[i], "err", lerr, "output", string(out))
		}
	}

	s := &PwCat{
		cmd:        cmd,
		frames:     make(chan Frame, 8),
		hopSize:    hopSize,
		sampleRate: sampleRate,
	}
	go s.logStderr(stderr)
	go s.readLoop(stdout)
	return s, nil
}

func (s *PwCat) readLoop(r io.Reader) {
	defer close(s.frames)

	raw := make([]byte, s.hopSize*4) // 4 bytes per float32
	samples := make([]float32, s.hopSize)

	for {
		if _, err := io.ReadFull(r, raw); err != nil {
			if err != io.EOF && err != io.ErrUnexpectedEOF {
				s.mu.Lock()
				s.err = fmt.Errorf("pw-cat read: %w", err)
				s.mu.Unlock()
			}
			return
		}
		for i := range samples {
			samples[i] = math.Float32frombits(binary.LittleEndian.Uint32(raw[i*4:]))
		}
		frame := Frame{
			Samples:    make([]float32, s.hopSize),
			SampleRate: s.sampleRate,
		}
		copy(frame.Samples, samples)
		select {
		case s.frames <- frame:
		default:
			// drop frame if the consumer is behind
		}
	}
}

func (s *PwCat) logStderr(r io.Reader) {
	buf := make([]byte, 512)
	for {
		n, err := r.Read(buf)
		if n > 0 {
			slog.Warn("pw-cat", "msg", string(buf[:n]))
		}
		if err != nil {
			return
		}
	}
}

func (s *PwCat) Frames() <-chan Frame { return s.frames }

func (s *PwCat) Err() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.err
}

func (s *PwCat) Close() error {
	if s.cmd.Process != nil {
		return s.cmd.Process.Kill()
	}
	return nil
}

// waitAndMatchPorts polls pw-dump until the target node's output ports and our
// pw-cat node's input ports are both visible, then pairs them by audio.channel.
func waitAndMatchPorts(ctx context.Context, targetName, ourName string) (outIDs, inIDs []int, err error) {
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return nil, nil, ctx.Err()
		default:
		}

		out, dumpErr := exec.Command("pw-dump").Output()
		if dumpErr != nil {
			time.Sleep(100 * time.Millisecond)
			continue
		}

		var objects []struct {
			ID   int             `json:"id"`
			Type string          `json:"type"`
			Info json.RawMessage `json:"info"`
		}
		if json.Unmarshal(out, &objects) != nil {
			time.Sleep(100 * time.Millisecond)
			continue
		}

		nodeIDs := make(map[string]int)
		for _, obj := range objects {
			if obj.Type != "PipeWire:Interface:Node" {
				continue
			}
			var info struct {
				Props map[string]any `json:"props"`
			}
			if json.Unmarshal(obj.Info, &info) != nil {
				continue
			}
			if name, ok := info.Props["node.name"].(string); ok {
				nodeIDs[name] = obj.ID
			}
		}

		targetID, targetOK := nodeIDs[targetName]
		ourID, ourOK := nodeIDs[ourName]
		if !targetOK || !ourOK {
			time.Sleep(100 * time.Millisecond)
			continue
		}

		var targetOuts, ourIns []portEntry
		for _, obj := range objects {
			if obj.Type != "PipeWire:Interface:Port" {
				continue
			}
			var info struct {
				Direction string         `json:"direction"`
				Props     map[string]any `json:"props"`
			}
			if json.Unmarshal(obj.Info, &info) != nil {
				continue
			}
			nodeIDf, ok := info.Props["node.id"].(float64)
			if !ok {
				continue
			}
			nodeID := int(nodeIDf)
			ch, _ := info.Props["audio.channel"].(string)

			switch {
			case nodeID == targetID && info.Direction == "output":
				targetOuts = append(targetOuts, portEntry{obj.ID, ch})
			case nodeID == ourID && info.Direction == "input":
				ourIns = append(ourIns, portEntry{obj.ID, ch})
			}
		}

		if len(targetOuts) == 0 || len(ourIns) == 0 {
			time.Sleep(100 * time.Millisecond)
			continue
		}

		pairs := matchPorts(targetOuts, ourIns)
		if len(pairs) == 0 {
			return nil, nil, fmt.Errorf("no matching ports between %q and %q", targetName, ourName)
		}
		for _, p := range pairs {
			outIDs = append(outIDs, p[0])
			inIDs = append(inIDs, p[1])
		}
		return outIDs, inIDs, nil
	}
	return nil, nil, fmt.Errorf("timeout: target %q not found or has no output ports", targetName)
}

// matchPorts pairs output ports to input ports by audio.channel name,
// falling back to positional order when channel names are missing or unmatched.
func matchPorts(outs, ins []portEntry) [][2]int {
	inByChannel := make(map[string]int, len(ins))
	for _, p := range ins {
		if p.channel != "" {
			inByChannel[p.channel] = p.id
		}
	}

	used := make(map[int]bool)
	var result [][2]int
	for _, o := range outs {
		if inID, ok := inByChannel[o.channel]; ok && !used[inID] {
			result = append(result, [2]int{o.id, inID})
			used[inID] = true
		}
	}
	if len(result) > 0 {
		return result
	}

	n := len(outs)
	if len(ins) < n {
		n = len(ins)
	}
	for i := range n {
		result = append(result, [2]int{outs[i].id, ins[i].id})
	}
	return result
}
