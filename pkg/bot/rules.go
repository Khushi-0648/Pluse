package bot

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"whatsapp-automation/pkg/config"
)

// Normalize lowercases text, collapses whitespace and strips surrounding punctuation.
func Normalize(text string) string {
	t := strings.ToLower(strings.Join(strings.Fields(text), " "))
	return strings.TrimFunc(t, func(r rune) bool {
		return unicode.IsPunct(r) || unicode.IsSymbol(r) || unicode.IsSpace(r)
	})
}

var optOutWords = map[string]bool{
	"stop": true, "stop all": true, "unsubscribe": true, "optout": true, "opt-out": true, "opt out": true,
	"no more messages": true, "stop messages": true, "stop messaging": true, "remove me": true,
	"rok do": true, "rokdo": true, "band karo": true, "bandh karo": true, "band kro": true, "band kar do": true,
	"mat bhejo": true, "message mat bhejo": true, "messages mat bhejo": true,
	"बंद करो": true, "बंद": true, "रोको": true, "रोक दो": true, "मत भेजो": true,
}

var optInWords = map[string]bool{
	"start": true, "unstop": true, "subscribe": true, "resume": true,
	"shuru": true, "shuru karo": true, "chalu karo": true, "chalu": true,
	"शुरू": true, "शुरू करो": true, "चालू करो": true,
}

var handoverWords = map[string]bool{
	"agent": true, "human": true, "person": true, "representative": true, "operator": true, "staff": true,
	"real person": true, "live agent": true, "customer care": true, "customer support": true,
	"insaan": true, "manager": true,
}

var handoverPhrases = []string{
	"talk to a person", "talk to person", "talk to someone", "talk to a human", "talk to human", "talk to agent", "talk to an agent",
	"speak to a person", "speak to someone", "speak to a human", "speak to human", "speak to agent", "speak to an agent",
	"speak with a person", "speak with someone", "speak with agent", "speak with an agent", "speak with a human",
	"connect me to", "need a human", "need human", "need a person", "want to talk to", "call me back", "please call me",
	"baat karni hai", "baat karna hai", "kisi se baat", "insaan se baat",
}

// IsOptOut reports whether text is a request to stop messages.
func IsOptOut(text string) bool { return optOutWords[Normalize(text)] }

// IsOptIn reports whether text is a request to resume messages.
func IsOptIn(text string) bool { return optInWords[Normalize(text)] }

// IsHandoverRequest reports whether the customer is asking for a person.
func IsHandoverRequest(text string) bool {
	n := Normalize(text)
	if handoverWords[n] {
		return true
	}
	for _, p := range handoverPhrases {
		if strings.Contains(n, p) {
			return true
		}
	}
	return false
}

func isNumberToken(s string) bool {
	if s == "" || len(s) > 2 {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

var regexCache sync.Map

func compiled(pat string) *regexp.Regexp {
	if v, ok := regexCache.Load(pat); ok {
		return v.(*regexp.Regexp)
	}
	re, err := regexp.Compile(pat)
	if err != nil {
		regexCache.Store(pat, (*regexp.Regexp)(nil))
		return nil
	}
	regexCache.Store(pat, re)
	return re
}

// Keywords splits a rule pattern into its comma-separated keywords.
func Keywords(pattern string) []string {
	var out []string
	for _, p := range strings.Split(pattern, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// MatchRule reports whether text triggers rule. Number keywords ("1", "2") only match
// while a menu is active, so a stray "2" in conversation does not trigger a menu reply.
func MatchRule(rule config.Rule, text string, menuActive bool) bool {
	raw := strings.TrimSpace(text)
	n := Normalize(raw)
	if n == "" {
		return false
	}
	for _, kw := range Keywords(rule.Pattern) {
		k := Normalize(kw)
		switch rule.Type {
		case "regex":
			if re := compiled(kw); re != nil && re.MatchString(raw) {
				return true
			}
		case "contains", "prefix":
			if k == "" {
				continue
			}
			if isNumberToken(k) {
				// Numbers behave like menu choices whatever the match type.
				if menuActive && n == k {
					return true
				}
				continue
			}
			if rule.Type == "contains" && containsWord(n, k) || rule.Type == "prefix" && strings.HasPrefix(n, k) {
				return true
			}
		default: // exact
			if k == "" {
				continue
			}
			if isNumberToken(k) {
				if menuActive && n == k {
					return true
				}
				continue
			}
			if n == k || strings.HasPrefix(n, k+" ") {
				return true
			}
		}
	}
	return false
}

// containsWord reports whether k appears in n as whole words, so "hi" matches
// "hi there" but not "which" or "this".
func containsWord(n, k string) bool {
	isWord := func(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) || unicode.IsMark(r) }
	for from := 0; from <= len(n)-len(k); {
		i := strings.Index(n[from:], k)
		if i < 0 {
			return false
		}
		i += from
		before, _ := utf8.DecodeLastRuneInString(n[:i])
		after, _ := utf8.DecodeRuneInString(n[i+len(k):])
		if (i == 0 || !isWord(before)) && (i+len(k) == len(n) || !isWord(after)) {
			return true
		}
		_, size := utf8.DecodeRuneInString(n[i:])
		from = i + size
	}
	return false
}

// FindRule returns the first enabled rule (rules must already be priority-ordered) matching text.
func FindRule(rules []config.Rule, text string, menuActive bool) (config.Rule, bool) {
	for _, r := range rules {
		if r.Enabled && MatchRule(r, text, menuActive) {
			return r, true
		}
	}
	return config.Rule{}, false
}

// HasMenu reports whether a reply offers numbered options ("1. Services").
var menuLineRe = regexp.MustCompile(`(?m)^\s*[1-9][.)]\s+\S`)

func HasMenu(reply string) bool { return len(menuLineRe.FindAllString(reply, 2)) >= 2 }

// RuleConflicts lists keywords shared by more than one enabled rule.
func RuleConflicts(rules []config.Rule) []string {
	owners := map[string][]string{}
	for _, r := range rules {
		if !r.Enabled || r.Type == "regex" {
			continue
		}
		mine := map[string]bool{}
		for _, kw := range Keywords(r.Pattern) {
			k := Normalize(kw)
			if k != "" && !mine[k] {
				mine[k] = true
				owners[k] = append(owners[k], r.Name)
			}
		}
	}
	out := []string{}
	for k, names := range owners {
		if len(names) > 1 {
			out = append(out, fmt.Sprintf("%q is used by %s", k, strings.Join(names, " and ")))
		}
	}
	sort.Strings(out)
	return out
}

// Location returns the configured business time zone.
func Location(tz string) *time.Location {
	tz = strings.TrimSpace(tz)
	if tz == "" || strings.EqualFold(tz, "local") {
		return time.Local
	}
	if l, err := time.LoadLocation(tz); err == nil {
		return l
	}
	return time.Local
}

func parseClock(s string) (int, bool) {
	var h, m int
	if _, err := fmt.Sscanf(strings.TrimSpace(s), "%d:%d", &h, &m); err != nil || h < 0 || h > 24 || m < 0 || m > 59 {
		return 0, false
	}
	return h*60 + m, true
}

// ValidClock reports whether s is HH:MM.
func ValidClock(s string) bool { _, ok := parseClock(s); return ok }

// IsOpen reports whether the business is open at now according to the weekly schedule
// and holiday list. Windows that end before they start run past midnight.
func IsOpen(bh config.BusinessHoursConfig, now time.Time) bool {
	if !bh.Enabled {
		return true
	}
	now = now.In(Location(bh.Timezone))
	holiday := func(t time.Time) bool {
		day := t.Format("2006-01-02")
		for _, h := range bh.Holidays {
			if strings.TrimSpace(h) == day {
				return true
			}
		}
		return false
	}
	// A shift that runs past midnight belongs to the day it started on.
	todayOff, yesterdayOff := holiday(now), holiday(now.AddDate(0, 0, -1))
	minute := now.Hour()*60 + now.Minute()
	dayIdx := (int(now.Weekday()) + 6) % 7 // Monday = 0
	sched := func(i int) (config.DaySchedule, bool) {
		want := config.Weekdays[(i+7)%7]
		for _, d := range bh.Days {
			if d.Day == want {
				return d, true
			}
		}
		return config.DaySchedule{}, false
	}
	if d, ok := sched(dayIdx); ok && d.Open && !todayOff {
		s, ok1 := parseClock(d.Start)
		e, ok2 := parseClock(d.End)
		if ok1 && ok2 {
			if s < e && minute >= s && minute < e {
				return true
			}
			if s > e && minute >= s {
				return true
			}
			if s == e {
				return true
			}
		}
	}
	if d, ok := sched(dayIdx - 1); ok && d.Open && !yesterdayOff {
		s, ok1 := parseClock(d.Start)
		e, ok2 := parseClock(d.End)
		if ok1 && ok2 && s > e && minute < e {
			return true
		}
	}
	return false
}
