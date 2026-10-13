// Package vzproto defines the JSONL protocol between the mackit Go client
// and the mackit-vz Swift helper binary.
//
// The helper reads Request objects (one JSON per line) from stdin and writes
// Response objects and Event objects to stdout. A Response always carries the
// same ID as the Request it answers. Events have no ID and may arrive at any
// time between a request and its response (e.g., progress updates during
// install).
package vzproto

import (
	"encoding/json"
	"fmt"
)

// Command names sent from Go to the helper.
const (
	CmdInstall    = "install"
	CmdBoot       = "boot"
	CmdScreenshot = "screenshot"
	CmdSendKeys   = "send_keys"
	CmdClick      = "click"
	CmdType       = "type"
	CmdStop       = "stop"
)

// Event names sent from the helper to Go (unsolicited).
const (
	EventProgress = "progress"
	EventBooted   = "booted"
	EventStopped  = "stopped"
	EventLog      = "log"
)

// Request is a command from Go to the helper.
type Request struct {
	Cmd string `json:"cmd"`
	ID  int    `json:"id"`

	// install
	IPSW       string `json:"ipsw,omitempty"`
	Disk       string `json:"disk,omitempty"`
	DiskSizeGB int    `json:"disk_size_gb,omitempty"`
	CPUs       uint   `json:"cpus,omitempty"`
	MemoryGB   uint64 `json:"memory_gb,omitempty"`
	OutDir     string `json:"out_dir,omitempty"`

	// guest provisioning (macOS 27+)
	GuestUser     string `json:"guest_user,omitempty"`
	GuestPassword string `json:"guest_password,omitempty"`
	EnableSSH     bool   `json:"enable_ssh,omitempty"`

	// send_keys
	Keys []string `json:"keys,omitempty"`

	// click
	X int `json:"x,omitempty"`
	Y int `json:"y,omitempty"`

	// type
	Text string `json:"text,omitempty"`
}

// Message is any line from the helper's stdout. It is either a Response
// (has ID > 0) or an Event (has Event set). Decode with UnmarshalMessage.
type Message struct {
	// Response fields
	ID    int    `json:"id,omitempty"`
	OK    bool   `json:"ok,omitempty"`
	Error string `json:"error,omitempty"`

	// Screenshot response
	Width     int    `json:"width,omitempty"`
	Height    int    `json:"height,omitempty"`
	PNGBase64 string `json:"png_base64,omitempty"`

	// Boot/VNC response
	VNCPort    int    `json:"vnc_port,omitempty"`
	MACAddress string `json:"mac_address,omitempty"`

	// Event fields
	Event    string `json:"event,omitempty"`
	Progress int    `json:"progress,omitempty"`
	UOM      string `json:"uom,omitempty"`
	Msg      string `json:"msg,omitempty"`
	Level    string `json:"level,omitempty"`
	ExitCode int    `json:"exit_code,omitempty"`
}

// IsEvent returns true if this message is an unsolicited event.
func (m *Message) IsEvent() bool { return m.Event != "" }

// IsResponse returns true if this message is a response to a request.
func (m *Message) IsResponse() bool { return m.ID > 0 }

// Err returns a non-nil error if the response indicates failure.
func (m *Message) Err() error {
	if m.Error != "" {
		return fmt.Errorf("helper: %s", m.Error)
	}
	return nil
}

// MarshalRequest encodes a request as a JSON line (no trailing newline).
func MarshalRequest(r *Request) ([]byte, error) {
	return json.Marshal(r)
}

// MarshalMessage encodes a message as a JSON line (no trailing newline).
func MarshalMessage(m *Message) ([]byte, error) {
	return json.Marshal(m)
}

// UnmarshalMessage decodes a JSON line into a Message.
func UnmarshalMessage(data []byte) (*Message, error) {
	var m Message
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, err
	}
	return &m, nil
}

// UnmarshalRequest decodes a JSON line into a Request.
func UnmarshalRequest(data []byte) (*Request, error) {
	var r Request
	if err := json.Unmarshal(data, &r); err != nil {
		return nil, err
	}
	return &r, nil
}
