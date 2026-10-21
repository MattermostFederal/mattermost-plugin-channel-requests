package main

import (
	"crypto/rand"
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/pkg/errors"
)

const (
	// kvExerciseManifestPrefix namespaces the per-team record of what
	// exercise mode created. It doesn't start with kvRequestPrefix, so the
	// pending-request migration never reads it.
	kvExerciseManifestPrefix = "exercise_manifest_"

	// kvExercisePersonaPrefix records, by user ID, the accounts exercise
	// mode created and the team each was created for. It outlives
	// teardown, so populate can reactivate the same accounts. Only
	// accounts recorded here are treated as personas: users can edit their
	// own username and props, so neither proves an account is a persona.
	kvExercisePersonaPrefix = "exercise_persona_"

	exerciseStatePopulating  = "populating"
	exerciseStateReady       = "ready"
	exerciseStateTearingDown = "tearing_down"

	// exerciseStaleMillis is how long a populate can go without updating
	// the manifest before teardown treats it as stopped.
	exerciseStaleMillis = 5 * 60 * 1000

	// exerciseMarker labels everything exercise mode creates.
	exerciseMarker         = "EXERCISE"
	exerciseDisplayPrefix  = exerciseMarker + " "
	exercisePurposePrefix  = "[" + exerciseMarker + "] "
	exercisePositionPrefix = exerciseMarker + ": "

	// exerciseBanner is the header of every exercise channel.
	exerciseBanner = "**EXERCISE EXERCISE EXERCISE**: this channel is exercise data from Mattermost Request Hub. Teardown archives it."

	exerciseRemovedText = exerciseMarker + ": removed by teardown."

	// The populate refusals when the request types it seeds are turned off.
	exerciseChannelRequestsOffText = "exercise mode seeds channel requests, which are turned off. Turn on **Allow channel requests** in System Console → Plugins → Mattermost Request Hub, then run populate again"
	exerciseAdminRequestsOffText   = "this scenario seeds Channel Admin requests, which are turned off. Turn on **Allow channel admin requests** in System Console → Plugins → Mattermost Request Hub, or use a scenario without them"

	// exerciseEmailDomain is a reserved domain (RFC 2606), so mail to a
	// persona goes nowhere.
	exerciseEmailDomain = "exercise.invalid"

	// exerciseTeamIDChars is how much of the team ID a persona username
	// carries, keeping each team's personas apart.
	exerciseTeamIDChars = 8

	// Scenario size limits, so a pasted scenario can't make populate run
	// for minutes.
	maxExercisePersonas   = 50
	maxExerciseOperations = 200

	manifestUpdateAttempts = 5
)

const (
	exerciseKindChannel        = "channel"
	exerciseKindChannelRequest = "channel_request"
	exerciseKindAdminRequest   = "admin_request"
)

// builtInExerciseScenario is a fictional scenario that shows the
// mechanics. Scenarios with real names are supplied on the server through
// the ExerciseScenarioJSON setting, never shipped in the plugin.
//
//go:embed exercise/silver_harbor.json
var builtInExerciseScenario []byte

var (
	// errNoExerciseManifest means the team has no exercise data recorded.
	errNoExerciseManifest = errors.New("no exercise data recorded for this team")
	errExerciseExists     = errors.New("exercise data already exists for this team, or a populate is running. Run `/mattermost-request-hub exercise status` to see it, or `/mattermost-request-hub exercise teardown` to remove it")
	// errExerciseStopped stops a populate once teardown has started.
	errExerciseStopped = errors.New("teardown started while seeding")
)

// personaTakenError means a persona's username belongs to an account
// exercise mode didn't create.
type personaTakenError struct{ username string }

func (e *personaTakenError) Error() string {
	return fmt.Sprintf("the username @%s is taken by an account that isn't exercise data", e.username)
}

func isPersonaTaken(err error) bool {
	var taken *personaTakenError
	return errors.As(err, &taken)
}

// exerciseScenario is the JSON form of a scenario.
type exerciseScenario struct {
	ID         string                  `json:"id"`
	Name       string                  `json:"name"`
	Personas   []exercisePersonaSpec   `json:"personas"`
	Operations []exerciseOperationSpec `json:"operations"`
}

type exercisePersonaSpec struct {
	Role      string `json:"role"`
	FirstName string `json:"first_name"`
	LastName  string `json:"last_name"`
	Position  string `json:"position"`
}

// exerciseOperationSpec is one step of a scenario. Persona fields hold
// roles; Channel holds the operation ID of a channel operation.
type exerciseOperationSpec struct {
	Act         int      `json:"act"`
	OperationID string   `json:"operation_id"`
	Kind        string   `json:"kind"`
	Suffix      string   `json:"suffix,omitempty"`
	DisplayName string   `json:"display_name,omitempty"`
	Purpose     string   `json:"purpose,omitempty"`
	Private     bool     `json:"private,omitempty"`
	Requester   string   `json:"requester"`
	Members     []string `json:"members,omitempty"`
	Admins      []string `json:"admins,omitempty"`
	AgeDays     int      `json:"age_days,omitempty"`
	Channel     string   `json:"channel,omitempty"`
	Nominees    []string `json:"nominees,omitempty"`
}

// exercisePlan is everything populate will create for one team, with
// every name, request ID and timestamp already decided.
type exercisePlan struct {
	Scenario        string                   `json:"scenario"`
	TeamID          string                   `json:"team_id"`
	Personas        []exercisePersona        `json:"personas"`
	Channels        []exerciseChannel        `json:"channels"`
	ChannelRequests []exerciseChannelRequest `json:"channel_requests"`
	AdminRequests   []exerciseAdminRequest   `json:"admin_requests"`
}

type exercisePersona struct {
	Role      string `json:"role"`
	Username  string `json:"username"`
	Email     string `json:"email"`
	FirstName string `json:"first_name"`
	LastName  string `json:"last_name"`
	Position  string `json:"position"`
}

type exerciseChannel struct {
	OpKey       string   `json:"op_key"`
	Name        string   `json:"name"`
	DisplayName string   `json:"display_name"`
	Purpose     string   `json:"purpose"`
	ChannelType string   `json:"channel_type"`
	Creator     string   `json:"creator"`
	Members     []string `json:"members"`
	Admins      []string `json:"admins"`
}

type exerciseChannelRequest struct {
	OpKey       string   `json:"op_key"`
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	DisplayName string   `json:"display_name"`
	Purpose     string   `json:"purpose"`
	ChannelType string   `json:"channel_type"`
	Requester   string   `json:"requester"`
	Members     []string `json:"members"`
	Admins      []string `json:"admins"`
	CreatedAt   int64    `json:"created_at"`
}

type exerciseAdminRequest struct {
	OpKey        string   `json:"op_key"`
	ID           string   `json:"id"`
	ChannelOpKey string   `json:"channel_op_key"`
	Requester    string   `json:"requester"`
	Nominees     []string `json:"nominees"`
	CreatedAt    int64    `json:"created_at"`
}

// exerciseManifest records a team's exercise data. It is claimed before
// populate writes anything, so it always names everything that may exist.
type exerciseManifest struct {
	CreatedBy string        `json:"created_by"`
	CreatedAt int64         `json:"created_at"`
	State     string        `json:"state"`
	Plan      *exercisePlan `json:"plan"`
	// UpdatedAt is refreshed by every write, so teardown can tell a
	// running populate from one that stopped.
	UpdatedAt int64 `json:"updated_at"`
	// Done maps each finished operation's key to the ID it created.
	Done map[string]string `json:"done"`
	// ChannelIDs lists every exercise channel, seeded or created by an
	// approval, for teardown to archive.
	ChannelIDs []string `json:"channel_ids"`
	// KVKeys lists the reply-forwarding lookups and approval card records
	// of the seeded requests, which outlive a decision, for teardown to
	// delete.
	KVKeys []string `json:"kv_keys,omitempty"`
}

func exerciseManifestKey(teamID string) string {
	return kvExerciseManifestPrefix + teamID
}

// exerciseOpKey identifies an operation by scenario, act and operation ID,
// so replaying an act never creates it twice.
func exerciseOpKey(scenario string, act int, operationID string) string {
	return fmt.Sprintf("%s/%d/%s", scenario, act, operationID)
}

// exercisePersonaUsername includes part of the team ID so each team gets
// its own personas.
func exercisePersonaUsername(role, teamID string) string {
	return "exercise-" + role + "-" + teamID[:min(len(teamID), exerciseTeamIDChars)]
}

// loadExerciseScenario parses the scenario from the ExerciseScenarioJSON
// setting, or the built-in one when the setting is blank.
func loadExerciseScenario(configured string) (*exerciseScenario, error) {
	data := builtInExerciseScenario
	if strings.TrimSpace(configured) != "" {
		data = []byte(configured)
	}
	var sc exerciseScenario
	if err := json.Unmarshal(data, &sc); err != nil {
		return nil, errors.New("the exercise scenario in the plugin settings is not valid JSON")
	}
	if sc.ID == "" || sc.Name == "" {
		return nil, errors.New("the exercise scenario needs an id and a name")
	}
	return &sc, nil
}

// buildExercisePlan turns a scenario into the plan for one team. It makes
// no API calls: names go through the same prefix rules as real requests,
// and request IDs come from newID, so a test can fix them. Channel names
// use the first configured prefix.
func buildExercisePlan(sc *exerciseScenario, teamID string, prefixes []channelPrefix, nowMs int64, newID func() string) (*exercisePlan, error) {
	if len(prefixes) == 0 {
		return nil, errors.New(notConfiguredMessage)
	}
	if len(sc.Personas) > maxExercisePersonas || len(sc.Operations) > maxExerciseOperations {
		return nil, errors.Errorf("a scenario can have at most %d personas and %d operations", maxExercisePersonas, maxExerciseOperations)
	}
	prefix := prefixes[0].Prefix

	plan := &exercisePlan{Scenario: sc.ID, TeamID: teamID}
	roles := map[string]bool{}
	for _, ps := range sc.Personas {
		if roles[ps.Role] {
			return nil, errors.Errorf("persona %q is listed twice", ps.Role)
		}
		roles[ps.Role] = true
		username := exercisePersonaUsername(ps.Role, teamID)
		if !model.IsValidUsername(username) {
			return nil, errors.Errorf("persona %q gives the invalid username %q", ps.Role, username)
		}
		if utf8.RuneCountInString(ps.FirstName) > model.UserFirstNameMaxRunes || utf8.RuneCountInString(ps.LastName) > model.UserLastNameMaxRunes {
			return nil, errors.Errorf("persona %q has a first or last name longer than %d characters", ps.Role, model.UserFirstNameMaxRunes)
		}
		position := exercisePositionPrefix + ps.Position
		if utf8.RuneCountInString(position) > model.UserPositionMaxRunes {
			return nil, errors.Errorf("persona %q has a position longer than %d characters", ps.Role, model.UserPositionMaxRunes-len(exercisePositionPrefix))
		}
		plan.Personas = append(plan.Personas, exercisePersona{
			Role:      ps.Role,
			Username:  username,
			Email:     username + "@" + exerciseEmailDomain,
			FirstName: ps.FirstName,
			LastName:  ps.LastName,
			Position:  position,
		})
	}

	checkRoles := func(op exerciseOperationSpec, list []string) error {
		if len(list) > maxMembersPerList {
			return errors.Errorf("operation %q names more than %d personas in one list", op.OperationID, maxMembersPerList)
		}
		for _, r := range list {
			if !roles[r] {
				return errors.Errorf("operation %q names the unknown persona %q", op.OperationID, r)
			}
		}
		return nil
	}

	seenOps := map[string]bool{}
	channelOps := map[string]string{} // operation ID -> op key
	names := map[string]bool{}
	for _, op := range sc.Operations {
		key := exerciseOpKey(sc.ID, op.Act, op.OperationID)
		if op.OperationID == "" || seenOps[op.OperationID] {
			return nil, errors.Errorf("operation %q needs a unique ID", op.OperationID)
		}
		seenOps[op.OperationID] = true
		if op.AgeDays < 0 || op.AgeDays > maxRequestExpiryDays {
			return nil, errors.Errorf("operation %q needs an age_days from 0 to %d", op.OperationID, maxRequestExpiryDays)
		}
		if err := checkRoles(op, []string{op.Requester}); err != nil {
			return nil, err
		}
		createdAt := nowMs - int64(op.AgeDays)*millisPerDay

		var name, displayName, purpose, channelType string
		if op.Kind == exerciseKindChannel || op.Kind == exerciseKindChannelRequest {
			if err := checkRoles(op, op.Members); err != nil {
				return nil, err
			}
			if err := checkRoles(op, op.Admins); err != nil {
				return nil, err
			}
			displayName = exerciseDisplayPrefix + strings.Join(strings.Fields(op.DisplayName), " ")
			purpose = exercisePurposePrefix + op.Purpose
			if err := validateRequestInput(requestInput{TeamID: teamID, DisplayName: displayName, Purpose: purpose}); err != nil {
				return nil, errors.Wrapf(err, "operation %q", op.OperationID)
			}
			var err error
			name, err = resolvePrefixedName(prefixes, requestInput{Prefix: prefix, Name: op.Suffix, DisplayName: op.DisplayName})
			if err != nil {
				return nil, errors.Wrapf(err, "operation %q", op.OperationID)
			}
			if names[name] {
				return nil, errors.Errorf("operation %q reuses the channel name %q", op.OperationID, name)
			}
			names[name] = true
			channelType = channelTypeOpen
			if op.Private {
				channelType = channelTypePrivate
			}
		}

		switch op.Kind {
		case exerciseKindChannel:
			channelOps[op.OperationID] = key
			plan.Channels = append(plan.Channels, exerciseChannel{
				OpKey: key, Name: name, DisplayName: displayName, Purpose: purpose, ChannelType: channelType,
				Creator: op.Requester, Members: op.Members, Admins: op.Admins,
			})
		case exerciseKindChannelRequest:
			plan.ChannelRequests = append(plan.ChannelRequests, exerciseChannelRequest{
				OpKey: key, ID: newID(), Name: name, DisplayName: displayName, Purpose: purpose, ChannelType: channelType,
				Requester: op.Requester, Members: op.Members, Admins: op.Admins, CreatedAt: createdAt,
			})
		case exerciseKindAdminRequest:
			channelKey, ok := channelOps[op.Channel]
			if !ok {
				return nil, errors.Errorf("operation %q needs an earlier channel operation %q", op.OperationID, op.Channel)
			}
			if len(op.Nominees) == 0 {
				return nil, errors.Errorf("operation %q names no nominees", op.OperationID)
			}
			if err := checkRoles(op, op.Nominees); err != nil {
				return nil, err
			}
			plan.AdminRequests = append(plan.AdminRequests, exerciseAdminRequest{
				OpKey: key, ID: newID(), ChannelOpKey: channelKey, Requester: op.Requester, Nominees: op.Nominees, CreatedAt: createdAt,
			})
		default:
			return nil, errors.Errorf("operation %q has the unknown kind %q", op.OperationID, op.Kind)
		}
	}
	return plan, nil
}

// loadExerciseManifest returns the team's manifest and its stored bytes,
// or errNoExerciseManifest. A manifest without a plan reads as an empty
// plan, so teardown can still clear it.
func (p *Plugin) loadExerciseManifest(teamID string) (*exerciseManifest, []byte, error) {
	data, appErr := p.API.KVGet(exerciseManifestKey(teamID))
	if appErr != nil {
		return nil, nil, p.exerciseFailure("could not load the exercise record", appErr)
	}
	if data == nil {
		return nil, nil, errNoExerciseManifest
	}
	var m exerciseManifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, nil, errors.New("the exercise record for this team is unreadable")
	}
	if m.Plan == nil {
		m.Plan = &exercisePlan{TeamID: teamID}
	}
	return &m, data, nil
}

// updateExerciseManifest applies change to the team's manifest with a
// compare-and-set, retrying when another writer got there first. A
// change that returns an error aborts without writing.
func (p *Plugin) updateExerciseManifest(teamID string, change func(*exerciseManifest) error) error {
	for range manifestUpdateAttempts {
		m, raw, err := p.loadExerciseManifest(teamID)
		if err != nil {
			return err
		}
		if m.Done == nil {
			m.Done = map[string]string{}
		}
		if err = change(m); err != nil {
			return err
		}
		m.UpdatedAt = model.GetMillis()
		data, _ := json.Marshal(m) // the manifest holds only strings, numbers and slices
		ok, appErr := p.API.KVCompareAndSet(exerciseManifestKey(teamID), raw, data)
		if appErr != nil {
			return p.exerciseFailure("could not save the exercise record", appErr)
		}
		if ok {
			return nil
		}
	}
	return errors.New("the exercise record kept changing")
}

// recordExerciseChannel adds a channel created by approving an exercise
// request to the team's manifest, so teardown archives it too. When the
// exercise was already torn down, the channel is archived at once.
func (p *Plugin) recordExerciseChannel(channel *model.Channel) {
	err := p.updateExerciseManifest(channel.TeamId, func(m *exerciseManifest) error {
		m.ChannelIDs = append(m.ChannelIDs, channel.Id)
		return nil
	})
	if errors.Is(err, errNoExerciseManifest) {
		if _, archiveErr := p.archiveExerciseChannel(channel.Id); archiveErr != nil {
			p.API.LogWarn("failed to archive exercise channel after teardown; archive it by hand", "channel_id", channel.Id, "error", archiveErr.Error())
		}
		return
	}
	if err != nil {
		p.API.LogWarn("failed to record exercise channel; archive it by hand", "channel_id", channel.Id, "error", err.Error())
	}
	channel.Header = exerciseBanner
	if _, appErr := p.API.UpdateChannel(channel); appErr != nil {
		p.API.LogWarn("failed to mark exercise channel", "channel_id", channel.Id, "error", appErr.Error())
	}
}

// exerciseLabel prefixes audit lines and card titles about exercise
// requests.
func exerciseLabel(exercise bool) string {
	if exercise {
		return exerciseDisplayPrefix
	}
	return ""
}

// exercisePassword returns a random password that is never stored or
// shown, so nobody can sign in as a persona. The fixed start meets any
// password policy.
func exercisePassword() string {
	b := make([]byte, 32)
	_, _ = rand.Read(b) // crypto/rand.Read never fails
	return "Ex1!" + base64.RawURLEncoding.EncodeToString(b)
}

func isNotFound(appErr *model.AppError) bool {
	return appErr != nil && appErr.StatusCode == http.StatusNotFound
}

// exerciseFailure logs an API failure in full and returns a short error
// for the reply.
func (p *Plugin) exerciseFailure(what string, appErr *model.AppError) error {
	p.API.LogWarn("exercise step failed", "step", what, "error", appErr.Error())
	return errors.Errorf("%s (%s)", what, strings.TrimRight(appErr.Message, "."))
}

// isExercisePersonaID reports whether userID is an account exercise mode
// created for teamID.
func (p *Plugin) isExercisePersonaID(userID, teamID string) (bool, error) {
	data, appErr := p.API.KVGet(kvExercisePersonaPrefix + userID)
	if appErr != nil {
		return false, p.exerciseFailure("could not check an exercise persona", appErr)
	}
	return string(data) == teamID, nil
}

// findExercisePersona returns the account with the persona's username, or
// nil when the name is free. An account exercise mode didn't create is an
// error, since it belongs to somebody real.
func (p *Plugin) findExercisePersona(persona exercisePersona, teamID string) (*model.User, error) {
	user, appErr := p.API.GetUserByUsername(persona.Username)
	if isNotFound(appErr) {
		return nil, nil
	}
	if appErr != nil {
		return nil, p.exerciseFailure("could not look up @"+persona.Username, appErr)
	}
	ok, err := p.isExercisePersonaID(user.Id, teamID)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, &personaTakenError{persona.Username}
	}
	return user, nil
}

// checkExercisePreconditions makes sure nothing the plan needs is taken and
// that personas may join the team, before anything is written.
func (p *Plugin) checkExercisePreconditions(plan *exercisePlan) error {
	if err := p.checkExerciseEmailDomain(plan.TeamID); err != nil {
		return err
	}
	for _, persona := range plan.Personas {
		if _, err := p.findExercisePersona(persona, plan.TeamID); err != nil {
			return err
		}
	}
	var names []string
	for _, ch := range plan.Channels {
		names = append(names, ch.Name)
	}
	for _, req := range plan.ChannelRequests {
		names = append(names, req.Name)
	}
	for _, name := range names {
		_, appErr := p.API.GetChannelByName(plan.TeamID, name, true)
		if appErr == nil {
			return errors.Errorf("a channel named ~%s already exists in this team", name)
		}
		if !isNotFound(appErr) {
			return p.exerciseFailure("could not look up ~"+name, appErr)
		}
	}
	return nil
}

// checkExerciseEmailDomain refuses when the server or the team only lets
// in accounts from email domains that don't include the personas' domain:
// Mattermost would refuse to add them to the team.
func (p *Plugin) checkExerciseEmailDomain(teamID string) error {
	var lists []string
	if config := p.API.GetConfig(); config != nil && config.TeamSettings.RestrictCreationToDomains != nil {
		lists = append(lists, *config.TeamSettings.RestrictCreationToDomains)
	}
	team, appErr := p.API.GetTeam(teamID)
	if appErr != nil {
		return p.exerciseFailure("could not load this team", appErr)
	}
	lists = append(lists, team.AllowedDomains)
	for _, list := range lists {
		domains := strings.Fields(strings.NewReplacer("@", " ", ",", " ").Replace(strings.ToLower(list)))
		if len(domains) > 0 && !strings.Contains(" "+strings.Join(domains, " ")+" ", " "+exerciseEmailDomain+" ") {
			return errors.Errorf("this server or team only allows accounts with email addresses at %s, so exercise personas (@%s) can't join. Add %s to the allowed domains while you run the exercise", strings.Join(domains, ", "), exerciseEmailDomain, exerciseEmailDomain)
		}
	}
	return nil
}

// populateExercise seeds the scenario into a team for actorID. It is the
// shared operation behind `/mattermost-request-hub exercise populate`.
func (p *Plugin) populateExercise(teamID, actorID string) (string, error) {
	// The claim below is what makes populate run once; this check only
	// gives a second populate the right answer instead of a name clash.
	if _, _, err := p.loadExerciseManifest(teamID); !errors.Is(err, errNoExerciseManifest) {
		if err != nil {
			return "", err
		}
		return "", errExerciseExists
	}
	config := p.getConfiguration()
	if !config.AllowChannelRequests {
		return "", errors.New(exerciseChannelRequestsOffText)
	}
	if !config.UsesPrefixList() {
		return "", errors.New(notConfiguredMessage)
	}
	sc, err := loadExerciseScenario(config.ExerciseScenarioJSON)
	if err != nil {
		return "", err
	}
	now := model.GetMillis()
	plan, err := buildExercisePlan(sc, teamID, config.Prefixes(), now, model.NewId)
	if err != nil {
		return "", errors.Wrap(err, "the exercise scenario doesn't fit this server's settings")
	}
	if len(plan.AdminRequests) > 0 && !config.AllowChannelAdminRequests {
		return "", errors.New(exerciseAdminRequestsOffText)
	}
	if err = p.checkExercisePreconditions(plan); err != nil {
		return "", err
	}

	// Claim the manifest before writing anything. Only one populate per
	// team can win, and teardown can always find what was written.
	manifest := exerciseManifest{
		CreatedBy: actorID, CreatedAt: now, UpdatedAt: now, State: exerciseStatePopulating,
		Plan: plan, Done: map[string]string{},
	}
	data, _ := json.Marshal(manifest)
	claimed, appErr := p.API.KVSetWithOptions(exerciseManifestKey(teamID), data, model.PluginKVSetOptions{Atomic: true, OldValue: nil})
	if appErr != nil {
		return "", p.exerciseFailure("could not start the exercise", appErr)
	}
	if !claimed {
		return "", errExerciseExists
	}

	if err := p.seedExercise(plan); err != nil {
		p.API.LogError("failed to populate exercise data", "team_id", teamID, "error", err.Error())
		return "", errors.Errorf("could not finish seeding: %s. Run `/mattermost-request-hub exercise teardown` to remove what was created, then try again", err.Error())
	}
	if err := p.updateExerciseManifest(teamID, func(m *exerciseManifest) error {
		if m.State != exerciseStatePopulating {
			return errExerciseStopped
		}
		m.State = exerciseStateReady
		return nil
	}); err != nil {
		p.API.LogError("failed to mark exercise data ready", "team_id", teamID, "error", err.Error())
		return "", errors.New("seeded the exercise but could not mark it finished. Run `/mattermost-request-hub exercise status` to check it, or `/mattermost-request-hub exercise teardown` to remove it")
	}
	return fmt.Sprintf("Seeded the **%s** exercise into this team: %d personas, %d channels, %d channel requests and %d Channel Admin requests. The approval cards are waiting for review. Run `/mattermost-request-hub exercise teardown` to remove it all.",
		escapeMarkdown(sc.Name), len(plan.Personas), len(plan.Channels), len(plan.ChannelRequests), len(plan.AdminRequests)), nil
}

// recordSeeded applies change to the manifest and refreshes its
// heartbeat. Once teardown has started it still records what was made, so
// teardown finds it, but returns errExerciseStopped to end the populate.
func (p *Plugin) recordSeeded(teamID string, change func(*exerciseManifest)) error {
	stopped := false
	err := p.updateExerciseManifest(teamID, func(m *exerciseManifest) error {
		change(m)
		stopped = m.State != exerciseStatePopulating
		return nil
	})
	if err == nil && stopped {
		return errExerciseStopped
	}
	return err
}

// seedExercise creates the plan's personas, channels and requests,
// recording each in the manifest as it goes, which also tells teardown it
// is still running. It stops at the first error, or once teardown has
// started. When teardown has already removed the manifest, it undoes the
// step it just made, since nothing else would find it.
func (p *Plugin) seedExercise(plan *exercisePlan) error {
	teamID := plan.TeamID
	noop := func(*exerciseManifest) {}
	made := func(change func(*exerciseManifest), undo func()) error {
		err := p.recordSeeded(teamID, change)
		if errors.Is(err, errNoExerciseManifest) {
			undo()
		}
		return err
	}

	users := map[string]*model.User{}
	for _, persona := range plan.Personas {
		user, err := p.ensureExercisePersona(persona, teamID)
		if err != nil {
			return err
		}
		if err := made(noop, func() { _ = p.API.UpdateUserActive(user.Id, false) }); err != nil {
			return err
		}
		users[persona.Role] = user
	}
	ids := func(roles []string) []string {
		out := make([]string, 0, len(roles))
		for _, r := range roles {
			out = append(out, users[r].Id)
		}
		return out
	}

	channels := map[string]*model.Channel{}
	for _, spec := range plan.Channels {
		channel, appErr := p.API.CreateChannel(&model.Channel{
			TeamId:      teamID,
			Name:        spec.Name,
			DisplayName: spec.DisplayName,
			Purpose:     spec.Purpose,
			Header:      exerciseBanner,
			Type:        model.ChannelType(spec.ChannelType),
			CreatorId:   users[spec.Creator].Id,
		})
		if appErr != nil {
			return p.exerciseFailure("could not create ~"+spec.Name, appErr)
		}
		if err := made(func(m *exerciseManifest) {
			m.Done[spec.OpKey] = channel.Id
			m.ChannelIDs = append(m.ChannelIDs, channel.Id)
		}, func() { _, _ = p.archiveExerciseChannel(channel.Id) }); err != nil {
			return err
		}
		admins := map[string]bool{}
		for _, r := range spec.Admins {
			admins[r] = true
		}
		for _, r := range dedupeNonEmpty(append(append([]string{spec.Creator}, spec.Members...), spec.Admins...)) {
			if _, appErr := p.API.AddChannelMember(channel.Id, users[r].Id); appErr != nil {
				return p.exerciseFailure(fmt.Sprintf("could not add @%s to ~%s", users[r].Username, spec.Name), appErr)
			}
			if admins[r] {
				if _, appErr := p.API.UpdateChannelMemberRoles(channel.Id, users[r].Id, channelAdminRoleString); appErr != nil {
					return p.exerciseFailure(fmt.Sprintf("could not make @%s Channel Admin of ~%s", users[r].Username, spec.Name), appErr)
				}
			}
		}
		channels[spec.OpKey] = channel
	}

	for _, spec := range plan.ChannelRequests {
		req := &channelRequest{
			ID: spec.ID, RequesterID: users[spec.Requester].Id, TeamID: teamID,
			Name: spec.Name, DisplayName: spec.DisplayName, Purpose: spec.Purpose, ChannelType: spec.ChannelType,
			MemberIDs: ids(spec.Members), AdminMemberIDs: ids(spec.Admins), CreatedAt: spec.CreatedAt, Exercise: true,
		}
		if err := p.storeRequest(req); err != nil {
			return errors.New("could not save a channel request")
		}
		undo := func() { _, _ = p.removeExerciseRequest(pendingKindChannel, req.ID) }
		entry := pendingEntry{ID: req.ID, CreatedAt: req.CreatedAt, Key: channelRequestKey(req)}
		if err := p.addPendingEntry(pendingKindChannel, req.RequesterID, entry); err != nil {
			return errors.New("could not list a channel request as pending")
		}
		rawBefore, _ := json.Marshal(req) // the bytes storeRequest wrote
		approvalPostID, approvalChannelID, dmPostIDs, err := p.postApprovalRequest(req, users[spec.Requester])
		if err != nil {
			return errors.New("could not post a channel request's approval card")
		}
		p.saveApprovalPosts(pendingKindChannel, req.ID, dmPostIDs)
		req.ApprovalPostID, req.ApprovalChannelID = approvalPostID, approvalChannelID
		req.DMRootPostID, req.DMChannelID = p.sendTicketCreatedDM(req, users[spec.Requester])
		p.persistTicketPosts(ticketPersist{
			KVKey: kvRequestPrefix + req.ID, RawBefore: rawBefore, Record: req,
			RequestID: req.ID, Label: "exercise channel request",
			Thread: ticketThread{
				ApprovalPostID: approvalPostID, ApprovalChannelID: approvalChannelID,
				DMRootPostID: req.DMRootPostID, DMChannelID: req.DMChannelID,
				Kind: requestTypeChannel, ScopeID: req.TeamID,
			},
		})
		keys := exerciseRequestKeys(req.ID, approvalPostID, req.DMRootPostID)
		if err := made(func(m *exerciseManifest) {
			m.Done[spec.OpKey] = req.ID
			m.KVKeys = append(m.KVKeys, keys...)
		}, undo); err != nil {
			return err
		}
	}

	for _, spec := range plan.AdminRequests {
		channel := channels[spec.ChannelOpKey]
		req := &adminRequest{
			ID: spec.ID, RequesterID: users[spec.Requester].Id, ChannelID: channel.Id,
			NomineeIDs: ids(spec.Nominees), CreatedAt: spec.CreatedAt, Exercise: true,
		}
		if err := p.storeAdminRequest(req); err != nil {
			return errors.New("could not save a Channel Admin request")
		}
		undo := func() { _, _ = p.removeExerciseRequest(pendingKindAdmin, req.ID) }
		entry := pendingEntry{ID: req.ID, CreatedAt: req.CreatedAt, Key: adminRequestKey(req)}
		if err := p.addPendingEntry(pendingKindAdmin, req.RequesterID, entry); err != nil {
			return errors.New("could not list a Channel Admin request as pending")
		}
		rawBefore, _ := json.Marshal(req) // the bytes storeAdminRequest wrote
		approvalPostID, approvalChannelID, dmPostIDs, err := p.postAdminApprovalRequest(req, users[spec.Requester], channel)
		if err != nil {
			return errors.New("could not post a Channel Admin request's approval card")
		}
		p.saveApprovalPosts(pendingKindAdmin, req.ID, dmPostIDs)
		req.ApprovalPostID, req.ApprovalChannelID = approvalPostID, approvalChannelID
		req.DMRootPostID, req.DMChannelID = p.sendAdminTicketCreatedDM(req, users[spec.Requester], channel)
		p.persistTicketPosts(ticketPersist{
			KVKey: kvAdminRequestPrefix + req.ID, RawBefore: rawBefore, Record: req,
			RequestID: req.ID, Label: "exercise Channel Admin request",
			Thread: ticketThread{
				ApprovalPostID: approvalPostID, ApprovalChannelID: approvalChannelID,
				DMRootPostID: req.DMRootPostID, DMChannelID: req.DMChannelID,
				Kind: requestTypeChannelAdmin, ScopeID: req.ChannelID,
			},
		})
		keys := exerciseRequestKeys(req.ID, approvalPostID, req.DMRootPostID)
		if err := made(func(m *exerciseManifest) {
			m.Done[spec.OpKey] = req.ID
			m.KVKeys = append(m.KVKeys, keys...)
		}, undo); err != nil {
			return err
		}
	}
	return nil
}

// ensureExercisePersona returns the persona's active account, creating it
// or reactivating the one an earlier teardown deactivated, and makes sure
// it is on the team.
func (p *Plugin) ensureExercisePersona(persona exercisePersona, teamID string) (*model.User, error) {
	user, err := p.findExercisePersona(persona, teamID)
	if err != nil {
		return nil, err
	}
	switch {
	case user == nil:
		newUser := &model.User{
			Username:  persona.Username,
			Email:     persona.Email,
			FirstName: persona.FirstName,
			LastName:  persona.LastName,
			Nickname:  exerciseMarker,
			Position:  persona.Position,
			Password:  exercisePassword(),
		}
		newUser.SetDefaultNotifications()
		newUser.NotifyProps[model.EmailNotifyProp] = "false"
		var appErr *model.AppError
		if user, appErr = p.API.CreateUser(newUser); appErr != nil {
			return nil, p.exerciseFailure("could not create @"+persona.Username, appErr)
		}
		if appErr = p.API.KVSet(kvExercisePersonaPrefix+user.Id, []byte(teamID)); appErr != nil {
			// Unrecorded, the account would never be treated as a persona
			// again, so don't leave it active.
			_ = p.API.UpdateUserActive(user.Id, false)
			return nil, p.exerciseFailure("could not record @"+persona.Username, appErr)
		}
	case user.DeleteAt != 0:
		if appErr := p.API.UpdateUserActive(user.Id, true); appErr != nil {
			return nil, p.exerciseFailure("could not reactivate @"+persona.Username, appErr)
		}
	}
	if _, appErr := p.API.CreateTeamMember(teamID, user.Id); appErr != nil {
		return nil, p.exerciseFailure(fmt.Sprintf("could not add @%s to the team", persona.Username), appErr)
	}
	return user, nil
}

// exerciseTeardownCounts tallies what teardown removed.
type exerciseTeardownCounts struct {
	removed, archived, deactivated, failed int
}

// teardownExercise removes everything the team's manifest names: pending
// exercise requests and their cards, exercise channels, and personas. Real
// requests and accounts are never changed. The manifest is kept when
// anything fails, so teardown can be run again.
func (p *Plugin) teardownExercise(teamID string) (string, error) {
	m, _, err := p.loadExerciseManifest(teamID)
	if errors.Is(err, errNoExerciseManifest) {
		return "No exercise data is recorded for this team, so there is nothing to tear down.", nil
	}
	if err != nil {
		return "", err
	}
	if m.State == exerciseStatePopulating && model.GetMillis()-m.UpdatedAt < exerciseStaleMillis {
		return "", errors.New("a populate is still running in this team. Wait for it to finish, then run teardown")
	}
	// A populate that is still running stops at its next step.
	err = p.updateExerciseManifest(teamID, func(m *exerciseManifest) error {
		m.State = exerciseStateTearingDown
		return nil
	})
	if err != nil {
		return "", err
	}

	// Each pass removes everything the manifest names, then deletes the
	// manifest with a compare-and-delete. A populate or approval that
	// records something in between makes the delete fail, and the next
	// pass removes that too. Every step skips what is already gone.
	var counts exerciseTeardownCounts
	for range manifestUpdateAttempts {
		latest, raw, loadErr := p.loadExerciseManifest(teamID)
		if loadErr != nil {
			return "", loadErr
		}
		counts.failed = 0
		p.teardownPass(teamID, latest, &counts)
		if counts.failed > 0 {
			return "", errors.Errorf("%d exercise items could not be removed; see the server logs, then run `/mattermost-request-hub exercise teardown` again", counts.failed)
		}
		deleted, appErr := p.API.KVCompareAndDelete(exerciseManifestKey(teamID), raw)
		if appErr != nil {
			p.API.LogWarn("failed to clear exercise record", "team_id", teamID, "error", appErr.Error())
			return "", errors.New("removed the exercise data but could not clear its record. Run `/mattermost-request-hub exercise teardown` again")
		}
		if deleted {
			return fmt.Sprintf("Removed the exercise data from this team: %d pending requests closed, %d channels archived and %d personas deactivated.",
				counts.removed, counts.archived, counts.deactivated), nil
		}
	}
	return "", errors.New("the exercise record kept changing while it was removed. Run `/mattermost-request-hub exercise teardown` again")
}

// teardownPass removes the requests, channels and personas the manifest
// names, adding to counts.
func (p *Plugin) teardownPass(teamID string, m *exerciseManifest, counts *exerciseTeardownCounts) {
	plan := m.Plan
	removeRequest := func(kind, id string) {
		ok, err := p.removeExerciseRequest(kind, id)
		switch {
		case err != nil:
			p.API.LogWarn("failed to remove exercise request", "request_id", id, "error", err.Error())
			counts.failed++
		case ok:
			counts.removed++
		}
	}
	for _, spec := range plan.ChannelRequests {
		removeRequest(pendingKindChannel, spec.ID)
	}
	for _, spec := range plan.AdminRequests {
		removeRequest(pendingKindAdmin, spec.ID)
	}
	for _, key := range m.KVKeys {
		if appErr := p.API.KVDelete(key); appErr != nil {
			p.API.LogWarn("failed to delete exercise record", "key", key, "error", appErr.Error())
			counts.failed++
		}
	}

	// A crash right after creating a channel leaves it unrecorded. Its
	// name finds it, but only a channel a persona created counts, since
	// members can rename a channel and change its header.
	channelIDs := append([]string{}, m.ChannelIDs...)
	var planned []string
	for _, spec := range plan.Channels {
		planned = append(planned, spec.Name)
	}
	for _, spec := range plan.ChannelRequests {
		planned = append(planned, spec.Name)
	}
	for _, name := range planned {
		ch, appErr := p.API.GetChannelByName(teamID, name, false)
		if isNotFound(appErr) {
			continue
		}
		if appErr != nil {
			p.API.LogWarn("failed to look up exercise channel", "name", name, "error", appErr.Error())
			counts.failed++
			continue
		}
		isPersona, err := p.isExercisePersonaID(ch.CreatorId, teamID)
		if err != nil {
			counts.failed++
			continue
		}
		if isPersona {
			channelIDs = append(channelIDs, ch.Id)
		}
	}
	for _, id := range dedupeNonEmpty(channelIDs) {
		ok, err := p.archiveExerciseChannel(id)
		switch {
		case err != nil:
			p.API.LogWarn("failed to archive exercise channel", "channel_id", id, "error", err.Error())
			counts.failed++
		case ok:
			counts.archived++
		}
	}

	for _, persona := range plan.Personas {
		user, err := p.findExercisePersona(persona, teamID)
		if isPersonaTaken(err) {
			// Never created, and the name has since gone to someone real.
			continue
		}
		if err != nil {
			p.API.LogWarn("failed to find exercise persona", "username", persona.Username, "error", err.Error())
			counts.failed++
			continue
		}
		if user == nil {
			continue
		}
		// A persona's pending lists only ever name exercise requests.
		for _, kind := range []string{pendingKindChannel, pendingKindAdmin} {
			if appErr := p.API.KVDelete(pendingListKey(kind, user.Id)); appErr != nil {
				p.API.LogWarn("failed to delete exercise persona's pending list", "user_id", user.Id, "error", appErr.Error())
				counts.failed++
			}
		}
		if user.DeleteAt != 0 {
			continue
		}
		if appErr := p.API.UpdateUserActive(user.Id, false); appErr != nil {
			p.API.LogWarn("failed to deactivate exercise persona", "user_id", user.Id, "error", appErr.Error())
			counts.failed++
			continue
		}
		counts.deactivated++
	}
}

// removeExerciseRequest claims and deletes a pending exercise request,
// releases its pending entry and resolves its cards. It reports false when
// the request was already decided.
func (p *Plugin) removeExerciseRequest(kind, requestID string) (bool, error) {
	key := requestKVPrefix(kind) + requestID
	data, appErr := p.API.KVGet(key)
	if appErr != nil {
		return false, errors.Wrap(appErr, "failed to load the request")
	}
	if data == nil {
		return false, nil
	}
	var rec struct {
		RequesterID    string `json:"requester_id"`
		Exercise       bool   `json:"exercise"`
		ApprovalPostID string `json:"approval_post_id"`
		DMRootPostID   string `json:"dm_root_post_id"`
		DMChannelID    string `json:"dm_channel_id"`
	}
	if err := json.Unmarshal(data, &rec); err != nil {
		return false, errors.Wrap(err, "failed to read the request")
	}
	if !rec.Exercise {
		return false, errors.New("the stored request isn't exercise data, so it was left alone")
	}
	// The same claim an approval makes, so only one of them wins.
	claimed, appErr := p.API.KVCompareAndDelete(key, data)
	if appErr != nil {
		return false, errors.Wrap(appErr, "failed to remove the request")
	}
	if !claimed {
		return false, nil
	}
	p.releasePending(kind, rec.RequesterID, requestID)
	// Close the persona's ticket and every approval card the way an
	// expiry would: no decision was made.
	p.finish(newDecisionCards(requestID, "", rec.ApprovalPostID), "", exerciseRemovedText, p.closeTicket(ticketClose{
		RequesterID: rec.RequesterID, DMRootPostID: rec.DMRootPostID, DMChannelID: rec.DMChannelID,
		ApprovalPostID: rec.ApprovalPostID, Expired: true,
		CardTitle:   exerciseDisplayPrefix + "Request Removed",
		CardText:    "Teardown removed this exercise request.",
		ThreadReply: exerciseRemovedText,
		StatusLine:  exerciseRemovedText,
	}))
	for _, key := range exerciseRequestKeys(requestID, rec.ApprovalPostID, rec.DMRootPostID) {
		if appErr := p.API.KVDelete(key); appErr != nil {
			p.API.LogWarn("failed to delete exercise record", "key", key, "error", appErr.Error())
		}
	}
	return true, nil
}

// exerciseRequestKeys names the records a request leaves besides itself:
// its approval card copies and the two reply-forwarding lookups, which
// outlive a decision.
func exerciseRequestKeys(requestID, approvalPostID, dmRootPostID string) []string {
	keys := []string{kvApprovalPostsPrefix + requestID}
	if approvalPostID != "" {
		keys = append(keys, kvApprovalPostPrefix+approvalPostID)
	}
	if dmRootPostID != "" {
		keys = append(keys, kvDMRootPrefix+dmRootPostID)
	}
	return keys
}

// archiveExerciseChannel renames a channel so its name can be used again,
// then archives it. It reports false when the channel is already gone.
func (p *Plugin) archiveExerciseChannel(channelID string) (bool, error) {
	channel, appErr := p.API.GetChannel(channelID)
	if isNotFound(appErr) {
		return false, nil
	}
	if appErr != nil {
		return false, errors.Wrap(appErr, "failed to load the channel")
	}
	if channel.DeleteAt != 0 {
		return false, nil
	}
	tail := "-" + channel.Id
	if !strings.HasSuffix(channel.Name, tail) {
		base := channel.Name
		if len(base)+len(tail) > model.ChannelNameMaxLength {
			base = strings.TrimRight(base[:model.ChannelNameMaxLength-len(tail)], "-")
		}
		channel.Name = base + tail
		if _, appErr = p.API.UpdateChannel(channel); appErr != nil {
			return false, errors.Wrap(appErr, "failed to rename the channel")
		}
	}
	if appErr = p.API.DeleteChannel(channelID); appErr != nil {
		return false, errors.Wrap(appErr, "failed to archive the channel")
	}
	return true, nil
}

// exerciseStatus compares what the team's manifest recorded with what is
// still live.
func (p *Plugin) exerciseStatus(teamID string) (string, error) {
	m, _, err := p.loadExerciseManifest(teamID)
	if errors.Is(err, errNoExerciseManifest) {
		return "No exercise data is recorded for this team. Run `/mattermost-request-hub exercise populate` to seed it.", nil
	}
	if err != nil {
		return "", err
	}
	plan := m.Plan

	unchecked := 0
	activePersonas := 0
	for _, persona := range plan.Personas {
		user, findErr := p.findExercisePersona(persona, teamID)
		switch {
		case isPersonaTaken(findErr):
		case findErr != nil:
			unchecked++
		case user != nil && user.DeleteAt == 0:
			activePersonas++
		}
	}
	liveChannels := 0
	for _, id := range m.ChannelIDs {
		ch, appErr := p.API.GetChannel(id)
		switch {
		case appErr != nil && !isNotFound(appErr):
			unchecked++
		case appErr == nil && ch.DeleteAt == 0:
			liveChannels++
		}
	}
	pending := map[string]int{}
	countPending := func(kind, id string) {
		ok, existsErr := p.pendingRequestExists(kind, id)
		switch {
		case existsErr != nil:
			unchecked++
		case ok:
			pending[kind]++
		}
	}
	for _, spec := range plan.ChannelRequests {
		countPending(pendingKindChannel, spec.ID)
	}
	for _, spec := range plan.AdminRequests {
		countPending(pendingKindAdmin, spec.ID)
	}

	var b strings.Builder
	fmt.Fprintf(&b, "Exercise **%s** in this team is **%s**.\n\n", escapeMarkdown(plan.Scenario), strings.ReplaceAll(m.State, "_", " "))
	b.WriteString("| | Recorded | Live |\n|---|---|---|\n")
	fmt.Fprintf(&b, "| Personas (active) | %d | %d |\n", len(plan.Personas), activePersonas)
	fmt.Fprintf(&b, "| Channels (not archived) | %d | %d |\n", len(m.ChannelIDs), liveChannels)
	fmt.Fprintf(&b, "| Channel requests (pending) | %d | %d |\n", len(plan.ChannelRequests), pending[pendingKindChannel])
	fmt.Fprintf(&b, "| Channel Admin requests (pending) | %d | %d |", len(plan.AdminRequests), pending[pendingKindAdmin])
	if unchecked > 0 {
		p.API.LogWarn("exercise status could not check some items", "team_id", teamID, "count", unchecked)
		fmt.Fprintf(&b, "\n\n%d items could not be checked, so the live counts may be low. See the server logs.", unchecked)
	}
	switch m.State {
	case exerciseStatePopulating:
		b.WriteString("\n\nIf no populate is running, it stopped partway. Run `/mattermost-request-hub exercise teardown` to remove what it created.")
	case exerciseStateTearingDown:
		b.WriteString("\n\nA teardown started but didn't finish. Run `/mattermost-request-hub exercise teardown` again.")
	}
	return b.String(), nil
}
