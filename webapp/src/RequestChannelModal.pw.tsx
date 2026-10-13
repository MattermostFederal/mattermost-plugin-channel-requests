import {expect, test} from '@playwright/experimental-ct-react';
import React from 'react';

import {ChannelModalHarness} from './RequestChannelModal.story';

// The modal fetches its admin-configured prefix list on open. These helpers
// stub that endpoint: a non-empty list renders the form, an empty list renders
// the "not configured" notice, and a 500 renders the load-error state.
const mockPrefixes = (page: any, prefixes: Array<{prefix: string; description?: string}>) =>
    page.route('**/api/v1/prefixes', (route: any) =>
        route.fulfill({status: 200, contentType: 'application/json', body: JSON.stringify(prefixes)}),
    );

const mockPrefixesError = (page: any) =>
    page.route('**/api/v1/prefixes', (route: any) => route.fulfill({status: 500, body: ''}));

const ONE_PREFIX = [{prefix: 'mkt-', description: 'Marketing'}];

test('renders the Request a Channel form when open and configured', async ({mount, page}) => {
    await mockPrefixes(page, ONE_PREFIX);
    const c = await mount(<ChannelModalHarness open={true}/>);
    await expect(c).toContainText('Request a Channel');
    await expect(c).toContainText('Channel name');
    await expect(c).toContainText('Submit request');
});

test('renders nothing when the modal is closed', async ({mount, page}) => {
    await mockPrefixes(page, ONE_PREFIX);
    const c = await mount(<ChannelModalHarness open={false}/>);
    await expect(c).not.toContainText('Request a Channel');
});

test('shows the not-configured notice when no prefixes are defined', async ({mount, page}) => {
    await mockPrefixes(page, []);
    const c = await mount(<ChannelModalHarness open={true}/>);
    await expect(c).toContainText('Channel requests aren’t configured yet');
});

test('shows a load-error state when the prefix fetch fails', async ({mount, page}) => {
    await mockPrefixesError(page);
    const c = await mount(<ChannelModalHarness open={true}/>);
    await expect(c).toContainText('Could not load channel request settings');
    await expect(c.getByRole('button', {name: 'Retry'})).toBeVisible();
});

test('blocks submit with a blank channel name and shows a validation error', async ({mount, page}) => {
    await mockPrefixes(page, ONE_PREFIX);
    const c = await mount(<ChannelModalHarness open={true}/>);
    await c.getByRole('button', {name: 'Submit request'}).click();
    await expect(c).toContainText('A channel name is required.');
});

// ---------------------------------------------------------------------------
// Accessibility & keyboard
// ---------------------------------------------------------------------------

test('exposes dialog semantics (role, modal, labelled by the title)', async ({mount, page}) => {
    await mockPrefixes(page, ONE_PREFIX);
    const c = await mount(<ChannelModalHarness open={true}/>);
    const dialog = c.getByRole('dialog');
    await expect(dialog).toHaveAttribute('aria-modal', 'true');
    await expect(dialog).toHaveAttribute('aria-labelledby', 'cr-title');
    await expect(c.locator('#cr-title')).toHaveText('Request a Channel');
});

test('autofocuses the channel name field once the form is shown', async ({mount, page}) => {
    await mockPrefixes(page, ONE_PREFIX);
    const c = await mount(<ChannelModalHarness open={true}/>);
    await expect(c.locator('#cr-display-name')).toBeFocused();
});

test('Escape closes the modal', async ({mount, page}) => {
    await mockPrefixes(page, ONE_PREFIX);
    const c = await mount(<ChannelModalHarness open={true}/>);
    await expect(c).toContainText('Request a Channel');
    await page.keyboard.press('Escape');
    await expect(c).not.toContainText('Request a Channel');
});

test('Tab is trapped: wraps from the last control back to the first', async ({mount, page}) => {
    await mockPrefixes(page, ONE_PREFIX);
    const c = await mount(<ChannelModalHarness open={true}/>);
    await c.getByRole('button', {name: 'Submit request'}).focus();
    await page.keyboard.press('Tab');

    // First focusable in the dialog is the "Need help?" link in the intro.
    await expect(c.getByRole('link', {name: 'Need help?'})).toBeFocused();
});

test('Shift+Tab is trapped: wraps from the first control to the last', async ({mount, page}) => {
    await mockPrefixes(page, ONE_PREFIX);
    const c = await mount(<ChannelModalHarness open={true}/>);
    await c.getByRole('link', {name: 'Need help?'}).focus();
    await page.keyboard.press('Shift+Tab');
    await expect(c.getByRole('button', {name: 'Submit request'})).toBeFocused();
});

test('validation error is announced as an alert', async ({mount, page}) => {
    await mockPrefixes(page, ONE_PREFIX);
    const c = await mount(<ChannelModalHarness open={true}/>);
    await c.getByRole('button', {name: 'Submit request'}).click();
    await expect(c.getByRole('alert')).toHaveText('A channel name is required.');
});
