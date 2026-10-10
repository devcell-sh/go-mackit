package vmstate

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

type State struct {
	Name      string    `json:"name"`
	ImagePath string    `json:"image_path"`
	PID       int       `json:"pid"`
	Backend   string    `json:"backend"`
	StartedAt time.Time `json:"started_at"`
	SSHPort   uint16    `json:"ssh_port"`
	VNCPort   uint16    `json:"vnc_port"`
	Accel     string    `json:"accel"`
	OutputDir string    `json:"output_dir"`
	Forwards  []string  `json:"forwards,omitempty"`
}

func DefaultDir() string {
	home, _ := os.UserHomeDir()
	if home == "" {
		home = os.TempDir()
	}
	return filepath.Join(home, ".mackit", "run")
}

func Save(dir string, st *State) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, st.Name+".json"), data, 0o644)
}

func List(dir string) ([]*State, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var states []*State
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".json" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		var st State
		if err := json.Unmarshal(data, &st); err != nil {
			continue
		}
		states = append(states, &st)
	}
	return states, nil
}

func Remove(dir, name string) error {
	return os.Remove(filepath.Join(dir, name+".json"))
}

func FindByImage(dir, imagePath string) (*State, error) {
	states, err := List(dir)
	if err != nil {
		return nil, err
	}
	for _, st := range states {
		if st.ImagePath == imagePath {
			return st, nil
		}
	}
	return nil, nil
}

func IsAlive(pid int) bool {
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	err = p.Signal(syscall.Signal(0))
	return err == nil
}

func Uptime(st *State) string {
	d := time.Since(st.StartedAt)
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm%ds", int(d.Minutes()), int(d.Seconds())%60)
	default:
		return fmt.Sprintf("%dh%dm", int(d.Hours()), int(d.Minutes())%60)
	}
}
