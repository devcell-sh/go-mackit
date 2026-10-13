package vz

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

// GuestIP discovers the IP address of a VZ guest from its MAC address.
// Checks the macOS DHCP leases file first, falls back to ARP table.
// Retries for up to timeout because the guest needs time to DHCP.
func GuestIP(mac string, timeout time.Duration) (string, error) {
	if mac == "" {
		return "", fmt.Errorf("no MAC address")
	}
	normalizedMAC := strings.ToLower(mac)
	arpMAC := normalizeMACForARP(mac)
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if ip := lookupDHCPLease(normalizedMAC); ip != "" {
			return ip, nil
		}
		if ip := lookupARP(arpMAC); ip != "" {
			return ip, nil
		}
		time.Sleep(2 * time.Second)
	}
	return "", fmt.Errorf("guest IP not found for MAC %s after %s", mac, timeout)
}

// lookupDHCPLease reads /var/db/dhcpd_leases and matches the MAC address.
// The file format is a sequence of blocks like:
//
//	{
//	  ip_address=192.168.64.2
//	  hw_address=1,aa:bb:cc:dd:ee:ff
//	  ...
//	}
func lookupDHCPLease(mac string) string {
	data, err := os.ReadFile("/var/db/dhcpd_leases")
	if err != nil {
		return ""
	}
	var currentIP string
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "ip_address=") {
			currentIP = strings.TrimPrefix(line, "ip_address=")
		}
		if strings.HasPrefix(line, "hw_address=") {
			hwAddr := strings.TrimPrefix(line, "hw_address=")
			// Format: "1,aa:bb:cc:dd:ee:ff" — type prefix, then MAC
			if idx := strings.Index(hwAddr, ","); idx >= 0 {
				hwAddr = hwAddr[idx+1:]
			}
			if strings.ToLower(hwAddr) == mac && currentIP != "" {
				return currentIP
			}
		}
		if line == "}" {
			currentIP = ""
		}
	}
	return ""
}

// lookupARP runs `arp -an` and finds the IP for the given MAC.
func lookupARP(mac string) string {
	out, err := exec.Command("arp", "-an").Output()
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(out), "\n") {
		if strings.Contains(strings.ToLower(line), mac) {
			start := strings.Index(line, "(")
			end := strings.Index(line, ")")
			if start >= 0 && end > start {
				return line[start+1 : end]
			}
		}
	}
	return ""
}

// normalizeMACForARP strips leading zeros per octet to match macOS arp output
// (e.g., "0a:01:02" -> "a:1:2").
func normalizeMACForARP(mac string) string {
	parts := strings.Split(strings.ToLower(mac), ":")
	for i, p := range parts {
		for len(p) > 1 && p[0] == '0' {
			p = p[1:]
		}
		parts[i] = p
	}
	return strings.Join(parts, ":")
}
