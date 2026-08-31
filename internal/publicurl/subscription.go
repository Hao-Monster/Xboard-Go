package publicurl

import (
	"crypto/rand"
	"fmt"
	"math/big"
	"net"
	"net/url"
	"strconv"
	"strings"
)

type SubscriptionConfig struct {
	Origins    string
	AppURL     string
	PanelURL   string
	Path       string
	ForceHTTPS bool
}

func BuildSubscription(config SubscriptionConfig, token, fragment string) (string, error) {
	path := strings.Trim(config.Path, "/")
	if !validPath(path) || !validToken(token) {
		return "", fmt.Errorf("invalid subscription URL input")
	}
	base := ""
	if config.Origins != "" {
		origins := strings.Split(config.Origins, ",")
		index := 0
		if len(origins) > 1 {
			selected, err := rand.Int(rand.Reader, big.NewInt(int64(len(origins))))
			if err != nil {
				return "", fmt.Errorf("select subscription origin: %w", err)
			}
			index = int(selected.Int64())
		}
		base = origins[index]
	} else {
		base = strings.TrimSpace(config.AppURL)
		if base == "" {
			base = strings.TrimSpace(config.PanelURL)
		}
	}
	parsed, err := url.Parse(base)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" || parsed.Hostname() == "" || parsed.User != nil || parsed.Opaque != "" || !validPort(parsed.Port()) {
		return "", fmt.Errorf("invalid subscription origin")
	}
	parsed.Scheme = strings.ToLower(parsed.Scheme)
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", fmt.Errorf("invalid subscription origin scheme")
	}
	if config.Origins != "" && (parsed.RawQuery != "" || parsed.ForceQuery || parsed.Fragment != "" || (parsed.Scheme == "http" && !loopbackHost(parsed.Hostname()))) {
		return "", fmt.Errorf("unsafe configured subscription origin")
	}
	if config.Origins == "" && config.ForceHTTPS && strings.EqualFold(parsed.Scheme, "http") {
		parsed.Scheme = "https"
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/") + "/" + path + "/" + token
	parsed.RawPath = ""
	parsed.RawQuery = ""
	parsed.ForceQuery = false
	parsed.Fragment = fragment
	return parsed.String(), nil
}

func loopbackHost(hostname string) bool {
	hostname = strings.TrimSuffix(strings.ToLower(hostname), ".")
	if hostname == "localhost" || strings.HasSuffix(hostname, ".localhost") {
		return true
	}
	address := net.ParseIP(hostname)
	return address != nil && address.IsLoopback()
}

func validPort(value string) bool {
	if value == "" {
		return true
	}
	port, err := strconv.Atoi(value)
	return err == nil && port >= 1 && port <= 65_535
}

func validPath(value string) bool {
	if len(value) < 1 || len(value) > 64 {
		return false
	}
	for _, character := range value {
		if (character < 'a' || character > 'z') && (character < 'A' || character > 'Z') &&
			(character < '0' || character > '9') && character != '_' && character != '-' {
			return false
		}
	}
	return true
}

func validToken(value string) bool {
	if len(value) != 32 {
		return false
	}
	for _, character := range value {
		if (character < '0' || character > '9') && (character < 'a' || character > 'f') {
			return false
		}
	}
	return true
}
