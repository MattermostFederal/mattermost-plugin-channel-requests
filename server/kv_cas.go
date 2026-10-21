package main

import (
	"encoding/json"

	"github.com/pkg/errors"
)

// kvUpdateIfUnchanged replaces key's value only when the current bytes still
// match expected. Used after posting approval/DM IDs so a concurrent approve
// that deleted the pending record cannot be resurrected by an unconditional KVSet.
func (p *Plugin) kvUpdateIfUnchanged(key string, expected []byte, next any) (updated bool, err error) {
	data, err := json.Marshal(next)
	if err != nil {
		return false, errors.Wrap(err, "failed to marshal for KV update")
	}
	set, appErr := p.API.KVCompareAndSet(key, expected, data)
	if appErr != nil {
		return false, errors.Wrap(appErr, "failed to CAS-update KV record")
	}
	return set, nil
}
