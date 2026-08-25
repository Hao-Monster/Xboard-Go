package subscription

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

func renderSingBox(input RenderInput) (Response, error) {
	config, err := parseSingBoxTemplate(input.Templates["singbox"], input.AppName)
	if err != nil {
		return Response{}, fmt.Errorf("parse sing-box subscription template: %w", err)
	}
	rawOutbounds, _ := config["outbounds"].([]any)
	proxies := make([]any, 0, len(input.Nodes))
	tags := make([]string, 0, len(input.Nodes))
	for _, node := range input.Nodes {
		outbound, ok := singBoxOutbound(node)
		if !ok {
			continue
		}
		proxies = append(proxies, outbound)
		tags = append(tags, node.Name)
	}
	for _, rawOutbound := range rawOutbounds {
		outbound, ok := rawOutbound.(map[string]any)
		if !ok {
			continue
		}
		typeName, _ := outbound["type"].(string)
		if typeName != "selector" && typeName != "urltest" {
			continue
		}
		selected := filterSingBoxTags(tags, outbound)
		delete(outbound, "include")
		delete(outbound, "exclude")
		delete(outbound, "fallback")
		current, _ := outbound["outbounds"].([]any)
		for _, tag := range selected {
			current = append(current, tag)
		}
		outbound["outbounds"] = current
	}
	config["outbounds"] = append(rawOutbounds, proxies...)
	adaptSingBoxConfig(config, input.Client)
	body, err := json.Marshal(config)
	if err != nil {
		return Response{}, fmt.Errorf("marshal sing-box subscription: %w", err)
	}
	appName := input.AppName
	if appName == "" {
		appName = "Xboard"
	}
	return Response{
		Body: body, ContentType: "application/json",
		Headers: map[string]string{
			"profile-title":           "base64:" + base64.StdEncoding.EncodeToString([]byte(appName)),
			"subscription-userinfo":   subscriptionUserInfo(input.Account),
			"profile-update-interval": "24",
		},
	}, nil
}

func parseSingBoxTemplate(content, appName string) (map[string]any, error) {
	if strings.TrimSpace(content) == "" {
		return defaultSingBoxTemplate(appName), nil
	}
	decoder := json.NewDecoder(strings.NewReader(content))
	decoder.UseNumber()
	var config map[string]any
	if err := decoder.Decode(&config); err != nil {
		return nil, err
	}
	if config == nil {
		return nil, fmt.Errorf("template must contain a JSON object")
	}
	if _, exists := config["outbounds"]; !exists {
		config["outbounds"] = []any{}
	}
	return config, nil
}

func defaultSingBoxTemplate(appName string) map[string]any {
	if appName == "" {
		appName = "Xboard"
	}
	return map[string]any{
		"log":      map[string]any{"level": "info", "timestamp": true},
		"dns":      map[string]any{"servers": []any{map[string]any{"tag": "dns-remote", "type": "https", "server": "1.1.1.1"}}},
		"inbounds": []any{map[string]any{"type": "tun", "tag": "tun-in", "address": []any{"172.19.0.1/30"}, "auto_route": true, "strict_route": true}},
		"outbounds": []any{
			map[string]any{"type": "selector", "tag": appName, "outbounds": []any{"自动选择", "direct"}},
			map[string]any{"type": "urltest", "tag": "自动选择", "outbounds": []any{}, "url": "https://www.gstatic.com/generate_204", "interval": "5m"},
			map[string]any{"type": "direct", "tag": "direct"},
		},
		"route": map[string]any{"rules": []any{map[string]any{"action": "sniff"}}, "final": appName},
	}
}

func singBoxOutbound(node PreparedNode) (map[string]any, bool) {
	settings := node.ProtocolSettings
	base := map[string]any{"tag": node.Name, "server": node.Host, "server_port": node.Port}
	switch node.Type {
	case "shadowsocks":
		base["type"], base["method"], base["password"] = "shadowsocks", stringSetting(settings, "cipher"), node.Password
		if plugin := stringSetting(settings, "plugin"); plugin != "" && stringSetting(settings, "plugin_opts") != "" {
			base["plugin"], base["plugin_opts"] = plugin, stringSetting(settings, "plugin_opts")
		}
	case "vmess":
		base["type"], base["uuid"], base["security"], base["alter_id"] = "vmess", node.Password, "auto", 0
		if boolSetting(settings, "tls") {
			base["tls"] = singBoxTLS(settings, "tls_settings", false)
		}
		appendSingBoxTransportAndMultiplex(base, settings, node.Host)
	case "vless":
		network := stringSetting(settings, "network")
		if network != "tcp" && network != "ws" && network != "grpc" && network != "http" && network != "h2" && network != "quic" && network != "httpupgrade" {
			return nil, false
		}
		base["type"], base["uuid"], base["packet_encoding"] = "vless", node.Password, "xudp"
		if flow := stringSetting(settings, "flow"); flow != "" {
			base["flow"] = flow
		}
		mode := numberValue(nested(settings, "tls"))
		if mode != 0 {
			base["tls"] = singBoxTLS(settings, map[bool]string{true: "reality_settings", false: "tls_settings"}[mode == 2], mode == 2)
		}
		appendSingBoxTransportAndMultiplex(base, settings, node.Host)
	case "trojan":
		base["type"], base["password"] = "trojan", node.Password
		mode := numberValue(nested(settings, "tls"))
		base["tls"] = singBoxTLS(settings, map[bool]string{true: "reality_settings", false: "tls_settings"}[mode == 2], mode == 2)
		appendSingBoxTransportAndMultiplex(base, settings, node.Host)
	case "hysteria":
		base["tls"] = singBoxTLS(settings, "tls", false)
		if node.Ports != "" {
			base["server_ports"] = []any{strings.ReplaceAll(node.Ports, "-", ":")}
		}
		if hop := numberValue(nested(settings, "hop_interval")); hop != 0 {
			base["hop_interval"] = fmt.Sprintf("%ds", hop)
		}
		if up := numberValue(nested(settings, "bandwidth.up")); up != 0 {
			base["up_mbps"] = up
		}
		if down := numberValue(nested(settings, "bandwidth.down")); down != 0 {
			base["down_mbps"] = down
		}
		if numberValue(nested(settings, "version")) == 2 {
			base["type"], base["password"] = "hysteria2", node.Password
			if boolSetting(settings, "obfs.open") {
				base["obfs"] = map[string]any{"type": defaultString(stringSetting(settings, "obfs.type"), "salamander"), "password": stringSetting(settings, "obfs.password")}
			}
		} else {
			base["type"], base["auth_str"], base["disable_mtu_discovery"] = "hysteria", node.Password, true
			if password := stringSetting(settings, "obfs.password"); password != "" {
				base["obfs"] = password
			}
		}
	case "tuic":
		base["type"] = "tuic"
		base["congestion_control"] = defaultString(stringSetting(settings, "congestion_control"), "cubic")
		base["udp_relay_mode"] = defaultString(stringSetting(settings, "udp_relay_mode"), "native")
		base["zero_rtt_handshake"], base["heartbeat"] = true, "10s"
		base["tls"] = singBoxTLS(settings, "tls", false)
		tls := base["tls"].(map[string]any)
		alpn := stringsSetting(settings, "alpn")
		if len(alpn) == 0 {
			alpn = []string{"h3"}
		}
		tls["alpn"] = alpn
		if numberValue(nested(settings, "version")) == 4 {
			base["token"] = node.Password
		} else {
			base["uuid"], base["password"] = node.Password, node.Password
		}
	case "anytls":
		base["type"], base["password"] = "anytls", node.Password
		base["tls"] = singBoxTLS(settings, "tls", false)
		tls := base["tls"].(map[string]any)
		alpn := stringsSetting(settings, "alpn")
		if len(alpn) == 0 {
			alpn = []string{"h3"}
		}
		tls["alpn"] = alpn
	case "socks":
		base["type"], base["version"], base["username"], base["password"] = "socks", "5", node.Password, node.Password
		if boolSetting(settings, "udp_over_tcp") {
			base["udp_over_tcp"] = true
		}
	case "http":
		base["type"], base["username"], base["password"] = "http", node.Password, node.Password
		if path := stringSetting(settings, "path"); path != "" {
			base["path"] = path
		}
		if headers := nested(settings, "headers"); headers != nil {
			base["headers"] = headers
		}
		if boolSetting(settings, "tls") {
			base["tls"] = singBoxTLS(settings, "tls_settings", false)
		}
	default:
		return nil, false
	}
	return base, true
}

func singBoxTLS(settings map[string]any, prefix string, reality bool) map[string]any {
	tls := map[string]any{"enabled": true, "insecure": boolSetting(settings, prefix+".allow_insecure")}
	if serverName := stringSetting(settings, prefix+".server_name"); serverName != "" {
		tls["server_name"] = serverName
	}
	if reality {
		tls["reality"] = map[string]any{
			"enabled": true, "public_key": nilIfEmpty(stringSetting(settings, "reality_settings.public_key")), "short_id": nilIfEmpty(stringSetting(settings, "reality_settings.short_id")),
		}
	}
	if object, ok := nested(settings, "utls").(map[string]any); ok && boolSetting(object, "enabled") {
		tls["utls"] = map[string]any{"enabled": true, "fingerprint": defaultString(stringSetting(object, "fingerprint"), "chrome")}
	}
	if ech, ok := nested(settings, prefix+".ech").(map[string]any); ok && boolSetting(ech, "enabled") {
		value := map[string]any{"enabled": true}
		if config := stringSetting(ech, "config"); config != "" {
			value["config"] = []any{config}
		}
		if queryName := stringSetting(ech, "query_server_name"); queryName != "" {
			value["query_server_name"] = queryName
		}
		tls["ech"] = value
	}
	return tls
}

func appendSingBoxTransportAndMultiplex(target map[string]any, settings map[string]any, fallbackHost string) {
	network := stringSetting(settings, "network")
	transport := map[string]any{}
	switch network {
	case "tcp":
		if stringSetting(settings, "network_settings.header.type") != "http" {
			transport = nil
			break
		}
		transport["type"] = "http"
		if paths := stringsSetting(settings, "network_settings.header.request.path"); len(paths) > 0 {
			transport["path"] = paths[0]
		}
		transport["host"] = stringsSetting(settings, "network_settings.header.request.headers.Host")
	case "ws":
		transport["type"], transport["path"], transport["max_early_data"] = "ws", stringSetting(settings, "network_settings.path"), 0
		if host := stringSetting(settings, "network_settings.headers.Host"); host != "" {
			transport["headers"] = map[string]any{"Host": host}
		}
	case "grpc":
		transport["type"], transport["service_name"] = "grpc", stringSetting(settings, "network_settings.serviceName")
	case "h2", "http":
		transport["type"], transport["host"], transport["path"] = "http", stringsSetting(settings, "network_settings.host"), stringSetting(settings, "network_settings.path")
	case "httpupgrade":
		transport["type"], transport["path"] = "httpupgrade", stringSetting(settings, "network_settings.path")
		host := stringSetting(settings, "network_settings.host")
		if host == "" {
			host = fallbackHost
		}
		transport["host"] = host
		if headers := nested(settings, "network_settings.headers"); headers != nil {
			transport["headers"] = headers
		}
	case "quic":
		transport["type"] = "quic"
	default:
		transport = nil
	}
	if len(transport) > 0 {
		target["transport"] = transport
	}
	if multiplex, ok := nested(settings, "multiplex").(map[string]any); ok && boolSetting(multiplex, "enabled") {
		value := map[string]any{"enabled": true, "protocol": defaultString(stringSetting(multiplex, "protocol"), "yamux"), "padding": boolSetting(multiplex, "padding")}
		for _, key := range []string{"max_connections", "min_streams", "max_streams"} {
			if number := numberValue(nested(multiplex, key)); number != 0 {
				value[key] = number
			}
		}
		if brutal, ok := nested(multiplex, "brutal").(map[string]any); ok && boolSetting(brutal, "enabled") {
			value["brutal"] = map[string]any{"enabled": true, "up_mbps": numberValue(nested(brutal, "up_mbps")), "down_mbps": numberValue(nested(brutal, "down_mbps"))}
		}
		target["multiplex"] = value
	}
}

func filterSingBoxTags(tags []string, outbound map[string]any) []string {
	selected := append([]string(nil), tags...)
	if include, ok := outbound["include"].(string); ok && include != "" {
		selected = regexFilterTags(selected, include, true)
	}
	if exclude, ok := outbound["exclude"].(string); ok && exclude != "" {
		selected = regexFilterTags(selected, exclude, false)
	}
	if len(selected) == 0 {
		if fallback, ok := outbound["fallback"].(string); ok && fallback != "" {
			return []string{fallback}
		}
	}
	return selected
}

func regexFilterTags(tags []string, pattern string, keepMatch bool) []string {
	pattern = strings.TrimSpace(pattern)
	if len(pattern) >= 2 && pattern[0] == '/' {
		if last := strings.LastIndex(pattern[1:], "/"); last >= 0 {
			last++
			flags := pattern[last+1:]
			pattern = pattern[1:last]
			if strings.Contains(flags, "i") {
				pattern = "(?i)" + pattern
			}
		}
	} else {
		pattern = "(?i)" + pattern
	}
	compiled, err := regexp.Compile(pattern)
	if err != nil {
		return nil
	}
	result := make([]string, 0, len(tags))
	for _, tag := range tags {
		if compiled.MatchString(tag) == keepMatch {
			result = append(result, tag)
		}
	}
	return result
}

func adaptSingBoxConfig(config map[string]any, client ClientInfo) {
	coreVersion := client.Version
	if client.Name != "sing-box" && coreVersion != "" {
		coreVersion = "1.13.0"
	}
	if coreVersion == "" {
		return
	}
	if VersionAtLeast(coreVersion, "1.13.0") {
		upgradeSingBoxSpecialOutbounds(config)
	}
	if !VersionAtLeast(coreVersion, "1.11.0") {
		downgradeSingBoxActions(config)
		restoreSingBoxInboundFields(config)
	}
	if !VersionAtLeast(coreVersion, "1.12.0") {
		convertSingBoxDNS(config)
	}
	if !VersionAtLeast(coreVersion, "1.10.0") {
		convertSingBoxTUN(config)
	}
}

func upgradeSingBoxSpecialOutbounds(config map[string]any) {
	outbounds, _ := config["outbounds"].([]any)
	removed := map[string]string{}
	kept := make([]any, 0, len(outbounds))
	for _, raw := range outbounds {
		outbound, _ := raw.(map[string]any)
		typeName, _ := outbound["type"].(string)
		if typeName == "block" || typeName == "dns" {
			tag, _ := outbound["tag"].(string)
			removed[tag] = typeName
			continue
		}
		kept = append(kept, raw)
	}
	config["outbounds"] = kept
	for _, rule := range singBoxRules(config) {
		outbound, _ := rule["outbound"].(string)
		typeName, exists := removed[outbound]
		if !exists {
			continue
		}
		delete(rule, "outbound")
		if typeName == "dns" {
			rule["action"] = "hijack-dns"
		} else {
			rule["action"] = "reject"
		}
	}
}

func downgradeSingBoxActions(config map[string]any) {
	needBlock, needDNS := false, false
	for _, rule := range singBoxRules(config) {
		action, _ := rule["action"].(string)
		switch action {
		case "reject":
			delete(rule, "action")
			rule["outbound"], needBlock = "block", true
		case "hijack-dns":
			delete(rule, "action")
			rule["outbound"], needDNS = "dns-out", true
		}
	}
	outbounds, _ := config["outbounds"].([]any)
	if needBlock {
		outbounds = append(outbounds, map[string]any{"type": "block", "tag": "block"})
	}
	if needDNS {
		outbounds = append(outbounds, map[string]any{"type": "dns", "tag": "dns-out"})
	}
	config["outbounds"] = outbounds
}

func singBoxRules(config map[string]any) []map[string]any {
	route, _ := config["route"].(map[string]any)
	rawRules, _ := route["rules"].([]any)
	rules := make([]map[string]any, 0, len(rawRules))
	for _, raw := range rawRules {
		if rule, ok := raw.(map[string]any); ok {
			rules = append(rules, rule)
		}
	}
	return rules
}

func restoreSingBoxInboundFields(config map[string]any) {
	for _, inbound := range mapSlice(config["inbounds"]) {
		if inbound["type"] == "tun" {
			inbound["endpoint_independent_nat"] = true
		}
		if sniff, _ := inbound["sniff"].(bool); sniff {
			inbound["sniff_override_destination"] = true
		}
	}
}

func convertSingBoxDNS(config map[string]any) {
	dns, _ := config["dns"].(map[string]any)
	for _, server := range mapSlice(dns["servers"]) {
		typeName, exists := server["type"].(string)
		if !exists {
			continue
		}
		host, _ := server["server"].(string)
		switch typeName {
		case "https":
			server["address"] = "https://" + host + "/dns-query"
		case "tls", "tcp", "quic":
			server["address"] = typeName + "://" + host
		case "block":
			server["address"] = "rcode://refused"
		case "rcode":
			rcode, _ := server["rcode"].(string)
			server["address"] = "rcode://" + defaultString(rcode, "success")
			delete(server, "rcode")
		default:
			server["address"] = host
		}
		delete(server, "type")
		delete(server, "server")
	}
}

func convertSingBoxTUN(config map[string]any) {
	for _, inbound := range mapSlice(config["inbounds"]) {
		if inbound["type"] != "tun" {
			continue
		}
		addresses, _ := inbound["address"].([]any)
		for _, raw := range addresses {
			address, _ := raw.(string)
			if strings.Contains(address, ":") {
				inbound["inet6_address"] = address
			} else if address != "" {
				inbound["inet4_address"] = address
			}
		}
		delete(inbound, "address")
	}
}

func mapSlice(value any) []map[string]any {
	rawItems, _ := value.([]any)
	result := make([]map[string]any, 0, len(rawItems))
	for _, raw := range rawItems {
		if item, ok := raw.(map[string]any); ok {
			result = append(result, item)
		}
	}
	return result
}

func defaultString(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}
