package middleware

import (
	"bufio"
	"encoding/hex"
	"fmt"
	"log"
	"net"
	"os"
	"strings"

	"github.com/gofiber/fiber/v2"
)

// DefaultBindPort is the default port to bind to
const DefaultBindPort = "3000"

// getDefaultRouteInterfaces reads /proc/net/route to find interfaces with default routes (0.0.0.0)
// Returns a list of interface names that have a default route
func getDefaultRouteInterfaces() []string {
	file, err := os.Open("/proc/net/route")
	if err != nil {
		log.Printf("Security: Warning - could not read routing table: %v", err)
		return nil
	}
	defer file.Close()

	var interfaces []string
	seen := make(map[string]bool)
	scanner := bufio.NewScanner(file)

	// Skip header line
	scanner.Scan()

	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 2 {
			continue
		}

		ifaceName := fields[0]
		destination := fields[1]

		// Check if this is a default route (destination = 00000000)
		if destination == "00000000" {
			if !seen[ifaceName] {
				seen[ifaceName] = true
				interfaces = append(interfaces, ifaceName)
				log.Printf("Security: Found default route on interface %s", ifaceName)
			}
		}
	}

	return interfaces
}

// getInterfaceSubnet returns the CIDR subnet for a network interface
func getInterfaceSubnet(ifaceName string) (string, error) {
	iface, err := net.InterfaceByName(ifaceName)
	if err != nil {
		return "", err
	}

	addrs, err := iface.Addrs()
	if err != nil {
		return "", err
	}

	for _, addr := range addrs {
		ipNet, ok := addr.(*net.IPNet)
		if !ok {
			continue
		}

		// Only process IPv4
		ip := ipNet.IP.To4()
		if ip == nil {
			continue
		}

		// Calculate network address
		network := &net.IPNet{
			IP:   ip.Mask(ipNet.Mask),
			Mask: ipNet.Mask,
		}
		return network.String(), nil
	}

	return "", fmt.Errorf("no IPv4 address found for interface %s", ifaceName)
}

// getSubnetsFromDefaultRoutes finds all subnets from interfaces that have a default route
// This allows only local network traffic, blocking internet-originated requests
func getSubnetsFromDefaultRoutes() []string {
	var subnets []string
	seen := make(map[string]bool)

	// Always include localhost
	subnets = append(subnets, "127.0.0.0/8")
	seen["127.0.0.0/8"] = true

	// Get interfaces with default routes
	defaultRouteIfaces := getDefaultRouteInterfaces()
	if len(defaultRouteIfaces) == 0 {
		log.Printf("Security: No default route interfaces found, only localhost will be allowed")
		return subnets
	}

	// Get subnet for each interface with a default route
	for _, ifaceName := range defaultRouteIfaces {
		subnet, err := getInterfaceSubnet(ifaceName)
		if err != nil {
			log.Printf("Security: Warning - could not get subnet for interface %s: %v", ifaceName, err)
			continue
		}

		if !seen[subnet] {
			seen[subnet] = true
			subnets = append(subnets, subnet)
			log.Printf("Security: Auto-allowing subnet %s from default-route interface %s", subnet, ifaceName)
		}
	}

	return subnets
}

// parseHexIP converts a hex-encoded IP from /proc/net/route to net.IP
// The format in /proc/net/route is little-endian hex
func parseHexIP(hexIP string) net.IP {
	if len(hexIP) != 8 {
		return nil
	}

	bytes, err := hex.DecodeString(hexIP)
	if err != nil {
		return nil
	}

	// /proc/net/route stores IPs in little-endian format
	return net.IPv4(bytes[3], bytes[2], bytes[1], bytes[0])
}

// GetBindAddr returns the bind address
// By default binds to 0.0.0.0 (all interfaces)
// Can be overridden with BIND_INTERFACE env var for a specific interface
func GetBindAddr() (string, error) {
	port := os.Getenv("BIND_PORT")
	if port == "" {
		port = DefaultBindPort
	}

	// Check if a specific interface is requested
	ifaceName := os.Getenv("BIND_INTERFACE")
	if ifaceName != "" {
		ip, err := getInterfaceIP(ifaceName)
		if err != nil {
			availableIfaces := ListAvailableInterfaces()
			return "", fmt.Errorf("%w. Available interfaces: %v", err, availableIfaces)
		}
		bindAddr := fmt.Sprintf("%s:%s", ip, port)
		log.Printf("Security: Binding to interface %s (%s)", ifaceName, bindAddr)
		return bindAddr, nil
	}

	// Default: bind to all interfaces
	bindAddr := fmt.Sprintf("0.0.0.0:%s", port)
	log.Printf("Security: Binding to all interfaces (%s)", bindAddr)
	return bindAddr, nil
}

// getInterfaceIP returns the first IPv4 address of a network interface
func getInterfaceIP(ifaceName string) (string, error) {
	iface, err := net.InterfaceByName(ifaceName)
	if err != nil {
		return "", fmt.Errorf("interface %s not found: %w", ifaceName, err)
	}

	addrs, err := iface.Addrs()
	if err != nil {
		return "", fmt.Errorf("failed to get addresses for interface %s: %w", ifaceName, err)
	}

	for _, addr := range addrs {
		ipNet, ok := addr.(*net.IPNet)
		if !ok {
			continue
		}

		ip := ipNet.IP.To4()
		if ip != nil {
			return ip.String(), nil
		}
	}

	return "", fmt.Errorf("no IPv4 address found for interface %s", ifaceName)
}

// ListAvailableInterfaces returns a list of available network interfaces for logging
func ListAvailableInterfaces() []string {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil
	}

	var names []string
	for _, iface := range ifaces {
		names = append(names, iface.Name)
	}
	return names
}

// GetAllowedNetworks returns the allowed networks
// If ALLOWED_NETWORKS env var is set, use that
// Otherwise, auto-detect subnets from interfaces with default routes (0.0.0.0)
// This ensures only local network traffic is allowed, blocking internet-originated requests
func GetAllowedNetworks() string {
	allowedNetworks := os.Getenv("ALLOWED_NETWORKS")
	if allowedNetworks != "" {
		return allowedNetworks
	}

	// Auto-detect subnets from interfaces with default routes
	subnets := getSubnetsFromDefaultRoutes()
	return strings.Join(subnets, ",")
}

// ParseCIDRs parses a comma-separated list of CIDR strings into net.IPNet slices
func ParseCIDRs(cidrList string) ([]*net.IPNet, error) {
	var networks []*net.IPNet
	cidrs := strings.Split(cidrList, ",")

	for _, cidr := range cidrs {
		cidr = strings.TrimSpace(cidr)
		if cidr == "" {
			continue
		}

		_, network, err := net.ParseCIDR(cidr)
		if err != nil {
			return nil, err
		}
		networks = append(networks, network)
	}

	return networks, nil
}

// isIPAllowed checks if an IP address is within the allowed networks
func isIPAllowed(ip net.IP, allowedNetworks []*net.IPNet) bool {
	for _, network := range allowedNetworks {
		if network.Contains(ip) {
			return true
		}
	}
	return false
}

// extractIP extracts the IP address from a remote address string (ip:port or just ip)
func extractIP(remoteAddr string) net.IP {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		host = remoteAddr
	}
	return net.ParseIP(host)
}

// IPFilterMiddleware returns a Fiber middleware that filters requests by client IP
func IPFilterMiddleware(allowedNetworks []*net.IPNet) fiber.Handler {
	return func(c *fiber.Ctx) error {
		clientIP := c.IP()

		ip := net.ParseIP(clientIP)
		if ip == nil {
			ip = extractIP(clientIP)
		}

		if ip == nil {
			log.Printf("Security: Could not parse client IP: %s", clientIP)
			return c.Status(fiber.StatusForbidden).JSON(fiber.Map{
				"error": "Access denied: invalid client address",
			})
		}

		if !isIPAllowed(ip, allowedNetworks) {
			log.Printf("Security: Blocked request from unauthorized IP: %s", ip.String())
			return c.Status(fiber.StatusForbidden).JSON(fiber.Map{
				"error": "Access denied: your IP is not in the allowed networks",
			})
		}

		return c.Next()
	}
}

// NewIPFilterMiddleware creates a new IP filter middleware using environment configuration
func NewIPFilterMiddleware() (fiber.Handler, error) {
	allowedNetworksStr := GetAllowedNetworks()
	allowedNetworks, err := ParseCIDRs(allowedNetworksStr)
	if err != nil {
		return nil, err
	}

	log.Printf("Security: IP filter initialized with allowed networks: %s", allowedNetworksStr)
	return IPFilterMiddleware(allowedNetworks), nil
}
