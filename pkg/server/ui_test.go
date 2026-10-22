package server

import (
	"io/fs"
	"regexp"
	"strings"
	"testing"
	"unicode"

	"whatsapp-automation/pkg/config"
	"whatsapp-automation/pkg/templates"
)

func webFiles(t *testing.T) map[string]string {
	t.Helper()
	out := map[string]string{}
	_ = fs.WalkDir(webFS, "web", func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() && (strings.HasSuffix(p, ".js") || strings.HasSuffix(p, ".html") || strings.HasSuffix(p, ".css")) {
			b, _ := fs.ReadFile(webFS, p)
			out[p] = string(b)
		}
		return nil
	})
	if len(out) < 10 {
		t.Fatalf("web assets missing: %d files", len(out))
	}
	return out
}

// Every button, form and input the UI renders must have a registered handler (C21, U1).
func TestEveryControlIsWired(t *testing.T) {
	files := webFiles(t)
	used := regexp.MustCompile(`data-(action|form|change)="([a-z0-9-]+)"`)
	reg := regexp.MustCompile(`P\.(act|form|change)\('([a-z0-9-]+)'`)
	registered := map[string]bool{}
	for _, src := range files {
		for _, m := range reg.FindAllStringSubmatch(src, -1) {
			kind := map[string]string{"act": "action", "form": "form", "change": "change"}[m[1]]
			registered[kind+":"+m[2]] = true
		}
	}
	count := 0
	for name, src := range files {
		for _, m := range used.FindAllStringSubmatch(src, -1) {
			count++
			if !registered[m[1]+":"+m[2]] {
				t.Errorf("%s uses data-%s=%q but no handler is registered", name, m[1], m[2])
			}
		}
	}
	if count < 80 {
		t.Fatalf("expected many wired controls, found %d", count)
	}
}

func TestNoInlineCodeOrStyles(t *testing.T) {
	inline := regexp.MustCompile(`(?i)\son[a-z]+\s*=\s*["']|\sstyle\s*=\s*["']|<style[\s>]|javascript:`)
	script := regexp.MustCompile(`(?i)<script(?:[^>]*)>`)
	for name, src := range webFiles(t) {
		if strings.HasSuffix(name, ".css") {
			continue
		}
		if m := inline.FindString(src); m != "" {
			t.Errorf("%s contains inline code or style %q (S12, U6)", name, m)
		}
		for _, tag := range script.FindAllString(src, -1) {
			if !strings.Contains(tag, "src=") {
				t.Errorf("%s has an inline <script> block", name)
			}
		}
		if strings.Contains(src, "innerHTML") && !strings.HasSuffix(name, "core.js") {
			t.Errorf("%s writes innerHTML directly; use P.mount with P.html so values are escaped", name)
		}
	}
}

func isEmoji(r rune) bool {
	return (r >= 0x1F000 && r <= 0x1FAFF) || (r >= 0x2600 && r <= 0x27BF) || (r >= 0x2B00 && r <= 0x2BFF) || r == 0xFE0F || r == 0x20E3
}

func TestNoEmojiAIOrFormattingMarkup(t *testing.T) {
	ai := regexp.MustCompile(`\bAI\b|(?i)gemini|openai|chatgpt`)
	check := func(where, text string) {
		for _, r := range text {
			if isEmoji(r) {
				t.Errorf("%s contains emoji %U (U3, U4)", where, r)
				break
			}
		}
		if m := ai.FindString(text); m != "" {
			t.Errorf("%s mentions %q (U2)", where, m)
		}
	}
	for name, src := range webFiles(t) {
		check(name, src)
	}
	c := config.DefaultConfig()
	for _, r := range c.Rules {
		check("rule "+r.Name, r.Reply)
		if strings.Contains(r.Reply, "*") || strings.Contains(r.Reply, "_") && !strings.Contains(r.Reply, "{{") {
			t.Errorf("default rule %s uses WhatsApp formatting markup (U5)", r.Name)
		}
		for _, r2 := range r.Reply {
			if unicode.Is(unicode.So, r2) {
				t.Errorf("default rule %s contains symbol %U", r.Name, r2)
			}
		}
	}
	for _, text := range []string{c.Automation.DefaultReply, c.Automation.HandoverReply, c.Automation.OptOutReply, c.Automation.OptInReply, c.BusinessHours.AwayReply} {
		check("built-in reply", text)
	}
	ts, err := templates.NewStore(t.TempDir() + "/t.json")
	if err != nil {
		t.Fatal(err)
	}
	for _, tp := range ts.List() {
		check("template "+tp.ID, tp.Content)
		if strings.Contains(tp.Content, "SAVE20") || strings.Contains(tp.Content, "{{order_id}}") {
			t.Errorf("template %s ships invented content (C2)", tp.ID)
		}
	}
}

func TestPagesRender(t *testing.T) {
	for _, name := range []string{"login.html", "setup.html", "index.html"} {
		var b strings.Builder
		if err := pageTmpl.ExecuteTemplate(&b, name, pageData{Product: "Pulse", Version: "1", Error: "<b>x</b>"}); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if strings.Contains(b.String(), "<b>x</b>") {
			t.Fatalf("%s does not escape the error message", name)
		}
	}
}
