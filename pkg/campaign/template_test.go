package campaign

import (
	"strings"
	"testing"

	"whatsapp-automation/pkg/msgtemplate"
)

func TestTemplateHeaderAndButtonValues(t *testing.T) {
	for _, bad := range []Template{
		{Name: "offer", HeaderMedia: "http://example.com/a.jpg"},
		{Name: "offer", HeaderMedia: "https://example.com/a.jpg", HeaderParams: []string{"x"}},
		{Name: "offer", HeaderParams: []string{"a", "b"}},
		{Name: "offer", ButtonParams: []string{"{{unknown}}"}},
	} {
		if _, err := bad.Clean(); err == nil {
			t.Errorf("template %+v must be refused", bad)
		}
	}
	tpl, err := (&Template{Name: "offer", HeaderParams: []string{" {{first_name}} "}, Params: []string{"{{first_name}}"}, ButtonParams: []string{"", "c/{{first_name}}", "", ""}}).Clean()
	if err != nil {
		t.Fatal(err)
	}
	if len(tpl.ButtonParams) != 2 || tpl.ButtonParams[0] != "" {
		t.Fatalf("button positions must be kept and trailing empties dropped: %q", tpl.ButtonParams)
	}
	parts, missing := tpl.Render(msgtemplate.Vars{Name: "Asha Verma"})
	if len(missing) != 0 || parts.Header[0] != "Asha" || parts.URLButtons[1] != "c/Asha" || parts.URLButtons[0] != "" {
		t.Fatalf("bad render: %+v missing=%v", parts, missing)
	}
	if d := tpl.Describe(parts); !strings.Contains(d, "Asha") {
		t.Fatalf("describe: %s", d)
	}
}
