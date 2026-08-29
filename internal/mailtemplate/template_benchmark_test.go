package mailtemplate

import "testing"

func BenchmarkRenderNotifyTemplate(b *testing.B) {
	definition, ok := DefinitionFor(Notify)
	if !ok {
		b.Fatal("notify template definition is missing")
	}
	template := Template{Name: Notify, Subject: definition.DefaultSubject, Content: definition.DefaultContent}
	values := map[string]string{
		"name": "Xboard-Go", "url": "https://panel.example.test", "content": "A bounded notification body.",
	}
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		if _, err := Render(template, values); err != nil {
			b.Fatal(err)
		}
	}
}
