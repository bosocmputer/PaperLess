// Browser QA for PaperLess — runs headless Chromium against the app and
// screenshots key screens. Read-only: it draws a signature but never submits,
// so it does not mutate data. Run inside a node+playwright container on the
// deploy_default network. Screenshots → /qa/shots, console errors → /qa/shots/console.log
import { chromium } from "playwright";
import { appendFileSync } from "node:fs";

const BASE = process.env.BASE || "http://paperless-web:3000";
const OUT = "/qa/shots";
const MOBILE = { width: 390, height: 844 };

function logErr(tag, msg) {
  appendFileSync(`${OUT}/console.log`, `[${tag}] ${msg}\n`);
}

async function newPage(browser, label) {
  const ctx = await browser.newContext({ viewport: MOBILE });
  const page = await ctx.newPage();
  page.on("console", (m) => { if (m.type() === "error") logErr(`${label}:console`, m.text()); });
  page.on("pageerror", (e) => logErr(`${label}:pageerror`, e.message));
  return page;
}

async function login(page, username) {
  await page.goto(`${BASE}/login`, { waitUntil: "networkidle" });
  await page.fill('input[autocomplete="username"]', username);
  await page.fill('input[autocomplete="current-password"]', "password123");
  await page.click('button[type="submit"]');
}

async function shot(page, name) {
  try { await page.screenshot({ path: `${OUT}/${name}.png`, fullPage: true }); }
  catch (e) { logErr("shot", `${name}: ${e.message}`); }
}

const run = async () => {
  const browser = await chromium.launch();

  // ── Admin ──
  const admin = await newPage(browser, "admin");
  await login(admin, "admin");
  await admin.waitForURL("**/admin/**", { timeout: 20000 }).catch(() => {});
  await admin.waitForTimeout(1500);
  await shot(admin, "01-admin-landing");
  for (const [path, name] of [
    ["/admin/documents", "02-documents"],
    ["/admin/workflows", "03-workflows"],
    ["/admin/users", "04-users"],
    ["/admin/workflows/1096", "05-workflow-editor"], // DEMO3 v2 draft (editable)
  ]) {
    await admin.goto(`${BASE}${path}`, { waitUntil: "networkidle" }).catch((e) => logErr("nav", `${path}: ${e.message}`));
    await admin.waitForTimeout(1200);
    await shot(admin, name);
  }
  // open first document detail (attachments + dashboard wiring)
  await admin.goto(`${BASE}/admin/documents`, { waitUntil: "networkidle" });
  await admin.waitForTimeout(1000);
  const firstDoc = admin.locator("a[href^='/admin/documents/'], button").filter({ hasText: /PO|INV|—/ }).first();
  // fall back: click first list card button
  const card = admin.locator("div >> button, a").first();
  await admin.goto(`${BASE}/admin/documents/457`, { waitUntil: "networkidle" }).catch(() => {});
  await admin.waitForTimeout(1200);
  await shot(admin, "06-document-detail");

  // ── Signer (checkerA has an open CHECKER task on the demo doc) ──
  const maker = await newPage(browser, "signer");
  await login(maker, "checkerA");
  await maker.waitForURL("**/inbox**", { timeout: 20000 }).catch(() => {});
  await maker.waitForTimeout(1500);
  await shot(maker, "07-inbox");
  // open first inbox task
  const task = maker.locator("ul li button").first();
  if (await task.count()) {
    await task.click().catch((e) => logErr("task", e.message));
    await maker.waitForTimeout(2500);
    await shot(maker, "08-signing-page");
    // draw a signature on the canvas (pointer), then confirm to preview (no submit)
    const canvas = maker.locator("canvas").first();
    if (await canvas.count()) {
      const box = await canvas.boundingBox();
      if (box) {
        await maker.mouse.move(box.x + 30, box.y + 40);
        await maker.mouse.down();
        await maker.mouse.move(box.x + 90, box.y + 90, { steps: 8 });
        await maker.mouse.move(box.x + 150, box.y + 40, { steps: 8 });
        await maker.mouse.up();
        await maker.waitForTimeout(500);
        await shot(maker, "09-signature-drawn");
        const confirmBtn = maker.getByRole("button", { name: /ยืนยันลายเซ็น/ });
        if (await confirmBtn.count()) {
          await confirmBtn.click().catch((e) => logErr("confirm", e.message));
          await maker.waitForTimeout(800);
          await shot(maker, "10-signature-preview");
        }
      }
    } else {
      logErr("canvas", "no canvas on signing page");
    }
  } else {
    logErr("inbox", "no task in maker inbox");
  }

  await browser.close();
  logErr("done", "qa run complete");
};

run().catch((e) => { logErr("fatal", e.stack || e.message); process.exit(1); });
