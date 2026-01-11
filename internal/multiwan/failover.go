package multiwan

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/iodesystems/ubuntu-router/internal/config"
	"github.com/iodesystems/ubuntu-router/internal/logger"
	"github.com/iodesystems/ubuntu-router/internal/system"
)

// WANStatus represents the current status of a WAN interface
type WANStatus struct {
	Name          string    `json:"name"`
	Interface     string    `json:"interface"`
	Priority      int       `json:"priority"`
	Enabled       bool      `json:"enabled"`
	Active        bool      `json:"active"`        // Currently the active WAN
	Up            bool      `json:"up"`            // Link is up
	Healthy       bool      `json:"healthy"`       // Passes health checks
	IPAddress     string    `json:"ip_address"`
	Gateway       string    `json:"gateway"`
	LastCheck     time.Time `json:"last_check"`
	LastHealthy   time.Time `json:"last_healthy"`
	FailureCount  int       `json:"failure_count"`
	ChecksRun     int       `json:"checks_run"`
	ChecksPassed  int       `json:"checks_passed"`
}

// FailoverStatus represents the overall multi-WAN status
type FailoverStatus struct {
	Enabled     bool        `json:"enabled"`
	Mode        string      `json:"mode"`
	ActiveWAN   string      `json:"active_wan"`   // Interface name of active WAN
	WANs        []WANStatus `json:"wans"`
	LastSwitch  time.Time   `json:"last_switch"`
	SwitchCount int         `json:"switch_count"`
}

// Manager handles multi-WAN failover
type Manager struct {
	mu           sync.RWMutex
	runner       system.CommandRunner
	fs           system.FileSystem
	config       *config.MultiWANConfig
	status       map[string]*WANStatus // keyed by interface name
	activeWAN    string
	lastSwitch   time.Time
	switchCount  int
	stopChan     chan struct{}
	running      bool
}

// New creates a new multi-WAN manager
func New(runner system.CommandRunner, fs system.FileSystem) *Manager {
	return &Manager{
		runner: runner,
		fs:     fs,
		status: make(map[string]*WANStatus),
	}
}

// Configure sets the multi-WAN configuration
func (m *Manager) Configure(cfg *config.MultiWANConfig) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.config = cfg
	m.status = make(map[string]*WANStatus)

	if cfg == nil || !cfg.Enabled {
		return
	}

	// Initialize status for each WAN
	for _, wan := range cfg.WANs {
		if !wan.Enabled {
			continue
		}
		m.status[wan.Interface] = &WANStatus{
			Name:      wan.Name,
			Interface: wan.Interface,
			Priority:  wan.Priority,
			Enabled:   wan.Enabled,
			Healthy:   true, // Assume healthy until proven otherwise
		}
	}
}

// Start begins the failover monitoring loop
func (m *Manager) Start(ctx context.Context) error {
	m.mu.Lock()
	if m.running {
		m.mu.Unlock()
		return nil
	}
	if m.config == nil || !m.config.Enabled {
		m.mu.Unlock()
		return nil
	}
	m.running = true
	m.stopChan = make(chan struct{})
	m.mu.Unlock()

	logger.Info("Multi-WAN failover starting with %d WANs", len(m.config.WANs))

	// Initial check and activation
	m.checkAllWANs(ctx)
	m.selectActiveWAN(ctx)

	// Start monitoring loop
	go m.monitorLoop(ctx)

	return nil
}

// Stop stops the failover monitoring
func (m *Manager) Stop() {
	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.running {
		return
	}

	close(m.stopChan)
	m.running = false
	logger.Info("Multi-WAN failover stopped")
}

// GetStatus returns the current failover status
func (m *Manager) GetStatus() *FailoverStatus {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.config == nil {
		return &FailoverStatus{Enabled: false}
	}

	status := &FailoverStatus{
		Enabled:     m.config.Enabled,
		Mode:        m.config.Mode,
		ActiveWAN:   m.activeWAN,
		LastSwitch:  m.lastSwitch,
		SwitchCount: m.switchCount,
	}

	// Get sorted WAN list
	var wans []WANStatus
	for _, ws := range m.status {
		wans = append(wans, *ws)
	}
	sort.Slice(wans, func(i, j int) bool {
		return wans[i].Priority < wans[j].Priority
	})
	status.WANs = wans

	return status
}

// monitorLoop continuously checks WAN health
func (m *Manager) monitorLoop(ctx context.Context) {
	// Get check interval from first enabled WAN, default 10s
	interval := 10 * time.Second
	m.mu.RLock()
	for _, wan := range m.config.WANs {
		if wan.Enabled && wan.HealthCheckInterval > 0 {
			interval = time.Duration(wan.HealthCheckInterval) * time.Second
			break
		}
	}
	m.mu.RUnlock()

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-m.stopChan:
			return
		case <-ticker.C:
			m.checkAllWANs(ctx)
			m.selectActiveWAN(ctx)
		}
	}
}

// checkAllWANs runs health checks on all configured WANs
func (m *Manager) checkAllWANs(ctx context.Context) {
	m.mu.RLock()
	cfg := m.config
	m.mu.RUnlock()

	if cfg == nil {
		return
	}

	for _, wan := range cfg.WANs {
		if !wan.Enabled {
			continue
		}
		m.checkWAN(ctx, &wan)
	}
}

// checkWAN checks the health of a single WAN
func (m *Manager) checkWAN(ctx context.Context, wan *config.WANConfig) {
	m.mu.Lock()
	status, ok := m.status[wan.Interface]
	if !ok {
		status = &WANStatus{
			Name:      wan.Name,
			Interface: wan.Interface,
			Priority:  wan.Priority,
			Enabled:   wan.Enabled,
		}
		m.status[wan.Interface] = status
	}
	m.mu.Unlock()

	// Check link state
	linkUp := m.checkLinkState(ctx, wan.Interface)

	// Get IP and gateway
	ip, gateway := m.getInterfaceInfo(ctx, wan.Interface)

	// Run health check (ping)
	healthy := false
	if linkUp && gateway != "" {
		healthy = m.runHealthCheck(ctx, wan, gateway)
	}

	// Update status
	m.mu.Lock()
	status.Up = linkUp
	status.IPAddress = ip
	status.Gateway = gateway
	status.LastCheck = time.Now()
	status.ChecksRun++

	retries := wan.HealthCheckRetries
	if retries <= 0 {
		retries = 3
	}

	if healthy {
		status.Healthy = true
		status.LastHealthy = time.Now()
		status.FailureCount = 0
		status.ChecksPassed++
	} else {
		status.FailureCount++
		if status.FailureCount >= retries {
			status.Healthy = false
		}
	}
	m.mu.Unlock()
}

// checkLinkState checks if the interface link is up
func (m *Manager) checkLinkState(ctx context.Context, iface string) bool {
	out, err := m.runner.Run(ctx, "ip", "link", "show", iface)
	if err != nil {
		return false
	}
	return strings.Contains(string(out), "state UP")
}

// getInterfaceInfo gets IP and gateway for an interface
func (m *Manager) getInterfaceInfo(ctx context.Context, iface string) (ip, gateway string) {
	// Get IP
	out, err := m.runner.Run(ctx, "ip", "-4", "addr", "show", iface)
	if err == nil {
		re := regexp.MustCompile(`inet (\d+\.\d+\.\d+\.\d+)`)
		if matches := re.FindStringSubmatch(string(out)); len(matches) >= 2 {
			ip = matches[1]
		}
	}

	// Get gateway - check routing table for this interface
	out, err = m.runner.Run(ctx, "ip", "route", "show", "dev", iface)
	if err == nil {
		// Look for default route or extract gateway from route
		lines := strings.Split(string(out), "\n")
		for _, line := range lines {
			if strings.HasPrefix(line, "default via ") {
				parts := strings.Fields(line)
				if len(parts) >= 3 {
					gateway = parts[2]
					break
				}
			}
		}
		// If no default route, try to find any gateway
		if gateway == "" {
			re := regexp.MustCompile(`via (\d+\.\d+\.\d+\.\d+)`)
			if matches := re.FindStringSubmatch(string(out)); len(matches) >= 2 {
				gateway = matches[1]
			}
		}
		// For DHCP, gateway might be in .1 of the subnet
		if gateway == "" && ip != "" {
			parts := strings.Split(ip, ".")
			if len(parts) == 4 {
				gateway = fmt.Sprintf("%s.%s.%s.1", parts[0], parts[1], parts[2])
			}
		}
	}

	return ip, gateway
}

// runHealthCheck pings targets to verify connectivity
func (m *Manager) runHealthCheck(ctx context.Context, wan *config.WANConfig, gateway string) bool {
	targets := wan.HealthCheckTargets
	if len(targets) == 0 {
		// Default targets: gateway and 8.8.8.8
		targets = []string{gateway, "8.8.8.8"}
	}

	timeout := wan.HealthCheckTimeout
	if timeout <= 0 {
		timeout = 5
	}

	// Must be able to reach at least one target
	for _, target := range targets {
		if target == "" {
			continue
		}
		// Ping through specific interface
		out, err := m.runner.Run(ctx, "ping", "-I", wan.Interface, "-c", "1", "-W", fmt.Sprintf("%d", timeout), target)
		if err == nil && strings.Contains(string(out), "1 received") {
			return true
		}
	}

	return false
}

// selectActiveWAN selects the best available WAN and activates it
func (m *Manager) selectActiveWAN(ctx context.Context) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.config == nil || !m.config.Enabled {
		return
	}

	// Sort WANs by priority
	var healthyWANs []*WANStatus
	for _, ws := range m.status {
		if ws.Enabled && ws.Healthy && ws.Up {
			healthyWANs = append(healthyWANs, ws)
		}
	}

	sort.Slice(healthyWANs, func(i, j int) bool {
		return healthyWANs[i].Priority < healthyWANs[j].Priority
	})

	if len(healthyWANs) == 0 {
		logger.Warn("Multi-WAN: No healthy WANs available!")
		return
	}

	bestWAN := healthyWANs[0]

	// Check if we need to switch
	if m.activeWAN == bestWAN.Interface {
		return
	}

	// Check failback delay - don't switch back to primary too quickly
	if m.activeWAN != "" && bestWAN.Priority < m.status[m.activeWAN].Priority {
		failbackDelay := m.config.FailbackDelay
		if failbackDelay <= 0 {
			failbackDelay = 60
		}
		if time.Since(m.lastSwitch) < time.Duration(failbackDelay)*time.Second {
			logger.Info("Multi-WAN: Waiting for failback delay before switching to %s", bestWAN.Interface)
			return
		}
	}

	// Perform the switch
	oldWAN := m.activeWAN
	m.activeWAN = bestWAN.Interface

	// Update active status
	for _, ws := range m.status {
		ws.Active = (ws.Interface == m.activeWAN)
	}

	m.lastSwitch = time.Now()
	m.switchCount++

	logger.Info("Multi-WAN: Switching from %s to %s (priority %d)", oldWAN, bestWAN.Interface, bestWAN.Priority)

	// Apply the route change (unlock during route change to avoid deadlock)
	m.mu.Unlock()
	m.applyRouteChange(ctx, bestWAN.Interface, bestWAN.Gateway)
	m.mu.Lock()
}

// applyRouteChange updates the default route to use the new WAN
func (m *Manager) applyRouteChange(ctx context.Context, iface, gateway string) {
	if gateway == "" {
		logger.Error("Multi-WAN: Cannot apply route change - no gateway for %s", iface)
		return
	}

	// Remove existing default routes
	m.runner.Run(ctx, "ip", "route", "del", "default")

	// Add new default route
	out, err := m.runner.Run(ctx, "ip", "route", "add", "default", "via", gateway, "dev", iface)
	if err != nil {
		logger.Error("Multi-WAN: Failed to add default route: %s", string(out))
		return
	}

	logger.Info("Multi-WAN: Default route updated to %s via %s", iface, gateway)

	// Update NAT rules if needed
	m.updateNATRules(ctx, iface)
}

// updateNATRules updates iptables NAT rules for the new WAN
func (m *Manager) updateNATRules(ctx context.Context, iface string) {
	// Remove old MASQUERADE rules for all WAN interfaces
	m.mu.RLock()
	for wanIface := range m.status {
		m.runner.Run(ctx, "iptables", "-t", "nat", "-D", "POSTROUTING", "-o", wanIface, "-j", "MASQUERADE")
	}
	m.mu.RUnlock()

	// Add MASQUERADE for new active WAN
	out, err := m.runner.Run(ctx, "iptables", "-t", "nat", "-A", "POSTROUTING", "-o", iface, "-j", "MASQUERADE")
	if err != nil {
		logger.Error("Multi-WAN: Failed to add NAT rule: %s", string(out))
	}
}

// ForceSwitch forces a switch to a specific WAN (for manual override)
func (m *Manager) ForceSwitch(ctx context.Context, iface string) error {
	m.mu.Lock()
	status, ok := m.status[iface]
	if !ok {
		m.mu.Unlock()
		return fmt.Errorf("unknown WAN interface: %s", iface)
	}
	if !status.Enabled {
		m.mu.Unlock()
		return fmt.Errorf("WAN interface %s is not enabled", iface)
	}

	oldWAN := m.activeWAN
	m.activeWAN = iface

	for _, ws := range m.status {
		ws.Active = (ws.Interface == m.activeWAN)
	}

	m.lastSwitch = time.Now()
	m.switchCount++
	gateway := status.Gateway
	m.mu.Unlock()

	logger.Info("Multi-WAN: Manual switch from %s to %s", oldWAN, iface)

	m.applyRouteChange(ctx, iface, gateway)
	return nil
}

// GetActiveWAN returns the currently active WAN interface
func (m *Manager) GetActiveWAN() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.activeWAN
}

// IsEnabled returns whether multi-WAN is enabled
func (m *Manager) IsEnabled() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.config != nil && m.config.Enabled
}
