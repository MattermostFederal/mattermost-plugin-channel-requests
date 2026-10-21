/**
 * Shared Playwright helpers for clipped doc screenshots
 * (dialog / menu / attachment — not full Mattermost chrome).
 */
import path from 'node:path';
import fs from 'node:fs';

export const MM_URL = process.env.MM_URL || 'http://localhost:8065';

export function ensureDir(dir) {
  fs.mkdirSync(dir, {recursive: true});
}

export async function login(page, username, password) {
  const resp = await page.context().request.post(`${MM_URL}/api/v4/users/login`, {
    data: {login_id: username, password},
  });
  if (!resp.ok()) {
    throw new Error(`login failed (${username}): ${resp.status()} ${await resp.text()}`);
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
  return user;
}

export async function gotoTownSquare(page, team = 'test') {
  await page.goto(`${MM_URL}/${team}/channels/town-square`, {waitUntil: 'domcontentloaded'});
  const browserLink = page.getByRole('link', {name: /View in Browser/i});
  if (await browserLink.isVisible({timeout: 2000}).catch(() => false)) {
    await browserLink.click();
  }
  await page.waitForURL(/\/channels\//, {timeout: 30000});
  await page.getByRole('textbox', {name: /Write to/i}).waitFor({timeout: 30000}).catch(() => {});
  await page.locator('a.close').first().click().catch(() => {});
  await page.waitForTimeout(400);
}

export async function shotElement(page, locator, filePath, pad = 8) {
  await locator.waitFor({state: 'visible', timeout: 15000});
  await locator.scrollIntoViewIfNeeded().catch(() => {});
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

/** Prefer element.screenshot when the node itself is the crop (no sibling bleed). */
export async function shotNode(locator, filePath) {
  await locator.waitFor({state: 'visible', timeout: 15000});
  await locator.scrollIntoViewIfNeeded().catch(() => {});
  await locator.screenshot({path: filePath, animations: 'disabled'});
}

export async function shotDialog(page, filePath) {
  await shotElement(page, page.getByRole('dialog').last(), filePath, 12);
}

export async function shotMenusUnion(page, filePath, anchorItemName) {
  const item = page.getByRole('menuitem', {name: anchorItemName});
  await item.waitFor({state: 'visible', timeout: 10000});
  const submenu = item.locator('xpath=ancestor::*[@role="menu"][1]');
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
    await shotNode(submenu, filePath);
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

export async function openMoreActions(page) {
  await page.keyboard.press('Escape');
  await page.waitForTimeout(250);
  await page.getByRole('button', {name: /channel menu/i}).click({timeout: 15000});
  const more = page.getByRole('menuitem', {name: 'More actions'});
  await more.waitFor({state: 'visible', timeout: 8000});
  await more.scrollIntoViewIfNeeded();
  await more.hover();
  await page.waitForTimeout(200);
  // Ensure submenu is open
  if (!(await page.getByRole('menuitem', {name: /Request /}).first().isVisible().catch(() => false))) {
    await more.click({force: true});
  }
}

export async function openHub(page) {
  const plugBtn = page.locator('[role="button"]').filter({has: page.locator('.fa-plug')}).first();
  await plugBtn.click({timeout: 15000});
  const requestsItem = page.getByText(/^Requests$/).first();
  if (await requestsItem.isVisible({timeout: 1500}).catch(() => false)) {
    await requestsItem.click();
  }
  await page.getByRole('heading', {name: /Mattermost Request Hub/i}).waitFor({timeout: 10000});
  return plugBtn;
}

export async function shotApprovalCard(page, titleRegex, filePath) {
  const title = page.locator('h1.attachment__title').filter({hasText: titleRegex}).first();
  await title.waitFor({state: 'visible', timeout: 8000});
  await title.scrollIntoViewIfNeeded();
  const card = title.locator('xpath=ancestor::*[contains(concat(" ", normalize-space(@class), " "), " attachment ")][1]');
  const target = (await card.count()) ? card.first() : title;
  await target.screenshot({path: filePath, animations: 'disabled'});
}

export async function shotRhsThread(page, filePath) {
  const candidates = [
    page.locator('#rhsContainer'),
    page.locator('.sidebar--right'),
    page.locator('[class*="RhsContainer"]'),
    page.locator('[class*="rhs-container"]'),
    page.locator('[data-testid="rhsContainer"]'),
    page.locator('.RHSContent'),
    page.getByRole('complementary').last(),
  ];
  for (const rhs of candidates) {
    if (await rhs.first().isVisible({timeout: 1500}).catch(() => false)) {
      await shotNode(rhs.first(), filePath);
      return;
    }
  }
  // Fallback: crop from Thread heading to bottom of viewport
  const heading = page.getByText(/^Thread$/).first();
  if (await heading.isVisible({timeout: 3000}).catch(() => false)) {
    const box = await heading.boundingBox();
    if (box) {
      const vw = page.viewportSize()?.width ?? 1440;
      const vh = page.viewportSize()?.height ?? 900;
      await page.screenshot({
        path: filePath,
        clip: {
          x: Math.max(0, box.x - 16),
          y: Math.max(0, box.y - 12),
          width: Math.min(vw - Math.max(0, box.x - 16), Math.max(box.width + 32, vw - box.x)),
          height: Math.min(vh - Math.max(0, box.y - 12), vh - box.y + 12),
        },
        animations: 'disabled',
      });
      return;
    }
  }
  throw new Error('RHS thread panel not found');
}

export function outPath(imagesRoot, folder, name) {
  const dir = path.join(imagesRoot, folder);
  ensureDir(dir);
  return path.join(dir, name);
}
