package startup

import (
	"context"
	"fmt"
	"log"
	"strings"

	"github.com/iodesystems/ubuntu-router/internal/config"
	"github.com/iodesystems/ubuntu-router/internal/system"
	"github.com/iodesystems/ubuntu-router/internal/wireguard"
)

// Bootstrap applies essential router configuration on startup.
// This ensures the router works after reboot without manual intervention.
type Bootstrap struct {
	runner       system.CommandRunner
	fs           system.FileSystem
	config       *config.Config
	services     *system.ServiceManager
	wireguardP2P *wireguard.P2PManager
}

// New creates a new Bootstrap instance
func New(runner system.CommandRunner, fs system.FileSystem, cfg *config.Config, wireguardP2P *wireguard.P2PManager) *Bootstrap {
	return &Bootstrap{
		runner:       runner,
		fs:           fs,
		config:       cfg,
		services:     system.NewServiceManager(runner),
		wireguardP2P: wireguardP2P,
	}
}

// Run applies all essential startup configuration
func (b *Bootstrap) Run(ctx context.Context) error {
	log.Println("Running startup bootstrap...")

	var errors []string

	// 1. Enable IP forwarding (immediate + persistent)
	if err := b.enableIPForwarding(ctx); err != nil {
		errors = append(errors, fmt.Sprintf("IP forwarding: %v", err))
	}

	// 2. Load WireGuard kernel module (immediate + persistent)
	if err := b.loadWireGuardModule(ctx); err != nil {
		errors = append(errors, fmt.Sprintf("WireGuard module: %v", err))
	}

	// 3. Apply NAT masquerade rules
	if err := b.applyNATRules(ctx); err != nil {
		errors = append(errors, fmt.Sprintf("NAT rules: %v", err))
	}

	// 4. Configure LAN bridge (bring up, add IP)
	if err := b.configureLANBridge(ctx); err != nil {
		errors = append(errors, fmt.Sprintf("LAN bridge: %v", err))
	}

	// 5. Start WireGuard interfaces and enable auto-start
	if err := b.startWireGuardInterfaces(ctx); err != nil {
		errors = append(errors, fmt.Sprintf("WireGuard interfaces: %v", err))
	}

	if len(errors) > 0 {
		log.Printf("Startup bootstrap completed with warnings: %s", strings.Join(errors, "; "))
	} else {
		log.Println("Startup bootstrap completed successfully")
	}

	return nil
}

// enableIPForwarding enables IPv4 and IPv6 forwarding both immediately and persistently
func (b *Bootstrap) enableIPForwarding(ctx context.Context) error {
	log.Println("  Enabling IP forwarding...")

	// Write persistent sysctl config
	sysctlConfig := `# Ubuntu Router - IP forwarding
# Auto-generated - do not edit manually
net.ipv4.ip_forward=1
net.ipv6.conf.all.forwarding=1
`
	if err := b.fs.WriteFile("/etc/sysctl.d/99-ubuntu-router.conf", []byte(sysctlConfig), 0644); err != nil {
		log.Printf("  Warning: failed to write persistent sysctl config: %v", err)
		// Continue - we can still apply immediate settings
	}

	// Apply immediately via sysctl
	if _, err := b.runner.Run(ctx, "sysctl", "-w", "net.ipv4.ip_forward=1"); err != nil {
		return fmt.Errorf("failed to enable IPv4 forwarding: %w", err)
	}
	if _, err := b.runner.Run(ctx, "sysctl", "-w", "net.ipv6.conf.all.forwarding=1"); err != nil {
		log.Printf("  Warning: failed to enable IPv6 forwarding: %v", err)
		// Not critical - continue
	}

	log.Println("  IP forwarding enabled")
	return nil
}

// loadWireGuardModule loads the WireGuard kernel module and makes it persistent
func (b *Bootstrap) loadWireGuardModule(ctx context.Context) error {
	log.Println("  Loading WireGuard kernel module...")

	// Check if module is already loaded
	out, _ := b.runner.Run(ctx, "lsmod")
	if strings.Contains(string(out), "wireguard") {
		log.Println("  WireGuard module already loaded")
		return nil
	}

	// Check if module is available
	if _, err := b.runner.Run(ctx, "modinfo", "wireguard"); err != nil {
		log.Println("  WireGuard module not available (built into kernel on 5.6+)")
		return nil
	}

	// Write persistent module loading config
	if err := b.fs.WriteFile("/etc/modules-load.d/wireguard.conf", []byte("wireguard\n"), 0644); err != nil {
		log.Printf("  Warning: failed to write persistent module config: %v", err)
	}

	// Load module immediately
	if _, err := b.runner.Run(ctx, "modprobe", "wireguard"); err != nil {
		return fmt.Errorf("failed to load wireguard module: %w", err)
	}

	log.Println("  WireGuard module loaded")
	return nil
}

// configureLANBridge creates (if needed), brings up the LAN bridge and adds configured IP addresses
func (b *Bootstrap) configureLANBridge(ctx context.Context) error {
	if b.config.LANBridge == "" {
		log.Println("  Skipping LAN bridge (no bridge configured)")
		return nil
	}

	bridgeName := b.config.LANBridge
	log.Printf("  Configuring LAN bridge %s...", bridgeName)

	// Check if bridge exists, create if not
	if _, err := b.runner.Run(ctx, "ip", "link", "show", bridgeName); err != nil {
		log.Printf("  Bridge %s does not exist, creating...", bridgeName)
		if _, err := b.runner.Run(ctx, "ip", "link", "add", bridgeName, "type", "bridge"); err != nil {
			return fmt.Errorf("failed to create bridge %s: %w", bridgeName, err)
		}
		log.Printf("  Bridge %s created", bridgeName)
	}

	// Add LAN ports to the bridge if configured
	for _, port := range b.config.LANPorts {
		// Check if port is already in bridge
		out, _ := b.runner.Run(ctx, "ip", "link", "show", port)
		if strings.Contains(string(out), "master "+bridgeName) {
			continue
		}
		// Add port to bridge
		if _, err := b.runner.Run(ctx, "ip", "link", "set", port, "master", bridgeName); err != nil {
			log.Printf("  Warning: failed to add %s to bridge: %v", port, err)
		} else {
			log.Printf("  Added %s to bridge %s", port, bridgeName)
		}
	}

	// Bring up the bridge
	if _, err := b.runner.Run(ctx, "ip", "link", "set", bridgeName, "up"); err != nil {
		log.Printf("  Warning: failed to bring up bridge: %v", err)
	}

	// Add configured IP addresses
	for _, addr := range b.config.LANAddresses {
		// Check if address already exists
		out, _ := b.runner.Run(ctx, "ip", "-4", "addr", "show", bridgeName)
		if strings.Contains(string(out), strings.Split(addr, "/")[0]) {
			log.Printf("  Address %s already configured on %s", addr, bridgeName)
			continue
		}

		// Add address
		if _, err := b.runner.Run(ctx, "ip", "addr", "add", addr, "dev", bridgeName); err != nil {
			log.Printf("  Warning: failed to add address %s: %v", addr, err)
		} else {
			log.Printf("  Added address %s to %s", addr, bridgeName)
		}
	}

	log.Printf("  LAN bridge %s configured", bridgeName)
	return nil
}

// applyNATRules applies NAT masquerade rules for the WAN interface
func (b *Bootstrap) applyNATRules(ctx context.Context) error {
	if b.config.WANInterface == "" {
		log.Println("  Skipping NAT rules (no WAN interface configured)")
		return nil
	}

	log.Printf("  Applying NAT rules for WAN interface %s...", b.config.WANInterface)

	// Check if rule already exists
	out, _ := b.runner.Run(ctx, "iptables", "-t", "nat", "-L", "POSTROUTING", "-n", "-v")
	if strings.Contains(string(out), b.config.WANInterface) && strings.Contains(string(out), "MASQUERADE") {
		log.Println("  NAT masquerade rule already exists")
		return nil
	}

	// Add masquerade rule
	if _, err := b.runner.Run(ctx, "iptables", "-t", "nat", "-A", "POSTROUTING", "-o", b.config.WANInterface, "-j", "MASQUERADE"); err != nil {
		return fmt.Errorf("failed to add NAT masquerade rule: %w", err)
	}

	log.Printf("  NAT masquerade configured for %s", b.config.WANInterface)
	return nil
}

// startWireGuardInterfaces enables auto-start for configured WireGuard interfaces via systemd.
// We DON'T start the interfaces directly here to avoid racing with systemd's wg-quick@ services.
// If the systemd service is enabled, let it handle starting the interface on boot.
// If it's not running after boot, the health check will offer a fixer.
func (b *Bootstrap) startWireGuardInterfaces(ctx context.Context) error {
	// Main WireGuard interface (wg0)
	if b.config.WireGuard != nil && b.config.WireGuard.Enabled {
		iface := b.config.WireGuard.Interface
		if iface == "" {
			iface = "wg0"
		}

		configPath := b.config.WireGuard.ConfigPath
		if configPath == "" {
			configPath = fmt.Sprintf("/etc/wireguard/%s.conf", iface)
		}

		// Only proceed if config exists
		if _, err := b.fs.Stat(configPath); err == nil {
			// Enable auto-start via systemd - this is the primary mechanism
			// The wg-quick@.service will start the interface on boot
			if b.enableWireGuardAutoStart(ctx, iface) {
				log.Printf("  WireGuard %s: systemd auto-start enabled", iface)
			}

			// Check if the interface actually exists (not just if service is "active")
			// wg-quick@.service is Type=oneshot, so "active (exited)" doesn't mean interface exists
			serviceName := fmt.Sprintf("wg-quick@%s", iface)
			if _, err := b.runner.Run(ctx, "ip", "link", "show", iface); err != nil {
				// Interface doesn't exist - restart the service
				log.Printf("  WireGuard %s interface not found, restarting service...", iface)
				if err := b.services.Restart(ctx, serviceName); err != nil {
					log.Printf("  Warning: failed to restart %s: %v", serviceName, err)
				} else {
					log.Printf("  WireGuard %s started", iface)
				}
			} else {
				log.Printf("  WireGuard %s already running", iface)
			}
		}
	}

	// P2P/Site-to-Site tunnels
	if b.config.WireGuardP2P != nil && b.wireguardP2P != nil {
		for i := range b.config.WireGuardP2P.Tunnels {
			tunnel := &b.config.WireGuardP2P.Tunnels[i]
			if tunnel.Enabled && tunnel.Interface != "" {
				configPath := fmt.Sprintf("/etc/wireguard/%s.conf", tunnel.Interface)

				// Write config file if it doesn't exist
				if _, err := b.fs.Stat(configPath); err != nil {
					log.Printf("  WireGuard P2P %s: config file missing, generating...", tunnel.Interface)
					if err := b.wireguardP2P.WriteConfig(tunnel); err != nil {
						log.Printf("  Warning: failed to write config for %s: %v", tunnel.Interface, err)
						continue
					}
					log.Printf("  WireGuard P2P %s: config file created", tunnel.Interface)
				}

				if b.enableWireGuardAutoStart(ctx, tunnel.Interface) {
					log.Printf("  WireGuard P2P %s: systemd auto-start enabled", tunnel.Interface)
				}

				// Check if the interface actually exists
				serviceName := fmt.Sprintf("wg-quick@%s", tunnel.Interface)
				if _, err := b.runner.Run(ctx, "ip", "link", "show", tunnel.Interface); err != nil {
					log.Printf("  WireGuard P2P %s interface not found, restarting service...", tunnel.Interface)
					if err := b.services.Restart(ctx, serviceName); err != nil {
						log.Printf("  Warning: failed to restart %s: %v", serviceName, err)
					} else {
						log.Printf("  WireGuard P2P %s started", tunnel.Interface)
					}
				} else {
					log.Printf("  WireGuard P2P %s already running", tunnel.Interface)
				}
			}
		}
	}

	return nil
}

// enableWireGuardAutoStart enables systemd auto-start for a WireGuard interface
// Returns true if it was newly enabled, false if already enabled
func (b *Bootstrap) enableWireGuardAutoStart(ctx context.Context, iface string) bool {
	serviceName := fmt.Sprintf("wg-quick@%s", iface)

	// Check if already enabled
	if b.services.IsEnabled(ctx, serviceName) {
		return false
	}

	// Enable the service
	if err := b.services.Enable(ctx, serviceName); err != nil {
		log.Printf("  Warning: failed to enable auto-start for %s: %v", iface, err)
		return false
	}
	return true
}
