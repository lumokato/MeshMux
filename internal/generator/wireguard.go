package generator

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
)

type wgConfig struct {
	Name       string
	PrivateKey string
	Address    []string
	DNS        []string
	MTU        string
	Peers      []wgPeer
}

type wgPeer struct {
	PublicKey    string
	PresharedKey string
	Endpoint     string
	AllowedIPs   []string
	Keepalive    string
}

type WireGuardSummary struct {
	ConfigCount   int `json:"configCount"`
	ReadableCount int `json:"readableCount"`
	PeerCount     int `json:"peerCount"`
}

func SummarizeWireGuard(paths []string) WireGuardSummary {
	var summary WireGuardSummary
	for _, path := range paths {
		if strings.TrimSpace(path) == "" {
			continue
		}
		summary.ConfigCount++
		cfg, err := readWireGuard(path)
		if err != nil {
			continue
		}
		summary.ReadableCount++
		summary.PeerCount += len(cfg.Peers)
	}
	return summary
}

func readWireGuard(path string) (wgConfig, error) {
	file, err := os.Open(path)
	if err != nil {
		return wgConfig{}, err
	}
	defer file.Close()

	cfg := wgConfig{Name: "wg-" + strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))}
	var section string
	var currentPeer *wgPeer

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			section = strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(line, "["), "]"))
			if section == "Peer" {
				cfg.Peers = append(cfg.Peers, wgPeer{})
				currentPeer = &cfg.Peers[len(cfg.Peers)-1]
			}
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)

		switch section {
		case "Interface":
			switch strings.ToLower(key) {
			case "privatekey":
				cfg.PrivateKey = value
			case "address":
				cfg.Address = splitList(value)
			case "dns":
				cfg.DNS = splitList(value)
			case "mtu":
				cfg.MTU = value
			}
		case "Peer":
			if currentPeer == nil {
				continue
			}
			switch strings.ToLower(key) {
			case "publickey":
				currentPeer.PublicKey = value
			case "presharedkey":
				currentPeer.PresharedKey = value
			case "endpoint":
				currentPeer.Endpoint = value
			case "allowedips":
				currentPeer.AllowedIPs = splitList(value)
			case "persistentkeepalive":
				currentPeer.Keepalive = value
			}
		}
	}
	return cfg, scanner.Err()
}

func splitList(value string) []string {
	var out []string
	for _, part := range strings.Split(value, ",") {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

func splitEndpoint(endpoint string) (string, string) {
	endpoint = strings.TrimSpace(endpoint)
	if strings.HasPrefix(endpoint, "[") {
		if end := strings.LastIndex(endpoint, "]:"); end > 0 {
			return endpoint[1:end], endpoint[end+2:]
		}
	}
	host, port, ok := strings.Cut(endpoint, ":")
	if ok {
		return host, port
	}
	return endpoint, "51820"
}

func wgAllowedRoutes(configs []wgConfig) []string {
	seen := map[string]bool{}
	var routes []string
	for _, cfg := range configs {
		for _, peer := range cfg.Peers {
			for _, cidr := range peer.AllowedIPs {
				if cidr == "0.0.0.0/0" || cidr == "::/0" || seen[cidr] {
					continue
				}
				seen[cidr] = true
				routes = append(routes, cidr)
			}
		}
	}
	return routes
}
