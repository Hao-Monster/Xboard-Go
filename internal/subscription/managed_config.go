package subscription

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

var surgeCiphers = map[string]struct{}{
	"aes-128-gcm": {}, "aes-192-gcm": {}, "aes-256-gcm": {}, "chacha20-ietf-poly1305": {},
	"2022-blake3-aes-128-gcm": {}, "2022-blake3-aes-256-gcm": {},
}

var surfboardCiphers = map[string]struct{}{
	"aes-128-gcm": {}, "aes-192-gcm": {}, "aes-256-gcm": {}, "chacha20-ietf-poly1305": {},
	"2022-blake3-aes-128-gcm": {}, "2022-blake3-aes-256-gcm": {}, "2022-blake3-chacha20-poly1305": {},
}

func renderManagedConfig(input RenderInput) (Response, error) {
	var proxies strings.Builder
	proxyNames := make([]string, 0, len(input.Nodes))
	for _, node := range input.Nodes {
		line, ok := managedProxyLine(node, input.Client.Kind)
		if !ok {
			continue
		}
		proxies.WriteString(line)
		proxyNames = append(proxyNames, node.Name)
	}
	templateName := string(input.Client.Kind)
	config := input.Templates[templateName]
	if strings.TrimSpace(config) == "" {
		config = defaultManagedTemplate(input.Client.Kind, input.AppName)
	}
	expiry := "长期有效"
	if input.Account.ExpiredAt != nil {
		expiry = input.Account.ExpiredAt.In(legacyLocation).Format("2006-01-02 15:04:05")
	}
	upload := roundedGiB(input.Account.TrafficUpload)
	download := roundedGiB(input.Account.TrafficDownload)
	total := roundedGiB(input.Account.TransferEnable)
	info := fmt.Sprintf("title=%s订阅信息, content=上传流量：%sGB\\n下载流量：%sGB\\n剩余流量：%sGB\\n套餐流量：%sGB\\n到期时间：%s",
		input.AppName, upload, download, roundedFloat(parseFloat(total)-parseFloat(upload)-parseFloat(download)), total, expiry)
	replacements := map[string]string{
		"$subs_link": input.SubscriptionURL, "$subs_domain": input.RequestHost, "$proxies": proxies.String(),
		"$proxy_group": strings.Join(proxyNames, ", "), "$subscribe_info": info, "$app_name": input.AppName,
	}
	for token, value := range replacements {
		config = strings.ReplaceAll(config, token, value)
	}
	return Response{
		Body: []byte(config), ContentType: "application/octet-stream",
		Headers: map[string]string{"content-disposition": "attachment; filename*=UTF-8''" + rawURLEncode(input.AppName) + ".conf"},
	}, nil
}

func managedProxyLine(node PreparedNode, kind Kind) (string, bool) {
	settings := node.ProtocolSettings
	delimiter := " = "
	if kind == KindSurfboard {
		delimiter = "="
	}
	parts := []string{}
	switch node.Type {
	case "shadowsocks":
		cipher := stringSetting(settings, "cipher")
		allowed := surgeCiphers
		if kind == KindSurfboard {
			allowed = surfboardCiphers
		}
		if _, exists := allowed[cipher]; !exists {
			return "", false
		}
		parts = []string{node.Name + delimiter + "ss", node.Host, strconv.Itoa(node.Port), "encrypt-method=" + cipher, "password=" + node.Password, "tfo=true", "udp-relay=true"}
		appendObfsManagedOptions(&parts, settings)
	case "vmess":
		parts = []string{node.Name + delimiter + "vmess", node.Host, strconv.Itoa(node.Port), "username=" + node.Password, "vmess-aead=true", "tfo=true", "udp-relay=true"}
		if boolSetting(settings, "tls") {
			parts = append(parts, "tls=true")
			if boolSetting(settings, "tls_settings.allow_insecure") {
				parts = append(parts, "skip-cert-verify=true")
			}
			if sni := stringSetting(settings, "tls_settings.server_name"); sni != "" {
				parts = append(parts, "sni="+sni)
			}
		}
		if stringSetting(settings, "network") == "ws" {
			parts = append(parts, "ws=true")
			if path := stringSetting(settings, "network_settings.path"); path != "" {
				parts = append(parts, "ws-path="+path)
			}
			if host := stringSetting(settings, "network_settings.headers.Host"); host != "" {
				parts = append(parts, "ws-headers=Host:"+host)
			}
		}
	case "trojan":
		parts = []string{node.Name + delimiter + "trojan", node.Host, strconv.Itoa(node.Port), "password=" + node.Password}
		if sni := stringSetting(settings, "tls_settings.server_name"); sni != "" {
			parts = append(parts, "sni="+sni)
		}
		parts = append(parts, "tfo=true", "udp-relay=true")
		if boolSetting(settings, "tls_settings.allow_insecure") {
			parts = append(parts, "skip-cert-verify=true")
		}
	case "hysteria":
		if kind != KindSurge || numberValue(nested(settings, "version")) != 2 {
			return "", false
		}
		parts = []string{node.Name + delimiter + "hysteria2", node.Host, strconv.Itoa(node.Port), "password=" + node.Password}
		if sni := stringSetting(settings, "tls.server_name"); sni != "" {
			parts = append(parts, "sni="+sni)
		}
		parts = append(parts, "udp-relay=true")
		if up := numberValue(nested(settings, "bandwidth.up")); up != 0 {
			parts = append(parts, fmt.Sprintf("upload-bandwidth=%d", up))
		}
		if down := numberValue(nested(settings, "bandwidth.down")); down != 0 {
			parts = append(parts, fmt.Sprintf("download-bandwidth=%d", down))
		}
		if boolSetting(settings, "tls.allow_insecure") {
			parts = append(parts, "skip-cert-verify=true")
		}
	case "anytls":
		parts = []string{node.Name + delimiter + "anytls", node.Host, strconv.Itoa(node.Port), "password=" + node.Password}
		if kind == KindSurfboard {
			parts = append(parts, "tfo=true", "udp-relay=true")
		}
		if sni := stringSetting(settings, "tls.server_name"); sni != "" {
			parts = append(parts, "sni="+sni)
		}
		if boolSetting(settings, "tls.allow_insecure") {
			parts = append(parts, "skip-cert-verify=true")
		}
	case "socks":
		if kind != KindSurge {
			return "", false
		}
		typeName := "socks5"
		if boolSetting(settings, "tls") {
			typeName = "socks5-tls"
		}
		parts = []string{node.Name + delimiter + typeName, node.Host, strconv.Itoa(node.Port), node.Password, node.Password}
		appendManagedTLSOptions(&parts, settings)
		parts = append(parts, "udp-relay=true")
	case "http":
		if kind != KindSurge {
			return "", false
		}
		typeName := "http"
		if boolSetting(settings, "tls") {
			typeName = "https"
		}
		parts = []string{node.Name + delimiter + typeName, node.Host, strconv.Itoa(node.Port), node.Password, node.Password}
		appendManagedTLSOptions(&parts, settings)
	default:
		return "", false
	}
	return strings.Join(nonEmpty(parts), ",") + "\r\n", true
}

func appendObfsManagedOptions(parts *[]string, settings map[string]any) {
	if stringSetting(settings, "plugin") != "obfs" || stringSetting(settings, "plugin_opts") == "" {
		return
	}
	options := parsePluginOptions(stringSetting(settings, "plugin_opts"))
	if value, ok := options["obfs"].(string); ok && value != "" {
		*parts = append(*parts, "obfs="+value)
	}
	if value, ok := options["obfs-host"].(string); ok && value != "" {
		*parts = append(*parts, "obfs-host="+value)
	}
	if value, ok := options["path"].(string); ok && value != "" {
		*parts = append(*parts, "obfs-uri="+value)
	}
}

func appendManagedTLSOptions(parts *[]string, settings map[string]any) {
	if !boolSetting(settings, "tls") {
		return
	}
	if sni := stringSetting(settings, "tls_settings.server_name"); sni != "" {
		*parts = append(*parts, "sni="+sni)
	}
	if boolSetting(settings, "tls_settings.allow_insecure") {
		*parts = append(*parts, "skip-cert-verify=true")
	}
}

func defaultManagedTemplate(kind Kind, appName string) string {
	if appName == "" {
		appName = "Xboard"
	}
	if kind == KindSurfboard {
		return "[General]\nloglevel = notify\n\n[Proxy]\n$proxies\n[Proxy Group]\n$app_name = select,$proxy_group\n\n[Rule]\nFINAL,$app_name\n"
	}
	return "[General]\nloglevel = notify\n\n[Proxy]\n$proxies\n[Proxy Group]\n$app_name = select,$proxy_group\n\n[Panel]\nSubscription = $subscribe_info\n\n[Rule]\nFINAL,$app_name\n"
}

func roundedGiB(bytes int64) string {
	return roundedFloat(float64(bytes) / float64(int64(1)<<30))
}

func roundedFloat(value float64) string {
	return strconv.FormatFloat(math.Round(value*100)/100, 'f', -1, 64)
}

func parseFloat(value string) float64 {
	parsed, _ := strconv.ParseFloat(value, 64)
	return parsed
}

func nonEmpty(values []string) []string {
	result := values[:0]
	for _, value := range values {
		if value != "" {
			result = append(result, value)
		}
	}
	return result
}
