import type {Page} from '@playwright/test';
import React from 'react';

import {IndexHarness} from './IndexHarness';
import type {Recorded, RecordedMenu} from './IndexHarness';

import {expect, test} from '../playwright/ct-coverage';

const PLUGIN_ID = 'com.mattermostfederal.mattermost-plugin-request-hub';

const allEnabled = {
    channel: true,
    channel_admin: true,
    team: true,
    team_admin: true,
    team_member: true,
    team_guest: true,
    bot: true,
    webhook: true,
};

async function routeConfig(page: Page, body: unknown = allEnabled) {
    await page.route(`**/plugins/${PLUGIN_ID}/api/v1/config`, (route) => route.fulfill({contentType: 'application/json', body: JSON.stringify(body)}));
}

async function recorded(page: Page): Promise<Recorded> {
    const pre = page.getByTestId('recorded');
    await expect(pre).not.toBeEmpty();
    return JSON.parse(await pre.textContent() ?? '{}');
}

const menu = (r: Recorded, text: string): RecordedMenu | undefined => r.menus.find((m) => m.text === text);
const action = (name: string) => `${PLUGIN_ID}_${name}`;

const realChannelsOnly = {O: true, P: true, D: false, G: false, unknown: false};

test.describe('plugin entry point', () => {
    test.beforeEach(async ({page}) => {
        await routeConfig(page);
    });

    test('registers the plugin and its UI', async ({mount, page}) => {
        await mount(<IndexHarness/>);
        const r = await recorded(page);
        expect(r.registeredId).toBe(PLUGIN_ID);
        expect(r.called[0]).toBe('registerReducer');
        expect(r.rootComponents).toEqual([
            'RequestHubModal',
            'RequestChannelModal',
            'RequestChannelAdminModal',
            'RequestTeamModal',
            'RequestTeamAdminModal',
            'RequestTeamMemberModal',
            'RequestTeamGuestModal',
            'RequestBotModal',
            'RequestIncomingWebhookModal',
            'RequestOutgoingWebhookModal',
        ]);
        expect(r.called.filter((c) => c === 'registerChannelHeaderButtonAction')).toHaveLength(1);
        expect(r.menus.map((m) => m.text)).toEqual([
            'Request a Channel',
            'Request Channel Admin',
            'Request a Team',
            'Request Team Admin',
            'Request Team Members',
            'Request Guest Invite',
            'Request Incoming Webhook',
            'Request Outgoing Webhook',
            'Request a Bot',
        ]);
        expect(r.adminSettings).toEqual(['ApprovalTeam', 'ChannelNamePrefixes']);
        expect(r.errors).toEqual([]);
    });

    test('the header button opens the Request Hub', async ({mount, page}) => {
        await mount(<IndexHarness/>);
        const r = await recorded(page);
        expect(r.headerButtonText).toBe('Request Hub');
        expect(r.headerDispatch).toEqual({type: action('open_hub_modal')});
    });

    test('each menu item opens its request modal', async ({mount, page}) => {
        await mount(<IndexHarness/>);
        const r = await recorded(page);
        expect(menu(r, 'Request a Channel')?.dispatch).toEqual({type: action('open_request_modal')});
        expect(menu(r, 'Request Channel Admin')?.dispatch).toEqual({type: action('open_admin_modal'), channelId: 'chan1'});
        expect(menu(r, 'Request a Team')?.dispatch).toEqual({type: action('open_team_creation_modal')});
        expect(menu(r, 'Request Team Admin')?.dispatch).toEqual({type: action('open_team_admin_request_modal')});
        expect(menu(r, 'Request Team Members')?.dispatch).toEqual({type: action('open_team_member_request_modal')});
        expect(menu(r, 'Request Guest Invite')?.dispatch).toEqual({type: action('open_team_guest_request_modal')});
        expect(menu(r, 'Request Incoming Webhook')?.dispatch).toEqual({type: action('open_incoming_webhook_modal')});
        expect(menu(r, 'Request Outgoing Webhook')?.dispatch).toEqual({type: action('open_outgoing_webhook_modal')});
        expect(menu(r, 'Request a Bot')?.dispatch).toEqual({type: action('open_bot_request_modal')});
    });

    test('channel-scoped menu items show only in public and private channels', async ({mount, page}) => {
        await mount(<IndexHarness/>);
        const r = await recorded(page);
        for (const text of ['Request Channel Admin', 'Request Guest Invite', 'Request Incoming Webhook', 'Request Outgoing Webhook']) {
            expect(menu(r, text)?.shouldRender, text).toEqual(realChannelsOnly);
        }
        expect(menu(r, 'Request a Channel')?.shouldRender).toBeUndefined();
        expect(menu(r, 'Request a Bot')?.shouldRender).toBeUndefined();
    });

    test('Request Team Admin shows for a user who is not a Team Admin', async ({mount, page}) => {
        await mount(<IndexHarness/>);
        const r = await recorded(page);
        expect(menu(r, 'Request Team Admin')?.shouldRender).toEqual({O: true, P: true, D: true, G: true, unknown: true});
    });

    test('menu items for turned-off request types do nothing', async ({mount, page}) => {
        await page.unrouteAll();
        await routeConfig(page, {channel_admin: true});
        await mount(<IndexHarness/>);
        const r = await recorded(page);
        for (const m of r.menus) {
            if (m.text === 'Request Channel Admin') {
                expect(m.dispatch).not.toBeNull();
            } else {
                expect(m.dispatch, m.text).toBeNull();
            }
        }
        expect(r.infos).toContain('[channel-requests] Channel requests are disabled by an administrator.');
        expect(r.infos).toContain('[request-hub] Bot requests are disabled by an administrator.');
    });

    test('a failing registration does not stop the others', async ({mount, page}) => {
        await mount(<IndexHarness throwOn='registerChannelHeaderButtonAction'/>);
        const r = await recorded(page);
        expect(r.errors).toEqual(['[channel-requests] channel-header button failed to initialize']);
        expect(r.menus).toHaveLength(9);
        expect(r.adminSettings).toHaveLength(2);
    });

    test('skips the admin console settings on servers without them', async ({mount, page}) => {
        await mount(<IndexHarness withoutAdminConsole={true}/>);
        const r = await recorded(page);
        expect(r.adminSettings).toEqual([]);
        expect(r.menus).toHaveLength(9);
    });
});
