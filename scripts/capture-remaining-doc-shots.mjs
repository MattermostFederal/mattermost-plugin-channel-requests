#!/usr/bin/env node
/**
 * Finish remaining / failed clipped doc screenshots after the main run.
 */
import {chromium} from '../webapp/node_modules/playwright/index.mjs';
import path from 'node:path';
import {fileURLToPath} from 'node:url';
import {
  MM_URL,
  login,
  gotoTownSquare,
  shotElement,
  shotNode,
  shotDialog,
  shotMenusUnion,
  openMoreActions,
  shotApprovalCard,
  shotRhsThread,
  outPath,
} from './lib/screenshot.mjs';

const __dirname = path.dirname(fileURLToPath(import.meta.url));
const IMAGES = path.join(__dirname, '../docs/images');

async function main() {
  const browser = await chromium.launch({headless: true});
  const context = await browser.newContext({
    viewport: {width: 1440, height: 900},
    deviceScaleFactor: 2,
  });

  // ---- user1: channel modal with prefixes, channel-admin, members button ----
  const page = await context.newPage();
  await login(page, 'user1', 'password');
  await gotoTownSquare(page);

  console.log('channel modal (with prefixes)…');
  await openMoreActions(page);
  await page.getByRole('menuitem', {name: 'Request a Channel'}).click();
  await page.getByRole('heading', {name: /Request a Channel/i}).waitFor({timeout: 10000});
  // Wait for prefix select to appear (not the "not configured" banner)
  const prefixSelect = page.getByRole('dialog').locator('select, [role="combobox"]').first();
  await prefixSelect.waitFor({state: 'visible', timeout: 10000}).catch(() => {});
  await page.waitForTimeout(500);
  const dialog = page.getByRole('dialog');
  // Fill channel name — find label
  const labels = dialog.locator('label');
  const n = await labels.count();
  for (let i = 0; i < n; i++) {
    const t = await labels.nth(i).innerText();
    if (/Channel name/i.test(t)) {
      const input = labels.nth(i).locator('xpath=following::input[1]');
      await input.fill('SRE Team');
      break;
    }
  }
  const purpose = dialog.locator('textarea').first();
  if (await purpose.isVisible().catch(() => false)) {
    await purpose.fill('Coordination channel for the SRE on-call rotation.');
  }
  await page.waitForTimeout(400);
  await shotDialog(page, outPath(IMAGES, 'channel-request', 'mattermost-creation-modal.png'));
  // Submit
  await dialog.getByRole('button', {name: /Submit request/i}).click().catch(() => {});
  await page.waitForTimeout(1500);
  await page.keyboard.press('Escape');

  console.log('channel-admin modal + menu…');
  await openMoreActions(page);
  await shotMenusUnion(page, outPath(IMAGES, 'channel-admin-request', 'mattermost-promotion-menu.png'), 'Request Channel Admin');
  await page.getByRole('menuitem', {name: 'Request Channel Admin'}).click();
  await page.getByRole('heading', {name: /Channel Admin/i}).waitFor({timeout: 10000});
  const picker = dialog.locator('input').first();
  if (await picker.isVisible().catch(() => false)) {
    await picker.fill('user2');
    await page.waitForTimeout(800);
    await page.getByText('user2').first().click().catch(() => {});
  }
  await page.waitForTimeout(300);
  await shotDialog(page, outPath(IMAGES, 'channel-admin-request', 'mattermost-promotion-modal.png'));
  await dialog.getByRole('button', {name: /Submit request/i}).click().catch(() => {});
  await page.waitForTimeout(1500);
  await page.keyboard.press('Escape');

  // Members panel button
  console.log('members Request Admin button…');
  const memberCount = page.locator('#member_popover, button[id*="member" i], button[aria-label*="Members" i]').first();
  // Channel header member count often looks like "2"
  const headerMembers = page.locator('.channel-header button').filter({hasText: /\d+/}).first();
  for (const btn of [memberCount, headerMembers, page.getByRole('button', {name: /members/i}).first()]) {
    if (await btn.isVisible({timeout: 1500}).catch(() => false)) {
      await btn.click();
      await page.waitForTimeout(1000);
      break;
    }
  }
  const reqAdmin = page.getByRole('button', {name: /Request Admin/i}).first();
  if (await reqAdmin.isVisible({timeout: 4000}).catch(() => false)) {
    // Crop button + nearby Add button row
    const row = reqAdmin.locator('xpath=ancestor::*[contains(@class,"Header") or contains(@class,"header") or contains(@class,"Members")][1]').or(reqAdmin);
    await shotElement(page, await row.count() ? row.first() : reqAdmin, outPath(IMAGES, 'channel-admin-request', 'mattermost-promotion-members-button.png'), 20);
  } else {
    console.log('  Request Admin still not visible');
  }
  await page.keyboard.press('Escape');
  await page.close();

  // ---- admin approvals ----
  console.log('admin approvals…');
  await context.clearCookies();
  const admin = await context.newPage();
  await login(admin, 'admin', 'password');

  const jobs = [
    ['mattermost-channel-requests', /Channel creation request/i, 'channel-request', 'mattermost-creation-approval-card.png'],
    ['mattermost-channel-requests', /Channel Admin request/i, 'channel-admin-request', 'mattermost-promotion-approval-card.png'],
    ['mattermost-team-requests', /Team creation request/i, 'team-request', 'mattermost-team-approval-card.png'],
    ['mattermost-team-requests', /Team Admin request/i, 'team-admin-request', 'mattermost-team-admin-approval-card.png'],
    ['mattermost-bot-requests', /Bot/i, 'bot-request', 'mattermost-bot-approval-card.png'],
    ['mattermost-webhook-requests', /webhook/i, 'webhook-request', 'mattermost-webhook-approval-card.png'],
  ];
  for (const [ch, title, folder, file] of jobs) {
    try {
      await admin.goto(`${MM_URL}/test/channels/${ch}`, {waitUntil: 'domcontentloaded'});
      await admin.waitForTimeout(1500);
      await admin.locator('a.close').first().click().catch(() => {});
      await shotApprovalCard(admin, title, outPath(IMAGES, folder, file));
      console.log('  saved', folder + '/' + file);
    } catch (e) {
      console.log('  skip', file, '—', e.message.split('\n')[0]);
    }
  }

  // Thread RHS
  try {
    await admin.goto(`${MM_URL}/test/channels/mattermost-team-requests`, {waitUntil: 'domcontentloaded'});
    await admin.waitForTimeout(1200);
    await admin.locator('a.close').first().click().catch(() => {});
    const title = admin.locator('h1.attachment__title').first();
    await title.click({timeout: 5000});
    await admin.waitForTimeout(800);
    // Open reply if needed
    const reply = admin.getByRole('button', {name: /^Reply$/i}).first();
    if (await reply.isVisible({timeout: 2000}).catch(() => false)) {
      await reply.click();
    }
    await admin.waitForTimeout(500);
    const box = admin.getByPlaceholder(/Reply to thread/i).first();
    if (await box.isVisible({timeout: 3000}).catch(() => false)) {
      await box.fill('Why do you need this? Who will use it?');
      await admin.keyboard.press('Enter');
      await admin.waitForTimeout(800);
    }
    await shotRhsThread(admin, outPath(IMAGES, 'shared', 'mattermost-thread-approver-ask.png'));
    console.log('  saved thread approver');
  } catch (e) {
    console.log('  thread skipped:', e.message.split('\n')[0]);
  }

  // Admin console
  try {
    await admin.goto(`${MM_URL}/admin_console/plugins/plugin_com.mattermostfederal.mattermost-plugin-request-hub`, {
      waitUntil: 'domcontentloaded',
      timeout: 15000,
    });
    await admin.waitForTimeout(2500);
    if (!(await admin.getByText(/Request Hub|ChannelNamePrefixes|Allow Channel/i).first().isVisible({timeout: 4000}).catch(() => false))) {
      await admin.goto(`${MM_URL}/admin_console`, {waitUntil: 'domcontentloaded'});
      await admin.waitForTimeout(1500);
      const search = admin.locator('input[placeholder*="Search" i], #adminConsoleSearch, input[type="search"]').first();
      if (await search.isVisible({timeout: 3000}).catch(() => false)) {
        await search.fill('Request Hub');
        await admin.waitForTimeout(1000);
        await admin.getByText(/Mattermost Request Hub|Request Hub/i).first().click({timeout: 5000});
        await admin.waitForTimeout(2000);
      }
    }
    const sidebar = admin.locator('.admin-sidebar, #adminSidebar').first();
    if (await sidebar.isVisible({timeout: 3000}).catch(() => false)) {
      await shotNode(sidebar, outPath(IMAGES, 'admin', 'mattermost-admin-settings.png'));
    }
    // Prefixes section — look for Channel name prefixes / Domain
    const prefixHeading = admin.getByText(/Channel name prefixes|ChannelNamePrefixes|Domain prefix/i).first();
    if (await prefixHeading.isVisible({timeout: 4000}).catch(() => false)) {
      await prefixHeading.scrollIntoViewIfNeeded();
      await admin.waitForTimeout(400);
      const section = prefixHeading.locator('xpath=ancestor::*[contains(@class,"form-group") or contains(@class,"config") or self::section][1]');
      await shotElement(admin, (await section.count()) ? section.first() : prefixHeading, outPath(IMAGES, 'admin', 'mattermost-admin-prefixes.png'), 24);
      await shotElement(admin, admin.locator('.admin-console__wrapper, .wrapper--fixed, #adminConsoleWrapper').first(), outPath(IMAGES, 'admin', 'mattermost-admin-prefix-preview.png'), 8);
    } else {
      const panel = admin.locator('.admin-console__wrapper, form').first();
      if (await panel.isVisible().catch(() => false)) {
        await shotElement(admin, panel, outPath(IMAGES, 'admin', 'mattermost-admin-prefixes.png'), 8);
        await shotElement(admin, panel, outPath(IMAGES, 'admin', 'mattermost-admin-prefix-preview.png'), 8);
      }
    }
    console.log('  admin console saved');
  } catch (e) {
    console.log('  admin console skipped:', e.message.split('\n')[0]);
  }
  await admin.close();

  // Requester DM / relay
  await context.clearCookies();
  const user = await context.newPage();
  await login(user, 'user1', 'password');
  await gotoTownSquare(user);
  try {
    const botDm = user.getByRole('link', {name: /mattermost-request-hub/i}).first();
    if (await botDm.isVisible({timeout: 5000}).catch(() => false)) {
      await botDm.click();
      await user.waitForTimeout(1200);
      const replyBtn = user.getByRole('button', {name: /^Reply$/i}).first();
      if (await replyBtn.isVisible({timeout: 3000}).catch(() => false)) {
        await replyBtn.click();
        await user.waitForTimeout(600);
        await shotRhsThread(user, outPath(IMAGES, 'shared', 'mattermost-thread-requester-dm.png'));
        const box = user.getByPlaceholder(/Reply to thread/i).first();
        if (await box.isVisible().catch(() => false)) {
          await box.fill('We need it for the on-call rotation.');
          await user.keyboard.press('Enter');
          await user.waitForTimeout(800);
        }
      }
    }
  } catch (e) {
    console.log('  requester dm skipped:', e.message.split('\n')[0]);
  }
  await user.close();

  await context.clearCookies();
  const admin2 = await context.newPage();
  await login(admin2, 'admin', 'password');
  try {
    await admin2.goto(`${MM_URL}/test/channels/mattermost-team-requests`, {waitUntil: 'domcontentloaded'});
    await admin2.waitForTimeout(1200);
    await admin2.locator('h1.attachment__title').first().click({timeout: 5000});
    await admin2.waitForTimeout(800);
    await shotRhsThread(admin2, outPath(IMAGES, 'shared', 'mattermost-thread-relay-back.png'));
    console.log('  relay-back saved');
  } catch (e) {
    console.log('  relay-back skipped:', e.message.split('\n')[0]);
  }

  await browser.close();
  console.log('Done');
}

main().catch((e) => {
  console.error(e);
  process.exit(1);
});
