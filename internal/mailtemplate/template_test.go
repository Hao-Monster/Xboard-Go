package mailtemplate

import (
	"strings"
	"testing"
)

func TestDefinitionsMatchLegacyFiveTemplateContract(t *testing.T) {
	want := []Name{Verify, Notify, RemindExpire, RemindTraffic, MailLogin}
	wantLabels := []string{"邮箱验证码", "站点通知", "到期提醒", "流量提醒", "邮件登录"}
	definitions := Definitions()
	if len(definitions) != len(want) {
		t.Fatalf("Definitions() length = %d, want %d", len(definitions), len(want))
	}
	for index, name := range want {
		if definitions[index].Name != name || definitions[index].Label != wantLabels[index] || definitions[index].DefaultSubject == "" || definitions[index].DefaultContent == "" {
			t.Fatalf("Definitions()[%d] = %#v, want complete %q definition", index, definitions[index], name)
		}
	}
	if !strings.Contains(definitions[2].DefaultContent, "24 小时") || !strings.Contains(definitions[3].DefaultContent, "80%") {
		t.Fatalf("legacy reminder semantics are missing: expire=%q traffic=%q", definitions[2].DefaultContent, definitions[3].DefaultContent)
	}
}

func TestLegacyDefaultEditorDeliveryAndTestSubjectsRemainDistinct(t *testing.T) {
	wantDelivery := map[Name]string{
		Verify: "XBoard邮箱验证码", Notify: "您在XBoard的工单得到了回复",
		RemindExpire: "您在XBoard的服务即将到期", RemindTraffic: "您在XBoard的流量使用已达到80%", MailLogin: "登录到XBoard",
	}
	wantTest := map[Name]string{
		Verify: "XBoard - 验证码测试", Notify: "XBoard - 通知测试",
		RemindExpire: "XBoard - 到期提醒测试", RemindTraffic: "XBoard - 流量提醒测试", MailLogin: "XBoard - 登录链接测试",
	}
	for _, definition := range Definitions() {
		delivery, deliveryOK := DeliverySubject(definition.Name, "XBoard")
		testSubject, testOK := TestSubject(definition.Name, "XBoard")
		if !deliveryOK || delivery != wantDelivery[definition.Name] || !testOK || testSubject != wantTest[definition.Name] || definition.DefaultSubject == delivery {
			t.Fatalf("subject contract for %q: editor=%q delivery=(%q,%t) test=(%q,%t)", definition.Name, definition.DefaultSubject, delivery, deliveryOK, testSubject, testOK)
		}
	}
}

func TestValidateRejectsUnknownMalformedAndMissingPlaceholders(t *testing.T) {
	tests := []struct {
		name    Name
		subject string
		content string
	}{
		{Verify, "{{name}}", "missing code"},
		{Verify, "{{code}} in subject", "{{code}}"},
		{Verify, "subject", "{{code}} and {{code}}"},
		{Verify, "{{unknown}}", "{{code}}"},
		{Notify, "subject", "{{ content }}"},
		{Notify, "subject", `<img src="{{content}}">`},
		{MailLogin, "subject", "{{link"},
		{Notify, strings.Repeat("a", MaxSubjectRunes+1), "{{content}}"},
		{Notify, "unsafe\r\nBcc: attacker@example.test", "{{content}}"},
		{Notify, "unsafe\tcontrol", "{{content}}"},
		{Notify, string([]byte{0xff}), "{{content}}"},
		{Notify, "subject", "{{content}}\x00hidden"},
		{Notify, "subject", "{{content}}" + strings.Repeat("a", MaxContentBytes)},
	}
	for _, test := range tests {
		if err := Validate(test.name, test.subject, test.content); err == nil {
			t.Fatalf("Validate(%q, %q, %q) succeeded", test.name, test.subject, test.content)
		}
	}
}

func TestRenderVerificationCodeCannotLeakThroughAnHTMLAttribute(t *testing.T) {
	_, err := Render(Template{
		Name: Verify, Subject: "{{name}} verification", Content: `<a href="{{url}}?code={{code}}">continue</a>`,
	}, map[string]string{"name": "Xboard-Go", "url": "https://panel.example.test", "code": "654321"})
	if err == nil {
		t.Fatal("Render() accepted a verification code that only appeared in an HTML attribute")
	}
}

func TestRenderEscapesVariablesSanitizesHTMLAndBuildsTextFallback(t *testing.T) {
	template := Template{
		Name: Notify, Subject: "{{name}} - 通知", Content: `<p>{{name}}</p><p onclick="alert(1)">你好 {{content}}</p><script>alert(2)</script><p><a href="javascript:alert(3)">继续</a></p><a href="https://evil.example/collect">恶意外链</a><a href="{{url}}/account">安全链接</a><img src="https://tracker.example/pixel">`,
	}
	rendered, err := Render(template, map[string]string{
		"name": "A&B", "url": "https://panel.example.test", "content": "<img src=x onerror=alert(4)>\n第二行",
	})
	if err != nil {
		t.Fatalf("Render() error = %v", err)
	}
	if rendered.Subject != "A&B - 通知" {
		t.Fatalf("subject = %q", rendered.Subject)
	}
	for _, forbidden := range []string{"onclick", "<script", "javascript:", "<img", "evil.example", "tracker.example"} {
		if strings.Contains(strings.ToLower(rendered.HTML), forbidden) {
			t.Fatalf("HTML contains %q: %s", forbidden, rendered.HTML)
		}
	}
	if !strings.Contains(rendered.HTML, "A&amp;B") || !strings.Contains(rendered.HTML, "&lt;img") || !strings.Contains(rendered.HTML, "<br") || !strings.Contains(rendered.HTML, "https://panel.example.test/account") {
		t.Fatalf("HTML did not escape variables: %s", rendered.HTML)
	}
	if !strings.Contains(rendered.Text, "你好 <img src=x onerror=alert(4)>\n第二行") || strings.Contains(rendered.Text, "alert(2)") || strings.Contains(rendered.Text, "evil.example") {
		t.Fatalf("text fallback = %q", rendered.Text)
	}
}

func TestRenderMailLoginCannotRedirectTheSystemLinkToAnotherOrigin(t *testing.T) {
	rendered, err := Render(Template{
		Name: MailLogin, Subject: "{{name}} login", Content: `<a href="https://evil.example/steal">{{link}}</a><a href="{{link}}">system</a>`,
	}, map[string]string{
		"name": "Xboard-Go", "url": "https://panel.example.test", "link": "https://panel.example.test/#/login?verify=secret",
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(rendered.HTML, "evil.example") || !strings.Contains(rendered.HTML, `href="https://panel.example.test/#/login?verify=secret"`) {
		t.Fatalf("mail login HTML did not enforce the system origin: %s", rendered.HTML)
	}
	if _, err := Render(Template{
		Name: MailLogin, Subject: "{{name}} login", Content: `<a href="{{link}}">system</a>`,
	}, map[string]string{
		"name": "Xboard-Go", "url": "https://panel.example.test", "link": "https://evil.example/#/login?verify=secret",
	}); err == nil {
		t.Fatal("Render() accepted a runtime mail login link from another origin")
	}
}

func TestRenderRequiresRuntimeValuesAndBoundsRenderedOutput(t *testing.T) {
	definition, ok := DefinitionFor(Verify)
	if !ok {
		t.Fatal("verify definition is missing")
	}
	if _, err := Render(Template{Name: Verify, Subject: definition.DefaultSubject, Content: definition.DefaultContent}, map[string]string{"name": "X"}); err == nil {
		t.Fatal("Render() succeeded without required code")
	}
	if _, err := Render(Template{Name: Verify, Subject: definition.DefaultSubject, Content: definition.DefaultContent}, map[string]string{
		"name": "X", "code": strings.Repeat("1", MaxRenderedBytes), "url": "https://example.test",
	}); err == nil {
		t.Fatal("Render() accepted oversized rendered output")
	}
}
