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
