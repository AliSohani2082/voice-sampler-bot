package main

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// Every sentence /onboard and /profile can show. They are consts because
// translate works on whole strings, so each question and each rejection has to be
// one authored sentence rather than something composed at runtime.
const (
	onboardIntro = "Before you record, the class needs a few details about you. They are stored with your recordings for the course dataset. Your first and last name are also shown to other participants on the /leader board; nothing else about you is."
	askGender    = "What is your gender?"
	askAge       = "How old are you? Send your age as a number."
	badAge       = "Please send your age as a number between 10 and 100."
	askFirstName = "What is your first name?"
	askLastName  = "What is your last name?"
	askStudent   = "What is your student number (شماره دانشجویی)?"
	badStudent   = "Please send your student number as 5 to 20 digits."
	dupStudent   = "That student number is already registered to another account. Please check it and send it again."
	profileCard  = "Your profile\nName: %s %s\nGender: %s\nAge: %d\nStudent number: %s"
	profileSaved = "Profile saved. You can start recording now."
)

type profile struct {
	Gender        string // 'male' or 'female', as the profiles table stores it
	Age           int
	FirstName     string
	LastName      string
	StudentNumber string
}

// args turns a stored profile back into collector answers, so editing one field
// can seed the other four and leave exactly one question to ask.
func (p profile) args() cmdArgs {
	a := newArgs()
	a.setValue("gender", genderToken(p.Gender))
	a.setValue("age", strconv.Itoa(p.Age))
	a.setValue("first", p.FirstName)
	a.setValue("last", p.LastName)
	a.setValue("student", p.StudentNumber)
	return a
}

// genderToken is the questionnaire token for a stored gender.
func genderToken(word string) string {
	if word == "female" {
		return "f"
	}
	return "m"
}

// genderWord is the stored gender for a questionnaire token.
func genderWord(token string) string {
	if token == "f" {
		return "female"
	}
	return "male"
}

// genderLabel is a stored gender in the reader's own language.
func genderLabel(ctx context.Context, word string) string {
	if word == "female" {
		return translate(ctx, "Female")
	}
	return translate(ctx, "Male")
}

// profileFields are the five questionnaire slots, shared by /onboard and /profile
// so the questions, their validation and their order exist in one place.
func profileFields() []argSpec {
	return []argSpec{
		{name: "gender", kind: argChoice, mandatory: true, ask: askGender,
			options: []argOption{{"m", "Male"}, {"f", "Female"}}},
		{name: "age", kind: argText, mandatory: true, ask: askAge, maxRunes: 8,
			validate: numberBetween(10, 100, badAge)},
		{name: "first", kind: argText, mandatory: true, ask: askFirstName, maxRunes: 64,
			validate: plainLine(askFirstName)},
		{name: "last", kind: argText, mandatory: true, ask: askLastName, maxRunes: 64,
			validate: plainLine(askLastName)},
		{name: "student", kind: argText, mandatory: true, ask: askStudent, maxRunes: 24,
			validate: digitString(5, 20, badStudent)},
	}
}

// profileEditOptions are the profile card's buttons. The tokens are two letters so
// none of them collides with the gender tokens m and f in the same command.
var profileEditOptions = []argOption{
	{"fn", "Edit first name"},
	{"ln", "Edit last name"},
	{"ge", "Edit gender"},
	{"ag", "Edit age"},
	{"sn", "Edit student number"},
}

// profileEditSlot maps a card button to the slot it re-asks.
func profileEditSlot(token string) string {
	switch token {
	case "fn":
		return "first"
	case "ln":
		return "last"
	case "ge":
		return "gender"
	case "ag":
		return "age"
	case "sn":
		return "student"
	}
	return ""
}

// getProfile reads a participant's profile. The bool reports whether one
// exists, which is the whole test for "has this participant onboarded".
func (a *app) getProfile(ctx context.Context, userID int64) (profile, bool, error) {
	var p profile
	err := a.db.QueryRow(ctx, `SELECT gender,age,first_name,last_name,student_number FROM profiles WHERE user_id=$1`, userID).
		Scan(&p.Gender, &p.Age, &p.FirstName, &p.LastName, &p.StudentNumber)
	if errors.Is(err, pgx.ErrNoRows) {
		return p, false, nil
	}
	if err != nil {
		return p, false, err
	}
	return p, true, nil
}

// putProfile writes all five answers as one row. A student number another
// account already holds surfaces as SQLSTATE 23505, which the caller recovers
// from by asking for that one field again.
func (a *app) putProfile(ctx context.Context, userID int64, p profile) error {
	_, err := a.db.Exec(ctx, `INSERT INTO profiles(user_id,gender,age,first_name,last_name,student_number,updated_at) VALUES($1,$2,$3,$4,$5,$6,now()) ON CONFLICT(user_id) DO UPDATE SET gender=$2,age=$3,first_name=$4,last_name=$5,student_number=$6,updated_at=now()`,
		userID, p.Gender, p.Age, p.FirstName, p.LastName, p.StudentNumber)
	return err
}

// showProfile renders the card: the five stored values, and a button per field.
func (a *app) showProfile(ctx context.Context, userID int64, p profile) error {
	text := fmt.Sprintf(translate(ctx, profileCard), p.FirstName, p.LastName, genderLabel(ctx, p.Gender), p.Age, p.StudentNumber)
	spec := lookupCommand("profile")
	var rows [][]map[string]string
	var row []map[string]string
	for _, o := range profileEditOptions {
		row = append(row, button(o.label, spec.callbackData(newArgs(), o.token)))
		if len(row) == 2 {
			rows, row = append(rows, row), nil
		}
	}
	if len(row) > 0 {
		rows = append(rows, row)
	}
	return a.send(ctx, userID, text, rows)
}

// startOnboarding explains why the details are needed, then asks the first
// question. The collector drives the rest of the questionnaire.
func (a *app) startOnboarding(ctx context.Context, userID int64) error {
	if err := a.send(ctx, userID, onboardIntro, nil); err != nil {
		return err
	}
	spec := lookupCommand("onboard")
	// Resume a half-finished questionnaire rather than opening with a question
	// the participant has already answered. This path is reached from /start and
	// from the /new gate, neither of which goes through runCommand's own restore.
	answers, stored := newArgs(), false
	s, err := a.getSession(ctx, userID)
	if err != nil {
		return err
	}
	if stage, ok := strings.CutPrefix(s.Stage, awaitPrefix); ok && stage == spec.name {
		if restored, ok := decodePending(spec, s.Pending); ok {
			answers, stored = restored, true
		}
	}
	return a.collect(ctx, userID, spec, answers, stored)
}

// onboardIfNew onboards a participant who has no profile yet, so a new user is
// asked straight after choosing a language.
func (a *app) onboardIfNew(ctx context.Context, userID int64) error {
	if _, ok, err := a.getProfile(ctx, userID); err != nil || ok {
		return err
	}
	return a.startOnboarding(ctx, userID)
}

// requireProfile is the hard gate on recording: without a profile no pair starts,
// and the questionnaire runs instead.
func requireProfile(a *app, ctx context.Context, userID int64, _ cmdArgs) (bool, cmdArgs, error) {
	if _, ok, err := a.getProfile(ctx, userID); err != nil || ok {
		return false, cmdArgs{}, err
	}
	return true, cmdArgs{}, a.startOnboarding(ctx, userID)
}

// profileAction is what an /onboard or /profile invocation should do.
type profileAction uint8

const (
	actionQuestionnaire profileAction = iota // ask the five questions
	actionCard                               // show the stored profile
	actionEditField                          // re-ask one field, seeding the rest
)

// profileEntry decides between the three, from whether a profile exists and which
// field a card button asked to edit.
//
// It is pure so that the hand-off between /onboard and /profile can be checked
// without a database. Both commands consult this one function with the same
// inputs, so they cannot disagree about a given state — which is what makes
// bouncing a user between them impossible, rather than merely unlikely.
func profileEntry(hasProfile bool, editToken string) profileAction {
	if !hasProfile {
		return actionQuestionnaire
	}
	if profileEditSlot(editToken) != "" {
		return actionEditField
	}
	return actionCard
}

// onboardPrecheck sends the card instead of the questionnaire once a participant
// has a profile, so /onboard and /profile lead to the same place. With no profile
// it falls through, and the collector asks the first question.
func onboardPrecheck(a *app, ctx context.Context, userID int64, _ cmdArgs) (bool, cmdArgs, error) {
	stored, ok, err := a.getProfile(ctx, userID)
	if err != nil {
		return false, cmdArgs{}, err
	}
	if profileEntry(ok, "") != actionCard {
		return false, cmdArgs{}, nil
	}
	if err := a.clearCollection(ctx, userID); err != nil {
		return false, cmdArgs{}, err
	}
	return true, cmdArgs{}, a.showProfile(ctx, userID, stored)
}

// profilePrecheck shows the card, or seeds every field except the one being edited
// so the collector has exactly one question left to ask. With no profile there is
// nothing to show, so it starts the questionnaire.
func profilePrecheck(a *app, ctx context.Context, userID int64, p cmdArgs) (bool, cmdArgs, error) {
	stored, ok, err := a.getProfile(ctx, userID)
	if err != nil {
		return false, cmdArgs{}, err
	}
	switch profileEntry(ok, p.get("edit")) {
	case actionQuestionnaire:
		return true, cmdArgs{}, a.startOnboarding(ctx, userID)
	case actionCard:
		// The card ends an edit that was waiting on an answer, so its collection
		// must go with it: otherwise the next ordinary message is read as that
		// answer and silently rewrites the field.
		if err := a.clearCollection(ctx, userID); err != nil {
			return false, cmdArgs{}, err
		}
		return true, cmdArgs{}, a.showProfile(ctx, userID, stored)
	}
	seed := stored.args()
	delete(seed.values, profileEditSlot(p.get("edit")))
	return false, seed, nil
}

// saveProfileWith builds the run shared by /onboard and /profile. It needs the
// command's own name so that re-asking after a duplicate student number continues
// the same command the user was in.
func saveProfileWith(name string) func(*app, context.Context, int64, cmdArgs) error {
	return func(a *app, ctx context.Context, userID int64, p cmdArgs) error {
		age, err := strconv.Atoi(p.get("age"))
		if err != nil {
			return a.send(ctx, userID, badAge, nil)
		}
		stored := profile{
			Gender:        genderWord(p.get("gender")),
			Age:           age,
			FirstName:     p.get("first"),
			LastName:      p.get("last"),
			StudentNumber: p.get("student"),
		}
		if err := a.putProfile(ctx, userID, stored); err != nil {
			var pgErr *pgconn.PgError
			if !errors.As(err, &pgErr) || pgErr.Code != "23505" {
				return err
			}
			// Another account holds this student number. Keep the four good
			// answers and ask for that one again.
			again := newArgs()
			again.fill(p)
			delete(again.values, "student")
			if err := a.send(ctx, userID, dupStudent, nil); err != nil {
				return err
			}
			return a.collect(ctx, userID, lookupCommand(name), again, false)
		}
		if p.has("edit") {
			return a.showProfile(ctx, userID, stored)
		}
		return a.sendWithKeyboard(ctx, userID, profileSaved)
	}
}
