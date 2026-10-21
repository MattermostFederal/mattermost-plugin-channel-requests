#!/usr/bin/env node
/**
 * Capture clipped doc screenshots for all Request Hub guides.
 * Assumes docker Mattermost at MM_URL with plugin enabled.
 *
 * Usage:
 *   node scripts/capture-all-doc-screenshots.mjs
 *   TOPICS=channel,webhook node scripts/capture-all-doc-screenshots.mjs
 */
import {chromium} from '../webapp/node_modules/playwright/index.mjs';
import path from 'node:path';
import {fileURLToPath} from 'node:url';
import {
  MM_URL,
  ensureDir,
  login,
  gotoTownSquare,
  shotElement,
  shotNode,
  shotDialog,
  shotMenusUnion,
  openMoreActions,
  openHub,
  shotApprovalCard,
  shotRhsThread,
  outPath,
} from './lib/screenshot.mjs';

const __dirname = path.dirname(fileURLToPath(import.meta.url));
const IMAGES = path.join(__dirname, '../docs/images');
const USER = process.env.MM_USER || 'user1';
const PASS = process.env.MM_PASS || 'password';
const ADMIN = process.env.MM_ADMIN || 'admin';
const ADMIN_PASS = process.env.MM_ADMIN_PASS || 'password';

const want = new Set(
  (process.env.TOPICS || 'channel,channel-admin,team,team-admin,bot,webhook,shared,admin,team-user')
    .split(',')
    .map((s) => s.trim())
    .filter(Boolean),
);

function log(...args) {
  console.log(...args);
}

async function fillIfVisible(locator, value) {
  if (await locator.isVisible({timeout: 1500}).catch(() => false)) {
    await locator.fill(value);
  }
}

async function submitOrClose(page) {
  const btn = page.getByRole('dialog').getByRole('button', {name: /Submit request/i});
  try {
    if (await btn.isVisible({timeout: 2000}).catch(() => false)) {
      await btn.click({timeout: 5000});
      await page.waitForTimeout(1200);
    }
  } catch (e) {
    log('  submit skipped:', e.message?.split('\n')[0] || e);
  }
  await page.keyboard.press('Escape');
  await page.waitForTimeout(300);
}

async function captureChannel(page) {
  log('== channel-request ==');
  const dir = 'channel-request';

  // Plus menu
  await gotoTownSquare(page);
  const plus = page.getByRole('button', {name: /Add Channel|Add channels|\+/i}).or(
    page.locator('#browseOrAddChannelMenuButton, button[aria-label*="Add" i]').first(),
  );
  // Prefer the sidebar "+" near find channels
  const addBtn = page.locator('#browseOrAddChannelMenuButton').or(
    page.getByRole('button', {name: /Browse or create channels|Add channels/i}),
  ).first();
  if (await addBtn.isVisible({timeout: 5000}).catch(() => false)) {
    await addBtn.click();
  } else {
    // Fall back: any visible "+" in LHS
    await page.locator('.SidebarChannelNavigator button, .AddChannelDropdown button').first().click();
  }
  await page.getByRole('menuitem', {name: /Request new channel|Create new channel/i}).waitFor({timeout: 8000});
  const plusMenu = page.getByRole('menuitem', {name: /Request new channel|Create new channel/i})
    .locator('xpath=ancestor::*[@role="menu"][1]');
  await shotNode(plusMenu, outPath(IMAGES, dir, 'mattermost-creation-plus-menu.png'));
  await page.keyboard.press('Escape');

  // Channel modal via more actions
  await openMoreActions(page);
  await page.getByRole('menuitem', {name: 'Request a Channel'}).click();
  await page.getByRole('heading', {name: /Request a Channel/i}).waitFor({timeout: 10000});
  await fillIfVisible(page.locator('#cr-name, input[id*="name"]').first(), 'SRE Team');
  // Prefer labeled channel name field inside dialog
  const dialog = page.getByRole('dialog');
  const nameInput = dialog.locator('input').nth(0);
  if (await dialog.locator('label', {hasText: /Channel name/i}).count()) {
    const byLabel = dialog.getByLabel(/Channel name/i);
    if (await byLabel.count()) {
      await byLabel.fill('SRE Team');
    } else {
      await nameInput.fill('SRE Team');
    }
  }
  const purpose = dialog.locator('textarea').first();
  await fillIfVisible(purpose, 'Coordination channel for the SRE on-call rotation.');
  await page.waitForTimeout(400);
  await shotDialog(page, outPath(IMAGES, dir, 'mattermost-creation-modal.png'));
  await submitOrClose(page);
}

async function captureChannelAdmin(page) {
  log('== channel-admin-request ==');
  const dir = 'channel-admin-request';
  await gotoTownSquare(page);

  // Members panel Request Admin button
  const membersBtn = page.getByRole('button', {name: /members/i}).or(
    page.locator('#member_popover, button[aria-label*="Members" i]'),
  ).first();
  if (await membersBtn.isVisible({timeout: 5000}).catch(() => false)) {
    await membersBtn.click();
    await page.waitForTimeout(800);
    const reqAdmin = page.getByRole('button', {name: /Request Admin/i}).first();
    if (await reqAdmin.isVisible({timeout: 5000}).catch(() => false)) {
      await shotElement(page, reqAdmin, outPath(IMAGES, dir, 'mattermost-promotion-members-button.png'), 24);
    } else {
      log('  (Request Admin button not visible — skipping members-button shot)');
    }
    await page.keyboard.press('Escape');
  }

  // More actions menu
  await openMoreActions(page);
  await page.getByRole('menuitem', {name: 'Request Channel Admin'}).waitFor({timeout: 8000});
  await shotMenusUnion(page, outPath(IMAGES, dir, 'mattermost-promotion-menu.png'), 'Request Channel Admin');

  // Modal
  await page.getByRole('menuitem', {name: 'Request Channel Admin'}).click();
  await page.getByRole('heading', {name: /Request Channel Admin|Channel Admin/i}).waitFor({timeout: 10000});
  const picker = page.getByRole('dialog').locator('input[placeholder*="name" i], input[placeholder*="Search" i]').first();
  if (await picker.isVisible().catch(() => false)) {
    await picker.fill('user2');
    await page.waitForTimeout(700);
    const suggestion = page.getByText('user2').first();
    if (await suggestion.isVisible().catch(() => false)) {
      await suggestion.click();
    }
  }
  await page.waitForTimeout(300);
  await shotDialog(page, outPath(IMAGES, dir, 'mattermost-promotion-modal.png'));
  await submitOrClose(page);
}

async function captureTeam(page) {
  log('== team-request ==');
  const dir = 'team-request';
  await gotoTownSquare(page);

  // Team sidebar / team menu native
  const teamBtn = page.locator('#teamHeaderDropdownButton, button.team-header, .team-header button').first()
    .or(page.getByRole('button', {name: /^Test$|team menu/i}).first());
  if (await teamBtn.isVisible({timeout: 5000}).catch(() => false)) {
    await teamBtn.click();
    await page.waitForTimeout(500);
    const reqTeam = page.getByRole('menuitem', {name: /Request a Team|Create a Team|Create a team/i}).first();
    if (await reqTeam.isVisible({timeout: 4000}).catch(() => false)) {
      const menu = reqTeam.locator('xpath=ancestor::*[@role="menu"][1]');
      await shotNode(menu, outPath(IMAGES, dir, 'mattermost-team-native-menu.png'));
    } else {
      log('  (native team menu item not found)');
    }
    await page.keyboard.press('Escape');
  }

  await openMoreActions(page);
  await page.getByRole('menuitem', {name: 'Request a Team'}).waitFor({timeout: 8000});
  await shotMenusUnion(page, outPath(IMAGES, dir, 'mattermost-team-more-actions.png'), 'Request a Team');
  await page.getByRole('menuitem', {name: 'Request a Team'}).click();
  await page.getByRole('heading', {name: /Request a (New )?Team|Request a Team/i}).waitFor({timeout: 10000});
  await fillIfVisible(page.getByRole('dialog').getByLabel(/Team name/i), 'Marketing');
  await fillIfVisible(page.getByRole('dialog').locator('textarea').first(), 'Cross-functional marketing team.');
  await page.waitForTimeout(300);
  await shotDialog(page, outPath(IMAGES, dir, 'mattermost-team-modal.png'));
  await submitOrClose(page);

  await openHub(page);
  await shotDialog(page, outPath(IMAGES, dir, 'mattermost-team-hub.png'));
  await page.keyboard.press('Escape');
}

async function captureTeamAdmin(page) {
  log('== team-admin-request ==');
  const dir = 'team-admin-request';
  await gotoTownSquare(page);

  const teamBtn = page.locator('#teamHeaderDropdownButton').first();
  if (await teamBtn.isVisible({timeout: 4000}).catch(() => false)) {
    await teamBtn.click();
    await page.waitForTimeout(400);
    const item = page.getByRole('menuitem', {name: /Request Team Admin/i}).first();
    if (await item.isVisible({timeout: 3000}).catch(() => false)) {
      const menu = item.locator('xpath=ancestor::*[@role="menu"][1]');
      await shotNode(menu, outPath(IMAGES, dir, 'mattermost-team-admin-native.png'));
    }
    await page.keyboard.press('Escape');
  }

  await openMoreActions(page);
  await page.getByRole('menuitem', {name: 'Request Team Admin'}).waitFor({timeout: 8000});
  await shotMenusUnion(page, outPath(IMAGES, dir, 'mattermost-team-admin-more-actions.png'), 'Request Team Admin');
  await page.getByRole('menuitem', {name: 'Request Team Admin'}).click();
  await page.getByRole('heading', {name: /Request Team Admin/i}).waitFor({timeout: 10000});
  await page.waitForTimeout(300);
  await shotDialog(page, outPath(IMAGES, dir, 'mattermost-team-admin-modal.png'));
  await submitOrClose(page);
}

async function captureBot(page) {
  log('== bot-request ==');
  const dir = 'bot-request';
  await gotoTownSquare(page);

  // Integrations page (injected request cards for non-admins)
  await page.goto(`${MM_URL}/test/integrations`, {waitUntil: 'networkidle'});
  await page.waitForTimeout(3500);
  const botCard = page.locator('[data-testid="request-hub-integrations-bot"]');
  if (await botCard.isVisible({timeout: 5000}).catch(() => false)) {
    await shotElement(page, page.locator('[data-request-hub-integrations="row"]'), outPath(IMAGES, dir, 'mattermost-bot-integrations.png'), 16);
  } else {
    // Native tile or empty — crop backstage content
    const host = page.locator('.backstage-content, .BackstageContent, main').first();
    await shotElement(page, host, outPath(IMAGES, dir, 'mattermost-bot-integrations.png'), 8);
  }

  await gotoTownSquare(page);
  await openMoreActions(page);
  await page.getByRole('menuitem', {name: 'Request a Bot'}).waitFor({timeout: 8000});
  await shotMenusUnion(page, outPath(IMAGES, dir, 'mattermost-bot-more-actions.png'), 'Request a Bot');
  await page.getByRole('menuitem', {name: 'Request a Bot'}).click();
  await page.getByRole('heading', {name: /Request a Bot/i}).waitFor({timeout: 10000});
  const d = page.getByRole('dialog');
  await fillIfVisible(d.locator('#bot-username'), 'deploy-notifier');
  await fillIfVisible(d.getByLabel(/Bot username/i), 'deploy-notifier');
  await fillIfVisible(d.locator('#bot-display-name'), 'Deploy Notifier');
  await fillIfVisible(d.getByLabel(/Display name/i), 'Deploy Notifier');
  // Check access token if present
  const tokenCb = d.getByText(/Request an access token/i).first();
  if (await tokenCb.isVisible().catch(() => false)) {
    await tokenCb.click().catch(() => {});
  }
  await page.waitForTimeout(300);
  await shotDialog(page, outPath(IMAGES, dir, 'mattermost-bot-modal.png'));
  await submitOrClose(page);

  await openHub(page);
  await shotDialog(page, outPath(IMAGES, dir, 'mattermost-bot-hub.png'));
  await page.keyboard.press('Escape');
}

async function captureWebhook(page) {
  log('== webhook-request ==');
  const dir = 'webhook-request';
  await gotoTownSquare(page);

  // Integrations — prefer Other team where native tiles are hidden
  await page.goto(`${MM_URL}/other/integrations`, {waitUntil: 'networkidle'});
  await page.waitForTimeout(4000);
  const row = page.locator('[data-request-hub-integrations="row"]');
  if (await row.isVisible({timeout: 5000}).catch(() => false)) {
    const incoming = page.locator('[data-testid="request-hub-integrations-incoming"]');
    const outgoing = page.locator('[data-testid="request-hub-integrations-outgoing"]');
    if (await incoming.count()) {
      await shotElement(page, incoming, outPath(IMAGES, dir, 'mattermost-webhook-integrations-incoming.png'), 12);
    }
    if (await outgoing.count()) {
      await shotElement(page, outgoing, outPath(IMAGES, dir, 'mattermost-webhook-integrations-outgoing.png'), 12);
    }
  }

  await gotoTownSquare(page);
  await openMoreActions(page);
  await page.getByRole('menuitem', {name: 'Request Incoming Webhook'}).waitFor({timeout: 8000});
  await shotMenusUnion(page, outPath(IMAGES, dir, 'mattermost-webhook-more-actions-incoming.png'), 'Request Incoming Webhook');
  await page.getByRole('menuitem', {name: 'Request Incoming Webhook'}).click();
  await page.getByRole('heading', {name: /Incoming Webhook/i}).waitFor({timeout: 10000});
  await fillIfVisible(page.locator('#iwh-display-name'), 'GitHub Notifications');
  await fillIfVisible(page.getByRole('dialog').getByLabel(/Display name/i), 'GitHub Notifications');
  await fillIfVisible(page.locator('#iwh-description'), 'Posts GitHub PR events into this channel.');
  await page.waitForTimeout(300);
  await shotDialog(page, outPath(IMAGES, dir, 'mattermost-webhook-incoming-modal.png'));
  await submitOrClose(page);

  await openMoreActions(page);
  await page.getByRole('menuitem', {name: 'Request Outgoing Webhook'}).waitFor({timeout: 8000});
  await shotMenusUnion(page, outPath(IMAGES, dir, 'mattermost-webhook-more-actions-outgoing.png'), 'Request Outgoing Webhook');
  await page.getByRole('menuitem', {name: 'Request Outgoing Webhook'}).click();
  await page.getByRole('heading', {name: /Outgoing Webhook/i}).waitFor({timeout: 10000});
  await fillIfVisible(page.locator('#owh-display-name'), 'Alerting Bridge');
  await fillIfVisible(page.getByRole('dialog').getByLabel(/Display name/i), 'Alerting Bridge');
  await fillIfVisible(page.locator('#owh-callback-url'), 'https://example.com/webhook');
  await fillIfVisible(page.getByRole('dialog').getByLabel(/Callback URL/i), 'https://example.com/webhook');
  await page.waitForTimeout(300);
  await shotDialog(page, outPath(IMAGES, dir, 'mattermost-webhook-outgoing-modal.png'));
  await submitOrClose(page);

  await openHub(page);
  await shotDialog(page, outPath(IMAGES, dir, 'mattermost-webhook-hub.png'));
  await page.keyboard.press('Escape');
}

async function captureShared(page) {
  log('== shared ==');
  const dir = 'shared';
  await gotoTownSquare(page);
  const plugBtn = await openHub(page);
  await page.keyboard.press('Escape');
  await page.waitForTimeout(300);
  await shotElement(page, plugBtn, outPath(IMAGES, dir, 'mattermost-requests-plug-appbar.png'), 16);
}

async function captureApprovalsAndExtras(context) {
  log('== approvals (admin) ==');
  await context.clearCookies();
  const page = await context.newPage();
  await login(page, ADMIN, ADMIN_PASS);
  await gotoTownSquare(page);

  const channels = [
    {path: 'mattermost-channel-requests', title: /Channel (creation )?request/i, file: ['channel-request', 'mattermost-creation-approval-card.png']},
    {path: 'mattermost-channel-requests', title: /Channel Admin request/i, file: ['channel-admin-request', 'mattermost-promotion-approval-card.png']},
    {path: 'mattermost-team-requests', title: /Team (creation )?request(?!.*Admin)/i, file: ['team-request', 'mattermost-team-approval-card.png']},
    {path: 'mattermost-team-requests', title: /Team Admin request/i, file: ['team-admin-request', 'mattermost-team-admin-approval-card.png']},
    {path: 'mattermost-bot-requests', title: /Bot (account )?request/i, file: ['bot-request', 'mattermost-bot-approval-card.png']},
    {path: 'mattermost-webhook-requests', title: /webhook request|Incoming webhook|Outgoing webhook/i, file: ['webhook-request', 'mattermost-webhook-approval-card.png']},
  ];

  for (const ch of channels) {
    try {
      await page.goto(`${MM_URL}/test/channels/${ch.path}`, {waitUntil: 'domcontentloaded'});
      await page.waitForTimeout(1800);
      await page.locator('a.close').first().click().catch(() => {});
      await shotApprovalCard(page, ch.title, outPath(IMAGES, ch.file[0], ch.file[1]));
      log('  saved', ch.file.join('/'));
    } catch (e) {
      log('  skip', ch.file.join('/'), '—', e.message);
    }
  }

  // Thread RHS: open latest request thread, reply, crop RHS
  try {
    await page.goto(`${MM_URL}/test/channels/mattermost-channel-requests`, {waitUntil: 'domcontentloaded'});
    await page.waitForTimeout(1500);
    await page.locator('a.close').first().click().catch(() => {});
    const replyBtn = page.getByRole('button', {name: /reply/i}).first();
    if (await replyBtn.isVisible({timeout: 5000}).catch(() => false)) {
      await replyBtn.click();
    } else {
      // Click the post to open thread
      await page.locator('h1.attachment__title').first().click();
    }
    await page.waitForTimeout(800);
    const replyBox = page.getByPlaceholder(/Reply to thread|Write to/i).first();
    if (await replyBox.isVisible({timeout: 5000}).catch(() => false)) {
      await replyBox.fill('Why do you need this? Who will use it?');
      await page.keyboard.press('Enter');
      await page.waitForTimeout(1000);
    }
    await shotRhsThread(page, outPath(IMAGES, 'shared', 'mattermost-thread-approver-ask.png'));
  } catch (e) {
    log('  thread approver shot skipped:', e.message);
  }

  // Welcome message — find a channel with bot welcome if any
  try {
    await page.goto(`${MM_URL}/test/channels/town-square`, {waitUntil: 'domcontentloaded'});
    await page.waitForTimeout(1000);
    const welcome = page.getByText(/welcome|created this channel|Request Hub/i).first();
    if (await welcome.isVisible({timeout: 3000}).catch(() => false)) {
      const post = welcome.locator('xpath=ancestor::*[contains(@class,"post")][1]');
      if (await post.count()) {
        await shotNode(post.first(), outPath(IMAGES, 'channel-request', 'mattermost-creation-welcome.png'));
      }
    }
  } catch (e) {
    log('  welcome shot skipped:', e.message);
  }

  // Admin console plugin settings
  try {
    await page.goto(`${MM_URL}/admin_console/plugins/com.mattermostfederal.mattermost-plugin-request-hub`, {
      waitUntil: 'domcontentloaded',
    });
    await page.waitForTimeout(2500);
    // Fallback search path
    if (!(await page.getByText(/Mattermost Request Hub|Request Hub/i).first().isVisible({timeout: 4000}).catch(() => false))) {
      await page.goto(`${MM_URL}/admin_console`, {waitUntil: 'domcontentloaded'});
      await page.waitForTimeout(1500);
      const search = page.getByPlaceholder(/Search|Find settings/i).first();
      if (await search.isVisible().catch(() => false)) {
        await search.fill('Request Hub');
        await page.waitForTimeout(800);
        await page.getByText(/Request Hub/i).first().click().catch(() => {});
        await page.waitForTimeout(1500);
      }
    }
    const sidebar = page.locator('.admin-sidebar, #adminSidebar, [class*="AdminSidebar"]').first();
    if (await sidebar.isVisible({timeout: 3000}).catch(() => false)) {
      await shotNode(sidebar, outPath(IMAGES, 'admin', 'mattermost-admin-settings.png'));
    }
    const panel = page.locator('.admin-console__wrapper, .wrapper--fixed, form').first();
    if (await panel.isVisible({timeout: 3000}).catch(() => false)) {
      // Scroll to prefixes if present
      const prefixes = page.getByText(/Domain prefix|Prefixes|prefix/i).first();
      if (await prefixes.isVisible({timeout: 2000}).catch(() => false)) {
        await prefixes.scrollIntoViewIfNeeded();
        await page.waitForTimeout(400);
        await shotElement(page, prefixes.locator('xpath=ancestor::*[contains(@class,"form-group") or contains(@class,"setting") or self::section][1]').or(prefixes), outPath(IMAGES, 'admin', 'mattermost-admin-prefixes.png'), 20);
        await shotElement(page, page.locator('.admin-console, .wrapper--fixed').first(), outPath(IMAGES, 'admin', 'mattermost-admin-prefix-preview.png'), 8);
      } else {
        await shotElement(page, panel, outPath(IMAGES, 'admin', 'mattermost-admin-prefixes.png'), 8);
      }
    }
  } catch (e) {
    log('  admin settings skipped:', e.message);
  }

  await page.close();
}

async function captureRequesterThreadDm(context) {
  log('== shared requester DM thread ==');
  await context.clearCookies();
  const page = await context.newPage();
  await login(page, USER, PASS);
  await gotoTownSquare(page);
  // Open DM with the request-hub bot if present
  try {
    const botDm = page.getByRole('link', {name: /mattermost-request-hub|request-hub/i}).first();
    if (await botDm.isVisible({timeout: 5000}).catch(() => false)) {
      await botDm.click();
      await page.waitForTimeout(1200);
      const replyBtn = page.getByRole('button', {name: /reply/i}).first();
      if (await replyBtn.isVisible({timeout: 4000}).catch(() => false)) {
        await replyBtn.click();
        await page.waitForTimeout(800);
        await shotRhsThread(page, outPath(IMAGES, 'shared', 'mattermost-thread-requester-dm.png'));
        const box = page.getByPlaceholder(/Reply to thread/i).first();
        if (await box.isVisible().catch(() => false)) {
          await box.fill('We need it for the on-call rotation.');
          await page.keyboard.press('Enter');
          await page.waitForTimeout(1000);
        }
      } else {
        // Crop a bot DM card if no thread
        const att = page.locator('.attachment').first();
        if (await att.isVisible({timeout: 3000}).catch(() => false)) {
          await shotNode(att, outPath(IMAGES, 'shared', 'mattermost-thread-requester-dm.png'));
        }
      }
    }
  } catch (e) {
    log('  requester DM skipped:', e.message);
  }

  // Webhook / bot DM delivery shots — crop latest bot DM attachment if secrets were delivered
  try {
    const botDm = page.getByRole('link', {name: /mattermost-request-hub|request-hub/i}).first();
    if (await botDm.isVisible().catch(() => false)) {
      await botDm.click();
      await page.waitForTimeout(1000);
      const tokenMsg = page.getByText(/access token|webhook URL|Incoming webhook/i).first();
      if (await tokenMsg.isVisible({timeout: 3000}).catch(() => false)) {
        const post = tokenMsg.locator('xpath=ancestor::*[contains(@class,"post")][1]');
        await shotNode(post.first(), outPath(IMAGES, 'bot-request', 'mattermost-bot-token-dm.png'));
        await shotNode(post.first(), outPath(IMAGES, 'webhook-request', 'mattermost-webhook-dm.png'));
      }
    }
  } catch (e) {
    log('  token/webhook DM skipped:', e.message);
  }

  await page.close();
}

async function captureRelayBack(context) {
  // After requester replied, crop approval thread again as relay-back
  log('== shared relay-back ==');
  await context.clearCookies();
  const page = await context.newPage();
  await login(page, ADMIN, ADMIN_PASS);
  try {
    await page.goto(`${MM_URL}/test/channels/mattermost-channel-requests`, {waitUntil: 'domcontentloaded'});
    await page.waitForTimeout(1500);
    await page.locator('a.close').first().click().catch(() => {});
    const replyBtn = page.getByRole('button', {name: /reply/i}).first();
    if (await replyBtn.isVisible({timeout: 5000}).catch(() => false)) {
      await replyBtn.click();
      await page.waitForTimeout(1000);
      await shotRhsThread(page, outPath(IMAGES, 'shared', 'mattermost-thread-relay-back.png'));
    }
  } catch (e) {
    log('  relay-back skipped:', e.message);
  }
  await page.close();
}

async function captureTeamUser(page) {
  // Reuse existing dedicated script logic lightly
  log('== team-user-request (hub/modals refresh) ==');
  const dir = 'team-user-request';
  await gotoTownSquare(page);
  await openMoreActions(page);
  await shotMenusUnion(page, outPath(IMAGES, dir, 'mattermost-team-member-more-actions.png'), 'Request Team Members');
  await shotMenusUnion(page, outPath(IMAGES, dir, 'mattermost-team-guest-more-actions.png'), 'Request Guest Invite');
  await page.getByRole('menuitem', {name: 'Request Team Members'}).click();
  await page.getByRole('heading', {name: 'Request Team Members'}).waitFor();
  await shotDialog(page, outPath(IMAGES, dir, 'mattermost-team-member-modal.png'));
  await page.keyboard.press('Escape');
  await openMoreActions(page);
  await page.getByRole('menuitem', {name: 'Request Guest Invite'}).click();
  await page.getByRole('heading', {name: 'Request Guest Invite'}).waitFor();
  await fillIfVisible(page.getByLabel('Guest email'), 'guest@example.com');
  await shotDialog(page, outPath(IMAGES, dir, 'mattermost-team-guest-modal.png'));
  await page.keyboard.press('Escape');
  await openHub(page);
  await shotDialog(page, outPath(IMAGES, dir, 'mattermost-team-member-hub.png'));
  await shotDialog(page, outPath(IMAGES, dir, 'mattermost-team-guest-hub.png'));
  await page.keyboard.press('Escape');
}

async function main() {
  ensureDir(IMAGES);
  const browser = await chromium.launch({headless: true});
  const context = await browser.newContext({
    viewport: {width: 1440, height: 900},
    deviceScaleFactor: 2,
  });
  const page = await context.newPage();

  log('Logging in as', USER);
  await login(page, USER, PASS);
  await gotoTownSquare(page);

  const sections = [
    ['channel', captureChannel],
    ['channel-admin', captureChannelAdmin],
    ['team', captureTeam],
    ['team-admin', captureTeamAdmin],
    ['bot', captureBot],
    ['webhook', captureWebhook],
    ['shared', captureShared],
    ['team-user', captureTeamUser],
  ];
  for (const [name, fn] of sections) {
    if (!want.has(name)) {
      continue;
    }
    try {
      await fn(page);
    } catch (e) {
      log(`SECTION ${name} FAILED:`, e.message?.split('\n')[0] || e);
      await page.keyboard.press('Escape').catch(() => {});
      await gotoTownSquare(page).catch(() => {});
    }
  }

  await page.close();

  // Admin-side approvals + console + threads
  if (want.has('admin') || want.has('channel') || want.has('bot') || want.has('webhook') || want.has('team') || want.has('shared')) {
    await captureApprovalsAndExtras(context);
    await captureRequesterThreadDm(context);
    await captureRelayBack(context);
  }

  await browser.close();
  log('Done. Images under', IMAGES);
}

main().catch((err) => {
  console.error(err);
  process.exit(1);
});
