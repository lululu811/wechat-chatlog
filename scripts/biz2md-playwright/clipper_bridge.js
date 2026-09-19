#!/usr/bin/env node
/*
 * Clipper Bridge — drives the user's real Chrome via Playwright/CDP
 * =================================================================
 *
 * Why this exists
 * ---------------
 * The "real" Obsidian Web Clipper is a Chrome MV3 extension that runs
 * inside a normal Chrome tab. It needs:
 *
 *   1. the user's mp.weixin.qq.com login cookie (their Chrome profile)
 *   2. an unblocked browser session (not headless; mp.weixin.qq.com
 *      actively rejects headless / non-Chrome UAs)
 *   3. the user's vault path (configured inside the extension popup)
 *
 * We can't replicate any of that from a fresh Playwright Chromium
 * binary. So instead we **connect to the user's already-running Chrome**
 * over the Chrome DevTools Protocol (CDP) and drive it.
 *
 * Prerequisites
 * -------------
 *   - Chrome already running with `--remote-debugging-port=9222`
 *   - Obsidian Web Clipper installed in that Chrome
 *   - The vault path is configured in the Clipper popup
 *
 * What this script does
 * ---------------------
 * For each candidate URL from chatlog:
 *   1. open it in a new tab in the user's Chrome
 *   2. wait for the article DOM (#js_content / etc.)
 *   3. let the Clipper extension auto-fire on its trigger
 *   4. watch the vault for a new .md file
 *   5. append the URL to chatlog's biz_archived table
 *
 * If the extension doesn't fire, we fall back to scraping the DOM
 * directly with page.evaluate() so the test pipeline still completes.
 */

const { chromium } = require('playwright-core');
const fs = require('fs');
const path = require('path');
const zlib = require('zlib');

// ─── Config (env-overridable) ──────────────────────────────────────
const CDP_URL    = process.env.CDP_URL    || 'http://127.0.0.1:9222';
const VAULT_PATH = process.env.OBSIDIAN_VAULT || path.join(process.env.HOME, 'Documents/MyVault');
const GH_ID      = process.argv[2] || 'gh_423b608e0744';
const LIMIT      = parseInt(process.argv[3] || '3', 10);
const CHATLOG_DB = '/tmp/chatlog-decrypted/db_storage/message/biz_message_0.db';
const CONTACT_DB = '/tmp/chatlog-decrypted/db_storage/contact/contact.db';
const ARCHIVE_DB = '/tmp/chatlog-decrypted/biz_archived.db';

// ─── Crypto + sqlite via Node's built-ins ──────────────────────────
// Node 22 has experimental sqlite, but to keep deps zero we shell out
// to the sqlite3 CLI for db ops.
const { execSync } = require('child_process');
const crypto = require('crypto');

function sqlite3Read(sql) {
  return execSync(`sqlite3 -separator '|' "${CHATLOG_DB.replace(/"/g, '\\"')}" "${sql.replace(/"/g, '\\"')}"`, { encoding: 'utf8' });
}

function fetchCandidates(ghId, limit) {
  const md5 = crypto.createHash('md5').update(ghId).digest('hex');
  const table = `Msg_${md5}`;
  // Check if table exists first
  let exists = '';
  try {
    exists = sqlite3Read(`SELECT 1 FROM sqlite_master WHERE type='table' AND name='${table}'`).trim();
  } catch (e) {
    return [];
  }
  if (!exists) return [];

  // Read raw rows; chatlog's message_content is zstd-compressed (WCDB CT=1).
  // We shell out to a small decompressor via Python (which has zstandard).
  const out = execSync(
    `python3 -c "
import sqlite3, zstandard as zstd, json, re, hashlib
md5 = hashlib.md5(b'${ghId}').hexdigest()
table = 'Msg_' + md5
con = sqlite3.connect('${CHATLOG_DB}')
cur = con.cursor()
try:
    rows = cur.execute(f'SELECT message_content, WCDB_CT_message_content FROM \\"{table}\\" ORDER BY sort_seq DESC LIMIT ${limit}').fetchall()
except sqlite3.OperationalError:
    print(json.dumps([])); raise SystemExit(0)
dctx = zstd.ZstdDecompressor()
items = []
for content, ct in rows:
    if ct and ct != 0:
        try: content = dctx.decompress(content)
        except: pass
    s = content.decode('utf-8', errors='ignore') if content else ''
    url_m = re.search(r'<url><!\\[CDATA\\[(.*?)\\]\\]></url>|<url>(.*?)</url>', s)
    url = (url_m.group(1) or url_m.group(2)) if url_m else ''
    if url:
        items.append(url)
print(json.dumps(items))
"`,
    { encoding: 'utf8' }
  ).trim();
  return JSON.parse(out);
}

function markArchived(url, title, source) {
  try {
    execSync(
      `sqlite3 "${ARCHIVE_DB}" "INSERT OR IGNORE INTO biz_archived (gh_id, url, title, archived_at, source) VALUES ('${GH_ID}', '${url.replace(/'/g, "''")}', '${title.replace(/'/g, "''")}', ${Math.floor(Date.now()/1000)}, '${source}');"`,
      { stdio: 'pipe' }
    );
  } catch (e) {
    console.error('  ! markArchived failed:', e.message);
  }
}

function lookupAccount(ghId) {
  try {
    const nick = execSync(
      `sqlite3 -separator '|' "${CONTACT_DB}" "SELECT nick_name FROM contact WHERE username='${ghId}'"`,
      { encoding: 'utf8' }
    ).trim();
    return nick || ghId;
  } catch (e) {
    return ghId;
  }
}

// ─── Vault file watching ──────────────────────────────────────────
function snapshotVault() {
  const dir = path.join(VAULT_PATH, '公众号');
  if (!fs.existsSync(dir)) return new Set();
  const out = new Set();
  walk(dir, out);
  return out;
}

function walk(dir, set) {
  for (const e of fs.readdirSync(dir, { withFileTypes: true })) {
    const full = path.join(dir, e.name);
    if (e.isDirectory()) walk(full, set);
    else if (e.name.endsWith('.md')) set.add(e.name);
  }
}

function waitForNewMd(baseline, timeoutSec = 25) {
  return new Promise((resolve) => {
    const start = Date.now();
    const tick = () => {
      const current = snapshotVault();
      const diff = [...current].filter(n => !baseline.has(n));
      if (diff.length > 0) return resolve(diff);
      if ((Date.now() - start) / 1000 > timeoutSec) return resolve([]);
      setTimeout(tick, 500);
    };
    tick();
  });
}

// ─── Fallback: DOM scrape ────────────────────────────────────────
async function fallbackScrape(page, account, url) {
  const data = await page.evaluate(() => {
    const titleEl  = document.querySelector('#activity-name');
    const publishEl = document.querySelector('#publish_time');
    const authorEl = document.querySelector('#js_name');
    const bodyEl = document.querySelector('#js_content');
    const title   = titleEl   ? titleEl.innerText.trim() : '';
    const publish = publishEl ? publishEl.innerText.trim() : '';
    const author  = authorEl  ? authorEl.innerText.trim() : '';
    const bodyText = bodyEl  ? bodyEl.innerText.trim() : '';
    return { title, publish, author, bodyText };
  });
  const date = (data.publish || new Date().toISOString()).slice(0, 10);
  const safe = data.title.replace(/[\\\/:*?"<>|]/g, '_').slice(0, 120).trim();
  const fname = `${account}-${date}-${safe}.md`;
  const rel = `公众号/${fname}`;
  const full = path.join(VAULT_PATH, rel);
  fs.mkdirSync(path.dirname(full), { recursive: true });
  const body =
`---
url: "${url}"
author: "${account}"
publishedAt: "${data.publish}"
source: chatlog-biz2md-playwright-fallback
tags:
  - 公众号
  - chatlog
---

# ${data.title}

${data.bodyText}

*Captured via Playwright/CDP bridge (Clipper fallback path)*
`;
  fs.writeFileSync(full, body, 'utf8');
  return [fname];
}

// ─── Main ─────────────────────────────────────────────────────────
(async () => {
  console.log(`vault: ${VAULT_PATH}`);
  console.log(`cdp  : ${CDP_URL}`);
  console.log(`gh   : ${GH_ID} (limit ${LIMIT})\n`);

  const candidates = fetchCandidates(GH_ID, LIMIT);
  if (candidates.length === 0) {
    console.log('no candidates found (table Msg_<md5> missing in chatlog?)');
    process.exit(1);
  }
  console.log(`candidates: ${candidates.length} url(s)\n`);

  const account = lookupAccount(GH_ID);
  console.log(`account  : ${account}\n`);

  const browser = await chromium.connectOverCDP(CDP_URL);
  const context = browser.contexts()[0] || await browser.newContext();
  const baseline = snapshotVault();
  console.log(`baseline .md count: ${baseline.size}\n`);

  for (const url of candidates) {
    console.log(`\n→ ${url.slice(0, 90)}${url.length > 90 ? '…' : ''}`);
    const page = await context.newPage();
    try {
      await page.goto(url, { waitUntil: 'domcontentloaded', timeout: 25000 });
      // Wait for the article body to render (wechat is a SPA).
      await page.waitForSelector('#js_content', { timeout: 12000 }).catch(() => {});
      // Give the Clipper extension a few seconds to fire (it listens
      // for matching URLs and saves in the background).
      await page.waitForTimeout(3500);
      const newFiles = await waitForNewMd(baseline, 25);
      if (newFiles.length > 0) {
        console.log(`  ✓ Clipper wrote ${newFiles.length} file(s): ${newFiles.join(', ')}`);
        for (const f of newFiles) markArchived(url, f, 'clipper-via-cdp');
        for (const f of newFiles) baseline.add(f);
      } else {
        console.log(`  ⚠ Clipper didn't fire (extension disabled / not installed / different trigger). Falling back to DOM scrape.`);
        const written = await fallbackScrape(page, account, url);
        for (const f of written) markArchived(url, f, 'playwright-fallback');
        for (const f of written) baseline.add(f);
      }
    } finally {
      await page.close();
    }
  }

  await browser.close();
  console.log('\n>>> done. Verify with:');
  console.log(`>>>   ./bin/chatlog biz2md --status --vault "${VAULT_PATH}"`);
})().catch(e => {
  console.error('FATAL:', e.message);
  process.exit(1);
});