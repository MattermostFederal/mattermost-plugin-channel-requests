package main

import (
	"cmp"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/pkg/errors"
)

// errNoVisibleRequest is the user-facing answer when a request doesn't
// exist or the user may not see it. Both read the same, so the answer
// doesn't say which.
var errNoVisibleRequest = errors.New("no pending request with that ID. It may have been approved, denied or closed; the bot sends the requester the outcome")

// listRequestsDisabledMsg is the list reply when both request types the
// list covers are turned off.
const listRequestsDisabledMsg = "Channel and Channel Admin requests are currently disabled by an administrator."

// maxListedRequests caps how many requests the list views load and show.
const maxListedRequests = 50

// requestSummary describes one pending request for the list and status
// views.
type requestSummary struct {
	ID      string
	Kind    string // pendingKindChannel or pendingKindAdmin
	Summary string // Markdown: what was requested, and where
	Expired bool
	// CreatedAt and ExpiresAt are in milliseconds. ExpiresAt is 0 when
	// requests never expire, or for a request saved by an older version
	// that has no CreatedAt yet.
	CreatedAt int64
	ExpiresAt int64
}

// pendingRecord is a stored request of either kind. Exactly one of
// channel and admin is set.
type pendingRecord struct {
	channel *channelRequest
	admin   *adminRequest
}

func (r *pendingRecord) requesterID() string {
	if r.admin != nil {
		return r.admin.RequesterID
	}
	return r.channel.RequesterID
}

// enabledPendingKinds returns the request kinds the list and status views
// cover, leaving out any type an administrator turned off.
func enabledPendingKinds(config *configuration) []string {
	var kinds []string
	if config.RequestEnabled(requestTypeChannel) {
		kinds = append(kinds, pendingKindChannel)
	}
	if config.RequestEnabled(requestTypeChannelAdmin) {
		kinds = append(kinds, pendingKindAdmin)
	}
	return kinds
}

// loadPendingRecord loads a stored request of the given kind, or returns
// nil when it isn't stored.
func (p *Plugin) loadPendingRecord(kind, requestID string) (*pendingRecord, error) {
	if kind == pendingKindAdmin {
		req, _, err := p.loadAdminRequest(requestID)
		if err != nil || req == nil {
			return nil, err
		}
		return &pendingRecord{admin: req}, nil
	}
	req, _, err := p.loadRequest(requestID)
	if err != nil || req == nil {
		return nil, err
	}
	return &pendingRecord{channel: req}, nil
}

// listOwnRequests returns up to limit of the user's pending requests of
// both kinds, oldest first, and how many more there are. A kind whose
// request type is turned off is left out. It reads the pending lists
// without changing them. Entries whose request no longer
// exists are skipped, and so are unreadable records, which are logged.
func (p *Plugin) listOwnRequests(userID string, limit int) ([]requestSummary, int, error) {
	type listed struct {
		kind  string
		entry pendingEntry
	}
	config := p.getConfiguration()
	var entries []listed
	for _, kind := range enabledPendingKinds(config) {
		raw, appErr := p.API.KVGet(pendingListKey(kind, userID))
		if appErr != nil {
			return nil, 0, errors.Wrap(appErr, "failed to load pending requests")
		}
		if raw == nil {
			continue
		}
		var list []pendingEntry
		if err := json.Unmarshal(raw, &list); err != nil {
			return nil, 0, errors.Wrap(err, "failed to read pending requests")
		}
		for _, e := range list {
			entries = append(entries, listed{kind: kind, entry: e})
		}
	}
	slices.SortStableFunc(entries, func(a, b listed) int {
		return cmp.Compare(a.entry.CreatedAt, b.entry.CreatedAt)
	})

	var out []requestSummary
	for i, e := range entries {
		if len(out) == limit {
			// Not loaded; some may since have been decided.
			return out, len(entries) - i, nil
		}
		rec, err := p.loadPendingRecord(e.kind, e.entry.ID)
		if err != nil {
			if isUnmarshalError(err) {
				p.API.LogWarn("skipping unreadable stored request", "request_id", e.entry.ID, "error", err.Error())
				continue
			}
			return nil, 0, err
		}
		// A request decided after its entry failed to release is gone, and
		// its entry is dropped on the next submit.
		if rec != nil && rec.requesterID() == userID {
			out = append(out, p.summarize(config, rec))
		}
	}
	return out, 0, nil
}

// isUnmarshalError reports whether err came from decoding a stored record
// rather than from reading it.
func isUnmarshalError(err error) bool {
	var syntaxErr *json.SyntaxError
	var typeErr *json.UnmarshalTypeError
	return errors.As(err, &syntaxErr) || errors.As(err, &typeErr)
}

// requestStatus returns one pending request, if the user may see it: its
// requester, or someone who can approve it. A request whose type is turned
// off reads as not found.
func (p *Plugin) requestStatus(userID, requestID string) (*requestSummary, error) {
	if !model.IsValidId(requestID) {
		return nil, errNoVisibleRequest
	}
	config := p.getConfiguration()
	for _, kind := range enabledPendingKinds(config) {
		rec, err := p.loadPendingRecord(kind, requestID)
		if err != nil {
			return nil, err
		}
		if rec == nil {
			continue
		}
		if rec.requesterID() != userID && !p.canDecide(userID, rec) {
			return nil, errNoVisibleRequest
		}
		summary := p.summarize(config, rec)
		return &summary, nil
	}
	return nil, errNoVisibleRequest
}

// canDecide reports whether the user can approve or deny the request, by
// the same rules the decisions use.
func (p *Plugin) canDecide(userID string, rec *pendingRecord) bool {
	user, appErr := p.API.GetUser(userID)
	if appErr != nil {
		p.API.LogWarn("failed to load user for request status", "user_id", userID, "error", appErr.Error())
		return false
	}
	if rec.admin != nil {
		return p.canApproveAdminRequest(user, rec.admin.ChannelID)
	}
	return p.canApproveChannelRequest(user, rec.channel.TeamID)
}

// summarize describes a stored request for the list and status views.
func (p *Plugin) summarize(config *configuration, rec *pendingRecord) requestSummary {
	var s requestSummary
	if req := rec.admin; req != nil {
		s.ID, s.Kind, s.CreatedAt = req.ID, pendingKindAdmin, req.CreatedAt
		s.Summary = fmt.Sprintf("Channel Admin for %s in %s", p.mentionList(req.NomineeIDs), p.channelLabel(req.ChannelID))
	} else {
		req := rec.channel
		s.ID, s.Kind, s.CreatedAt = req.ID, pendingKindChannel, req.CreatedAt
		s.Summary = fmt.Sprintf("Channel **%s** (`%s`) in %s", escapeMarkdown(req.DisplayName), req.Name, escapeMarkdown(p.teamDisplayName(req.TeamID)))
	}
	s.Expired = requestExpired(s.CreatedAt, config.ExpiryDays(), model.GetMillis())
	if days := config.ExpiryDays(); days > 0 && s.CreatedAt > 0 {
		s.ExpiresAt = s.CreatedAt + int64(days)*millisPerDay
	}
	return s
}

// channelLabel renders a channel as ~name for summaries, falling back to
// "a channel that no longer exists".
func (p *Plugin) channelLabel(channelID string) string {
	channel, appErr := p.API.GetChannel(channelID)
	if appErr != nil || channel == nil {
		return "a channel that no longer exists"
	}
	return "~" + channel.Name
}

// summaryLine renders a request as one Markdown line for the slash
// command.
func (s requestSummary) summaryLine() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s, submitted %s", s.Summary, formatDay(s.CreatedAt))
	switch {
	case s.Expired:
		b.WriteString(", expired (an approver can close it)")
	case s.ExpiresAt > 0:
		fmt.Fprintf(&b, ", expires %s", formatDay(s.ExpiresAt))
	}
	fmt.Fprintf(&b, ". ID `%s`", s.ID)
	return b.String()
}

// formatDay renders a millisecond time as a UTC date, or "before the
// upgrade" for a request saved without one.
func formatDay(millis int64) string {
	if millis <= 0 {
		return "before the upgrade"
	}
	return model.GetTimeForMillis(millis).UTC().Format(time.DateOnly)
}
