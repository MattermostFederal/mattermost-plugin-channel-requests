#!/usr/bin/env node
/**
 * Re-capture hub + approval card shots only (clipped to dialog / card).
 */
import {chromium} from '../webapp/node_modules/playwright/index.mjs';
import path from 'node:path';
import {fileURLToPath} from 'node:url';
import fs from 'node:fs';

const __dirname = path.dirname(fileURLToPath(import.meta.url));
const OUT = path.join(__dirname, '../docs/images/team-user-request');
const SHARED = path.join(__dirname, '../docs/images/shared');
const MM_URL = process.env.MM_URL || 'http://localhost:8065';

fs.mkdirSync(OUT, {recursive: true});
fs.mkdirSync(SHARED, {recursive: true});

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
    throw new Error(`login failed: ${resp.status()}`);
  }
  const token = resp.headers().token;
  const user = await resp.json();
  await page.context().addCookies([
    {name: 'MMAUTHTOKEN', value: token, domain: 'localhost', path: '/'},
    {name: 'MMUSERID', value: user.id, domain: 'localhost', path: '/'},
    {name: 'MMCSRF', value: token, domain: 'localhost', path: '/'},
  ]);
  await page.addInitScript(() => {
    localStorage.setItem('__landingPageSeen__', 'true');
    localStorage.setItem('was_landing', 'true');
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

async function shotApprovalCard(page, titleRegex, filePath) {
  const title = page.locator('h1.attachment__title').filter({hasText: titleRegex}).first();
  await title.waitFor({state: 'visible', timeout: 15000});
  await title.scrollIntoViewIfNeeded();
  const card = title.locator('xpath=ancestor::*[contains(concat(" ", normalize-space(@class), " "), " attachment ")][1]');
  const target = (await card.count()) ? card.first() : title;
  await target.screenshot({path: filePath, animations: 'disabled'});
}

async function main() {
  const browser = await chromium.launch({headless: true});
  const context = await browser.newContext({
    viewport: {width: 1440, height: 900},
    deviceScaleFactor: 2,
  });
  const page = await context.newPage();

  console.log('user1 — hub screenshots (dialog only)');
  await login(page, 'user1', 'password');

  const plugBtn = page.locator('[role="button"]').filter({has: page.locator('.fa-plug')}).first();
  await plugBtn.click({timeout: 15000});
  const requestsItem = page.getByText(/^Requests$/).first();
  if (await requestsItem.isVisible({timeout: 1500}).catch(() => false)) {
    await requestsItem.click();
  }
  await page.getByRole('heading', {name: /Mattermost Request Hub/i}).waitFor({timeout: 10000});
  await page.getByText('Request Team Members').waitFor({state: 'visible'});
  await page.waitForTimeout(400);
  await shotDialog(page, path.join(OUT, 'mattermost-team-member-hub.png'));
  await shotDialog(page, path.join(OUT, 'mattermost-team-guest-hub.png'));
  await page.keyboard.press('Escape');
  await page.waitForTimeout(300);
  await shotElement(page, plugBtn, path.join(SHARED, 'mattermost-requests-plug-appbar.png'), 16);
  console.log('hub shots saved');

  console.log('admin — approval cards (card only)');
  await page.context().clearCookies();
  await login(page, 'admin', 'password');
  await page.goto(`${MM_URL}/test/channels/mattermost-team-requests`, {waitUntil: 'domcontentloaded'});
  await page.waitForTimeout(2000);
  await page.locator('a.close').first().click().catch(() => {});

  await shotApprovalCard(page, /Team member request/i, path.join(OUT, 'mattermost-team-member-approval-card.png'));
  await shotApprovalCard(page, /guest invite request/i, path.join(OUT, 'mattermost-team-guest-approval-card.png'));
  console.log('approval cards saved');

  await browser.close();
}

main().catch((e) => {
  console.error(e);
  process.exit(1);
});
