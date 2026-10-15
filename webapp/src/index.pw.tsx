import type {Page} from '@playwright/test';
import React from 'react';

import {IndexHarness} from './IndexHarness';
import type {Recorded} from './IndexHarness';

import {expect, test} from '../playwright/ct-coverage';

async function recorded(page: Page): Promise<Recorded> {
    const pre = page.getByTestId('recorded');
    await expect(pre).not.toBeEmpty();
    return JSON.parse(await pre.textContent() ?? '{}');
}

test.describe('plugin entry point', () => {
    test('registers the plugin and its UI', async ({mount, page}) => {
        await mount(<IndexHarness/>);
        const r = await recorded(page);
        expect(r.registeredId).toBe('com.mattermostfederal.channel-requests');
        expect(r.called).toEqual([
            'registerReducer',
            'registerRootComponent',
            'registerRootComponent',
            'registerChannelHeaderButtonAction',
            'registerChannelHeaderMenuAction',
            'registerAdminConsoleCustomSetting',
            'registerAdminConsoleCustomSetting',
            'registerAdminConsoleCustomSetting',
            'registerAdminConsoleCustomSetting',
        ]);
        expect(r.rootComponents).toEqual(['RequestChannelModal', 'RequestChannelAdminModal']);
        expect(r.adminSettings).toEqual(['ApprovalTeam', 'ApprovalChannel', 'ChannelNamePrefixes', 'AutoApproveUserIDs']);
        expect(r.errors).toEqual([]);
    });

    test('the header button opens the request modal', async ({mount, page}) => {
        await mount(<IndexHarness/>);
        const r = await recorded(page);
        expect(r.headerButtonText).toBe('Request Channel');
        expect(r.headerDispatch).toEqual({type: 'com.mattermostfederal.channel-requests_open_request_modal'});
    });

    test('the channel menu item opens the admin modal only in public and private channels', async ({mount, page}) => {
        await mount(<IndexHarness/>);
        const r = await recorded(page);
        expect(r.menuText).toBe('Request Channel Admin');
        expect(r.menuDispatch).toEqual({type: 'com.mattermostfederal.channel-requests_open_admin_modal', channelId: 'chan1'});
        expect(r.shouldRender).toEqual({O: true, P: true, D: false, G: false, unknown: false});
    });

    test('a failing registration does not stop the others', async ({mount, page}) => {
        await mount(<IndexHarness throwOn='registerChannelHeaderButtonAction'/>);
        const r = await recorded(page);
        expect(r.errors).toEqual(['[channel-requests] channel-header button failed to initialize']);
        expect(r.called).toContain('registerChannelHeaderMenuAction');
        expect(r.adminSettings).toHaveLength(4);
    });

    test('skips the admin console settings on servers without them', async ({mount, page}) => {
        await mount(<IndexHarness withoutAdminConsole={true}/>);
        const r = await recorded(page);
        expect(r.adminSettings).toEqual([]);
        expect(r.called).toContain('registerChannelHeaderMenuAction');
    });
});
