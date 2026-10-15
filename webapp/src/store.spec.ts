import {expect, test} from '@playwright/test';
import manifest from 'manifest';

import reducer, {
    CLOSE_ADMIN_MODAL,
    CLOSE_REQUEST_MODAL,
    OPEN_ADMIN_MODAL,
    OPEN_REQUEST_MODAL,
    closeAdminRequestModal,
    closeRequestModal,
    getAdminModalChannelId,
    getChannelDisplayName,
    getChannelType,
    getCurrentChannelId,
    getCurrentTeamId,
    isCurrentUserChannelAdmin,
    isRequestModalOpen,
    openAdminRequestModal,
    openRequestModal,
} from './store';
import type {GlobalState} from './store';

const pluginKey = `plugins-${manifest.id}`;

function stateWith(overrides: Partial<GlobalState['entities']> = {}, plugin?: unknown): GlobalState {
    return {
        entities: {
            teams: {currentTeamId: 'team1'},
            ...overrides,
        },
        ...(plugin === undefined ? {} : {[pluginKey]: plugin}),
    };
}

test.describe('action creators', () => {
    test('action types are namespaced by the plugin id', () => {
        for (const type of [OPEN_REQUEST_MODAL, CLOSE_REQUEST_MODAL, OPEN_ADMIN_MODAL, CLOSE_ADMIN_MODAL]) {
            expect(type.startsWith(`${manifest.id}_`)).toBe(true);
        }
    });

    test('build the expected actions', () => {
        expect(openRequestModal()).toEqual({type: OPEN_REQUEST_MODAL});
        expect(closeRequestModal()).toEqual({type: CLOSE_REQUEST_MODAL});
        expect(openAdminRequestModal('chan1')).toEqual({type: OPEN_ADMIN_MODAL, channelId: 'chan1'});
        expect(closeAdminRequestModal()).toEqual({type: CLOSE_ADMIN_MODAL});
    });
});

test.describe('reducer', () => {
    test('starts with both modals closed', () => {
        expect(reducer(undefined, {type: '@@INIT'})).toEqual({modalOpen: false, adminModalChannelId: null});
    });

    test('opens and closes the request modal', () => {
        const opened = reducer(undefined, openRequestModal());
        expect(opened.modalOpen).toBe(true);
        expect(reducer(opened, closeRequestModal()).modalOpen).toBe(false);
    });

    test('opens the admin modal for a channel and closes it', () => {
        const opened = reducer(undefined, openAdminRequestModal('chan1'));
        expect(opened.adminModalChannelId).toBe('chan1');
        expect(reducer(opened, closeAdminRequestModal()).adminModalChannelId).toBeNull();
    });

    test('keeps the other modal state when one changes', () => {
        const both = reducer(reducer(undefined, openRequestModal()), openAdminRequestModal('chan1'));
        expect(both).toEqual({modalOpen: true, adminModalChannelId: 'chan1'});
    });

    test('returns the same state for unknown actions', () => {
        const state = {modalOpen: true, adminModalChannelId: 'chan1'};
        expect(reducer(state, {type: 'something_else'})).toBe(state);
    });
});

test.describe('selectors', () => {
    test('modal selectors default when plugin state is missing', () => {
        const state = stateWith();
        expect(isRequestModalOpen(state)).toBe(false);
        expect(getAdminModalChannelId(state)).toBeNull();
    });

    test('modal selectors read plugin state', () => {
        const state = stateWith({}, {modalOpen: true, adminModalChannelId: 'chan1'});
        expect(isRequestModalOpen(state)).toBe(true);
        expect(getAdminModalChannelId(state)).toBe('chan1');
    });

    test('team and channel selectors read entities', () => {
        const state = stateWith({
            channels: {
                currentChannelId: 'chan1',
                channels: {chan1: {display_name: 'Town Square', type: 'O'}},
            },
        });
        expect(getCurrentTeamId(state)).toBe('team1');
        expect(getCurrentChannelId(state)).toBe('chan1');
        expect(getChannelDisplayName(state, 'chan1')).toBe('Town Square');
        expect(getChannelType(state, 'chan1')).toBe('O');
    });

    test('team and channel selectors default to empty strings', () => {
        const state = {entities: {}} as unknown as GlobalState;
        expect(getCurrentTeamId(state)).toBe('');
        expect(getCurrentChannelId(state)).toBe('');
        expect(getChannelDisplayName(state, 'chan1')).toBe('');
        expect(getChannelType(state, 'chan1')).toBe('');
    });
});

test.describe('isCurrentUserChannelAdmin', () => {
    const users = (roles: string) => ({currentUserId: 'me', profiles: {me: {roles}}});

    test('is true for a System Admin even without channel membership', () => {
        expect(isCurrentUserChannelAdmin(stateWith({users: users('system_user system_admin')}), 'chan1')).toBe(true);
    });

    test('is true for a scheme admin member', () => {
        const state = stateWith({
            users: users('system_user'),
            channels: {myMembers: {chan1: {roles: 'channel_user', scheme_admin: true}}},
        });
        expect(isCurrentUserChannelAdmin(state, 'chan1')).toBe(true);
    });

    test('is true when the member roles include channel_admin', () => {
        const state = stateWith({
            users: users('system_user'),
            channels: {myMembers: {chan1: {roles: 'channel_user channel_admin'}}},
        });
        expect(isCurrentUserChannelAdmin(state, 'chan1')).toBe(true);
    });

    test('is false for a regular member', () => {
        const state = stateWith({
            users: users('system_user'),
            channels: {myMembers: {chan1: {roles: 'channel_user'}}},
        });
        expect(isCurrentUserChannelAdmin(state, 'chan1')).toBe(false);
    });

    test('is false for a member with no roles string', () => {
        const state = stateWith({channels: {myMembers: {chan1: {}}}});
        expect(isCurrentUserChannelAdmin(state, 'chan1')).toBe(false);
    });

    test('is false when not a member or no channel is given', () => {
        const state = stateWith({users: users('system_user'), channels: {myMembers: {}}});
        expect(isCurrentUserChannelAdmin(state, 'chan1')).toBe(false);
        expect(isCurrentUserChannelAdmin(state, '')).toBe(false);
    });

    test('is false when there is no current user', () => {
        expect(isCurrentUserChannelAdmin(stateWith({users: {profiles: {}}}), 'chan1')).toBe(false);
    });
});
