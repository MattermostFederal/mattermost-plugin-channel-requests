# Sourced by pr-preview's instance/deploy.sh after the PR's bundle is
# installed, with its helpers (compose, mmctl, wait_for_ping) and $id in
# scope. `make docker-setup` and `make deploy` only create the admin user and
# the `test` team and install the bundle, which the preview already does, so
# this adds what a reviewer needs to try the request flow: an approval channel
# in `test`, the plugin pointed at it, and native channel creation taken away
# from non-admins (the README's recommended setup, since the plugin API can't
# veto it). The database outlives deploys but the config is fresh each time,
# so every step is idempotent.

APPROVAL_TEAM=test
APPROVAL_CHANNEL=channel-requests
MM_SOCKET=docker/mattermost/run/mattermost_local.socket

mm_api() {
  curl -sf --unix-socket "$MM_SOCKET" -X "$1" "http://localhost/api/v4$2" \
    -H 'Content-Type: application/json' ${3:+-d "$3"}
}

# The channel survives redeploys with the database, so create it only when
# it's missing and fail the deploy if it still isn't there.
channel_path="/teams/name/${APPROVAL_TEAM}/channels/name/${APPROVAL_CHANNEL}"
if ! mm_api GET "$channel_path" >/dev/null; then
  mmctl channel create --team "$APPROVAL_TEAM" --name "$APPROVAL_CHANNEL" \
    --display-name "Channel Requests" >/dev/null
  mm_api GET "$channel_path" >/dev/null || { echo "approval channel ~${APPROVAL_CHANNEL} is missing"; exit 1; }
fi

# The plugin stores the approval team and channel as IDs, not names.
team_id="$(mm_api GET "/teams/name/${APPROVAL_TEAM}" | jq -er .id)" || { echo "could not look up team ${APPROVAL_TEAM}"; exit 1; }
channel_id="$(mm_api GET "$channel_path" | jq -er .id)" || { echo "could not look up ~${APPROVAL_CHANNEL}"; exit 1; }

# Channel creation is a team permission, held by team_user in the default
# scheme; system_user is cleared too in case a server version keeps it there.
CREATE_PERMS='["create_public_channel","create_private_channel"]'
# A failed role lookup must fail the deploy: an empty answer would otherwise
# read as "holds none of the permissions" and the check would pass.
role_create_perms() {
  local role_json
  role_json="$(mm_api GET "/roles/name/$1")" || { echo "could not look up role $1" >&2; return 1; }
  jq -er --argjson p "$CREATE_PERMS" '.permissions | arrays | .[] | select(IN($p[]))' <<<"$role_json" \
    || jq -e '.permissions | arrays' <<<"$role_json" >/dev/null \
    || { echo "role $1 has no permissions list" >&2; return 1; }
}
for role in team_user system_user; do
  held="$(role_create_perms "$role")" || exit 1
  [ -z "$held" ] && continue
  # shellcheck disable=SC2086
  mmctl permissions remove "$role" $held >/dev/null
  held="$(role_create_perms "$role")" || exit 1
  [ -z "$held" ] || { echo "${role} still holds ${held//$'\n'/ }"; exit 1; }
done

config="$(mm_api GET /config | jq -ec --arg id "$id" --arg team "$team_id" --arg channel "$channel_id" \
  '.PluginSettings.Plugins[$id] = ((.PluginSettings.Plugins[$id] // {}) + {approvalteam: $team, approvalchannel: $channel})')" \
  || { echo "could not read the server config"; exit 1; }
mm_api PUT /config "$config" >/dev/null || { echo "could not save the plugin config"; exit 1; }
echo "configured ${id}: approvals post to ~${APPROVAL_CHANNEL} in ${APPROVAL_TEAM}; non-admins request channels instead of creating them"
