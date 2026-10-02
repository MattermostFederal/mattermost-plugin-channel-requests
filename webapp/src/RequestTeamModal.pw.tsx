import {expect, test} from '@playwright/experimental-ct-react';
import React from 'react';

import {TeamModalHarness} from './RequestTeamModal.story';

test('renders the Request a Team modal when open', async ({mount}) => {
    const c = await mount(<TeamModalHarness open={true}/>);
    await expect(c).toContainText('Request a Team');
    await expect(c).toContainText('Team name');
    await expect(c).toContainText('Submit request');
});

test('renders nothing when the modal is closed', async ({mount}) => {
    const c = await mount(<TeamModalHarness open={false}/>);
    await expect(c).not.toContainText('Request a Team');
});

test('blocks submit with a blank team name and shows a validation error', async ({mount}) => {
    const c = await mount(<TeamModalHarness open={true}/>);
    await c.getByRole('button', {name: 'Submit request'}).click();
    await expect(c).toContainText('A team name is required.');
});

test('Cancel closes the modal', async ({mount}) => {
    const c = await mount(<TeamModalHarness open={true}/>);
    await expect(c).toContainText('Request a Team');
    await c.getByRole('button', {name: 'Cancel'}).click();
    await expect(c).not.toContainText('Request a Team');
});

test('live URL preview reflects the typed team name', async ({mount}) => {
    const c = await mount(<TeamModalHarness open={true}/>);
    await c.locator('#tr-display-name').fill('Marketing Team');

    // slugifyName mirrors the server: "Marketing Team" -> "marketing-team".
    await expect(c).toContainText('marketing-team');
});

test('successful submit shows the success state + Close', async ({mount, page}) => {
    await page.route('**/api/v1/create_team', (route) =>
        route.fulfill({status: 200, contentType: 'application/json', body: JSON.stringify({message: 'Your team request has been submitted.'})}),
    );
    const c = await mount(<TeamModalHarness open={true}/>);
    await c.locator('#tr-display-name').fill('Marketing');
    await c.getByRole('button', {name: 'Submit request'}).click();
    await expect(c).toContainText('Your team request has been submitted.');
    await expect(c.getByRole('button', {name: 'Close'})).toBeVisible();
});

test('server validation error is surfaced in the modal', async ({mount, page}) => {
    await page.route('**/api/v1/create_team', (route) =>
        route.fulfill({status: 200, contentType: 'application/json', body: JSON.stringify({error: 'A team with the URL name "marketing" already exists. Pick a different URL name.'})}),
    );
    const c = await mount(<TeamModalHarness open={true}/>);
    await c.locator('#tr-display-name').fill('Marketing');
    await c.getByRole('button', {name: 'Submit request'}).click();
    await expect(c).toContainText('already exists');

    // On error the form stays open so the user can correct and retry.
    await expect(c.getByRole('button', {name: 'Submit request'})).toBeVisible();
});

// ---------------------------------------------------------------------------
// Accessibility & keyboard
// ---------------------------------------------------------------------------

test('exposes dialog semantics (role, modal, labelled by the title)', async ({mount}) => {
    const c = await mount(<TeamModalHarness open={true}/>);
    const dialog = c.getByRole('dialog');
    await expect(dialog).toHaveAttribute('aria-modal', 'true');

    // aria-labelledby must point at the heading so screen readers announce
    // the dialog's name on focus.
    await expect(dialog).toHaveAttribute('aria-labelledby', 'tr-title');
    await expect(c.locator('#tr-title')).toHaveText('Request a Team');
});

test('autofocuses the team name field when opened', async ({mount}) => {
    const c = await mount(<TeamModalHarness open={true}/>);
    await expect(c.locator('#tr-display-name')).toBeFocused();
});

test('Escape closes the modal', async ({mount, page}) => {
    const c = await mount(<TeamModalHarness open={true}/>);
    await expect(c).toContainText('Request a Team');
    await page.keyboard.press('Escape');
    await expect(c).not.toContainText('Request a Team');
});

test('Tab is trapped: wraps from the last control back to the first', async ({mount, page}) => {
    const c = await mount(<TeamModalHarness open={true}/>);
    await c.getByRole('button', {name: 'Submit request'}).focus();
    await page.keyboard.press('Tab');
    await expect(c.locator('#tr-display-name')).toBeFocused();
});

test('Shift+Tab is trapped: wraps from the first control to the last', async ({mount, page}) => {
    const c = await mount(<TeamModalHarness open={true}/>);
    await c.locator('#tr-display-name').focus();
    await page.keyboard.press('Shift+Tab');
    await expect(c.getByRole('button', {name: 'Submit request'})).toBeFocused();
});

test('validation error is announced as an alert', async ({mount}) => {
    const c = await mount(<TeamModalHarness open={true}/>);
    await c.getByRole('button', {name: 'Submit request'}).click();
    await expect(c.getByRole('alert')).toHaveText('A team name is required.');
});

// ---------------------------------------------------------------------------
// MemberPicker — keyboard interaction & a11y
// ---------------------------------------------------------------------------

const mockAutocomplete = (page: any, users: Array<{id: string; username: string}>) =>
    page.route('**/api/v1/user_autocomplete*', (route: any) =>
        route.fulfill({status: 200, contentType: 'application/json', body: JSON.stringify(users)}),
    );

test('member picker exposes a named combobox', async ({mount}) => {
    const c = await mount(<TeamModalHarness open={true}/>);
    const combo = c.getByRole('combobox', {name: 'Type a name — Tab / Enter to add'});
    await expect(combo).toHaveAttribute('aria-label', 'Type a name — Tab / Enter to add');
    await expect(combo).toHaveAttribute('aria-expanded', 'false');
});

test('member picker: Enter on a highlighted candidate adds a pill', async ({mount, page}) => {
    await mockAutocomplete(page, [{id: 'u1', username: 'alice'}, {id: 'u2', username: 'bob'}]);
    const c = await mount(<TeamModalHarness open={true}/>);
    const combo = c.getByRole('combobox', {name: 'Type a name — Tab / Enter to add'});
    await combo.fill('a');

    // The dropdown is a listbox; wait for the first option to appear.
    await expect(c.getByRole('option', {name: /@alice/})).toBeVisible();
    await expect(combo).toHaveAttribute('aria-expanded', 'true');

    await combo.press('Enter');
    await expect(c.getByText('@alice')).toBeVisible();

    // Committing a selection collapses the listbox again.
    await expect(combo).toHaveAttribute('aria-expanded', 'false');
});

test('member picker: ArrowDown moves the selection before Enter', async ({mount, page}) => {
    await mockAutocomplete(page, [{id: 'u1', username: 'alice'}, {id: 'u2', username: 'bob'}]);
    const c = await mount(<TeamModalHarness open={true}/>);
    const combo = c.getByRole('combobox', {name: 'Type a name — Tab / Enter to add'});
    await combo.fill('b');
    await expect(c.getByRole('option', {name: /@bob/})).toBeVisible();

    await combo.press('ArrowDown');
    await combo.press('Enter');
    await expect(c.getByText('@bob')).toBeVisible();
});

test('member picker: Enter with no dropdown accepts the literal typed name', async ({mount, page}) => {
    await mockAutocomplete(page, []); // no matches -> no dropdown
    const c = await mount(<TeamModalHarness open={true}/>);
    const combo = c.getByRole('combobox', {name: 'Type a name — Tab / Enter to add'});
    await combo.fill('charlie');
    await combo.press('Enter');
    await expect(c.getByText('@charlie')).toBeVisible();
});

test('member picker: Backspace on an empty input removes the last pill', async ({mount, page}) => {
    await mockAutocomplete(page, []);
    const c = await mount(<TeamModalHarness open={true}/>);
    const combo = c.getByRole('combobox', {name: 'Type a name — Tab / Enter to add'});
    await combo.fill('charlie');
    await combo.press('Enter');
    await expect(c.getByText('@charlie')).toBeVisible();

    // Query is now empty; Backspace deletes the trailing pill.
    await combo.press('Backspace');
    await expect(c.getByText('@charlie')).toHaveCount(0);
});

test('member picker: pill remove button has an accessible name and removes the user', async ({mount, page}) => {
    await mockAutocomplete(page, []);
    const c = await mount(<TeamModalHarness open={true}/>);
    const combo = c.getByRole('combobox', {name: 'Type a name — Tab / Enter to add'});
    await combo.fill('charlie');
    await combo.press('Enter');

    await c.getByRole('button', {name: 'Remove @charlie'}).click();
    await expect(c.getByText('@charlie')).toHaveCount(0);
});
