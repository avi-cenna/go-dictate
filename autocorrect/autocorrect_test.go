package autocorrect

import "testing"

// These test the engine with a small table of their own, so they don't depend
// on the gitignored patterns.go. Tests for the real table go in the gitignored
// patterns_test.go.
var testPatterns = []Pattern{
	{Kind: Always, Match: `u+m+`},
	{Kind: Always, Match: `u+h+`},
	{Kind: Delimited, Match: `you know`},
	{Kind: Leading, Match: `so`},
	{Kind: Trailing, Match: `or something`},
	{Kind: Regex, Match: `\b(she|he) was like (\w+ing)\b`, Replace: `${1} was ${2}`},
	{Kind: Regex, Match: `\b(it['’]s|that['’]s) kind of (\w+)`, Replace: `${1} ${2}`, Unless: `of you$`},
	{Kind: Vocab, Match: `cloud code`, Replace: `Claude Code`},
}

func TestCorrectWith(t *testing.T) {
	rules := compile(testPatterns)

	tests := []struct {
		name, in, want string
	}{
		// Always
		{"always between commas", "I had, um, a dream.", "I had a dream."},
		{"always sentence start", "Um, what was I saying?", "What was I saying?"},
		{"always start no comma", "Done. Um I think so.", "Done. I think so."},
		{"always sentence end", "That was it, um.", "That was it."},
		{"always bare", "Get the uh thing.", "Get the thing."},
		{"always not inside words", "Bring an umbrella.", "Bring an umbrella."},
		{"always not hyphenated", "Uh-huh, that works.", "Uh-huh, that works."},

		// Delimited
		{"delimited between commas", "I had, you know, a dream.", "I had a dream."},
		{"delimited sentence start", "You know, it was good.", "It was good."},
		{"delimited sentence end", "It's hard, you know.", "It's hard."},
		{"delimited needs commas", "Do you know the way?", "Do you know the way?"},

		// Leading and Trailing
		{"leading", "So, what now?", "What now?"},
		{"leading needs comma", "So many people came.", "So many people came."},
		{"leading only at start", "I was tired, so I left.", "I was tired, so I left."},
		{"trailing", "Grab lunch or something.", "Grab lunch."},
		{"trailing only at end", "Is it red or something else?", "Is it red or something else?"},

		// Regex, Unless, Vocab
		{"regex groups", "Where she was like talking about it.", "Where she was talking about it."},
		{"regex unless applies", "That's kind of you.", "That's kind of you."},
		{"regex unless passes", "It's kind of weird.", "It's weird."},
		{"vocab", "I use cloud code daily.", "I use Claude Code daily."},

		// Go passes
		{"repeated words", "We went to the the store.", "We went to the store."},
		{"valid repeats", "I had had enough.", "I had had enough."},
		{"filler exposes repeat", "Put it in the, um, the box.", "Put it in the box."},
		{"capitalizes after removal", "Um, so, we left.", "We left."},
		{"iPhone stays", "Uh, iPhone sales were up.", "iPhone sales were up."},
		{"abbreviation not a sentence end", "Um, pick one, e.g. this one.", "Pick one, e.g. this one."},
		{"multibyte", "Um, café naïve 🦀 works.", "Café naïve 🦀 works."},

		// Nothing matched: returned byte-for-byte, no cleanup.
		{"untouched", " lowercase start ,  odd spacing .", " lowercase start ,  odd spacing ."},
		{"empty", "", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := correctWith(tt.in, rules); got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestUnknownKindPanics(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("expected a panic for an unknown Kind")
		}
	}()
	compile([]Pattern{{Kind: Kind(99), Match: "x"}})
}
