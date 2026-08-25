package subscription

import (
	"crypto/md5"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"math/big"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Hao-Monster/Xboard-Go/internal/store"
)

var legacyLocation = time.FixedZone("Asia/Shanghai", 8*60*60)

type Kind string

const (
	KindGeneral      Kind = "general"
	KindShadowsocks  Kind = "shadowsocks"
	KindClash        Kind = "clash"
	KindClashMeta    Kind = "clashmeta"
	KindSingBox      Kind = "singbox"
	KindSurge        Kind = "surge"
	KindStash        Kind = "stash"
	KindSurfboard    Kind = "surfboard"
	KindShadowrocket Kind = "shadowrocket"
	KindQuantumultX  Kind = "quantumultx"
	KindLoon         Kind = "loon"
)

func (kind Kind) String() string { return string(kind) }

type clientDefinition struct {
	Kind  Kind
	Flags []string
}

// The order mirrors ProtocolManager's reverse alphabetical class discovery.
var clientDefinitions = []clientDefinition{
	{Kind: KindSurge, Flags: []string{"surge"}},
	{Kind: KindSurfboard, Flags: []string{"surfboard"}},
	{Kind: KindStash, Flags: []string{"stash"}},
	{Kind: KindSingBox, Flags: []string{"sing-box", "hiddify", "sfm", "karing"}},
	{Kind: KindShadowsocks, Flags: []string{"shadowsocks"}},
	{Kind: KindShadowrocket, Flags: []string{"shadowrocket"}},
	{Kind: KindQuantumultX, Flags: []string{"quantumult%20x", "quantumult-x"}},
	{Kind: KindLoon, Flags: []string{"loon"}},
	{Kind: KindGeneral, Flags: []string{"general", "v2rayn", "v2rayng", "passwall", "ssrplus", "sagernet"}},
	{Kind: KindClashMeta, Flags: []string{"meta", "verge", "flclash", "nekobox", "clashmetaforandroid"}},
	{Kind: KindClash, Flags: []string{"clash"}},
}

var namedVersionPattern = regexp.MustCompile(`([a-zA-Z0-9_-]+)[/\s]+(v?[0-9]+(?:\.[0-9]+){0,2})`)
var slashVersionPattern = regexp.MustCompile(`/v?([0-9]+(?:\.[0-9]+){0,2})`)
var knownClientFlags, sortedClientFlags, clientVersionPatterns = buildClientFlagIndex()

type ClientInfo struct {
	Kind    Kind
	Flag    string
	Name    string
	Version string
}

func DetectClient(queryFlag, userAgent string) ClientInfo {
	flag := strings.ToLower(queryFlag)
	if queryFlag == "" {
		flag = strings.ToLower(userAgent)
	}
	info := ClientInfo{Kind: KindGeneral, Flag: flag}
	if match := namedVersionPattern.FindStringSubmatch(flag); len(match) == 3 {
		potentialName := strings.ToLower(match[1])
		if _, exists := knownClientFlags[potentialName]; exists {
			info.Name = potentialName
			info.Version = strings.TrimPrefix(match[2], "v")
		}
	}
	if info.Name == "" {
		for _, name := range sortedClientFlags {
			if strings.Contains(flag, name) {
				info.Name = name
				if info.Version == "" {
					if match := clientVersionPatterns[name].FindStringSubmatch(flag); len(match) == 2 {
						info.Version = strings.TrimPrefix(match[1], "v")
					}
				}
				break
			}
		}
	}
	if info.Version == "" {
		if match := slashVersionPattern.FindStringSubmatch(flag); len(match) == 2 {
			info.Version = match[1]
		}
	}
	for _, definition := range clientDefinitions {
		for _, candidate := range definition.Flags {
			if strings.Contains(flag, candidate) {
				info.Kind = definition.Kind
				return info
			}
		}
	}
	return info
}

func buildClientFlagIndex() (map[string]struct{}, []string, map[string]*regexp.Regexp) {
	flags := make(map[string]struct{})
	for _, definition := range clientDefinitions {
		for _, flag := range definition.Flags {
			flags[flag] = struct{}{}
		}
	}
	sorted := make([]string, 0, len(flags))
	patterns := make(map[string]*regexp.Regexp, len(flags))
	for flag := range flags {
		sorted = append(sorted, flag)
		patterns[flag] = regexp.MustCompile(regexp.QuoteMeta(flag) + `[/\s]+(v?[0-9]+(?:\.[0-9]+){0,2})`)
	}
	sort.Slice(sorted, func(i, j int) bool {
		if len(sorted[i]) == len(sorted[j]) {
			return sorted[i] < sorted[j]
		}
		return len(sorted[i]) > len(sorted[j])
	})
	return flags, sorted, patterns
}

var validNodeTypes = map[string]struct{}{
	"hysteria": {}, "vless": {}, "trojan": {}, "vmess": {}, "tuic": {}, "shadowsocks": {},
	"anytls": {}, "socks": {}, "naive": {}, "http": {}, "mieru": {},
}

type PreparedNode struct {
	ID                int64
	Type              string
	ExternalCode      string
	Name              string
	Tags              []string
	Host              string
	Port              int
	Ports             string
	ServerPort        int
	Password          string
	ProtocolSettings  map[string]any
	RawSettings       json.RawMessage
	ConfiguredRate    float64
	CreatedAt         time.Time
	ParentCreatedAt   *time.Time
	CustomOutbounds   json.RawMessage
	CustomRoutes      json.RawMessage
	CertificateConfig json.RawMessage
}

func FilterNodes(nodes []PreparedNode, typeInput, filterInput string) []PreparedNode {
	allowedTypes := parseRequestedTypes(typeInput)
	keywords := parseFilterKeywords(filterInput)
	result := make([]PreparedNode, 0, len(nodes))
	for _, node := range nodes {
		if len(allowedTypes) > 0 {
			if _, allowed := allowedTypes[node.Type]; !allowed {
				continue
			}
		}
		if len(keywords) > 0 {
			matched := false
			for _, keyword := range keywords {
				if strings.Contains(strings.ToLower(node.Name), strings.ToLower(keyword)) || containsExact(node.Tags, keyword) {
					matched = true
					break
				}
			}
			if !matched {
				continue
			}
		}
		result = append(result, node)
	}
	return result
}

func parseRequestedTypes(input string) map[string]struct{} {
	if strings.TrimSpace(input) == "" || input == "all" {
		result := make(map[string]struct{}, len(validNodeTypes))
		for value := range validNodeTypes {
			result[value] = struct{}{}
		}
		return result
	}
	result := make(map[string]struct{})
	for _, value := range splitFilterValues(input) {
		if _, valid := validNodeTypes[value]; valid {
			result[value] = struct{}{}
		}
	}
	return result
}

func parseFilterKeywords(input string) []string {
	if strings.TrimSpace(input) == "" || utf8.RuneCountInString(input) > 20 {
		return nil
	}
	return splitFilterValues(input)
}

func splitFilterValues(input string) []string {
	fields := strings.FieldsFunc(input, func(r rune) bool { return r == '|' || r == ',' || r == '｜' })
	result := make([]string, 0, len(fields))
	for _, field := range fields {
		if value := strings.TrimSpace(field); value != "" {
			result = append(result, value)
		}
	}
	return result
}

func containsExact(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}

type PrepareOptions struct {
	ShowInfo                bool
	ShowProtocol            bool
	RejectedByRequestFilter int
	NextResetAt             *time.Time
	Now                     time.Time
	Location                *time.Location
	SelectPort              func(minimum, maximum int) (int, error)
}

func PrepareNodes(account store.SubscriptionAccount, source []store.SubscriptionNode, options PrepareOptions) ([]PreparedNode, error) {
	if options.Now.IsZero() {
		options.Now = time.Now()
	}
	if options.Location == nil {
		options.Location = legacyLocation
	}
	if options.SelectPort == nil {
		options.SelectPort = securePort
	}
	nodes := make([]PreparedNode, 0, len(source)+4)
	for _, sourceNode := range source {
		var settings map[string]any
		if err := json.Unmarshal(sourceNode.ProtocolSettings, &settings); err != nil {
			return nil, fmt.Errorf("decode protocol settings for node %d: %w", sourceNode.ID, err)
		}
		port, ports, err := selectNodePort(sourceNode.Port, options.SelectPort)
		if err != nil {
			return nil, fmt.Errorf("select port for node %d: %w", sourceNode.ID, err)
		}
		node := PreparedNode{
			ID: sourceNode.ID, Type: sourceNode.Type, ExternalCode: sourceNode.ExternalCode, Name: sourceNode.Name,
			Tags: append([]string(nil), sourceNode.Tags...), Host: sourceNode.Host, Port: port, Ports: ports,
			ServerPort: sourceNode.ServerPort, ProtocolSettings: settings, RawSettings: append(json.RawMessage(nil), sourceNode.ProtocolSettings...),
			ConfiguredRate: sourceNode.ConfiguredRate, CreatedAt: sourceNode.CreatedAt, ParentCreatedAt: sourceNode.ParentCreatedAt,
			CustomOutbounds: append(json.RawMessage(nil), sourceNode.CustomOutbounds...), CustomRoutes: append(json.RawMessage(nil), sourceNode.CustomRoutes...),
			CertificateConfig: append(json.RawMessage(nil), sourceNode.CertificateConfig...),
		}
		node.Password = nodePassword(account.UUID, node)
		nodes = append(nodes, node)
	}
	return PresentNodes(account, nodes, options), nil
}

// PresentNodes adds the synthetic status nodes and protocol labels after request
// filtering, matching the order used by the legacy subscription controller.
func PresentNodes(account store.SubscriptionAccount, source []PreparedNode, options PrepareOptions) []PreparedNode {
	nodes := append([]PreparedNode(nil), source...)
	if len(nodes) == 0 {
		return nodes
	}
	if options.Now.IsZero() {
		options.Now = time.Now()
	}
	if options.Location == nil {
		options.Location = legacyLocation
	}
	if options.RejectedByRequestFilter > 0 {
		copyNode := nodes[0]
		copyNode.Name = fmt.Sprintf("过滤掉%d条线路", options.RejectedByRequestFilter)
		nodes = append([]PreparedNode{copyNode}, nodes...)
	}
	if options.ShowInfo {
		first := nodes[0]
		expiry := "长期有效"
		if account.ExpiredAt != nil {
			expiry = account.ExpiredAt.In(options.Location).Format("2006-01-02")
		}
		expiryNode := first
		expiryNode.Name = "套餐到期：" + expiry
		nodes = append([]PreparedNode{expiryNode}, nodes...)
		if options.NextResetAt != nil {
			days := int(math.Ceil(options.NextResetAt.Sub(options.Now).Hours() / 24))
			if days < 0 {
				days = 0
			}
			resetNode := first
			resetNode.Name = fmt.Sprintf("距离下次重置剩余：%d 天", days)
			nodes = append([]PreparedNode{resetNode}, nodes...)
		}
		remainingNode := first
		remainingNode.Name = "剩余流量：" + trafficConvert(account.TransferEnable-account.TrafficUpload-account.TrafficDownload)
		nodes = append([]PreparedNode{remainingNode}, nodes...)
	}
	if options.ShowProtocol {
		for index := range nodes {
			nodes[index].Name = protocolPrefix(nodes[index]) + nodes[index].Name
		}
	}
	return nodes
}

func selectNodePort(value string, selector func(int, int) (int, error)) (int, string, error) {
	if !strings.Contains(value, "-") {
		port, err := strconv.Atoi(value)
		if err != nil || port < 1 || port > 65535 {
			return 0, "", fmt.Errorf("invalid port %q", value)
		}
		return port, "", nil
	}
	parts := strings.SplitN(value, "-", 2)
	minimum, firstErr := strconv.Atoi(parts[0])
	maximum, secondErr := strconv.Atoi(parts[1])
	if firstErr != nil || secondErr != nil || minimum < 1 || maximum < 1 || minimum > 65535 || maximum > 65535 {
		return 0, "", fmt.Errorf("invalid port range %q", value)
	}
	if minimum > maximum {
		minimum, maximum = maximum, minimum
	}
	port, err := selector(minimum, maximum)
	if err != nil {
		return 0, "", err
	}
	if port < minimum || port > maximum {
		return 0, "", errorsNewPortOutsideRange(port)
	}
	return port, value, nil
}

func errorsNewPortOutsideRange(port int) error {
	return fmt.Errorf("selected port %d is outside range", port)
}

func securePort(minimum, maximum int) (int, error) {
	span := int64(maximum-minimum) + 1
	value, err := rand.Int(rand.Reader, big.NewInt(span))
	if err != nil {
		return 0, err
	}
	return minimum + int(value.Int64()), nil
}

func nodePassword(uuid string, node PreparedNode) string {
	if node.Type != "shadowsocks" {
		return uuid
	}
	cipher, _ := node.ProtocolSettings["cipher"].(string)
	serverKeySize := 0
	switch cipher {
	case "2022-blake3-aes-128-gcm":
		serverKeySize = 16
	case "2022-blake3-aes-256-gcm", "2022-blake3-chacha20-poly1305":
		serverKeySize = 32
	default:
		return uuid
	}
	createdAt := node.CreatedAt
	if node.ParentCreatedAt != nil {
		createdAt = *node.ParentCreatedAt
	}
	digest := md5.Sum([]byte(strconv.FormatInt(createdAt.Unix(), 10)))
	hexDigest := hex.EncodeToString(digest[:])
	serverKey := base64.StdEncoding.EncodeToString([]byte(hexDigest[:serverKeySize]))
	userKey := base64.StdEncoding.EncodeToString([]byte(uuid[:min(serverKeySize, len(uuid))]))
	return serverKey + ":" + userKey
}

func protocolPrefix(node PreparedNode) string {
	switch node.Type {
	case "hysteria":
		if numberValue(node.ProtocolSettings["version"]) == 2 {
			return "[Hy2]"
		}
		return "[Hy]"
	case "vless":
		return "[vless]"
	case "shadowsocks":
		return "[ss]"
	case "vmess":
		return "[vmess]"
	case "trojan":
		return "[trojan]"
	case "tuic":
		return "[tuic]"
	case "socks":
		return "[socks]"
	case "anytls":
		return "[anytls]"
	default:
		return ""
	}
}

func trafficConvert(bytes int64) string {
	if bytes < 0 {
		return "0"
	}
	value := float64(bytes)
	unit := "B"
	switch {
	case bytes > 1<<30:
		value /= 1 << 30
		unit = "GB"
	case bytes > 1<<20:
		value /= 1 << 20
		unit = "MB"
	case bytes > 1<<10:
		value /= 1 << 10
		unit = "KB"
	}
	text := strconv.FormatFloat(math.Round(value*100)/100, 'f', -1, 64)
	return text + " " + unit
}

func VersionAtLeast(actual, minimum string) bool {
	if actual == "" {
		return false
	}
	actualParts := versionParts(actual)
	minimumParts := versionParts(minimum)
	length := max(len(actualParts), len(minimumParts))
	for index := 0; index < length; index++ {
		actualValue, minimumValue := 0, 0
		if index < len(actualParts) {
			actualValue = actualParts[index]
		}
		if index < len(minimumParts) {
			minimumValue = minimumParts[index]
		}
		if actualValue != minimumValue {
			return actualValue > minimumValue
		}
	}
	return true
}

func versionParts(value string) []int {
	parts := strings.Split(value, ".")
	result := make([]int, len(parts))
	for index, part := range parts {
		result[index], _ = strconv.Atoi(part)
	}
	return result
}

func numberValue(value any) int {
	switch typed := value.(type) {
	case float64:
		return int(typed)
	case int:
		return typed
	case json.Number:
		result, _ := typed.Int64()
		return int(result)
	default:
		return 0
	}
}
