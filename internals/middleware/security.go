package middleware

import (
	"bufio"
	"encoding/hex"
	"encoding/xml"
	"fmt"
	"log"
	"net"
	"os"
	"os/exec"
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

// virshNetworkIP represents the IP configuration in a virsh network XML
type virshNetworkIP struct {
	Address string `xml:"address,attr"`
	Netmask string `xml:"netmask,attr"`
	Prefix  string `xml:"prefix,attr"` // Alternative to netmask
}

// virshNetwork represents a virsh network XML structure
type virshNetwork struct {
	XMLName xml.Name         `xml:"network"`
	Name    string           `xml:"name"`
	IPs     []virshNetworkIP `xml:"ip"`
}

// netmaskToCIDR converts a netmask string (e.g., "255.255.255.0") to CIDR prefix length
func netmaskToCIDR(netmask string) (int, error) {
	ip := net.ParseIP(netmask)
	if ip == nil {
		return 0, fmt.Errorf("invalid netmask: %s", netmask)
	}

	ip4 := ip.To4()
	if ip4 == nil {
		return 0, fmt.Errorf("not an IPv4 netmask: %s", netmask)
	}

	mask := net.IPMask(ip4)
	ones, _ := mask.Size()
	return ones, nil
}

// getVirshNetworkSubnets queries libvirt for active networks and returns their subnets
func getVirshNetworkSubnets() []string {
	var subnets []string

	// Get list of active networks
	cmd := exec.Command("virsh", "net-list", "--name")
	cmd.Env = append(os.Environ(), "LIBVIRT_DEFAULT_URI=qemu:///system")
	output, err := cmd.Output()
	if err != nil {
		log.Printf("Security: Warning - could not list virsh networks: %v", err)
		return subnets
	}

	scanner := bufio.NewScanner(strings.NewReader(string(output)))
	for scanner.Scan() {
		netName := strings.TrimSpace(scanner.Text())
		if netName == "" {
			continue
		}

		// Get network XML
		xmlCmd := exec.Command("virsh", "net-dumpxml", netName)
		xmlCmd.Env = append(os.Environ(), "LIBVIRT_DEFAULT_URI=qemu:///system")
		xmlOutput, err := xmlCmd.Output()
		if err != nil {
			log.Printf("Security: Warning - could not get XML for virsh network %s: %v", netName, err)
			continue
		}

		// Parse the XML
		var network virshNetwork
		if err := xml.Unmarshal(xmlOutput, &network); err != nil {
			log.Printf("Security: Warning - could not parse XML for virsh network %s: %v", netName, err)
			continue
		}

		// Extract subnets from IP configurations
		for _, ipConfig := range network.IPs {
			if ipConfig.Address == "" {
				continue
			}

			ip := net.ParseIP(ipConfig.Address)
			if ip == nil || ip.To4() == nil {
				continue
			}

			var cidrPrefix int
			if ipConfig.Prefix != "" {
				fmt.Sscanf(ipConfig.Prefix, "%d", &cidrPrefix)
			} else if ipConfig.Netmask != "" {
				cidrPrefix, err = netmaskToCIDR(ipConfig.Netmask)
				if err != nil {
					log.Printf("Security: Warning - invalid netmask for virsh network %s: %v", netName, err)
					continue
				}
			} else {
				// Default to /24 if no mask specified
				cidrPrefix = 24
			}

			// Calculate network address
			mask := net.CIDRMask(cidrPrefix, 32)
			networkIP := ip.To4().Mask(mask)
			subnet := fmt.Sprintf("%s/%d", networkIP.String(), cidrPrefix)
			subnets = append(subnets, subnet)
			log.Printf("Security: Auto-allowing subnet %s from virsh network %s", subnet, netName)
		}
	}

	return subnets
}

// getAutoDetectedSubnets finds all subnets that should be allowed by default:
// 1. Localhost (127.0.0.0/8)
// 2. Subnets from interfaces with default routes (local network)
// 3. Subnets from libvirt/virsh networks (VM networks)
func getAutoDetectedSubnets() []string {
	var subnets []string
	seen := make(map[string]bool)

	// Always include localhost
	subnets = append(subnets, "127.0.0.0/8")
	seen["127.0.0.0/8"] = true

	// Get interfaces with default routes
	defaultRouteIfaces := getDefaultRouteInterfaces()
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

	// Get virsh network subnets
	virshSubnets := getVirshNetworkSubnets()
	for _, subnet := range virshSubnets {
		if !seen[subnet] {
			seen[subnet] = true
			subnets = append(subnets, subnet)
		}
	}

	if len(subnets) == 1 {
		log.Printf("Security: No default route interfaces or virsh networks found, only localhost will be allowed")
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
// Otherwise, auto-detect subnets from:
// - Interfaces with default routes (local network)
// - Libvirt/virsh networks (VM networks)
// This ensures only local and VM network traffic is allowed, blocking internet-originated requests
func GetAllowedNetworks() string {
	allowedNetworks := os.Getenv("ALLOWED_NETWORKS")
	if allowedNetworks != "" {
		return allowedNetworks
	}

	// Auto-detect subnets
	subnets := getAutoDetectedSubnets()
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
