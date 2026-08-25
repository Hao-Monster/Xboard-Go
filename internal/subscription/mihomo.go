package subscription

import (
	"fmt"
	"regexp"
	"strings"

	yaml "go.yaml.in/yaml/v3"
)

func renderMihomo(input RenderInput) (Response, error) {
	templateName := string(input.Client.Kind)
	template := input.Templates[templateName]
	config, err := parseMihomoTemplate(template, input.AppName)
	if err != nil {
		return Response{}, fmt.Errorf("parse %s subscription template: %w", templateName, err)
	}
	proxies := make([]any, 0, len(input.Nodes))
	proxyNames := make([]string, 0, len(input.Nodes))
	for _, node := range input.Nodes {
		proxy, ok := mihomoProxy(node, input.Client.Kind, input.Fingerprint)
		if !ok {
			continue
		}
		proxies = append(proxies, proxy)
		proxyNames = append(proxyNames, node.Name)
	}
	if existing, ok := config["proxies"].([]any); ok {
		config["proxies"] = append(existing, proxies...)
	} else {
		config["proxies"] = proxies
	}
	config["proxy-groups"] = expandProxyGroups(config["proxy-groups"], proxyNames)
	if input.RequestHost != "" {
		rules, _ := config["rules"].([]any)
		config["rules"] = append([]any{"DOMAIN," + input.RequestHost + ",DIRECT"}, rules...)
	}
	body, err := yaml.Marshal(config)
	if err != nil {
		return Response{}, fmt.Errorf("marshal %s subscription: %w", templateName, err)
	}
	body = []byte(strings.ReplaceAll(string(body), "$app_name", input.AppName))
	return Response{
		Body: body, ContentType: "text/yaml; charset=utf-8",
		Headers: map[string]string{
			"subscription-userinfo":   subscriptionUserInfo(input.Account),
			"profile-update-interval": "24",
			"content-disposition":     "attachment; filename*=UTF-8''" + rawURLEncode(input.AppName),
		},
	}, nil
}

func parseMihomoTemplate(content, appName string) (map[string]any, error) {
	if strings.TrimSpace(content) == "" {
		return defaultMihomoTemplate(appName), nil
	}
	var config map[string]any
	if err := yaml.Unmarshal([]byte(content), &config); err != nil {
		return nil, err
	}
	if config == nil {
		return nil, fmt.Errorf("template must contain a YAML object")
	}
	if _, exists := config["proxies"]; !exists {
		config["proxies"] = []any{}
	}
	if _, exists := config["proxy-groups"]; !exists {
		config["proxy-groups"] = []any{}
	}
	if _, exists := config["rules"]; !exists {
		config["rules"] = []any{}
	}
	return config, nil
}

func defaultMihomoTemplate(appName string) map[string]any {
	if appName == "" {
		appName = "Xboard"
	}
	return map[string]any{
		"mixed-port": 7890, "allow-lan": false, "mode": "rule", "log-level": "info",
		"proxies": []any{},
		"proxy-groups": []any{
			map[string]any{"name": appName, "type": "select", "proxies": []any{"自动选择", "故障转移", "DIRECT"}},
			map[string]any{"name": "自动选择", "type": "url-test", "proxies": []any{}, "url": "http://www.gstatic.com/generate_204", "interval": 300, "tolerance": 50},
			map[string]any{"name": "故障转移", "type": "fallback", "proxies": []any{}, "url": "http://www.gstatic.com/generate_204", "interval": 300},
		},
		"rules": []any{"MATCH," + appName},
	}
}

func expandProxyGroups(raw any, names []string) []any {
	groups, ok := raw.([]any)
	if !ok {
		return []any{}
	}
	result := make([]any, 0, len(groups))
	for _, rawGroup := range groups {
		group, ok := rawGroup.(map[string]any)
		if !ok {
			continue
		}
		rawEntries, _ := group["proxies"].([]any)
		entries := make([]any, 0, len(rawEntries)+len(names))
		usedPattern := false
		for _, rawEntry := range rawEntries {
			entry, ok := rawEntry.(string)
			if !ok {
				entries = append(entries, rawEntry)
				continue
			}
			pattern, regex := legacyProxyPattern(entry)
			if !regex {
				entries = append(entries, entry)
				continue
			}
			usedPattern = true
			for _, name := range names {
				if pattern.MatchString(name) {
					entries = append(entries, name)
				}
			}
		}
		if !usedPattern {
			for _, name := range names {
				entries = append(entries, name)
			}
		}
		if len(entries) == 0 {
			continue
		}
		group["proxies"] = entries
		result = append(result, group)
	}
	return result
}

func legacyProxyPattern(value string) (*regexp.Regexp, bool) {
	if len(value) < 2 || value[0] != '/' {
		return nil, false
	}
	last := strings.LastIndex(value[1:], "/")
	if last < 0 {
		return nil, false
	}
	last++
	pattern := value[1:last]
	flags := value[last+1:]
	if strings.Contains(flags, "i") {
		pattern = "(?i)" + pattern
	}
	compiled, err := regexp.Compile(pattern)
	return compiled, err == nil
}

func mihomoProxy(node PreparedNode, kind Kind, selectFingerprint func() string) (map[string]any, bool) {
	settings := node.ProtocolSettings
	base := map[string]any{"name": node.Name, "server": node.Host, "port": node.Port}
	switch node.Type {
	case "shadowsocks":
		cipher := stringSetting(settings, "cipher")
		if kind == KindClash {
			if _, supported := sip008Ciphers[cipher]; !supported {
				return nil, false
			}
		}
		base["type"], base["cipher"], base["password"], base["udp"] = "ss", cipher, node.Password, true
		if plugin := stringSetting(settings, "plugin"); plugin != "" {
			base["plugin"] = plugin
			base["plugin-opts"] = parsePluginOptions(stringSetting(settings, "plugin_opts"))
		}
	case "vmess":
		base["type"], base["uuid"], base["alterId"], base["cipher"], base["udp"] = "vmess", node.Password, 0, "auto", true
		applyMihomoTLS(base, settings, kind, node.Type, selectFingerprint)
		applyMihomoTransport(base, settings, kind, node.Type)
	case "vless":
		if kind == KindClash {
			return nil, false
		}
		base["type"], base["uuid"], base["udp"] = "vless", node.Password, true
		if kind == KindClashMeta {
			base["alterId"], base["cipher"], base["encryption"], base["tls"] = 0, "auto", "none", false
		}
		if flow := stringSetting(settings, "flow"); flow != "" {
			base["flow"] = flow
		}
		applyMihomoTLS(base, settings, kind, node.Type, selectFingerprint)
		applyMihomoTransport(base, settings, kind, node.Type)
	case "trojan":
		base["type"], base["password"], base["udp"] = "trojan", node.Password, true
		applyMihomoTLS(base, settings, kind, node.Type, selectFingerprint)
		applyMihomoTransport(base, settings, kind, node.Type)
	case "hysteria":
		if kind == KindClash {
			return nil, false
		}
		version := numberValue(nested(settings, "version"))
		if version == 2 {
			base["type"], base["password"] = "hysteria2", node.Password
			if boolSetting(settings, "obfs.open") {
				base["obfs"], base["obfs-password"] = "salamander", stringSetting(settings, "obfs.password")
			}
		} else {
			base["type"], base["auth-str"], base["protocol"] = "hysteria", node.Password, "udp"
		}
		if node.Ports != "" {
			base["ports"] = node.Ports
		}
		if sni := stringSetting(settings, "tls.server_name"); sni != "" {
			base["sni"] = sni
		}
		base["skip-cert-verify"] = boolSetting(settings, "tls.allow_insecure")
		upKey, downKey := "up", "down"
		if kind == KindStash {
			upKey, downKey = "up-speed", "down-speed"
		}
		if up := numberValue(nested(settings, "bandwidth.up")); up != 0 {
			base[upKey] = up
		}
		if down := numberValue(nested(settings, "bandwidth.down")); down != 0 {
			base[downKey] = down
		}
		if kind == KindStash && version == 2 {
			delete(base, "password")
			base["auth"], base["fast-open"] = node.Password, true
		}
	case "tuic":
		if kind == KindClash {
			return nil, false
		}
		base["type"], base["udp"] = "tuic", true
		if numberValue(nested(settings, "version")) == 4 {
			base["token"] = node.Password
		} else {
			base["uuid"], base["password"] = node.Password, node.Password
		}
		if sni := stringSetting(settings, "tls.server_name"); sni != "" {
			base["sni"] = sni
		}
		base["skip-cert-verify"] = boolSetting(settings, "tls.allow_insecure")
		alpn := stringsSetting(settings, "alpn")
		if len(alpn) == 0 {
			alpn = []string{"h3"}
		}
		base["alpn"] = alpn
		congestion := stringSetting(settings, "congestion_control")
		if congestion == "" {
			congestion = "cubic"
		}
		base["congestion-controller"] = congestion
		udpRelay := stringSetting(settings, "udp_relay_mode")
		if udpRelay == "" {
			udpRelay = "native"
		}
		base["udp-relay-mode"] = udpRelay
		if kind == KindStash {
			base["reduce-rtt"], base["fast-open"] = true, true
			base["heartbeat-interval"], base["request-timeout"], base["max-udp-relay-packet-size"] = 10000, 8000, 1500
			version := numberValue(nested(settings, "version"))
			if version == 0 {
				version = 5
			}
			base["version"] = version
		}
	case "anytls":
		if kind == KindClash {
			return nil, false
		}
		base["type"], base["password"], base["udp"] = "anytls", node.Password, true
		if sni := stringSetting(settings, "tls.server_name"); sni != "" {
			base["sni"] = sni
		}
		allowInsecure := boolSetting(settings, "tls.allow_insecure")
		if kind == KindStash || allowInsecure {
			base["skip-cert-verify"] = allowInsecure
		}
	case "socks":
		base["type"], base["username"], base["password"], base["udp"] = "socks5", node.Password, node.Password, true
		if boolSetting(settings, "tls") {
			base["tls"] = true
			base["skip-cert-verify"] = boolSetting(settings, "tls_settings.allow_insecure")
		}
	case "http":
		base["type"], base["username"], base["password"] = "http", node.Password, node.Password
		if boolSetting(settings, "tls") {
			base["tls"] = true
			base["skip-cert-verify"] = boolSetting(settings, "tls_settings.allow_insecure")
			if kind == KindStash {
				if sni := stringSetting(settings, "tls_settings.server_name"); sni != "" {
					base["sni"] = sni
				}
			}
		}
	case "mieru":
		if kind != KindClashMeta {
			return nil, false
		}
		base["type"], base["password"], base["port-range"] = "mieru", node.Password, node.Ports
		transport := strings.ToUpper(stringSetting(settings, "transport"))
		if transport == "" {
			transport = "TCP"
		}
		base["transport"] = transport
		if pattern := stringSetting(settings, "traffic_pattern"); pattern != "" {
			base["traffic-pattern"] = pattern
		}
	default:
		return nil, false
	}
	return base, true
}

func applyMihomoTLS(target map[string]any, settings map[string]any, kind Kind, nodeType string, selectFingerprint func() string) {
	mode := numberValue(nested(settings, "tls"))
	if mode == 0 && nodeType != "trojan" {
		return
	}
	if nodeType == "vmess" {
		target["tls"] = true
		target["skip-cert-verify"] = boolSetting(settings, "tls_settings.allow_insecure")
		if serverName := stringSetting(settings, "tls_settings.server_name"); serverName != "" {
			target["servername"] = serverName
		}
		if kind == KindClashMeta {
			appendConfiguredMihomoFingerprint(target, settings, selectFingerprint)
		}
		return
	}
	if nodeType == "trojan" && kind == KindClash {
		target["skip-cert-verify"] = boolSetting(settings, "tls_settings.allow_insecure")
		if serverName := stringSetting(settings, "tls_settings.server_name"); serverName != "" {
			target["sni"] = serverName
		}
		return
	}
	if nodeType == "vless" {
		target["tls"] = true
	}
	if mode == 2 {
		if nodeType == "trojan" && kind == KindStash {
			target["tls"] = true
		}
		target["skip-cert-verify"] = boolSetting(settings, "reality_settings.allow_insecure")
		target["reality-opts"] = map[string]any{
			"public-key": stringSetting(settings, "reality_settings.public_key"),
			"short-id":   stringSetting(settings, "reality_settings.short_id"),
		}
		if serverName := stringSetting(settings, "reality_settings.server_name"); serverName != "" {
			if nodeType == "trojan" {
				target["sni"] = serverName
			} else {
				target["servername"] = serverName
				if kind == KindStash {
					target["sni"] = serverName
				}
			}
		}
	} else {
		target["skip-cert-verify"] = boolSetting(settings, "tls_settings.allow_insecure")
		if serverName := stringSetting(settings, "tls_settings.server_name"); serverName != "" {
			if nodeType == "trojan" {
				target["sni"] = serverName
			} else {
				target["servername"] = serverName
			}
		}
	}
	if kind == KindClashMeta {
		appendConfiguredMihomoFingerprint(target, settings, selectFingerprint)
	} else if kind == KindStash && nodeType == "vless" {
		if fingerprint := tlsFingerprint(settings, selectFingerprint); fingerprint != "" {
			target["client-fingerprint"] = fingerprint
		}
	}
}

func appendConfiguredMihomoFingerprint(target map[string]any, settings map[string]any, selectFingerprint func() string) {
	if _, configured := nested(settings, "utls").(map[string]any); !configured {
		return
	}
	if fingerprint := tlsFingerprint(settings, selectFingerprint); fingerprint != "" {
		target["client-fingerprint"] = fingerprint
	}
}

func applyMihomoTransport(target map[string]any, settings map[string]any, kind Kind, nodeType string) {
	network := stringSetting(settings, "network")
	if network == "" {
		if kind == KindClash && nodeType == "trojan" {
			target["network"] = "tcp"
		}
		return
	}
	if network == "tcp" {
		if nodeType == "trojan" {
			target["network"] = "tcp"
		}
		return
	}
	if network == "httpupgrade" {
		if kind != KindClashMeta {
			return
		}
		target["network"] = "ws"
		options := map[string]any{"v2ray-http-upgrade": true}
		if path := stringSetting(settings, "network_settings.path"); path != "" {
			options["path"] = path
		}
		if host := stringSetting(settings, "network_settings.host"); host != "" {
			options["headers"] = map[string]any{"Host": host}
		}
		target["ws-opts"] = options
		return
	}
	target["network"] = network
	switch network {
	case "ws", "httpupgrade":
		options := map[string]any{}
		if path := stringSetting(settings, "network_settings.path"); path != "" {
			options["path"] = path
		}
		host := stringSetting(settings, "network_settings.headers.Host")
		if host == "" {
			host = stringSetting(settings, "network_settings.host")
		}
		if host != "" {
			options["headers"] = map[string]any{"Host": host}
		}
		target[network+"-opts"] = options
	case "grpc":
		target["grpc-opts"] = map[string]any{"grpc-service-name": stringSetting(settings, "network_settings.serviceName")}
	case "h2":
		target["h2-opts"] = map[string]any{"path": stringSetting(settings, "network_settings.path"), "host": stringsSetting(settings, "network_settings.host")}
	case "xhttp":
		options := map[string]any{"path": stringSetting(settings, "network_settings.path"), "host": stringSetting(settings, "network_settings.host")}
		if mode := stringSetting(settings, "network_settings.mode"); mode != "" {
			options["mode"] = mode
		}
		target["xhttp-opts"] = options
	}
}

func parsePluginOptions(value string) map[string]any {
	result := map[string]any{}
	for _, item := range strings.Split(value, ";") {
		key, itemValue, found := strings.Cut(item, "=")
		if found {
			result[strings.TrimSpace(key)] = strings.TrimSpace(itemValue)
		}
	}
	return result
}
