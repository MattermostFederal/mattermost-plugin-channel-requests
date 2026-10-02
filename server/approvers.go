package main

import (
	"encoding/json"
	"strings"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/pkg/errors"
)

// approverStep identifies which pool of a two-step request an approval is for.
// A two-step request requires one approval from EACH step, from two distinct
// users (see the engine in Phase 4's request handling).
type approverStep int

const (
	// stepSecurity is the security-team approval. It is gated purely on the
	// security attribute — System Admins do NOT auto-qualify, because the whole
	// point of this step is an independent security sign-off.
	stepSecurity approverStep = iota

	// stepSystem is the admin/dev approval. System Admins auto-qualify here (as
	// they do for every other approval in the plugin), in addition to anyone
	// holding the system approver attribute.
	stepSystem
)

func (s approverStep) String() string {
	if s == stepSecurity {
		return "security"
	}
	return "system"
}

// approverAttributeName returns the configured User Attribute name for the given
// step, trimmed. Empty means the admin hasn't configured that pool.
func (c *configuration) approverAttributeName(step approverStep) string {
	switch step {
	case stepSecurity:
		return strings.TrimSpace(c.SecurityApproverAttribute)
	case stepSystem:
		return strings.TrimSpace(c.SystemApproverAttribute)
	default:
		return ""
	}
}

// canApproveStep reports whether the user may approve the given step of a
// two-step request. System Admins may approve the SYSTEM step unconditionally;
// the SECURITY step always requires the attribute so it can't be bypassed.
// Returns an error only on a genuine attribute-lookup failure, so callers can
// distinguish "not eligible" from "couldn't check".
func (p *Plugin) canApproveStep(user *model.User, step approverStep) (bool, error) {
	if user == nil {
		return false, nil
	}
	if step == stepSystem && user.IsSystemAdmin() {
		return true, nil
	}
	return p.userIsApprover(user, p.getConfiguration().approverAttributeName(step))
}

// userIsApprover reports whether the user has a non-empty value for the named
// User Attribute (a custom profile attribute in the "access_control" property
// group). This is a pure attribute read — no role shortcuts — so it can be
// composed by canApproveStep. An empty attrName (pool not configured) is not an
// approver.
func (p *Plugin) userIsApprover(user *model.User, attrName string) (bool, error) {
	if user == nil {
		return false, nil
	}
	attrName = strings.TrimSpace(attrName)
	if attrName == "" {
		return false, nil
	}

	group, err := p.API.GetPropertyGroup(model.AccessControlPropertyGroupName)
	if err != nil {
		return false, errors.Wrap(err, "failed to load access_control property group")
	}
	if group == nil {
		return false, nil
	}

	// Custom profile attribute fields carry no target, so they're looked up by
	// name with an empty targetID.
	field, err := p.API.GetPropertyFieldByName(group.ID, "", attrName)
	if err != nil {
		return false, errors.Wrapf(err, "failed to load approver attribute %q", attrName)
	}
	if field == nil {
		return false, nil
	}

	values, err := p.API.SearchPropertyValues(group.ID, model.PropertyValueSearchOpts{
		GroupID:    group.ID,
		TargetType: model.PropertyValueTargetTypeUser,
		TargetIDs:  []string{user.Id},
		FieldID:    field.ID,
		PerPage:    10,
	})
	if err != nil {
		return false, errors.Wrapf(err, "failed to read approver attribute %q", attrName)
	}

	for _, v := range values {
		if v != nil && attributeValueIsSet(v.Value) {
			return true, nil
		}
	}
	return false, nil
}

// attributeValueIsSet reports whether a CPA value marks the user as an approver.
// CPA values are JSON: "opt-id" (select), ["a","b"] (multiselect), "text", or a
// bool. We treat a value as "set" when it's a non-empty, non-false-y string; a
// non-empty array; or boolean true. Absent/empty/false-y means not an approver,
// so admins should use an attribute that is only set for approvers (e.g. a
// yes/no select, or an LDAP/SAML-synced attribute present only for the pool).
func attributeValueIsSet(raw json.RawMessage) bool {
	if len(raw) == 0 {
		return false
	}

	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		t := strings.TrimSpace(strings.ToLower(s))
		return t != "" && t != "false" && t != "no" && t != "0"
	}

	var arr []any
	if err := json.Unmarshal(raw, &arr); err == nil {
		return len(arr) > 0
	}

	var b bool
	if err := json.Unmarshal(raw, &b); err == nil {
		return b
	}

	return false
}
