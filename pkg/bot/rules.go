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
	"whatsapp-automation/pkg/msgtemplate"
)

// Normalize lowercases text, collapses whitespace and strips surrounding punctuation. It
// also drops invisible zero-width characters and turns full-width letters (ＳＴＯＰ) into
// plain ones, so an opt-out cannot slip through looking like "STOP" but not matching.
func Normalize(text string) string {
	text = strings.Map(func(r rune) rune {
		switch {
		case r == 0x200B, r == 0x200C, r == 0x200D, r == 0x2060, r == 0xFEFF: // zero-width
			return -1
		case r >= 0xFF01 && r <= 0xFF5E: // full-width ASCII
			return r - 0xFEE0
		case r == 0x3000: // ideographic space
			return ' '
		}
		return r
	}, text)
	t := strings.ToLower(strings.Join(strings.Fields(text), " "))
	return strings.TrimFunc(t, func(r rune) bool {
		return unicode.IsPunct(r) || unicode.IsSymbol(r) || unicode.IsSpace(r)
	})
}

// CleanDisplayText removes control characters and the direction overrides that can make
// a name or message look like something else (U+202E turns "fdp.exe" into "exe.pdf")
// before it is stored or shown (S43). Line breaks and tabs are kept in messages only;
// the joiners used by emoji are kept.
func CleanDisplayText(text string, multiline bool) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r == '\n' || r == '\t':
			if multiline {
				return r
			}
			return ' '
		case r < 0x20, r == 0x7F, r >= 0x80 && r <= 0x9F: // C0 and C1 controls (NUL, \r)
			return -1
		case r >= 0x202A && r <= 0x202E, r >= 0x2066 && r <= 0x2069: // direction overrides and isolates
			return -1
		}
		return r
	}, text)
}

var optOutWords = map[string]bool{
	"stop": true, "stop all": true, "unsubscribe": true, "optout": true, "opt-out": true, "opt out": true,
	"no more messages": true, "stop messages": true, "stop messaging": true, "remove me": true,
	// "Stop promotions" is the label of the opt-out button on Meta's marketing templates:
	// a tap arrives as that text.
	"stop promotions": true, "stop promotion": true, "stop promo": true, "stop marketing": true,
	"unsubscribe me": true, "please unsubscribe": true, "unsubscribe please": true,
	"stop sending": true, "stop sending messages": true, "stop sending me messages": true, "please stop": true,
	"do not message me": true, "dont message me": true, "don't message me": true,
	// DND is how Indian customers know the opt-out from SMS ("do not disturb").
	"dnd": true, "do not disturb": true, "dont disturb": true, "don't disturb": true, "stop na": true,
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
	"want a human", "want a person", "want to speak", "real person",
	"baat karni hai", "baat karna hai", "kisi se baat", "insaan se baat",
}

// lookalikes maps Cyrillic and Greek letters that look like Latin ones, so "ЅТОР" typed
// with look-alike letters still reads as STOP. Used only for opt-out and hand-over
// detection, never for rule matching, so customers writing in Russian are unaffected.
var lookalikes = strings.NewReplacer(
	"а", "a", "в", "b", "е", "e", "ё", "e", "к", "k", "м", "m", "н", "h", "о", "o", "р", "p", "с", "c", "т", "t", "у", "y", "х", "x", "ѕ", "s", "і", "i", "ј", "j", "ԁ", "d", "ӏ", "l",
	"α", "a", "β", "b", "ε", "e", "ι", "i", "κ", "k", "ν", "v", "ο", "o", "ρ", "p", "τ", "t", "υ", "u", "χ", "x",
)

// intentText normalises text for opt-out and hand-over detection.
func intentText(text string) string { return Normalize(lookalikes.Replace(strings.ToLower(text))) }

// optOutSingle are the standard carrier opt-out words (STOP, STOPALL, UNSUBSCRIBE,
// CANCEL, END, QUIT) when sent as the whole message.
var optOutSingle = map[string]bool{"cancel": true, "quit": true, "end": true, "stopall": true}

// optOutPhrases mark an opt-out anywhere in a short message ("please stop messaging me").
var optOutPhrases = []string{
	"stop messaging", "stop sending", "stop texting", "stop contacting", "stop the messages", "stop these messages",
	"dont message me", "don't message me", "do not message me", "dont text me", "don't text me", "do not text me",
	"dont contact me", "don't contact me", "do not contact me", "dont send me", "don't send me", "do not send me",
	"remove me from", "remove my number", "delete my number", "unsubscribe", "opt me out", "opt out", "opt-out",
	"mat bhejo", "band karo", "band kar do", "message band",
	"do not disturb", "dont disturb", "don't disturb",
}

// negations turn a short "stop" message into something else ("don't stop"). "na" is not
// one: in Hinglish "stop na" means "please stop".
var negations = map[string]bool{"dont": true, "don't": true, "not": true, "never": true, "nahi": true}

// IsOptOut reports whether text is a request to stop messages: a known word or phrase,
// or a message of up to three words that starts or ends with "stop" (P4). Customers rarely use the
// exact keyword, and an ignored opt-out leads to blocks and reports.
func IsOptOut(text string) bool {
	n := intentText(text)
	if optOutWords[n] || optOutSingle[n] {
		return true
	}
	words := strings.Fields(n)
	if len(words) == 0 || len(words) > 12 {
		return false
	}
	for _, p := range optOutPhrases {
		if strings.Contains(n, p) {
			return true
		}
	}
	if len(words) > 3 { // "stop by the shop tomorrow" is not an opt-out
		return false
	}
	for _, w := range words {
		if negations[strings.Trim(w, ".,!?")] {
			return false // "don't stop", "not stop"
		}
	}
	for _, w := range words {
		if strings.Trim(w, ".,!?") == "dnd" {
			return true // "dnd please", "add to dnd"
		}
	}
	first, last := strings.Trim(words[0], ".,!?"), strings.Trim(words[len(words)-1], ".,!?")
	return first == "stop" || last == "stop"
}

// IsOptIn reports whether text is a request to resume messages.
func IsOptIn(text string) bool { return optInWords[Normalize(text)] }

// IsHandoverRequest reports whether the customer is asking for a person.
// A short message naming one of these words asks for a person ("AGENT please",
// "I want a human being") (P5).
var handoverKeywords = map[string]bool{"agent": true, "human": true, "representative": true, "insaan": true}

func IsHandoverRequest(text string) bool {
	n := intentText(text)
	if handoverWords[n] {
		return true
	}
	for _, p := range handoverPhrases {
		if strings.Contains(n, p) {
			return true
		}
	}
	if words := strings.Fields(n); len(words) <= 8 {
		for _, w := range words {
			if handoverKeywords[strings.Trim(w, ".,!?")] {
				return true
			}
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
func Location(tz string) *time.Location { return msgtemplate.Location(tz) }

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
