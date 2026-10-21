import type {Page} from '@playwright/test';
import React from 'react';

import {DomInjectorHarness} from './DomInjectorHarness';
import type {HarnessRecord} from './DomInjectorHarness';

import {expect, test} from '../playwright/ct-coverage';

const OPEN_REQUEST_MODAL = 'com.mattermostfederal.mattermost-plugin-request-hub_open_request_modal';

const member = {entities: {users: {currentUserId: 'me', profiles: {me: {roles: 'system_user'}}}}};
const admin = {entities: {users: {currentUserId: 'me', profiles: {me: {roles: 'system_user system_admin'}}}}};

const menuWithNativeById = `
<ul role="menu">
  <li><button id="createNewChannelMenuItem"><i class="icon"></i><span>Create new channel</span></button></li>
  <li><button><span>Browse channels</span></button></li>
  <li><button><span>Open a direct message</span></button></li>
</ul>`;

const menuWithNativeByText = `
<ul role="menu">
  <li><button><span>Create new channel</span></button></li>
  <li><button><span>Browse channels</span></button></li>
  <li><button><span>Create new category</span></button></li>
</ul>`;

const menuWithoutNative = `
<ul role="menu">
  <li><a href="/browse"><span>Browse channels</span></a></li>
  <li><a href="/dm"><span>Open a direct message</span></a></li>
</ul>`;

const menuWithComposedLabels = `
<div role="menu">
  <button><i class="icon-globe"></i>Browse <b>channels</b></button>
  <button>Invite people</button>
</div>`;

async function recorded(page: Page): Promise<HarnessRecord> {
    // eslint-disable-next-line no-underscore-dangle
    return page.evaluate(() => ({actions: window.__harness!.actions, escapes: window.__harness!.escapes}));
}

test.describe('channelCreationOverride', () => {
    test.describe('with the native item', () => {
        test('relabels it by id and opens the request modal on click', async ({mount, page}) => {
            await mount(<DomInjectorHarness
                injector='channelCreation'
                markup={menuWithNativeById}
                state={member}
                        />);
            const item = page.locator('#createNewChannelMenuItem');
            await expect(item).toHaveText('Request new channel');
            await item.click();
            expect(await recorded(page)).toEqual({actions: [{type: OPEN_REQUEST_MODAL}], escapes: 1});
        });

        test('relabels it by text and opens the request modal on click', async ({mount, page}) => {
            await mount(<DomInjectorHarness
                injector='channelCreation'
                markup={menuWithNativeByText}
                state={member}
                        />);
            const item = page.getByRole('button', {name: 'Request new channel'});
            await expect(item).toBeVisible();
            await item.click();
            expect((await recorded(page)).actions).toEqual([{type: OPEN_REQUEST_MODAL}]);
        });

        test('leaves the menu alone for a System Admin', async ({mount, page}) => {
            await mount(<DomInjectorHarness
                injector='channelCreation'
                markup={menuWithNativeById}
                state={admin}
                        />);
            await expect(page.getByTestId('harness-ready')).toBeAttached();
            await page.waitForTimeout(100);
            await expect(page.locator('#createNewChannelMenuItem')).toHaveText('Create new channel');
            await page.locator('#createNewChannelMenuItem').click();
            expect((await recorded(page)).actions).toEqual([]);
        });
    });

    test.describe('without the native item', () => {
        test('injects one request item that opens the modal', async ({mount, page}) => {
            await mount(<DomInjectorHarness
                injector='channelCreation'
                markup={menuWithoutNative}
                state={member}
                        />);
            const injected = page.getByTestId('channelRequestsInjectedItem');
            await expect(injected).toHaveText('Request new channel');
            await expect(page.locator('ul li').first()).toHaveAttribute('data-mm-plugin-channel-requests-injected', 'true');

            // Another DOM change rescans the menu without injecting again.
            // eslint-disable-next-line no-underscore-dangle
            await page.evaluate(() => window.__harness!.addMarkup('<p>unrelated</p>'));
            await page.waitForTimeout(100);
            await expect(injected).toHaveCount(1);

            await injected.click();
            expect(await recorded(page)).toEqual({actions: [{type: OPEN_REQUEST_MODAL}], escapes: 1});
        });

        test('points an injected link nowhere', async ({mount, page}) => {
            const markup = '<nav role="menu"><a href="/browse">Browse channels</a><a href="/dm">Open a direct message</a></nav>';
            await mount(<DomInjectorHarness
                injector='channelCreation'
                markup={markup}
                state={member}
                        />);
            await expect(page.getByTestId('channelRequestsInjectedItem')).toHaveAttribute('href', '#');
        });

        test('keeps the icon when the label is split across elements', async ({mount, page}) => {
            await mount(<DomInjectorHarness
                injector='channelCreation'
                markup={menuWithComposedLabels}
                state={member}
                        />);
            const injected = page.getByTestId('channelRequestsInjectedItem');
            await expect(injected).toHaveText('Request new channel');
            await expect(injected.locator('i.icon-globe')).toBeAttached();
        });

        test('handles a menu that opens after install', async ({mount, page}) => {
            await mount(<DomInjectorHarness
                injector='channelCreation'
                markup=''
                state={member}
                        />);
            await expect(page.getByTestId('harness-ready')).toBeAttached();
            // eslint-disable-next-line no-underscore-dangle
            await page.evaluate((html) => window.__harness!.addMarkup(html), menuWithoutNative);
            await expect(page.getByTestId('channelRequestsInjectedItem')).toBeVisible();
        });

        test('ignores a page with no channel menu', async ({mount, page}) => {
            await mount(<DomInjectorHarness
                injector='channelCreation'
                markup='<ul><li>Browse channels</li></ul>'
                state={member}
                        />);
            await page.waitForTimeout(100);
            await expect(page.getByTestId('channelRequestsInjectedItem')).toHaveCount(0);
        });
    });

    test.describe('click routing', () => {
        test('ignores a matching label outside the channel menu', async ({mount, page}) => {
            await mount(<DomInjectorHarness
                injector='channelCreation'
                markup='<button>Request new channel</button>'
                state={member}
                        />);
            await page.getByRole('button', {name: 'Request new channel'}).click();
            expect((await recorded(page)).actions).toEqual([]);
        });

        test('ignores other clicks', async ({mount, page}) => {
            await mount(<DomInjectorHarness
                injector='channelCreation'
                markup={menuWithNativeById}
                state={member}
                        />);
            await page.getByRole('button', {name: 'Browse channels'}).click();
            expect((await recorded(page)).actions).toEqual([]);
        });
    });
});
