import type {Page} from '@playwright/test';
import React from 'react';

import {AutoApprovePicker} from './AutoApprovePicker';

import {expect, test} from '../playwright/ct-coverage';

type Change = [string, string];

const alice = {id: 'u1', username: 'alice'};
const bob = {id: 'u2', username: 'bob'};
const carol = {id: 'u3', username: 'carol'};

async function routeIDs(page: Page, users: unknown[] = [alice, bob]) {
    const requested: string[][] = [];
    await page.route('**/api/v4/users/ids', async (route) => {
        const ids = route.request().postDataJSON() as string[];
        requested.push(ids);
        const known = (users as Array<{id: string}>).filter((u) => ids.includes(u.id));
        await route.fulfill({contentType: 'application/json', body: JSON.stringify(known)});
    });
    return requested;
}

async function routeSearch(page: Page, users: unknown[] = [carol]) {
    await page.route('**/api/v4/users/autocomplete**', (route) => route.fulfill({contentType: 'application/json', body: JSON.stringify({users})}));
}

const pill = (page: Page, username: string) => page.locator('span', {hasText: `@${username}`}).filter({has: page.getByRole('button', {name: '×'})});
const input = (page: Page) => page.locator('input');

test.describe('AutoApprovePicker', () => {
    test('shows the saved users by username', async ({mount, page}) => {
        const requested = await routeIDs(page);
        await mount(<AutoApprovePicker
            id='AutoApproveUserIDs'
            value='u1, u2;'
            onChange={() => undefined}
                    />);
        await expect(pill(page, 'alice')).toBeVisible();
        await expect(pill(page, 'bob')).toBeVisible();
        expect(requested).toEqual([['u1', 'u2']]);
    });

    test('shows the empty picker for no saved users', async ({mount, page}) => {
        const requested = await routeIDs(page);
        await mount(<AutoApprovePicker
            id='AutoApproveUserIDs'
            value=''
            onChange={() => undefined}
                    />);
        await expect(page.getByPlaceholder('Type a name to search — Tab or Enter to add')).toBeVisible();
        expect(requested).toEqual([]);
    });

    test('drops saved IDs that no longer resolve', async ({mount, page}) => {
        await routeIDs(page);
        await mount(<AutoApprovePicker
            id='AutoApproveUserIDs'
            value='u1,gone'
            onChange={() => undefined}
                    />);
        await expect(pill(page, 'alice')).toBeVisible();
        await expect(page.getByRole('button', {name: '×'})).toHaveCount(1);
    });

    test('blocks editing when the saved users cannot be loaded', async ({mount, page}) => {
        await page.route('**/api/v4/users/ids', (route) => route.fulfill({status: 500, body: ''}));
        await mount(<AutoApprovePicker
            id='AutoApproveUserIDs'
            value='u1'
            onChange={() => undefined}
                    />);
        await expect(page.getByText('Could not load the saved users. Reload the page before editing this list.')).toBeVisible();
        await expect(input(page)).toBeDisabled();
    });

    test('honors disabled', async ({mount, page}) => {
        await mount(<AutoApprovePicker
            id='AutoApproveUserIDs'
            value=''
            disabled={true}
            onChange={() => undefined}
                    />);
        await expect(input(page)).toBeDisabled();
    });

    test('adds a searched user and saves IDs', async ({mount, page}) => {
        await routeIDs(page);
        await routeSearch(page);
        const changes: Change[] = [];
        let saveNeeded = 0;
        await mount(
            <AutoApprovePicker
                id='AutoApproveUserIDs'
                value='u1'
                onChange={(id, v) => changes.push([id, v])}
                setSaveNeeded={() => {
                    saveNeeded++;
                }}
            />,
        );
        await expect(pill(page, 'alice')).toBeVisible();
        await input(page).fill('car');
        await page.getByText('@carol').click();
        expect(changes).toEqual([['AutoApproveUserIDs', 'u1,u3']]);
        expect(saveNeeded).toBe(1);
        await expect(pill(page, 'carol')).toBeVisible();
    });

    test('removes a user and saves the remaining IDs', async ({mount, page}) => {
        await routeIDs(page);
        const changes: Change[] = [];
        await mount(<AutoApprovePicker
            id='AutoApproveUserIDs'
            value='u1,u2'
            onChange={(id, v) => changes.push([id, v])}
                    />);
        await pill(page, 'alice').getByRole('button', {name: '×'}).click();
        expect(changes).toEqual([['AutoApproveUserIDs', 'u2']]);
    });

    test('leaves out a typed name that matches no user', async ({mount, page}) => {
        await routeSearch(page, []);
        const changes: Change[] = [];
        await mount(<AutoApprovePicker
            id='AutoApproveUserIDs'
            value=''
            onChange={(id, v) => changes.push([id, v])}
                    />);
        await input(page).fill('nobody');
        await input(page).press('Enter');
        expect(changes).toEqual([['AutoApproveUserIDs', '']]);
    });

    test('does not look up IDs it already knows', async ({mount, page}) => {
        const requested = await routeIDs(page);
        const component = await mount(<AutoApprovePicker
            id='AutoApproveUserIDs'
            value='u1,u2'
            onChange={() => undefined}
                                      />);
        await expect(pill(page, 'bob')).toBeVisible();
        await component.update(<AutoApprovePicker
            id='AutoApproveUserIDs'
            value='u2'
            onChange={() => undefined}
                               />);
        await expect(pill(page, 'alice')).toBeHidden();
        expect(requested).toEqual([['u1', 'u2']]);
    });

    test('ignores a lookup that finishes after the value changed', async ({mount, page}) => {
        await page.route('**/api/v4/users/ids', async (route) => {
            const ids = route.request().postDataJSON() as string[];
            if (ids.includes('u1')) {
                await new Promise((resolve) => setTimeout(resolve, 600));
                await route.fulfill({contentType: 'application/json', body: JSON.stringify([alice])});
                return;
            }
            await route.fulfill({contentType: 'application/json', body: JSON.stringify([bob])});
        });
        const component = await mount(<AutoApprovePicker
            id='AutoApproveUserIDs'
            value='u1'
            onChange={() => undefined}
                                      />);
        await component.update(<AutoApprovePicker
            id='AutoApproveUserIDs'
            value='u2'
            onChange={() => undefined}
                               />);
        await expect(pill(page, 'bob')).toBeVisible();
        await page.waitForTimeout(800);
        await expect(pill(page, 'alice')).toBeHidden();
    });
});
