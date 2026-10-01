import manifest from 'manifest';

export type ChannelRequestPayload = {
    team_id: string;
    display_name: string;
    name: string;

    // prefix is the selected domain prefix (e.g., "team-") when the
    // admin has configured a prefix list.
    prefix?: string;
    purpose: string;
    channel_type: string;
    members: string[];

    // admin_members are usernames the requester is proposing to have
    // Channel Admin role on the newly-created channel. Server promotes
    // them via UpdateChannelMemberRoles at creation time.
    admin_members: string[];
};

export type ChannelRequestResult = {
    message?: string;
    error?: string;
};

// TeamRequestPayload is sent by the "Request a Team" modal. members are
// usernames to add to the team once it's created.
export type TeamRequestPayload = {
    display_name: string;
    name: string;
    description: string;
    team_type: string;
    members: string[];
};

// AdminRequestPayload is sent by the "Request Channel Admin" modal. nominees
// are usernames the requester wants promoted to Channel Admin on channel_id.
export type AdminRequestPayload = {
    channel_id: string;
    nominees: string[];
};

export type ChannelPrefix = {
    prefix: string;
    description: string;
    suffix_regex: string;
};

// fetchPrefixes returns the admin-configured domain prefix list. An empty
// array means the admin hasn't configured any prefixes ("not configured").
// A load FAILURE (network error / non-OK status) THROWS so callers can
// distinguish a transient outage from a genuinely empty config — otherwise
// a 500 would masquerade as "not configured" and block valid requests.
export async function fetchPrefixes(): Promise<ChannelPrefix[]> {
    const response = await fetch(`/plugins/${manifest.id}/api/v1/prefixes`, {
        method: 'GET',
        credentials: 'same-origin',
        headers: {'X-Requested-With': 'XMLHttpRequest'},
    });
    if (!response.ok) {
        throw new Error(`Failed to load prefixes (${response.status})`);
    }
    const body = await response.json();
    return Array.isArray(body) ? body : [];
}

// EnabledRequestTypes mirrors the per-type toggles served by the server's
// /api/v1/config endpoint — which kinds of request the admin has enabled.
export type EnabledRequestTypes = {
    channel: boolean;
    channelAdmin: boolean;
    team: boolean;
    webhook: boolean;
};

// fetchEnabledRequestTypes returns which request types the admin has
// enabled, used to hide entry points for disabled types. It FAILS OPEN
// (channel enabled) on any error: the server re-checks the toggle
// authoritatively on every submission, so a transient config-load failure
// should never hide an otherwise-working feature.
export async function fetchEnabledRequestTypes(): Promise<EnabledRequestTypes> {
    const failOpen: EnabledRequestTypes = {channel: true, channelAdmin: true, team: false, webhook: false};
    try {
        const response = await fetch(`/plugins/${manifest.id}/api/v1/config`, {
            method: 'GET',
            credentials: 'same-origin',
            headers: {'X-Requested-With': 'XMLHttpRequest'},
        });
        if (!response.ok) {
            return failOpen;
        }
        const body = await response.json();
        return {
            channel: Boolean(body?.channel),
            channelAdmin: Boolean(body?.channel_admin),
            team: Boolean(body?.team),
            webhook: Boolean(body?.webhook),
        };
    } catch {
        return failOpen;
    }
}

// getCSRFToken reads the CSRF token Mattermost sets as a cookie, required for authenticated
// state-changing requests to plugin endpoints.
function getCSRFToken(): string {
    const match = (typeof document === 'undefined' ? '' : document.cookie).match(/(?:^|;\s*)MMCSRF=([^;]+)/);
    return match ? match[1] : '';
}

export async function submitChannelRequest(payload: ChannelRequestPayload): Promise<ChannelRequestResult> {
    const response = await fetch(`/plugins/${manifest.id}/api/v1/create`, {
        method: 'POST',
        credentials: 'same-origin',
        headers: {
            'Content-Type': 'application/json',
            'X-CSRF-Token': getCSRFToken(),
            'X-Requested-With': 'XMLHttpRequest',
        },
        body: JSON.stringify(payload),
    });

    let body: ChannelRequestResult = {};
    try {
        body = await response.json();
    } catch {
        // Body may be empty or non-JSON on unexpected errors; fall through to status handling.
    }

    if (!response.ok && !body.error) {
        return {error: `Request failed (${response.status}). Please try again.`};
    }

    return body;
}

export async function submitTeamRequest(payload: TeamRequestPayload): Promise<ChannelRequestResult> {
    const response = await fetch(`/plugins/${manifest.id}/api/v1/create_team`, {
        method: 'POST',
        credentials: 'same-origin',
        headers: {
            'Content-Type': 'application/json',
            'X-CSRF-Token': getCSRFToken(),
            'X-Requested-With': 'XMLHttpRequest',
        },
        body: JSON.stringify(payload),
    });

    let body: ChannelRequestResult = {};
    try {
        body = await response.json();
    } catch {
        // Body may be empty or non-JSON on unexpected errors; fall through to status handling.
    }

    if (!response.ok && !body.error) {
        return {error: `Request failed (${response.status}). Please try again.`};
    }

    return body;
}

export async function submitAdminRequest(payload: AdminRequestPayload): Promise<ChannelRequestResult> {
    const response = await fetch(`/plugins/${manifest.id}/api/v1/request_admin`, {
        method: 'POST',
        credentials: 'same-origin',
        headers: {
            'Content-Type': 'application/json',
            'X-CSRF-Token': getCSRFToken(),
            'X-Requested-With': 'XMLHttpRequest',
        },
        body: JSON.stringify(payload),
    });

    let body: ChannelRequestResult = {};
    try {
        body = await response.json();
    } catch {
        // Body may be empty or non-JSON on unexpected errors; fall through to status handling.
    }

    if (!response.ok && !body.error) {
        return {error: `Request failed (${response.status}). Please try again.`};
    }

    return body;
}
