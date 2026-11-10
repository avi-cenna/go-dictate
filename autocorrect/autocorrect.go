// Package autocorrect removes dictation fillers ("um", "you know", "like")
// and fixes misheard vocabulary in transcriptions, using a table of regex
// patterns.
//
// The table itself lives in patterns.go, which is gitignored so it can hold
// personal vocabulary. That file fills Patterns from an init func; without it
// the package still builds and Correct only does repeated-word removal.
//
// Go's regexp is RE2: no lookarounds and no backreferences. That's why
// repeated words ("the the") are a Go pass rather than a pattern, and why
// context rules capture their neighbors and put them back with ${1}.
package autocorrect

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
	"sync"
	"unicode"
)

// Kind decides which regexes a Pattern expands into. Every kind matches
// case-insensitively.
type Kind int

const (
	// Always removes a vocal filler ("um", "uh") wherever it appears, along
	// with the commas around it.
	Always Kind = iota
	// Delimited removes a phrase ("you know", "like") only when it's set off
	// by commas or sits at a sentence edge with a comma.
	Delimited
	// Leading removes a phrase only at the start of a sentence, followed by a
	// comma ("So, ...", "Well, ...").
	Leading
	// Trailing removes a phrase at the end of a sentence, comma optional
	// ("... or something.").
	Trailing
	// Regex is a raw regex. Replace can use ${1}-style groups (write ${1}, not
	// $1, when a letter follows). If Unless matches the matched text, it's left
	// alone.
	Regex
	// Vocab replaces a phrase with exactly Replace ("cloud code" -> "Claude Code").
	Vocab
)

// Pattern is one row of the autocorrect table.
type Pattern struct {
	Kind Kind
	// Match is a regex fragment for every kind except Vocab, where it's a plain
	// phrase.
	Match   string
	Replace string // Regex and Vocab only
	Unless  string // Regex only
}

// Patterns runs top to bottom, so longer phrases go before the phrases they
// contain ("you know what I mean" before "you know"). Set by patterns.go.
var Patterns []Pattern

// Doubled words that are real grammar or emphasis, not a stutter.
var validRepeats = []string{"had", "that", "is", "no", "yes", "yeah", "bye", "very", "really", "so", "ha", "blah", "now", "go", "well"}

type rule struct {
	re     *regexp.Regexp
	repl   string
	unless *regexp.Regexp
}

// Compiled on first use rather than at package init, because patterns.go
// sets Patterns from an init func, which runs after package variables.
var rules = sync.OnceValue(func() []rule { return compile(Patterns) })

// Correct returns text with the table applied, repeated words removed, and
// the punctuation and capitalization left behind by removals tidied up. Text
// that nothing matches is returned untouched.
func Correct(text string) string {
	return correctWith(text, rules())
}

func correctWith(text string, rules []rule) string {
	out := text
	for _, r := range rules {
		out = r.apply(out)
	}
	out = removeRepeats(out)
	if out == text {
		return text
	}
	for _, c := range cleanups {
		out = c.re.ReplaceAllString(out, c.repl)
	}
	return capitalizeSentences(out)
}

// A sentence starts at the beginning of the text, after .!? and whitespace,
// or after a newline.
const sentenceStart = `(^|[.!?]\s+|\n\s*)`

func compile(patterns []Pattern) []rule {
	var rules []rule
	add := func(re, repl string) {
		rules = append(rules, rule{re: regexp.MustCompile(`(?i)` + re), repl: repl})
	}

	for _, p := range patterns {
		f := `\b(?:` + p.Match + `)`
		switch p.Kind {
		case Always:
			add(`\s*,\s*`+f+`\s*,`, ``)                    // "had, um, a" -> "had a"
			add(sentenceStart+f+`(?:\s*,\s*|\s+)`, `${1}`) // "Um, so" / "Um so" -> "so"
			add(`\s*,?\s*`+f+`(\s*[.!?]|\s*$)`, `${1}`)    // "done, um." -> "done."
			add(`\s+`+f+`(\s|,|$)`, `${1}`)                // "the um thing" -> "the thing"; "uh-huh" survives
		case Delimited:
			add(`\s*,\s*`+f+`\b\s*,`, ``)
			add(sentenceStart+f+`\b\s*,\s*`, `${1}`)
			add(`\s*,\s*`+f+`(\s*[.!?]|\s*$)`, `${1}`)
		case Leading:
			add(sentenceStart+f+`\b\s*,\s*`, `${1}`)
		case Trailing:
			add(`\s*,?\s*`+f+`(\s*[.!?]|\s*$)`, `${1}`)
		case Regex:
			r := rule{re: regexp.MustCompile(`(?i)` + p.Match), repl: p.Replace}
			if p.Unless != "" {
				r.unless = regexp.MustCompile(`(?i)` + p.Unless)
			}
			rules = append(rules, r)
		case Vocab:
			phrase := strings.ReplaceAll(regexp.QuoteMeta(p.Match), ` `, `\s+`)
			add(`\b`+phrase+`\b`, strings.ReplaceAll(p.Replace, `$`, `$$`))
		default:
			panic(fmt.Sprintf("autocorrect: pattern %q has unknown kind %d", p.Match, p.Kind))
		}
	}
	return rules
}

func (r rule) apply(s string) string {
	if r.unless == nil {
		return r.re.ReplaceAllString(s, r.repl)
	}

	var b []byte
	last := 0
	for _, m := range r.re.FindAllStringSubmatchIndex(s, -1) {
		if r.unless.MatchString(s[m[0]:m[1]]) {
			continue
		}
		b = append(b, s[last:m[0]]...)
		b = r.re.ExpandString(b, r.repl, s, m)
		last = m[1]
	}
	return string(append(b, s[last:]...))
}

var wordRe = regexp.MustCompile(`[\p{L}'’]+`)

// removeRepeats drops the second of two identical words separated only by
// spaces ("the the" -> "the"). RE2 has no backreferences, so it can't be a pattern.
func removeRepeats(s string) string {
	locs := wordRe.FindAllStringIndex(s, -1)
	var b strings.Builder
	last := 0
	for i := 1; i < len(locs); i++ {
		prev, cur := locs[i-1], locs[i]
		gap := s[prev[1]:cur[0]]
		word := s[prev[0]:prev[1]]
		if gap != "" && strings.Trim(gap, " ") == "" &&
			strings.EqualFold(word, s[cur[0]:cur[1]]) &&
			!slices.Contains(validRepeats, strings.ToLower(word)) {
			b.WriteString(s[last:prev[1]])
			last = cur[1]
		}
	}
	b.WriteString(s[last:])
	return b.String()
}

// Debris that removals leave behind, fixed in this order.
var cleanups = []struct {
	re   *regexp.Regexp
	repl string
}{
	{regexp.MustCompile(`,(?:\s*,)+`), `,`},          // "So,, I" -> "So, I"
	{regexp.MustCompile(`,\s*([.!?])`), `${1}`},      // "hard, ." -> "hard."
	{regexp.MustCompile(`[ \t]+([,.!?;:])`), `${1}`}, // "had , a" -> "had, a"
	{regexp.MustCompile(`^[\s,]+`), ``},              // leading space or comma
	{regexp.MustCompile(`[ \t]{2,}`), ` `},           // double spaces
}

var sentenceStartRe = regexp.MustCompile(sentenceStart + `(\p{Ll})`)

// Abbreviations whose period doesn't end a sentence ("e.g. this").
var abbreviations = []string{"e.g.", "i.e.", "etc.", "vs.", "cf.", "approx.", "mr.", "mrs.", "ms.", "dr."}

// capitalizeSentences uppercases the first letter of each sentence, since
// removing an opener ("Um, so we...") leaves a lowercase start.
func capitalizeSentences(s string) string {
	b := []byte(s)
	for _, m := range sentenceStartRe.FindAllStringSubmatchIndex(s, -1) {
		before := strings.ToLower(strings.TrimRight(s[:m[3]], " \t\n"))
		if slices.ContainsFunc(abbreviations, func(a string) bool { return strings.HasSuffix(before, a) }) {
			continue
		}
		// Leave "iPhone"-style words alone.
		rest := s[m[5]:]
		if end := strings.IndexFunc(rest, func(r rune) bool { return !unicode.IsLetter(r) }); end >= 0 {
			rest = rest[:end]
		}
		if strings.IndexFunc(rest, unicode.IsUpper) >= 0 {
			continue
		}
		upper := strings.ToUpper(s[m[4]:m[5]])
		if len(upper) == m[5]-m[4] {
			copy(b[m[4]:], upper)
		}
	}
	return string(b)
}
