package main

import (
	"strconv"
	"strings"
	"testing"
)

func TestNormalizeDigits(t *testing.T) {
	for raw, want := range map[string]string{
		"22":            "22",
		"۲۲":            "22", // Persian, U+06F0-U+06F9
		"٢٢":            "22", // Arabic-Indic, U+0660-U+0669
		"۴۰۱۲۳۴۵۶":      "40123456",
		"۱۲ab٣":         "12ab3",
		"":              "",
		"no digits":     "no digits",
		"۰۱۲۳۴۵۶۷۸۹":    "0123456789",
		"٠١٢٣٤٥٦٧٨٩":    "0123456789",
		"mixed ۵ and 5": "mixed 5 and 5",
	} {
		if got := normalizeDigits(raw); got != want {
			t.Errorf("normalizeDigits(%q) = %q, want %q", raw, got, want)
		}
	}
}

// The questionnaire has to accept the digits a Persian-speaking class actually
// types, and reject everything that is not an age.
func TestAgeValidation(t *testing.T) {
	age := slotByName(t, "onboard", "age")
	for _, raw := range []string{"22", "۲۲", "٢٢", " 22 ", "10", "100"} {
		value, reply := validateSlot(age, raw)
		if reply != "" || value == "" {
			t.Errorf("age %q rejected: %q", raw, reply)
		}
	}
	if value, _ := validateSlot(age, "۲۲"); value != "22" {
		t.Errorf("Persian age stored as %q, want %q", value, "22")
	}
	for _, raw := range []string{"", "   ", "9", "101", "0", "abc", "22abc", "-5", "22.5", "۹"} {
		if value, reply := validateSlot(age, raw); reply == "" {
			t.Errorf("age %q accepted as %q", raw, value)
		}
	}
}

func TestStudentNumberValidation(t *testing.T) {
	student := slotByName(t, "onboard", "student")
	for _, raw := range []string{"12345", "40123456", "۴۰۱۲۳۴۵۶", "٤٠١٢٣٤٥٦", "00012345678901234567"} {
		if value, reply := validateSlot(student, raw); reply != "" || value == "" {
			t.Errorf("student number %q rejected: %q", raw, reply)
		}
	}
	// Leading zeros have to survive, so the value is kept as text.
	if value, _ := validateSlot(student, "00123"); value != "00123" {
		t.Errorf("leading zeros lost: %q", value)
	}
	for _, raw := range []string{"", "1234", "123456789012345678901", "40a23456", "4012 3456"} {
		if value, reply := validateSlot(student, raw); reply == "" {
			t.Errorf("student number %q accepted as %q", raw, value)
		}
	}
}

func TestNameValidation(t *testing.T) {
	for _, slot := range []string{"first", "last"} {
		s := slotByName(t, "onboard", slot)
		if value, reply := validateSlot(s, "  Ali  "); reply != "" || value != "Ali" {
			t.Errorf("%s: %q, %q", slot, value, reply)
		}
		if _, reply := validateSlot(s, "علی"); reply != "" {
			t.Errorf("%s rejected a Persian name: %q", slot, reply)
		}
		for _, raw := range []string{"", "   ", strings.Repeat("a", 65)} {
			if value, reply := validateSlot(s, raw); reply == "" {
				t.Errorf("%s accepted %d chars as %q", slot, len(raw), value)
			}
		}
	}
}

// The order the questions are asked in is the slot order, and it is the one thing
// about the questionnaire a reader would expect to be able to check at a glance.
func TestQuestionnaireOrder(t *testing.T) {
	var got []string
	for _, s := range profileFields() {
		got = append(got, s.name)
	}
	if want := "gender age first last student"; strings.Join(got, " ") != want {
		t.Errorf("questionnaire asks %q, want %q", strings.Join(got, " "), want)
	}
}

// Editing one field seeds the other four, so the collector has exactly one
// question left. This is the whole editing feature, so it is worth pinning.
func TestEditSeedsEveryOtherField(t *testing.T) {
	spec := lookupCommand("profile")
	stored := profile{Gender: "female", Age: 22, FirstName: "Ali", LastName: "Sohani", StudentNumber: "40123456"}
	for _, o := range profileEditOptions {
		editing := profileEditSlot(o.token)
		if editing == "" {
			t.Fatalf("card button %q maps to no slot", o.token)
		}
		seed := stored.args()
		delete(seed.values, editing)
		missing := spec.missingIn(seed)
		if len(missing) != 1 || spec.args[missing[0]].name != editing {
			var names []string
			for _, i := range missing {
				names = append(names, spec.args[i].name)
			}
			t.Errorf("editing %s leaves %v to ask, want just %s", editing, names, editing)
		}
	}
}

// Every card button has to come back through the same parser as a typed command.
func TestProfileCardButtonsRoundTrip(t *testing.T) {
	spec := lookupCommand("profile")
	for _, o := range profileEditOptions {
		data := spec.callbackData(newArgs(), o.token)
		if len(data) >= 64 {
			t.Errorf("%q is too long for callback data", data)
		}
		parts := strings.SplitN(data, ":", 3)
		if len(parts) != 3 {
			t.Fatalf("%q is not cmd:<name>:<tokens>", data)
		}
		p, _, bad := spec.parse(strings.ReplaceAll(parts[2], ".", " "))
		if bad != "" || p.get("edit") != o.token {
			t.Errorf("%q parsed back as edit=%q, bad %q", data, p.get("edit"), bad)
		}
		if profileEditSlot(p.get("edit")) == "" {
			t.Errorf("%q resolves to no slot", data)
		}
	}
}

// /onboard and /profile hand off to each other, so they must never disagree about
// what a given state calls for — otherwise they could bounce a user in a loop.
// Both commands consult this one decision, so their agreeing is structural and
// not something a test could break. What is worth pinning is the decision itself,
// and in particular that every action it returns is terminal: the questionnaire,
// the card and a field edit are all done directly, so neither command ever hands
// back to the other.
func TestOnboardAndProfileAgreeOnEntry(t *testing.T) {
	for _, tc := range []struct {
		hasProfile bool
		edit       string
		want       profileAction
	}{
		{false, "", actionQuestionnaire},
		{false, "sn", actionQuestionnaire}, // nothing to edit yet
		{true, "", actionCard},
		{true, "sn", actionEditField},
		{true, "nope", actionCard}, // an unknown token is not an edit
	} {
		if got := profileEntry(tc.hasProfile, tc.edit); got != tc.want {
			t.Errorf("profileEntry(%v, %q) = %v, want %v", tc.hasProfile, tc.edit, got, tc.want)
		}
	}
}

func TestGenderRoundTrip(t *testing.T) {
	for token, word := range map[string]string{"m": "male", "f": "female"} {
		if got := genderWord(token); got != word {
			t.Errorf("genderWord(%q) = %q", token, got)
		}
		if got := genderToken(word); got != token {
			t.Errorf("genderToken(%q) = %q", word, got)
		}
	}
	// The gender a profile stores has to be a token the questionnaire accepts.
	gender := slotByName(t, "onboard", "gender")
	for _, word := range []string{"male", "female"} {
		found := false
		for _, o := range gender.options {
			if o.token == genderToken(word) {
				found = true
			}
		}
		if !found {
			t.Errorf("stored gender %q has no matching option", word)
		}
	}
}

// A stored profile must survive the round trip into collector answers and back,
// or editing one field would quietly rewrite the others.
func TestProfileArgsRoundTrip(t *testing.T) {
	stored := profile{Gender: "female", Age: 22, FirstName: "Ali", LastName: "Sohani", StudentNumber: "40123456"}
	p := stored.args()
	age, err := strconv.Atoi(p.get("age"))
	if err != nil {
		t.Fatalf("age %q: %v", p.get("age"), err)
	}
	back := profile{
		Gender:        genderWord(p.get("gender")),
		Age:           age,
		FirstName:     p.get("first"),
		LastName:      p.get("last"),
		StudentNumber: p.get("student"),
	}
	if back != stored {
		t.Errorf("round trip gave %+v, want %+v", back, stored)
	}
}

func slotByName(t *testing.T, command, slot string) argSpec {
	t.Helper()
	c := lookupCommand(command)
	if c == nil {
		t.Fatalf("no command %q", command)
	}
	for _, s := range c.args {
		if s.name == slot {
			return s
		}
	}
	t.Fatalf("%s has no slot %q", command, slot)
	return argSpec{}
}
