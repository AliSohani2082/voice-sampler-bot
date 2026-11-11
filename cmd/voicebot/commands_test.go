package main

import (
	"regexp"
	"strings"
	"testing"
)

var (
	commandNamePattern = regexp.MustCompile(`^[a-z][a-z0-9_]{0,31}$`)
	tokenPattern       = regexp.MustCompile(`^[a-z0-9]{1,8}$`)
)

// widestCallback is the longest callback data a command could ever emit: every
// choice slot answered with its longest token and every flag set.
func widestCallback(c *commandSpec) string {
	p := newArgs()
	for i := range c.args {
		s := &c.args[i]
		switch s.kind {
		case argFlag:
			p.setFlag(s.name)
		case argChoice:
			longest := ""
			for _, o := range s.options {
				if len(o.token) > len(longest) {
					longest = o.token
				}
			}
			p.setValue(s.name, longest)
		}
	}
	return c.callbackData(p, "")
}

func TestCommandSpecsWellFormed(t *testing.T) {
	seen := map[string]bool{}
	for i := range commandSpecs {
		c := &commandSpecs[i]
		if !commandNamePattern.MatchString(c.name) {
			t.Errorf("command name %q is not a usable Telegram command", c.name)
		}
		if seen[c.name] {
			t.Errorf("duplicate command %q", c.name)
		}
		seen[c.name] = true
		if c.run == nil {
			t.Errorf("%s: no run", c.name)
		}
		if len([]rune(c.description)) > 256 {
			t.Errorf("%s: description too long for setMyCommands", c.name)
		}
		// Telegram caps callback_data at 64 bytes, and a prompt's buttons carry
		// every answer so far, so the widest case is what matters.
		if data := widestCallback(c); len(data) >= 64 {
			t.Errorf("%s: callback data can reach %d bytes: %q", c.name, len(data), data)
		}
		slots := map[string]bool{}
		tokens := map[string]string{}
		texts := 0
		for j := range c.args {
			s := &c.args[j]
			if s.name == "" || slots[s.name] {
				t.Errorf("%s: slot %d has a missing or duplicate name %q", c.name, j, s.name)
			}
			slots[s.name] = true
			if s.kind != argText && (s.maxRunes != 0 || s.validate != nil) {
				t.Errorf("%s/%s: maxRunes and validate belong to text slots only", c.name, s.name)
			}
			switch s.kind {
			case argChoice:
				if len(s.options) < 2 {
					t.Errorf("%s/%s: a choice needs at least two options", c.name, s.name)
				}
				if s.mandatory && s.ask == "" {
					t.Errorf("%s/%s: a mandatory choice must have a question to ask", c.name, s.name)
				}
				for _, o := range s.options {
					if !tokenPattern.MatchString(o.token) {
						t.Errorf("%s/%s: token %q must be 1-8 of [a-z0-9]", c.name, s.name, o.token)
					}
					if o.label == "" {
						t.Errorf("%s/%s: token %q has no button label", c.name, s.name, o.token)
					}
					// Two slots sharing a token would make parsing order-dependent.
					if other, dup := tokens[o.token]; dup {
						t.Errorf("%s: token %q is claimed by both %s and %s", c.name, o.token, other, s.name)
					}
					tokens[o.token] = s.name
				}
			case argFlag:
				if !tokenPattern.MatchString(s.token) {
					t.Errorf("%s/%s: flag token %q must be 1-8 of [a-z0-9]", c.name, s.name, s.token)
				}
				if s.mandatory {
					t.Errorf("%s/%s: a flag is present or absent, never mandatory", c.name, s.name)
				}
				if other, dup := tokens[s.token]; dup {
					t.Errorf("%s: token %q is claimed by both %s and %s", c.name, s.token, other, s.name)
				}
				tokens[s.token] = s.name
			case argText:
				texts++
				if !s.mandatory {
					t.Errorf("%s/%s: an optional text slot can never be supplied", c.name, s.name)
				}
				if s.ask == "" {
					t.Errorf("%s/%s: a text slot must have a question to ask", c.name, s.name)
				}
			}
		}
		// One text slot alone can be read off the command line; several can only
		// be answered one question at a time, which soleTextSlot has to report.
		if texts > 1 && c.soleTextSlot() != -1 {
			t.Errorf("%s: %d text slots cannot be parsed positionally", c.name, texts)
		}
	}
}

// TestMenuCommands owns the expected command list. Everything else derives from
// the registry, so adding a command is a deliberate edit here rather than a
// surprise in the menu.
func TestMenuCommands(t *testing.T) {
	public := []string{"start", "new", "my", "onboard", "profile", "leader", "help", "cancel", "download"}
	adminOnly := []string{"stats", "users", "broadcast", "allow", "unallow"}

	names := func(menu []map[string]string) []string {
		var out []string
		for _, c := range menu {
			out = append(out, c["command"])
		}
		return out
	}
	if got := strings.Join(names(menuCommands(false)), " "); got != strings.Join(public, " ") {
		t.Errorf("public menu = %q", got)
	}
	if got := strings.Join(names(menuCommands(true)), " "); got != strings.Join(append(append([]string{}, public...), adminOnly...), " ") {
		t.Errorf("admin menu = %q", got)
	}
	admins := map[string]bool{}
	for _, name := range adminOnly {
		admins[name] = true
	}
	for i := range commandSpecs {
		c := &commandSpecs[i]
		if c.adminOnly != admins[c.name] {
			t.Errorf("%s: adminOnly = %v", c.name, c.adminOnly)
		}
	}
}

func TestParse(t *testing.T) {
	for _, tc := range []struct {
		command string
		rest    string
		values  map[string]string
		flags   []string
		missing int
		bad     string
	}{
		{command: "leader", rest: "", missing: 1},
		{command: "leader", rest: "d", values: map[string]string{"period": "d"}},
		{command: "leader", rest: "W", values: map[string]string{"period": "w"}},
		{command: "leader", rest: "d l", values: map[string]string{"period": "d"}, flags: []string{"detail"}},
		{command: "leader", rest: "l d", values: map[string]string{"period": "d"}, flags: []string{"detail"}},
		{command: "leader", rest: "d d", values: map[string]string{"period": "d"}},
		{command: "leader", rest: "l", flags: []string{"detail"}, missing: 1},
		{command: "leader", rest: "d w", bad: "w"},
		{command: "leader", rest: "x", bad: "x"},
		{command: "download", rest: "today", values: map[string]string{"range": "today"}},
		{command: "help", rest: "whatever it says"},
		{command: "allow", rest: "", missing: 1},
		{command: "allow", rest: "  12  ", values: map[string]string{"id": "12"}},
		// Text answers are never read off the command line when a command has
		// several of them: only the choice slots match, and anything left over is
		// reported rather than silently dropped.
		{command: "onboard", rest: "", missing: 5},
		{command: "onboard", rest: "m", values: map[string]string{"gender": "m"}, missing: 4},
		{command: "onboard", rest: "m 22", bad: "22"},
		// The profile card's buttons must still parse, text slots notwithstanding.
		{command: "profile", rest: "sn", values: map[string]string{"edit": "sn"}, missing: 5},
	} {
		name := tc.command + " " + tc.rest
		c := lookupCommand(tc.command)
		if c == nil {
			t.Fatalf("%s: no such command", name)
		}
		p, missing, bad := c.parse(tc.rest)
		if bad != tc.bad {
			t.Errorf("%s: bad = %q, want %q", name, bad, tc.bad)
			continue
		}
		if len(missing) != tc.missing {
			t.Errorf("%s: %d missing, want %d", name, len(missing), tc.missing)
		}
		for slot, want := range tc.values {
			if got := p.get(slot); got != want {
				t.Errorf("%s: %s = %q, want %q", name, slot, got, want)
			}
		}
		for _, flag := range tc.flags {
			if !p.has(flag) {
				t.Errorf("%s: flag %s not set", name, flag)
			}
		}
	}
}

// TestPromptCallbacksRoundTrip is the guarantee that answering a question by
// button is the same thing as having typed the command with that answer.
func TestPromptCallbacksRoundTrip(t *testing.T) {
	for i := range commandSpecs {
		c := &commandSpecs[i]
		for j := range c.args {
			s := &c.args[j]
			if s.kind != argChoice {
				continue
			}
			for _, o := range s.options {
				data := c.callbackData(newArgs(), o.token)
				if len(data) >= 64 {
					t.Errorf("%s: callback data too long: %q", c.name, data)
				}
				parts := strings.SplitN(data, ":", 3)
				if len(parts) != 3 || parts[0] != "cmd" || parts[1] != c.name {
					t.Errorf("%s: callback data %q is not cmd:<name>:<tokens>", c.name, data)
					continue
				}
				p, _, bad := c.parse(strings.ReplaceAll(parts[2], ".", " "))
				if bad != "" || p.get(s.name) != o.token {
					t.Errorf("%s/%s: %q parsed back as %q, bad %q", c.name, s.name, data, p.get(s.name), bad)
				}
			}
		}
	}
	// A flag already supplied has to survive the question it is waiting on.
	leader := lookupCommand("leader")
	p := newArgs()
	p.setFlag("detail")
	data := leader.callbackData(p, "d")
	answered, _, bad := leader.parse(strings.ReplaceAll(strings.SplitN(data, ":", 3)[2], ".", " "))
	if bad != "" || answered.get("period") != "d" || !answered.has("detail") {
		t.Errorf("%q lost the detail flag: %+v, bad %q", data, answered, bad)
	}
}

// TestRegistryStringsAreTranslated keeps the next command from shipping
// English-only text to Persian users. Admin-facing strings stay English, as the
// rest of the admin surface does.
func TestRegistryStringsAreTranslated(t *testing.T) {
	// Deliberately untranslated: the language picker names each language in its
	// own script, and its prompt is already bilingual.
	exempt := map[string]bool{
		"English": true,
		"فارسی":   true,
		"Choose your language / زبان خود را انتخاب کنید:": true,
	}
	check := func(what, s string) {
		if s == "" || exempt[s] || fa[s] != "" {
			return
		}
		t.Errorf("%s has no Persian translation: %q", what, s)
	}
	for i := range commandSpecs {
		c := &commandSpecs[i]
		if c.adminOnly {
			continue
		}
		for j := range c.args {
			s := &c.args[j]
			check(c.name+"/"+s.name+" question", s.ask)
			for _, o := range s.options {
				check(c.name+"/"+s.name+" label", o.label)
			}
			if s.validate != nil {
				if _, reply := s.validate(""); reply != "" {
					check(c.name+"/"+s.name+" rejection", reply)
				}
			}
		}
	}
	for _, s := range []string{unknownCommand, unrecognizedOption, canceled, busyWithPair} {
		check("dispatcher reply", s)
	}
}
