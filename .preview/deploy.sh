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

mmctl channel create --team "$APPROVAL_TEAM" --name "$APPROVAL_CHANNEL" \
  --display-name "Channel Requests" >/dev/null 2>&1 || true

# Channel creation is a team permission, held by team_user in the default
# scheme; system_user is cleared too in case a server version keeps it there.
for role in team_user system_user; do
  mmctl permissions remove "$role" create_public_channel create_private_channel >/dev/null 2>&1 || true
done

config="$(mm_api GET /config | jq -c --arg id "$id" --arg team "$APPROVAL_TEAM" --arg channel "$APPROVAL_CHANNEL" \
  '.PluginSettings.Plugins[$id] = ((.PluginSettings.Plugins[$id] // {}) + {approvalteam: $team, approvalchannel: $channel})')"
mm_api PUT /config "$config" >/dev/null
echo "configured ${id}: approvals post to ~${APPROVAL_CHANNEL} in ${APPROVAL_TEAM}; non-admins request channels instead of creating them"
