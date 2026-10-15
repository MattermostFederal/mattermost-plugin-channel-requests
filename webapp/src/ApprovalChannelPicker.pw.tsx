import type {Page} from '@playwright/test';
import React from 'react';

import {ChannelPicker, TeamPicker} from './ApprovalChannelPicker';

import {expect, test} from '../playwright/ct-coverage';

const API = '**/plugins/com.mattermostfederal.channel-requests/api/v1';
const PLUGIN_ID = 'com.mattermostfederal.channel-requests';

type Change = [string, string];

const teams = [
    {id: 't1', name: 'ops', display_name: 'Operations'},
    {id: 't2', name: 'eng', display_name: ''},
];

const channelsByTeam: Record<string, unknown[]> = {
    t1: [
        {id: 'c1', name: 'approvals', display_name: 'Approvals', type: 'O'},
        {id: 'c2', name: 'leads', display_name: '', type: 'P'},
    ],
    t2: [{id: 'c3', name: 'eng-approvals', display_name: 'Eng Approvals', type: 'O'}],
};

function settings(values: Record<string, string>) {
    return {PluginSettings: {Plugins: {[PLUGIN_ID]: values}}};
}

async function routeChannels(page: Page, delays: Record<string, number> = {}) {
    await page.route(`${API}/channels**`, async (route) => {
        const team = new URL(route.request().url()).searchParams.get('team_id') ?? '';
        if (delays[team]) {
            await new Promise((resolve) => setTimeout(resolve, delays[team]));
        }
        await route.fulfill({contentType: 'application/json', body: JSON.stringify(channelsByTeam[team] ?? [])});
    });
}

const options = (page: Page) => page.locator('option');

test.describe('TeamPicker', () => {
    test('lists teams by ID once loaded', async ({mount, page}) => {
        let release: () => void = () => undefined;
        const gate = new Promise<void>((resolve) => {
            release = resolve;
        });
        await page.route(`${API}/teams`, async (route) => {
            await gate;
            await route.fulfill({contentType: 'application/json', body: JSON.stringify(teams)});
        });
        await mount(<TeamPicker
            id='ApprovalTeam'
            value='t1'
            onChange={() => undefined}
                    />);
        await expect(page.locator('select')).toBeDisabled();
        await expect(options(page).first()).toHaveText('-- loading teams --');
        release();

        await expect(options(page)).toHaveText(['-- pick a team --', 'Operations  (/ops)', 'eng  (/eng)']);
        await expect(page.locator('select')).toHaveValue('t1');
        await expect(page.locator('select')).toBeEnabled();
    });

    test('writes the picked team ID', async ({mount, page}) => {
        await page.route(`${API}/teams`, (route) => route.fulfill({contentType: 'application/json', body: JSON.stringify(teams)}));
        const changes: Change[] = [];
        let saveNeeded = 0;
        await mount(
            <TeamPicker
                id='ApprovalTeam'
                value=''
                onChange={(id, v) => changes.push([id, v])}
                setSaveNeeded={() => {
                    saveNeeded++;
                }}
            />,
        );
        await page.locator('select').selectOption('t2');
        expect(changes).toEqual([['ApprovalTeam', 't2']]);
        expect(saveNeeded).toBe(1);
    });

    test('shows no teams when the list fails to load', async ({mount, page}) => {
        await page.route(`${API}/teams`, (route) => route.fulfill({status: 500, body: ''}));
        await mount(<TeamPicker
            id='ApprovalTeam'
            value=''
            onChange={() => undefined}
                    />);
        await expect(options(page)).toHaveText(['-- pick a team --']);
    });

    test('shows no teams when the request fails', async ({mount, page}) => {
        await page.route(`${API}/teams`, (route) => route.abort('connectionrefused'));
        await mount(<TeamPicker
            id='ApprovalTeam'
            value=''
            onChange={() => undefined}
                    />);
        await expect(options(page)).toHaveText(['-- pick a team --']);
    });

    test('honors disabled', async ({mount, page}) => {
        await page.route(`${API}/teams`, (route) => route.fulfill({contentType: 'application/json', body: JSON.stringify(teams)}));
        await mount(<TeamPicker
            id='ApprovalTeam'
            value=''
            disabled={true}
            onChange={() => undefined}
                    />);
        await expect(options(page)).toHaveCount(3);
        await expect(page.locator('select')).toBeDisabled();
    });
});

test.describe('ChannelPicker', () => {
    test('asks for a team first', async ({mount, page}) => {
        await mount(<ChannelPicker
            id='ApprovalChannel'
            value=''
            onChange={() => undefined}
                    />);
        await expect(options(page)).toHaveText(['-- pick a team first --']);
        await expect(page.locator('select')).toBeDisabled();
        await expect(page.getByText('Pick a team above first.')).toBeVisible();
    });

    test('lists the saved team’s channels and marks private ones', async ({mount, page}) => {
        await routeChannels(page);
        await mount(<ChannelPicker
            id='ApprovalChannel'
            value='c1'
            config={settings({ApprovalTeam: 't1'})}
            onChange={() => undefined}
                    />);
        await expect(options(page)).toHaveText(['-- pick a channel --', 'Approvals  (/approvals)', '🔒 leads  (/leads)']);
        await expect(page.locator('select')).toHaveValue('c1');
    });

    test('prefers the unsaved team and reads a lowercase key', async ({mount, page}) => {
        await routeChannels(page);
        await mount(
            <ChannelPicker
                id='ApprovalChannel'
                value=''
                config={settings({ApprovalTeam: 't1'})}
                currentState={settings({approvalteam: 't2'})}
                onChange={() => undefined}
            />,
        );
        await expect(options(page)).toHaveText(['-- pick a channel --', 'Eng Approvals  (/eng-approvals)']);
    });

    test('falls back to the saved team when the unsaved state has none', async ({mount, page}) => {
        await routeChannels(page);
        await mount(
            <ChannelPicker
                id='ApprovalChannel'
                value=''
                config={settings({ApprovalTeam: 't1'})}
                currentState={{PluginSettings: {Plugins: {}}}}
                onChange={() => undefined}
            />,
        );
        await expect(options(page)).toHaveCount(3);
    });

    test('ignores a non-string team setting', async ({mount, page}) => {
        await mount(<ChannelPicker
            id='ApprovalChannel'
            value=''
            config={{PluginSettings: {Plugins: {[PLUGIN_ID]: {ApprovalTeam: 5}}}}}
            onChange={() => undefined}
                    />);
        await expect(options(page)).toHaveText(['-- pick a team first --']);
    });

    test('shows loading, then an empty list', async ({mount, page}) => {
        await routeChannels(page, {t9: 300});
        await mount(<ChannelPicker
            id='ApprovalChannel'
            value=''
            config={settings({ApprovalTeam: 't9'})}
            onChange={() => undefined}
                    />);
        await expect(options(page).first()).toHaveText('-- loading channels --');
        await expect(options(page).first()).toHaveText('-- no channels visible --');
    });

    test('shows an empty list when channels fail to load', async ({mount, page}) => {
        await page.route(`${API}/channels**`, (route) => route.fulfill({status: 403, body: ''}));
        await mount(<ChannelPicker
            id='ApprovalChannel'
            value=''
            config={settings({ApprovalTeam: 't1'})}
            onChange={() => undefined}
                    />);
        await expect(options(page).first()).toHaveText('-- no channels visible --');
    });

    test('shows an empty list when the request fails', async ({mount, page}) => {
        await page.route(`${API}/channels**`, (route) => route.abort('connectionrefused'));
        await mount(<ChannelPicker
            id='ApprovalChannel'
            value=''
            config={settings({ApprovalTeam: 't1'})}
            onChange={() => undefined}
                    />);
        await expect(options(page).first()).toHaveText('-- no channels visible --');
    });

    test('writes the picked channel ID', async ({mount, page}) => {
        await routeChannels(page);
        const changes: Change[] = [];
        let saveNeeded = 0;
        await mount(
            <ChannelPicker
                id='ApprovalChannel'
                value=''
                config={settings({ApprovalTeam: 't1'})}
                onChange={(id, v) => changes.push([id, v])}
                setSaveNeeded={() => {
                    saveNeeded++;
                }}
            />,
        );
        await page.locator('select').selectOption('c2');
        expect(changes).toEqual([['ApprovalChannel', 'c2']]);
        expect(saveNeeded).toBe(1);
    });

    test('keeps the saved channel on first load', async ({mount, page}) => {
        await routeChannels(page);
        const changes: Change[] = [];
        await mount(<ChannelPicker
            id='ApprovalChannel'
            value='c9'
            config={settings({ApprovalTeam: 't1'})}
            onChange={(id, v) => changes.push([id, v])}
                    />);
        await expect(options(page)).toHaveCount(3);
        expect(changes).toEqual([]);
    });

    test('clears a channel from the previous team', async ({mount, page}) => {
        await routeChannels(page);
        const changes: Change[] = [];
        let saveNeeded = 0;
        const onChange = (id: string, v: string) => changes.push([id, v]);
        const setSaveNeeded = () => {
            saveNeeded++;
        };
        const component = await mount(<ChannelPicker
            id='ApprovalChannel'
            value='c1'
            currentState={settings({ApprovalTeam: 't1'})}
            onChange={onChange}
            setSaveNeeded={setSaveNeeded}
                                      />);
        await expect(options(page)).toHaveCount(3);

        await component.update(<ChannelPicker
            id='ApprovalChannel'
            value='c1'
            currentState={settings({ApprovalTeam: 't2'})}
            onChange={onChange}
            setSaveNeeded={setSaveNeeded}
                               />);
        await expect(options(page)).toHaveCount(2);
        expect(changes).toEqual([['ApprovalChannel', '']]);
        expect(saveNeeded).toBe(1);
    });

    test('keeps a channel that the new team also has', async ({mount, page}) => {
        channelsByTeam.t3 = [{id: 'c1', name: 'approvals', display_name: 'Approvals', type: 'O'}];
        await routeChannels(page);
        const changes: Change[] = [];
        const onChange = (id: string, v: string) => changes.push([id, v]);
        const component = await mount(<ChannelPicker
            id='ApprovalChannel'
            value='c1'
            currentState={settings({ApprovalTeam: 't1'})}
            onChange={onChange}
                                      />);
        await expect(options(page)).toHaveCount(3);
        await component.update(<ChannelPicker
            id='ApprovalChannel'
            value='c1'
            currentState={settings({ApprovalTeam: 't3'})}
            onChange={onChange}
                               />);
        await expect(options(page)).toHaveCount(2);
        expect(changes).toEqual([]);
        delete channelsByTeam.t3;
    });

    test('clears the channel when the team is removed', async ({mount, page}) => {
        await routeChannels(page);
        const changes: Change[] = [];
        const onChange = (id: string, v: string) => changes.push([id, v]);
        const component = await mount(<ChannelPicker
            id='ApprovalChannel'
            value='c1'
            currentState={settings({ApprovalTeam: 't1'})}
            onChange={onChange}
                                      />);
        await expect(options(page)).toHaveCount(3);
        await component.update(<ChannelPicker
            id='ApprovalChannel'
            value='c1'
            currentState={settings({ApprovalTeam: ''})}
            onChange={onChange}
                               />);
        await expect(options(page)).toHaveText(['-- pick a team first --']);
        expect(changes).toEqual([['ApprovalChannel', '']]);
    });

    test('ignores a slow response for a team that is no longer selected', async ({mount, page}) => {
        await routeChannels(page, {t1: 600});
        const component = await mount(<ChannelPicker
            id='ApprovalChannel'
            value=''
            currentState={settings({ApprovalTeam: 't1'})}
            onChange={() => undefined}
                                      />);
        await component.update(<ChannelPicker
            id='ApprovalChannel'
            value=''
            currentState={settings({ApprovalTeam: 't2'})}
            onChange={() => undefined}
                               />);
        await expect(options(page)).toHaveText(['-- pick a channel --', 'Eng Approvals  (/eng-approvals)']);
        await page.waitForTimeout(800);
        await expect(options(page)).toHaveCount(2);
    });
});
