package subscription

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
)

func renderGeneral(input RenderInput) (Response, error) {
	var body strings.Builder
	body.Grow(len(input.Nodes) * 180)
	for _, node := range input.Nodes {
		var line string
		var err error
		switch node.Type {
		case "shadowsocks":
			line = generalShadowsocks(node)
		case "vmess":
			line, err = generalVMess(node, input.Fingerprint)
		case "vless":
			line = generalVLESS(node, input.Fingerprint)
		case "trojan":
			line = generalTrojan(node, input.Fingerprint)
		case "hysteria":
			line = generalHysteria(node)
		case "tuic":
			line = generalTUIC(node)
		case "anytls":
			line = generalAnyTLS(node)
		case "socks":
			line = generalSOCKS(node)
		case "http":
			line = generalHTTP(node)
		}
		if err != nil {
			return Response{}, err
		}
		body.WriteString(line)
	}
	encoded := base64.StdEncoding.EncodeToString([]byte(body.String()))
	return Response{
		Body: []byte(encoded), ContentType: "text/plain; charset=utf-8",
		Headers: map[string]string{"subscription-userinfo": subscriptionUserInfo(input.Account)},
	}, nil
}

func generalShadowsocks(node PreparedNode) string {
	credentials := base64.RawURLEncoding.EncodeToString([]byte(stringSetting(node.ProtocolSettings, "cipher") + ":" + node.Password))
	result := fmt.Sprintf("ss://%s@%s:%d", credentials, wrapAddress(node.Host), node.Port)
	plugin := stringSetting(node.ProtocolSettings, "plugin")
	pluginOptions := stringSetting(node.ProtocolSettings, "plugin_opts")
	if plugin != "" && pluginOptions != "" {
		result += "/?plugin=" + rawURLEncode(plugin+";"+pluginOptions)
	}
	return result + "#" + rawURLEncode(node.Name) + "\r\n"
}

func generalVMess(node PreparedNode, selectFingerprint func() string) (string, error) {
	settings := node.ProtocolSettings
	config := map[string]any{
		"v": "2", "ps": node.Name, "add": node.Host, "port": fmt.Sprint(node.Port), "id": node.Password,
		"aid": "0", "net": nested(settings, "network"), "type": "none", "host": "", "path": "", "tls": "",
	}
	if boolSetting(settings, "tls") {
		config["tls"] = "tls"
	}
	if serverName := stringSetting(settings, "tls_settings.server_name"); serverName != "" {
		config["sni"] = serverName
	}
	if fingerprint := tlsFingerprint(settings, selectFingerprint); fingerprint != "" {
		config["fp"] = fingerprint
	}
	network := stringSetting(settings, "network")
	switch network {
	case "tcp":
		headerType := stringSetting(settings, "network_settings.header.type")
		if headerType != "" && headerType != "none" {
			config["type"] = headerType
			if paths := stringsSetting(settings, "network_settings.header.request.path"); len(paths) > 0 {
				config["path"] = paths[0]
			}
			if hosts := stringsSetting(settings, "network_settings.header.request.headers.Host"); len(hosts) > 0 {
				config["host"] = hosts[0]
			} else {
				config["host"] = nil
			}
		}
	case "ws":
		config["type"] = "ws"
		setNonEmpty(config, "path", stringSetting(settings, "network_settings.path"))
		setNonEmpty(config, "host", stringSetting(settings, "network_settings.headers.Host"))
	case "grpc":
		config["type"] = "grpc"
		setNonEmpty(config, "path", stringSetting(settings, "network_settings.serviceName"))
	case "h2":
		config["net"], config["type"] = "h2", "h2"
		setNonEmpty(config, "path", stringSetting(settings, "network_settings.path"))
		if hosts := stringsSetting(settings, "network_settings.host"); len(hosts) > 0 {
			config["host"] = strings.Join(hosts, ",")
		}
	case "httpupgrade", "xhttp":
		config["net"], config["type"] = network, network
		setNonEmpty(config, "path", stringSetting(settings, "network_settings.path"))
		host := stringSetting(settings, "network_settings.host")
		if host == "" {
			host = node.Host
		}
		config["host"] = host
		if network == "xhttp" {
			mode := stringSetting(settings, "network_settings.mode")
			if mode == "" {
				mode = "auto"
			}
			config["mode"] = mode
			if extra := nested(settings, "network_settings.extra"); extra != nil {
				if encoded, err := json.Marshal(extra); err == nil && string(encoded) != "{}" {
					config["extra"] = string(encoded)
				}
			}
		}
	}
	payload, err := json.Marshal(config)
	if err != nil {
		return "", fmt.Errorf("marshal VMess subscription node %d: %w", node.ID, err)
	}
	return "vmess://" + base64.StdEncoding.EncodeToString(payload) + "\r\n", nil
}

func generalVLESS(node PreparedNode, selectFingerprint func() string) string {
	settings := node.ProtocolSettings
	encryption := "none"
	if boolSetting(settings, "encryption.enabled") {
		if configured := stringSetting(settings, "encryption.encryption"); configured != "" {
			encryption = configured
		}
	}
	values := []queryValue{{"mode", "multi"}, {"security", ""}, {"encryption", encryption}, {"type", nil}, {"flow", nil}}
	if network := stringSetting(settings, "network"); network != "" {
		values[3].value = network
	}
	if flow := stringSetting(settings, "flow"); flow != "" {
		values[4].value = flow
	}
	switch numberValue(nested(settings, "tls")) {
	case 1:
		values[1].value = "tls"
		if fingerprint := tlsFingerprint(settings, selectFingerprint); fingerprint != "" {
			values = append(values, queryValue{"fp", fingerprint})
		}
		if serverName := stringSetting(settings, "tls_settings.server_name"); serverName != "" {
			values = append(values, queryValue{"sni", serverName})
		}
		if boolSetting(settings, "tls_settings.allow_insecure") {
			values = append(values, queryValue{"allowInsecure", "1"})
		}
	case 2:
		values[1].value = "reality"
		values = append(values,
			queryValue{"pbk", nilIfEmpty(stringSetting(settings, "reality_settings.public_key"))},
			queryValue{"sid", nilIfEmpty(stringSetting(settings, "reality_settings.short_id"))},
			queryValue{"sni", nilIfEmpty(stringSetting(settings, "reality_settings.server_name"))},
			queryValue{"servername", nilIfEmpty(stringSetting(settings, "reality_settings.server_name"))},
			queryValue{"spx", "/"},
		)
		if fingerprint := tlsFingerprint(settings, selectFingerprint); fingerprint != "" {
			values = append(values, queryValue{"fp", fingerprint})
		}
	}
	values = append(values, transportQueryValues(settings, node.Host, true)...)
	return fmt.Sprintf("vless://%s@%s:%d?%s#%s\r\n", node.Password, wrapAddress(node.Host), node.Port, buildQuery(values...), rawURLEncodeQueryStyle(node.Name))
}

func generalTrojan(node PreparedNode, selectFingerprint func() string) string {
	settings := node.ProtocolSettings
	values := make([]queryValue, 0, 12)
	if numberValue(nested(settings, "tls")) == 2 {
		values = append(values,
			queryValue{"security", "reality"},
			queryValue{"pbk", nilIfEmpty(stringSetting(settings, "reality_settings.public_key"))},
			queryValue{"sid", nilIfEmpty(stringSetting(settings, "reality_settings.short_id"))},
			queryValue{"sni", nilIfEmpty(stringSetting(settings, "reality_settings.server_name"))},
		)
	} else {
		values = append(values, queryValue{"allowInsecure", boolSetting(settings, "tls_settings.allow_insecure")})
		if serverName := stringSetting(settings, "tls_settings.server_name"); serverName != "" {
			values = append(values, queryValue{"peer", serverName}, queryValue{"sni", serverName})
		}
	}
	if fingerprint := tlsFingerprint(settings, selectFingerprint); fingerprint != "" {
		values = append(values, queryValue{"fp", fingerprint})
	}
	values = append(values, transportQueryValues(settings, node.Host, false)...)
	return fmt.Sprintf("trojan://%s@%s:%d?%s#%s\r\n", node.Password, wrapAddress(node.Host), node.Port, buildQuery(values...), rawURLEncode(node.Name))
}

func generalHysteria(node PreparedNode) string {
	settings := node.ProtocolSettings
	values := []queryValue{}
	if serverName := stringSetting(settings, "tls.server_name"); serverName != "" {
		values = append(values, queryValue{"sni", serverName})
	}
	values = append(values, queryValue{"insecure", boolSetting(settings, "tls.allow_insecure")})
	name := rawURLEncode(node.Name)
	if numberValue(nested(settings, "version")) == 2 {
		if boolSetting(settings, "obfs.open") {
			values = append(values, queryValue{"obfs", "salamander"}, queryValue{"obfs-password", stringSetting(settings, "obfs.password")})
		}
		if node.Ports != "" {
			values = append(values, queryValue{"mport", node.Ports})
		}
		return fmt.Sprintf("hysteria2://%s@%s:%d?%s#%s\r\n", node.Password, wrapAddress(node.Host), node.Port, buildQuery(values...), name)
	}
	values = append(values, queryValue{"protocol", "udp"}, queryValue{"auth", node.Password})
	if up := nested(settings, "bandwidth.up"); numberValue(up) != 0 {
		values = append(values, queryValue{"upmbps", up})
	}
	if down := nested(settings, "bandwidth.down"); numberValue(down) != 0 {
		values = append(values, queryValue{"downmbps", down})
	}
	if boolSetting(settings, "obfs.open") && stringSetting(settings, "obfs.password") != "" {
		values = append(values, queryValue{"obfs", "xplus"}, queryValue{"obfsParam", stringSetting(settings, "obfs.password")})
	}
	return fmt.Sprintf("hysteria://%s:%d?%s#%s\r\n", wrapAddress(node.Host), node.Port, buildQuery(values...), name)
}

func generalTUIC(node PreparedNode) string {
	settings := node.ProtocolSettings
	values := []queryValue{}
	if serverName := stringSetting(settings, "tls.server_name"); serverName != "" {
		values = append(values, queryValue{"sni", serverName})
	}
	alpn := stringsSetting(settings, "alpn")
	if len(alpn) == 0 {
		alpn = []string{"h3"}
	}
	values = append(values, queryValue{"alpn", strings.Join(alpn, ",")})
	congestion := stringSetting(settings, "congestion_control")
	if congestion == "" {
		congestion = "cubic"
	}
	udpRelay := stringSetting(settings, "udp_relay_mode")
	if udpRelay == "" {
		udpRelay = "native"
	}
	values = append(values, queryValue{"congestion_control", congestion}, queryValue{"udp-relay-mode", udpRelay})
	if boolSetting(settings, "tls.allow_insecure") {
		values = append(values, queryValue{"insecure", "1"})
	}
	return fmt.Sprintf("tuic://%s:%s@%s:%d?%s#%s\r\n", node.Password, node.Password, wrapAddress(node.Host), node.Port, buildQuery(values...), rawURLEncode(node.Name))
}

func generalAnyTLS(node PreparedNode) string {
	query := buildQuery(
		queryValue{"sni", nilIfEmpty(stringSetting(node.ProtocolSettings, "tls.server_name"))},
		queryValue{"insecure", boolSetting(node.ProtocolSettings, "tls.allow_insecure")},
	)
	return fmt.Sprintf("anytls://%s@%s:%d?%s#%s\r\n", node.Password, wrapAddress(node.Host), node.Port, query, rawURLEncode(node.Name))
}

func generalSOCKS(node PreparedNode) string {
	credentials := base64.StdEncoding.EncodeToString([]byte(node.Password + ":" + node.Password))
	return fmt.Sprintf("socks://%s@%s:%d#%s\r\n", credentials, wrapAddress(node.Host), node.Port, rawURLEncode(node.Name))
}

func generalHTTP(node PreparedNode) string {
	credentials := base64.StdEncoding.EncodeToString([]byte(node.Password + ":" + node.Password))
	result := fmt.Sprintf("http://%s@%s:%d", credentials, wrapAddress(node.Host), node.Port)
	if boolSetting(node.ProtocolSettings, "tls") {
		query := buildQuery(
			queryValue{"security", "tls"},
			queryValue{"sni", nilIfEmpty(stringSetting(node.ProtocolSettings, "tls_settings.server_name"))},
			queryValue{"allowInsecure", boolSetting(node.ProtocolSettings, "tls_settings.allow_insecure")},
		)
		result += "?" + query
	}
	return result + "#" + rawURLEncode(node.Name) + "\r\n"
}

func transportQueryValues(settings map[string]any, fallbackHost string, vless bool) []queryValue {
	network := stringSetting(settings, "network")
	values := []queryValue{}
	switch network {
	case "ws":
		values = append(values, queryValue{"type", "ws"}, queryValue{"path", nilIfEmpty(stringSetting(settings, "network_settings.path"))}, queryValue{"host", nilIfEmpty(stringSetting(settings, "network_settings.headers.Host"))})
	case "grpc":
		key := "serviceName"
		values = append(values, queryValue{"type", "grpc"}, queryValue{key, nilIfEmpty(stringSetting(settings, "network_settings.serviceName"))})
	case "h2":
		hosts := stringsSetting(settings, "network_settings.host")
		values = append(values, queryValue{"type", "http"}, queryValue{"path", nilIfEmpty(stringSetting(settings, "network_settings.path"))})
		if len(hosts) > 0 {
			values = append(values, queryValue{"host", strings.Join(hosts, ",")})
		}
	case "kcp":
		if vless {
			values = append(values, queryValue{"path", nilIfEmpty(stringSetting(settings, "network_settings.seed"))})
			header := stringSetting(settings, "network_settings.header.type")
			if header == "" {
				header = "none"
			}
			values = append(values, queryValue{"type", header})
		}
	case "httpupgrade", "xhttp":
		values = append(values, queryValue{"type", network}, queryValue{"path", nilIfEmpty(stringSetting(settings, "network_settings.path"))})
		host := stringSetting(settings, "network_settings.host")
		if host == "" {
			host = fallbackHost
		}
		values = append(values, queryValue{"host", host})
		if network == "xhttp" {
			mode := stringSetting(settings, "network_settings.mode")
			if mode == "" {
				mode = "auto"
			}
			values = append(values, queryValue{"mode", mode})
			if extra := nested(settings, "network_settings.extra"); extra != nil {
				if encoded, err := json.Marshal(extra); err == nil && string(encoded) != "{}" {
					values = append(values, queryValue{"extra", string(encoded)})
				}
			}
		}
	}
	return values
}

func setNonEmpty(target map[string]any, key, value string) {
	if value != "" {
		target[key] = value
	}
}

func nilIfEmpty(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func rawURLEncodeQueryStyle(value string) string {
	return strings.ReplaceAll(rawURLEncode(value), "%20", "+")
}
