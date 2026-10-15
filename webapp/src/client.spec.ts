import {expect, test} from '@playwright/test';
import manifest from 'manifest';

import {
    autocompleteUsers,
    fetchPrefixes,
    fetchUsersByIDs,
    getCSRFToken,
    submitAdminRequest,
    submitChannelRequest,
} from './client';
import type {ChannelRequestPayload} from './client';

type Call = {url: string; init?: RequestInit};

const originalFetch = globalThis.fetch;
const originalDocument = (globalThis as {document?: unknown}).document;

let calls: Call[];

// mockFetch replaces fetch with one that records each call and answers with
// the given status and body. A body of undefined makes json() throw, like an
// empty or non-JSON response.
function mockFetch(status: number, body?: unknown) {
    globalThis.fetch = (async (url: string, init?: RequestInit) => {
        calls.push({url, init});
        return {
            ok: status >= 200 && status < 300,
            status,
            json: async () => {
                if (body === undefined) {
                    throw new SyntaxError('Unexpected end of JSON input');
                }
                return body;
            },
        };
    }) as unknown as typeof globalThis.fetch;
}

function setCookie(cookie: string) {
    (globalThis as {document?: unknown}).document = {cookie};
}

function headersOf(call: Call): Record<string, string> {
    return call.init?.headers as Record<string, string>;
}

const payload: ChannelRequestPayload = {
    team_id: 'team1',
    display_name: 'Ops',
    name: 'ops',
    prefix: 'team-',
    purpose: '',
    channel_type: 'O',
    members: ['alice'],
    admin_members: [],
};

test.beforeEach(() => {
    calls = [];
});

test.afterEach(() => {
    globalThis.fetch = originalFetch;
    if (originalDocument === undefined) {
        delete (globalThis as {document?: unknown}).document;
    } else {
        (globalThis as {document?: unknown}).document = originalDocument;
    }
});

test.describe('getCSRFToken', () => {
    test('is empty without a document', () => {
        expect(getCSRFToken()).toBe('');
    });

    test('reads the MMCSRF cookie', () => {
        setCookie('MMUSERID=u1; MMCSRF=token123; other=x');
        expect(getCSRFToken()).toBe('token123');
    });

    test('is empty when the cookie is missing', () => {
        setCookie('MMUSERID=u1');
        expect(getCSRFToken()).toBe('');
    });
});

test.describe('fetchPrefixes', () => {
    test('returns the prefix list', async () => {
        const list = [{prefix: 'team-', description: 'Team', suffix_regex: ''}];
        mockFetch(200, list);
        expect(await fetchPrefixes()).toEqual(list);
        expect(calls[0].url).toBe(`/plugins/${manifest.id}/api/v1/prefixes`);
        expect(calls[0].init?.method).toBe('GET');
    });

    test('returns an empty list for a non-array body', async () => {
        mockFetch(200, {unexpected: true});
        expect(await fetchPrefixes()).toEqual([]);
    });

    test('throws on a non-OK status', async () => {
        mockFetch(500, {});
        await expect(fetchPrefixes()).rejects.toThrow('Failed to load prefixes (500)');
    });
});

([
    ['submitChannelRequest', () => submitChannelRequest(payload), 'create'],
    ['submitAdminRequest', () => submitAdminRequest({channel_id: 'chan1', nominees: ['alice']}), 'request_admin'],
] as const).forEach(([name, submit, path]) => {
    test.describe(name, () => {
        test('posts JSON with the CSRF token', async () => {
            setCookie('MMCSRF=token123');
            mockFetch(200, {message: 'ok'});
            expect(await submit()).toEqual({message: 'ok'});
            expect(calls[0].url).toBe(`/plugins/${manifest.id}/api/v1/${path}`);
            expect(calls[0].init?.method).toBe('POST');
            expect(headersOf(calls[0])['X-CSRF-Token']).toBe('token123');
            expect(headersOf(calls[0])['Content-Type']).toBe('application/json');
        });

        test('returns the server error message', async () => {
            mockFetch(400, {error: 'Channel name is already taken'});
            expect(await submit()).toEqual({error: 'Channel name is already taken'});
        });

        test('builds an error when a failed response has no error body', async () => {
            mockFetch(502);
            expect(await submit()).toEqual({error: 'Request failed (502). Please try again.'});
        });

        test('returns an empty result for a successful response with no body', async () => {
            mockFetch(200);
            expect(await submit()).toEqual({});
        });
    });
});

test.describe('fetchUsersByIDs', () => {
    test('skips the request for no ids', async () => {
        mockFetch(200, []);
        expect(await fetchUsersByIDs([])).toEqual([]);
        expect(calls).toHaveLength(0);
    });

    test('posts the ids and returns the users', async () => {
        const users = [{id: 'u1', username: 'alice'}];
        mockFetch(200, users);
        expect(await fetchUsersByIDs(['u1'])).toEqual(users);
        expect(calls[0].url).toBe('/api/v4/users/ids');
        expect(calls[0].init?.body).toBe(JSON.stringify(['u1']));
    });

    test('returns an empty list for a non-array body', async () => {
        mockFetch(200, {});
        expect(await fetchUsersByIDs(['u1'])).toEqual([]);
    });

    test('throws on a non-OK status', async () => {
        mockFetch(403, {});
        await expect(fetchUsersByIDs(['u1'])).rejects.toThrow('Failed to load users (403)');
    });
});

test.describe('autocompleteUsers', () => {
    test('searches by name and returns the users', async () => {
        const users = [{id: 'u1', username: 'alice'}];
        mockFetch(200, {users});
        expect(await autocompleteUsers('ali')).toEqual(users);
        expect(calls[0].url).toBe('/api/v4/users/autocomplete?name=ali&limit=20');
    });

    test('returns an empty list when users is missing', async () => {
        mockFetch(200, {});
        expect(await autocompleteUsers('ali')).toEqual([]);
    });

    test('throws on a non-OK status', async () => {
        mockFetch(500, {});
        await expect(autocompleteUsers('ali')).rejects.toThrow('User search failed (500)');
    });
});
