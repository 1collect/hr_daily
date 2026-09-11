const fs = require('fs');
const path = require('path');
const { chromium } = require('../web/node_modules/playwright-core');
const ts = require('../web/node_modules/typescript');
const root = path.resolve(__dirname, '..');
const output = path.join(root, 'screenshots', 'corrections');
const read = name => fs.readFileSync(path.join(root, name), 'utf8');
const app = ts.transpileModule(read('web/src/app.ts'), { compilerOptions: { target: ts.ScriptTarget.ES2020, module: ts.ModuleKind.ES2020 } }).outputText;
const user = { id: 'demo', username: 'a.sadykova', role: 'employee', firstName: 'Айгуль', lastName: 'Садыкова', middleName: '', plan: 16, invitationPlan: 40 };
const people = { invited_candidates: ['Иванов Иван Иванович', 'Петров Пётр Сергеевич'], interviewed_candidates: ['Иванов Иван Иванович'], interns: [], reserve_candidates: [], dismissed_workers: [] };
const row = { id: 'demo-row', officeId: 'demo-office', officeName: 'Алматы', sortOrder: 0, openVacancies: 3, plannedReserve: 0, invitedCandidates: 2, interviewedCandidates: 1, interns: 0, reserveCandidates: 0, dismissedWorkers: 0, hiredWorkers: [], people, assigned: true, responsibleCount: 1, invitationEfficiency: 5, hiringEfficiency: 0, invitationPlan: 40, hiringPlan: 16 };
let requests = [];
(async () => {
  fs.mkdirSync(output, { recursive: true });
  const browser = await chromium.launch({ channel: 'msedge', headless: true });
  try {
    const page = await browser.newPage({ viewport: { width: 1600, height: 1000 }, deviceScaleFactor: 1 });
    const errors = [];
    page.on('pageerror', e => errors.push(e.message));
    await page.route('**/*', async route => {
      const url = new URL(route.request().url());
      const pathname = url.pathname;
      let body, contentType = 'application/json';
      if (pathname === '/' || pathname === '/correction-requests') { body = read('web/src/index.html'); contentType = 'text/html'; }
      else if (pathname === '/ui.css') { body = read('web/src/ui.css'); contentType = 'text/css'; }
      else if (pathname === '/app.js') { body = app; contentType = 'text/javascript'; }
      else if (pathname === '/api/auth/me') body = JSON.stringify(user);
      else if ((pathname === '/api/bootstrap' || pathname === '/api/main-office/bootstrap')) body = JSON.stringify({ report: { id: 'demo-report', date: url.searchParams.get('date'), editable: false, status: 'draft' }, rows: [row], totals: {}, plan: 16, invitationPlan: 40, hiringPlan: 16 });
      else if (pathname === '/api/correction-requests' && route.request().method() === 'POST') {
        const input = route.request().postDataJSON();
        if (input.rows[0].values.people.invited_candidates.length !== 1) throw Error('Expected one remaining candidate');
        requests = [{ id: 'demo-request', reportDate: input.reportDate, reportType: input.reportType, userId: user.id, employeeName: 'Садыкова Айгуль', username: user.username, note: input.note, changes: [{ rowId: row.id, unitName: row.officeName, before: { ...row, responsibleIds: [] }, after: input.rows[0].values }], status: 'pending', reviewNote: '', reviewerName: '', createdAt: new Date().toISOString() }];
        body = JSON.stringify({ id: 'demo-request', status: 'pending' });
      } else if (pathname === '/api/correction-requests') body = JSON.stringify(requests);
      else body = '[]';
      await route.fulfill({ body, contentType });
    });
    const shot = async name => {
      await page.mouse.move(1590, 990);
      await page.screenshot({ path: path.join(output, name + '.png'), fullPage: true, animations: 'disabled' });
    };
    await page.goto('http://preview.local/?date=2026-09-10');
    await page.locator('.people-list[data-people-key="invitedCandidates"]').click();
    await page.locator('#request-cell-correction').waitFor();
    await shot('01-cell-list');
    await page.locator('#request-cell-correction').click();
    await page.locator('#cell-correction-note').fill('Удалить ошибочно добавленного кандидата.');
    await page.locator('.remove-hire').nth(1).click();
    if (await page.locator('#cell-correction-note').inputValue() !== 'Удалить ошибочно добавленного кандидата.') throw Error('Note lost on deletion');
    await shot('02-cell-edit');
    await page.locator('#add-hire').click();
    await page.locator('.hire-input').last().fill('Сидоров Сергей Сергеевич');
    await shot('03-cell-add');
    await page.locator('.remove-hire').last().click();
    await page.locator('.modal .save').click();
    await page.locator('.modal').waitFor({ state: 'hidden' });
    if (await page.locator('.people-list[data-people-key="invitedCandidates"]').textContent() !== '2') throw Error('Report changed before approval');
    if (requests.length !== 1 || requests[0].changes[0].after.people.interviewed_candidates.length !== 1) throw Error('Unrelated cell changed');
    await page.goto('http://preview.local/correction-requests');
    await page.locator('.employee-request').waitFor();
    await shot('04-request-sent');
    await page.goto('http://preview.local/?date=2026-09-10');
    await page.locator('.hires').click();
    await page.locator('#request-cell-correction').click();
    await page.locator('#add-hire').click();
    await page.locator('.hire-input').fill('Тестов Тест Тестович');
    await page.locator('.hire-position').fill('Специалист');
    await page.locator('#cell-correction-note').fill('Добавить принятого сотрудника.');
    await page.setViewportSize({ width: 760, height: 900 });
    await shot('06-hired-cell-edit');
    await page.locator('.remove-hire').click();
    if (await page.locator('.hire-input').count()) throw Error('Cannot clear list');
    await page.locator('.cancel').click();
    if (await page.locator('.hires').textContent() !== '0') throw Error('Cancel mutated report');
    await page.setViewportSize({ width: 1600, height: 1000 });
    await page.goto('http://preview.local/correction-requests');
    user.role = 'admin';
    user.firstName = 'Администратор';
    user.lastName = '';
    await page.reload();
    await page.locator('.inspect-correction').click();
    await page.locator('.correction-review-modal').waitFor();
    await shot('05-admin-review');
    if (errors.length) throw Error(errors.join('\n'));
    console.log('Cell correction checks passed; screenshots saved to ' + output);
  } finally { await browser.close(); }
})().catch(error => { console.error(error); process.exit(1); });
