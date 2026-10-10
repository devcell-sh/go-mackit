package config

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/devcell-sh/go-mackit/build/buildopts"
)

type CommandEntry struct {
	Cmd            string   `yaml:"cmd"`
	Timeout        duration `yaml:"timeout"`
	Retries        int      `yaml:"retries"`
	ValidExitCodes []int    `yaml:"valid-exit-codes"`
}

type duration time.Duration

func (d *duration) UnmarshalYAML(value *yaml.Node) error {
	var s string
	if err := value.Decode(&s); err != nil {
		return err
	}
	parsed, err := time.ParseDuration(s)
	if err != nil {
		return fmt.Errorf("invalid duration %q: %w", s, err)
	}
	*d = duration(parsed)
	return nil
}

func (d duration) Duration() time.Duration {
	return time.Duration(d)
}

type FileEntry struct {
	Src  string `yaml:"src"`
	Dest string `yaml:"dest"`
}

type PortsConfig struct {
	SSH     int      `yaml:"ssh"`
	VNC     int      `yaml:"vnc"`
	Forward []string `yaml:"forward"`
}

type OpenCoreConfig struct {
	Version string `yaml:"version"`
}

type Config struct {
	From     string         `yaml:"from"`
	Hostname string         `yaml:"hostname"`
	Recovery bool           `yaml:"recovery"`
	Files    []FileEntry    `yaml:"files"`
	Ports    *PortsConfig   `yaml:"ports"`
	OpenCore *OpenCoreConfig `yaml:"opencore"`
	Commands commandsField  `yaml:"-"`
}

type commandsField struct {
	Phases map[string][]CommandEntry
}

func (c *Config) UnmarshalYAML(value *yaml.Node) error {
	type plain struct {
		From     string          `yaml:"from"`
		Hostname string          `yaml:"hostname"`
		Recovery bool            `yaml:"recovery"`
		Files    []FileEntry     `yaml:"files"`
		Ports    *PortsConfig    `yaml:"ports"`
		OpenCore *OpenCoreConfig `yaml:"opencore"`
	}

	var p plain
	if err := value.Decode(&p); err != nil {
		return err
	}
	c.From = p.From
	c.Hostname = p.Hostname
	c.Recovery = p.Recovery
	c.Files = p.Files
	c.Ports = p.Ports
	c.OpenCore = p.OpenCore
	c.Commands.Phases = make(map[string][]CommandEntry)

	cmdNode := findKey(value, "commands")
	if cmdNode == nil {
		return nil
	}

	if cmdNode.Kind == yaml.SequenceNode {
		entries, err := parseCommandList(cmdNode)
		if err != nil {
			return fmt.Errorf("commands: %w", err)
		}
		c.Commands.Phases["boot"] = entries
		return nil
	}

	if cmdNode.Kind == yaml.MappingNode {
		for i := 0; i+1 < len(cmdNode.Content); i += 2 {
			phase := cmdNode.Content[i].Value
			entries, err := parseCommandList(cmdNode.Content[i+1])
			if err != nil {
				return fmt.Errorf("commands.%s: %w", phase, err)
			}
			c.Commands.Phases[phase] = entries
		}
		return nil
	}

	return fmt.Errorf("commands: expected list or map, got %v", cmdNode.Kind)
}

func findKey(mapping *yaml.Node, key string) *yaml.Node {
	if mapping.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		if mapping.Content[i].Value == key {
			return mapping.Content[i+1]
		}
	}
	return nil
}

func parseCommandList(node *yaml.Node) ([]CommandEntry, error) {
	var result []CommandEntry
	for _, item := range node.Content {
		switch item.Kind {
		case yaml.ScalarNode:
			var s string
			if err := item.Decode(&s); err != nil {
				return nil, err
			}
			result = append(result, CommandEntry{Cmd: s})
		case yaml.MappingNode:
			var entry CommandEntry
			if err := item.Decode(&entry); err != nil {
				return nil, err
			}
			result = append(result, entry)
		default:
			return nil, fmt.Errorf("unexpected node type %v in command list", item.Kind)
		}
	}
	return result, nil
}

// hostnameRe validates RFC 1123 hostnames.
var hostnameRe = regexp.MustCompile(`^[a-zA-Z0-9]([a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?$`)

var defaultNames = []string{"mackit.yaml", "mackit.yml"}

func Discover(dir string) (string, error) {
	for _, name := range defaultNames {
		p := filepath.Join(dir, name)
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
	}
	return "", fmt.Errorf("no mackit.yaml or mackit.yml found in %s", dir)
}

func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading config: %w", err)
	}
	return Parse(data)
}

func Parse(data []byte) (*Config, error) {
	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parsing config: %w", err)
	}
	return &cfg, nil
}

func (c *Config) Validate() error {
	if c.Recovery {
		for phase := range c.Commands.Phases {
			if phase != "boot" {
				return fmt.Errorf("recovery mode only supports boot phase commands, got %s", phase)
			}
		}
	}
	validPhases := map[string]bool{"firstboot": true, "boot": true}
	for phase := range c.Commands.Phases {
		if !validPhases[phase] {
			return fmt.Errorf("unknown command phase %q (want firstboot or boot)", phase)
		}
	}
	if c.Hostname != "" {
		if !hostnameRe.MatchString(c.Hostname) || len(c.Hostname) > 63 {
			return fmt.Errorf("hostname %q invalid: 1-63 chars, letters, digits, or hyphens, must start and end with alphanumeric", c.Hostname)
		}
	}
	if c.Ports != nil {
		for _, f := range c.Ports.Forward {
			if _, _, err := ParseForward(f); err != nil {
				return fmt.Errorf("ports.%w", err)
			}
		}
		seen := map[int]string{}
		for name, p := range map[string]int{
			"ssh": c.Ports.SSH, "vnc": c.Ports.VNC,
		} {
			if p < 0 || p > 65535 {
				return fmt.Errorf("ports.%s: %d out of range (1-65535)", name, p)
			}
			if p == 0 {
				continue
			}
			if other, dup := seen[p]; dup {
				return fmt.Errorf("ports.%s and ports.%s both set to %d", other, name, p)
			}
			seen[p] = name
		}
	}
	return nil
}

func ParseForward(spec string) (host, guest int, err error) {
	h, g, convErr := buildopts.ParseForward(spec)
	return int(h), int(g), convErr
}

func (c *Config) LoadDirectoryHooks(dir string) error {
	hooksDir := filepath.Join(dir, "hooks")
	info, err := os.Stat(hooksDir)
	if err != nil || !info.IsDir() {
		return nil
	}

	phases := []string{"firstboot", "boot"}
	for _, phase := range phases {
		phaseDir := filepath.Join(hooksDir, phase)
		entries, err := os.ReadDir(phaseDir)
		if err != nil {
			continue
		}

		var scripts []string
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			scripts = append(scripts, e.Name())
		}
		sort.Strings(scripts)

		for _, name := range scripts {
			fullPath := filepath.Join(phaseDir, name)
			data, err := os.ReadFile(fullPath)
			if err != nil {
				return fmt.Errorf("reading hook %s: %w", fullPath, err)
			}
			cmd := strings.TrimSpace(string(data))
			if cmd == "" {
				continue
			}
			if c.Commands.Phases == nil {
				c.Commands.Phases = make(map[string][]CommandEntry)
			}
			c.Commands.Phases[phase] = append(c.Commands.Phases[phase], CommandEntry{Cmd: cmd})
		}
	}
	return nil
}
