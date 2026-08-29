package mailtemplate

import (
	"errors"
	"fmt"
	htmlstd "html"
	"net/url"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/microcosm-cc/bluemonday"
	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

const (
	MaxSubjectRunes  = 255
	MaxSubjectBytes  = 1 << 10
	MaxContentBytes  = 256 << 10
	MaxRenderedBytes = 512 << 10
)

type Name string

const (
	Verify        Name = "verify"
	Notify        Name = "notify"
	RemindExpire  Name = "remindExpire"
	RemindTraffic Name = "remindTraffic"
	MailLogin     Name = "mailLogin"
)

type Definition struct {
	Name           Name
	Label          string
	Required       []string
	Optional       []string
	DefaultSubject string
	DefaultContent string
}

type Template struct {
	Name    Name
	Subject string
	Content string
}

type Rendered struct {
	Subject string `json:"subject"`
	HTML    string `json:"html"`
	Text    string `json:"text"`
}

var definitions = [...]Definition{
	{
		Name:           Verify,
		Label:          "邮箱验证码",
		Required:       []string{"code"},
		Optional:       []string{"name", "url"},
		DefaultSubject: "{{name}} - 邮箱验证码",
		DefaultContent: `<h1>{{name}}</h1><h2>邮箱验证码</h2><p>请使用以下验证码完成验证，有效期 5 分钟。如非本人操作，请忽略此邮件。</p><p><strong>{{code}}</strong></p><p>如果您没有请求此验证码，无需进行任何操作。</p><p><a href="{{url}}">{{url}}</a></p><p>此邮件由系统自动发送，请勿直接回复。</p>`,
	},
	{
		Name:           Notify,
		Label:          "站点通知",
		Required:       []string{"content"},
		Optional:       []string{"name", "url"},
		DefaultSubject: "{{name}} - 站点通知",
		DefaultContent: `<h1>{{name}}</h1><h2>网站通知</h2><p>{{content}}</p><p><a href="{{url}}">前往查看</a></p><p><a href="{{url}}">{{url}}</a></p><p>此邮件由系统自动发送，请勿直接回复。</p>`,
	},
	{
		Name:           RemindExpire,
		Label:          "到期提醒",
		Required:       []string{},
		Optional:       []string{"name", "url"},
		DefaultSubject: "{{name}} - 服务即将到期",
		DefaultContent: `<h1>{{name}}</h1><h2>订阅即将到期</h2><p>您的订阅服务将在 <strong>24 小时</strong>内到期。</p><p>为避免服务中断，请及时续费。如您已完成续费，请忽略此提醒。</p><p><a href="{{url}}">立即续费</a></p><p><a href="{{url}}">{{url}}</a></p><p>此邮件由系统自动发送，请勿直接回复。</p>`,
	},
	{
		Name:           RemindTraffic,
		Label:          "流量提醒",
		Required:       []string{},
		Optional:       []string{"name", "url"},
		DefaultSubject: "{{name}} - 流量使用提醒",
		DefaultContent: `<h1>{{name}}</h1><h2>流量使用提醒</h2><p>您本月的套餐流量已使用 <strong>80%</strong>。</p><p>请合理安排使用，避免提前耗尽。如需更多流量，可前往面板升级套餐。</p><p><a href="{{url}}">查看用量</a></p><p><a href="{{url}}">{{url}}</a></p><p>此邮件由系统自动发送，请勿直接回复。</p>`,
	},
	{
		Name:           MailLogin,
		Label:          "邮件登录",
		Required:       []string{"link"},
		Optional:       []string{"name", "url"},
		DefaultSubject: "{{name}} - 邮件登录",
		DefaultContent: `<h1>{{name}}</h1><h2>登录确认</h2><p>点击下方按钮登录到 {{name}}，链接有效期 5 分钟且只能使用一次。如非本人操作，请忽略此邮件。</p><p><a href="{{link}}">确认登录</a></p><p>如果按钮无法点击，请复制以下链接到浏览器中打开：</p><p>{{link}}</p><p><a href="{{url}}">{{url}}</a></p><p>此邮件由系统自动发送，请勿直接回复。</p>`,
	},
}

// Policies are immutable after construction and bluemonday supports concurrent
// sanitization with a shared policy. Reuse avoids rebuilding its rule graph for
// every queued email.
var mailHTMLPolicy = bluemonday.UGCPolicy()

func Definitions() []Definition {
	result := make([]Definition, len(definitions))
	for index, definition := range definitions {
		result[index] = definition
		result[index].Required = append([]string(nil), definition.Required...)
		result[index].Optional = append([]string(nil), definition.Optional...)
	}
	return result
}

func DefinitionFor(name Name) (Definition, bool) {
	definition, ok := definitionFor(name)
	if !ok {
		return Definition{}, false
	}
	result := *definition
	result.Required = append([]string(nil), definition.Required...)
	result.Optional = append([]string(nil), definition.Optional...)
	return result, true
}

func Validate(name Name, subject, content string) error {
	if _, err := validateTemplateSyntax(name, subject, content); err != nil {
		return err
	}
	_, err := renderValidated(Template{Name: name, Subject: subject, Content: content}, map[string]string{
		"name": "Xboard-Go", "url": "https://panel.example.invalid", "code": "123456",
		"content": "Notification content", "link": "https://panel.example.invalid/#/login?verify=example",
	})
	return err
}

func Render(template Template, values map[string]string) (Rendered, error) {
	definition, err := validateTemplateSyntax(template.Name, template.Subject, template.Content)
	if err != nil {
		return Rendered{}, err
	}
	for _, required := range definition.Required {
		if values[required] == "" {
			return Rendered{}, fmt.Errorf("required mail template value %q is missing", required)
		}
	}
	return renderValidated(template, values)
}

func definitionFor(name Name) (*Definition, bool) {
	for index := range definitions {
		if definitions[index].Name == name {
			return &definitions[index], true
		}
	}
	return nil, false
}

func validateTemplateSyntax(name Name, subject, content string) (*Definition, error) {
	definition, ok := definitionFor(name)
	if !ok {
		return nil, errors.New("unknown mail template")
	}
	if !utf8.ValidString(subject) || subject == "" || utf8.RuneCountInString(subject) > MaxSubjectRunes || len(subject) > MaxSubjectBytes || containsForbiddenControl(subject, false) {
		return nil, errors.New("invalid mail template subject")
	}
	if !utf8.ValidString(content) || content == "" || len(content) > MaxContentBytes || containsForbiddenControl(content, true) {
		return nil, errors.New("invalid mail template content")
	}
	if name == Verify {
		if strings.Contains(subject, "{{code}}") || strings.Count(content, "{{code}}") != 1 {
			return nil, errors.New("mail verification code placeholder must appear exactly once in the content")
		}
		inAttribute, err := placeholderInHTMLAttribute(content, "{{code}}")
		if err != nil {
			return nil, errors.New("invalid mail template HTML")
		}
		if inAttribute {
			return nil, errors.New("mail verification code placeholder is not allowed in an HTML attribute")
		}
	}
	allowed := make(map[string]struct{}, len(definition.Required)+len(definition.Optional))
	for _, placeholder := range definition.Required {
		allowed[placeholder] = struct{}{}
	}
	for _, placeholder := range definition.Optional {
		allowed[placeholder] = struct{}{}
	}
	used, err := placeholders(subject + "\n" + content)
	if err != nil {
		return nil, err
	}
	for placeholder := range used {
		if _, ok := allowed[placeholder]; !ok {
			return nil, fmt.Errorf("unknown mail template placeholder %q", placeholder)
		}
	}
	for _, required := range definition.Required {
		if _, ok := used[required]; !ok {
			return nil, fmt.Errorf("required mail template placeholder %q is missing", required)
		}
	}
	return definition, nil
}

func placeholderInHTMLAttribute(value, placeholder string) (bool, error) {
	contextNode := &html.Node{Type: html.ElementNode, Data: "div", DataAtom: atom.Div}
	nodes, err := html.ParseFragment(strings.NewReader(value), contextNode)
	if err != nil {
		return false, err
	}
	var inspect func(*html.Node) bool
	inspect = func(node *html.Node) bool {
		for _, attribute := range node.Attr {
			if strings.Contains(attribute.Val, placeholder) {
				return true
			}
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			if inspect(child) {
				return true
			}
		}
		return false
	}
	for _, node := range nodes {
		if inspect(node) {
			return true, nil
		}
	}
	return false, nil
}

func renderValidated(template Template, values map[string]string) (Rendered, error) {
	if template.Name == MailLogin {
		appOrigin, appOK := absoluteHTTPOrigin(values["url"])
		linkOrigin, linkOK := absoluteHTTPOrigin(values["link"])
		if !appOK || !linkOK || appOrigin != linkOrigin {
			return Rendered{}, errors.New("mail login link must use the application origin")
		}
	}
	subject := replacePlaceholders(template.Subject, values, false)
	if !utf8.ValidString(subject) || subject == "" || utf8.RuneCountInString(subject) > MaxSubjectRunes || len(subject) > MaxSubjectBytes || containsForbiddenControl(subject, false) {
		return Rendered{}, errors.New("rendered mail subject is invalid")
	}
	rawHTML := replacePlaceholders(template.Content, values, true)
	if len(rawHTML) > MaxRenderedBytes {
		return Rendered{}, errors.New("rendered mail HTML is too large")
	}
	sanitizedHTML, err := constrainMailHTML(mailHTMLPolicy.Sanitize(rawHTML), values)
	if err != nil {
		return Rendered{}, fmt.Errorf("constrain rendered mail HTML: %w", err)
	}
	text, err := htmlToText(sanitizedHTML)
	if err != nil {
		return Rendered{}, fmt.Errorf("build mail text fallback: %w", err)
	}
	if sanitizedHTML == "" || strings.TrimSpace(text) == "" || len(sanitizedHTML) > MaxRenderedBytes || len(text) > MaxRenderedBytes {
		return Rendered{}, errors.New("rendered mail body is invalid")
	}
	return Rendered{Subject: subject, HTML: sanitizedHTML, Text: text}, nil
}

func containsForbiddenControl(value string, allowLayoutWhitespace bool) bool {
	return strings.IndexFunc(value, func(character rune) bool {
		if allowLayoutWhitespace && (character == '\t' || character == '\n' || character == '\r') {
			return false
		}
		return unicode.IsControl(character)
	}) >= 0
}

func placeholders(value string) (map[string]struct{}, error) {
	result := make(map[string]struct{})
	for offset := 0; offset < len(value); {
		start := strings.Index(value[offset:], "{{")
		endOnly := strings.Index(value[offset:], "}}")
		if start < 0 {
			if endOnly >= 0 {
				return nil, errors.New("malformed mail template placeholder")
			}
			break
		}
		start += offset
		if endOnly >= 0 && offset+endOnly < start {
			return nil, errors.New("malformed mail template placeholder")
		}
		end := strings.Index(value[start+2:], "}}")
		if end < 0 {
			return nil, errors.New("malformed mail template placeholder")
		}
		end += start + 2
		name := value[start+2 : end]
		if !validPlaceholderName(name) {
			return nil, errors.New("malformed mail template placeholder")
		}
		result[name] = struct{}{}
		offset = end + 2
	}
	return result, nil
}

func validPlaceholderName(value string) bool {
	if value == "" {
		return false
	}
	for index, character := range value {
		if !((character >= 'a' && character <= 'z') || (character >= 'A' && character <= 'Z') || (index > 0 && character >= '0' && character <= '9') || (index > 0 && character == '_')) {
			return false
		}
	}
	return true
}

func replacePlaceholders(value string, values map[string]string, escapeHTML bool) string {
	var result strings.Builder
	for offset := 0; offset < len(value); {
		start := strings.Index(value[offset:], "{{")
		if start < 0 {
			result.WriteString(value[offset:])
			break
		}
		start += offset
		result.WriteString(value[offset:start])
		end := strings.Index(value[start+2:], "}}") + start + 2
		placeholder := value[start+2 : end]
		replacement := values[placeholder]
		if escapeHTML {
			replacement = htmlstd.EscapeString(replacement)
			if placeholder == "content" {
				replacement = strings.ReplaceAll(strings.ReplaceAll(replacement, "\r\n", "\n"), "\r", "\n")
				replacement = strings.ReplaceAll(replacement, "\n", "<br>")
			}
		}
		result.WriteString(replacement)
		offset = end + 2
	}
	return result.String()
}

func constrainMailHTML(value string, values map[string]string) (string, error) {
	allowedOrigins := make(map[string]struct{}, 2)
	for _, key := range []string{"url", "link"} {
		if origin, ok := absoluteHTTPOrigin(values[key]); ok {
			allowedOrigins[origin] = struct{}{}
		}
	}
	contextNode := &html.Node{Type: html.ElementNode, Data: "div", DataAtom: atom.Div}
	nodes, err := html.ParseFragment(strings.NewReader(value), contextNode)
	if err != nil {
		return "", err
	}
	var result strings.Builder
	for _, node := range nodes {
		if !constrainMailHTMLNode(node, allowedOrigins, values["code"]) {
			continue
		}
		if err := html.Render(&result, node); err != nil {
			return "", err
		}
	}
	return result.String(), nil
}

func absoluteHTTPOrigin(value string) (string, bool) {
	parsed, err := url.Parse(strings.TrimSpace(value))
	if err != nil || parsed.User != nil || parsed.Host == "" || !(strings.EqualFold(parsed.Scheme, "http") || strings.EqualFold(parsed.Scheme, "https")) {
		return "", false
	}
	return strings.ToLower(parsed.Scheme) + "://" + strings.ToLower(parsed.Host), true
}

func constrainMailHTMLNode(node *html.Node, allowedOrigins map[string]struct{}, verificationCode string) bool {
	if node.Type == html.ElementNode && mailResourceElement(node.Data) {
		return false
	}
	if node.Type == html.ElementNode {
		attributes := node.Attr[:0]
		for _, attribute := range node.Attr {
			if verificationCode != "" && strings.Contains(attribute.Val, verificationCode) {
				continue
			}
			key := strings.ToLower(attribute.Key)
			if node.Data == "a" && key == "href" {
				if safeMailLink(attribute.Val, allowedOrigins) {
					attributes = append(attributes, attribute)
				}
				continue
			}
			if mailResourceAttribute(key) {
				continue
			}
			attributes = append(attributes, attribute)
		}
		node.Attr = attributes
	}
	for child := node.FirstChild; child != nil; {
		next := child.NextSibling
		if !constrainMailHTMLNode(child, allowedOrigins, verificationCode) {
			node.RemoveChild(child)
		}
		child = next
	}
	return true
}

func safeMailLink(value string, allowedOrigins map[string]struct{}) bool {
	value = strings.TrimSpace(value)
	if strings.HasPrefix(value, "#") {
		return true
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.User != nil || parsed.Host == "" || !(strings.EqualFold(parsed.Scheme, "http") || strings.EqualFold(parsed.Scheme, "https")) {
		return false
	}
	_, ok := allowedOrigins[strings.ToLower(parsed.Scheme)+"://"+strings.ToLower(parsed.Host)]
	return ok
}

func mailResourceElement(name string) bool {
	switch name {
	case "audio", "base", "button", "embed", "form", "iframe", "img", "input", "link", "meta", "object", "picture", "select", "source", "textarea", "track", "video":
		return true
	default:
		return false
	}
}

func mailResourceAttribute(name string) bool {
	switch name {
	case "action", "background", "data", "formaction", "href", "ping", "poster", "src", "srcdoc", "srcset":
		return true
	default:
		return false
	}
}

func htmlToText(value string) (string, error) {
	document, err := html.Parse(strings.NewReader(value))
	if err != nil {
		return "", err
	}
	var result strings.Builder
	var visit func(*html.Node, bool)
	visit = func(node *html.Node, suppressed bool) {
		if node.Type == html.ElementNode && (node.Data == "script" || node.Data == "style") {
			suppressed = true
		}
		if !suppressed && node.Type == html.TextNode {
			result.WriteString(node.Data)
		}
		if !suppressed && node.Type == html.ElementNode && node.Data == "br" {
			result.WriteByte('\n')
		}
		textStart := result.Len()
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			visit(child, suppressed)
		}
		if !suppressed && node.Type == html.ElementNode && node.Data == "a" {
			for _, attribute := range node.Attr {
				if attribute.Key == "href" && attribute.Val != "" && strings.TrimSpace(result.String()[textStart:]) != attribute.Val {
					result.WriteString(" (" + attribute.Val + ")")
					break
				}
			}
		}
		if !suppressed && node.Type == html.ElementNode && isBlockElement(node.Data) {
			result.WriteByte('\n')
		}
	}
	visit(document, false)
	lines := strings.Split(strings.ReplaceAll(result.String(), "\u00a0", " "), "\n")
	compacted := lines[:0]
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line != "" {
			compacted = append(compacted, line)
		}
	}
	return strings.Join(compacted, "\n"), nil
}

func isBlockElement(name string) bool {
	switch name {
	case "address", "article", "aside", "blockquote", "div", "footer", "h1", "h2", "h3", "h4", "h5", "h6", "header", "li", "main", "nav", "ol", "p", "pre", "section", "table", "tr", "ul":
		return true
	default:
		return false
	}
}
