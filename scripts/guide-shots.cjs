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
const SHEET = path.join(__dirname, 'guide-sheet.xlsx');
const SLUG = 'badminton-sampai-pingsan';
const T = `${B}/t/${SLUG}`;
const NAMES = ['Jacque Oui', 'Prabski', 'Bahlul', 'Mas Gembul', 'Tante Smash', 'Om Netting',
  'Si Lob', 'Dewi Dropshot', 'Koh Backhand', 'Mbak Servis', 'Pak Rally', 'Bang Defense'];

// Deterministic "random" scores, so reruns look alike.
let seed = 7;
const rnd = (lo, hi) => { seed = (seed * 9301 + 49297) % 233280; return lo + Math.floor(seed / 233280 * (hi - lo + 1)); };

(async () => {
  // A browser per language, so native date fields read like it (01/11/2026
  // in Indonesian); Chromium takes that format from --lang, not the context.
  let browser;
  const newPage = async (locale) => {
    if (browser) await browser.close();
    browser = await chromium.launch({ args: ['--lang=' + locale],
      env: { ...process.env, LANG: locale.replace('-', '_') + '.UTF-8', LANGUAGE: locale } });
    const ctx = await browser.newContext({ viewport: { width: 390, height: 844 }, deviceScaleFactor: 2, locale });
    const p = await ctx.newPage();
    p.on('dialog', d => d.accept());
    return p;
  };
  let page = await newPage('id-ID');
  const go = async (url) => { await page.goto(url); await page.waitForLoadState('networkidle'); };
  const post = async (url, form, multipart) => {
    const res = await page.request.post(url, multipart ? { multipart: form, maxRedirects: 5 } : { form, maxRedirects: 5 });
    if (res.status() >= 400) throw new Error(`POST ${url}: ${res.status()}`);
    return res;
  };
  const submit = (sel) => Promise.all([page.waitForNavigation(), page.click(sel)]);

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

  const startDay = async (date) => {
    await go(T + '/play');
    await page.fill('#playDate', date);
    await submit('form.startday button[type=submit]');
    return page.url().split('?')[0];
  };
  // A play day recorded per player on the Poin tab, like the club's sheet.
  const pointsDay = async (date, plays) => {
    const url = await startDay(date);
    await go(url + '?tab=poin');
    for (const [name, pts] of plays) {
      await page.fill('#poin input[name=player]', name);
      await page.fill('#poin input[name=points]', String(pts));
      await submit('#poin .sf-add');
    }
    return url;
  };
  // A play day with shuffled matches on the Pertandingan tab.
  const matchDay = async (date, absent, scoredRounds, leaveOnCourt) => {
    const url = await startDay(date);
    await go(url + '?tab=match');
    await page.locator('details:has(form[action$="/setup"])').evaluate(d => d.open = true);
    await page.selectOption('form[action$="/setup"] select[name=courts]', '2');
    await page.click('form[action$="/setup"] [data-check-all=present]');
    for (const name of absent) await page.uncheck(`form[action$="/setup"] label.att:has-text("${name}") input`);
    await submit('form[action$="/setup"] button[type=submit]');
    for (let i = 0; i < scoredRounds; i++) {
      await post(url + '/shuffle', {});
      await go(url + '?tab=match');
      for (let c = 0; c < 2; c++) {
        const court = page.locator('#court article.court').first();
        const win = rnd(0, 1), lose = rnd(24, 40);
        await court.locator('input[name=s0_0]').fill(String(win ? 42 : lose));
        await court.locator('input[name=s1_0]').fill(String(win ? lose : 42));
        await Promise.all([page.waitForNavigation(), court.locator('button.primary').click()]);
      }
    }
    if (leaveOnCourt) await post(url + '/shuffle', {});
    return url;
  };
  const sheetPlays = (who, n) => who.flatMap(name => Array.from({ length: n }, () => [name, rnd(0, 2) ? 42 : rnd(22, 41)]));
  await matchDay('2026-10-04', ['Bang Defense'], 3, false);
  const day2 = await pointsDay('2026-10-11', sheetPlays(NAMES.slice(0, 8), 3));
  await page.locator('#awards details').evaluate(d => d.open = true);
  await page.fill('.extraform input[name=player]', 'Tante Smash');
  await page.fill('.extraform input[name=points]', '10');
  await page.fill('.extraform input[name=reason]', 'Juara turnamen antarklub');
  await submit('.extraform .sf-add');
  await pointsDay('2026-10-22', sheetPlays(NAMES.slice(2, 10), 2));
  const day4 = await matchDay('2026-10-25', ['Si Lob'], 3, true);

  // --- Capture, once per language ---
  const shot = async (lang, name, locator) => {
    await locator.first().scrollIntoViewIfNeeded();
    await locator.first().screenshot({ path: path.join(OUT, lang, name + '.jpg'), type: 'jpeg', quality: 72 });
  };
  for (const [lang, locale] of [['id', 'id-ID'], ['en', 'en-GB']]) {
    page = await newPage(locale);
    await post(B + '/login', { username: 'demo', password: 'demo-password' });
    await go(`${B}/lang/${lang}?next=/`);

    await page.fill('#tallyName', lang === 'id' ? 'Badminton Sampai Pingsan' : 'Sunday Smashers');
    await page.dispatchEvent('#tallyName', 'input');
    await shot(lang, 'create', page.locator('#new'));

    await go(T + '/players');
    await shot(lang, 'players', page.locator('main section.card').nth(0));
    await shot(lang, 'player-list', page.locator('main section.card').nth(1));

    await go(T + '/periods');
    await shot(lang, 'season', page.locator('main section.card').last());
    await submit('.periodrow a strong');
    await shot(lang, 'season-weeks', page.locator('main section.card').nth(1));

    await go(page.url() + '/import');
    await page.setInputFiles('input[name=sheet]', SHEET);
    await submit('form[enctype] button[type=submit]');
    // Preview, new players and notes together, without the confirm button.
    const clip = await page.evaluate(() => {
      const cards = [...document.querySelectorAll('main section.card')].slice(0, 3);
      const top = cards[0].getBoundingClientRect().top + window.scrollY;
      const last = cards[cards.length - 1].getBoundingClientRect();
      const left = cards[0].getBoundingClientRect().left;
      return { x: left, y: top, width: cards[0].getBoundingClientRect().width, height: last.bottom + window.scrollY - top };
    });
    await page.screenshot({ path: path.join(OUT, lang, 'import.jpg'), type: 'jpeg', quality: 72, clip, fullPage: true });

    await go(T + '/play');
    await page.fill('#playDate', '2026-11-01');
    await page.locator('main h2').first().click(); // blur the date field
    await shot(lang, 'start-day', page.locator('#mulai'));

    await go(day2 + '?tab=poin');
    await shot(lang, 'day-tabs', page.locator('.daytabs'));
    await page.fill('#poin input[name=player]', 'Prabski');
    await page.fill('#poin input[name=points]', '38');
    await page.locator('#poin h2').click();
    await shot(lang, 'points', page.locator('#poin'));
    await shot(lang, 'today', page.locator('#today'));
    await page.locator('#awards details').evaluate(d => d.open = true);
    await shot(lang, 'awards', page.locator('#awards'));

    await go(day4 + '?tab=match');
    await shot(lang, 'courts', page.locator('#court'));
    await page.locator('#court details.mf-swap').first().evaluate(d => d.open = true);
    await shot(lang, 'swap', page.locator('#court article.court').first());
    await page.locator('#court details.mf-swap').first().evaluate(d => d.open = false);
    await shot(lang, 'done', page.locator('#done'));
    await go(day4 + '?tab=poin'); // the last tab used is remembered; leave it on Poin

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
