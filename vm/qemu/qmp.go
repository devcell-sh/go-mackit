package qemu

import (
	"encoding/json"
	"fmt"
	"net"
	"time"
)

type qmpResponse struct {
	Return json.RawMessage `json:"return"`
	Error  *struct {
		Class string `json:"class"`
		Desc  string `json:"desc"`
	} `json:"error"`
}

func qmpHandshake(sock string) (net.Conn, error) {
	conn, err := net.DialTimeout("unix", sock, 5*time.Second)
	if err != nil {
		return nil, fmt.Errorf("connecting to QMP: %w", err)
	}
	conn.SetDeadline(time.Now().Add(5 * time.Second))

	buf := make([]byte, 4096)
	n, err := conn.Read(buf)
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("reading QMP greeting: %w", err)
	}
	_ = n

	cmd := `{"execute": "qmp_capabilities"}`
	if _, err := conn.Write([]byte(cmd)); err != nil {
		conn.Close()
		return nil, fmt.Errorf("sending qmp_capabilities: %w", err)
	}

	n, err = conn.Read(buf)
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("reading qmp_capabilities response: %w", err)
	}

	var resp qmpResponse
	if err := json.Unmarshal(buf[:n], &resp); err != nil {
		conn.Close()
		return nil, fmt.Errorf("parsing qmp_capabilities response: %w", err)
	}
	if resp.Error != nil {
		conn.Close()
		return nil, fmt.Errorf("qmp_capabilities error: %s", resp.Error.Desc)
	}

	conn.SetDeadline(time.Time{})
	return conn, nil
}

func qmpCommand(sock string, cmd string) (json.RawMessage, error) {
	conn, err := qmpHandshake(sock)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	conn.SetDeadline(time.Now().Add(10 * time.Second))

	if _, err := conn.Write([]byte(cmd)); err != nil {
		return nil, fmt.Errorf("sending QMP command: %w", err)
	}

	buf := make([]byte, 65536)
	n, err := conn.Read(buf)
	if err != nil {
		return nil, fmt.Errorf("reading QMP response: %w", err)
	}

	var resp qmpResponse
	if err := json.Unmarshal(buf[:n], &resp); err != nil {
		return nil, fmt.Errorf("parsing QMP response: %w", err)
	}
	if resp.Error != nil {
		return nil, fmt.Errorf("QMP error: %s: %s", resp.Error.Class, resp.Error.Desc)
	}
	return resp.Return, nil
}

func QMPScreendump(sock, path string) error {
	cmd := fmt.Sprintf(`{"execute": "screendump", "arguments": {"filename": %q}}`, path)
	_, err := qmpCommand(sock, cmd)
	return err
}

func QMPQuit(sock string) error {
	_, err := qmpCommand(sock, `{"execute": "quit"}`)
	return err
}

func QMPSendKey(sock string, qcode string) error {
	cmd := fmt.Sprintf(`{"execute": "send-key", "arguments": {"keys": [{"type": "qcode", "data": %q}]}}`, qcode)
	_, err := qmpCommand(sock, cmd)
	return err
}

func QMPBlockReadBytes(sock string) (int64, error) {
	raw, err := qmpCommand(sock, `{"execute": "query-blockstats"}`)
	if err != nil {
		return 0, err
	}

	var stats []struct {
		Stats struct {
			RdBytes int64 `json:"rd_bytes"`
		} `json:"stats"`
	}
	if err := json.Unmarshal(raw, &stats); err != nil {
		return 0, err
	}

	var total int64
	for _, s := range stats {
		total += s.Stats.RdBytes
	}
	return total, nil
}
