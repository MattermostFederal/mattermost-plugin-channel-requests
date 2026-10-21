#!/usr/bin/env node
/**
 * Capture real Mattermost UI screenshots for team member / guest docs.
 * Assumes docker Mattermost at MM_URL with plugin enabled.
 *
 * Modals / hub / menus are clipped to the popup itself (not the full
 * Mattermost chrome). Approval cards are clipped to the attachment post.
 *
 * Usage:
 *   node scripts/capture-team-user-screenshots.mjs
 */
import {chromium} from '../webapp/node_modules/playwright/index.mjs';
import path from 'node:path';
import {fileURLToPath} from 'node:url';
import fs from 'node:fs';

const __dirname = path.dirname(fileURLToPath(import.meta.url));
const OUT = path.join(__dirname, '../docs/images/team-user-request');
const SHARED = path.join(__dirname, '../docs/images/shared');
const MM_URL = process.env.MM_URL || 'http://localhost:8065';
const USER = process.env.MM_USER || 'user1';
const PASS = process.env.MM_PASS || 'password';
const ADMIN = process.env.MM_ADMIN || 'admin';
const ADMIN_PASS = process.env.MM_ADMIN_PASS || 'password';

fs.mkdirSync(OUT, {recursive: true});
fs.mkdirSync(SHARED, {recursive: true});

/** Screenshot a locator only (popup / card), with a small padding via clip. */
async function shotElement(page, locator, filePath, pad = 8) {
  await locator.waitFor({state: 'visible', timeout: 15000});
  const box = await locator.boundingBox();
  if (!box) {
    throw new Error(`no bounding box for ${filePath}`);
  }
  const vw = page.viewportSize()?.width ?? 1440;
  const vh = page.viewportSize()?.height ?? 900;
  const clip = {
    x: Math.max(0, box.x - pad),
    y: Math.max(0, box.y - pad),
    width: Math.min(vw - Math.max(0, box.x - pad), box.width + pad * 2),
    height: Math.min(vh - Math.max(0, box.y - pad), box.height + pad * 2),
  };
  await page.screenshot({path: filePath, clip, animations: 'disabled'});
}

async function shotDialog(page, filePath) {
  await shotElement(page, page.getByRole('dialog').last(), filePath, 12);
}

async function login(page, username, password) {
  const resp = await page.context().request.post(`${MM_URL}/api/v4/users/login`, {
    data: {login_id: username, password},
  });
  if (!resp.ok()) {
    throw new Error(`login failed: ${resp.status()} ${await resp.text()}`);
  }
  const token = resp.headers().token;
  const user = await resp.json();
  if (!token) {
    throw new Error('login response missing Token header');
  }
  await page.context().addCookies([
    {name: 'MMAUTHTOKEN', value: token, domain: 'localhost', path: '/'},
    {name: 'MMUSERID', value: user.id, domain: 'localhost', path: '/'},
    {name: 'MMCSRF', value: token, domain: 'localhost', path: '/'},
  ]);
  await page.addInitScript(() => {
    window.localStorage.setItem('__landingPageSeen__', 'true');
    window.localStorage.setItem('was_landing', 'true');
  });
  await page.goto(`${MM_URL}/test/channels/town-square`, {waitUntil: 'domcontentloaded'});
  const browserLink = page.getByRole('link', {name: /View in Browser/i});
  if (await browserLink.isVisible({timeout: 2000}).catch(() => false)) {
    await browserLink.click();
  }
  await page.waitForURL(/\/channels\//, {timeout: 30000});
  await page.getByRole('textbox', {name: /Write to/i}).waitFor({timeout: 30000});
  const close = page.locator('a.close').first();
  if (await close.isVisible().catch(() => false)) {
    await close.click().catch(() => {});
  }
}

async function openMoreActions(page) {
  await page.keyboard.press('Escape');
  await page.waitForTimeout(300);
  const menuBtn = page.getByRole('button', {name: /channel menu/i});
  await menuBtn.click({timeout: 15000});
  const more = page.getByRole('menuitem', {name: 'More actions'});
  try {
    await more.waitFor({state: 'visible', timeout: 5000});
  } catch (err) {
    await page.screenshot({path: '/tmp/mm-no-menu.png'});
    throw err;
  }
  await more.scrollIntoViewIfNeeded();
  await more.hover();
  await page.waitForTimeout(200);
  if (!(await page.getByRole('menuitem', {name: 'Request Team Members'}).isVisible().catch(() => false))) {
    await more.click({force: true});
  }
  await page.getByRole('menuitem', {name: 'Request Team Members'}).waitFor({state: 'visible', timeout: 10000});
}

/** Clip to the More-actions submenu (the request entries), not the full channel chrome. */
async function shotOpenMenus(page, filePath) {
  const item = page.getByRole('menuitem', {name: 'Request Team Members'});
  await item.waitFor({state: 'visible', timeout: 10000});
  const submenu = item.locator('xpath=ancestor::*[@role="menu"][1]');
  // Prefer both channel menu + submenu when the parent menu is still open.
  const parentMenu = page.getByRole('menuitem', {name: 'More actions'}).locator(
    'xpath=ancestor::*[@role="menu"][1]',
  );
  const boxes = [];
  for (const loc of [parentMenu, submenu]) {
    if (await loc.count()) {
      const box = await loc.first().boundingBox();
      if (box) {
        boxes.push(box);
      }
    }
  }
  if (!boxes.length) {
    await shotElement(page, submenu, filePath, 10);
    return;
  }
  let minX = Infinity;
  let minY = Infinity;
  let maxX = 0;
  let maxY = 0;
  for (const box of boxes) {
    minX = Math.min(minX, box.x);
    minY = Math.min(minY, box.y);
    maxX = Math.max(maxX, box.x + box.width);
    maxY = Math.max(maxY, box.y + box.height);
  }
  const pad = 10;
  const vw = page.viewportSize()?.width ?? 1440;
  const vh = page.viewportSize()?.height ?? 900;
  await page.screenshot({
    path: filePath,
    clip: {
      x: Math.max(0, minX - pad),
      y: Math.max(0, minY - pad),
      width: Math.min(vw - Math.max(0, minX - pad), maxX - minX + pad * 2),
      height: Math.min(vh - Math.max(0, minY - pad), maxY - minY + pad * 2),
    },
    animations: 'disabled',
  });
}

async function openHub(page) {
  const plugBtn = page.locator('[role="button"]').filter({has: page.locator('.fa-plug')}).first();
  if (await plugBtn.isVisible({timeout: 5000}).catch(() => false)) {
    await plugBtn.click();
    const requestsItem = page.getByText(/^Requests$/).first();
    if (await requestsItem.isVisible({timeout: 1500}).catch(() => false)) {
      await requestsItem.click();
    }
    await page.getByRole('heading', {name: /Mattermost Request Hub/i}).waitFor({timeout: 10000});
    return;
  }
  throw new Error('Could not open Request Hub from App Bar');
}

/** Prefer the interactive attachment card around a title match. */
async function approvalCardLocator(page, titleRegex) {
  const title = page.locator('h1.attachment__title').filter({hasText: titleRegex}).first();
  await title.waitFor({state: 'visible', timeout: 15000});
  await title.scrollIntoViewIfNeeded();
  const card = title.locator('xpath=ancestor::*[contains(concat(" ", normalize-space(@class), " "), " attachment ")][1]');
  if (await card.count()) {
    return card.first();
  }
  return title;
}

async function shotApprovalCard(page, titleRegex, filePath) {
  const card = await approvalCardLocator(page, titleRegex);
  await card.screenshot({path: filePath, animations: 'disabled'});
}

async function main() {
  const browser = await chromium.launch({headless: true});
  const context = await browser.newContext({
    viewport: {width: 1440, height: 900},
    deviceScaleFactor: 2,
  });
  const page = await context.newPage();

  console.log('Logging in as', USER);
  await login(page, USER, PASS);

  console.log('Capturing more-actions (team members)…');
  await openMoreActions(page);
  await shotOpenMenus(page, path.join(OUT, 'mattermost-team-member-more-actions.png'));

  console.log('Capturing more-actions (guest)…');
  await shotOpenMenus(page, path.join(OUT, 'mattermost-team-guest-more-actions.png'));

  console.log('Capturing team member modal…');
  await page.getByRole('menuitem', {name: 'Request Team Members'}).click();
  await page.getByRole('heading', {name: 'Request Team Members'}).waitFor({timeout: 10000});
  await page.waitForTimeout(400);
  await shotDialog(page, path.join(OUT, 'mattermost-team-member-modal.png'));
  await page.keyboard.press('Escape');
  await page.waitForTimeout(300);

  console.log('Capturing guest modal…');
  await openMoreActions(page);
  await page.getByRole('menuitem', {name: 'Request Guest Invite'}).click();
  await page.getByRole('heading', {name: 'Request Guest Invite'}).waitFor({timeout: 10000});
  await page.getByLabel('Guest email').fill('guest@example.com');
  await page.waitForTimeout(300);
  await shotDialog(page, path.join(OUT, 'mattermost-team-guest-modal.png'));
  await page.keyboard.press('Escape');
  await page.waitForTimeout(300);

  console.log('Opening Request Hub…');
  await openHub(page);
  await page.getByText('Request Team Members').waitFor({state: 'visible'});
  await page.waitForTimeout(400);
  console.log('Capturing hub (clipped to dialog)…');
  await shotDialog(page, path.join(OUT, 'mattermost-team-member-hub.png'));
  await shotDialog(page, path.join(OUT, 'mattermost-team-guest-hub.png'));
  // Shared app-bar shot stays contextual (icon in chrome) — keep a tight plug crop.
  const plugBtn = page.locator('[role="button"]').filter({has: page.locator('.fa-plug')}).first();
  await page.keyboard.press('Escape');
  await page.waitForTimeout(300);
  if (await plugBtn.isVisible().catch(() => false)) {
    await shotElement(page, plugBtn, path.join(SHARED, 'mattermost-requests-plug-appbar.png'), 16);
  }

  console.log('Submitting team member request…');
  await openMoreActions(page);
  await page.getByRole('menuitem', {name: 'Request Team Members'}).click();
  await page.getByRole('heading', {name: 'Request Team Members'}).waitFor();
  const picker = page.locator('input[placeholder*="Search"]').first();
  if (await picker.isVisible().catch(() => false)) {
    await picker.fill('user2');
    await page.waitForTimeout(800);
    const suggestion = page.getByText('user2').first();
    if (await suggestion.isVisible().catch(() => false)) {
      await suggestion.click();
    }
  }
  const reason = page.getByRole('dialog').locator('textarea').first();
  if (await reason.isVisible().catch(() => false)) {
    await reason.fill('Docs screenshot');
  }
  await page.getByRole('button', {name: 'Submit request'}).click();
  await page.waitForTimeout(1500);
  await page.keyboard.press('Escape');

  console.log('Submitting guest request…');
  await openMoreActions(page);
  await page.getByRole('menuitem', {name: 'Request Guest Invite'}).click();
  await page.getByLabel('Guest email').fill('guest@example.com');
  const guestReason = page.getByRole('dialog').locator('textarea').first();
  if (await guestReason.isVisible().catch(() => false)) {
    await guestReason.fill('Docs screenshot');
  }
  await page.getByRole('button', {name: 'Submit request'}).click();
  await page.waitForTimeout(1500);
  await page.keyboard.press('Escape');

  console.log('Logging in as admin for approval cards…');
  await page.context().clearCookies();
  await login(page, ADMIN, ADMIN_PASS);

  await page.goto(`${MM_URL}/test/channels/mattermost-team-requests`, {waitUntil: 'domcontentloaded'});
  await page.waitForTimeout(2000);
  await page.locator('a.close').first().click().catch(() => {});

  console.log('Capturing team member approval card…');
  try {
    await shotApprovalCard(page, /Team member request/i, path.join(OUT, 'mattermost-team-member-approval-card.png'));
  } catch (e) {
    console.warn('Team member approval card not found:', e.message);
  }

  console.log('Capturing guest approval card…');
  try {
    await shotApprovalCard(page, /guest invite request/i, path.join(OUT, 'mattermost-team-guest-approval-card.png'));
  } catch (e) {
    console.warn('Guest approval card not found:', e.message);
  }

  await browser.close();
  console.log('Done. Screenshots in', OUT);
}

main().catch((err) => {
  console.error(err);
  process.exit(1);
});
