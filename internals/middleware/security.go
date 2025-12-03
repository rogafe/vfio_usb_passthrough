package middleware

import (
	"log"
	"net"
	"os"
	"strings"

	"github.com/gofiber/fiber/v2"
)

// Default allowed networks (private/local networks)
const defaultAllowedNetworks = "127.0.0.0/8,10.0.0.0/8,172.16.0.0/12,192.168.0.0/16"

// DefaultBindAddr is the default address to bind the server to
const DefaultBindAddr = "127.0.0.1:3000"

// GetBindAddr returns the bind address from environment or default
func GetBindAddr() string {
	bindAddr := os.Getenv("BIND_ADDR")
	if bindAddr == "" {
		return DefaultBindAddr
	}
	return bindAddr
}

// GetAllowedNetworks returns the allowed networks from environment or default
func GetAllowedNetworks() string {
	allowedNetworks := os.Getenv("ALLOWED_NETWORKS")
	if allowedNetworks == "" {
		return defaultAllowedNetworks
	}
	return allowedNetworks
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
	// Try to parse as host:port first
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		// If that fails, try to parse as just an IP
		host = remoteAddr
	}

	return net.ParseIP(host)
}

// IPFilterMiddleware returns a Fiber middleware that filters requests by client IP
func IPFilterMiddleware(allowedNetworks []*net.IPNet) fiber.Handler {
	return func(c *fiber.Ctx) error {
		// Get client IP - Fiber provides this through various headers and connection info
		clientIP := c.IP()

		// Parse the client IP
		ip := net.ParseIP(clientIP)
		if ip == nil {
			// Try extracting from the full remote address
			ip = extractIP(clientIP)
		}

		if ip == nil {
			log.Printf("Security: Could not parse client IP: %s", clientIP)
			return c.Status(fiber.StatusForbidden).JSON(fiber.Map{
				"error": "Access denied: invalid client address",
			})
		}

		// Check if IP is in allowed networks
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

