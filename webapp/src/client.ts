import manifest from 'manifest';

export type ChannelRequestPayload = {
    team_id: string;
    display_name: string;
    name: string;
    // prefix is the selected domain prefix (e.g., "team-") when the
    // admin has configured a prefix list. Empty in legacy mode; server
    // ignores it there.
    prefix?: string;
    purpose: string;
    channel_type: string;
    members: string[];
};

export type ChannelRequestResult = {
    message?: string;
    error?: string;
};

export type ChannelPrefix = {
    prefix: string;
    description: string;
    suffix_regex: string;
};

// fetchPrefixes returns the admin-configured domain prefix list. Empty
// array = legacy mode (no dropdown, free-form URL entry).
export async function fetchPrefixes(): Promise<ChannelPrefix[]> {
    try {
        const response = await fetch(`/plugins/${manifest.id}/api/v1/prefixes`, {
            method: 'GET',
            credentials: 'same-origin',
            headers: {'X-Requested-With': 'XMLHttpRequest'},
        });
        if (!response.ok) {
            return [];
        }
        const body = await response.json();
        return Array.isArray(body) ? body : [];
    } catch {
        return [];
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
