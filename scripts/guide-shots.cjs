// Generates the user guide's screenshots (static/guide/{id,en}/*.jpg).
//
// Run it against a tally with an EMPTY database and AUTH_PROVIDER=local: it
// signs up a demo admin and seeds a fictional club. See README "User guide".
//
//   node scripts/guide-shots.cjs <path to playwright-core> [base URL]
const { chromium } = require(process.argv[2]);
const path = require('path');

const B = process.argv[3] || 'http://localhost:8095';
const OUT = path.join(__dirname, '..', 'static', 'guide');
const SLUG = 'badminton-sampai-pingsan';
const T = `${B}/t/${SLUG}`;
const NAMES = ['Jacque Oui', 'Prabski', 'Bahlul', 'Mas Gembul', 'Tante Smash', 'Om Netting',
  'Si Lob', 'Dewi Dropshot', 'Koh Backhand', 'Mbak Servis', 'Pak Rally', 'Bang Defense'];

// Deterministic "random" scores, so reruns look alike.
let seed = 7;
const rnd = (lo, hi) => { seed = (seed * 9301 + 49297) % 233280; return lo + Math.floor(seed / 233280 * (hi - lo + 1)); };

(async () => {
  const browser = await chromium.launch();
  const ctx = await browser.newContext({ viewport: { width: 390, height: 844 }, deviceScaleFactor: 2 });
  const page = await ctx.newPage();
  page.on('dialog', d => d.accept());
  const go = async (url) => { await page.goto(url); await page.waitForLoadState('networkidle'); };
  const post = async (url, form, multipart) => {
    const res = await page.request.post(url, multipart ? { multipart: form, maxRedirects: 5 } : { form, maxRedirects: 5 });
    if (res.status() >= 400) throw new Error(`POST ${url}: ${res.status()}`);
    return res;
  };

  // --- Seed ---
  await go(B + '/lang/id?next=/');
  await post(B + '/register', { username: 'demo', password: 'demo-password', password2: 'demo-password' });
  await post(B + '/tallies', { name: 'Badminton Sampai Pingsan', slug: SLUG, template: 'badminton_doubles',
    description: 'Main bareng tiap Minggu pagi.' });
  await post(T + '/players', { names: NAMES.join('\n') });
  await post(T + '/periods', { name: 'Musim #1', starts_on: '2026-10-01', ends_on: '2026-12-31',
    format: 'continuous', target: '42', game_target: '21', win_bonus: '0' });
  await post(T + '/settings', { name: 'Badminton Sampai Pingsan', description: 'Main bareng tiap Minggu pagi.', visibility: 'link' });
  await post(T + '/branding', { title1: '', title2: '', badge: 'Badminton Sampai Pingsan', footer: 'Sehat bareng, seru bareng' }, true);

  const playDay = async (date, absent, scoredRounds, leaveOnCourt) => {
    await go(T + '/play?new=1');
    await page.fill('input[name=played_on]', date);
    await page.selectOption('select[name=courts]', '2');
    await page.click('[data-check-all=present]');
    for (const name of absent) await page.uncheck(`label.att:has-text("${name}") input`);
    await Promise.all([page.waitForNavigation(), page.click('form[action$="/rounds"] button[type=submit]')]);
    const roundURL = page.url();
    for (let i = 0; i < scoredRounds; i++) {
      await post(roundURL + '/shuffle', {});
      await go(roundURL);
      for (let c = 0; c < 2; c++) {
        const court = page.locator('#court article.court').first();
        const win = rnd(0, 1), lose = rnd(24, 40);
        await court.locator('input[name=s0_0]').fill(String(win ? 42 : lose));
        await court.locator('input[name=s1_0]').fill(String(win ? lose : 42));
        await Promise.all([page.waitForNavigation(), court.locator('button.primary').click()]);
      }
    }
    if (leaveOnCourt) await post(roundURL + '/shuffle', {});
    return roundURL;
  };
  await playDay('2026-10-04', ['Bang Defense'], 3, false);
  const day2 = await playDay('2026-10-11', ['Si Lob'], 3, true);

  // --- Capture, once per language ---
  const shot = async (lang, name, locator) => {
    await locator.first().scrollIntoViewIfNeeded();
    await locator.first().screenshot({ path: path.join(OUT, lang, name + '.jpg'), type: 'jpeg', quality: 72 });
  };
  for (const lang of ['id', 'en']) {
    await go(`${B}/lang/${lang}?next=/`);

    await page.fill('#tallyName', lang === 'id' ? 'Badminton Sampai Pingsan' : 'Sunday Smashers');
    await page.dispatchEvent('#tallyName', 'input');
    await shot(lang, 'create', page.locator('#new'));

    await go(T + '/players');
    await shot(lang, 'players', page.locator('main section.card').nth(0));
    await shot(lang, 'player-list', page.locator('main section.card').nth(1));

    await go(T + '/periods');
    await shot(lang, 'season', page.locator('main section.card').last());

    await go(T + '/play?new=1');
    await page.fill('input[name=played_on]', '2026-10-18');
    await page.click('[data-check-all=present]');
    await page.uncheck('label.att:has-text("Si Lob") input');
    await page.locator('main h2').first().click(); // blur the date field
    await shot(lang, 'start-day', page.locator('main section.card').first());

    await go(day2);
    await shot(lang, 'courts', page.locator('#court'));
    await page.locator('#court details.mf-swap').first().evaluate(d => d.open = true);
    await shot(lang, 'swap', page.locator('#court article.court').first());
    await page.locator('#court details.mf-swap').first().evaluate(d => d.open = false);
    await shot(lang, 'done', page.locator('#done'));
    await page.locator('#awards details').evaluate(d => d.open = true);
    await shot(lang, 'awards', page.locator('#awards'));

    await go(T);
    await shot(lang, 'board', page.locator('.boardcard'));
    await shot(lang, 'share', page.locator('main section.card').last());

    await go(T + '/poster');
    await page.evaluate(() => document.fonts.ready);
    await page.waitForTimeout(500);
    await shot(lang, 'poster', page.locator('#posterFrame'));

    await go(T + '/settings');
    await shot(lang, 'branding', page.locator('#poster'));
    await shot(lang, 'visibility', page.locator('main section.card').first());

    await go(T);
    await Promise.all([page.waitForNavigation(), page.click('.board td.pname a >> nth=0')]);
    await page.waitForLoadState('networkidle');
    await page.screenshot({ path: path.join(OUT, lang, 'player.jpg'), type: 'jpeg', quality: 72 });
  }
  await browser.close();
  console.log('screenshots written to', OUT);
})().catch(e => { console.error(e); process.exit(1); });
