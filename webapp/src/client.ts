import manifest from 'manifest';

export type ChannelRequestPayload = {
    team_id: string;
    display_name: string;
    name: string;
    purpose: string;
    channel_type: string;
    members: string[];
    channel_admins: string[];
};

export type ChannelRequestResult = {
    message?: string;
    error?: string;
};

// UserProfile is the minimal shape of a Mattermost user this plugin needs to display and submit
// a member selection. It mirrors the fields returned by the users autocomplete endpoint.
export type UserProfile = {
    id: string;
    username: string;
    nickname?: string;
    first_name?: string;
    last_name?: string;
};

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

// searchUsers queries Mattermost's built-in user autocomplete for members that exist in the
// current instance. It scopes results to the given team when one is available and returns an empty
// list on any error so the dropdown degrades gracefully rather than surfacing a hard failure.
export async function searchUsers(teamId: string, term: string): Promise<UserProfile[]> {
    const params = new URLSearchParams({name: term, limit: '25'});
    if (teamId) {
        params.set('in_team', teamId);
    }

    const response = await fetch(`/api/v4/users/autocomplete?${params.toString()}`, {
        method: 'GET',
        credentials: 'same-origin',
        headers: {'X-Requested-With': 'XMLHttpRequest'},
    });

    if (!response.ok) {
        return [];
    }

    try {
        const body = await response.json();
        return Array.isArray(body?.users) ? (body.users as UserProfile[]) : [];
    } catch {
        return [];
    }
}
