package knowledge

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"unicode"

	"github.com/microcosm-cc/bluemonday"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/renderer/html"
	"github.com/yuin/goldmark/text"
	nethtml "golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

const NoSubscriptionMessage = "您必须拥有有效的订阅才可以查看该区域的内容"

var accessRegion = regexp.MustCompile(`(?s)<!--access start-->.*?<!--access end-->`)
var publicSanitizer = newPublicPolicy()

type TOCEntry struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	Level int    `json:"level"`
}

type PublicDocument struct {
	HTML string     `json:"body"`
	TOC  []TOCEntry `json:"toc"`
}

func UserContent(body, siteName, subscriptionURL string, subscriptionValid bool) string {
	if !subscriptionValid {
		body = accessRegion.ReplaceAllString(body, `<div class="v2board-no-access">`+NoSubscriptionMessage+`</div>`)
	} else {
		body = strings.NewReplacer("<!--access start-->", "", "<!--access end-->", "").Replace(body)
	}
	return replacePlaceholders(body, siteName, subscriptionURL)
}

func PublicContent(body, siteName, loginURL string) string {
	return replacePlaceholders(body, siteName, loginURL)
}

func replacePlaceholders(body, siteName, subscriptionURL string) string {
	replacements := strings.NewReplacer(
		"{{siteName}}", siteName,
		"{{subscribeUrl}}", subscriptionURL,
		"{{urlEncodeSubscribeUrl}}", url.QueryEscape(subscriptionURL),
		"{{safeBase64SubscribeUrl}}", base64.RawURLEncoding.EncodeToString([]byte(subscriptionURL)),
	)
	return replacements.Replace(body)
}

func RenderPublic(source string) (PublicDocument, error) {
	markdown := goldmark.New(
		goldmark.WithExtensions(extension.GFM),
		goldmark.WithParserOptions(parser.WithAutoHeadingID()),
		goldmark.WithRendererOptions(html.WithUnsafe()),
	)
	input := []byte(source)
	document := markdown.Parser().Parse(text.NewReader(input))
	toc := make([]TOCEntry, 0)
	if err := ast.Walk(document, func(node ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		heading, ok := node.(*ast.Heading)
		if !ok {
			return ast.WalkContinue, nil
		}
		idValue, exists := heading.AttributeString("id")
		if !exists {
			return ast.WalkContinue, nil
		}
		var id string
		switch value := idValue.(type) {
		case []byte:
			id = string(value)
		case string:
			id = value
		default:
			id = fmt.Sprint(value)
		}
		toc = append(toc, TOCEntry{ID: id, Title: strings.TrimSpace(string(heading.Text(input))), Level: heading.Level})
		return ast.WalkContinue, nil
	}); err != nil {
		return PublicDocument{}, fmt.Errorf("walk knowledge document: %w", err)
	}

	var rendered bytes.Buffer
	if err := markdown.Renderer().Render(&rendered, input, document); err != nil {
		return PublicDocument{}, fmt.Errorf("render knowledge markdown: %w", err)
	}
	sanitized := publicSanitizer.SanitizeBytes(rendered.Bytes())
	augmented, err := augmentPublicHTML(sanitized)
	if err != nil {
		return PublicDocument{}, err
	}
	return PublicDocument{HTML: augmented, TOC: toc}, nil
}

func newPublicPolicy() *bluemonday.Policy {
	policy := bluemonday.NewPolicy()
	policy.AllowElements(
		"a", "blockquote", "br", "code", "del", "div", "em", "figcaption", "figure",
		"h1", "h2", "h3", "h4", "h5", "h6", "hr", "img", "li", "ol", "p", "pre",
		"source", "span", "strong", "table", "tbody", "td", "th", "thead", "tr", "u", "ul", "video",
	)
	policy.AllowAttrs("href", "title", "target", "rel").OnElements("a")
	policy.AllowAttrs("class").OnElements("div", "figcaption", "figure", "span", "table")
	policy.AllowAttrs("src", "alt", "title", "width", "height", "loading").OnElements("img")
	policy.AllowAttrs("src", "type").OnElements("source")
	policy.AllowAttrs("colspan", "rowspan").OnElements("td", "th")
	policy.AllowAttrs("src", "controls", "preload", "poster", "width", "height").OnElements("video")
	policy.AllowAttrs("id").OnElements("h1", "h2", "h3", "h4", "h5", "h6")
	policy.RequireParseableURLs(true)
	policy.AllowRelativeURLs(true)
	policy.AllowURLSchemes("http", "https", "mailto")
	return policy
}

func augmentPublicHTML(source []byte) (string, error) {
	contextNode := &nethtml.Node{Type: nethtml.ElementNode, Data: "div", DataAtom: atom.Div}
	nodes, err := nethtml.ParseFragment(bytes.NewReader(source), contextNode)
	if err != nil {
		return "", fmt.Errorf("parse sanitized knowledge HTML: %w", err)
	}
	for _, node := range nodes {
		walkHTML(node, func(element *nethtml.Node) {
			switch element.Data {
			case "img":
				setHTMLAttribute(element, "loading", "lazy")
			case "video":
				setHTMLAttribute(element, "controls", "")
				setHTMLAttribute(element, "preload", "metadata")
			case "a":
				if htmlAttribute(element, "target") == "_blank" {
					setHTMLAttribute(element, "rel", "noopener noreferrer")
				}
			}
		})
	}
	var output bytes.Buffer
	for _, node := range nodes {
		if err := nethtml.Render(&output, node); err != nil {
			return "", fmt.Errorf("serialize sanitized knowledge HTML: %w", err)
		}
	}
	return output.String(), nil
}

func walkHTML(node *nethtml.Node, visit func(*nethtml.Node)) {
	if node.Type == nethtml.ElementNode {
		visit(node)
	}
	for child := node.FirstChild; child != nil; child = child.NextSibling {
		walkHTML(child, visit)
	}
}

func htmlAttribute(node *nethtml.Node, name string) string {
	for _, attribute := range node.Attr {
		if attribute.Key == name {
			return attribute.Val
		}
	}
	return ""
}

func setHTMLAttribute(node *nethtml.Node, name, value string) {
	for index := range node.Attr {
		if node.Attr[index].Key == name {
			node.Attr[index].Val = value
			return
		}
	}
	node.Attr = append(node.Attr, nethtml.Attribute{Key: name, Val: value})
}

func Slug(title string) string {
	title = strings.ToLower(strings.TrimSpace(title))
	var result strings.Builder
	result.Grow(min(len(title), 80))
	pendingDash := false
	for _, character := range title {
		if result.Len() >= 80 {
			break
		}
		if character <= unicode.MaxASCII && (unicode.IsLetter(character) || unicode.IsDigit(character)) {
			if pendingDash && result.Len() > 0 {
				result.WriteByte('-')
			}
			result.WriteRune(character)
			pendingDash = false
			continue
		}
		pendingDash = result.Len() > 0
	}
	slug := strings.Trim(result.String(), "-")
	if slug == "" {
		return "article"
	}
	return slug
}
