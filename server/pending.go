package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/pkg/errors"
)

const (
	// kvPendingPrefix namespaces each user's list of pending requests.
	kvPendingPrefix = "pending_"

	pendingKindChannel = "channel"
	pendingKindAdmin   = "admin"

	// pendingUpdateAttempts bounds the compare-and-set retries when the
	// same user's list is being updated at the same time.
	pendingUpdateAttempts = 5
)

// pendingEntry is one pending request in a user's list. Key identifies
// what was requested, so the same request can't be pending twice.
type pendingEntry struct {
	ID        string `json:"id"`
	CreatedAt int64  `json:"created_at"`
	Key       string `json:"key"`
}

func pendingListKey(kind, userID string) string {
	return kvPendingPrefix + kind + "_" + userID
}

// channelRequestKey identifies a channel request by team and channel name.
func channelRequestKey(req *channelRequest) string {
	return req.TeamID + "/" + req.Name
}

// adminRequestKey identifies a Channel Admin request by channel and the
// set of nominees, in any order.
func adminRequestKey(req *adminRequest) string {
	nominees := slices.Clone(req.NomineeIDs)
	slices.Sort(nominees)
	return req.ChannelID + "/" + strings.Join(nominees, ",")
}

// updatePending applies change to a user's pending list with
// compare-and-set, retrying when another update lands first. An empty
// result removes the list.
func (p *Plugin) updatePending(kind, userID string, change func([]pendingEntry) ([]pendingEntry, error)) error {
	key := pendingListKey(kind, userID)
	for range pendingUpdateAttempts {
		raw, appErr := p.API.KVGet(key)
		if appErr != nil {
			return errors.Wrap(appErr, "failed to load pending requests")
		}
		var entries []pendingEntry
		if raw != nil {
			if err := json.Unmarshal(raw, &entries); err != nil {
				return errors.Wrap(err, "failed to read pending requests")
			}
		}

		updated, err := change(entries)
		if err != nil {
			return err
		}

		var ok bool
		if len(updated) == 0 {
			if raw == nil {
				return nil
			}
			ok, appErr = p.API.KVCompareAndDelete(key, raw)
		} else {
			data, marshalErr := json.Marshal(updated)
			if marshalErr != nil {
				return errors.Wrap(marshalErr, "failed to save pending requests")
			}
			if bytes.Equal(data, raw) {
				return nil
			}
			ok, appErr = p.API.KVCompareAndSet(key, raw, data)
		}
		if appErr != nil {
			return errors.Wrap(appErr, "failed to save pending requests")
		}
		if ok {
			return nil
		}
	}
	return errors.New("pending requests changed while saving")
}

// reservePending adds entry to the user's pending list, refusing when the
// same request is already pending or the user is at the configured limit.
// Expired entries don't count, and entries whose request no longer exists
// are dropped. Errors are user-facing.
func (p *Plugin) reservePending(kind, userID string, entry pendingEntry, config *configuration) error {
	var refusal error
	err := p.updatePending(kind, userID, func(entries []pendingEntry) ([]pendingEntry, error) {
		refusal = nil
		now := model.GetMillis()
		kept := make([]pendingEntry, 0, len(entries)+1)
		var counted []pendingEntry
		for _, e := range entries {
			if e.ID == entry.ID {
				continue
			}
			if e.Key == entry.Key && !requestExpired(e.CreatedAt, config.ExpiryDays(), now) {
				exists, err := p.pendingRequestExists(kind, e.ID)
				if err != nil {
					return nil, err
				}
				if !exists {
					continue
				}
				refusal = duplicateRequestError(kind, entry.Key)
				return nil, refusal
			}
			kept = append(kept, e)
			if !requestExpired(e.CreatedAt, config.ExpiryDays(), now) {
				counted = append(counted, e)
			}
		}
		if limit := config.MaxPending(); limit > 0 && len(counted) >= limit {
			// Only look up the requests when the list says the user is at
			// the limit, so the usual submit costs no extra reads.
			gone := map[string]bool{}
			for _, e := range counted {
				exists, err := p.pendingRequestExists(kind, e.ID)
				if err != nil {
					return nil, err
				}
				if !exists {
					gone[e.ID] = true
				}
			}
			if len(counted)-len(gone) >= limit {
				refusal = fmt.Errorf("you have %d pending requests waiting for review; you can submit more once some are approved, denied or expire", len(counted)-len(gone))
				return nil, refusal
			}
			kept = slices.DeleteFunc(kept, func(e pendingEntry) bool { return gone[e.ID] })
		}
		return append(kept, entry), nil
	})
	if refusal != nil {
		return refusal
	}
	if err != nil {
		p.API.LogError("failed to reserve pending request", "user_id", userID, "error", err.Error())
		return errors.New("could not save your request; try again")
	}
	return nil
}

// pendingRequestExists reports whether the request behind a pending entry
// is still stored.
func (p *Plugin) pendingRequestExists(kind, requestID string) (bool, error) {
	data, appErr := p.API.KVGet(requestKVPrefix(kind) + requestID)
	if appErr != nil {
		return false, errors.Wrap(appErr, "failed to load pending request")
	}
	return data != nil, nil
}

// requestKVPrefix is the KV prefix of the records of a pending kind.
func requestKVPrefix(kind string) string {
	if kind == pendingKindAdmin {
		return kvAdminRequestPrefix
	}
	return kvRequestPrefix
}

func duplicateRequestError(kind, key string) error {
	if kind == pendingKindAdmin {
		return errors.New("you already have a pending Channel Admin request for these people in this channel")
	}
	_, name, _ := strings.Cut(key, "/")
	return errors.Errorf("you already have a pending request for ~%s in this team", name)
}

// addPendingEntry adds entry to the user's list without the limit or
// duplicate checks. Used for requests saved by older versions.
func (p *Plugin) addPendingEntry(kind, userID string, entry pendingEntry) error {
	return p.updatePending(kind, userID, func(entries []pendingEntry) ([]pendingEntry, error) {
		for _, e := range entries {
			if e.ID == entry.ID {
				return entries, nil
			}
		}
		return append(entries, entry), nil
	})
}

// releasePending removes a request from the user's pending list. Failures
// are logged; reservePending drops entries whose request no longer exists.
func (p *Plugin) releasePending(kind, userID, requestID string) {
	err := p.updatePending(kind, userID, func(entries []pendingEntry) ([]pendingEntry, error) {
		return slices.DeleteFunc(entries, func(e pendingEntry) bool { return e.ID == requestID }), nil
	})
	if err != nil {
		p.API.LogWarn("failed to update pending requests", "user_id", userID, "request_id", requestID, "error", err.Error())
	}
}

const (
	// kvPendingMigrated marks that requests saved by older versions have a
	// CreatedAt and appear in their requester's pending list. While one
	// server runs the migration it holds migrationRunning, which expires so
	// an interrupted run is retried on a later activation.
	kvPendingMigrated       = "migrated_pending_v1"
	migrationDone           = "1"
	migrationRunning        = "running"
	migrationClaimSeconds   = 10 * 60
	migrationRecordAttempts = 3
)

// migratePendingRequests gives requests saved by older versions a
// CreatedAt of now, so expiry counts from the upgrade, and adds them to
// their requester's pending list. It runs once, on one server.
func (p *Plugin) migratePendingRequests() error {
	state, appErr := p.API.KVGet(kvPendingMigrated)
	if appErr != nil {
		return errors.Wrap(appErr, "failed to check pending-request migration")
	}
	if state != nil {
		return nil
	}
	claimed, appErr := p.API.KVSetWithOptions(kvPendingMigrated, []byte(migrationRunning), model.PluginKVSetOptions{
		Atomic:          true,
		OldValue:        nil,
		ExpireInSeconds: migrationClaimSeconds,
	})
	if appErr != nil {
		return errors.Wrap(appErr, "failed to start pending-request migration")
	}
	if !claimed {
		return nil // another server is running it
	}

	// List every key before changing any, so writes made along the way
	// can't shift the pages.
	const perPage = 200
	var keys []string
	for page := 0; ; page++ {
		batch, appErr := p.API.KVList(page, perPage)
		if appErr != nil {
			p.releaseMigrationClaim()
			return errors.Wrap(appErr, "failed to list stored requests")
		}
		for _, key := range batch {
			if strings.HasPrefix(key, kvRequestPrefix) || strings.HasPrefix(key, kvAdminRequestPrefix) {
				keys = append(keys, key)
			}
		}
		if len(batch) < perPage {
			break
		}
	}

	now := model.GetMillis()
	failed := 0
	for _, key := range keys {
		if err := p.migratePendingRequest(key, now); err != nil {
			p.API.LogWarn("failed to update stored request; will retry on a later activation", "key", key, "error", err.Error())
			failed++
		}
	}
	if failed > 0 {
		p.releaseMigrationClaim()
		return errors.Errorf("%d stored requests could not be updated", failed)
	}

	if appErr := p.API.KVSet(kvPendingMigrated, []byte(migrationDone)); appErr != nil {
		return errors.Wrap(appErr, "failed to record pending-request migration")
	}
	return nil
}

// releaseMigrationClaim lets a later activation run the migration again.
func (p *Plugin) releaseMigrationClaim() {
	if appErr := p.API.KVDelete(kvPendingMigrated); appErr != nil {
		p.API.LogWarn("failed to clear pending-request migration claim", "error", appErr.Error())
	}
}

// migratePendingRequest backfills one stored request. Unreadable records
// are logged and skipped; storage errors are returned so the migration is
// retried.
func (p *Plugin) migratePendingRequest(key string, now int64) error {
	for range migrationRecordAttempts {
		raw, appErr := p.API.KVGet(key)
		if appErr != nil {
			return errors.Wrap(appErr, "failed to load stored request")
		}
		if raw == nil {
			return nil // approved or denied meanwhile
		}

		b, decodeErr := backfillRequest(key, raw, now)
		if decodeErr != nil {
			p.API.LogWarn("skipping unreadable stored request", "key", key, "error", decodeErr.Error())
			return nil
		}

		if b.updated != nil {
			// Compare against what was read so a request approved or
			// denied meanwhile isn't written back.
			ok, appErr := p.API.KVCompareAndSet(key, raw, b.updated)
			if appErr != nil {
				return errors.Wrap(appErr, "failed to update stored request")
			}
			if !ok {
				continue // changed meanwhile; read it again
			}
		}
		if err := p.addPendingEntry(b.kind, b.requesterID, b.entry); err != nil {
			return errors.Wrap(err, "failed to add request to pending list")
		}
		// The request may have been approved or denied after it was read,
		// before its entry was added; if so, take the entry back out.
		exists, err := p.pendingRequestExists(b.kind, b.entry.ID)
		if err != nil {
			return err
		}
		if !exists {
			p.releasePending(b.kind, b.requesterID, b.entry.ID)
		}
		return nil
	}
	return errors.New("stored request kept changing")
}

// backfilledRequest is a stored request ready to migrate: its pending entry,
// and the record rewritten with a CreatedAt when it had none (nil when the
// record needs no change).
type backfilledRequest struct {
	kind        string
	requesterID string
	entry       pendingEntry
	updated     []byte
}

// backfillRequest decodes a stored request and gives it a CreatedAt of now
// if it has none.
func backfillRequest(key string, raw []byte, now int64) (backfilledRequest, error) {
	var (
		b         backfilledRequest
		id        string
		createdAt int64
		dedupe    string
		record    any
	)
	if strings.HasPrefix(key, kvAdminRequestPrefix) {
		var req adminRequest
		if err := json.Unmarshal(raw, &req); err != nil {
			return b, err
		}
		if req.CreatedAt <= 0 {
			req.CreatedAt = now
			record = req
		}
		b.kind, b.requesterID, id, createdAt, dedupe = pendingKindAdmin, req.RequesterID, req.ID, req.CreatedAt, adminRequestKey(&req)
	} else {
		var req channelRequest
		if err := json.Unmarshal(raw, &req); err != nil {
			return b, err
		}
		if req.CreatedAt <= 0 {
			req.CreatedAt = now
			record = req
		}
		b.kind, b.requesterID, id, createdAt, dedupe = pendingKindChannel, req.RequesterID, req.ID, req.CreatedAt, channelRequestKey(&req)
	}
	if !model.IsValidId(id) || !model.IsValidId(b.requesterID) || !strings.HasSuffix(key, "_"+id) {
		return b, errors.New("stored request has a missing or mismatched ID")
	}
	if record != nil {
		updated, err := json.Marshal(record)
		if err != nil {
			return b, err
		}
		b.updated = updated
	}
	b.entry = pendingEntry{ID: id, CreatedAt: createdAt, Key: dedupe}
	return b, nil
}
