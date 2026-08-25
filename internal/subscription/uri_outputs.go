package subscription

import (
	"encoding/base64"
	"fmt"
	"strconv"
	"strings"
)

func renderURIClient(input RenderInput) (Response, error) {
	var body strings.Builder
	body.Grow(len(input.Nodes) * 180)
	switch input.Client.Kind {
	case KindShadowrocket:
		body.WriteString(shadowrocketStatus(input))
		for _, node := range input.Nodes {
			body.WriteString(shadowrocketLine(node))
		}
		return Response{Body: []byte(base64.StdEncoding.EncodeToString([]byte(body.String()))), ContentType: "text/plain; charset=utf-8", Headers: map[string]string{}}, nil
	case KindQuantumultX:
		for _, node := range input.Nodes {
			body.WriteString(quantumultXLine(node))
		}
		return Response{
			Body: []byte(base64.StdEncoding.EncodeToString([]byte(body.String()))), ContentType: "text/plain; charset=utf-8",
			Headers: map[string]string{"subscription-userinfo": subscriptionUserInfo(input.Account)},
		}, nil
	case KindLoon:
		for _, node := range input.Nodes {
			body.WriteString(loonLine(node))
		}
		return Response{
			Body: []byte(body.String()), ContentType: "text/plain; charset=utf-8",
			Headers: map[string]string{"subscription-userinfo": subscriptionUserInfo(input.Account)},
		}, nil
	default:
		return Response{}, fmt.Errorf("unsupported URI subscription output %q", input.Client.Kind)
	}
}

func shadowrocketStatus(input RenderInput) string {
	expiry := "N/A"
	if input.Account.ExpiredAt != nil {
		expiry = input.Account.ExpiredAt.In(legacyLocation).Format("2006-01-02")
	}
	return fmt.Sprintf("STATUS=🚀↑:%sGB,↓:%sGB,TOT:%sGB💡Expires:%s\r\n",
		roundedGiB(input.Account.TrafficUpload), roundedGiB(input.Account.TrafficDownload), roundedGiB(input.Account.TransferEnable), expiry)
}

func shadowrocketLine(node PreparedNode) string {
	settings := node.ProtocolSettings
	switch node.Type {
	case "shadowsocks":
		return generalShadowsocks(node)
	case "vmess", "vless":
		credentials := base64.StdEncoding.EncodeToString([]byte("auto:" + node.Password + "@" + wrapAddress(node.Host) + ":" + strconv.Itoa(node.Port)))
		values := []queryValue{{"tfo", "1"}, {"remark", node.Name}}
		if node.Type == "vmess" {
			values = append(values, queryValue{"alterId", "0"})
		} else if flow := stringSetting(settings, "flow"); flow != "" {
			values = append(values, queryValue{"flow", flow})
		}
		mode := numberValue(nested(settings, "tls"))
		if mode != 0 {
			values = append(values, queryValue{"tls", "1"})
			if node.Type == "vless" && stringSetting(settings, "flow") != "" {
				values = append(values, queryValue{"xtls", "2"})
			}
			values = append(values, queryValue{"allowInsecure", boolSetting(settings, "tls_settings.allow_insecure")})
			if peer := stringSetting(settings, "tls_settings.server_name"); peer != "" {
				values = append(values, queryValue{"peer", peer})
			}
		}
		appendShadowrocketTransport(&values, settings)
		return node.Type + "://" + credentials + "?" + buildQuery(values...) + "\r\n"
	case "trojan":
		return generalTrojan(node, randomFingerprint)
	case "hysteria":
		values := []queryValue{{"fastopen", "1"}}
		if sni := stringSetting(settings, "tls.server_name"); sni != "" {
			values = append(values, queryValue{"peer", sni})
		}
		if boolSetting(settings, "obfs.open") {
			values = append([]queryValue{{"obfs", defaultString(stringSetting(settings, "obfs.type"), "salamander")}}, values...)
			values = append(values, queryValue{"obfs-password", stringSetting(settings, "obfs.password")})
		}
		values = append(values, queryValue{"insecure", boolSetting(settings, "tls.allow_insecure")})
		return fmt.Sprintf("hysteria2://%s@%s:%d?%s#%s\r\n", node.Password, wrapAddress(node.Host), node.Port, buildQuery(values...), node.Name)
	case "tuic":
		values := []queryValue{}
		alpn := stringsSetting(settings, "alpn")
		if len(alpn) == 0 {
			alpn = []string{"h3"}
		}
		for index, value := range alpn {
			values = append(values, queryValue{fmt.Sprintf("alpn[%d]", index), value})
		}
		values = append(values,
			queryValue{"sni", nilIfEmpty(stringSetting(settings, "tls.server_name"))},
			queryValue{"insecure", boolSetting(settings, "tls.allow_insecure")},
			queryValue{"congestion_control", defaultString(stringSetting(settings, "congestion_control"), "cubic")},
			queryValue{"uuid", node.Password}, queryValue{"password", node.Password},
		)
		return fmt.Sprintf("tuic://%s:%d?%s#%s\r\n", wrapAddress(node.Host), node.Port, buildQuery(values...), rawURLEncode(node.Name))
	case "anytls":
		return generalAnyTLS(node)
	case "socks":
		credentials := base64.StdEncoding.EncodeToString([]byte(node.Password + ":" + node.Password + "@" + wrapAddress(node.Host) + ":" + strconv.Itoa(node.Port)))
		return "socks://" + credentials + "?method=auto#" + rawURLEncode(node.Name) + "\r\n"
	default:
		return ""
	}
}

func appendShadowrocketTransport(values *[]queryValue, settings map[string]any) {
	switch stringSetting(settings, "network") {
	case "ws":
		*values = append(*values, queryValue{"obfs", "websocket"}, queryValue{"path", nilIfEmpty(stringSetting(settings, "network_settings.path"))}, queryValue{"obfsParam", nilIfEmpty(stringSetting(settings, "network_settings.headers.Host"))})
	case "grpc":
		*values = append(*values, queryValue{"obfs", "grpc"}, queryValue{"serviceName", nilIfEmpty(stringSetting(settings, "network_settings.serviceName"))})
	case "h2":
		*values = append(*values, queryValue{"obfs", "h2"}, queryValue{"path", nilIfEmpty(stringSetting(settings, "network_settings.path"))})
	case "httpupgrade":
		*values = append(*values, queryValue{"obfs", "httpupgrade"}, queryValue{"path", nilIfEmpty(stringSetting(settings, "network_settings.path"))}, queryValue{"obfsParam", nilIfEmpty(stringSetting(settings, "network_settings.host"))})
	}
}

func quantumultXLine(node PreparedNode) string {
	settings := node.ProtocolSettings
	parts := []string{}
	switch node.Type {
	case "shadowsocks":
		parts = []string{"shadowsocks=" + node.Host + ":" + strconv.Itoa(node.Port), "method=" + stringSetting(settings, "cipher"), "password=" + node.Password, "fast-open=true", "udp-relay=true", "tag=" + node.Name}
	case "vmess":
		parts = []string{"vmess=" + node.Host + ":" + strconv.Itoa(node.Port), "method=auto", "password=" + node.Password}
		appendQuantumultTLS(&parts, settings, "tls_settings", "obfs-host")
		appendQuantumultTransport(&parts, settings)
		parts = append(parts, "fast-open=true", "udp-relay=true", "tag="+node.Name)
	case "trojan":
		parts = []string{"trojan=" + node.Host + ":" + strconv.Itoa(node.Port), "password=" + node.Password}
		prefix := "tls_settings"
		if numberValue(nested(settings, "tls")) == 2 {
			prefix = "reality_settings"
		}
		if host := stringSetting(settings, prefix+".server_name"); host != "" {
			parts = append(parts, "tls-host="+host)
		}
		parts = append(parts, "fast-open=true", "udp-relay=true", "tag="+node.Name)
	case "vless":
		parts = []string{"vless=" + node.Host + ":" + strconv.Itoa(node.Port), "method=none", "password=" + node.Password}
		appendQuantumultTLS(&parts, settings, "tls_settings", "obfs-host")
		if flow := stringSetting(settings, "flow"); flow != "" {
			parts = append(parts, "vless-flow="+flow)
		}
		appendQuantumultTransport(&parts, settings)
		parts = append(parts, "fast-open=true", "udp-relay=true", "tag="+node.Name)
	case "anytls":
		parts = []string{"anytls=" + node.Host + ":" + strconv.Itoa(node.Port), "password=" + node.Password, "udp-relay=true", "tag=" + node.Name, "over-tls=true"}
		parts = append(parts, "tls-verification="+strconv.FormatBool(!boolSetting(settings, "tls.allow_insecure")))
		if host := stringSetting(settings, "tls.server_name"); host != "" {
			parts = append(parts, "tls-host="+host)
		}
	case "socks":
		parts = []string{"socks5=" + node.Host + ":" + strconv.Itoa(node.Port), "username=" + node.Password, "password=" + node.Password, "fast-open=true", "udp-relay=true", "tag=" + node.Name}
	case "http":
		parts = []string{"http=" + node.Host + ":" + strconv.Itoa(node.Port), "username=" + node.Password, "password=" + node.Password}
		if boolSetting(settings, "tls") {
			parts = append(parts, "over-tls=true", "tls-verification="+strconv.FormatBool(!boolSetting(settings, "tls_settings.allow_insecure")))
			if host := stringSetting(settings, "tls_settings.server_name"); host != "" {
				parts = append(parts, "tls-host="+host)
			}
		}
		parts = append(parts, "fast-open=true", "tag="+node.Name)
	default:
		return ""
	}
	return strings.Join(nonEmpty(parts), ",") + "\r\n"
}

func appendQuantumultTLS(parts *[]string, settings map[string]any, prefix, hostKey string) {
	if !boolSetting(settings, "tls") {
		return
	}
	*parts = append(*parts, "tls-verification="+strconv.FormatBool(!boolSetting(settings, prefix+".allow_insecure")))
	if host := stringSetting(settings, prefix+".server_name"); host != "" {
		*parts = append(*parts, hostKey+"="+host)
	}
}

func appendQuantumultTransport(parts *[]string, settings map[string]any) {
	if stringSetting(settings, "network") != "ws" {
		return
	}
	*parts = append(*parts, "obfs=wss")
	if path := stringSetting(settings, "network_settings.path"); path != "" {
		*parts = append(*parts, "obfs-uri="+path)
	}
	if host := stringSetting(settings, "network_settings.headers.Host"); host != "" {
		*parts = append(*parts, "obfs-host="+host)
	}
}

func loonLine(node PreparedNode) string {
	settings := node.ProtocolSettings
	parts := []string{}
	switch node.Type {
	case "shadowsocks":
		parts = []string{node.Name + "=Shadowsocks", node.Host, strconv.Itoa(node.Port), stringSetting(settings, "cipher"), node.Password, "fast-open=false", "udp=true"}
		if stringSetting(settings, "plugin") == "obfs" {
			options := parsePluginOptions(stringSetting(settings, "plugin_opts"))
			if value, ok := options["obfs"].(string); ok {
				parts = append(parts, "obfs-name="+value)
			}
			if value, ok := options["obfs-host"].(string); ok {
				parts = append(parts, "obfs-host="+value)
			}
		}
	case "vmess":
		parts = []string{node.Name + "=vmess", node.Host, strconv.Itoa(node.Port), "auto", node.Password, "fast-open=false", "udp=true", "alterId=0"}
		appendLoonTLS(&parts, settings, "tls_settings")
		appendLoonTransport(&parts, settings)
	case "vless":
		parts = []string{node.Name + "=VLESS", node.Host, strconv.Itoa(node.Port), node.Password, "alterId=0", "udp=true"}
		if flow := stringSetting(settings, "flow"); flow != "" {
			parts = append(parts, "flow="+flow)
		}
		appendLoonVlessTLS(&parts, settings)
		network := stringSetting(settings, "network")
		if network == "" {
			network = "tcp"
		}
		parts = append(parts, "transport="+network)
		appendLoonTransport(&parts, settings)
	case "trojan":
		parts = []string{node.Name + "=trojan", node.Host, strconv.Itoa(node.Port), node.Password, "fast-open=false", "udp=true"}
		appendLoonTLS(&parts, settings, "tls_settings")
	case "hysteria":
		if numberValue(nested(settings, "version")) != 2 {
			return ""
		}
		parts = []string{node.Name + "=Hysteria2", node.Host, strconv.Itoa(node.Port), node.Password}
		if sni := stringSetting(settings, "tls.server_name"); sni != "" {
			parts = append(parts, "sni="+sni)
		}
		if down := numberValue(nested(settings, "bandwidth.down")); down != 0 {
			parts = append(parts, fmt.Sprintf("download-bandwidth=%d", down))
		}
		parts = append(parts, "udp=true")
	case "anytls":
		parts = []string{node.Name + "=anytls", node.Host, strconv.Itoa(node.Port), node.Password, "udp=true"}
		if sni := stringSetting(settings, "tls.server_name"); sni != "" {
			parts = append(parts, "sni="+sni)
		}
	default:
		return ""
	}
	return strings.Join(nonEmpty(parts), ",") + "\r\n"
}

func appendLoonVlessTLS(parts *[]string, settings map[string]any) {
	switch numberValue(nested(settings, "tls")) {
	case 1:
		*parts = append(*parts, "over-tls=true", "skip-cert-verify="+strconv.FormatBool(boolSetting(settings, "tls_settings.allow_insecure")))
		if name := stringSetting(settings, "tls_settings.server_name"); name != "" {
			*parts = append(*parts, "sni="+name)
		}
	case 2:
		*parts = append(*parts, "over-tls=true", "skip-cert-verify="+strconv.FormatBool(boolSetting(settings, "reality_settings.allow_insecure")))
		if name := stringSetting(settings, "reality_settings.server_name"); name != "" {
			*parts = append(*parts, "sni="+name)
		}
		if publicKey := stringSetting(settings, "reality_settings.public_key"); publicKey != "" {
			*parts = append(*parts, "public-key="+publicKey)
		}
		if shortID := stringSetting(settings, "reality_settings.short_id"); shortID != "" {
			*parts = append(*parts, "short-id="+shortID)
		}
	default:
		*parts = append(*parts, "over-tls=false")
	}
}

func appendLoonTLS(parts *[]string, settings map[string]any, prefix string) {
	if !boolSetting(settings, "tls") {
		return
	}
	*parts = append(*parts, "over-tls=true", "skip-cert-verify="+strconv.FormatBool(boolSetting(settings, prefix+".allow_insecure")))
	if name := stringSetting(settings, prefix+".server_name"); name != "" {
		*parts = append(*parts, "tls-name="+name)
	}
}

func appendLoonTransport(parts *[]string, settings map[string]any) {
	if stringSetting(settings, "network") != "ws" {
		return
	}
	*parts = append(*parts, "transport=ws")
	if path := stringSetting(settings, "network_settings.path"); path != "" {
		*parts = append(*parts, "path="+path)
	}
	if host := stringSetting(settings, "network_settings.headers.Host"); host != "" {
		*parts = append(*parts, "host="+host)
	}
}
