package discovery

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// TargetConfig represents the minimal target information needed for discovery.
type TargetConfig struct {
	ID        string `json:"id"`
	Host      string `json:"host"`
	Port      int    `json:"port"`
	SSHAlias  string `json:"ssh_alias,omitempty"`
	User      string `json:"user,omitempty"`
	Platform  string `json:"platform,omitempty"`
	Enabled   bool   `json:"enabled"`
}

// HostKeyEntry represents a parsed line from known_hosts or ssh-keyscan output.
type HostKeyEntry struct {
	Patterns    []string `json:"patterns"`
	KeyType     string   `json:"key_type"`
	KeyB64      string   `json:"key_b64"`
	RawKey      []byte   `json:"-"`
	Fingerprint string   `json:"fingerprint"`
	Marker      string   `json:"marker,omitempty"`
}

// KeyInfo holds the public metadata for a key.
type KeyInfo struct {
	KeyType     string `json:"key_type"`
	Fingerprint string `json:"fingerprint"`
}

// TargetIdentityResult represents the inspection status of a target's SSH identity.
type TargetIdentityResult struct {
	Status       string    `json:"status"` // TRUSTED, UNTRUSTED, CHANGED, UNAVAILABLE
	TargetID     string    `json:"target_id"`
	Endpoint     string    `json:"endpoint"`
	TrustedKeys  []KeyInfo `json:"trusted_keys"`
	PresentedKey *KeyInfo  `json:"presented_key"`
}

// DiscoveryResult represents the result of network target discovery.
type DiscoveryResult struct {
	Status       string `json:"status"` // IDENTITY_MATCH, TARGET_NOT_FOUND, AMBIGUOUS_TARGET_IDENTITY, ERROR
	NewHost      string `json:"new_host,omitempty"`
	NewPort      int    `json:"new_port,omitempty"`
	Method       string `json:"method,omitempty"`
	DurationMs   int    `json:"duration_ms"`
	Fingerprint  string `json:"fingerprint,omitempty"`
	CandidateKey string `json:"candidate_key,omitempty"`
	Error        string `json:"error,omitempty"`
}

// ComputeSHA256Fingerprint generates the standard OpenSSH SHA256 base64 fingerprint without padding.
func ComputeSHA256Fingerprint(rawKey []byte) string {
	digest := sha256.Sum256(rawKey)
	return "SHA256:" + base64.RawStdEncoding.EncodeToString(digest[:])
}

// ParseKnownHostsLine parses a known_hosts formatted line.
func ParseKnownHostsLine(line string) *HostKeyEntry {
	line = strings.TrimSpace(line)
	if line == "" || strings.HasPrefix(line, "#") {
		return nil
	}

	parts := strings.Fields(line)
	if len(parts) < 3 {
		return nil
	}

	idx := 0
	marker := ""
	if strings.HasPrefix(parts[0], "@") {
		marker = parts[0]
		idx++
		if len(parts) < idx+3 {
			return nil
		}
	}

	hostPattern := parts[idx]
	keyType := parts[idx+1]
	keyB64 := parts[idx+2]

	rawKey, err := base64.StdEncoding.DecodeString(keyB64)
	if err != nil {
		return nil
	}

	patterns := strings.Split(hostPattern, ",")
	for i := range patterns {
		patterns[i] = strings.TrimSpace(patterns[i])
	}

	return &HostKeyEntry{
		Patterns:    patterns,
		KeyType:     keyType,
		KeyB64:      keyB64,
		RawKey:      rawKey,
		Fingerprint: ComputeSHA256Fingerprint(rawKey),
		Marker:      marker,
	}
}

// TargetDiscovery manages host-key pinning and target discovery.
type TargetDiscovery struct {
	KnownHostsPath string
	mu             sync.Mutex
	khMu           sync.Mutex
}

// NewTargetDiscovery initializes a TargetDiscovery with the specified known_hosts path.
func NewTargetDiscovery(knownHostsPath string) *TargetDiscovery {
	if knownHostsPath == "" {
		home, err := os.UserHomeDir()
		if err == nil {
			defaultPath := filepath.Join(home, ".ssh", "known_hosts")
			knownHostsPath = defaultPath
		} else {
			knownHostsPath = "/home/mcp-gateway/.ssh/known_hosts"
		}
	}
	return &TargetDiscovery{
		KnownHostsPath: knownHostsPath,
	}
}

func validateKnownHostsName(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" || strings.ContainsAny(value, " \t\r\n,") || strings.HasPrefix(value, "@") {
		return "", errors.New("invalid Target ID or SSH alias for known_hosts")
	}
	return value, nil
}

// GetCanonicalKeys returns the pinned host keys matching targetID or alias from known_hosts.
func (d *TargetDiscovery) GetCanonicalKeys(targetID, alias string) ([]*HostKeyEntry, error) {
	d.khMu.Lock()
	defer d.khMu.Unlock()

	if _, err := os.Stat(d.KnownHostsPath); os.IsNotExist(err) {
		return nil, nil
	}

	f, err := os.Open(d.KnownHostsPath)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	searchNames := map[string]bool{targetID: true}
	if alias != "" {
		searchNames[alias] = true
	}

	var matches []*HostKeyEntry
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		entry := ParseKnownHostsLine(scanner.Text())
		if entry == nil {
			continue
		}
		for _, p := range entry.Patterns {
			if searchNames[p] {
				matches = append(matches, entry)
				break
			}
		}
	}

	return matches, scanner.Err()
}

func preferredKey(keys []*HostKeyEntry) *HostKeyEntry {
	if len(keys) == 0 {
		return nil
	}
	priority := map[string]int{
		"ssh-ed25519":         0,
		"ecdsa-sha2-nistp256": 1,
		"ecdsa-sha2-nistp384": 2,
		"ecdsa-sha2-nistp521": 3,
		"ssh-rsa":             4,
	}
	sorted := make([]*HostKeyEntry, len(keys))
	copy(sorted, keys)
	sort.Slice(sorted, func(i, j int) bool {
		pI, okI := priority[sorted[i].KeyType]
		if !okI {
			pI = 99
		}
		pJ, okJ := priority[sorted[j].KeyType]
		if !okJ {
			pJ = 99
		}
		return pI < pJ
	})
	return sorted[0]
}

// GetRemoteHostKeys queries remote SSH host keys using ssh-keyscan.
func (d *TargetDiscovery) GetRemoteHostKeys(host string, port int, timeout time.Duration) ([]*HostKeyEntry, error) {
	if port <= 0 {
		port = 22
	}
	if timeout <= 0 {
		timeout = 2 * time.Second
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout+1*time.Second)
	defer cancel()

	cmd := exec.CommandContext(
		ctx,
		"ssh-keyscan",
		"-p", strconv.Itoa(port),
		"-t", "ed25519,ecdsa,rsa",
		"-T", strconv.Itoa(int(timeout.Seconds())),
		host,
	)

	out, err := cmd.Output()
	if err != nil && len(out) == 0 {
		return nil, err
	}

	var keys []*HostKeyEntry
	scanner := bufio.NewScanner(strings.NewReader(string(out)))
	for scanner.Scan() {
		entry := ParseKnownHostsLine(scanner.Text())
		if entry != nil {
			keys = append(keys, entry)
		}
	}
	return keys, nil
}

// InspectTargetIdentity inspects the target's pinned known_hosts entries and presented keys.
func (d *TargetDiscovery) InspectTargetIdentity(target TargetConfig) (*TargetIdentityResult, error) {
	targetID, err := validateKnownHostsName(target.ID)
	if err != nil {
		return nil, err
	}
	var alias string
	if target.SSHAlias != "" {
		alias, err = validateKnownHostsName(target.SSHAlias)
		if err != nil {
			return nil, err
		}
	}

	host := strings.TrimSpace(target.Host)
	port := target.Port
	if port <= 0 {
		port = 22
	}
	if host == "" || port > 65535 {
		return nil, errors.New("target endpoint is incomplete or invalid")
	}

	trusted, err := d.GetCanonicalKeys(targetID, alias)
	if err != nil {
		return nil, err
	}

	presentedKeys, _ := d.GetRemoteHostKeys(host, port, 2*time.Second)

	trustedFPMap := make(map[string]bool)
	for _, k := range trusted {
		trustedFPMap[k.Fingerprint] = true
	}

	var matched *HostKeyEntry
	for _, pk := range presentedKeys {
		if trustedFPMap[pk.Fingerprint] {
			matched = pk
			break
		}
	}

	presented := matched
	if presented == nil {
		presented = preferredKey(presentedKeys)
	}

	var trustedPub []KeyInfo
	for _, k := range trusted {
		trustedPub = append(trustedPub, KeyInfo{
			KeyType:     k.KeyType,
			Fingerprint: k.Fingerprint,
		})
	}

	var presentedPub *KeyInfo
	if presented != nil {
		presentedPub = &KeyInfo{
			KeyType:     presented.KeyType,
			Fingerprint: presented.Fingerprint,
		}
	}

	status := "UNAVAILABLE"
	if len(presentedKeys) == 0 {
		status = "UNAVAILABLE"
	} else if len(trusted) == 0 {
		status = "UNTRUSTED"
	} else if matched != nil {
		status = "TRUSTED"
	} else {
		status = "CHANGED"
	}

	return &TargetIdentityResult{
		Status:       status,
		TargetID:     targetID,
		Endpoint:     fmt.Sprintf("%s:%d", host, port),
		TrustedKeys:  trustedPub,
		PresentedKey: presentedPub,
	}, nil
}

// RewriteKnownHosts safely replaces a target's pin in known_hosts atomically.
func (d *TargetDiscovery) RewriteKnownHosts(targetID, alias, newLine string) error {
	d.khMu.Lock()
	defer d.khMu.Unlock()

	targetID, err := validateKnownHostsName(targetID)
	if err != nil {
		return err
	}
	names := map[string]bool{targetID: true}
	if alias != "" {
		aliasClean, err := validateKnownHostsName(alias)
		if err != nil {
			return err
		}
		names[aliasClean] = true
	}

	dir := filepath.Dir(d.KnownHostsPath)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}

	var kept []string
	if _, err := os.Stat(d.KnownHostsPath); err == nil {
		f, err := os.Open(d.KnownHostsPath)
		if err != nil {
			return err
		}
		scanner := bufio.NewScanner(f)
		for scanner.Scan() {
			text := scanner.Text()
			parsed := ParseKnownHostsLine(text)
			drop := false
			if parsed != nil {
				for _, p := range parsed.Patterns {
					if names[p] {
						drop = true
						break
					}
				}
			}
			if !drop {
				kept = append(kept, text)
			}
		}
		f.Close()
	}

	if newLine != "" {
		kept = append(kept, strings.TrimRight(newLine, "\n"))
	}

	tmpFile, err := os.CreateTemp(dir, ".known_hosts.*")
	if err != nil {
		return err
	}
	tmpName := tmpFile.Name()
	defer os.Remove(tmpName)

	if err := os.Chmod(tmpName, 0600); err != nil {
		tmpFile.Close()
		return err
	}

	writer := bufio.NewWriter(tmpFile)
	for _, l := range kept {
		if _, err := writer.WriteString(l + "\n"); err != nil {
			tmpFile.Close()
			return err
		}
	}
	if err := writer.Flush(); err != nil {
		tmpFile.Close()
		return err
	}
	if err := tmpFile.Sync(); err != nil {
		tmpFile.Close()
		return err
	}
	if err := tmpFile.Close(); err != nil {
		return err
	}

	return os.Rename(tmpName, d.KnownHostsPath)
}

// TrustPresentedKey pins the reviewed host fingerprint for target.
func (d *TargetDiscovery) TrustPresentedKey(target TargetConfig, expectedFP string, replace bool) (*TargetIdentityResult, error) {
	if expectedFP == "" || !strings.HasPrefix(expectedFP, "SHA256:") {
		return nil, errors.New("a reviewed SHA256 fingerprint is required")
	}

	targetID, err := validateKnownHostsName(target.ID)
	if err != nil {
		return nil, err
	}
	alias := target.SSHAlias
	port := target.Port
	if port <= 0 {
		port = 22
	}

	presentedKeys, err := d.GetRemoteHostKeys(target.Host, port, 2*time.Second)
	if err != nil || len(presentedKeys) == 0 {
		return nil, errors.New("the Target is unreachable or returned no host keys")
	}

	var presented *HostKeyEntry
	for _, k := range presentedKeys {
		if k.Fingerprint == expectedFP {
			presented = k
			break
		}
	}
	if presented == nil {
		return nil, errors.New("the Target no longer presents the reviewed host fingerprint")
	}

	trusted, _ := d.GetCanonicalKeys(targetID, alias)
	for _, k := range trusted {
		if k.Fingerprint == expectedFP {
			return d.InspectTargetIdentity(target)
		}
	}

	if len(trusted) > 0 && !replace {
		return nil, errors.New("a different host fingerprint is already pinned; explicit replacement is required")
	}

	newLine := fmt.Sprintf("%s %s %s", targetID, presented.KeyType, presented.KeyB64)
	if err := d.RewriteKnownHosts(targetID, alias, newLine); err != nil {
		return nil, err
	}

	res, err := d.InspectTargetIdentity(target)
	if err != nil {
		return nil, err
	}
	if res.Status != "TRUSTED" {
		return nil, errors.New("the reviewed key was pinned, but the Target no longer presents it")
	}
	return res, nil
}

// RemoveTrustedKey deletes the pinned fingerprint for target from known_hosts.
func (d *TargetDiscovery) RemoveTrustedKey(target TargetConfig) (*TargetIdentityResult, error) {
	targetID := target.ID
	alias := target.SSHAlias
	if err := d.RewriteKnownHosts(targetID, alias, ""); err != nil {
		return nil, err
	}
	return d.InspectTargetIdentity(target)
}

// ProbePort checks if an IP:port is reachable via TCP with a timeout.
func ProbePort(ip string, port int, timeout time.Duration) bool {
	if timeout <= 0 {
		timeout = 350 * time.Millisecond
	}
	addr := net.JoinHostPort(ip, strconv.Itoa(port))
	conn, err := net.DialTimeout("tcp", addr, timeout)
	if err != nil {
		return false
	}
	conn.Close()
	return true
}

// GetKernelNeighbors reads kernel ARP table /proc/net/arp or parses ip neigh.
func GetKernelNeighbors() []string {
	var ips []string
	f, err := os.Open("/proc/net/arp")
	if err == nil {
		defer f.Close()
		scanner := bufio.NewScanner(f)
		isHeader := true
		for scanner.Scan() {
			if isHeader {
				isHeader = false
				continue
			}
			parts := strings.Fields(scanner.Text())
			if len(parts) >= 4 {
				ip := parts[0]
				flags := parts[2]
				if flags != "0x0" && !strings.HasPrefix(ip, "0.") && !strings.HasPrefix(ip, "127.") {
					ips = append(ips, ip)
				}
			}
		}
	}
	sort.Strings(ips)
	return ips
}
