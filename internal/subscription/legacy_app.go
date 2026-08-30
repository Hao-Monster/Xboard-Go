package subscription

import (
	_ "embed"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"unicode/utf8"

	yaml "go.yaml.in/yaml/v3"
)

const MaxLegacyAppConfigBytes = 16 << 20

//go:embed assets/legacy_app_clash.yaml
var defaultLegacyAppClashTemplate string

func DefaultLegacyAppClashTemplate() string { return defaultLegacyAppClashTemplate }

// ValidateLegacyAppClashTemplate validates an operator-supplied template at
// startup, before it can affect client routing.
func ValidateLegacyAppClashTemplate(content string) error {
	if len(content) == 0 || len(content) > 1<<20 || !utf8.ValidString(content) {
		return errors.New("legacy app Clash template must contain 1 byte to 1 MiB")
	}
	_, err := parseLegacyAppClashTemplate(content)
	return err
}

func LoadLegacyAppClashTemplateFile(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("open legacy app Clash template: %w", err)
	}
	defer file.Close()
	body, err := io.ReadAll(io.LimitReader(file, 1<<20+1))
	if err != nil {
		return "", fmt.Errorf("read legacy app Clash template: %w", err)
	}
	content := string(body)
	if err := ValidateLegacyAppClashTemplate(content); err != nil {
		return "", err
	}
	return content, nil
}

// LegacyAppClashRenderer keeps the parsed, immutable template in memory so the
// hot path only clones its bounded object tree and serializes the response.
type LegacyAppClashRenderer struct {
	template map[string]any
}

func NewLegacyAppClashRenderer(content string) (*LegacyAppClashRenderer, error) {
	if content == "" {
		content = defaultLegacyAppClashTemplate
	}
	if len(content) > 1<<20 || !utf8.ValidString(content) {
		return nil, errors.New("legacy app Clash template must contain at most 1 MiB of valid UTF-8")
	}
	config, err := parseLegacyAppClashTemplate(content)
	if err != nil {
		return nil, err
	}
	return &LegacyAppClashRenderer{template: config}, nil
}

func RenderLegacyAppClash(content string, nodes []PreparedNode) ([]byte, error) {
	renderer, err := NewLegacyAppClashRenderer(content)
	if err != nil {
		return nil, err
	}
	return renderer.Render(nodes)
}

func (renderer *LegacyAppClashRenderer) Render(nodes []PreparedNode) ([]byte, error) {
	if renderer == nil || renderer.template == nil {
		return nil, errors.New("legacy app Clash renderer is not initialized")
	}
	config := cloneLegacyAppValue(renderer.template).(map[string]any)
	proxies := sequence(config["proxies"])
	proxyNames := make([]string, 0, len(nodes))
	for _, node := range nodes {
		proxy, ok := legacyAppProxy(node)
		if !ok {
			continue
		}
		proxies = append(proxies, proxy)
		proxyNames = append(proxyNames, node.Name)
	}
	config["proxies"] = proxies
	groups := sequence(config["proxy-groups"])
	for index, raw := range groups {
		group, ok := raw.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("legacy app Clash proxy group %d must be an object", index)
		}
		entries := sequence(group["proxies"])
		for _, name := range proxyNames {
			entries = append(entries, name)
		}
		group["proxies"] = entries
	}
	config["proxy-groups"] = groups
	body, err := yaml.Marshal(config)
	if err != nil {
		return nil, fmt.Errorf("marshal legacy app Clash template: %w", err)
	}
	if len(body) > MaxLegacyAppConfigBytes {
		return nil, errors.New("legacy app Clash response exceeds 16 MiB")
	}
	return body, nil
}

func cloneLegacyAppValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		cloned := make(map[string]any, len(typed))
		for key, child := range typed {
			cloned[key] = cloneLegacyAppValue(child)
		}
		return cloned
	case []any:
		cloned := make([]any, len(typed))
		for index, child := range typed {
			cloned[index] = cloneLegacyAppValue(child)
		}
		return cloned
	default:
		return value
	}
}

func parseLegacyAppClashTemplate(content string) (map[string]any, error) {
	var config map[string]any
	if err := yaml.Unmarshal([]byte(content), &config); err != nil {
		return nil, fmt.Errorf("parse legacy app Clash template: %w", err)
	}
	if config == nil {
		return nil, errors.New("legacy app Clash template must contain a YAML object")
	}
	for _, key := range []string{"proxies", "proxy-groups", "rules"} {
		value, exists := config[key]
		if !exists || value == nil {
			config[key] = []any{}
			continue
		}
		if _, ok := value.([]any); !ok {
			return nil, fmt.Errorf("legacy app Clash template %q must be a sequence", key)
		}
	}
	return config, nil
}

func sequence(value any) []any {
	if values, ok := value.([]any); ok {
		return values
	}
	return []any{}
}

func legacyAppProxy(node PreparedNode) (map[string]any, bool) {
	settings := node.ProtocolSettings
	base := map[string]any{"name": node.Name, "server": node.Host, "port": node.Port}
	switch node.Type {
	case "shadowsocks":
		cipher := stringSetting(settings, "cipher")
		if _, supported := sip008Ciphers[cipher]; !supported {
			return nil, false
		}
		base["type"], base["cipher"], base["password"], base["udp"] = "ss", cipher, node.Password, true
		plugin, pluginOptions := stringSetting(settings, "plugin"), stringSetting(settings, "plugin_opts")
		if plugin != "" && pluginOptions != "" {
			parsed := parsePluginOptions(pluginOptions)
			base["plugin"] = plugin
			switch plugin {
			case "obfs":
				mode := stringValue(parsed["obfs"])
				if mode == "" {
					mode = stringSetting(settings, "obfs")
				}
				if mode == "" {
					mode = "http"
				}
				host := stringValue(parsed["obfs-host"])
				if host == "" {
					host = stringSetting(settings, "obfs_settings.host")
				}
				options := map[string]any{"mode": mode, "host": host}
				if path := stringValue(parsed["path"]); path != "" {
					options["path"] = path
				}
				base["plugin-opts"] = options
			case "v2ray-plugin":
				mode := stringValue(parsed["mode"])
				if mode == "" {
					mode = "websocket"
				}
				path := stringValue(parsed["path"])
				if path == "" {
					path = "/"
				}
				base["plugin-opts"] = map[string]any{
					"mode": mode, "tls": stringValue(parsed["tls"]) == "true",
					"host": stringValue(parsed["host"]), "path": path,
				}
			default:
				base["plugin-opts"] = parsed
			}
		}
	case "vmess":
		base["type"], base["uuid"], base["alterId"], base["cipher"], base["udp"] = "vmess", node.Password, 0, "auto", true
		if numberValue(nested(settings, "tls")) != 0 {
			base["tls"] = true
			base["skip-cert-verify"] = boolSetting(settings, "tls_settings.allow_insecure")
			if serverName := stringSetting(settings, "tls_settings.server_name"); serverName != "" {
				base["servername"] = serverName
			}
		}
		switch stringSetting(settings, "network") {
		case "tcp":
			headerType := stringSetting(settings, "network_settings.header.type")
			if headerType == "http" {
				base["network"] = "http"
				options := map[string]any{}
				if headers := nested(settings, "network_settings.header.request.headers"); headers != nil {
					options["headers"] = headers
				}
				path := nested(settings, "network_settings.header.request.path")
				if path == nil {
					path = []any{"/"}
				}
				options["path"] = path
				base["http-opts"] = options
			} else {
				base["network"] = "tcp"
			}
		case "ws":
			base["network"] = "ws"
			options := map[string]any{}
			if path := stringSetting(settings, "network_settings.path"); path != "" {
				options["path"] = path
			}
			if host := stringSetting(settings, "network_settings.headers.Host"); host != "" {
				options["headers"] = map[string]any{"Host": host}
			}
			if len(options) > 0 {
				base["ws-opts"] = options
			}
		case "grpc":
			base["network"] = "grpc"
			if serviceName := stringSetting(settings, "network_settings.serviceName"); serviceName != "" {
				base["grpc-opts"] = map[string]any{"grpc-service-name": serviceName}
			}
		}
	case "trojan":
		base["type"], base["password"], base["udp"] = "trojan", node.Password, true
		if serverName := stringSetting(settings, "tls_settings.server_name"); serverName != "" {
			base["sni"] = serverName
		}
		base["skip-cert-verify"] = boolSetting(settings, "tls_settings.allow_insecure")
		network := stringSetting(settings, "network")
		switch network {
		case "ws":
			base["network"] = "ws"
			options := map[string]any{}
			if path := stringSetting(settings, "network_settings.path"); path != "" {
				options["path"] = path
			}
			if host := stringSetting(settings, "network_settings.headers.Host"); host != "" {
				options["headers"] = map[string]any{"Host": host}
			}
			if len(options) > 0 {
				base["ws-opts"] = options
			}
		case "grpc":
			base["network"] = "grpc"
			if serviceName := stringSetting(settings, "network_settings.serviceName"); serviceName != "" {
				base["grpc-opts"] = map[string]any{"grpc-service-name": serviceName}
			}
		default:
			base["network"] = "tcp"
		}
	default:
		return nil, false
	}
	return base, true
}

func stringValue(value any) string {
	text, _ := value.(string)
	return strings.TrimSpace(text)
}
