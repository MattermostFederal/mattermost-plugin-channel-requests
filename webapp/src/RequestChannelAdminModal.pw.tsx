import {expect, test} from '@playwright/experimental-ct-react';
import React from 'react';

import {ChannelAdminModalHarness} from './RequestChannelAdminModal.story';

// The modal's only field is a MemberPicker, which fetches autocomplete
// candidates. Stub it to an empty list so no dropdown appears and Tab/Escape
// bubble to the dialog instead of being consumed by the picker.
const mockAutocomplete = (page: any, users: Array<{id: string; username: string}> = []) =>
    page.route('**/api/v1/user_autocomplete*', (route: any) =>
        route.fulfill({status: 200, contentType: 'application/json', body: JSON.stringify(users)}),
    );

test('renders the Request Channel Admin modal when open', async ({mount}) => {
    const c = await mount(<ChannelAdminModalHarness open={true}/>);
    await expect(c).toContainText('Request a Channel Admin');

    // The channel display name from the seeded store is shown in the intro.
    await expect(c).toContainText('Marketing');
    await expect(c).toContainText('Submit request');
});

test('renders nothing when the modal is closed', async ({mount}) => {
    const c = await mount(<ChannelAdminModalHarness open={false}/>);
    await expect(c).not.toContainText('Request a Channel Admin');
});

test('blocks submit with no nominees and shows a validation error', async ({mount, page}) => {
    await mockAutocomplete(page);
    const c = await mount(<ChannelAdminModalHarness open={true}/>);
    await c.getByRole('button', {name: 'Submit request'}).click();
    await expect(c).toContainText('Pick at least one person to make a Channel Admin.');
});

// ---------------------------------------------------------------------------
// Accessibility & keyboard
// ---------------------------------------------------------------------------

test('exposes dialog semantics (role, modal, labelled by the title)', async ({mount}) => {
    const c = await mount(<ChannelAdminModalHarness open={true}/>);
    const dialog = c.getByRole('dialog');
    await expect(dialog).toHaveAttribute('aria-modal', 'true');
    await expect(dialog).toHaveAttribute('aria-labelledby', 'cra-title');
    await expect(c.locator('#cra-title')).toHaveText('Request a Channel Admin');
});

test('autofocuses the member picker when opened', async ({mount, page}) => {
    await mockAutocomplete(page);
    const c = await mount(<ChannelAdminModalHarness open={true}/>);
    await expect(c.getByRole('combobox', {name: 'Type a name — Tab / Enter to add'})).toBeFocused();
});

test('Escape closes the modal', async ({mount, page}) => {
    await mockAutocomplete(page);
    const c = await mount(<ChannelAdminModalHarness open={true}/>);
    await expect(c).toContainText('Request a Channel Admin');
    await page.keyboard.press('Escape');
    await expect(c).not.toContainText('Request a Channel Admin');
});

test('Tab is trapped: wraps from the last control back to the first', async ({mount, page}) => {
    await mockAutocomplete(page);
    const c = await mount(<ChannelAdminModalHarness open={true}/>);
    await c.getByRole('button', {name: 'Submit request'}).focus();
    await page.keyboard.press('Tab');

    // First focusable in the dialog is the member picker input.
    await expect(c.getByRole('combobox', {name: 'Type a name — Tab / Enter to add'})).toBeFocused();
});

test('Shift+Tab is trapped: wraps from the first control to the last', async ({mount, page}) => {
    await mockAutocomplete(page);
    const c = await mount(<ChannelAdminModalHarness open={true}/>);
    await c.getByRole('combobox', {name: 'Type a name — Tab / Enter to add'}).focus();
    await page.keyboard.press('Shift+Tab');
    await expect(c.getByRole('button', {name: 'Submit request'})).toBeFocused();
});

test('validation error is announced as an alert', async ({mount, page}) => {
    await mockAutocomplete(page);
    const c = await mount(<ChannelAdminModalHarness open={true}/>);
    await c.getByRole('button', {name: 'Submit request'}).click();
    await expect(c.getByRole('alert')).toHaveText('Pick at least one person to make a Channel Admin.');
});
