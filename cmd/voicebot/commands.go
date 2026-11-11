package main

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"unicode"
)

// Replies the dispatcher itself sends. They are consts so language.go and the
// tests can refer to the same strings the bot shows.
const (
	unknownCommand     = "Choose an option below or open the Menu for commands."
	unrecognizedOption = "I didn't recognize that option."
	canceled           = "Canceled."
	busyWithPair       = "Finish or /cancel your current pair first."
)

// argKind decides how the text after the command word becomes a value.
type argKind uint8

const (
	argChoice argKind = iota // one token out of a fixed set: /leader d
	argFlag                  // a single token, present or absent: /leader d l
	argText                  // free text supplied in a reply: an age, a name, a broadcast body
)

// argOption is one accepted token of a choice slot, with the button label the bot
// shows when it has to ask for that slot.
type argOption struct {
	token string // what the user types; [a-z0-9]{1,8}, never ':' or '.'
	label string // English button label; a.send runs it through translate
}

type argSpec struct {
	name      string // slot name for cmdArgs lookups; never shown to the user
	kind      argKind
	mandatory bool        // argChoice and argText only
	token     string      // argFlag: the token that sets it
	options   []argOption // argChoice: accepted tokens, in button order
	perRow    int         // choice prompt layout; 0 means 2
	ask       string      // the question put to the user when this slot is missing
	maxRunes  int         // argText, 0 = unlimited
	// validate canonicalizes a text value. A non-empty reply means the value is
	// unusable and that sentence is what the user sees; it must be one of a small
	// fixed set of sentences, because translate works on whole strings.
	validate func(string) (value, reply string)
}

type commandSpec struct {
	name        string // without the slash, lowercase
	description string // setMyCommands description; empty keeps it out of the menu
	adminOnly   bool   // full admins only: both dispatch and menu visibility
	args        []argSpec
	// precheck runs before any argument collection and may handle the invocation
	// itself (the profile gate, the profile card). Its seed fills slots the user
	// should not be asked about. Together with a command that collects() this is
	// the only reason runCommand reads the database — see runCommand.
	precheck func(a *app, ctx context.Context, userID int64, p cmdArgs) (handled bool, seed cmdArgs, err error)
	run      func(a *app, ctx context.Context, userID int64, p cmdArgs) error
}

// cmdArgs holds one invocation's resolved slots. Reads are nil-map-safe, so the
// zero value is a usable "nothing supplied yet".
type cmdArgs struct {
	values map[string]string // choice token / text value, by slot name
	flags  map[string]bool   // flag presence, by slot name
}

// newArgs returns args with its maps ready to write to.
func newArgs() cmdArgs {
	return cmdArgs{values: map[string]string{}, flags: map[string]bool{}}
}

// get returns a slot's value, or "" when it is unanswered.
func (p cmdArgs) get(name string) string { return p.values[name] }

// has reports whether a slot has been answered, by value or by flag.
func (p cmdArgs) has(name string) bool { return p.flags[name] || p.values[name] != "" }

// setValue answers a choice or text slot.
func (p *cmdArgs) setValue(name, value string) {
	if p.values == nil {
		p.values = map[string]string{}
	}
	p.values[name] = value
}

// setFlag marks a flag slot present.
func (p *cmdArgs) setFlag(name string) {
	if p.flags == nil {
		p.flags = map[string]bool{}
	}
	p.flags[name] = true
}

// fill copies values from src into the slots p leaves unanswered, so callers can
// layer sources by precedence: what the user supplied now, then a precheck seed,
// then a stored collection.
func (p *cmdArgs) fill(src cmdArgs) {
	for name, value := range src.values {
		if p.values[name] == "" {
			p.setValue(name, value)
		}
	}
	for name, on := range src.flags {
		if on && !p.flags[name] {
			p.setFlag(name)
		}
	}
}

// lookupCommand finds a command by name, or returns nil. It hands back a
// pointer into the registry, which callers must treat as read-only.
func lookupCommand(name string) *commandSpec {
	for i := range commandSpecs {
		if commandSpecs[i].name == name {
			return &commandSpecs[i]
		}
	}
	return nil
}

// soleTextSlot reports the index of the one text slot a command fills straight
// from the command line, or -1 when the remainder is not a text value: no text
// slot, several of them, or one mixed with other slots. Those are answerable only
// by replying to the questions, since positional text parsing is unresolvable.
func (c *commandSpec) soleTextSlot() int {
	idx := -1
	for i := range c.args {
		if c.args[i].kind != argText {
			continue
		}
		if idx >= 0 {
			return -1
		}
		idx = i
	}
	if idx >= 0 && len(c.args) > 1 {
		return -1
	}
	return idx
}

// missingIn lists the indexes of c's mandatory slots that p leaves unanswered, in
// spec order, so the first one is always the next question to ask.
func (c *commandSpec) missingIn(p cmdArgs) []int {
	var missing []int
	for i := range c.args {
		if c.args[i].mandatory && !p.has(c.args[i].name) {
			missing = append(missing, i)
		}
	}
	return missing
}

// collects reports whether c gathers answers across several messages. That is what
// makes runCommand restore a stored collection, and the reason a one-slot command
// such as /allow or /broadcast never reads the database before dispatch.
func (c *commandSpec) collects() bool {
	n := 0
	for i := range c.args {
		if c.args[i].mandatory {
			n++
		}
	}
	return n > 1
}

// awaits reports whether c can leave a stored collection behind. Only a typed
// answer needs one, because a choice slot's buttons carry every answer
// themselves, so only a command with a mandatory text slot can — which is why a
// plain /leader or /download never has to go looking for one.
func (c *commandSpec) awaits() bool {
	for i := range c.args {
		if c.args[i].kind == argText && c.args[i].mandatory {
			return true
		}
	}
	return false
}

// firstChoiceSlot reports the index of the first choice slot the bot can put a
// question to the user for, or -1 when the command has none.
func (c *commandSpec) firstChoiceSlot() int {
	for i := range c.args {
		if c.args[i].kind == argChoice && c.args[i].ask != "" {
			return i
		}
	}
	return -1
}

// parse maps the text after the command word onto c's slots. It is structural
// only: text values are returned raw for the caller to validate. bad is the first
// token that matches no slot or that conflicts with one already chosen.
func (c *commandSpec) parse(rest string) (p cmdArgs, missing []int, bad string) {
	p = newArgs()
	if len(c.args) == 0 {
		return p, nil, "" // trailing text is ignored, as it always has been
	}
	if slot := c.soleTextSlot(); slot >= 0 {
		if v := strings.TrimSpace(rest); v != "" {
			p.setValue(c.args[slot].name, v)
		}
		return p, c.missingIn(p), ""
	}
tokens:
	for _, tok := range strings.Fields(strings.ToLower(rest)) {
		for i := range c.args {
			s := &c.args[i]
			if s.kind == argFlag && s.token == tok {
				p.setFlag(s.name)
				continue tokens
			}
			if s.kind != argChoice {
				continue
			}
			for _, o := range s.options {
				if o.token != tok {
					continue
				}
				if have := p.values[s.name]; have != "" && have != tok {
					return p, nil, tok // /leader d w: one slot, two answers
				}
				p.setValue(s.name, tok)
				continue tokens
			}
		}
		return p, nil, tok
	}
	return p, c.missingIn(p), ""
}

const cmdPrefix = "cmd:"

// encodeTokens renders the supplied slots as the tokens the user could have typed,
// in spec order, joined with '.' because callback_data allows no spaces. extra is
// the answer a button is about to supply.
func (c *commandSpec) encodeTokens(p cmdArgs, extra string) string {
	var tokens []string
	for i := range c.args {
		s := &c.args[i]
		switch {
		case s.kind == argFlag && p.flags[s.name]:
			tokens = append(tokens, s.token)
		case s.kind == argChoice && p.values[s.name] != "":
			tokens = append(tokens, p.values[s.name])
		}
	}
	if extra != "" {
		tokens = append(tokens, extra)
	}
	return strings.Join(tokens, ".")
}

// callbackData renders a press as the command line it replays. It stays well under
// Telegram's 64-byte limit for every command in the registry, which a test checks.
func (c *commandSpec) callbackData(p cmdArgs, extra string) string {
	return cmdPrefix + c.name + ":" + c.encodeTokens(p, extra)
}

// menuCommands renders the registry as a setMyCommands list. Registry order is
// menu order, and this is the only place the menu is described.
func menuCommands(admin bool) []map[string]string {
	out := make([]map[string]string, 0, len(commandSpecs))
	for i := range commandSpecs {
		c := &commandSpecs[i]
		if c.description == "" || (c.adminOnly && !admin) {
			continue
		}
		out = append(out, map[string]string{"command": c.name, "description": c.description})
	}
	return out
}

const awaitPrefix = "await:"

// awaitStage is the session stage of a collection waiting for a typed answer. It
// shares the sessions row with the recording flow but matches none of its
// prefixes, so the two state machines cannot overlap.
func awaitStage(name string) string { return awaitPrefix + name }

// pendingCommand is the JSON in sessions.pending: the answers a collection has
// gathered so far.
type pendingCommand struct {
	Command string            `json:"command"`
	Values  map[string]string `json:"values,omitempty"`
	Flags   []string          `json:"flags,omitempty"`
}

// encodePending renders the answers gathered so far for storage in
// sessions.pending, tagged with the command they belong to.
func encodePending(c *commandSpec, p cmdArgs) []byte {
	stored := pendingCommand{Command: c.name, Values: map[string]string{}}
	for name, value := range p.values {
		stored.Values[name] = value
	}
	for name, on := range p.flags {
		if on {
			stored.Flags = append(stored.Flags, name)
		}
	}
	data, err := json.Marshal(stored)
	if err != nil {
		return nil // cannot happen for strings; an empty collection just re-asks
	}
	return data
}

// decodePending restores a stored collection, dropping anything that no longer
// matches the registry so a renamed command or slot cannot wedge a user.
func decodePending(c *commandSpec, raw []byte) (cmdArgs, bool) {
	p := newArgs()
	if c == nil || len(raw) == 0 {
		return p, false
	}
	var stored pendingCommand
	if err := json.Unmarshal(raw, &stored); err != nil || stored.Command != c.name {
		return p, false
	}
	for i := range c.args {
		s := &c.args[i]
		if value, ok := stored.Values[s.name]; ok && value != "" {
			p.setValue(s.name, value)
		}
	}
	for _, name := range stored.Flags {
		for i := range c.args {
			if c.args[i].kind == argFlag && c.args[i].name == name {
				p.setFlag(name)
			}
		}
	}
	return p, true
}

// validateSlot canonicalizes a raw value for a slot. A non-empty reply means the
// value is unusable and that sentence is what the user should see; re-sending the
// question is the default, so an empty or over-long answer simply asks again.
func validateSlot(s argSpec, raw string) (value, reply string) {
	value = strings.TrimSpace(raw)
	// The slot's own validator speaks first, even about an over-long answer, so a
	// mistyped age is told what an age looks like rather than just asked again.
	if s.validate != nil {
		if value, reply = s.validate(value); reply != "" {
			return "", reply
		}
	} else if value == "" {
		return "", s.ask
	}
	if s.maxRunes > 0 && len([]rune(value)) > s.maxRunes {
		return "", s.ask
	}
	return value, ""
}

// normalizeDigits maps Persian (U+06F0-U+06F9) and Arabic-Indic (U+0660-U+0669)
// digits to ASCII, so a Persian-speaking user can type ۲۲ for an age.
func normalizeDigits(s string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r >= '۰' && r <= '۹':
			return '0' + (r - '۰')
		case r >= '٠' && r <= '٩':
			return '0' + (r - '٠')
		}
		return r
	}, s)
}

// digitsOnly reports whether s is a non-empty run of ASCII digits.
func digitsOnly(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// numberBetween builds a validator for a whole number in [lo, hi], accepting
// Persian and Arabic-Indic digits. reply is the one sentence it answers with.
func numberBetween(lo, hi int, reply string) func(string) (string, string) {
	return func(raw string) (string, string) {
		v := normalizeDigits(strings.TrimSpace(raw))
		if !digitsOnly(v) {
			return "", reply
		}
		n, err := strconv.Atoi(v)
		if err != nil || n < lo || n > hi {
			return "", reply
		}
		return strconv.Itoa(n), ""
	}
}

// digitString builds a validator for a run of lo..hi digits kept as text, since a
// student number may carry leading zeros.
func digitString(lo, hi int, reply string) func(string) (string, string) {
	return func(raw string) (string, string) {
		v := normalizeDigits(strings.TrimSpace(raw))
		if !digitsOnly(v) || len(v) < lo || len(v) > hi {
			return "", reply
		}
		return v, ""
	}
}

// plainLine builds a validator for a one-line answer: present, and free of line
// breaks and other control characters. validateSlot has already applied maxRunes.
//
// A name carrying a newline would break the profile card's layout and, worse, let
// one participant inject extra rows into the leaderboard every other participant
// reads. Rejecting it at the input is what keeps both renderers honest.
func plainLine(reply string) func(string) (string, string) {
	return func(raw string) (string, string) {
		v := strings.TrimSpace(raw)
		if v == "" {
			return "", reply
		}
		for _, r := range v {
			if unicode.IsControl(r) {
				return "", reply
			}
		}
		return v, ""
	}
}

// runCommand is the single dispatch path: a typed command, a cmd: button, and the
// legacy lang:/action:/dl: buttons all arrive here.
//
// It reads the database only for a command with a precheck or one that collects(),
// which keeps a mistyped or unauthorized command — and /allow and /broadcast — a
// pure parse plus one reply.
func (a *app) runCommand(ctx context.Context, userID int64, name, rest string) error {
	c := lookupCommand(name)
	if c == nil || (c.adminOnly && !a.cfg.fullAdmins[userID]) {
		// Same reply as an unknown command, so an admin command typed by a
		// non-admin does not reveal that it exists.
		return a.send(ctx, userID, unknownCommand, actionButtons())
	}
	p, _, bad := c.parse(rest)
	if bad != "" {
		if slot := c.firstChoiceSlot(); slot >= 0 {
			return a.askSlot(ctx, userID, c, p, slot, unrecognizedOption)
		}
		return a.send(ctx, userID, unrecognizedOption, actionButtons())
	}
	// Text typed on the command line still needs checking. Values restored from a
	// stored collection were validated when they were stored.
	for i := range c.args {
		s := &c.args[i]
		if s.kind != argText || p.values[s.name] == "" {
			continue
		}
		value, reply := validateSlot(*s, p.values[s.name])
		if reply != "" {
			return a.send(ctx, userID, reply, nil)
		}
		p.setValue(s.name, value)
	}
	seeded := false
	if c.precheck != nil {
		handled, seed, err := c.precheck(a, ctx, userID, p)
		if err != nil || handled {
			return err
		}
		p.fill(seed)
		seeded = len(seed.values) > 0 || len(seed.flags) > 0
	}
	// A seed is a complete statement of intent — the slots it leaves empty are
	// exactly the ones to ask about — so a half-finished collection must not fill
	// them in. Without this, switching from editing one profile field to another
	// would answer the second question from the first attempt's leftovers.
	stored := false
	if c.collects() && !seeded {
		s, err := a.getSession(ctx, userID)
		if err != nil {
			return err
		}
		if stage, ok := strings.CutPrefix(s.Stage, awaitPrefix); ok && stage == c.name {
			if answers, ok := decodePending(c, s.Pending); ok {
				p.fill(answers)
				stored = true
			}
		}
	}
	return a.collect(ctx, userID, c, p, stored)
}

// collect asks for the first slot a command still needs, or runs it when nothing
// is missing. A choice slot needs no stored state, because its buttons carry every
// answer so far in their callback data; a typed answer carries no context, so the
// answers so far are written to the session first.
//
// stored says a collection row exists for this command and must be dropped before
// the command runs. Callers that never stored one pass false, which is what keeps a
// plain /leader or /download from touching the database at all.
func (a *app) collect(ctx context.Context, userID int64, c *commandSpec, p cmdArgs, stored bool) error {
	missing := c.missingIn(p)
	if len(missing) == 0 {
		// Drop a collection row before running, including one left by an earlier
		// attempt at this same command — otherwise the user's next message would
		// be read as an answer to a question they have already moved past. This
		// covers a one-slot command answered on the command line (/broadcast text
		// after a bare /broadcast), which never restores answers and so would
		// otherwise leave its row in place.
		if stored || c.awaits() {
			if err := a.clearCollection(ctx, userID); err != nil {
				return err
			}
		}
		return c.run(a, ctx, userID, p)
	}
	slot := missing[0]
	if c.args[slot].kind == argChoice {
		return a.askSlot(ctx, userID, c, p, slot, "")
	}
	s, err := a.getSession(ctx, userID)
	if err != nil {
		return err
	}
	if s.Stage != "" && !strings.HasPrefix(s.Stage, awaitPrefix) {
		return a.send(ctx, userID, busyWithPair, nil)
	}
	if err := a.putSession(ctx, userID, session{Stage: awaitStage(c.name), Pending: encodePending(c, p)}); err != nil {
		return err
	}
	return a.askSlot(ctx, userID, c, p, slot, "")
}

// clearCollection drops a session that is only holding a half-finished
// collection. It checks the stage first, so a recording in progress is never
// destroyed by a stale argument button someone presses from their history.
func (a *app) clearCollection(ctx context.Context, userID int64) error {
	s, err := a.getSession(ctx, userID)
	if err != nil {
		return err
	}
	if !strings.HasPrefix(s.Stage, awaitPrefix) {
		return nil
	}
	return a.clearSession(ctx, userID)
}

// askSlot puts one slot's question to the user. intro replaces the question when
// the bot is explaining why it is asking again.
func (a *app) askSlot(ctx context.Context, userID int64, c *commandSpec, p cmdArgs, slot int, intro string) error {
	s := c.args[slot]
	text := s.ask
	if intro != "" {
		text = intro
	}
	if s.kind != argChoice {
		return a.send(ctx, userID, text, nil)
	}
	// The buttons supply this slot's own answer, so what they carry must not
	// already hold one. Otherwise re-asking a slot that was answered twice
	// (/leader d w) offers buttons that conflict with the value already parsed,
	// and the same question repeats forever. Other slots are preserved, so a
	// flag such as /leader l survives the question.
	offered := newArgs()
	offered.fill(p)
	delete(offered.values, s.name)
	perRow := s.perRow
	if perRow <= 0 {
		perRow = 2
	}
	var rows [][]map[string]string
	var row []map[string]string
	for _, o := range s.options {
		row = append(row, button(o.label, c.callbackData(offered, o.token)))
		if len(row) == perRow {
			rows, row = append(rows, row), nil
		}
	}
	if len(row) > 0 {
		rows = append(rows, row)
	}
	return a.send(ctx, userID, text, rows)
}

// resumePending consumes the user's message as the answer to the slot a collection
// is waiting for. A voice note or an unusable answer re-sends the question.
func (a *app) resumePending(ctx context.Context, userID int64, s session, text string) error {
	name, _ := strings.CutPrefix(s.Stage, awaitPrefix)
	c := lookupCommand(name)
	p, ok := decodePending(c, s.Pending)
	if !ok || (c.adminOnly && !a.cfg.fullAdmins[userID]) {
		// The registry changed under a stored session, or access was withdrawn.
		if err := a.clearSession(ctx, userID); err != nil {
			return err
		}
		return a.send(ctx, userID, canceled, nil)
	}
	missing := c.missingIn(p)
	if len(missing) > 0 {
		slot := c.args[missing[0]]
		if slot.kind != argChoice {
			value, reply := validateSlot(slot, text)
			if reply != "" {
				return a.send(ctx, userID, reply, nil)
			}
			p.setValue(slot.name, value)
		}
		// A choice slot falls through unanswered: the user typed where a button
		// was expected, so collect asks the question again.
	}
	return a.collect(ctx, userID, c, p, true)
}
