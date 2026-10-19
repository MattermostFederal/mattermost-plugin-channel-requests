package main

import (
	"encoding/json"
	"testing"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin/plugintest"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestAttributeValueIsSet(t *testing.T) {
	textField := &model.PropertyField{Type: model.PropertyFieldTypeText}
	set := func(s string) bool { return attributeValueIsSet(json.RawMessage(s), textField) }

	// Set values (text / bool / array).
	require.True(t, set(`"yes"`))
	require.True(t, set(`"security"`))
	require.True(t, set(`["a","b"]`))
	require.True(t, set(`true`))

	// Unset / false-y values.
	require.False(t, set(``))
	require.False(t, set(`""`))
	require.False(t, set(`"no"`))
	require.False(t, set(`"false"`))
	require.False(t, set(`"0"`))
	require.False(t, set(`[]`))
	require.False(t, set(`false`))
}

func TestAttributeValueIsSet_SelectResolvesOptionNames(t *testing.T) {
	yesID := model.NewId()
	noID := model.NewId()
	field := &model.PropertyField{
		Type: model.PropertyFieldTypeSelect,
		Attrs: model.StringInterface{
			model.PropertyFieldAttributeOptions: []*model.CustomProfileAttributesSelectOption{
				{ID: yesID, Name: "Yes"},
				{ID: noID, Name: "No"},
			},
		},
	}

	require.True(t, attributeValueIsSet(json.RawMessage(`"`+yesID+`"`), field))
	require.False(t, attributeValueIsSet(json.RawMessage(`"`+noID+`"`), field))
	// Opaque ID with no matching option must not authorize.
	require.False(t, attributeValueIsSet(json.RawMessage(`"opt-unknown"`), field))
}

// mockApproverAttr wires the three Property API calls for one attribute lookup.
func mockApproverAttr(api *plugintest.API, attrName, userID string, values []*model.PropertyValue) {
	api.On("GetPropertyGroup", model.AccessControlPropertyGroupName).Return(&model.PropertyGroup{ID: "grp"}, nil)
	api.On("GetPropertyFieldByName", "grp", "", attrName).Return(&model.PropertyField{
		ID:   "fld-" + attrName,
		Type: model.PropertyFieldTypeText,
	}, nil)
	api.On("SearchPropertyValues", "grp", mock.MatchedBy(func(opts model.PropertyValueSearchOpts) bool {
		return opts.TargetType == model.PropertyValueTargetTypeUser &&
			len(opts.TargetIDs) == 1 && opts.TargetIDs[0] == userID &&
			opts.FieldID == "fld-"+attrName
	})).Return(values, nil)
}

func TestUserIsApprover(t *testing.T) {
	t.Run("empty attribute name is never an approver", func(t *testing.T) {
		api := &plugintest.API{}
		p := newTestPlugin(api)
		ok, err := p.userIsApprover(&model.User{Id: "u1"}, "  ")
		require.NoError(t, err)
		require.False(t, ok)
		api.AssertNotCalled(t, "GetPropertyGroup", mock.Anything)
	})

	t.Run("set attribute value means approver", func(t *testing.T) {
		api := &plugintest.API{}
		p := newTestPlugin(api)
		mockApproverAttr(api, "security_approver", "u1", []*model.PropertyValue{{Value: json.RawMessage(`"yes"`)}})
		ok, err := p.userIsApprover(&model.User{Id: "u1"}, "security_approver")
		require.NoError(t, err)
		require.True(t, ok)
	})

	t.Run("no value means not an approver", func(t *testing.T) {
		api := &plugintest.API{}
		p := newTestPlugin(api)
		mockApproverAttr(api, "security_approver", "u1", []*model.PropertyValue{})
		ok, err := p.userIsApprover(&model.User{Id: "u1"}, "security_approver")
		require.NoError(t, err)
		require.False(t, ok)
	})
}

func TestCanApproveStep(t *testing.T) {
	t.Run("system admin auto-qualifies for the system step", func(t *testing.T) {
		api := &plugintest.API{}
		p := newTestPlugin(api)
		p.setConfiguration(&configuration{SystemApproverAttribute: "system_approver"})
		ok, err := p.canApproveStep(&model.User{Id: "a", Roles: model.SystemAdminRoleId}, stepSystem)
		require.NoError(t, err)
		require.True(t, ok)
		// The role shortcut must skip the attribute lookup entirely.
		api.AssertNotCalled(t, "GetPropertyGroup", mock.Anything)
	})

	t.Run("system admin does NOT auto-qualify for the security step", func(t *testing.T) {
		api := &plugintest.API{}
		p := newTestPlugin(api)
		p.setConfiguration(&configuration{SecurityApproverAttribute: "security_approver"})
		// No security attribute value set for this admin.
		mockApproverAttr(api, "security_approver", "a", []*model.PropertyValue{})
		ok, err := p.canApproveStep(&model.User{Id: "a", Roles: model.SystemAdminRoleId}, stepSecurity)
		require.NoError(t, err)
		require.False(t, ok)
	})

	t.Run("non-admin with the security attribute qualifies for the security step", func(t *testing.T) {
		api := &plugintest.API{}
		p := newTestPlugin(api)
		p.setConfiguration(&configuration{SecurityApproverAttribute: "security_approver"})
		mockApproverAttr(api, "security_approver", "u2", []*model.PropertyValue{{Value: json.RawMessage(`"yes"`)}})
		ok, err := p.canApproveStep(&model.User{Id: "u2"}, stepSecurity)
		require.NoError(t, err)
		require.True(t, ok)
	})
}
